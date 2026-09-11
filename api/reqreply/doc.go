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
//	// Register with a Server to get a RouteHandle and an AsyncAPI 3.0 spec.
//	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5556", Protocol: "zmq"})
//	// OR: server.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
//	handle, err := ComputeRoute.Register(server)
//
//	// Same handle — works with any request-reply adapter. Handler/encode
//	// errors matching a declared ErrorPattern get the typed payload as the
//	// reply instead of a plain-text error string:
//	zmqadapter.Serve(ctx, sock, handle, fn, zmqadapter.ServeOptions{Observer: obs})
//	mqtt5adapter.Serve(ctx, client, router, handle, fn, mqtt5.ServeOptions{Observer: obs})
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
// mode), and [mqtt5.Serve]/[zeromq.Serve]/[zeromq.ServeRouter] automatically
// send it on handler/encode failure instead of a plain-text error string.
// [ErrorPattern] also drives the AsyncAPI reply-error channel/operation that
// [ErrorReplyMeta] previously required a separate declaration for — one
// declaration now produces both. [ErrorReplyMeta] remains available
// unchanged for spec-only declarations that need no runtime dispatch.
//
// # Server/Client + Attach (workflow simplification, in progress)
//
// [Server] absorbs [Builder]'s spec-accumulation role (AddServer,
// AddGlobalSecurity, route registration) AND owns request-reply dispatch
// once a [ServerTransport] is attached via [Server.Attach] — mirroring
// [rest.Server]'s identical unification. [Builder]/[NewBuilder] remain
// available as DEPRECATED aliases for [Server]/[NewServer] during the
// migration described in
// [docs/design/d-0004-reqreply-workflow-simplification.md]; existing code using
// [Builder] keeps compiling and behaving identically.
//
// [Route.WithHandler] attaches a domain handler fluently, PRE-registration
// — mirroring [rest.Route.WithHandler]'s real, dominant idiom exactly:
//
//	handle, err := ComputeRoute.WithHandler(computeHandler).Register(server)
//	_ = mqtt5.Attach(server, client, router) // adapter-specific, lands per-adapter
//	err = server.Serve(ctx)                  // dispatches every registered route
//
// [Client] offers a blocking [Client.Call] (accepting EITHER a raw,
// unregistered [Route] — REST-style, [RouteHandle.GlobalSecurity]
// invisible — OR an already-registered *[RouteHandle], with
// [RouteHandle.GlobalSecurity] enforced) AND an additive, non-blocking
// [Client.CallAsync] returning a *[Future][Resp] resolved later,
// asynchronously — needed because reqreply's transport (unlike REST's
// synchronous HTTP) is genuinely asynchronous underneath.
//
// Per-adapter [ServerTransport]/[ClientTransport] implementations (e.g.
// mqtt5.Attach, zeromq.Attach) land incrementally — see the roadmap doc's
// phased implementation plan for the current status of each transport.
package reqreply
