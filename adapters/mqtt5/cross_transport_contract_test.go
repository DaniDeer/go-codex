package mqtt5

import (
	"context"
	"testing"
	"time"
)

// TestCrossTransportContract_ComputeRoundTrip is Phase 3 (case 4) of
// docs/roadmap/adapter-integration-architecture.md's roadmap: "A
// reqreply contract bound to two different transports behaves
// consistently." It declares the EXACT SAME logical contract as
// adapters/zeromq's TestCrossTransportContract_ComputeRoundTrip (same
// computeReq/computeResp types, same "/compute" RouteHandle, same
// X+Y=Sum handler semantics — see that test's doc comment for the full
// cross-reference) and proves it round-trips correctly over MQTT5's own
// in-process mock broker.
//
// Unlike the zeromq counterpart, mqtt5's existing mockClient/mockRouter
// test harness already supports a true, fully-wired, in-process Serve+Call
// round trip (this is exactly what TestCall_ValidRoundTrip already proves)
// — so this test is a genuine end-to-end proof, not a split
// encode/decode-only proof like zeromq's. That asymmetry in achievable
// rigor between the two mock harnesses is itself noted, not hidden: no
// live MQTT5/ZeroMQ broker is available in this environment for one
// single shared cross-package test, so each adapter's own idiomatic mock
// harness is used instead, with this explicit comment establishing both
// tests represent the same contract.
func TestCrossTransportContract_ComputeRoundTrip(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{}); err != nil {
		t.Fatalf("Serve setup failed: %v", err)
	}

	resp, err := testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{
			ReplyTopicPrefix: "replies",
			Timeout:          2 * time.Second,
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Fatalf("expected Sum=7 (same contract as adapters/zeromq's mirrored test), got %d", resp.Sum)
	}
}
