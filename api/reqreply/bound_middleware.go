package reqreply

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// BoundMiddleware is the route-BOUND counterpart to [Middleware] — its Fn
// is EMBEDDED AT CONSTRUCTION (via [NewBoundMiddleware]/
// [BoundSecurityMiddleware]), never supplied separately later, and
// additionally receives the attaching route's own decoded *Req value —
// for middleware logic that genuinely needs to read/write the route's
// own request struct (not just a topic/property merge field), e.g. an
// in-payload credential field on a transport with no property side
// channel at all (zeromq). See docs/design/d-0003-codec-declared-middlewares.md's Addendum 7
// for the full design this type implements (reqreply's Phase C).
//
// Attach via [Route.HandleBoundMW] — NEVER via plain .Use()
// (BoundMiddleware deliberately does NOT satisfy
// [routeMiddlewareContributor]'s agnostic path; see the INTERNAL LAYOUT
// note below for why).
//
// A Security-carrying BoundMiddleware (server) and a
// [BoundClientMiddleware] (client) for the SAME scheme name CANNOT be
// attached to ONE shared route value — reqreply's [Route][Req, Resp] is
// ONE shared value carrying BOTH server (HandleMW/HandleBoundMW) and
// client (ClientMW/ClientBoundMW) attachments, EXACTLY like REST's own
// [Route] (NOT independent per-role values the way
// `events.Subscriber`/`events.Publisher` are). [HandleBoundMW]/
// [ClientBoundMW] each independently contribute a spec entry under the
// scheme's Declaration Name, and D6(b)'s name-uniqueness check rejects
// the resulting duplicate with [DuplicateMiddlewareNameError] when both
// are attached for the SAME scheme on the SAME route value. Build TWO
// SEPARATE route values instead — one per role — exactly like REST's own
// examples do (see e.g. examples/adapters-sse's securedServerMw/
// securedClientMw split).
//
// INTERNAL LAYOUT — a NAMED field, not an embedded one, by design: mw
// holds a [Middleware][In, Out]-shaped merge-field/Declaration value
// giving BoundMiddleware the EXACT SAME topic/property merge-field
// vocabulary as [Middleware] for free, letting
// [BoundMiddleware.applyBoundRoute] call the EXISTING
// [buildMiddlewareHandlerAny]/[boundSpecContributionOf] helpers
// UNCHANGED, passing mw. Using a NAMED field (never anonymous/embedded)
// is DELIBERATE: Go promotes ALL methods of an embedded field, which
// would silently promote [Middleware.applyAgnosticRoute] onto
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
// construction, attached via [Route.HandleBoundMW] (never .Use()/
// HandleMW).
//
// **`Out` MUST carry a field literally named `GrantedScopes
// map[string][]string`**, read by the adapter via reflection and merged
// into the SAME [middleware.CheckScopes] call every Security attachment
// uses — REQUIRED even when zero specific scopes are declared. An
// `Out{}` zero value (nil map) means NOTHING satisfies the scheme at
// all, silently turning an otherwise-successful Fn into a REJECTION.
// Unlike `api/events`' own bound-class constructor, reqreply's
// [Middleware.WithReceive] (the REUSABLE class) can ALSO populate this
// convention normally (it already returns `(Out, error)`, symmetric with
// REST — confirmed NO structural asymmetry exists here) — use THIS bound
// constructor only when the Fn genuinely needs `*Req` access (an
// in-payload credential field, no property channel available), not as
// the only way to satisfy a declared Security requirement.
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

// WithRequestTopic mirrors [Middleware.WithRequestTopic] — a one-line
// forwarder onto the named mw field's own existing method.
func (m BoundMiddleware[Req, In, Out]) WithRequestTopic(p MergedTopicParam[In]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestTopic(p)
	return m
}

// WithResponseTopic mirrors [Middleware.WithResponseTopic].
func (m BoundMiddleware[Req, In, Out]) WithResponseTopic(p MergedTopicParam[Out]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseTopic(p)
	return m
}

// WithRequestProperty mirrors [Middleware.WithRequestProperty].
func (m BoundMiddleware[Req, In, Out]) WithRequestProperty(p MergedPropertyParam[In]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestProperty(p)
	return m
}

// WithResponseProperty mirrors [Middleware.WithResponseProperty].
func (m BoundMiddleware[Req, In, Out]) WithResponseProperty(p MergedPropertyParam[Out]) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseProperty(p)
	return m
}

// WithRequestPropertySpec mirrors [Middleware.WithRequestPropertySpec].
func (m BoundMiddleware[Req, In, Out]) WithRequestPropertySpec(p PropertyParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestPropertySpec(p)
	return m
}

// WithResponsePropertySpec mirrors [Middleware.WithResponsePropertySpec].
func (m BoundMiddleware[Req, In, Out]) WithResponsePropertySpec(p PropertyParam) BoundMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponsePropertySpec(p)
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

// applyBoundRoute satisfies [boundContributor][Req] — BoundMiddleware's
// ONLY attach path. Calls the EXISTING, UNCHANGED
// buildMiddlewareHandlerAny/boundSpecContributionOf helpers with the
// named mw field. Additionally synthesizes a legacy-shaped
// middleware.Middleware{Name, Security} entry into rb.middlewares (when
// mw carries a Security declaration) so [applySecurityDeclarations]
// renders the scheme into the spec with ZERO changes to that function —
// REQUIRED because, unlike the (now-removed) reflection-era mechanism
// (which always paired a bound HandleMW attachment with a SEPARATE
// .Use(mw) call to populate rb.middlewares), BoundMiddleware's whole
// point is ONE call doing both: there is no separate .Use() step anymore.
func (m BoundMiddleware[Req, In, Out]) applyBoundRoute(rb *routeBuilder) {
	rb.middlewareHandlers = append(rb.middlewareHandlers, buildMiddlewareHandlerAny(m.mw, m.fn))
	rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, boundSpecContributionOf(m.mw))
	if sec := m.mw.SecurityDeclaration(); sec != nil {
		rb.middlewares = append(rb.middlewares, middleware.Middleware{Name: m.mw.MiddlewareName(), Security: sec})
	}
}

// boundReqWitness satisfies [boundContributor]'s type-level witness —
// never called; see that interface's doc comment.
func (m BoundMiddleware[Req, In, Out]) boundReqWitness(Req) {}

// BoundClientMiddleware is [BoundMiddleware]'s SENDING-role sibling,
// attached via [Route.ClientBoundMW]. Fn shape differs (Req BY VALUE,
// matching ClientMW's existing bound-shape convention): func(ctx, req
// Req) (In, error). Same named-field layout as [BoundMiddleware], same
// rationale — including the SAME "cannot share one route value with a
// same-scheme [BoundMiddleware]" constraint documented on
// [BoundMiddleware]'s own doc comment; build two separate route values
// instead.
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

// WithRequestTopic mirrors [BoundMiddleware.WithRequestTopic].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestTopic(p MergedTopicParam[In]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestTopic(p)
	return m
}

// WithResponseTopic mirrors [BoundMiddleware.WithResponseTopic].
func (m BoundClientMiddleware[Req, In, Out]) WithResponseTopic(p MergedTopicParam[Out]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseTopic(p)
	return m
}

// WithRequestProperty mirrors [BoundMiddleware.WithRequestProperty].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestProperty(p MergedPropertyParam[In]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestProperty(p)
	return m
}

// WithResponseProperty mirrors [BoundMiddleware.WithResponseProperty].
func (m BoundClientMiddleware[Req, In, Out]) WithResponseProperty(p MergedPropertyParam[Out]) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponseProperty(p)
	return m
}

// WithRequestPropertySpec mirrors [BoundMiddleware.WithRequestPropertySpec].
func (m BoundClientMiddleware[Req, In, Out]) WithRequestPropertySpec(p PropertyParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithRequestPropertySpec(p)
	return m
}

// WithResponsePropertySpec mirrors [BoundMiddleware.WithResponsePropertySpec].
func (m BoundClientMiddleware[Req, In, Out]) WithResponsePropertySpec(p PropertyParam) BoundClientMiddleware[Req, In, Out] {
	m.mw = m.mw.WithResponsePropertySpec(p)
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

// applyBoundClientRoute satisfies [boundClientContributor][Req] —
// BoundClientMiddleware's ONLY attach path. See
// [BoundMiddleware.applyBoundRoute]'s identical rb.middlewares-synthesis
// rationale.
func (m BoundClientMiddleware[Req, In, Out]) applyBoundClientRoute(rb *routeBuilder) {
	rb.clientMiddlewareHandlers = append(rb.clientMiddlewareHandlers, buildClientMiddlewareHandlerAny(m.mw, m.fn))
	rb.middlewareSpecContributions = append(rb.middlewareSpecContributions, boundSpecContributionOf(m.mw))
	if sec := m.mw.SecurityDeclaration(); sec != nil {
		rb.middlewares = append(rb.middlewares, middleware.Middleware{Name: m.mw.MiddlewareName(), Security: sec})
	}
}

// boundReqWitness satisfies [boundClientContributor]'s type-level
// witness — never called; see [BoundMiddleware.boundReqWitness]'s
// identical rationale.
func (m BoundClientMiddleware[Req, In, Out]) boundReqWitness(Req) {}

// boundContributor is Req-parameterized — [BoundMiddleware][Req, ...]
// satisfies it FOR ITS OWN Req only. Go's own generic interface
// satisfaction does the matching; no Fn-shape reflection anywhere.
//
// boundReqWitness is a DELIBERATE, never-called no-op method whose SOLE
// purpose is making Req appear in a method SIGNATURE — without it,
// applyBoundRoute's signature (func(rb *routeBuilder)) never mentions
// Req at all, so EVERY BoundMiddleware[X,...] would satisfy
// boundContributor[Y] for ANY X, Y (confirmed via REST's own Phase A
// implementation, found there only via a failing test — baked in here
// from the start instead).
type boundContributor[Req any] interface {
	applyBoundRoute(rb *routeBuilder)
	boundReqWitness(Req)
}

// boundClientContributor is [boundContributor]'s sending-role mirror —
// [BoundClientMiddleware][Req, ...] satisfies it for its own Req only.
type boundClientContributor[Req any] interface {
	applyBoundClientRoute(rb *routeBuilder)
	boundReqWitness(Req)
}

// boundNamed is a Req-FREE interface a bound middleware's name can be
// extracted through even when it's the WRONG Req (so
// [BoundMiddlewareReqMismatchError]'s message can still name the
// middleware, when possible).
type boundNamed interface {
	MiddlewareName() string
}

// boundHandleMWOpt is the [RouteOpt] returned by [Route.HandleBoundMW]
// when bm's Req matched the route's own. fn is already a closure over
// the concrete, Req-confirmed bm value (bound at the SUCCESSFUL
// type-assertion call site, before any type-erasure) — RouteOpt itself
// cannot be generic over Req, so by the time this opt is constructed,
// the Req match has ALREADY been verified; nothing further needs Req at
// this point.
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
	fn   func(rb *routeBuilder)
}

func (o boundHandleMWOpt) applyRoute(rb *routeBuilder) { o.fn(rb) }

// boundClientAttachOpt mirrors boundHandleMWOpt for the client/sending
// role — see that type's doc comment for why name is captured here.
type boundClientAttachOpt struct {
	name string
	fn   func(rb *routeBuilder)
}

func (o boundClientAttachOpt) applyRoute(rb *routeBuilder) { o.fn(rb) }

// boundNameOf extracts bm's name via the Req-free [boundNamed] interface,
// returning "" when bm doesn't implement it — used at HandleBoundMW/
// ClientBoundMW construction time to populate boundHandleMWOpt/
// boundClientAttachOpt's own name field.
func boundNameOf(bm any) string {
	if n, ok := bm.(boundNamed); ok {
		return n.MiddlewareName()
	}
	return ""
}

// boundMismatchOpt is the [RouteOpt] returned by [Route.HandleBoundMW]/
// [Route.ClientBoundMW] when bm's concrete Req did NOT match the route's
// own (or bm wasn't a bound-middleware value at all) — stashes a
// build-time error onto rb, surfaced early by Register/RegisterHandle,
// mirroring InvalidTopicError's own existing early-return pattern.
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
		r.opts = append(slices.Clone(r.opts), boundHandleMWOpt{name: boundNameOf(bm), fn: v.applyBoundRoute})
		return r
	}
	r.opts = append(slices.Clone(r.opts), boundMismatchOpt{route: r.topic, got: bm})
	return r
}

// ClientBoundMW attaches bm — a [BoundClientMiddleware][Req, In, Out]
// value whose Req matches this route's own — mirroring
// [Route.HandleBoundMW] for the sending role.
func (r Route[Req, Resp]) ClientBoundMW(bm any) Route[Req, Resp] {
	if v, ok := bm.(boundClientContributor[Req]); ok {
		r.opts = append(slices.Clone(r.opts), boundClientAttachOpt{name: boundNameOf(bm), fn: v.applyBoundClientRoute})
		return r
	}
	r.opts = append(slices.Clone(r.opts), boundMismatchOpt{route: r.topic, got: bm})
	return r
}

// BoundMiddlewareReqMismatchError is returned (via rb.buildErr) when
// [Route.HandleBoundMW]/[Route.ClientBoundMW] is called with a value
// whose concrete Req does not match the route's own — or with a value
// that isn't a bound-middleware at all (e.g. a plain [Middleware] value,
// which deliberately has NO bound attachment path anymore). Surfaced as
// a normal error at Register/RegisterHandle time; [Route.ClientHandle]
// has no error return at all, so it instead PANICS with this error's
// message (matching that method's own existing [FormatOptError] panic
// precedent) — mirrors REST's identical confirmed fix (`ClientHandle`
// used to silently drop the mismatched attachment with ZERO error or
// panic anywhere).
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
		return fmt.Sprintf("api/reqreply: route %q: middleware %q: not attachable via HandleBoundMW/ClientBoundMW here — either its Req type parameter doesn't match this route's own, or it isn't a BoundMiddleware/BoundClientMiddleware value at all (got %T)", e.Route, e.Name, e.Got)
	}
	return fmt.Sprintf("api/reqreply: route %q: HandleBoundMW/ClientBoundMW requires a BoundMiddleware/BoundClientMiddleware value matching this route's Req (got %T)", e.Route, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BoundMiddlewareReqMismatchError) LogValue() slog.Value {
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
// [Route.ClientMW] instead of its OWN dedicated attachment point —
// [Middleware] attaches ONLY via .Use(); [BoundMiddleware]/
// [BoundClientMiddleware] attach ONLY via [Route.HandleBoundMW]/
// [Route.ClientBoundMW]. HandleMW/ClientMW are reserved for the
// general-purpose (mw == nil) decorator case and the bare legacy
// [middleware.Middleware] type — this error enforces that split
// structurally, closing the legacy raw-adapter-Fn-pairing escape hatch
// for good (see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Motivation).
type MiddlewareMisattachedError struct {
	Route string
	Name  string
}

func (e MiddlewareMisattachedError) Error() string {
	return fmt.Sprintf("api/reqreply: route %q: middleware %q: a codec-backed Middleware must be attached via .Use() (reusable) or HandleBoundMW/ClientBoundMW (bound) — HandleMW/ClientMW no longer accept it", e.Route, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareMisattachedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("name", e.Name),
	)
}

// LegacySecurityMWRemovedError is returned (via rb.buildErr) when a
// legacy [middleware.Middleware] carrying a Security declaration (built
// via [middleware.SecurityScheme]/[FromSecurityScheme]) is passed to
// [Route.HandleMW]/[Route.ClientMW]. Retired per
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 8: a full-repo search
// found zero uses of this mechanism's one distinguishing feature — a
// single declared scheme shared, by VALUE, across REST/events/reqreply —
// so security-scheme declaration is now a concrete, per-api-layer concern
// exclusively. Declare and implement a security scheme via
// [Route.HandleBoundMW]/[Route.ClientBoundMW] +
// [BoundSecurityMiddleware]/[BoundSecurityClientMiddleware] instead — it
// embeds the Security declaration directly (no separate .Use() call
// needed). HandleMW/ClientMW's GENERAL-PURPOSE (non-security) use is
// UNCHANGED — this error fires only for a Security-carrying mw.
type LegacySecurityMWRemovedError struct {
	Route string
	Name  string
	Op    string // "HandleMW" or "ClientMW"
}

func (e LegacySecurityMWRemovedError) Error() string {
	boundOp, boundCtor := "HandleBoundMW", "BoundSecurityMiddleware"
	if e.Op == "ClientMW" {
		boundOp, boundCtor = "ClientBoundMW", "BoundSecurityClientMiddleware"
	}
	return fmt.Sprintf("api/reqreply: route %q: middleware %q: a Security-carrying middleware.Middleware can no longer be attached via %s — use %s with %s instead", e.Route, e.Name, e.Op, boundOp, boundCtor)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e LegacySecurityMWRemovedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("route", e.Route),
		slog.String("name", e.Name),
		slog.String("op", e.Op),
	)
}
