package reqreply

import (
	"context"
	"fmt"
)

// ServeWithTransport is the API-LAYER-OWNED "attach and drive" verb for
// serving exactly ONE route against an ALREADY-BUILT [ServerTransport] —
// mirrors [events.SubscribeHandle]'s shape exactly (docs/roadmap/
// capability-requirement-composition.md's Phase 5a). It replaces the
// former adapter-owned `mqtt5.Serve[Req,Resp]`/`zeromq.Serve[Req,Resp]`/
// `zeromq.ServeRouter[Req,Resp]` escape hatches, which built the SAME
// type-erased transport internally and delegated identically — this is
// a PURE RELOCATION, not a redesign: the function body IS the deleted
// escape hatch's own body, unchanged.
//
// Build transport via an adapter's own `New*Transport(opts) ServerTransport`
// factory (e.g. [mqtt5.NewServerTransport], [zeromq.NewServerTransport]/
// [zeromq.NewRouterServerTransport]) — the SAME factory
// [Server.Attach] consumes — then call ServeWithTransport directly,
// WITHOUT constructing a [*Server] at all, for a caller who wants
// compile-time type safety and zero [*Server] registration ceremony for
// a single route.
//
//	transport := mqtt5.NewServerTransport(mqtt5.ServerTransportOptions{
//	    Client: client, Router: router,
//	    Serve:  mqtt5.ServeOptions{Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce}},
//	})
//	err := reqreply.ServeWithTransport(ctx, transport, computeHandle, computeHandler)
func ServeWithTransport[Req, Resp any](
	ctx context.Context,
	transport ServerTransport,
	handle *RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
) error {
	return transport.Serve(ctx, handle, fn)
}

// CallWithTransport is the API-LAYER-OWNED "attach and drive" verb for
// calling exactly ONE route against an ALREADY-BUILT [ClientTransport] —
// mirrors [events.PublishHandle]'s shape exactly (docs/roadmap/
// capability-requirement-composition.md's Phase 5a). It replaces the
// former adapter-owned `mqtt5.Call[Req,Resp]`/`mqtt5.CallHandle[Req,Resp]`/
// `zeromq.Call[Req,Resp]`/`zeromq.CallHandle[Req,Resp]`/
// `zeromq.CallDealer[Req,Resp]` escape hatches, which built the SAME
// type-erased transport internally and delegated identically — this is
// a PURE RELOCATION, not a redesign.
//
// Build transport via an adapter's own `New*Transport(opts) ClientTransport`
// factory (e.g. [mqtt5.NewClientTransport], [zeromq.NewClientTransport]/
// [zeromq.NewDealerClientTransport]) — the SAME factory
// [Client.Attach] consumes — then call CallWithTransport directly,
// WITHOUT constructing a [*Client] at all.
//
//	transport := mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: client, Router: router})
//	resp, err := reqreply.CallWithTransport(ctx, transport, computeHandle, ComputeReq{X: 1, Y: 2})
func CallWithTransport[Req, Resp any](
	ctx context.Context,
	transport ClientTransport,
	handle *RouteHandle[Req, Resp],
	req Req,
	opts ...ClientCallOptions,
) (Resp, error) {
	var zero Resp
	respAny, err := transport.Call(ctx, handle, req, opts...)
	if err != nil {
		return zero, err
	}
	resp, ok := respAny.(Resp)
	if !ok {
		return zero, TransportTypeMismatchError{Topic: handle.Topic, Want: fmt.Sprintf("%T", zero), Got: fmt.Sprintf("%T", respAny)}
	}
	return resp, nil
}
