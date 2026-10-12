package reqreply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
)

// ClientTransport is implemented by each adapter's internal, unexported
// binding returned by an adapter-specific NewClientTransport constructor and
// attached to a [Client] via [Client.Attach] (e.g. client.Attach(mqtt5.NewClientTransport(...)),
// client.Attach(zeromq.NewClientTransport(...))). Mirrors
// [rest.ClientTransport]/[events.Transport]'s Publish side.
type ClientTransport interface {
	// Call performs a round trip against route (dynamic type
	// [Route][Req,Resp] OR *[RouteHandle][Req,Resp] — see [Client.Call]'s
	// doc comment for the confirmed dual-mode acceptance) with req
	// (dynamic type Req), returning resp (dynamic type Resp). opts (0 or
	// 1 value) carries a per-call format override — see
	// [ClientCallOptions].
	Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)

	// CallRaw publishes payload to topic VERBATIM (already encoded by the
	// caller — no Req/Resp type involvement here) and awaits exactly ONE
	// correlated reply — the single-hop primitive
	// docs/roadmap/reqreply-forward-bounce.md's forward-following chain
	// and docs/roadmap/reqreply-dead-letter-replay.md's replay both build
	// on, via a shared loop living in [*Client] (NOT inside CallRaw
	// itself, and NOT inside either consuming roadmap feature's own
	// code — CallRaw is Phase 0 infrastructure, added ahead of its real
	// consumers).
	//
	// CallRaw classifies its reply the SAME 3-way way an ordinary
	// Call/CallAsync reply already does:
	//   - an ordinary successful reply:              (replyBytes, nil)
	//   - a reply tagged as a Forward (reserved wire
	//     signal — mqtt5's ContentType+User Property,
	//     zeromq's status frame — SHIPPED,
	//     docs/roadmap/reqreply-forward-bounce.md):    (nil, ForwardError{TargetTopic, Payload})
	//   - a reply matching a declared ErrorPattern:    (nil, ForwardRawErrorReply{Code, Body})
	//
	// CallRaw deliberately does NOT decode the ErrorPattern-matched case
	// into a typed ErrorPatternResponse — it has no route to resolve a
	// declared pattern against (only a topic string and raw bytes).
	// [ForwardRawErrorReply] carries the RAW wire-level code (if present)
	// + body instead — a caller holding the registry-resolved target
	// *RouteHandle (e.g. the chain-following loop
	// docs/roadmap/reqreply-forward-bounce.md's Client.Call and
	// docs/roadmap/reqreply-dead-letter-replay.md's ReplayDeadLetter both
	// build) can finish the decode itself via
	// RouteHandle.DecodeErrorFor(Code, Body) — moving the "hard part"
	// (interpreting the code against a declared ErrorPattern) to
	// api/reqreply, keeping CallRaw a pure wire-level extraction
	// primitive. Code is "" when the reply carries no code (the
	// plain-text fallback case — mirrors Call's own identical fallback).
	//
	// The Forward leg's wire signal is SHIPPED
	// (docs/roadmap/reqreply-forward-bounce.md, Phase 1) — each
	// adapter's CallRaw implements all 3 legs; [Client.followForward]
	// (docs/roadmap/reqreply-forward-bounce.md's chain-following loop)
	// is the real consumer, built on top of this primitive.
	//
	// properties (Phase 1.5, docs/roadmap/reqreply-forward-bounce.md's
	// own "Addendum" section) carries wire-level properties/headers
	// (e.g. an mqtt5 User Property delivering a credential) alongside
	// payload — nil when there are none, mirroring every other optional
	// map parameter in this package (e.g. [Forward]'s own vars
	// parameter). Closes the LATENT gap found during a
	// reqreply-dead-letter-replay.md review round: CallRaw previously
	// had NO channel to carry anything beyond the body, so neither an
	// ordinary [Forward] hop nor a dead-letter [ReplayDeadLetter] could
	// ever supply a property/header-delivered credential to the target.
	// adapters/mqtt5 threads this into the outgoing PublishProperties.
	// User; adapters/zeromq accepts it for interface symmetry only — it
	// has no property/header mechanism of its own to put it on, a
	// confirmed, deliberate no-op there, not a partial implementation.
	CallRaw(ctx context.Context, topic string, payload []byte, properties map[string]string) ([]byte, error)
}

// ForwardRawErrorReply is returned by [ClientTransport.CallRaw] when a
// reply is classified as matching a declared [ErrorPattern] — carries
// the RAW wire-level code (if present) and body, NOT a decoded
// [ErrorPatternResponse]-equivalent, since CallRaw has no route to
// decode against (see [ClientTransport.CallRaw]'s doc comment for the
// full rationale). A caller holding the registry-resolved target
// *[RouteHandle] can finish the decode itself via
// RouteHandle.DecodeErrorFor(Code, Body).
type ForwardRawErrorReply struct {
	// Code is the matched ErrorPattern's reserved wire-level code (e.g.
	// mqtt5's "x-error-code" User Property, zeromq's 3rd/4th status
	// frame), or "" when the reply carries no code — the plain-text
	// fallback case (no declared ErrorPattern matched, or the server
	// predates this mechanism).
	Code string
	// Body is the reply's raw payload bytes.
	Body []byte
}

func (e ForwardRawErrorReply) Error() string {
	return "api/reqreply: server error: " + string(e.Body)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ForwardRawErrorReply) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("code", e.Code),
		slog.String("body", string(e.Body)),
	)
}

// ClientCallOptions configures a single [Client.Call]/[Client.CallAsync]
// invocation — the reqreply mirror of [rest.ClientCallOptions] (identical
// field names/shape; type-erased since [Client.Call]/[ClientTransport.
// Call] have no Req/Resp type parameter to constrain a generic options
// type here either). Closes Phase 0 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's "capability parity" work: the
// per-call format-override half of the gap between the [Attach]-based
// workflow and the lower-level [Serve]/[Call] escape hatch (which already
// supports this via each adapter's own `CallOptions.RequestFormats`/
// `ResponseFormats`).
type ClientCallOptions struct {
	// RequestFormats, when non-nil, OVERRIDES the route's declared
	// request-body encode format for THIS call only
	// ([]format.Format[Req]) — resolved generically by the attached
	// [ClientTransport], falling back to the route's declared
	// [RouteHandle.RequestFormats] when nil.
	RequestFormats any

	// ResponseFormats is [RequestFormats]'s response-direction sibling
	// ([]format.Format[Resp]), falling back to [RouteHandle.Formats]
	// when nil.
	ResponseFormats any

	// MaxForwards caps how many [Forward] hops [Client.Call]/[Client.
	// CallAsync] will follow before giving up with
	// [ForwardChainTooDeepError] — direct port of [rest.
	// ClientCallOptions.MaxRedirects]'s identical mechanics (a
	// recursive-call counter, covering both unreasonably long chains
	// and cycles with one mechanism). MaxForwards <= 0 (including the
	// zero value) means "use the default of 10" — mirrors
	// [rest.ClientCallOptions.MaxRedirects]'s own resolution exactly
	// (see adapters/nethttp/redirect.go's followRedirect).
	MaxForwards int
}

// Client accumulates ONE piece of spec state beyond a dispatch handle: a
// redirect-target-style registry (see [Client.RegisterRoute]) — added for
// docs/roadmap/reqreply-forward-bounce.md's forward/bounce mechanism,
// mirroring [rest.Client]'s own registry (see docs/features/
// rest-redirects.md). Still just ONE [ClientTransport] attached via
// [Client.Attach], mirroring [rest.Client]/[events.Client]'s identical
// attach shape.
type Client struct {
	mu        sync.RWMutex
	transport ClientTransport
	routes    map[string]*registeredRoute
}

// registeredRoute is one entry in [Client]'s topic-keyed registry.
// Reqreply has no REST-style {Method, Path} pair — a route's identity is
// just its Topic string, already used as the map key, so no further
// descriptor needs storing here beyond the recovered handle itself.
type registeredRoute struct {
	// Handle is the recovered *RouteHandle[Req, Resp] (dynamic type),
	// exactly as [recoverRegistryEntry] returns it.
	Handle any

	// poisoned is set by [Client.registerAutoFromCall] when a SECOND,
	// genuinely different route type is auto-registered against an
	// ALREADY-registered topic — [Call]/[CallAsync]'s own contract
	// requires registerAutoFromCall to never error, so the conflict is
	// recorded here instead and surfaced lazily by whichever future
	// consumer resolves this topic (docs/roadmap/reqreply-forward-bounce.md's
	// forward resolution, docs/roadmap/reqreply-dead-letter-replay.md's
	// replay resolution) at the exact point it would otherwise decode
	// using the WRONG Resp type.
	poisoned bool

	// conflictingType records the SECOND type seen, for
	// [DuplicateTopicTypeMismatchError]'s NewType field once a future
	// consumer surfaces this poisoning.
	conflictingType string
}

// reqreplyClientPkgPath is api/reqreply's own import path — used to
// recognize a genuine reqreply.Route[Req,Resp]/*reqreply.RouteHandle[Req,Resp]
// value (for ANY type params), mirroring api/rest's identical
// clientRegistryPkgPath-based discrimination in its own
// recoverRegistryEntry.
const reqreplyClientPkgPath = "github.com/DaniDeer/go-codex/api/reqreply"

// recoverRegistryEntry reflects routeAny into this Client's registry
// shape: the route's Topic plus the recovered handle (dynamic type
// *RouteHandle[Req,Resp]). Accepts the SAME dual-mode shapes [Client.
// Call]/[Client.CallAsync] do: a raw, unregistered Route[Req,Resp]
// (calls its own ClientHandle() method reflectively), or an
// already-built *RouteHandle[Req,Resp] (used as-is).
func recoverRegistryEntry(routeAny any) (topic string, handle any, err error) {
	rv := reflect.ValueOf(routeAny)
	if !rv.IsValid() {
		return "", nil, TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]",
			Got:  fmt.Sprintf("%T", routeAny),
		}
	}
	t := rv.Type()
	switch {
	case t.PkgPath() == reqreplyClientPkgPath && strings.HasPrefix(t.Name(), "Route["):
		handleVal := rv.MethodByName("ClientHandle").Call(nil)[0]
		return topicOf(handleVal), handleVal.Interface(), nil
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == reqreplyClientPkgPath && strings.HasPrefix(t.Elem().Name(), "RouteHandle["):
		return topicOf(rv), rv.Interface(), nil
	default:
		return "", nil, TransportTypeMismatchError{
			Want: "reqreply.Route[Req, Resp] or *reqreply.RouteHandle[Req, Resp]",
			Got:  fmt.Sprintf("%T", routeAny),
		}
	}
}

// topicOf reads Topic off handleVal (handleVal is always a
// *RouteHandle[Req,Resp] reflect.Value) — RouteHandle's Topic field is a
// plain string, unlike [rest.RouteHandle]'s nested Descriptor, so no
// further field traversal is needed.
func topicOf(handleVal reflect.Value) string {
	return handleVal.Elem().FieldByName("Topic").String()
}

// RegisterRoute warms c's registry with route without making a live call
// against it — for a route that is only ever reached as a
// [docs/roadmap/reqreply-forward-bounce.md Forward] target or a
// [docs/roadmap/reqreply-dead-letter-replay.md ReplayDeadLetter] source,
// never called directly (routes called directly via [Client.Call]/
// [Client.CallAsync] are indexed automatically). route may be a [Route]
// or [*RouteHandle] — discriminated via reflection.
//
// Re-registering the SAME Topic with a route recovering to the IDENTICAL
// concrete handle type is a safe no-op. Re-registering the SAME Topic
// with a GENUINELY DIFFERENT route type (unlike [rest.Client.
// RegisterRoute]'s own "always safe" guarantee — reqreply has no
// structural guarantee two routes sharing a Topic are "the same route")
// returns [DuplicateTopicTypeMismatchError] immediately, without
// mutating the existing entry.
func (c *Client) RegisterRoute(route any) error {
	topic, handle, err := recoverRegistryEntry(route)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routes == nil {
		c.routes = make(map[string]*registeredRoute)
	}
	existing, ok := c.routes[topic]
	if !ok {
		c.routes[topic] = &registeredRoute{Handle: handle}
		return nil
	}
	existingType := reflect.TypeOf(existing.Handle)
	newType := reflect.TypeOf(handle)
	if existingType == newType {
		return nil
	}
	return DuplicateTopicTypeMismatchError{
		Topic:        topic,
		ExistingType: existingType.String(),
		NewType:      newType.String(),
	}
}

// registerAutoFromCall is called by [Client.Call]/[Client.CallAsync]
// BEFORE delegating to the attached [ClientTransport] — auto-populates
// the registry the first time a given route is seen, per [Client.
// RegisterRoute]'s own doc comment ("routes called directly via Call/
// CallAsync are indexed automatically"). Best-effort: a route value that
// doesn't match the recognized shapes is silently skipped here (NOT an
// error) — the attached [ClientTransport]'s own dispatch is the
// authoritative place a genuine type-mismatch is reported, this is pure
// bookkeeping that must never mask or duplicate that error.
//
// A genuine topic collision here (unlike [Client.RegisterRoute]'s
// explicit error) marks the EXISTING entry POISONED instead of
// returning an error — [Call]/[CallAsync]'s own contract requires this
// method to never error. The poisoning surfaces lazily, later, at
// whichever future consumer (forward/replay resolution) actually
// resolves this topic.
func (c *Client) registerAutoFromCall(route any) {
	topic, handle, err := recoverRegistryEntry(route)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.routes == nil {
		c.routes = make(map[string]*registeredRoute)
	}
	existing, ok := c.routes[topic]
	if !ok {
		c.routes[topic] = &registeredRoute{Handle: handle}
		return
	}
	newType := reflect.TypeOf(handle)
	if reflect.TypeOf(existing.Handle) == newType {
		return
	}
	existing.poisoned = true
	existing.conflictingType = newType.String()
}

// DuplicateTopicTypeMismatchError is returned by [Client.RegisterRoute]
// when a SECOND, genuinely different route is registered against an
// ALREADY-registered Topic — a GENUINE DIVERGENCE from [rest.Client.
// RegisterRoute]'s own always-safe "first-registered-wins" policy, since
// reqreply has no structural guarantee that two routes sharing a Topic
// are "the same route" (REST's guarantee holds only because every
// re-registration of the same key is, by construction, the SAME
// underlying Route wrapped in a different credential-bound handle).
//
// ExistingType/NewType are the WHOLE registered handle's
// reflect.Type.String() (e.g. "*reqreply.RouteHandle[foo.FooReq,foo.FooResp]"),
// not individually-recovered Req/Resp types — Go's reflect package has
// no API to extract a generic type's own instantiated type arguments
// after the fact.
type DuplicateTopicTypeMismatchError struct {
	// Topic is the topic that was registered more than once with
	// incompatible route types.
	Topic string
	// ExistingType is the FIRST-registered handle's whole type string.
	ExistingType string
	// NewType is the SECOND (rejected or poisoned) handle's whole type
	// string.
	NewType string
}

func (e DuplicateTopicTypeMismatchError) Error() string {
	return "api/reqreply: " + e.Topic + ": registered with incompatible route types: " + e.ExistingType + " vs. " + e.NewType
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e DuplicateTopicTypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("existing_type", e.ExistingType),
		slog.String("new_type", e.NewType),
	)
}

// NewClient returns an unattached [Client]. Call client.Attach(...) with an
// adapter-specific NewClientTransport result (e.g.
// client.Attach(mqtt5.NewClientTransport(...))) before using
// [Client.Call]/[Client.CallAsync].
func NewClient() *Client {
	return &Client{}
}

// Attach binds t to c as c's transport — the "attach the adapter to the
// client" step behind [Client.Call]/[Client.CallAsync]. Each adapter
// provides its own `New*Transport` factory (e.g.
// `mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router})`)
// that builds a configured [ClientTransport] value; application code
// attaches it via THIS method directly — never via an adapter-namespaced
// convenience function (removed, see docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4d). No adapter's
// [ClientTransport] currently needs a [*Client] back-reference at bind
// time (unlike [ServerAwareTransport]'s server-side need), so there is
// no `ClientAwareTransport` counterpart today.
//
// Returns [ClientTransportAlreadyAttachedError] if c already has a
// transport attached — Attach is exclusive, mirrors [rest.Client.Attach]/
// [events.Client.Attach]/[Server.Attach] exactly.
func (c *Client) Attach(t ClientTransport) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.transport != nil {
		return ClientTransportAlreadyAttachedError{}
	}
	c.transport = t
	return nil
}

// Call performs a blocking round trip against route with req, via c's
// attached [ClientTransport]. route accepts EITHER a raw, unregistered
// [Route][Req,Resp] (REST-style — zero [Server] needed, [RouteHandle.
// GlobalSecurity] invisible, the SAME accepted limitation [rest.Route.
// ClientHandle] has) OR an already-registered *[RouteHandle][Req,Resp]
// (obtained via [Route.Register] — [RouteHandle.GlobalSecurity]
// enforced) — CONFIRMED via a throwaway Go prototype to be strictly MORE
// flexible than REST's own Client.Call, which can only ever accept the
// raw form. See docs/design/d-0004-reqreply-workflow-simplification.md's
// Decision 1 for the full confirmed evidence.
//
// opts (0 or 1 value) OVERRIDES the route's declared request/response
// formats for THIS call only — see [ClientCallOptions]. Closes Phase 0 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum.
//
// Returns [NoClientTransportAttachedError] if [Client.Attach] was never
// called.
//
// Auto-follows a [ForwardError] (docs/roadmap/reqreply-forward-bounce.md)
// via an explicit CHAIN-FOLLOWING LOOP, CENTRALIZED here — not inside
// the adapter — see [Client.followForward]'s doc comment for the full
// resolution mechanism (registry lookup, security-scheme check,
// [ClientCallOptions.MaxForwards] capping, decode).
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error) {
	c.registerAutoFromCall(route)
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}
	resp, err := t.Call(ctx, route, req, opts...)
	var fwd ForwardError
	if !errors.As(err, &fwd) {
		return resp, err
	}
	return c.followForward(ctx, t, route, fwd, opts...)
}

// CallAsync is the non-blocking counterpart to [Client.Call] — returns a
// *[Future][Resp] (as any) IMMEDIATELY, resolved later, asynchronously,
// when the correlated reply arrives — the "send here, resolve elsewhere"
// mechanism CONFIRMED via a throwaway Go prototype (Decision 5).
// Additive: [Client.Call] is unchanged; CallAsync exists alongside it
// because reqreply's underlying transport (MQTT5/ZeroMQ correlation-
// based reply matching) is genuinely asynchronous, unlike REST's
// synchronous HTTP.
//
// RESTRUCTURED (docs/roadmap/reqreply-forward-bounce.md's "CallAsync/
// Future" section) — CallAsync no longer delegates to a
// ClientTransport.CallAsync method at all (that method was REMOVED from
// the interface entirely); it resolves a [FutureFactory] from route
// itself, then launches a goroutine that calls [Client.Call] directly,
// reusing Call's OWN chain-following loop (docs/roadmap/
// reqreply-forward-bounce.md's forward-following, docs/roadmap/
// reqreply-dead-letter-replay.md's replay) rather than needing a SECOND,
// adapter-level copy of it. A prior design had each adapter's own
// CallAsync run a background goroutine that resolved the Future entirely
// INSIDE the adapter — traced against the real, shipped code and found
// structurally unable to let api/reqreply.Client intercept a forward
// reply before the Future resolved; this restructure is the confirmed
// fix (consistent with this project's general preference for hard logic
// in the api/* layer, thin adapters).
//
// route accepts the SAME dual-mode shape [Client.Call] does. opts is
// [Client.Call]'s IDENTICAL trailing per-call format-override parameter —
// resolved inside the SAME underlying dispatch [Client.Call] uses, so it
// applies to CallAsync automatically (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum's
// "Interaction with CallAsync/Future" for the
// confirmed async-timing nuance this implies for general-purpose
// middleware, unrelated to format resolution itself). Returns
// [NoClientTransportAttachedError] if [Client.Attach] was never called
// — checked up front, synchronously, before any goroutine launches, so
// a caller sees it immediately rather than only once the Future resolves.
//
//	future, err := client.CallAsync(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
//	// ... do other independent work here ...
//	resp, err := future.(*reqreply.Future[ComputeResp]).Wait(ctx)
func (c *Client) CallAsync(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error) {
	c.registerAutoFromCall(route)
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}
	_, handle, err := recoverRegistryEntry(route)
	if err != nil {
		return nil, err
	}
	ff, ok := handle.(FutureFactory)
	if !ok {
		return nil, TransportTypeMismatchError{Want: "reqreply.FutureFactory", Got: fmt.Sprintf("%T", route)}
	}
	future, resolve := ff.NewFutureAny()
	go func() {
		resp, err := c.Call(ctx, route, req, opts...)
		resolve(resp, err)
	}()
	return future, nil
}

// ClientTransportAlreadyAttachedError is returned by [Client.Attach] when
// c already has a [ClientTransport] attached — Attach is exclusive, see
// its doc comment for the rationale.
type ClientTransportAlreadyAttachedError struct{}

func (e ClientTransportAlreadyAttachedError) Error() string {
	return "api/reqreply: Client already has a ClientTransport attached (Attach is exclusive; build a fresh Client for a different transport)"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e ClientTransportAlreadyAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// NoClientTransportAttachedError is returned by [Client.Call]/
// [Client.CallAsync] when [Client.Attach] was never called.
type NoClientTransportAttachedError struct{}

func (e NoClientTransportAttachedError) Error() string {
	return "api/reqreply: Client has no ClientTransport attached (build an adapter transport and call Attach yourself, e.g. client.Attach(mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router})))"
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoClientTransportAttachedError) LogValue() slog.Value {
	return slog.GroupValue()
}

// TransportTypeMismatchError is returned by an adapter's [ClientTransport.
// Call]/[ClientTransport.CallAsync] or [ServerTransport.Serve] when the
// dynamic types of its `any`-typed arguments don't match each other as
// expected — mirrors [rest.TransportTypeMismatchError]/
// [events.TransportTypeMismatchError] exactly (each API layer keeps its
// own parallel error vocabulary).
type TransportTypeMismatchError struct {
	// Topic is the route topic involved, when known (empty when the
	// mismatch is detected before a route/topic can be resolved, e.g. an
	// entirely wrong route type).
	Topic string
	// Want describes the expected type.
	Want string
	// Got describes the actual, mismatched type.
	Got string
}

func (e TransportTypeMismatchError) Error() string {
	if e.Topic != "" {
		return "api/reqreply: " + e.Topic + ": type mismatch: want " + e.Want + ", got " + e.Got
	}
	return "api/reqreply: type mismatch: want " + e.Want + ", got " + e.Got
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e TransportTypeMismatchError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.String("want", e.Want),
		slog.String("got", e.Got),
	)
}
