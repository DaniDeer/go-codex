package rest

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/DaniDeer/go-codex/middleware"
)

// Middleware is a codec-backed, REST-specific middleware declaration — the
// per-pattern counterpart to [middleware.Declaration], adding REST's own
// header/cookie/query merge-field vocabulary on both the request (In) and
// response (Out) side. Built via [NewMiddleware], populated via
// [Middleware.WithRequestHeader]/[Middleware.WithRequestCookie]/
// [Middleware.WithRequestQuery]/[Middleware.WithResponseHeader]/
// [Middleware.WithResponseCookie] — REUSING the SAME merge-field
// constructors a route's own Req/Resp already use
// ([NewRequiredHeaderParam]/[NewRequiredCookieParam]/[NewRequiredQueryParam]/
// [NewRequiredResponseHeaderParam]/[NewRequiredResponseCookieParam] and
// their Optional siblings), since those constructors are already generic
// over any T, not hardcoded to a route's own Req/Resp. No new REST-side
// param constructors exist for this — see
// docs/design/d-0003-codec-declared-middlewares.md for the full design.
//
// Middleware supports EXACTLY ONE attachment style — route/channel-
// AGNOSTIC: a Req/Resp-FREE fn bundled directly onto this value via
// [Middleware.WithReceive]/[Middleware.WithSend], attached via plain
// .Use(mw) — reusable verbatim across many routes, since the Fn never
// needs route-specific typing.
//
// For a concern whose Fn genuinely needs read/enrich access to the
// route's own decoded `*Req`/`Req`, use the SEPARATE, explicitly-typed
// [BoundMiddleware][Req, In, Out]/[BoundClientMiddleware][Req, In, Out]
// type instead (bound_middleware.go), attached via
// [Route.HandleBoundMW]/[Route.ClientBoundMW] — NEVER via .Use()/
// HandleMW/ClientMW. Middleware[In,Out] deliberately has NO bound
// attachment path at all anymore (see docs/roadmap/bound-middleware-split.md):
// the former dual-attachment-style design (and its
// AmbiguousMiddlewareAttachmentError ambiguity check) was replaced by
// these two explicit, compile-time-distinct types — a single Middleware
// value can never be bound, so the two styles can never collide on one
// value.
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	reqHeaderParams  []MergedHeaderParam[In]
	reqCookieParams  []MergedCookieParam[In]
	reqQueryParams   []MergedQueryParam[In]
	respHeaderParams []MergedResponseHeaderParam[Out]
	respCookieParams []MergedResponseCookieParam[Out]

	// reqHeaderSpecs/reqCookieSpecs/reqQuerySpecs/respHeaderSpecs/
	// respCookieSpecs carry PRESENCE-ONLY (non-merged) param
	// declarations — pure spec+validation entries with NO corresponding
	// In/Out struct field to decode into, mirroring legacy
	// [middleware.Middleware]'s RequestHeaderParams/etc. shape. Part of
	// the middleware-consolidation effort
	// (docs/design/d-0003-codec-declared-middlewares.md) closing the one real gap
	// the codec-backed family had relative to the legacy type.
	reqHeaderSpecs  []HeaderParam
	reqCookieSpecs  []CookieParam
	reqQuerySpecs   []QueryParam
	respHeaderSpecs []ResponseHeaderParam
	respCookieSpecs []ResponseCookieParam

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// Req/Resp-FREE runtime Fn directly on the value itself — enabling
	// route/channel-AGNOSTIC attachment via plain .Use(mw). There is no
	// route/channel-BOUND counterpart on THIS type at all anymore — a Fn
	// needing `*Req`/`Req` access is built as a SEPARATE
	// [BoundMiddleware][Req, In, Out]/[BoundClientMiddleware][Req, In, Out]
	// value instead (see bound_middleware.go), attached via
	// [Route.HandleBoundMW]/[Route.ClientBoundMW].
	receiveFn func(ctx context.Context, in In) (Out, error)
	sendFn    func(ctx context.Context) (In, error)

	// ctxFieldsFromIn/ctxFieldsFromOut hold every
	// [Middleware.SetContextFieldFromIn]/[Middleware.SetContextFieldFromOut]
	// registration — dispatched automatically by buildDecodeIn (right
	// after decode+validate) and buildEncodeOut/buildEncodeIn (alongside
	// the merge-field encode), publishing into the SAME
	// [middleware.ContextField] box [middleware.EnsureContextFields]
	// pre-allocated (docs/design/d-0007-declarative-middleware-layering.md's
	// Rollout Phase A).
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
// chain [Middleware.WithRequestHeader]/etc. to populate its merge-field
// vocabulary, or [Middleware.WithReceive]/[Middleware.WithSend] for the
// route/channel-agnostic attachment style.
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out] {
	return Middleware[In, Out]{Declaration: decl}
}

// WithRequestHeader registers p (built via [NewRequiredHeaderParam]/
// [NewOptionalHeaderParam]) so its value is merged into this middleware's
// decoded In at dispatch time, and layered into the attaching route's spec
// (the SAME conflict-detection pass legacy middleware.Middleware values
// already feed) — and returns the updated Middleware.
func (m Middleware[In, Out]) WithRequestHeader(p MergedHeaderParam[In]) Middleware[In, Out] {
	m.reqHeaderParams = append(slices.Clone(m.reqHeaderParams), p)
	return m
}

// WithRequestCookie is [Middleware.WithRequestHeader]'s cookie-request-param
// sibling — p is built via [NewRequiredCookieParam]/[NewOptionalCookieParam].
func (m Middleware[In, Out]) WithRequestCookie(p MergedCookieParam[In]) Middleware[In, Out] {
	m.reqCookieParams = append(slices.Clone(m.reqCookieParams), p)
	return m
}

// WithRequestQuery is [Middleware.WithRequestHeader]'s query-param sibling —
// p is built via [NewRequiredQueryParam]/[NewOptionalQueryParam].
func (m Middleware[In, Out]) WithRequestQuery(p MergedQueryParam[In]) Middleware[In, Out] {
	m.reqQueryParams = append(slices.Clone(m.reqQueryParams), p)
	return m
}

// WithResponseHeader registers p (built via [NewRequiredResponseHeaderParam]/
// [NewOptionalResponseHeaderParam]) so its value is derived from this
// middleware's Out and set as an actual HTTP response header, and returns
// the updated Middleware.
func (m Middleware[In, Out]) WithResponseHeader(p MergedResponseHeaderParam[Out]) Middleware[In, Out] {
	m.respHeaderParams = append(slices.Clone(m.respHeaderParams), p)
	return m
}

// WithResponseCookie is [Middleware.WithResponseHeader]'s cookie sibling —
// p is built via [NewRequiredResponseCookieParam]/[NewOptionalResponseCookieParam].
// This is the mechanism that closes the original gap motivating this
// design: a merge-derived response cookie can now carry declared
// attributes (via Out's own fields), not just a name/value pair.
func (m Middleware[In, Out]) WithResponseCookie(p MergedResponseCookieParam[Out]) Middleware[In, Out] {
	m.respCookieParams = append(slices.Clone(m.respCookieParams), p)
	return m
}

// WithRequestHeaderSpec registers a PRESENCE-ONLY (non-merged) request
// header declaration — p is validated and rendered into the route's spec,
// but has NO corresponding In struct field to decode into. Use
// [Middleware.WithRequestHeader] instead when a merge field is wanted.
// Mirrors legacy middleware.Middleware's RequestHeaderParams shape.
func (m Middleware[In, Out]) WithRequestHeaderSpec(p HeaderParam) Middleware[In, Out] {
	m.reqHeaderSpecs = append(slices.Clone(m.reqHeaderSpecs), p)
	return m
}

// WithRequestCookieSpec is [Middleware.WithRequestHeaderSpec]'s cookie
// sibling.
func (m Middleware[In, Out]) WithRequestCookieSpec(p CookieParam) Middleware[In, Out] {
	m.reqCookieSpecs = append(slices.Clone(m.reqCookieSpecs), p)
	return m
}

// WithRequestQuerySpec is [Middleware.WithRequestHeaderSpec]'s query
// sibling.
func (m Middleware[In, Out]) WithRequestQuerySpec(p QueryParam) Middleware[In, Out] {
	m.reqQuerySpecs = append(slices.Clone(m.reqQuerySpecs), p)
	return m
}

// WithResponseHeaderSpec is [Middleware.WithRequestHeaderSpec]'s
// response-side sibling.
func (m Middleware[In, Out]) WithResponseHeaderSpec(p ResponseHeaderParam) Middleware[In, Out] {
	m.respHeaderSpecs = append(slices.Clone(m.respHeaderSpecs), p)
	return m
}

// WithResponseCookieSpec is [Middleware.WithRequestHeaderSpec]'s
// response-cookie sibling.
func (m Middleware[In, Out]) WithResponseCookieSpec(p ResponseCookieParam) Middleware[In, Out] {
	m.respCookieSpecs = append(slices.Clone(m.respCookieSpecs), p)
	return m
}

// WithReceive attaches a route/channel-AGNOSTIC runtime Fn directly to m —
// its signature never mentions Req/Resp, so the returned Middleware value
// (fn included) can be passed to .Use(...) verbatim, on as many different
// routes as needed. Use [BoundMiddleware]/[Route.HandleBoundMW] instead
// when fn genuinely needs req access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out] {
	m.receiveFn = fn
	return m
}

// WithSend is [Middleware.WithReceive]'s client/publish-side sibling — fn
// produces an In value with no req access, attached via .Use(...). Use
// [BoundClientMiddleware]/[Route.ClientBoundMW] instead when fn genuinely
// needs req access.
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out] {
	m.sendFn = fn
	return m
}

// SetContextFieldFromIn registers field to be published (via
// [middleware.ContextFieldSetter.Set]) from get(in)'s return value —
// dispatched automatically right after this middleware's own In is
// decoded+validated (DecodeIn). [BoundMiddleware] has its OWN identical
// forwarder (bound_middleware.go) for the bound attachment class, so a
// handler (or any LATER-dispatched middleware, regardless of class)
// retrieves the published value the SAME way, fully-typed, via
// [middleware.ContextField.Get] — see [middleware.ContextField]'s own
// doc comment for the full cross-cutting-data rationale.
//
// field takes [middleware.ContextFieldSetter], not a concrete
// [middleware.ContextField][V] directly — V is NOT a type parameter this
// method can introduce (Go forbids new type params on a method beyond the
// receiver's own); every ContextField[V] already satisfies
// ContextFieldSetter regardless of V, since Set's own signature never
// references V (confirmed via an actual compile check — see
// docs/design/d-0007-declarative-middleware-layering.md's Phase 3 design
// review).
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
// server/receiving role) or right after it is decoded from the response's
// headers/cookies (DecodeOut, the client/sending role).
func (m Middleware[In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) Middleware[In, Out] {
	m.ctxFieldsFromOut = append(slices.Clone(m.ctxFieldsFromOut), contextFieldOutSetter[Out]{field: field, get: get})
	return m
}

// RouteMiddlewareMarker makes Middleware[In,Out] satisfy
// [middleware.RouteMiddleware] — EXPORTED (unlike [ports.Pattern]'s
// unexported-method sealing) because Go's unexported-method interface
// satisfaction is scoped per package: a type declared in api/rest can
// never satisfy an interface whose method is unexported in package
// middleware, no matter the name — see [middleware.RouteMiddleware]'s doc
// comment. So a route/channel's plain .Use(...) can recognize and
// dispatch a route/channel-agnostic Middleware value carrying a
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

// applyAgnosticRoute implements routeMiddlewareContributor — called by
// [routeMiddlewareOpt.applyRoute] for a .Use()-attached Middleware value.
// In/Out are concrete here (m's own type parameters), so it can build the
// SAME spec contribution and (when bundled) the SAME runtime dispatch
// handler shape [BoundMiddleware.applyBoundRoute] produces for the bound
// class — feeding both into the SAME rb fields, so downstream consumers
// (applyParamDeclarations, adapters) treat both classes uniformly.
func (m Middleware[In, Out]) applyAgnosticRoute(rb *routeBuilder) {
	rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, specContributionOf(m))
	if m.receiveFn != nil {
		rb.middlewareHandlers = append(rb.middlewareHandlers, buildAgnosticMiddlewareHandler(m))
	}
	if m.sendFn != nil {
		rb.clientMiddlewareHandlers = append(rb.clientMiddlewareHandlers, buildAgnosticClientMiddlewareHandler(m))
	}
}

// NOTE: applyBoundRoute/applyBoundClientRoute (the route/channel-BOUND
// attachment methods Middleware[In,Out] used to carry) were REMOVED as
// part of docs/roadmap/bound-middleware-split.md — Middleware[In,Out] is
// now reusable-ONLY (.Use() is its one attachment path); the route/
// channel-BOUND case moved to the dedicated [BoundMiddleware][Req, In, Out]
// type (see bound_middleware.go), attached via [Route.HandleBoundMW]/
// [Route.ClientBoundMW] instead.

// MiddlewareInputError is returned when a [Middleware]'s In value fails to
// decode/validate from the raw request header/cookie/query vars.
//
// Use errors.As to extract the failing middleware's name:
//
//	var inputErr rest.MiddlewareInputError
//	if errors.As(err, &inputErr) {
//	    log.Printf("middleware %q: invalid input: %v", inputErr.Name, inputErr.Err)
//	}
type MiddlewareInputError struct {
	Name string
	Err  error
}

func (e MiddlewareInputError) Error() string {
	return fmt.Sprintf("api/rest: middleware %q: invalid input: %s", e.Name, e.Err.Error())
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

// MiddlewareError is returned when a codec-backed middleware's own Fn —
// a [Middleware.WithReceive]/[Middleware.WithSend] Fn (reusable class,
// attached via plain .Use()) or a [BoundMiddleware]/[BoundClientMiddleware]
// Fn (bound class, attached via [Route.HandleBoundMW]/[Route.ClientBoundMW])
// — returns a business error that does not match any [ErrorPattern]
// declared on the attaching route. NEVER returned for [Route.HandleMW]/
// [Route.ClientMW], which reject a codec-backed value outright (see
// [MiddlewareMisattachedError]) and otherwise only ever carry the legacy,
// non-codec-backed [middleware.Middleware] shape. Distinct from
// [SecurityError] (reserved for the [middleware.SecurityScheme]
// mechanism specifically).
//
// Use errors.As to extract the failing middleware's name:
//
//	var mwErr rest.MiddlewareError
//	if errors.As(err, &mwErr) {
//	    log.Printf("middleware %q failed: %v", mwErr.Name, mwErr.Err)
//	}
type MiddlewareError struct {
	Name string
	Err  error
}

func (e MiddlewareError) Error() string {
	return fmt.Sprintf("api/rest: middleware %q: %s", e.Name, e.Err.Error())
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
// encode into response headers/cookies (via [Middleware].EncodeOut/
// EncodeOutCookieAttrs) — the OUTPUT-side counterpart of
// [MiddlewareInputError], added for symmetry (previously this failure
// propagated as a bare, unwrapped error with no way to recover which
// middleware failed; see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2).
//
// Use errors.As to extract the failing middleware's name:
//
//	var outputErr rest.MiddlewareOutputError
//	if errors.As(err, &outputErr) {
//	    log.Printf("middleware %q: invalid output: %v", outputErr.Name, outputErr.Err)
//	}
type MiddlewareOutputError struct {
	Name string
	Err  error
}

func (e MiddlewareOutputError) Error() string {
	return fmt.Sprintf("api/rest: middleware %q: invalid output: %s", e.Name, e.Err.Error())
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

// DuplicateMiddlewareNameError is returned at Register/Handle time when two
// [Middleware] values attached to the SAME route (via ANY combination of
// .Use()/HandleMW/ClientMW) share the same Declaration.Name —
// enforced to avoid ambiguous error/observability attribution.
type DuplicateMiddlewareNameError struct {
	Route string
	Name  string
}

func (e DuplicateMiddlewareNameError) Error() string {
	return fmt.Sprintf("api/rest: route %q: duplicate middleware name %q", e.Route, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateMiddlewareNameError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("name", e.Name),
	)
}

// NOTE: AmbiguousMiddlewareAttachmentError (D7) was REMOVED — the
// dual-attachment ambiguity it caught is now structurally impossible, not
// merely checked; see [checkMiddlewareNameUniquenessAndAttachment]'s doc
// comment and docs/roadmap/bound-middleware-split.md.
