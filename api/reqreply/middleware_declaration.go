package reqreply

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/DaniDeer/go-codex/middleware"
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
// Middleware supports TWO attachment styles, exactly mirroring
// [rest.Middleware]:
//   - Route-BOUND: attached via [Transform]/[ClientTransform], whose fn
//     additionally receives the route's own req *Req/req Req.
//   - Route-AGNOSTIC: a Req/Resp-FREE fn bundled directly onto this value
//     via [Middleware.WithReceive]/[Middleware.WithSend], attached via
//     plain .Use(mw) — reusable verbatim across many routes.
//
// A single Middleware value must use EXACTLY ONE style, never both — see
// [AmbiguousMiddlewareAttachmentError].
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	topicMergeFieldsIn     []MergedTopicParam[In]
	topicMergeFieldsOut    []MergedTopicParam[Out]
	propertyMergeFieldsIn  []MergedPropertyParam[In]
	propertyMergeFieldsOut []MergedPropertyParam[Out]

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// Req/Resp-FREE runtime Fn directly on the value itself — enabling
	// route-AGNOSTIC attachment via plain .Use(mw). Left nil for the
	// route-BOUND case, where Transform/ClientTransform supply a
	// req/T-accessing fn separately instead (never both — combining is
	// rejected as ambiguous via [AmbiguousMiddlewareAttachmentError]).
	receiveFn func(ctx context.Context, in In) (Out, error)
	sendFn    func(ctx context.Context) (In, error)
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
// encoded into the reply's topic vars once [Transform]'s fn (or a bundled
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

// WithReceive attaches a route-AGNOSTIC runtime Fn directly to m — its
// signature never mentions Req/Resp, so the returned Middleware value (fn
// included) can be passed to .Use(...) verbatim, on as many different
// routes as needed. Use [Transform] instead when fn genuinely needs req
// access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out] {
	m.receiveFn = fn
	return m
}

// WithSend is [Middleware.WithReceive]'s client-side sibling — fn produces
// an In value with no req access, attached via .Use(...). Use
// [ClientTransform] instead when fn genuinely needs req access.
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out] {
	m.sendFn = fn
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

// applyAgnosticRoute implements [routeMiddlewareContributor] — called by
// [routeMiddlewareOpt.applyRoute] for a .Use()-attached Middleware value.
// In/Out are concrete here (m's own type parameters), so it can build the
// SAME spec contribution and (when bundled) the SAME runtime dispatch
// handler as [Transform]/[ClientTransform] produce for the route-BOUND
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

// MiddlewareError is returned when a [Transform]/[ClientTransform] fn's
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

// DuplicateMiddlewareNameError is returned at Register/ClientHandle time
// when two [Middleware] values attached to the SAME route (via ANY
// combination of .Use()/[Transform]/[ClientTransform]) share the same
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

// AmbiguousMiddlewareAttachmentError is returned at Register/ClientHandle
// time when a SINGLE [Middleware] value carries a bundled
// [Middleware.WithReceive]/[Middleware.WithSend] fn AND is ALSO passed to
// [Transform]/[ClientTransform] (which supplies its own, separate fn) —
// ambiguous, since only one fn can run per role.
type AmbiguousMiddlewareAttachmentError struct {
	Name string
}

func (e AmbiguousMiddlewareAttachmentError) Error() string {
	return fmt.Sprintf("api/reqreply: middleware %q: attached via BOTH .Use() (bundled fn) and Transform/ClientTransform (separate fn) — ambiguous, use only one attachment style per value", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e AmbiguousMiddlewareAttachmentError) LogValue() slog.Value {
	return slog.GroupValue(
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
// docs/roadmap/reqreply-codec-declared-middleware.md's decision #5,
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
