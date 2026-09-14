package reqreply

import (
	"context"
	"log/slog"
	"sync"
)

// ClientTransport is implemented by each adapter's internal, unexported
// binding attached to a [Client] via an adapter-specific Attach function
// (e.g. [mqtt5.Attach], [zeromq.Attach]) — see [Client.Attach]. Mirrors
// [rest.ClientTransport]/[events.Transport]'s Publish side.
type ClientTransport interface {
	// Call performs a round trip against route (dynamic type
	// [Route][Req,Resp] OR *[RouteHandle][Req,Resp] — see [Client.Call]'s
	// doc comment for the confirmed dual-mode acceptance) with req
	// (dynamic type Req), returning resp (dynamic type Resp). opts (0 or
	// 1 value) carries a per-call format override — see
	// [ClientCallOptions].
	Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)

	// CallAsync is the non-blocking counterpart to Call — returns
	// (*[Future][Resp] as any, nil) immediately (or a non-nil error if
	// dispatch itself fails before any future could be constructed); the
	// future resolves later, asynchronously, when the correlated reply
	// arrives. See [Client.CallAsync]'s doc comment for the confirmed
	// "send here, resolve elsewhere" mechanism. opts is [Call]'s
	// identical per-call override parameter — resolved inside the SAME
	// underlying dispatch [Call] uses (see docs/roadmap/
	// D-0004's Addendum's "Interaction with CallAsync/Future").
	CallAsync(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error)
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
}

// Client accumulates NO spec state — it is purely a dispatch handle, one
// [ClientTransport] attached via [Client.Attach], mirroring
// [rest.Client]/[events.Client]'s identical shape.
type Client struct {
	mu        sync.RWMutex
	transport ClientTransport
}

// NewClient returns an unattached [Client]. Call an adapter's Attach
// function (e.g. [mqtt5.Attach]) before using [Client.Call]/[Client.CallAsync].
func NewClient() *Client {
	return &Client{}
}

// Attach binds t to c as c's transport — the "attach the adapter to the
// client" step behind [Client.Call]/[Client.CallAsync]. Each adapter
// provides its own entry point (e.g. [mqtt5.Attach](client, mqttClient,
// router)) that builds an internal ClientTransport implementation and
// calls this method internally; application code calls the ADAPTER's
// Attach function, not this method directly, in the common case.
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
func (c *Client) Call(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error) {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}
	return t.Call(ctx, route, req, opts...)
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
// route accepts the SAME dual-mode shape [Client.Call] does. opts is
// [Client.Call]'s IDENTICAL trailing per-call format-override parameter —
// resolved inside the SAME underlying dispatch [Client.Call] uses, so it
// applies to CallAsync automatically (see docs/roadmap/
// D-0004's Addendum's "Interaction with CallAsync/Future" for the
// confirmed async-timing nuance this implies for general-purpose
// middleware, unrelated to format resolution itself). Returns
// [NoClientTransportAttachedError] if [Client.Attach] was never called.
//
//	future, err := client.CallAsync(ctx, ComputeRoute, ComputeReq{X: 1, Y: 2})
//	// ... do other independent work here ...
//	resp, err := future.(*reqreply.Future[ComputeResp]).Wait(ctx)
func (c *Client) CallAsync(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error) {
	c.mu.RLock()
	t := c.transport
	c.mu.RUnlock()
	if t == nil {
		return nil, NoClientTransportAttachedError{}
	}
	return t.CallAsync(ctx, route, req, opts...)
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
	return "api/reqreply: Client has no ClientTransport attached (call an adapter's Attach function first, e.g. mqtt5.Attach(client, mqttClient, router))"
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
