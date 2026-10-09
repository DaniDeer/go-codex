package reqreply

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/DaniDeer/go-codex/internal/middleware"
)

// Middleware is a codec-backed, reqreply-specific middleware declaration —
// the per-pattern counterpart to [middleware.Declaration], adding
// reqreply's own topic-var AND property merge vocabularies. Unlike events
// (whose Subscribe/Publish roles are asymmetric — only one of In/Out is
// ever used per role), reqreply's SINGLE [Route] sees BOTH directions in
// one round-trip (mirrors [rest.Middleware] exactly) — so BOTH the In-side
// AND Out-side merge fields (topic AND property) are used together, on the
// SAME Middleware value.
//
// Middleware is the REUSABLE class ONLY (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)
// — attached via plain .Use(mw), always carrying a Req-FREE Fn bundled
// directly onto the value via [Middleware.WithReceive]/[Middleware.WithSend].
// Reusable verbatim across many routes, since the Fn never inspects the
// route's own request/response types.
//
// For a middleware whose Fn genuinely needs to read/write the route's
// own decoded req *Req/req Req, use the SEPARATE, distinct
// [BoundMiddleware]/[BoundClientMiddleware] types instead
// (bound_middleware.go), attached via [Route.HandleBoundMW]/
// [Route.ClientBoundMW] — Middleware[In,Out] itself has NO route-bound
// attachment path anymore; passing one to HandleMW/ClientMW now returns
// [MiddlewareMisattachedError].
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	topicMergeFieldsIn     []MergedTopicParam[In]
	topicMergeFieldsOut    []MergedTopicParam[Out]
	propertyMergeFieldsIn  []MergedPropertyParam[In]
	propertyMergeFieldsOut []MergedPropertyParam[Out]

	// propertySpecsIn/Out carry PRESENCE-ONLY (non-merged) property
	// declarations — pure spec+validation entries with NO corresponding
	// In/Out struct field to decode into (an MQTT5 User Property that
	// needs validating and rendering into the AsyncAPI spec, but has no
	// corresponding struct field). Replaces the former, now-deleted
	// adapters/mqtt5.FromUserPropertyParam, which built this same
	// declaration from the legacy middleware.Middleware type's
	// RequestHeaderParams/ResponseHeaderParams fields (removed along with
	// it). Part of the middleware-consolidation effort
	// (docs/design/d-0003-codec-declared-middlewares.md) closing the one real gap
	// the codec-backed family had relative to the legacy type.
	propertySpecsIn  []PropertyParam
	propertySpecsOut []PropertyParam

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// Req/Resp-FREE runtime Fn directly on the value itself — enabling
	// route-AGNOSTIC attachment via plain .Use(mw). Middleware[In,Out]
	// has no route-bound counterpart anymore (see this type's own doc
	// comment) — a Fn needing req *Req/req Req access uses
	// [BoundMiddleware]/[BoundClientMiddleware] instead.
	receiveFn func(ctx context.Context, in In) (Out, error)
	sendFn    func(ctx context.Context) (In, error)

	// ctxFieldsFromIn/ctxFieldsFromOut hold every
	// [Middleware.SetContextFieldFromIn]/[Middleware.SetContextFieldFromOut]
	// registration — dispatched automatically by buildDecodeIn (right
	// after decode+validate) and buildEncodeOut/buildEncodeIn (alongside
	// the merge-field encode), publishing into the SAME
	// [middleware.ContextField] box [middleware.EnsureContextFields]
	// pre-allocated (docs/design/d-0007-declarative-middleware-layering.md's
	// Rollout Phase C, mirroring rest's identical Phase A mechanism —
	// reqreply is fully symmetric with REST, unlike events' asymmetric
	// Subscribe-has-no-Out case, so this is a direct port of REST's exact
	// pattern).
	ctxFieldsFromIn  []contextFieldInSetter[In]
	ctxFieldsFromOut []contextFieldOutSetter[Out]
}

// contextFieldInSetter pairs a [middleware.ContextFieldSetter] with the
// getter that extracts its raw value from this middleware's decoded In —
// one entry per [Middleware.SetContextFieldFromIn] call.
type contextFieldInSetter[In any] struct {
	field middleware.ContextFieldSetter
	get   func(In) any
}

// contextFieldOutSetter is [contextFieldInSetter]'s Out-side mirror — one
// entry per [Middleware.SetContextFieldFromOut] call.
type contextFieldOutSetter[Out any] struct {
	field middleware.ContextFieldSetter
	get   func(Out) any
}

// NewMiddleware builds a [Middleware] from a [middleware.Declaration] —
// chain [Middleware.WithRequestTopic]/[Middleware.WithRequestProperty]/etc.
// to populate its merge-field vocabulary, or
// [Middleware.WithReceive]/[Middleware.WithSend] for the route-agnostic
// attachment style.
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out] {
	return Middleware[In, Out]{Declaration: decl}
}

// WithRequestTopic registers one REQUEST-side topic-var merge field into
// mw's own In vocabulary — reuses the EXISTING [NewTopicParam][In]
// constructor directly (no new reqreply-side param type). Only meaningful
// for a topic var the route's OWN template declares but that the route's
// OWN Req does NOT already merge via its own NewTopicParam.
func (m Middleware[In, Out]) WithRequestTopic(p MergedTopicParam[In]) Middleware[In, Out] {
	m.topicMergeFieldsIn = append(slices.Clone(m.topicMergeFieldsIn), p)
	return m
}

// WithResponseTopic is [Middleware.WithRequestTopic]'s REPLY-side sibling
// — registers one topic-var merge field into mw's own Out vocabulary,
// encoded into the reply's topic vars once [Route.HandleMW]'s fn (or a bundled
// WithReceive) produces an Out value, OR decoded from the reply's topic
// vars on the client side (mechanical, no Fn — mirrors
// [rest.ClientMiddlewareHandler.DecodeOut] exactly).
func (m Middleware[In, Out]) WithResponseTopic(p MergedTopicParam[Out]) Middleware[In, Out] {
	m.topicMergeFieldsOut = append(slices.Clone(m.topicMergeFieldsOut), p)
	return m
}

// WithRequestProperty is [Middleware.WithRequestTopic]'s property-axis
// sibling — registers one REQUEST-side property merge field into mw's own
// In vocabulary, using [NewPropertyParam]/[NewOptionalPropertyParam].
func (m Middleware[In, Out]) WithRequestProperty(p MergedPropertyParam[In]) Middleware[In, Out] {
	m.propertyMergeFieldsIn = append(slices.Clone(m.propertyMergeFieldsIn), p)
	return m
}

// WithResponseProperty is [Middleware.WithRequestProperty]'s REPLY-side
// sibling.
func (m Middleware[In, Out]) WithResponseProperty(p MergedPropertyParam[Out]) Middleware[In, Out] {
	m.propertyMergeFieldsOut = append(slices.Clone(m.propertyMergeFieldsOut), p)
	return m
}

// WithRequestPropertySpec registers a PRESENCE-ONLY (non-merged) REQUEST-
// side property declaration — p is validated and rendered into the
// route's AsyncAPI spec, but has NO corresponding In struct field to
// decode into. Use [Middleware.WithRequestProperty] instead when a merge
// field is wanted. Mirrors legacy middleware.Middleware's
// RequestHeaderParams shape (see adapters/mqtt5.FromUserPropertyParam).
func (m Middleware[In, Out]) WithRequestPropertySpec(p PropertyParam) Middleware[In, Out] {
	m.propertySpecsIn = append(slices.Clone(m.propertySpecsIn), p)
	return m
}

// WithResponsePropertySpec is [Middleware.WithRequestPropertySpec]'s
// REPLY-side sibling — mirrors legacy middleware.Middleware's
// ResponseHeaderParams shape (see
// adapters/mqtt5.FromResponseUserPropertyParam).
func (m Middleware[In, Out]) WithResponsePropertySpec(p PropertyParam) Middleware[In, Out] {
	m.propertySpecsOut = append(slices.Clone(m.propertySpecsOut), p)
	return m
}

// WithReceive attaches a route-AGNOSTIC runtime Fn directly to m — its
// signature never mentions Req/Resp, so the returned Middleware value (fn
// included) can be passed to .Use(...) verbatim, on as many different
// routes as needed. Use [BoundMiddleware] instead when fn genuinely needs
// req access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out] {
	m.receiveFn = fn
	return m
}

// WithSend is [Middleware.WithReceive]'s client-side sibling — fn produces
// an In value with no req access, attached via .Use(...). Use
// [BoundClientMiddleware] instead when fn genuinely needs req access.
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out] {
	m.sendFn = fn
	return m
}

// SetContextFieldFromIn registers field to be published (via
// [middleware.ContextFieldSetter.Set]) from get(in)'s return value —
// dispatched automatically right after this middleware's own In is
// decoded+validated (DecodeIn), on EVERY attachment style (bound via
// [Route.HandleBoundMW], or agnostic via [Middleware.WithReceive] + plain
// .Use()). The handler (or any LATER-dispatched middleware, regardless of
// shape) retrieves it fully-typed via [middleware.ContextField.Get].
//
// field takes [middleware.ContextFieldSetter], not a concrete
// [middleware.ContextField][V] directly — V is NOT a type parameter this
// method can introduce (Go forbids new type params on a method beyond the
// receiver's own); every ContextField[V] already satisfies
// ContextFieldSetter regardless of V, since Set's own signature never
// references V.
//
//	var TenantIDField = middleware.NewContextField(codex.String())
//	authMw = authMw.SetContextFieldFromIn(TenantIDField, func(in AuthIn) any { return in.TenantID })
func (m Middleware[In, Out]) SetContextFieldFromIn(field middleware.ContextFieldSetter, get func(In) any) Middleware[In, Out] {
	m.ctxFieldsFromIn = append(slices.Clone(m.ctxFieldsFromIn), contextFieldInSetter[In]{field: field, get: get})
	return m
}

// SetContextFieldFromOut is [Middleware.SetContextFieldFromIn]'s
// response-side sibling — field is published from get(out)'s return
// value, dispatched at whichever point this middleware's OWN Out becomes
// concretely available: right after Fn returns it (EncodeOut, the
// server/receiving role) or right after it is decoded from the reply's
// topic/property vars (DecodeOut, the client/sending role).
func (m Middleware[In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) Middleware[In, Out] {
	m.ctxFieldsFromOut = append(slices.Clone(m.ctxFieldsFromOut), contextFieldOutSetter[Out]{field: field, get: get})
	return m
}

// RouteMiddlewareMarker makes Middleware[In,Out] satisfy
// [middleware.RouteMiddleware] — EXPORTED (unlike [ports.Pattern]'s
// unexported-method sealing) because Go's unexported-method interface
// satisfaction is scoped per package: a type declared in api/reqreply can
// never satisfy an interface whose method is unexported in package
// middleware, no matter the name. So a route's plain .Use(...) can
// recognize and dispatch a route-agnostic Middleware value carrying a
// WithReceive/WithSend fn.
func (Middleware[In, Out]) RouteMiddlewareMarker() {}

// SecurityDeclaration makes Middleware[In,Out] satisfy
// [middleware.SecurityCarrier] — returns the embedded Declaration's own
// Security field directly. Part of the middleware-consolidation effort
// (docs/design/d-0003-codec-declared-middlewares.md) folding Security into the
// codec-backed family: [Route.HandleMW]/[Route.ClientMW] extract Security
// via this method UNIFORMLY, regardless of whether the attached value is
// this type or the legacy [middleware.Middleware].
func (m Middleware[In, Out]) SecurityDeclaration() *middleware.SecurityDeclaration {
	return m.Declaration.Security
}

// MiddlewareName makes Middleware[In,Out] satisfy a name-exposing
// interface used internally when synthesizing a legacy-shaped Security
// entry from a codec-backed value (see api/rest's routeMiddlewareOpt.applyRoute
// for the consuming side) — returns the embedded Declaration's own Name.
func (m Middleware[In, Out]) MiddlewareName() string { return m.Declaration.Name }

// applyAgnosticRoute implements [routeMiddlewareContributor] — called by
// [routeMiddlewareOpt.applyRoute] for a .Use()-attached Middleware value.
// In/Out are concrete here (m's own type parameters), so it can build the
// SAME spec contribution and (when bundled) the SAME runtime dispatch
// handler [BoundMiddleware.ApplyBoundRoute] produces for the route-bound
// case — feeding both into the SAME rb fields, so downstream consumers
// (applyParamDeclarations, adapters) treat both attachment styles
// uniformly.
func (m Middleware[In, Out]) applyAgnosticRoute(rb *routeBuilder) {
	rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, specContributionOf(m))
	if m.receiveFn != nil {
		rb.middlewareHandlers = append(rb.middlewareHandlers, buildAgnosticMiddlewareHandler(m))
	}
	if m.sendFn != nil {
		rb.clientMiddlewareHandlers = append(rb.clientMiddlewareHandlers, buildAgnosticClientMiddlewareHandler(m))
	}
}

// MiddlewareInputError is returned when a [Middleware]'s In value fails to
// decode/validate from the raw request topic/property vars.
//
// Use errors.As to extract the failing middleware's name:
//
//	var inputErr reqreply.MiddlewareInputError
//	if errors.As(err, &inputErr) {
//	    log.Printf("middleware %q: invalid input: %v", inputErr.Name, inputErr.Err)
//	}
type MiddlewareInputError struct {
	Name string
	Err  error
}

func (e MiddlewareInputError) Error() string {
	return fmt.Sprintf("api/reqreply: middleware %q: invalid input: %s", e.Name, e.Err.Error())
}

// Unwrap allows errors.As/errors.Is to traverse the underlying error.
func (e MiddlewareInputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareInputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// MiddlewareError is returned when a [Route.HandleMW]/[Route.ClientMW] fn's
// own returned business error does not match any [ErrorPattern] declared
// on the attaching route — the fallback for a codec-backed middleware's
// business errors, distinct from [SecurityError] (reserved for the
// [middleware.SecurityScheme] mechanism specifically).
//
// Use errors.As to extract the failing middleware's name:
//
//	var mwErr reqreply.MiddlewareError
//	if errors.As(err, &mwErr) {
//	    log.Printf("middleware %q failed: %v", mwErr.Name, mwErr.Err)
//	}
type MiddlewareError struct {
	Name string
	Err  error
}

func (e MiddlewareError) Error() string {
	return fmt.Sprintf("api/reqreply: middleware %q: %s", e.Name, e.Err.Error())
}

// Unwrap allows errors.As/errors.Is to traverse the underlying error.
func (e MiddlewareError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// MiddlewareOutputError is returned when a [Middleware]'s Out value fails to
// encode/decode — server-side, building the REPLY's Out (via
// [Middleware].EncodeOut); client-side, reading the REPLY's Out (via
// [ClientMiddlewareHandler].DecodeOut) — the OUTPUT-side counterpart of
// [MiddlewareInputError], added for symmetry (previously this failure
// propagated as a bare, unwrapped error with no way to recover which
// middleware failed; see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2). Mirrors
// [rest.MiddlewareOutputError]/[events.MiddlewareOutputError] exactly.
//
// Use errors.As to extract the failing middleware's name:
//
//	var outputErr reqreply.MiddlewareOutputError
//	if errors.As(err, &outputErr) {
//	    log.Printf("middleware %q: invalid output: %v", outputErr.Name, outputErr.Err)
//	}
type MiddlewareOutputError struct {
	Name string
	Err  error
}

func (e MiddlewareOutputError) Error() string {
	return fmt.Sprintf("api/reqreply: middleware %q: invalid output: %s", e.Name, e.Err.Error())
}

// Unwrap allows errors.As/errors.Is to traverse the underlying error.
func (e MiddlewareOutputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareOutputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// DuplicateMiddlewareNameError is returned at Register/ClientHandle time
// when two [Middleware] values attached to the SAME route (via ANY
// combination of .Use()/[Route.HandleMW]/[Route.ClientMW]) share the same
// Declaration.Name — enforced to avoid ambiguous error/observability
// attribution.
type DuplicateMiddlewareNameError struct {
	Route string
	Name  string
}

func (e DuplicateMiddlewareNameError) Error() string {
	return fmt.Sprintf("api/reqreply: route %q: duplicate middleware name %q", e.Route, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateMiddlewareNameError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("name", e.Name),
	)
}

// ConflictingParamContributionError is returned at Register/ClientHandle
// time when two DIFFERENT contributions (a [Middleware][In,Out]'s topic/
// property merge field, or Phase 1b's flat [Route.Use]-attached
// [mqtt5.FromUserPropertyParam]-style declaration) declare the SAME
// topic-var/property name with DIFFERENT attributes (Required-ness or
// codec schema) — mirrors [rest.ConflictingParamContributionError]
// exactly.
//
// ONE uniform algorithm applies to ALL contributions, regardless of which
// mechanism declared them — Phase 1b's own historical silent-first-seen-
// wins dedupe for MISMATCHED declarations is retired (a deliberate,
// narrow, accepted breaking change — see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum's decision #5,
// Round 18).
type ConflictingParamContributionError struct {
	Route        string
	ParamName    string
	FirstSource  string
	SecondSource string
}

func (e ConflictingParamContributionError) Error() string {
	return fmt.Sprintf("api/reqreply: route %q: param %q: conflicting declarations from %q and %q", e.Route, e.ParamName, e.FirstSource, e.SecondSource)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingParamContributionError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("param_name", e.ParamName),
		slog.String("first_source", e.FirstSource),
		slog.String("second_source", e.SecondSource),
	)
}
