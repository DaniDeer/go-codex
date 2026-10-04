package events

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// Middleware is a codec-backed, events-specific middleware declaration —
// the per-pattern counterpart to [middleware.Declaration], adding events'
// own topic-var merge vocabulary. Unlike [rest.Middleware][In, Out] (which
// has independent request AND response merge vocabularies, since REST has
// header/cookie/query on BOTH sides), events has only ONE wire location
// per role — the topic — and subscribe/publish are asymmetric (subscribe
// has no reply channel to encode a value INTO; publish has no incoming
// message to decode a value FROM). Concretely:
//   - Subscribe (RECEIVING role): In is decoded from incoming topic vars
//     (via topicMergeFieldsIn + Declaration.InCodec) — Out is UNUSED.
//   - Publish (SENDING role): Out is encoded into outgoing topic vars (via
//     topicMergeFieldsOut + Declaration.OutCodec) — In is UNUSED.
//
// Built via [NewMiddleware], populated via [Middleware.WithSubscribeTopic]/
// [Middleware.WithPublishTopic] — REUSING the SAME [NewTopicParam]
// constructor a channel's own Item already uses (generic over any T, not
// hardcoded to a channel's own payload type). No new events-side param
// constructor exists for this.
//
// Middleware supports TWO attachment styles, mirroring [rest.Middleware]
// exactly:
//   - Channel-BOUND: attached via [Transform]/[ClientTransform], whose fn
//     additionally receives the channel's own msg *T/msg T (read/enrich
//     access on subscribe; caller's own already-built value on publish) —
//     for concerns whose fn genuinely needs that access.
//   - Channel-AGNOSTIC: a T-FREE fn bundled directly onto this value via
//     [Middleware.WithReceive]/[Middleware.WithSend], attached via plain
//     .Use(mw) — reusable verbatim across many channels.
//
// A single Middleware value must use EXACTLY ONE style, never both — see
// [AmbiguousMiddlewareAttachmentError].
type Middleware[In, Out any] struct {
	middleware.Declaration[In, Out]

	topicMergeFieldsIn  []codex.FieldCodec[In]
	topicMergeFieldsOut []codex.FieldCodec[Out]

	// propertyMergeFieldsIn/Out mirror topicMergeFieldsIn/Out exactly, for
	// the property vocabulary axis (MQTT5 User Properties/future AMQP
	// message headers) — see [Middleware.WithSubscribeProperty]/
	// [Middleware.WithPublishProperty]. Kept SEPARATE from the topic-var
	// slices (different validation rules — property names are never
	// checked against a topic template).
	propertyMergeFieldsIn  []codex.FieldCodec[In]
	propertyMergeFieldsOut []codex.FieldCodec[Out]

	// propertyParamsIn/Out carry the SAME declarations as
	// propertyMergeFieldsIn/Out in spec-level form (Name/Required/Codec)
	// — needed because [codex.FieldCodec]'s schema-rendering method is
	// package-codex-only (unexported), so conflict-detection/AsyncAPI
	// rendering (both living in api/events) cannot introspect
	// propertyMergeFieldsIn/Out directly. Populated 1:1 alongside the
	// FieldCodec slices by WithSubscribeProperty/WithPublishProperty.
	propertyParamsIn  []PropertyParam
	propertyParamsOut []PropertyParam

	// receiveFn/sendFn, when set (via WithReceive/WithSend below), carry a
	// T-FREE runtime Fn directly on the value itself — enabling
	// channel-AGNOSTIC attachment via plain .Use(mw). Left nil for the
	// channel-BOUND case, where Transform/ClientTransform supply a
	// msg-accessing fn separately instead (never both — combining is
	// rejected as ambiguous via [AmbiguousMiddlewareAttachmentError]).
	//
	// sendFn deliberately returns (Out, error), NOT (In, error) — the
	// roadmap doc's original draft showed WithSend producing In (a
	// leftover mirroring rest.Middleware's own WithSend, not updated for
	// events' In/Out role swap); corrected here for consistency with
	// [ClientTransform]'s own (Out, error)-producing fn shape, since
	// publish's OWN Declaration.OutCodec is what validates and encodes
	// whatever a publish-side Fn produces, regardless of attachment
	// style. See docs/design/d-0003-codec-declared-middlewares.md's
	// "Lessons learned" section.
	receiveFn func(ctx context.Context, in In) error
	sendFn    func(ctx context.Context) (Out, error)

	// ctxFieldsFromIn/ctxFieldsFromOut hold every
	// [Middleware.SetContextFieldFromIn]/[Middleware.SetContextFieldFromOut]
	// registration — mirrors [rest.Middleware]'s identical fields
	// (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase
	// B). ctxFieldsFromIn dispatches from [buildDecodeIn] (Subscribe side
	// ONLY — Publish's Fn has no In parameter at all to source a value
	// from); ctxFieldsFromOut dispatches from [buildEncodeOut] (Publish
	// side ONLY — Subscribe's Fn produces no Out at all, the one
	// confirmed, genuine asymmetry relative to REST/reqreply).
	ctxFieldsFromIn  []contextFieldInSetter[In]
	ctxFieldsFromOut []contextFieldOutSetter[Out]
}

// contextFieldInSetter mirrors [rest]'s identical type — pairs a
// [middleware.ContextFieldSetter] with the getter that extracts its raw
// value from this middleware's decoded In — one entry per
// [Middleware.SetContextFieldFromIn] call.
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
// chain [Middleware.WithSubscribeTopic]/[Middleware.WithPublishTopic] to
// populate its topic merge-field vocabulary, then attach via
// [Transform]/[ClientTransform] (channel-BOUND) or [Middleware.WithReceive]/
// [Middleware.WithSend] + plain .Use(mw) (channel-AGNOSTIC).
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out] {
	return Middleware[In, Out]{Declaration: decl}
}

// WithSubscribeTopic registers one topic-var merge field into mw's OWN In
// vocabulary — reuses [NewTopicParam][In, V] directly (no new events-side
// constructor). Only meaningful for a topic var the channel's OWN template
// declares but that the channel's own Item does NOT already merge via its
// OWN NewTopicParam (e.g. a template "sensors/{sensorID}/{region}/data"
// where the channel merges only sensorID, leaving region for this
// middleware to claim instead) — see
// docs/design/d-0003-codec-declared-middlewares.md's events section for
// the full scope rationale.
func (m Middleware[In, Out]) WithSubscribeTopic(p MergedTopicParam[In]) Middleware[In, Out] {
	m.topicMergeFieldsIn = append(cloneFieldCodecs(m.topicMergeFieldsIn), p.Field)
	return m
}

// WithPublishTopic is [Middleware.WithSubscribeTopic]'s publish-side
// sibling — registers one topic-var merge field into mw's OWN Out
// vocabulary, encoded into the outgoing publish's topic vars once
// [ClientTransform]'s (or a bundled [Middleware.WithSend]'s) fn produces
// an Out value.
func (m Middleware[In, Out]) WithPublishTopic(p MergedTopicParam[Out]) Middleware[In, Out] {
	m.topicMergeFieldsOut = append(cloneFieldCodecs(m.topicMergeFieldsOut), p.Field)
	return m
}

// WithSubscribeProperty registers one property merge field into mw's OWN In
// vocabulary — mirrors [Middleware.WithSubscribeTopic] exactly, using
// [NewPropertyParam][In, V]/[NewOptionalPropertyParam][In, V] instead of
// [NewTopicParam][In, V]. Meaningful ONLY for Subscribe attachment (mirrors
// propertyMergeFieldsIn's own Subscribe-only use).
func (m Middleware[In, Out]) WithSubscribeProperty(p MergedPropertyParam[In]) Middleware[In, Out] {
	m.propertyMergeFieldsIn = append(cloneFieldCodecs(m.propertyMergeFieldsIn), p.Field)
	m.propertyParamsIn = append(cloneParams(m.propertyParamsIn), PropertyParam{Param: p.Param, Required: p.Required})
	return m
}

// WithPublishProperty is [Middleware.WithSubscribeProperty]'s publish-side
// sibling — registers one property merge field into mw's OWN Out
// vocabulary, encoded into the outgoing publish's property vars once
// [ClientTransform]'s (or a bundled [Middleware.WithSend]'s) fn produces an
// Out value. Meaningful ONLY for Publish attachment.
func (m Middleware[In, Out]) WithPublishProperty(p MergedPropertyParam[Out]) Middleware[In, Out] {
	m.propertyMergeFieldsOut = append(cloneFieldCodecs(m.propertyMergeFieldsOut), p.Field)
	m.propertyParamsOut = append(cloneParams(m.propertyParamsOut), PropertyParam{Param: p.Param, Required: p.Required})
	return m
}

// WithSubscribePropertySpec registers a PRESENCE-ONLY (non-merged)
// SUBSCRIBE-side property declaration — p is validated and rendered into
// the channel's AsyncAPI spec, but has NO corresponding In struct field
// to decode into. Use [Middleware.WithSubscribeProperty] instead when a
// merge field is wanted. Mirrors legacy middleware.Middleware's
// RequestHeaderParams shape (see adapters/mqtt5.FromUserPropertyParam).
// Part of the middleware-consolidation effort
// (docs/design/d-0003-codec-declared-middlewares.md) closing the one real gap
// the codec-backed family had relative to the legacy type.
func (m Middleware[In, Out]) WithSubscribePropertySpec(p PropertyParam) Middleware[In, Out] {
	m.propertyParamsIn = append(cloneParams(m.propertyParamsIn), p)
	return m
}

// WithPublishPropertySpec is [Middleware.WithSubscribePropertySpec]'s
// publish-side sibling — mirrors legacy middleware.Middleware's
// ResponseHeaderParams shape (see
// adapters/mqtt5.FromResponseUserPropertyParam).
func (m Middleware[In, Out]) WithPublishPropertySpec(p PropertyParam) Middleware[In, Out] {
	m.propertyParamsOut = append(cloneParams(m.propertyParamsOut), p)
	return m
}

// cloneParams is [cloneFieldCodecs]'s []PropertyParam sibling, avoiding the
// SAME aliasing bug across chained With* calls.
func cloneParams(ps []PropertyParam) []PropertyParam {
	out := make([]PropertyParam, len(ps))
	copy(out, ps)
	return out
}

// cloneFieldCodecs is a tiny generic helper avoiding aliasing bugs across
// chained With* calls — mirrors rest.Middleware's identical use of
// slices.Clone for the same reason.
func cloneFieldCodecs[T any](fs []codex.FieldCodec[T]) []codex.FieldCodec[T] {
	out := make([]codex.FieldCodec[T], len(fs))
	copy(out, fs)
	return out
}

// WithReceive attaches a channel-AGNOSTIC Fn directly to mw — NEITHER
// WithReceive NOR WithSend below mentions T, so the returned
// Middleware[In,Out] value (fn included) can be passed to .Use(...)
// verbatim, on as many different channels as needed. Use
// [Subscriber.SubscribeMW]'s bound path instead when fn genuinely needs
// msg *T access.
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) error) Middleware[In, Out] {
	m.receiveFn = fn
	return m
}

// WithSend is [Middleware.WithReceive]'s publish-side sibling — fn
// produces mw's own Out value (the SAME shape [Publisher.PublishMW]'s
// bound path produces — see [Middleware]'s doc comment for why this is
// Out, not In).
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (Out, error)) Middleware[In, Out] {
	m.sendFn = fn
	return m
}

// SetContextFieldFromIn registers field to be published (via
// field.Set(ctx, get(in))) immediately after this middleware's In value is
// decoded+validated (DecodeIn), BEFORE the Fn runs — mirrors
// [rest.Middleware.SetContextFieldFromIn] exactly (docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase B). Works on BOTH
// Subscribe and Publish attachment, since In is always decoded
// post-merge-field-decode regardless of direction — though in practice
// this is the ONLY context-propagation option on Subscribe specifically
// (see [Middleware.SetContextFieldFromOut]'s doc comment for why).
//
//	authMw = authMw.SetContextFieldFromIn(UserIDField, func(in BearerCred) any { return in.UserID })
func (m Middleware[In, Out]) SetContextFieldFromIn(field middleware.ContextFieldSetter, get func(In) any) Middleware[In, Out] {
	m.ctxFieldsFromIn = append(slices.Clone(m.ctxFieldsFromIn), contextFieldInSetter[In]{field: field, get: get})
	return m
}

// SetContextFieldFromOut is [Middleware.SetContextFieldFromIn]'s sibling
// for the PRODUCED value — dispatched immediately after this middleware's
// Out value is validated (inside [buildEncodeOut]), alongside the
// merge-field encode. **NOT usable for events' Subscribe** — Subscribe's
// Fn (WithReceive) produces no Out at all (confirmed, the one genuine
// asymmetry vs. REST/reqreply); Go's own type system means this method
// simply isn't reachable there in practice, not a runtime restriction —
// it is only ever meaningfully called on a Publish-side [Middleware].
func (m Middleware[In, Out]) SetContextFieldFromOut(field middleware.ContextFieldSetter, get func(Out) any) Middleware[In, Out] {
	m.ctxFieldsFromOut = append(slices.Clone(m.ctxFieldsFromOut), contextFieldOutSetter[Out]{field: field, get: get})
	return m
}

// RouteMiddlewareMarker makes Middleware[In,Out] satisfy
// [middleware.RouteMiddleware] — EXPORTED (unlike [ports.Pattern]'s
// unexported-method sealing) because Go's unexported-method interface
// satisfaction is scoped per package: a type declared in api/events can
// never satisfy an interface whose method is unexported in package
// middleware, no matter the name — see [middleware.RouteMiddleware]'s doc
// comment. So a channel's plain .Use(...) can recognize and dispatch a
// channel-agnostic Middleware value carrying a WithReceive/WithSend fn.
func (Middleware[In, Out]) RouteMiddlewareMarker() {}

// SecurityDeclaration makes Middleware[In,Out] satisfy
// [middleware.SecurityCarrier] — returns the embedded Declaration's own
// Security field directly. Part of the middleware-consolidation effort
// (docs/design/d-0003-codec-declared-middlewares.md) folding Security into the
// codec-backed family: [Subscriber.SubscribeMW]/[Publisher.PublishMW]
// extract Security via this method UNIFORMLY, regardless of whether the
// attached value is this type or the legacy [middleware.Middleware].
func (m Middleware[In, Out]) SecurityDeclaration() *middleware.SecurityDeclaration {
	return m.Declaration.Security
}

// MiddlewareName makes Middleware[In,Out] satisfy a name-exposing
// interface used internally when synthesizing a legacy-shaped Security
// entry from a codec-backed value (see api/rest's routeMiddlewareOpt.applyRoute
// for the consuming side) — returns the embedded Declaration's own Name.
func (m Middleware[In, Out]) MiddlewareName() string { return m.Declaration.Name }

// applyAgnosticSubscriber implements the events-side routeMiddlewareContributor
// pattern — called by [Subscriber.Use] for a bundled Middleware value. In
// is concrete here, so it can build the SAME runtime dispatch handler
// [Transform] produces for the channel-BOUND case.
func (m Middleware[In, Out]) applyAgnosticSubscriber() (MiddlewareHandler, bool) {
	if m.receiveFn == nil {
		return MiddlewareHandler{}, false
	}
	return buildAgnosticMiddlewareHandler(m), true
}

// applyAgnosticPublisher is [applyAgnosticSubscriber]'s publish-side
// sibling — called by [Publisher.Use] for a bundled Middleware value.
func (m Middleware[In, Out]) applyAgnosticPublisher() (ClientMiddlewareHandler, bool) {
	if m.sendFn == nil {
		return ClientMiddlewareHandler{}, false
	}
	return buildAgnosticClientMiddlewareHandler(m), true
}

// applyBoundSubscriber implements [eventsMiddlewareContributor] — called by
// [Subscriber.SubscribeMW] for a bound-shaped codec-backed [Middleware][In,
// Out] (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase
// B — events' own Architecture-revision fold-in, mirroring
// [rest.Middleware.applyBoundRoute]). fn is UNTYPED here (already `any` at
// SubscribeMW's own signature) — m's own In/Out type parameters are ALL
// this method needs. **Simpler than REST's equivalent** (confirmed
// Learning #10, this session's Phase B model-review round): events
// already bundles spec metadata (propertyParams) DIRECTLY on
// [MiddlewareHandler] itself — no separate middlewareSpecContribution-
// style object to also populate, so this method need only build and
// return ONE handler value; [Subscriber.SubscribeMW] appends it directly.
func (m Middleware[In, Out]) applyBoundSubscriber(fn any) MiddlewareHandler {
	h := buildMiddlewareHandlerAny(m, fn)
	h.dualAttached = m.isBundled() // SubscribeMW is the BOUND path — D7 fires only here, mirrors Transform's identical rule.
	return h
}

// applyBoundSubscriberWithOut is [applyBoundSubscriber]'s NEW, ADDITIVE
// sibling for fn's matching [isBoundSubscribeMWShapeWithOut] (docs/
// roadmap/declarative-middleware-layering.md's "Prerequisite for Phase 2
// (api/events)") — sets [MiddlewareHandler.HasOut] and
// [MiddlewareHandler.ValidateOut] so [DispatchSubscribeMiddlewareHandlers]
// knows to expect TWO return values (Out, error) and to validate the
// decoded Out via m's OWN OutCodec, mirroring [buildClientMiddlewareHandlerAny]'s
// identical EncodeOut-validation step on the publish side — Subscribe's
// Out is never wire-encoded, only validated and surfaced for the
// GrantedScopes merge-and-enforce step.
func (m Middleware[In, Out]) applyBoundSubscriberWithOut(fn any) MiddlewareHandler {
	h := buildMiddlewareHandlerAny(m, fn)
	h.dualAttached = m.isBundled()
	h.HasOut = true
	h.ValidateOut = func(out any) error {
		o, _ := out.(Out)
		return m.OutCodec.Validate(o)
	}
	return h
}

// applyBoundPublisher is [applyBoundSubscriber]'s publish-side mirror —
// called by [Publisher.PublishMW] for a bound-shaped codec-backed
// [Middleware][In, Out].
func (m Middleware[In, Out]) applyBoundPublisher(fn any) ClientMiddlewareHandler {
	h := buildClientMiddlewareHandlerAny(m, fn)
	h.dualAttached = m.isBundled()
	return h
}

// isBundled reports whether mw carries a WithReceive/WithSend Fn — used by
// [checkMiddlewareNameUniquenessAndAttachment]'s D7 ambiguous-dual-attachment
// check, mirroring rest's identical technique.
func (m Middleware[In, Out]) isBundled() bool {
	return m.receiveFn != nil || m.sendFn != nil
}

// isBoundSubscribeMWShape is [Subscriber.SubscribeMW]'s events-side mirror
// of [rest.isBoundHandleMWShape] (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase B) — detects the channel-BOUND shape
// (func(ctx, *T, In) error, arity 3-in/1-out) via fn's OWN REFLECTED
// signature, never mw's dynamic type (same reasoning as REST: a
// generalized [SecurityMiddleware][In, Out] can ALSO be used purely as a
// legacy credential-shape carrier).
//
// Arity alone is NOT sufficient here, unlike REST's 2nd-param check:
// confirmed via code, `adapters/mqtt`'s/`adapters/zeromq`'s OWN legacy
// subscribe-security Fn shape — func(ctx, *T, []route.SecurityRequirement)
// error — shares the EXACT SAME 3-in/1-out arity AND the EXACT SAME 2nd
// param type (*T) as the bound shape. The 3rd param is the only
// differentiator: legacy's is always []route.SecurityRequirement; the
// bound shape's is mw's own In (never that exact type in practice).
// (`adapters/mqtt5`'s OWN legacy subscribe-security Fn — func(ctx,
// *pahomqtt5.Publish, *T) (map[string][]string, error) — already differs
// in both 2nd-param type AND arity, so needs no special-casing here.)
func isBoundSubscribeMWShape[T any](fn any) bool {
	fnVal := reflect.ValueOf(fn)
	if !fnVal.IsValid() {
		return false
	}
	t := fnVal.Type()
	if t.Kind() != reflect.Func || t.NumIn() != 3 || t.NumOut() != 1 {
		return false
	}
	if t.In(1) != reflect.TypeOf((*T)(nil)) {
		return false
	}
	return t.In(2) != reflect.TypeOf([]route.SecurityRequirement(nil))
}

// isBoundSubscribeMWShapeWithOut is [isBoundSubscribeMWShape]'s NEW,
// ADDITIVE sibling (docs/design/d-0007-declarative-middleware-layering.md's
// "Prerequisite for Phase 2 (api/events)") — detects the ALSO-channel-
// BOUND, Out-carrying shape (func(ctx, *T, In) (Out, error), arity
// 3-in/2-out), alongside (NOT replacing) [isBoundSubscribeMWShape]'s
// EXISTING 1-return shape. A Security-carrying middleware needing to
// return GrantedScopes on Subscribe uses THIS shape instead; a
// general-purpose middleware with nothing to return keeps using the
// existing 1-return shape, completely unaffected.
//
// The 2nd-param-type check alone is sufficient to disambiguate from every
// confirmed legacy shape: `adapters/mqtt`'s/`adapters/zeromq`'s legacy
// subscribe-security Fn is 1-return (rejected outright by the
// `NumOut() != 2` check below); `adapters/mqtt5`'s legacy subscribe-
// security Fn IS 3-in/2-out (func(ctx, *pahomqtt5.Publish, *T)
// (map[string][]string, error)), but its 2nd param is
// `*pahomqtt5.Publish` — an adapter-specific concrete type, never equal
// to `*T` for any real channel payload type.
func isBoundSubscribeMWShapeWithOut[T any](fn any) bool {
	fnVal := reflect.ValueOf(fn)
	if !fnVal.IsValid() {
		return false
	}
	t := fnVal.Type()
	if t.Kind() != reflect.Func || t.NumIn() != 3 || t.NumOut() != 2 {
		return false
	}
	return t.In(1) == reflect.TypeOf((*T)(nil))
}

// isBoundPublishMWShape is [Publisher.PublishMW]'s events-side mirror of
// [rest.isBoundClientMWShape] — the bound shape is func(ctx, msg T)
// (Out, error) (T BY VALUE, arity 2-in/2-out), vs. EVERY confirmed legacy
// publish-security Fn shape (`adapters/mqtt5`: func(ctx, *T,
// []route.SecurityRequirement) ([]UserProperty, error); `adapters/mqtt`/
// `adapters/zeromq`: func(ctx, *T, []route.SecurityRequirement) error) —
// all 3-in, distinguished from the bound shape's 2-in by arity ALONE, no
// param-type inspection needed.
func isBoundPublishMWShape[T any](fn any) bool {
	fnVal := reflect.ValueOf(fn)
	if !fnVal.IsValid() {
		return false
	}
	t := fnVal.Type()
	if t.Kind() != reflect.Func || t.NumIn() != 2 || t.NumOut() != 2 {
		return false
	}
	return t.In(1) == reflect.TypeOf((*T)(nil)).Elem()
}

// MiddlewareInputError is returned when a [Middleware]'s In value fails to
// decode/validate from the raw incoming topic vars (subscribe side only —
// publish has no In to decode).
//
// Use errors.As to extract the middleware name:
//
//	var mie events.MiddlewareInputError
//	if errors.As(err, &mie) {
//	    log.Printf("middleware %q: invalid input: %v", mie.Name, mie.Err)
//	}
type MiddlewareInputError struct {
	Name string
	Err  error
}

func (e MiddlewareInputError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: invalid input: %s", e.Name, e.Err.Error())
}

func (e MiddlewareInputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareInputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// MiddlewareError is returned when a [Transform]/[ClientTransform] fn
// (or a bundled WithReceive/WithSend fn) returns its own business error
// that does NOT match any declared [ErrorChannel] pattern — D2's fallback,
// mirrors [rest.MiddlewareError] exactly.
type MiddlewareError struct {
	Name string
	Err  error
}

func (e MiddlewareError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: %s", e.Name, e.Err.Error())
}

func (e MiddlewareError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// MiddlewareOutputError is returned when a [Middleware]'s Out value fails to
// encode into outgoing topic/property vars (via [Middleware].EncodeOut) —
// the OUTPUT-side counterpart of [MiddlewareInputError], added for symmetry
// (previously this failure propagated as a bare, unwrapped error with no
// way to recover which middleware failed; see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2). Mirrors
// [rest.MiddlewareOutputError] exactly.
//
// Use errors.As to extract the failing middleware's name:
//
//	var outputErr events.MiddlewareOutputError
//	if errors.As(err, &outputErr) {
//	    log.Printf("middleware %q: invalid output: %v", outputErr.Name, outputErr.Err)
//	}
type MiddlewareOutputError struct {
	Name string
	Err  error
}

func (e MiddlewareOutputError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: invalid output: %s", e.Name, e.Err.Error())
}

func (e MiddlewareOutputError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareOutputError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}

// DuplicateMiddlewareNameError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when two [Middleware] values with the SAME
// Declaration.Name are attached to one channel (D6(b)) — mirrors
// [rest.DuplicateMiddlewareNameError] exactly.
type DuplicateMiddlewareNameError struct {
	Topic string
	Name  string
}

func (e DuplicateMiddlewareNameError) Error() string {
	return fmt.Sprintf("api/events: channel %q: duplicate middleware name %q", e.Topic, e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateMiddlewareNameError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("name", e.Name),
	)
}

// AmbiguousMiddlewareAttachmentError is returned by [Subscriber.Handle]/
// [Publisher.Handle] when a SINGLE [Middleware] value carries a bundled
// WithReceive/WithSend Fn AND is ALSO passed to [Transform]/[ClientTransform]
// on the SAME channel (D7) — mirrors
// [rest.AmbiguousMiddlewareAttachmentError] exactly.
type AmbiguousMiddlewareAttachmentError struct {
	Name string
}

func (e AmbiguousMiddlewareAttachmentError) Error() string {
	return fmt.Sprintf("api/events: middleware %q: attached via BOTH .Use() (bundled fn) and Transform/ClientTransform (separate fn) — ambiguous, use only one attachment style per value", e.Name)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e AmbiguousMiddlewareAttachmentError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
	)
}

// ConflictingParamContributionError is returned when two DIFFERENT sources
// (a manual [PropertyParam] channel declaration or a specific [Middleware]'s
// Name) declare the SAME property name with a DIFFERENT Required value or
// codec schema. Identical redundant declarations dedupe silently — only a
// genuine MISMATCH errors. This is 100% NEW code for events (confirmed via
// grep: events shipped ZERO conflict-detection machinery before this) —
// mirrors [rest.ConflictingParamContributionError] field-for-field, using
// Topic (not Route) to match [DuplicateMiddlewareNameError]'s own
// events-specific field-naming convention.
type ConflictingParamContributionError struct {
	Topic                     string
	ParamName                 string
	FirstSource, SecondSource string
}

func (e ConflictingParamContributionError) Error() string {
	return fmt.Sprintf("api/events: channel %q: conflicting param contribution for %q: %q vs %q",
		e.Topic, e.ParamName, e.FirstSource, e.SecondSource)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConflictingParamContributionError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("param_name", e.ParamName),
		slog.String("first_source", e.FirstSource),
		slog.String("second_source", e.SecondSource),
	)
}

// eventsParamContribution is one source's declaration for a single property
// name, tracked for conflict detection — mirrors [rest]'s own
// paramContribution (Round 15 originally added a Codec field here that
// REST's own paramContribution lacked; REST has since gained its own
// equivalent field too, see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2,
// Candidate 2 — all 3 APIs now compare codec schemas the same way, via the
// shared [route.CodecSchemaMismatch]).
type eventsParamContribution struct {
	source   string
	required bool
	codec    *codex.Codec[string]
}

// checkEventsParamConflicts mirrors [rest]'s real checkParamConflicts
// (api/rest/middleware.go), SCOPED to events' property axis — topic vars
// have no pre-existing conflict-detection machinery to extend and are
// therefore NEVER cross-checked here (independent namespaces, Round 15
// decision: a topic var and a property sharing the same name is
// explicitly fine). Two contributions for the SAME name conflict if
// Required differs, OR their codec schemas mismatch per
// [route.CodecSchemaMismatch].
func checkEventsParamConflicts(topic string, contributions map[string][]eventsParamContribution) error {
	for name, list := range contributions {
		first := list[0]
		for _, c := range list[1:] {
			if c.required != first.required || route.CodecSchemaMismatch(first.codec, c.codec) {
				return ConflictingParamContributionError{
					Topic: topic, ParamName: name,
					FirstSource: first.source, SecondSource: c.source,
				}
			}
		}
	}
	return nil
}

// checkEventsMiddlewareNameUniquenessAndAttachment enforces D6(b) and D7
// from docs/design/d-0003-codec-declared-middlewares.md — mirrors
// api/rest's identical checkMiddlewareNameUniquenessAndAttachment exactly,
// adapted for events' split subscribe/publish handler lists (a channel's
// SAME topic can have independent Subscriber/Publisher declarations, so
// names are checked separately per role, matching how [Subscriber.Handle]/
// [Publisher.Handle] are themselves independent calls).
func checkEventsMiddlewareNameUniquenessAndAttachment(topic string, middlewareHandlers []MiddlewareHandler, clientMiddlewareHandlers []ClientMiddlewareHandler) error {
	seen := make(map[string]bool, len(middlewareHandlers)+len(clientMiddlewareHandlers))
	for _, h := range middlewareHandlers {
		if seen[h.Name] {
			return DuplicateMiddlewareNameError{Topic: topic, Name: h.Name}
		}
		seen[h.Name] = true
		if h.dualAttached {
			return AmbiguousMiddlewareAttachmentError{Name: h.Name}
		}
	}
	seen = make(map[string]bool, len(clientMiddlewareHandlers))
	for _, h := range clientMiddlewareHandlers {
		if seen[h.Name] {
			return DuplicateMiddlewareNameError{Topic: topic, Name: h.Name}
		}
		seen[h.Name] = true
		if h.dualAttached {
			return AmbiguousMiddlewareAttachmentError{Name: h.Name}
		}
	}
	return nil
}

// eventsMiddlewareContributor is implemented by [Middleware][In, Out] (via
// its unexported applyAgnosticSubscriber/applyAgnosticPublisher methods)
// to let [Subscriber.Use]/[Publisher.Use] apply a channel-AGNOSTIC
// codec-backed middleware's runtime dispatch handler WITHOUT Use itself
// needing to know In/Out — mirrors api/rest's identical
// routeMiddlewareContributor pattern.
type eventsMiddlewareContributor interface {
	middleware.RouteMiddleware
	applyAgnosticSubscriber() (MiddlewareHandler, bool)
	applyAgnosticPublisher() (ClientMiddlewareHandler, bool)

	// applyBoundSubscriber/applyBoundPublisher (docs/roadmap/declarative-
	// middleware-layering.md's Rollout Phase B) let [Subscriber.SubscribeMW]/
	// [Publisher.PublishMW] apply a BOUND codec-backed middleware WITHOUT
	// needing to know In/Out — mirrors api/rest's identical
	// routeMiddlewareContributor extension (Rollout Phase A's Architecture
	// revision). fn is `any` (already erased at SubscribeMW/PublishMW's
	// own signature).
	applyBoundSubscriber(fn any) MiddlewareHandler
	applyBoundPublisher(fn any) ClientMiddlewareHandler

	// applyBoundSubscriberWithOut is [applyBoundSubscriber]'s NEW,
	// ADDITIVE sibling (docs/design/d-0007-declarative-middleware-layering.md's
	// "Prerequisite for Phase 2 (api/events)") for fn matching
	// [isBoundSubscribeMWShapeWithOut] instead of [isBoundSubscribeMWShape].
	applyBoundSubscriberWithOut(fn any) MiddlewareHandler
}
