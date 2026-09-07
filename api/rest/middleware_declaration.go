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
// Middleware supports TWO attachment styles:
//   - Route/channel-BOUND: attached via [Transform]/[ClientTransform], whose
//     fn additionally receives the route's own req *Req/req Req (read/
//     enrich access) — for concerns whose fn genuinely needs that access.
//   - Route/channel-AGNOSTIC: a Req/Resp-FREE fn bundled directly onto this
//     value via [Middleware.WithReceive]/[Middleware.WithSend], attached via
//     plain .Use(mw) — reusable verbatim across many routes.
//
// A single Middleware value must use EXACTLY ONE style, never both — see
// [AmbiguousMiddlewareAttachmentError].
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	reqHeaderParams  []MergedHeaderParam[In]
	reqCookieParams  []MergedCookieParam[In]
	reqQueryParams   []MergedQueryParam[In]
	respHeaderParams []MergedResponseHeaderParam[Out]
	respCookieParams []MergedResponseCookieParam[Out]

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// Req/Resp-FREE runtime Fn directly on the value itself — enabling
	// route/channel-AGNOSTIC attachment via plain .Use(mw). Left nil for
	// the route/channel-BOUND case, where Transform/ClientTransform supply
	// an req/T-accessing fn separately instead (never both — combining is
	// rejected as ambiguous via [AmbiguousMiddlewareAttachmentError]).
	receiveFn func(ctx context.Context, in In) (Out, error)
	sendFn    func(ctx context.Context) (In, error)
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

// WithReceive attaches a route/channel-AGNOSTIC runtime Fn directly to m —
// its signature never mentions Req/Resp, so the returned Middleware value
// (fn included) can be passed to .Use(...) verbatim, on as many different
// routes as needed. Use [Transform] instead when fn genuinely needs req
// access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out] {
	m.receiveFn = fn
	return m
}

// WithSend is [Middleware.WithReceive]'s client/publish-side sibling — fn
// produces an In value with no req access, attached via .Use(...). Use
// [ClientTransform] instead when fn genuinely needs req access.
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out] {
	m.sendFn = fn
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

// applyAgnosticRoute implements routeMiddlewareContributor — called by
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

// MiddlewareError is returned when a [Transform]/[ClientTransform] fn's own
// returned business error does not match any [ErrorPattern] declared on the
// attaching route — the fallback for a codec-backed middleware's business
// errors, distinct from [SecurityError] (reserved for the
// [middleware.SecurityScheme] mechanism specifically).
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

// DuplicateMiddlewareNameError is returned at Register/Handle time when two
// [Middleware] values attached to the SAME route (via ANY combination of
// .Use()/[Transform]/[ClientTransform]) share the same Declaration.Name —
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

// AmbiguousMiddlewareAttachmentError is returned at Register/Handle time
// when a SINGLE [Middleware] value carries a bundled
// [Middleware.WithReceive]/[Middleware.WithSend] fn AND is ALSO passed to
// [Transform]/[ClientTransform] (which supplies its own, separate fn) —
// ambiguous, since only one fn can run per role.
type AmbiguousMiddlewareAttachmentError struct {
	Name string
}

func (e AmbiguousMiddlewareAttachmentError) Error() string {
	return fmt.Sprintf("api/rest: middleware %q: attached via BOTH .Use() (bundled fn) and Transform/ClientTransform (separate fn) — ambiguous, use only one attachment style per value", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e AmbiguousMiddlewareAttachmentError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
	)
}
