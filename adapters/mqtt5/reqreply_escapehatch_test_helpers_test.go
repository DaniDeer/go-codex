package mqtt5

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// testServe/testCall mirror the exact signatures of the now-DELETED
// Serve[Req,Resp]/Call[Req,Resp] escape hatches (docs/roadmap/
// d-0006-protocol-native-capabilities.md's Phase 5a) — a thin,
// test-only shim reducing this package's ~90 existing call sites (both
// exercising the SAME underlying serverTransport.Serve/clientTransport.
// Call dispatch, unchanged by the deletion) to a single mechanical
// rename instead of restructuring every argument list. Real callers
// use [reqreply.ServeWithTransport]/[reqreply.CallWithTransport]
// directly against a transport built via [NewServerTransport]/
// [NewClientTransport] — these helpers exist ONLY to keep this
// package's existing test suite's assertions (which were never about
// Serve/Call's OWN construction shape, only the dispatch behavior
// underneath) exercising that exact, unchanged dispatch without a
// wholesale per-call-site rewrite.
func testServe[Req, Resp any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	fn func(context.Context, Req) (Resp, error),
	opts ServeOptions,
) error {
	transport := NewServerTransport(ServerTransportOptions{Client: client, Router: router, Serve: opts})
	return reqreply.ServeWithTransport(ctx, transport, handle, fn)
}

func testCall[Req, Resp any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *reqreply.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	transport := NewClientTransport(ClientTransportOptions{Client: client, Router: router, Call: opts})
	return reqreply.CallWithTransport(ctx, transport, handle, req)
}
