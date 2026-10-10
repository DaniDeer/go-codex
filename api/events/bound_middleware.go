package events

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// BoundSubscribeMiddleware is the channel-BOUND counterpart to
// [Middleware] for the SUBSCRIBE (RECEIVING) role — its Fn is EMBEDDED AT
// CONSTRUCTION (via [NewBoundSubscribeMiddleware]/
// [BoundSecuritySubscribeMiddleware]), never supplied separately later,
// and additionally receives the attaching channel's own decoded *T value
// — for middleware logic that genuinely needs to read the channel's own
// message struct (not just a topic/property merge field). See
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 for the full design this type
// implements (events' Phase B).
//
// Attach via [Subscriber.SubscribeBoundMW] — NEVER via plain .Use()
// (BoundSubscribeMiddleware deliberately does NOT satisfy
// [eventsMiddlewareContributor]'s agnostic path; see the INTERNAL LAYOUT
// note below for why).
//
// Unlike the reusable class's [Middleware.WithReceive] (which has NO Out
// return at all — see [Middleware]'s own doc comment for this confirmed,
// genuine asymmetry), BoundSubscribeMiddleware's Fn ALWAYS returns
// `(Out, error)` — uniform with REST's bound shape — so a
// Security-carrying attachment needing to return `GrantedScopes` has
// somewhere to put it; a pure-enrichment attachment with nothing to
// return just declares `Out = struct{}`.
//
// A Security-carrying BoundSubscribeMiddleware (subscribe) and a
// [BoundPublishMiddleware] (publish) for the SAME scheme name CANNOT be
// attached to one shared value either way — they are independent
// per-role attachments on independent [Subscriber]/[Publisher] values to
// begin with (unlike REST, where this is a route-level constraint to call
// out) — so this is not a comparable concern here.
//
// INTERNAL LAYOUT — a NAMED field, not an embedded one, by design: mw
// holds a [Middleware][In, Out]-shaped merge-field/Declaration value
// giving BoundSubscribeMiddleware the EXACT SAME topic/property
// merge-field vocabulary as [Middleware] for free, letting
// [BoundSubscribeMiddleware.ApplyBoundRoute] call the EXISTING
// [buildMiddlewareHandlerAny] helper UNCHANGED, passing mw. Using a NAMED
// field (never anonymous/embedded) is DELIBERATE: Go promotes ALL methods
// of an embedded field, which would silently promote
// [Middleware.applyAgnosticSubscriber] onto BoundSubscribeMiddleware too
// — making it accidentally satisfy [eventsMiddlewareContributor] and
// attachable via plain .Use(), exactly the bound/reusable ambiguity this
// type exists to eliminate (see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's
// REST "Finding A" for the full rationale, carried forward identically
// here).
//
// Only exposes the SUBSCRIBE (In-decoding) half of [Middleware]'s merge-
// field vocabulary — WithSubscribeTopic/WithSubscribeProperty/
// WithSubscribePropertySpec/SetContextFieldFromIn — since a
// BoundSubscribeMiddleware's Fn never encodes an outgoing Out (Subscribe's
// Out, when HasOut-equivalent behavior is needed, is validated only, never
// wire-encoded — see [MiddlewareHandler.ValidateOut]).
type BoundSubscribeMiddleware[T, In, Out any] struct {
	mw Middleware[In, Out] // NAMED, not embedded — see doc comment above
	fn func(ctx context.Context, msg *T, in In) (Out, error)
}

// NewBoundSubscribeMiddleware builds a [BoundSubscribeMiddleware] from a
// [Declaration] and its Fn — fn's shape is checked by the
// ordinary Go compiler at this call, zero reflection needed to verify
// arity/types (contrast with the now-removed isBoundSubscribeMWShape/
// isBoundSubscribeMWShapeWithOut runtime detectors).
func NewBoundSubscribeMiddleware[T, In, Out any](
	decl Declaration[In, Out],
	fn func(ctx context.Context, msg *T, in In) (Out, error),
) BoundSubscribeMiddleware[T, In, Out] {
	return BoundSubscribeMiddleware[T, In, Out]{mw: NewMiddleware[In, Out](decl), fn: fn}
}

// BoundSecuritySubscribeMiddleware mirrors [SecurityMiddleware]'s role
// for the bound class — a Security-carrying bound subscribe middleware,
// fn embedded at construction.
//
// **`Out` MUST carry a field literally named `GrantedScopes
// map[string][]string`**, read by the adapter via reflection and merged
// into the SAME [CheckScopes] call every Security attachment
// uses — REQUIRED even when zero specific scopes are declared. An
// `Out{}` zero value (nil map) means NOTHING satisfies the scheme at
// all, silently turning an otherwise-successful Fn into a REJECTION. See
// [SecurityMiddleware]'s own doc comment for the full writeup of this
// convention (shared identically across REST/events/reqreply).
//
// **This constructor is the ONLY way to satisfy a declared
// `Subscribe.Security` requirement on events' subscribe side** —
// [SecurityMiddleware] (the reusable class, attached via `.Use()`) can
// NEVER populate `GrantedScopes` at all, since [Middleware.WithReceive]'s
// Fn has no Out return — see that method's own doc comment for the full
// rationale. Use [SecurityMiddleware] only when `Subscribe.Security` is
// left undeclared (an unpaired presence/validity check); use THIS
// constructor whenever the channel declares `Subscribe.Security`.
func BoundSecuritySubscribeMiddleware[T, In, Out any](
	schemeName string, scheme SecurityScheme, scopes []string,
	fn func(ctx context.Context, msg *T, in In) (Out, error),
) BoundSubscribeMiddleware[T, In, Out] {
	return BoundSubscribeMiddleware[T, In, Out]{
		mw: NewMiddleware[In, Out](Declaration[In, Out]{
			Name:     "declare-security:" + schemeName,
			InCodec:  codex.Struct[In](),
			OutCodec: codex.Struct[Out](),
			Security: NewSecurityDeclaration(schemeName, scheme, scopes),
		}),
		fn: fn,
	}
}

// WithSubscribeTopic mirrors [Middleware.WithSubscribeTopic] — a one-line
// forwarder onto the named mw field's own existing method.
func (m BoundSubscribeMiddleware[T, In, Out]) WithSubscribeTopic(p MergedTopicParam[In]) BoundSubscribeMiddleware[T, In, Out] {
	m.mw = m.mw.WithSubscribeTopic(p)
	return m
}

// WithSubscribeProperty mirrors [Middleware.WithSubscribeProperty].
func (m BoundSubscribeMiddleware[T, In, Out]) WithSubscribeProperty(p MergedPropertyParam[In]) BoundSubscribeMiddleware[T, In, Out] {
	m.mw = m.mw.WithSubscribeProperty(p)
	return m
}

// WithSubscribePropertySpec mirrors [Middleware.WithSubscribePropertySpec].
func (m BoundSubscribeMiddleware[T, In, Out]) WithSubscribePropertySpec(p PropertyParam) BoundSubscribeMiddleware[T, In, Out] {
	m.mw = m.mw.WithSubscribePropertySpec(p)
	return m
}

// SetContextFieldFromIn mirrors [Middleware.SetContextFieldFromIn].
func (m BoundSubscribeMiddleware[T, In, Out]) SetContextFieldFromIn(field ContextFieldSetter, get func(In) any) BoundSubscribeMiddleware[T, In, Out] {
	m.mw = m.mw.SetContextFieldFromIn(field, get)
	return m
}

// MiddlewareName lets callers extract a human-readable name even when a
// T mismatch means [boundContributor] itself can't be asserted — used by
// [Subscriber.SubscribeBoundMW]'s error-message enrichment.
func (m BoundSubscribeMiddleware[T, In, Out]) MiddlewareName() string { return m.mw.MiddlewareName() }

// ApplyBoundRoute satisfies [boundContributor][T] — BoundSubscribeMiddleware's
// ONLY attach path. Calls the EXISTING, UNCHANGED [buildMiddlewareHandlerAny]
// helper with the named mw field, then sets HasOut/ValidateOut directly
// (BoundSubscribeMiddleware ALWAYS carries an Out-returning Fn — see this
// type's own doc comment), exactly as the (now-removed)
// isBoundSubscribeMWShapeWithOut-detected case did. Mutates s directly —
// events' [Subscriber[T]] has no opts-deferral pattern to go through (see
// [Subscriber.SubscribeBoundMW]'s own doc comment).
//
// Subscriber.SubscribeBoundMW's own type-asserted interface value —
// staticcheck's U1000 cannot trace a generic interface's method called
// through a runtime type assertion; confirmed via REAL dispatch in
// TestSubscribeBoundMW_StackedWithUse_BothDispatch and every migrated
// events example, not dead code)
//
//lint:ignore U1000 implements boundContributor interface (dispatched via
func (m BoundSubscribeMiddleware[T, In, Out]) ApplyBoundRoute(rb middleware.BoundRouteBuilder) {
	h := buildMiddlewareHandlerAny(m.mw, m.fn)
	h.HasOut = true
	h.ValidateOut = func(out any) error {
		o, _ := out.(Out)
		return m.mw.OutCodec.Validate(o)
	}
	rb.AppendMiddlewareHandler(h)
	// A Security-carrying attachment must also land in s.mws — the ONLY
	// place [applyEventsSecurityDeclarations] (called from
	// [buildChannelHandle]) reads to populate
	// [ChannelHandle.Descriptor.Subscribe.Security]/[ChannelHandle.SecuritySchemes].
	// Without this, a Bound-only Security attachment (no companion .Use()
	// call) silently enforces scopes at dispatch time (middlewareHandlers
	// IS populated) while the published AsyncAPI spec omits the
	// requirement entirely — mirrors [rest.BoundMiddleware.ApplyBoundRoute]/
	// [reqreply.BoundMiddleware.ApplyBoundRoute]'s identical, existing line.
	if sec := m.mw.SecurityDeclaration(); sec != nil {
		rb.AppendSecurityDeclaration(m.mw.MiddlewareName(), sec)
	}
}

// BoundReqWitness satisfies [boundContributor]'s type-level witness —
// never called; see that interface's doc comment. The purpose is making
// T appear in a method SIGNATURE — without it, ApplyBoundRoute's
// signature never mentions T at all, so EVERY BoundSubscribeMiddleware[X,...]
// would satisfy boundContributor[Y] for ANY X, Y (the exact bug Phase A's
// REST implementation found and fixed via this SAME technique — carried
// forward here from the start, not discovered via a failing test).
//
// ApplyBoundRoute above — required by the interface, invisible to
// static reachability analysis through a runtime type assertion)
//
//lint:ignore U1000 implements boundContributor interface (same reason as
func (m BoundSubscribeMiddleware[T, In, Out]) BoundReqWitness(T) {}

// BoundPublishMiddleware is [BoundSubscribeMiddleware]'s PUBLISH (SENDING)
// role sibling, attached via [Publisher.PublishBoundMW]. Fn shape differs
// (T BY VALUE, matching [Publisher.PublishMW]'s existing bound-shape
// convention): func(ctx, msg T) (Out, error). Same named-field layout as
// [BoundSubscribeMiddleware], same rationale. Only exposes the PUBLISH
// (Out-encoding) half of [Middleware]'s merge-field vocabulary —
// WithPublishTopic/WithPublishProperty/WithPublishPropertySpec/
// SetContextFieldFromOut.
type BoundPublishMiddleware[T, In, Out any] struct {
	mw Middleware[In, Out] // NAMED, not embedded — same rationale as BoundSubscribeMiddleware
	fn func(ctx context.Context, msg T) (Out, error)
}

// NewBoundPublishMiddleware builds a [BoundPublishMiddleware] from a
// [Declaration] and its Fn.
func NewBoundPublishMiddleware[T, In, Out any](
	decl Declaration[In, Out],
	fn func(ctx context.Context, msg T) (Out, error),
) BoundPublishMiddleware[T, In, Out] {
	return BoundPublishMiddleware[T, In, Out]{mw: NewMiddleware[In, Out](decl), fn: fn}
}

// BoundSecurityPublishMiddleware mirrors [BoundSecuritySubscribeMiddleware]
// for the publish/sending role.
func BoundSecurityPublishMiddleware[T, In, Out any](
	schemeName string, scheme SecurityScheme, scopes []string,
	fn func(ctx context.Context, msg T) (Out, error),
) BoundPublishMiddleware[T, In, Out] {
	return BoundPublishMiddleware[T, In, Out]{
		mw: NewMiddleware[In, Out](Declaration[In, Out]{
			Name:     "declare-security:" + schemeName,
			InCodec:  codex.Struct[In](),
			OutCodec: codex.Struct[Out](),
			Security: NewSecurityDeclaration(schemeName, scheme, scopes),
		}),
		fn: fn,
	}
}

// WithPublishTopic mirrors [Middleware.WithPublishTopic].
func (m BoundPublishMiddleware[T, In, Out]) WithPublishTopic(p MergedTopicParam[Out]) BoundPublishMiddleware[T, In, Out] {
	m.mw = m.mw.WithPublishTopic(p)
	return m
}

// WithPublishProperty mirrors [Middleware.WithPublishProperty].
func (m BoundPublishMiddleware[T, In, Out]) WithPublishProperty(p MergedPropertyParam[Out]) BoundPublishMiddleware[T, In, Out] {
	m.mw = m.mw.WithPublishProperty(p)
	return m
}

// WithPublishPropertySpec mirrors [Middleware.WithPublishPropertySpec].
func (m BoundPublishMiddleware[T, In, Out]) WithPublishPropertySpec(p PropertyParam) BoundPublishMiddleware[T, In, Out] {
	m.mw = m.mw.WithPublishPropertySpec(p)
	return m
}

// SetContextFieldFromOut mirrors [Middleware.SetContextFieldFromOut].
func (m BoundPublishMiddleware[T, In, Out]) SetContextFieldFromOut(field ContextFieldSetter, get func(Out) any) BoundPublishMiddleware[T, In, Out] {
	m.mw = m.mw.SetContextFieldFromOut(field, get)
	return m
}

// MiddlewareName mirrors [BoundSubscribeMiddleware.MiddlewareName].
func (m BoundPublishMiddleware[T, In, Out]) MiddlewareName() string { return m.mw.MiddlewareName() }

// ApplyBoundClientRoute satisfies [boundClientContributor][T] —
// BoundPublishMiddleware's ONLY attach path. rb is the narrow
// [middleware.BoundRouteBuilder] interface — see
// [BoundSubscribeMiddleware.ApplyBoundRoute]'s identical rationale.
//
// reason as BoundSubscribeMiddleware.ApplyBoundRoute above)
//
//lint:ignore U1000 implements boundClientContributor interface (same
func (m BoundPublishMiddleware[T, In, Out]) ApplyBoundClientRoute(rb middleware.BoundRouteBuilder) {
	rb.AppendClientMiddlewareHandler(buildClientMiddlewareHandlerAny(m.mw, m.fn))
	// See [BoundSubscribeMiddleware.ApplyBoundRoute]'s identical
	// rationale — without this, Publish.Security/SecuritySchemes is
	// silently left unpopulated for a Bound-only Security attachment.
	if sec := m.mw.SecurityDeclaration(); sec != nil {
		rb.AppendSecurityDeclaration(m.mw.MiddlewareName(), sec)
	}
}

// BoundReqWitness satisfies [boundClientContributor]'s type-level
// witness — never called; see [BoundSubscribeMiddleware.BoundReqWitness]'s
// identical rationale.
//
// reason as BoundSubscribeMiddleware.BoundReqWitness above)
//
//lint:ignore U1000 implements boundClientContributor interface (same
func (m BoundPublishMiddleware[T, In, Out]) BoundReqWitness(T) {}

// boundContributor/boundClientContributor/boundNamed are internal
// aliases for the shared, EXPORTED [middleware.BoundContributor]/
// [middleware.BoundClientContributor]/[middleware.BoundNamed] —
// consolidated per docs/design/d-0009-internalize-shared-mechanics.md's Phase 2
// (kept under their pre-consolidation unexported names so every existing
// call site in this file keeps compiling unchanged). [BoundSubscribeMiddleware]/
// [BoundPublishMiddleware] satisfy them for their own T only — Go's own
// generic interface satisfaction does the matching; no Fn-shape
// reflection anywhere. See [middleware.BoundContributor]'s own doc
// comment for the BoundReqWitness discriminator-trick rationale (ported
// directly from REST's Phase A implementation, found there via an actual
// failing test — applied here from the start).
//
// ApplyBoundRoute/ApplyBoundClientRoute take the narrow
// [middleware.BoundRouteBuilder] interface, implemented below by
// *Subscriber[T]/*Publisher[T] via pointer receiver — events' Subscriber/
// Publisher have no routeBuilder-style opts-deferral pattern (unlike
// REST's [Route[Req,Resp]]), so the interface's methods mutate the
// pointee DIRECTLY — see [Subscriber.SubscribeBoundMW]'s own doc comment
// for the full rationale.
type boundContributor[T any] = middleware.BoundContributor[T]
type boundClientContributor[T any] = middleware.BoundClientContributor[T]
type boundNamed = middleware.BoundNamed

// Subscriber[T]'s 2 meaningful [middleware.BoundRouteBuilder] methods
// (pointer receiver — mutates the pointee directly, since Subscriber has
// no routeBuilder-style deferred-opts pattern); AppendClientMiddlewareHandler/
// AppendSpecContribution are no-ops — Subscriber has no client/sending
// role and no spec-contribution concept of its own (see
// [Publisher.AppendSpecContribution]'s doc comment for the latter's full
// rationale).
func (s *Subscriber[T]) AppendMiddlewareHandler(h any) {
	s.middlewareHandlers = append(slices.Clone(s.middlewareHandlers), h.(MiddlewareHandler))
}

func (s *Subscriber[T]) AppendClientMiddlewareHandler(any) {}

func (s *Subscriber[T]) AppendSpecContribution(any) {}

func (s *Subscriber[T]) AppendSecurityDeclaration(name string, sec *SecurityDeclaration) {
	s.mws = append(slices.Clone(s.mws), AttachedMiddleware{Name: name, Security: sec})
}

// Publisher[T]'s [middleware.BoundRouteBuilder] methods — mirrors
// [Subscriber]'s identical rationale. AppendMiddlewareHandler is a no-op
// — Publisher has no server/receiving role.
//
// AppendSpecContribution is a no-op: events has NO spec-contribution
// concept of its own at all (unlike api/rest/api/reqreply's separate
// middlewareSpecContribution list) — events' [MiddlewareHandler]/
// [ClientMiddlewareHandler] already carry every spec-relevant field
// directly (see docs/design/d-0009-internalize-shared-mechanics.md's Phase 2 for
// the full investigation that confirmed this difference is a genuine,
// not-yet-resolved vocabulary gap, not an oversight).
func (p *Publisher[T]) AppendMiddlewareHandler(any) {}

func (p *Publisher[T]) AppendClientMiddlewareHandler(h any) {
	p.clientMiddlewareHandlers = append(slices.Clone(p.clientMiddlewareHandlers), h.(ClientMiddlewareHandler))
}

func (p *Publisher[T]) AppendSpecContribution(any) {}

func (p *Publisher[T]) AppendSecurityDeclaration(name string, sec *SecurityDeclaration) {
	p.mws = append(slices.Clone(p.mws), AttachedMiddleware{Name: name, Security: sec})
}

// BoundMiddlewareReqMismatchError is returned when [Subscriber.SubscribeBoundMW]/
// [Publisher.PublishBoundMW] is called with a value whose concrete T does
// not match the channel's own — or with a value that isn't a
// bound-middleware at all (e.g. a plain [Middleware] value, which
// deliberately has NO bound attachment path anymore).
//
// Got holds the raw mismatched value itself; format it via %T (or
// reflect.TypeOf(Got)) to show its concrete type — never its contents.
// Name is populated when extractable (bm was the right CLASS, just the
// wrong T) via a T-free name-only interface assertion, empty otherwise.
type BoundMiddlewareReqMismatchError struct {
	Topic string
	Got   any
	Name  string
}

func (e BoundMiddlewareReqMismatchError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("api/events: topic %q: middleware %q: not attachable via SubscribeBoundMW/PublishBoundMW here — either its T type parameter doesn't match this channel's own, or it isn't a BoundSubscribeMiddleware/BoundPublishMiddleware value at all (got %T)", e.Topic, e.Name, e.Got)
	}
	return fmt.Sprintf("api/events: topic %q: SubscribeBoundMW/PublishBoundMW requires a BoundSubscribeMiddleware/BoundPublishMiddleware value matching this channel's T (got %T)", e.Topic, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e BoundMiddlewareReqMismatchError) LogValue() slog.Value {
	gotType := "<nil>"
	if e.Got != nil {
		gotType = reflect.TypeOf(e.Got).String()
	}
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("name", e.Name),
		slog.String("got_type", gotType),
	)
}

// MiddlewareMisattachedError is returned when a codec-backed [Middleware][In,
// Out] or [BoundSubscribeMiddleware][T, In, Out]/[BoundPublishMiddleware][T,
// In, Out] value is passed to [Subscriber.SubscribeMW]/[Publisher.PublishMW]
// instead of its OWN dedicated attachment point — [Middleware] attaches
// ONLY via .Use(); [BoundSubscribeMiddleware]/[BoundPublishMiddleware]
// attach ONLY via [Subscriber.SubscribeBoundMW]/[Publisher.PublishBoundMW].
// SubscribeMW/PublishMW are reserved for the general-purpose (mw == nil)
// decorator case and the bare legacy [AttachedMiddleware] type — this
// error enforces that split structurally, closing the legacy raw-adapter-
// Fn-pairing escape hatch for good (see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's
// Motivation).
type MiddlewareMisattachedError struct {
	Topic string
	Name  string
}

func (e MiddlewareMisattachedError) Error() string {
	return fmt.Sprintf("api/events: topic %q: middleware %q: a codec-backed Middleware must be attached via .Use() (reusable) or SubscribeBoundMW/PublishBoundMW (bound) — SubscribeMW/PublishMW no longer accept it", e.Topic, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareMisattachedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("name", e.Name),
	)
}

// SubscribeBoundMW attaches bm — a [BoundSubscribeMiddleware][T, In, Out]
// value whose T matches THIS Subscriber's own T type parameter — giving
// its embedded Fn *T access via the SAME channel-BOUND mechanism as the
// (now-removed) reflection-detected case, now reached through an
// explicit, dedicated method and a concrete, distinct Go type instead of
// Fn-shape guessing.
//
// bm is accepted as `any` because a method cannot introduce a NEW type
// parameter beyond its receiver's own (T, here) — attaching is resolved
// via a Go generic interface assertion against [boundContributor][T],
// instantiated from THIS Subscriber's OWN T, not inferred from bm. A bm
// constructed with the WRONG T (e.g. a [BoundSubscribeMiddleware][Foo,
// ...] attached to a Subscriber[Bar]) is a deliberate-misuse case, caught
// immediately and loudly via [BoundMiddlewareReqMismatchError] at Handle
// time — never silently mis-dispatched. Unlike REST's [Route.HandleBoundMW]
// (which stashes a build-time error for later, since [Route] has no
// opts-deferral-free accumulator), the mismatch is recorded directly on
// s's own buildErr field, checked at the top of [Subscriber.Handle] —
// events has no infallible-by-design sibling method requiring a panic
// workaround (see [Subscriber.Handle]'s own doc comment).
func (s Subscriber[T]) SubscribeBoundMW(bm any) Subscriber[T] {
	if v, ok := bm.(boundContributor[T]); ok {
		v.ApplyBoundRoute(&s)
		return s
	}
	if s.buildErr == nil {
		name := ""
		if n, ok := bm.(boundNamed); ok {
			name = n.MiddlewareName()
		}
		s.buildErr = BoundMiddlewareReqMismatchError{Topic: s.channel.topic, Got: bm, Name: name}
	}
	return s
}

// PublishBoundMW attaches bm — a [BoundPublishMiddleware][T, In, Out]
// value whose T matches this Publisher's own — mirroring
// [Subscriber.SubscribeBoundMW] for the sending role.
func (p Publisher[T]) PublishBoundMW(bm any) Publisher[T] {
	if v, ok := bm.(boundClientContributor[T]); ok {
		v.ApplyBoundClientRoute(&p)
		return p
	}
	if p.buildErr == nil {
		name := ""
		if n, ok := bm.(boundNamed); ok {
			name = n.MiddlewareName()
		}
		p.buildErr = BoundMiddlewareReqMismatchError{Topic: p.channel.topic, Got: bm, Name: name}
	}
	return p
}
