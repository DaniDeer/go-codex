package zeromq

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// testServe/testCall/testServeRouter/testCallDealer mirror the exact
// signatures of the now-DELETED Serve[Req,Resp]/Call[Req,Resp]/
// ServeRouter[Req,Resp]/CallDealer[Req,Resp] escape hatches
// (docs/design/d-0006-protocol-native-capabilities.md's Phase 5a) — a
// thin, test-only shim reducing this package's existing call sites to a
// single mechanical rename instead of restructuring every argument
// list. Real callers use [reqreply.ServeWithTransport]/
// [reqreply.CallWithTransport] directly against a transport built via
// [NewServerTransport]/[NewRouterServerTransport]/[NewClientTransport]/
// [NewDealerClientTransport] — these helpers exist ONLY to keep this
// package's existing test suite's assertions (which were never about
// Serve/Call's OWN construction shape, only the dispatch behavior
// underneath, unchanged by the deletion) exercising that exact dispatch
// without a wholesale per-call-site rewrite.
func testServe[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	transport := NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}, Serve: opts})
	return reqreply.ServeWithTransport(ctx, transport, handle, fn)
}

func testCall[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}, Call: opts})
	return reqreply.CallWithTransport(ctx, transport, handle, req)
}

func testServeRouter[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	transport := NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}, Serve: opts})
	return reqreply.ServeWithTransport(ctx, transport, handle, fn)
}

func testCallDealer[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}, Call: opts})
	return reqreply.CallWithTransport(ctx, transport, handle, req)
}
