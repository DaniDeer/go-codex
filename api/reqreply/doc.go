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
//	    reqreply.ErrorReplyMeta{
//	        Code:        "conflict",
//	        Description: "Business conflict.",
//	        Schema:      codex.String().Schema,
//	        SchemaName:  "ConflictError",
//	    },
//	)
//
//	// Register with a Builder to get a RouteHandle and an AsyncAPI 3.0 spec.
//	builder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
//	builder.AddServer("zmq", reqreply.Server{URL: "tcp://localhost:5556", Protocol: "zmq"})
//	// OR: builder.AddServer("mqtt5", reqreply.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
//	handle, err := ComputeRoute.Register(builder)
//
//	// Same handle — works with any request-reply adapter:
//	zmqadapter.Serve(ctx, sock, handle, fn, zmqadapter.ServeOptions{Observer: obs})
//	mqtt5adapter.ServeRequestReply(ctx, client, router, handle, fn, mqtt5.ServeOptions{Observer: obs})
//
//	// AsyncAPI 3.0 spec with request-reply reply: block:
//	doc, _ := builder.AsyncAPISpec()
//	yaml, _ := doc.MarshalYAML()
package reqreply
