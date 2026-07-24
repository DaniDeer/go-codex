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
//	// Register with a Builder to get a RouteHandle and an AsyncAPI 3.0 spec.
//	builder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	builder.AddServer("zmq", reqreply.Server{URL: "tcp://localhost:5556", Protocol: "zmq"})
//	// OR: builder.AddServer("mqtt5", reqreply.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
//	handle, err := ComputeRoute.Register(builder)
//
//	// Same handle — works with any request-reply adapter. Handler/encode
//	// errors matching a declared ErrorPattern get the typed payload as the
//	// reply instead of a plain-text error string:
//	zmqadapter.Serve(ctx, sock, handle, fn, zmqadapter.ServeOptions{Observer: obs})
//	mqtt5adapter.Serve(ctx, client, router, handle, fn, mqtt5.ServeOptions{Observer: obs})
//
//	// AsyncAPI 3.0 spec with request-reply reply: block, plus the
//	// ErrorPattern-derived reply-error channel/operation:
//	doc, _ := builder.AsyncAPISpec()
//	yaml, _ := doc.MarshalYAML()
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
package reqreply
