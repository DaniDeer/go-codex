package zeromq

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/format"
)

// TestCrossTransportContract_ComputeRoundTrip is Phase 3 (case 4) of
// docs/roadmap/adapter-integration-architecture.md's roadmap: "A
// reqreply contract bound to two different transports behaves
// consistently." It declares the EXACT SAME logical contract as
// adapters/mqtt5's TestCrossTransportContract_ComputeRoundTrip (same
// computeReq/computeResp types, same "/compute" RouteHandle, same
// X+Y=Sum handler semantics) and proves it against ZeroMQ's own
// in-process mock socket harness.
//
// Unlike the mqtt5 counterpart, zeromq's existing mockSocket test
// harness (see adapter_test.go) is one-way/scripted (canned inFrames in,
// recorded sentFrames out) rather than a true wired Serve->Call loopback
// — this mirrors how zeromq's OWN existing suite already tests Serve and
// Call entirely independently (TestServe_ValidRoundTrip,
// TestCall_ValidRoundTrip), never end-to-end in one test. So this test
// proves the SAME contract on BOTH halves instead: Serve's encode path
// (handler computes the correct Sum and it is reflected verbatim in the
// reply frame) and Call's decode path (a reply frame carrying the same
// Sum is decoded into the correct computeResp) — together equivalent
// proof of the identical request/response semantics mqtt5's single
// wired round trip demonstrates directly. No live MQTT5/ZeroMQ broker is
// available in this environment for one single shared cross-package
// test, hence this reduced-rigor, explicitly cross-referenced mirrored
// pair rather than one literally shared test.
func TestCrossTransportContract_ComputeRoundTrip(t *testing.T) {
	t.Run("serve_encodes_same_contract", func(t *testing.T) {
		sock := &mockSocket{
			inFrames: [][][]byte{
				{[]byte(validComputeJSON)}, // {"x":3,"y":4}
			},
		}
		if err := runServe(sock, func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		}, ServeOptions{}); err != nil {
			t.Fatalf("Serve setup failed: %v", err)
		}

		if len(sock.sentFrames) != 1 {
			t.Fatalf("expected 1 reply, got %d", len(sock.sentFrames))
		}
		if string(sock.sentFrames[0][0]) != "ok" {
			t.Fatalf("expected status frame 'ok', got %q", sock.sentFrames[0][0])
		}
		got, err := format.JSON(computeRespCodec).Unmarshal(sock.sentFrames[0][1])
		if err != nil {
			t.Fatalf("decode reply payload: %v", err)
		}
		if got.Sum != 7 {
			t.Fatalf("expected Sum=7 (same contract as adapters/mqtt5's mirrored test), got %d", got.Sum)
		}
	})

	t.Run("call_decodes_same_contract", func(t *testing.T) {
		sock := &mockSocket{
			inFrames: [][][]byte{
				{[]byte("ok"), []byte(`{"sum":7}`)},
			},
		}
		result, err := testCall(context.Background(), sock, newRouteHandle(),
			computeReq{X: 3, Y: 4}, CallOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Sum != 7 {
			t.Fatalf("expected Sum=7 (same contract as adapters/mqtt5's mirrored test), got %d", result.Sum)
		}
	})
}
