package rest

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// BoundMiddleware is the route/channel-BOUND counterpart to [Middleware] —
// its Fn is EMBEDDED AT CONSTRUCTION (via [NewBoundMiddleware]/
// [BoundSecurityMiddleware]), never supplied separately later, and
// additionally receives the attaching route's own decoded *Req value —
// for middleware logic that genuinely needs to read/write the route's
// own request struct (not just a header/cookie/query merge field), e.g.
// an in-payload credential field on a transport with no header/property
// side channel at all. See docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 for
// the full design this type implements.
//
// Attach via [Route.HandleBoundMW]/[SSERoute.HandleBoundMW] — NEVER via
// plain .Use() (BoundMiddleware deliberately does NOT satisfy
// [routeMiddlewareContributor]'s agnostic path; see the INTERNAL LAYOUT
// note below for why).
//
// A Security-carrying BoundMiddleware (server) and a
// [BoundClientMiddleware] (client) for the SAME scheme name CANNOT be
// attached to ONE shared route value — unlike [Middleware][In,Out],
// which can carry both a receiveFn and a sendFn and be `.Use()`'d ONCE
// for both roles, [HandleBoundMW]/[ClientBoundMW] each independently
// contribute a spec entry under the scheme's Declaration Name, and
// D6(b)'s name-uniqueness check rejects the resulting duplicate with
// [DuplicateMiddlewareNameError]. Build TWO SEPARATE route values
// instead — one per role — exactly like every example in this repo
// does (see e.g. examples/adapters-sse's securedServerMw/
// securedClientMw split).
//
// INTERNAL LAYOUT — a NAMED field, not an embedded one, by design: mw
// holds a [Middleware][Req, In, Out]-shaped merge-field/Declaration value
// giving BoundMiddleware the EXACT SAME merge-field vocabulary as
// [Middleware] for free, letting [BoundMiddleware.ApplyBoundRoute] call
// the EXISTING [buildMiddlewareHandlerAny]/[boundSpecContributionOf]
// helpers UNCHANGED, passing mw. Using a NAMED field (never anonymous/
// embedded) is DELIBERATE: Go promotes ALL methods of an embedded field,
// which would silently promote [Middleware.applyAgnosticRoute] onto
// BoundMiddleware too — making it accidentally satisfy
// [routeMiddlewareContributor] and attachable via plain .Use(), exactly
// the bound/reusable ambiguity this type exists to eliminate.
type BoundMiddleware[Req, In, Out any] struct {
	mw Middleware[In, Out] // NAMED, not embedded — see doc comment above
	fn func(ctx context.Context, req *Req, in In) (Out, error)
}

// NewBoundMiddleware builds a [BoundMiddleware] from a
// [middleware.Declaration] and its Fn — fn's shape is checked by the
// ordinary Go compiler at this call, zero reflection needed to verify
// arity/types (contrast with the now-removed isBoundHandleMWShape
// runtime detector).
func NewBoundMiddleware[Req, In, Out any](
	decl middleware.Declaration[In, Out],
	fn func(ctx context.Context, req *Req, in In) (Out, error),
) BoundMiddleware[Req, In, Out] {
	return BoundMiddleware[Req, In, Out]{mw: NewMiddleware[In, Out](decl), fn: fn}
}

// BoundSecurityMiddleware mirrors [SecurityMiddleware]'s role for the
// bound class — a Security-carrying [BoundMiddleware], fn embedded at
// construction, attached via [Route.HandleBoundMW]/[SSERoute.HandleBoundMW]
// (never .Use()/HandleMW). The SAME GrantedScopes convention
// [SecurityMiddleware]'s own doc comment documents in full applies
// identically here: Out MUST carry a field literally named
// `GrantedScopes map[string][]string`, required even when zero specific
// scopes are declared — see that doc comment for the full writeup and a
// runnable example of the gotcha this convention guards against.
func BoundSecurityMiddleware[Req, In, Out any](
	schemeName string, scheme SecurityScheme, scopes []string,
	fn func(ctx context.Context, req *Req, in In) (Out, error),
) BoundMiddleware[Req, In, Out] {
	return BoundMiddleware[Req, In, Out]{
		mw: NewMiddleware[In, Out](middleware.Declaration[In, Out]{
			Name:     "declare-security:" + schemeName,
			InCodec:  codex.Struct[In](),
			OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme.SecurityScheme, scopes, scheme.Codec),
		}),
		fn: fn,
	}
}

// WithRequestHeader mirrors [Middleware.WithRequestHeader] — a one-line
// forwarder onto the named mw field's own existing method.
func (m BoundMiddleware[Req, In, Out]) WithRequestHeader(p MergedHeaderParam[In]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestHeader(p)
	return m
}

// WithRequestCookie mirrors [Middleware.WithRequestCookie].
func (m BoundMiddleware[Req, In, Out]) WithRequestCookie(p MergedCookieParam[In]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestCookie(p)
	return m
}

// WithRequestQuery mirrors [Middleware.WithRequestQuery].
func (m BoundMiddleware[Req, In, Out]) WithRequestQuery(p MergedQueryParam[In]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestQuery(p)
	return m
}

// WithResponseHeader mirrors [Middleware.WithResponseHeader].
func (m BoundMiddleware[Req, In, Out]) WithResponseHeader(p MergedResponseHeaderParam[Out]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseHeader(p)
	return m
}

// WithResponseCookie mirrors [Middleware.WithResponseCookie].
func (m BoundMiddleware[Req, In, Out]) WithResponseCookie(p MergedResponseCookieParam[Out]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseCookie(p)
	return m
}

// WithRequestHeaderSpec mirrors [Middleware.WithRequestHeaderSpec].
func (m BoundMiddleware[Req, In, Out]) WithRequestHeaderSpec(p HeaderParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestHeaderSpec(p)
	return m
}

// WithRequestCookieSpec mirrors [Middleware.WithRequestCookieSpec].
func (m BoundMiddleware[Req, In, Out]) WithRequestCookieSpec(p CookieParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestCookieSpec(p)
	return m
}

// WithRequestQuerySpec mirrors [Middleware.WithRequestQuerySpec].
func (m BoundMiddleware[Req, In, Out]) WithRequestQuerySpec(p QueryParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestQuerySpec(p)
	return m
}

// WithResponseHeaderSpec mirrors [Middleware.WithResponseHeaderSpec].
func (m BoundMiddleware[Req, In, Out]) WithResponseHeaderSpec(p ResponseHeaderParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseHeaderSpec(p)
	return m
}

// WithResponseCookieSpec mirrors [Middleware.WithResponseCookieSpec].
func (m BoundMiddleware[Req, In, Out]) WithResponseCookieSpec(p ResponseCookieParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseCookieSpec(p)
	return m
}

// SetContextFieldFromIn mirrors [Middleware.SetContextFieldFromIn].
func (m BoundMiddleware[Req, In, Out]) SetContextFieldFromIn(field middleware.ContextFieldSetter, get func(In) any) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.SetContextFieldFromIn(field, get)
	return m
}

// SetContextFieldFromOut mirrors [Middleware.SetContextFieldFromOut].
func (m BoundMiddleware[Req, In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.SetContextFieldFromOut(field, get)
	return m
}

// MiddlewareName lets callers extract a human-readable name even when a
// Req mismatch means [boundContributor] itself can't be asserted — used
// by [Route.HandleBoundMW]'s error-message enrichment.
func (m BoundMiddleware[Req, In, Out]) MiddlewareName() string { return m.mw.MiddlewareName() }

// ApplyBoundRoute satisfies [middleware.BoundContributor][Req] —
// BoundMiddleware's ONLY attach path. Calls the EXISTING, UNCHANGED
// buildMiddlewareHandlerAny/boundSpecContributionOf helpers with the
// named mw field, exactly as the (now-removed) reflection-detected bound
// case used to, back when `Middleware[In,Out]` itself carried this
// method (see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7). rb is the
// narrow [middleware.BoundRouteBuilder] interface (docs/roadmap/
// shared-api-layer-mechanics.md's Phase 2) — [*routeBuilder] implements
// it below.
func (m BoundMiddleware[Req, In, Out]) ApplyBoundRoute(rb middleware.BoundRouteBuilder) {
	rb.AppendMiddlewareHandler(buildMiddlewareHandlerAny(m.mw, m.fn))
	rb.AppendSpecContribution(boundSpecContributionOf(m.mw))
	if sec := m.mw.SecurityDeclaration(); sec != nil {
		rb.AppendSecurityDeclaration(m.mw.MiddlewareName(), sec)
	}
}

// BoundReqWitness satisfies [middleware.BoundContributor]'s type-level
// witness — never called; see that interface's doc comment.
func (m BoundMiddleware[Req, In, Out]) BoundReqWitness(Req) {}

// BoundClientMiddleware is [BoundMiddleware]'s SENDING-role sibling,
// attached via [Route.ClientBoundMW]/[SSERoute.ClientBoundMW]. Fn shape
// differs (Req BY VALUE, matching ClientMW's existing bound-shape
// convention): func(ctx, req Req) (In, error). Same named-field layout
// as [BoundMiddleware], same rationale — including the SAME "cannot
// share one route value with a same-scheme [BoundMiddleware]" constraint
// documented on [BoundMiddleware]'s own doc comment; build two separate
// route values instead.
type BoundClientMiddleware[Req, In, Out any] struct {
	mw Middleware[In, Out] // NAMED, not embedded — same rationale as BoundMiddleware
	fn func(ctx context.Context, req Req) (In, error)
}

// NewBoundClientMiddleware builds a [BoundClientMiddleware] from a
// [middleware.Declaration] and its Fn.
func NewBoundClientMiddleware[Req, In, Out any](
	decl middleware.Declaration[In, Out],
	fn func(ctx context.Context, req Req) (In, error),
) BoundClientMiddleware[Req, In, Out] {
	return BoundClientMiddleware[Req, In, Out]{mw: NewMiddleware[In, Out](decl), fn: fn}
}

// BoundSecurityClientMiddleware mirrors [BoundSecurityMiddleware] for the
// client/sending role.
func BoundSecurityClientMiddleware[Req, In, Out any](
	schemeName string, scheme SecurityScheme, scopes []string,
	fn func(ctx context.Context, req Req) (In, error),
) BoundClientMiddleware[Req, In, Out] {
	return BoundClientMiddleware[Req, In, Out]{
		mw: NewMiddleware[In, Out](middleware.Declaration[In, Out]{
			Name:     "declare-security:" + schemeName,
			InCodec:  codex.Struct[In](),
			OutCodec: codex.Struct[Out](),
			Security: middleware.NewSecurityDeclaration(schemeName, scheme.SecurityScheme, scopes, scheme.Codec),
		}),
		fn: fn,
	}
}

// WithRequestHeader mirrors [BoundMiddleware.WithRequestHeader].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestHeader(p MergedHeaderParam[In]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestHeader(p)
	return m
}

// WithRequestCookie mirrors [BoundMiddleware.WithRequestCookie].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestCookie(p MergedCookieParam[In]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestCookie(p)
	return m
}

// WithRequestQuery mirrors [BoundMiddleware.WithRequestQuery].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestQuery(p MergedQueryParam[In]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestQuery(p)
	return m
}

// WithResponseHeader mirrors [BoundMiddleware.WithResponseHeader].
func (m BoundClientMiddleware[Req, In, Out]) WithResponseHeader(p MergedResponseHeaderParam[Out]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseHeader(p)
	return m
}

// WithResponseCookie mirrors [BoundMiddleware.WithResponseCookie].
func (m BoundClientMiddleware[Req, In, Out]) WithResponseCookie(p MergedResponseCookieParam[Out]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseCookie(p)
	return m
}

// WithRequestHeaderSpec mirrors [BoundMiddleware.WithRequestHeaderSpec].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestHeaderSpec(p HeaderParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestHeaderSpec(p)
	return m
}

// WithRequestCookieSpec mirrors [BoundMiddleware.WithRequestCookieSpec].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestCookieSpec(p CookieParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestCookieSpec(p)
	return m
}

// WithRequestQuerySpec mirrors [BoundMiddleware.WithRequestQuerySpec].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestQuerySpec(p QueryParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestQuerySpec(p)
	return m
}

// WithResponseHeaderSpec mirrors [BoundMiddleware.WithResponseHeaderSpec].
func (m BoundClientMiddleware[Req, In, Out]) WithResponseHeaderSpec(p ResponseHeaderParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseHeaderSpec(p)
	return m
}

// WithResponseCookieSpec mirrors [BoundMiddleware.WithResponseCookieSpec].
func (m BoundClientMiddleware[Req, In, Out]) WithResponseCookieSpec(p ResponseCookieParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseCookieSpec(p)
	return m
}

// SetContextFieldFromIn mirrors [BoundMiddleware.SetContextFieldFromIn].
func (m BoundClientMiddleware[Req, In, Out]) SetContextFieldFromIn(field middleware.ContextFieldSetter, get func(In) any) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.SetContextFieldFromIn(field, get)
	return m
}

// SetContextFieldFromOut mirrors [BoundMiddleware.SetContextFieldFromOut].
func (m BoundClientMiddleware[Req, In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.SetContextFieldFromOut(field, get)
	return m
}

// MiddlewareName mirrors [BoundMiddleware.MiddlewareName].
func (m BoundClientMiddleware[Req, In, Out]) MiddlewareName() string { return m.mw.MiddlewareName() }

// ApplyBoundClientRoute satisfies [middleware.BoundClientContributor][Req]
// — BoundClientMiddleware's ONLY attach path.
func (m BoundClientMiddleware[Req, In, Out]) ApplyBoundClientRoute(rb middleware.BoundRouteBuilder) {
	rb.AppendClientMiddlewareHandler(buildClientMiddlewareHandlerAny(m.mw, m.fn))
	rb.AppendSpecContribution(boundSpecContributionOf(m.mw))
	if sec := m.mw.SecurityDeclaration(); sec != nil {
		rb.AppendSecurityDeclaration(m.mw.MiddlewareName(), sec)
	}
}

// BoundReqWitness satisfies [middleware.BoundClientContributor]'s
// type-level witness — never called; see that interface's doc comment.
func (m BoundClientMiddleware[Req, In, Out]) BoundReqWitness(Req) {}

// boundContributor/boundClientContributor/boundNamed are internal
// aliases for the shared, EXPORTED [middleware.BoundContributor]/
// [middleware.BoundClientContributor]/[middleware.BoundNamed] —
// consolidated per docs/design/d-0009-internalize-shared-mechanics.md's Phase 2
// (kept under their pre-consolidation unexported names so every existing
// call site in this file keeps compiling unchanged). [BoundMiddleware]/
// [BoundClientMiddleware] satisfy them for their own Req only — Go's own
// generic interface satisfaction does the matching; no Fn-shape
// reflection anywhere. See [middleware.BoundContributor]'s own doc
// comment for the BoundReqWitness discriminator-trick rationale.
type boundContributor[Req any] = middleware.BoundContributor[Req]
type boundClientContributor[Req any] = middleware.BoundClientContributor[Req]
type boundNamed = middleware.BoundNamed

// routeBuilder's 4 [middleware.BoundRouteBuilder] methods — one-line
// appends to its EXISTING internal slices, unchanged from what
// ApplyBoundRoute/ApplyBoundClientRoute did inline before this
// consolidation.
func (rb *routeBuilder) AppendMiddlewareHandler(h any) {
	rb.middlewareHandlers = append(rb.middlewareHandlers, h.(MiddlewareHandler))
}

func (rb *routeBuilder) AppendClientMiddlewareHandler(h any) {
	rb.clientMiddlewareHandlers = append(rb.clientMiddlewareHandlers, h.(ClientMiddlewareHandler))
}

func (rb *routeBuilder) AppendSpecContribution(c any) {
	rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, c.(middlewareSpecContribution))
}

func (rb *routeBuilder) AppendSecurityDeclaration(name string, sec *middleware.SecurityDeclaration) {
	rb.middlewares = append(rb.middlewares, middleware.Middleware{Name: name, Security: sec})
}

// boundHandleMWOpt is the [RouteOpt] returned by [Route.HandleBoundMW]/
// [SSERoute.HandleBoundMW] when bm's Req matched the route's own. fn is
// already a closure over the concrete, Req-confirmed bm value (bound at
// the SUCCESSFUL type-assertion call site, before any type-erasure) —
// RouteOpt itself cannot be generic over Req, so by the time this opt is
// constructed, the Req match has ALREADY been verified; nothing further
// needs Req at this point.
//
// name is captured HERE (via [boundNamed], at the SAME call site fn is
// built) rather than read back out of rb later — a non-security-carrying
// bound middleware never touches rb.middlewares (only
// rb.middlewareSpecContributions, which [Router.middlewareNames] does
// NOT consult either), so without storing name directly on the opt
// itself, a Router-grouped leaf's [RouterEntry.MiddlewareNames] would
// silently omit every bound middleware with no Security declaration —
// a confirmed, previously-real gap (HandleBoundMW is a documented,
// actively-used feature, not an obscure corner).
type boundHandleMWOpt struct {
	name string
	fn   func(rb middleware.BoundRouteBuilder)
}

func (o boundHandleMWOpt) applyRoute(rb *routeBuilder) { o.fn(rb) }

// boundClientAttachOpt mirrors boundHandleMWOpt for the client/sending
// role — see that type's doc comment for why name is captured here.
type boundClientAttachOpt struct {
	name string
	fn   func(rb middleware.BoundRouteBuilder)
}

func (o boundClientAttachOpt) applyRoute(rb *routeBuilder) { o.fn(rb) }

// boundNameOf extracts bm's name via the Req-free [boundNamed] interface,
// returning "" when bm doesn't implement it — used at HandleBoundMW/
// ClientBoundMW construction time to populate boundHandleMWOpt/
// boundClientAttachOpt's own name field. Forwards to the shared
// [middleware.BoundNameOf] (docs/design/d-0009-internalize-shared-mechanics.md's
// Phase 2) — kept as a thin, same-named local wrapper so every existing
// call site in this file keeps compiling unchanged.
func boundNameOf(bm any) string { return middleware.BoundNameOf(bm) }

// boundMismatchOpt is the [RouteOpt] returned by [Route.HandleBoundMW]/
// [ClientBoundMW] when bm's concrete Req did NOT match the route's own
// (or bm wasn't a bound-middleware value at all) — stashes a build-time
// error onto rb, surfaced early by registerHandle, mirroring
// InvalidPathError's own existing early-return pattern.
type boundMismatchOpt struct {
	route string
	got   any
}

func (o boundMismatchOpt) applyRoute(rb *routeBuilder) {
	if rb.buildErr == nil {
		name := ""
		if n, ok := o.got.(boundNamed); ok {
			name = n.MiddlewareName()
		}
		rb.buildErr = BoundMiddlewareReqMismatchError{Route: o.route, Got: o.got, Name: name}
	}
}

// HandleBoundMW attaches bm — a [BoundMiddleware][Req, In, Out] value
// whose Req matches THIS route's own Req type parameter — giving its
// embedded Fn *Req access via the SAME route-BOUND mechanism as the
// (now-removed) reflection-detected case, now reached through an
// explicit, dedicated method and a concrete, distinct Go type instead of
// Fn-shape guessing.
//
// bm is accepted as `any` because a method cannot introduce a NEW type
// parameter beyond its receiver's own (Req/Resp, here) — attaching is
// resolved via a Go generic interface assertion against
// [boundContributor][Req], instantiated from THIS route's OWN Req, not
// inferred from bm. A bm constructed with the WRONG Req (e.g. a
// [BoundMiddleware][Foo, ...] attached to a Route[Bar, ...]) is a
// deliberate-misuse case, caught immediately and loudly via
// [BoundMiddlewareReqMismatchError] at Register time — never silently
// mis-dispatched.
func (r Route[Req, Resp]) HandleBoundMW(bm any) Route[Req, Resp] {
	if v, ok := bm.(boundContributor[Req]); ok {
		r.opts = append(slices.Clone(r.opts), boundHandleMWOpt{name: boundNameOf(bm), fn: v.ApplyBoundRoute})
		return r
	}
	r.opts = append(slices.Clone(r.opts), boundMismatchOpt{route: r.path, got: bm})
	return r
}

// HandleBoundMW is [SSERoute]'s equivalent of [Route.HandleBoundMW].
func (s SSERoute[Req, Event]) HandleBoundMW(bm any) SSERoute[Req, Event] {
	if v, ok := bm.(boundContributor[Req]); ok {
		s.opts = append(slices.Clone(s.opts), boundHandleMWOpt{name: boundNameOf(bm), fn: v.ApplyBoundRoute})
		return s
	}
	s.opts = append(slices.Clone(s.opts), boundMismatchOpt{route: s.path, got: bm})
	return s
}

// ClientBoundMW attaches bm — a [BoundClientMiddleware][Req, In, Out]
// value whose Req matches this route's own — mirroring
// [Route.HandleBoundMW] for the sending role.
func (r Route[Req, Resp]) ClientBoundMW(bm any) Route[Req, Resp] {
	if v, ok := bm.(boundClientContributor[Req]); ok {
		r.opts = append(slices.Clone(r.opts), boundClientAttachOpt{name: boundNameOf(bm), fn: v.ApplyBoundClientRoute})
		return r
	}
	r.opts = append(slices.Clone(r.opts), boundMismatchOpt{route: r.path, got: bm})
	return r
}

// ClientBoundMW is [SSERoute]'s equivalent of [Route.ClientBoundMW].
func (s SSERoute[Req, Event]) ClientBoundMW(bm any) SSERoute[Req, Event] {
	if v, ok := bm.(boundClientContributor[Req]); ok {
		s.opts = append(slices.Clone(s.opts), boundClientAttachOpt{name: boundNameOf(bm), fn: v.ApplyBoundClientRoute})
		return s
	}
	s.opts = append(slices.Clone(s.opts), boundMismatchOpt{route: s.path, got: bm})
	return s
}

// BoundMiddlewareReqMismatchError is returned (via rb.buildErr) when
// [Route.HandleBoundMW]/[Route.ClientBoundMW] (or their SSERoute
// equivalents) is called with a value whose concrete Req does not match
// the route's own — or with a value that isn't a bound-middleware at
// all (e.g. a plain [Middleware] value, which deliberately has NO bound
// attachment path anymore). Surfaced as a normal error at
// Register/RegisterHandle time; [Route.ClientHandle]/
// [SSERoute.ClientHandle] have no error return at all, so they instead
// PANIC with this error's message (matching those methods' own existing
// [FormatOptError] panic precedent) — a confirmed, previously-real bug:
// ClientHandle used to silently drop the mismatched attachment with
// ZERO error or panic anywhere.
//
// Got holds the raw mismatched value itself; format it via %T (or
// reflect.TypeOf(Got)) to show its concrete type — never its contents.
// Name is populated when extractable (bm was the right CLASS, just the
// wrong Req) via a Req-free name-only interface assertion, empty
// otherwise.
type BoundMiddlewareReqMismatchError struct {
	Route string
	Got   any
	Name  string
}

func (e BoundMiddlewareReqMismatchError) Error() string {
	if e.Name != "" {
		// Name being populated only means Got satisfies the Req-free
		// [boundNamed] interface (which a plain, never-bound-attachable
		// [Middleware][In,Out] ALSO satisfies) — it does NOT mean Got was
		// the right CLASS, just the wrong Req. The wording below
		// therefore covers BOTH possibilities rather than asserting a
		// Req mismatch specifically (a confirmed, previously-misleading
		// wording — see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's review
		// findings).
		return fmt.Sprintf("api/rest: route %q: middleware %q: not attachable via HandleBoundMW/ClientBoundMW here — either its Req type parameter doesn't match this route's own, or it isn't a BoundMiddleware/BoundClientMiddleware value at all (got %T)", e.Route, e.Name, e.Got)
	}
	return fmt.Sprintf("api/rest: route %q: HandleBoundMW/ClientBoundMW requires a BoundMiddleware/BoundClientMiddleware value matching this route's Req (got %T)", e.Route, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BoundMiddlewareReqMismatchError) LogValue() slog.Value {
	// Got may be nil (e.g. HandleBoundMW(nil)) — reflect.TypeOf(nil)
	// returns a nil reflect.Type, and calling .String() on it panics;
	// confirmed via a real crash this guard fixes (see
	// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's review findings).
	gotType := "<nil>"
	if e.Got != nil {
		gotType = reflect.TypeOf(e.Got).String()
	}
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("name", e.Name),
		slog.String("got_type", gotType),
	)
}

// MiddlewareMisattachedError is returned (via rb.buildErr) when a
// codec-backed [Middleware][In, Out] or [BoundMiddleware][Req, In, Out]/
// [BoundClientMiddleware][Req, In, Out] value is passed to [Route.HandleMW]/
// [Route.ClientMW] (or their SSERoute equivalents) instead of its OWN
// dedicated attachment point — [Middleware] attaches ONLY via .Use();
// [BoundMiddleware]/[BoundClientMiddleware] attach ONLY via
// [Route.HandleBoundMW]/[Route.ClientBoundMW]. HandleMW/ClientMW are
// reserved for the general-purpose (mw == nil) decorator case and the
// bare legacy [middleware.Middleware] type — this error enforces that
// split structurally, closing the legacy raw-adapter-Fn-pairing escape
// hatch for good (see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's
// Motivation).
type MiddlewareMisattachedError struct {
	Route string
	Name  string
}

func (e MiddlewareMisattachedError) Error() string {
	return fmt.Sprintf("api/rest: route %q: middleware %q: a codec-backed Middleware must be attached via .Use() (reusable) or HandleBoundMW/ClientBoundMW (bound) — HandleMW/ClientMW no longer accept it", e.Route, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareMisattachedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("name", e.Name),
	)
}
