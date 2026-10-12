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
// docs/design/d-0006-protocol-native-capabilities.md's "zero backdoor
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
// [api/events] uses — see docs/design/d-0006-protocol-native-capabilities.md
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
// [stats.Disposition]'s own placement rationale: cheap to duplicate
// a small type, rather than introduce a cross-API import for it). The
// generic adapter-side helpers `events.ResolveCapabilityValue`/
// `events.RecordCapabilityApplied` ARE reused as-is by reqreply's adapter
// dispatch code (fully generic, no events-specific types beyond the
// trivially-structural `CapabilityName` interface) — only the
// declaration-side pieces above are duplicated. Reuses the SAME sealed
// adapter-owned Capability values [api/events] already ships
// (`mqtt5.QoS`/`mqtt5.Retained`, `zeromq.HWM`/`zeromq.Conflate`) — zero
// new adapter-side capability types needed.
//
// # Forward/Bounce
//
// A handler signals "route X should handle this instead" within the SAME
// correlation-ID round trip by returning the error [Forward] produces — the
// SERVER publishes a reserved wire signal, and [Client.Call]/[Client.CallAsync]'s
// CENTRALIZED chain-following loop republishes and decodes the TARGET route's
// own response, transparently to the caller:
//
//	func(ctx context.Context, req LegacyAddReq) (LegacyAddResp, error) {
//	    var zero LegacyAddResp
//	    return zero, reqreply.Forward(sumRoute, SumReq{A: req.X, B: req.Y}, nil, nil)
//	}
//
// Unlike [rest.Redirect], target's Req/Resp are both free to differ from the
// originating route's — no method/body-preservation constraint, no "from"
// parameter. A target route that is only ever reached as a Forward target
// needs its registry entry "warmed" explicitly via [Client.RegisterRoute]
// before the forward is followed. Forward's last parameter, properties, carries
// wire-level properties/headers (e.g. an MQTT5 User Property credential) through
// to the target — see docs/features/reqreply-forward.md for the full reference.
//
// # Dead-Letter Replay
//
// [Client.ReplayDeadLetter]/[Client.ReplayDeadLetterAsync] recover a message a
// route's [DeadLetter] fallback previously captured: given a decoded
// [DeadLetterEnvelope], they resolve its SourceTopic back against the registry
// and reissue Payload VERBATIM (never re-decoded — DeadLetter fires on decode
// failure too, so re-decoding would fail identically):
//
//	envelope, err := reqreply.DecodeDeadLetterEnvelope(dlqMessagePayload)
//	respAny, err := client.ReplayDeadLetter(ctx, envelope)
//	sum := respAny.(SumResp)
//
// [DecodeDeadLetterEnvelope] is the single sanctioned decode path for the raw
// bytes an ops/replay consumer reads off the dead-letter topic — sharing the
// SAME codec [RouteHandle.DeadLetterFor] itself encodes with, so it can never
// drift out of sync. A header-delivered credential captured into
// [DeadLetterEnvelope.Properties] at dead-letter time survives the round trip
// automatically, reusing the SAME wire-level properties mechanism Forward's
// own properties parameter uses — see docs/features/reqreply-dead-letter-replay.md
// for the full reference.
package reqreply
