package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/stats"
)

// TestReqreplyClientCallOne_DirectCall proves
// [reqreplyClientRecoverAndResolvePath]/[reqreplyClientResolveDispatchSetup]/
// [reqreplyClientCallOne] themselves — independent of which call variant
// ((*clientTransport).call or (*dealerClientTransport).call) uses
// them — correctly encode a request, send it, decode a scripted reply,
// and return the response. Phase 4 events/reqreply family, Workstream
// C's own client-side conformance seed, mirroring this session's
// established pattern (the server-side reqreply_dispatch_core_test.go,
// and mqtt5's/mqtt v3's subscribe_dispatch_core_test.go).
func TestReqreplyClientCallOne_DirectCall(t *testing.T) {
	handle := newRouteHandle()
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}
	obs := stats.NoopObserver{}
	start := time.Now()

	rv, elem, reqVal, path, err := reqreplyClientRecoverAndResolvePath(handle, computeReq{X: 3, Y: 4}, sock, obs, start, "TEST", CallOptions{})
	if err != nil {
		t.Fatalf("reqreplyClientRecoverAndResolvePath: %v", err)
	}
	setup, err := reqreplyClientResolveDispatchSetup(rv, elem, obs, start, "TEST", path, CallOptions{}, reqreply.ClientCallOptions{})
	if err != nil {
		t.Fatalf("reqreplyClientResolveDispatchSetup: %v", err)
	}

	sink := reqreplyClientSink{
		label: "TEST",
		sendRequest: func(payload []byte) error {
			return sock.SendFrames([][]byte{payload})
		},
		recvReply: func(ctx context.Context) ([][]byte, error) {
			frames, recvErr := reqreplyClientRecvOne(ctx, sock)
			if recvErr != nil {
				return nil, recvErr
			}
			if len(frames) < 2 {
				t.Fatalf("want >= 2 frames, got %d", len(frames))
			}
			return frames, nil
		},
	}

	result, err := reqreplyClientCallOne(context.Background(), setup, sink, obs, start, path, reqVal)
	if err != nil {
		t.Fatalf("reqreplyClientCallOne: %v", err)
	}
	resp, ok := result.(computeResp)
	if !ok {
		t.Fatalf("result type: want computeResp, got %T", result)
	}
	if resp.Sum != 7 {
		t.Errorf("Sum: want 7, got %d", resp.Sum)
	}
}
