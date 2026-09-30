// Package reqreply provides a transport-agnostic request-reply API layer for
// async transports (ZeroMQ, MQTT 5, AMQP RPC, etc.).
//
// It follows the same declare → register → handle pattern as [api/events] and
// [api/rest]. [Route] is the reqreply analogue of [rest.Route]: a typed
// request-reply declaration with a topic/address instead of an HTTP method+path.
//
// The protocol is just a server string in [Builder.AddServer] — the same
// [Route] declaration works for any transport. Adapters accept
// [*RouteHandle] directly.
//
// # Usage
//
//	// Declare once — no HTTP method, just a topic.
//	var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
//	    "compute/add",
//	    computeReqCodec, computeRespCodec,
//	    reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers."},
//	    reqreply.ErrorPattern[domain.ConflictError, ErrorPayload](errorPayloadCodec,
//	        func(e domain.ConflictError) (ErrorPayload, error) {
//	            return ErrorPayload{Code: "conflict", Message: e.Error()}, nil
//	        },
//	    ).WithCode("conflict").WithDescription("Business conflict.").WithSchemaName("ConflictError"),
//	)
//
//	// Register a domain handler, then attach a Server to get a RouteHandle
//	// and an AsyncAPI 3.0 spec. Handler/encode errors matching a declared
//	// ErrorPattern get the typed payload as the reply instead of a
//	// plain-text error string — dispatched uniformly regardless of adapter.
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5556", Protocol: "zmq"})
//	// OR: server.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
//	handle, err := ComputeRoute.WithHandler(computeHandler).Register(server)
//
//	// Attach an adapter transport, then Serve — dispatches every registered route:
//	transport := zeromqadapter.NewServerTransport(zeromqadapter.ServerTransportOptions{
//	    Sockets: map[string]zeromqadapter.FramedSocket{"compute/add": repSock},
//	})
//	// OR: transport := mqtt5adapter.NewServerTransport(mqtt5adapter.ServerTransportOptions{Client: client, Router: router})
//	if err := server.Attach(transport); err != nil { /* handle */ }
//	err = server.Serve(ctx)
//
//	// AsyncAPI 3.0 spec with request-reply reply: block, plus the
//	// ErrorPattern-derived reply-error channel/operation:
//	doc, _ := server.AsyncAPISpec()
//	yaml, _ := doc.MarshalYAML()
//
// # Reusing a topic across routes
//
// The plain-string form above is the default. When the SAME topic template
// and [TopicParam] declarations are shared by two or more routes of
// DIFFERENT Req/Resp type pairs, extract the shape once as a [Topic] and
// reuse it via [NewRouteFromTopic] — mirrors [events.Topic]/
// [events.NewChannelFromTopic] and [rest.Path]/[rest.NewRouteFromPath]
// exactly. [Builder.AddGlobalSecurity] aside, [WithTopicCodec]/
// [WithTopicConstraints] on [NewBuilder] set a builder-wide default topic
// codec enforced at [Route.Register] time, mirroring [events.WithTopicCodec]/
// [events.WithTopicConstraints].
//
// # Error-path ergonomics
//
// [ErrorPattern] is the codec-first, runtime-wired error declaration — the
// request-reply analogue of [rest.ErrorPattern] and [events.ErrorChannel]:
// declare a typed error payload for a matched error type (direct or mapped
// mode), and every adapter's [ServerTransport] (mqtt5, zeromq REQ/REP,
// zeromq ROUTER/DEALER) automatically sends it on handler/encode failure
// instead of a plain-text error string.
// [ErrorPattern] also drives the AsyncAPI reply-error channel/operation that
// [ErrorReplyMeta] previously required a separate declaration for — one
// declaration now produces both. [ErrorReplyMeta] remains available
// unchanged for spec-only declarations that need no runtime dispatch.
//
// # Server/Client + Attach
//
// [Server] absorbs [Builder]'s spec-accumulation role (AddServer,
// AddGlobalSecurity, route registration) AND owns request-reply dispatch
// once a [ServerTransport] is attached via [Server.Attach] — mirroring
// [rest.Server]'s identical unification. [Builder]/[NewBuilder] remain
// available as DEPRECATED aliases for [Server]/[NewServer]; existing code
// using [Builder] keeps compiling and behaving identically. There is no
// adapter-namespaced Attach function anymore (removed, breaking, per
// docs/roadmap/capability-requirement-composition.md's "zero backdoor
// between the api layer and the adapters" directive) — every adapter
// instead exposes a NewServerTransport/NewClientTransport constructor,
// consumed uniformly via [Server.Attach]/[Client.Attach].
//
// [Route.WithHandler] attaches a domain handler fluently, PRE-registration
// — mirroring [rest.Route.WithHandler]'s real, dominant idiom exactly:
//
//	handle, err := ComputeRoute.WithHandler(computeHandler).Register(server)
//	transport := mqtt5adapter.NewServerTransport(mqtt5adapter.ServerTransportOptions{Client: client, Router: router})
//	if err := server.Attach(transport); err != nil { /* handle */ }
//	err = server.Serve(ctx) // dispatches every registered route concurrently
//
// [Client] offers a blocking [Client.Call] (accepting EITHER a raw,
// unregistered [Route] — REST-style, [RouteHandle.GlobalSecurity]
// invisible — OR an already-registered *[RouteHandle], with
// [RouteHandle.GlobalSecurity] enforced) AND an additive, non-blocking
// [Client.CallAsync] returning a *[Future][Resp] resolved later,
// asynchronously — needed because reqreply's transport (unlike REST's
// synchronous HTTP) is genuinely asynchronous underneath.
//
// Every adapter (mqtt5, zeromq REQ/REP, zeromq ROUTER/DEALER) ships both a
// [ServerTransport] and [ClientTransport] implementation — see each
// adapter's own doc.go for its NewServerTransport/NewClientTransport
// signature and options.
//
// # Protocol-native capabilities
//
// A route's protocol behavior (MQTT QoS, retained messages, ZeroMQ
// high-water-mark, ...) is classified into the same three-tier vocabulary
// [api/events] uses — see docs/roadmap/capability-requirement-composition.md
// for the full design:
//
//   - Baseline — the Topic itself. Mandatory, never declared explicitly.
//   - Implicit — a requirement that arises as a side effect of declaring a
//     codec-backed field (e.g. an adapter's own UserPropertyParam-style
//     option). Declaring the field IS the requirement.
//   - Explicit — a standalone requirement, declared via [CapabilityRequirement]
//     (or the sugar helpers [RequireQoS]/[RequireRetained]/[RequireHWM]/
//     [RequireConflate]), independent of any adapter until [Server.Attach]/
//     [Client.Attach] time. [CheckCapabilityCoverage] verifies the supplied
//     adapter Capability values (via each adapter's `ServeOptions`/
//     `CallOptions.Capabilities` field) satisfy every declared requirement
//     — including, for requirements with a MinLevel (e.g. RequireQoS), a
//     genuine VALUE check via the optional [LeveledCapability] interface,
//     not just presence-by-name. Checked once at Serve/Attach setup;
//     the resolved values are then applied to EVERY reply publish
//     (success, error-pattern-matched, and dead-letter alike).
//
// [reqreply] deliberately does NOT import [api/events] for this — it has
// its OWN, byte-for-byte-identical-in-shape [CapabilityRequirement]/
// [CheckCapabilityCoverage]/[CapabilityCoverageError]/
// [VerifyCapabilityCoverage]/[LeveledCapability] types (mirrors
// [middleware.Disposition]'s own placement rationale: cheap to duplicate
// a small type, rather than introduce a cross-API import for it). The
// generic adapter-side helpers `events.ResolveCapabilityValue`/
// `events.RecordCapabilityApplied` ARE reused as-is by reqreply's adapter
// dispatch code (fully generic, no events-specific types beyond the
// trivially-structural `CapabilityName` interface) — only the
// declaration-side pieces above are duplicated. Reuses the SAME sealed
// adapter-owned Capability values [api/events] already ships
// (`mqtt5.QoS`/`mqtt5.Retained`, `zeromq.HWM`/`zeromq.Conflate`) — zero
// new adapter-side capability types needed.
package reqreply
