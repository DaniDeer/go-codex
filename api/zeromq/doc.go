// Package zeromq provides an AsyncAPI 3.0 spec builder for ZeroMQ REQ/REP
// socket contracts declared with [api/rest].
//
// It follows the same declare → register → handle pattern as [api/events] and
// [api/rest]: route declarations are unchanged and [Register] returns the same
// [rest.RouteHandle] used with [adapters/zeromq].
//
// # Usage
//
//	// Declare once (same as Phase 1 / HTTP adapter)
//	var ComputeRoute = rest.NewRoute[ComputeReq, ComputeResp](
//	    "POST", "/compute", reqCodec, respCodec,
//	    rest.RouteMeta{OperationID: "compute"},
//	)
//
//	// Register with ZMQ builder to get AsyncAPI spec
//	zmqBuilder := zeromq.NewBuilder(zeromq.Info{Title: "Compute API", Version: "1.0.0"})
//	zmqBuilder.AddServer("zmq", zeromq.Server{URL: "tcp://localhost:5556", Protocol: "zmq"})
//	handle, _ := zeromq.Register(zmqBuilder, ComputeRoute,
//	    zeromq.SocketMeta{OperationID: "compute", Summary: "Add two integers."})
//
//	// Adapter: EXACTLY the same as Phase 1
//	zmqadapter.Serve(ctx, sock, handle, fn, zmqadapter.ServeOptions{Observer: obs})
//	zmqadapter.Call(ctx, sock, handle, req, zmqadapter.CallOptions{Observer: obs})
//
//	// AsyncAPI spec
//	doc, _ := zmqBuilder.AsyncAPISpec()
//	yaml, _ := doc.MarshalYAML()
package zeromq
