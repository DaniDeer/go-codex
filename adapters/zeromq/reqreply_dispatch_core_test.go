package zeromq

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
)

// TestReqreplyServerDispatchOne_DirectCall proves
// [reqreplyServerSetupCore]/[reqreplyServerDispatchOne] themselves —
// independent of which Serve variant ((*serverTransport).Serve or
// (*routerServerTransport).Serve) calls them — correctly decode a
// request, dispatch to the handler, and encode the reply. Phase 4
// events/reqreply family, Workstream C's own conformance seed, mirroring
// this session's established pattern (mqtt5's/mqtt v3's
// subscribe_dispatch_core_test.go) for this family's reqreply side.
func TestReqreplyServerDispatchOne_DirectCall(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	var callCount int
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		callCount++
		return computeResp{Sum: r.X + r.Y}, nil
	}

	sock := &mockSocket{}
	obs := stats.NoopObserver{}
	setup, err := reqreplyServerSetupCore(route, fn, sock, obs, nil)
	if err != nil {
		t.Fatalf("reqreplyServerSetupCore: %v", err)
	}

	var gotPayload []byte
	sink := reqreplyReplySink{
		label: "TEST",
		sendSuccess: func(respPayload []byte) error {
			gotPayload = respPayload
			return nil
		},
		sendError: func(_ context.Context, err error) {
			t.Fatalf("unexpected error reply: %v", err)
		},
		sendForward: func(_ reqreply.ForwardError) {
			t.Fatal("unexpected forward reply")
		},
	}

	reqreplyServerDispatchOne(context.Background(), setup, sink,
		map[string]FramedSocket{"/compute": sock}, obs, []byte(validComputeJSON), nil)

	if callCount != 1 {
		t.Fatalf("handler call count: want 1, got %d", callCount)
	}
	resp, err := format.JSON(computeRespCodec).Unmarshal(gotPayload)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("Sum: want 7 (3+4), got %d", resp.Sum)
	}
}

// TestReqreplyServerDispatchOne_DecodeFailure_InvokesOnError proves a
// decode failure reaches onError (since no ErrorChannel/DeadLetter is
// declared on this fixture) and that the handler itself is never
// invoked — exercised directly against [reqreplyServerDispatchOne],
// independent of which Serve variant built it.
func TestReqreplyServerDispatchOne_DecodeFailure_InvokesOnError(t *testing.T) {
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	var handlerCalled bool
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		handlerCalled = true
		return computeResp{}, nil
	}

	sock := &mockSocket{}
	obs := stats.NoopObserver{}
	setup, err := reqreplyServerSetupCore(route, fn, sock, obs, nil)
	if err != nil {
		t.Fatalf("reqreplyServerSetupCore: %v", err)
	}

	var gotErr error
	sink := reqreplyReplySink{
		label:       "TEST",
		sendSuccess: func(_ []byte) error { t.Fatal("unexpected success reply"); return nil },
		sendError: func(_ context.Context, err error) {
			gotErr = err
		},
		sendForward: func(_ reqreply.ForwardError) { t.Fatal("unexpected forward reply") },
	}

	var gotOnError ServeError
	onError := func(e ServeError) { gotOnError = e }

	reqreplyServerDispatchOne(context.Background(), setup, sink,
		map[string]FramedSocket{"/compute": sock}, obs, []byte("not valid json"), onError)

	if handlerCalled {
		t.Error("handler must not be called on a decode failure")
	}
	if gotErr == nil {
		t.Error("want sendError invoked with a non-nil error")
	}
	if gotOnError.Kind != KindDecode {
		t.Errorf("onError Kind: want KindDecode, got %v", gotOnError.Kind)
	}
}
