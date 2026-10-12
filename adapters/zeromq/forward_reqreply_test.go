package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// This file tests docs/roadmap/reqreply-forward-bounce.md's Phase 1.4
// zeromq wire signal: a handler returning a [reqreply.ForwardError]
// (constructed via [reqreply.Forward]) must publish/recognize the
// statusForward-tagged reply SHAPE, and must skip ErrorPattern/
// DeadLetter entirely — the "Dispatch ordering" rule.

// forwardTestTargetRoute is the Forward() target used by these tests —
// never itself registered/served; Forward only needs its ClientHandle to
// resolve the topic/encode the payload.
var forwardTestTargetRoute = reqreply.NewRoute[computeReq, computeResp](
	"/compute-forward-target", computeReqCodec, computeRespCodec,
)

// ── Server-publish side: serverTransport.Serve (REQ/REP) ────────────────

// TestServerTransport_Serve_HandlerForward_SendsForwardWireSignal_SkipsErrorPatternAndDeadLetter
// declares BOTH an ErrorPattern AND a DeadLetter on the SAME route as the
// handler that returns a Forward — proves the skip is real (neither
// fires), not merely untested.
func TestServerTransport_Serve_HandlerForward_SendsForwardWireSignal_SkipsErrorPatternAndDeadLetter(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(forwardTestTargetRoute, computeReq{X: 9, Y: 10}, nil, nil)
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-forward", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-forward/dlq"),
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := fwdRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	dlqSock := &mockSocket{}
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{
		"/compute-forward":     repSock,
		"/compute-forward/dlq": dlqSock,
	}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	handle := fwdRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if err := reqSock.SendFrames([][]byte{payload}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}

	reqSock.timeout = 2 * time.Second
	frames, recvErr := reqSock.RecvFrames()
	if recvErr != nil {
		t.Fatalf("RecvFrames: %v", recvErr)
	}
	if len(frames) != 3 {
		t.Fatalf("reply frames = %d, want 3 ([statusForward, targetTopic, payload]), got %v", len(frames), frames)
	}
	if string(frames[0]) != string(statusForward) {
		t.Errorf("frames[0] = %q, want %q", frames[0], statusForward)
	}
	if string(frames[1]) != "/compute-forward-target" {
		t.Errorf("frames[1] (target topic) = %q, want %q", frames[1], "/compute-forward-target")
	}
	targetHandle := forwardTestTargetRoute.ClientHandle()
	decoded, decodeErr := targetHandle.Decode(frames[2])
	if decodeErr != nil {
		t.Fatalf("Decode(frames[2]): %v", decodeErr)
	}
	if decoded != (computeReq{X: 9, Y: 10}) {
		t.Errorf("decoded forward payload = %+v, want {X:9 Y:10}", decoded)
	}

	// No extra dead-letter publish must have happened.
	time.Sleep(100 * time.Millisecond)
	dlqSock.mu.Lock()
	defer dlqSock.mu.Unlock()
	if len(dlqSock.sentFrames) != 0 {
		t.Errorf("want NO dead-letter publish for a Forward, got %d", len(dlqSock.sentFrames))
	}
}

// ── Server-publish side: routerServerTransport.Serve (ROUTER/DEALER) ────

func TestRouterServerTransport_Serve_HandlerForward_SendsForwardWireSignal_SkipsErrorPatternAndDeadLetter(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(forwardTestTargetRoute, computeReq{X: 11, Y: 12}, nil, nil)
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-router-forward", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-router-forward/dlq"),
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := fwdRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	dlqSock := &mockSocket{}
	if err := server.Attach(NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{
		"/compute-router-forward":     routerSock,
		"/compute-router-forward/dlq": dlqSock,
	}})); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	handle := fwdRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if err := dealerSock.SendFrames([][]byte{emptyDelimiter, payload}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}

	dealerSock.timeout = 2 * time.Second
	frames, recvErr := dealerSock.RecvFrames()
	if recvErr != nil {
		t.Fatalf("RecvFrames: %v", recvErr)
	}
	// dealerSocket.RecvFrames strips the leading identity frame the
	// router addressed the reply to, leaving [delimiter, statusForward,
	// targetTopic, payload].
	if len(frames) != 4 {
		t.Fatalf("reply frames = %d, want 4 ([delimiter, statusForward, targetTopic, payload]), got %v", len(frames), frames)
	}
	if string(frames[1]) != string(statusForward) {
		t.Errorf("frames[1] = %q, want %q", frames[1], statusForward)
	}
	if string(frames[2]) != "/compute-forward-target" {
		t.Errorf("frames[2] (target topic) = %q, want %q", frames[2], "/compute-forward-target")
	}
	targetHandle := forwardTestTargetRoute.ClientHandle()
	decoded, decodeErr := targetHandle.Decode(frames[3])
	if decodeErr != nil {
		t.Fatalf("Decode(frames[3]): %v", decodeErr)
	}
	if decoded != (computeReq{X: 11, Y: 12}) {
		t.Errorf("decoded forward payload = %+v, want {X:11 Y:12}", decoded)
	}

	time.Sleep(100 * time.Millisecond)
	dlqSock.mu.Lock()
	defer dlqSock.mu.Unlock()
	if len(dlqSock.sentFrames) != 0 {
		t.Errorf("want NO dead-letter publish for a Forward, got %d", len(dlqSock.sentFrames))
	}
}

// ── Client-recognize side: clientTransport.call() ────────────────────────

func TestClientTransport_Call_ForwardReply_ReturnsForwardError(t *testing.T) {
	handle := newRouteHandle()
	sock := &mockSocket{inFrames: [][][]byte{{statusForward, []byte("/compute-forward-target"), []byte(`{"x":9,"y":10}`)}}}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}})

	_, callErr := reqreply.CallWithTransport(context.Background(), transport, handle, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("CallWithTransport: want a non-nil error (ForwardError), got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("Call error: want reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.TargetTopic != "/compute-forward-target" {
		t.Errorf("ForwardError.TargetTopic = %q, want %q", fwd.TargetTopic, "/compute-forward-target")
	}
	if string(fwd.Payload) != `{"x":9,"y":10}` {
		t.Errorf("ForwardError.Payload = %s, want %s", fwd.Payload, `{"x":9,"y":10}`)
	}
}

// ── Client-recognize side: clientTransport.CallRaw(, nil) ──────────────────────

func TestClientTransport_CallRaw_ForwardReply_ReturnsForwardError(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{{statusForward, []byte("/compute-forward-target"), []byte(`{"x":9,"y":10}`)}}}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-forward": sock}})

	_, callErr := transport.CallRaw(context.Background(), "/compute-callraw-forward", []byte(`{"x":1,"y":2}`), nil)
	if callErr == nil {
		t.Fatal("CallRaw: want a non-nil error (ForwardError), got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("CallRaw error: want reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.TargetTopic != "/compute-forward-target" {
		t.Errorf("ForwardError.TargetTopic = %q, want %q", fwd.TargetTopic, "/compute-forward-target")
	}
	if string(fwd.Payload) != `{"x":9,"y":10}` {
		t.Errorf("ForwardError.Payload = %s, want %s", fwd.Payload, `{"x":9,"y":10}`)
	}
}

// ── Client-recognize side: dealerClientTransport.call() ───────────────────

func TestDealerClientTransport_Call_ForwardReply_ReturnsForwardError(t *testing.T) {
	handle := newRouteHandle()
	sock := &mockSocket{inFrames: [][][]byte{dealerForwardReply("/compute-forward-target", `{"x":9,"y":10}`)}}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}})

	_, callErr := reqreply.CallWithTransport(context.Background(), transport, handle, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("CallWithTransport: want a non-nil error (ForwardError), got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("Call error: want reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.TargetTopic != "/compute-forward-target" {
		t.Errorf("ForwardError.TargetTopic = %q, want %q", fwd.TargetTopic, "/compute-forward-target")
	}
	if string(fwd.Payload) != `{"x":9,"y":10}` {
		t.Errorf("ForwardError.Payload = %s, want %s", fwd.Payload, `{"x":9,"y":10}`)
	}
}

// ── Client-recognize side: dealerClientTransport.CallRaw(, nil) ────────────────

func TestDealerClientTransport_CallRaw_ForwardReply_ReturnsForwardError(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{dealerForwardReply("/compute-forward-target", `{"x":9,"y":10}`)}}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-dealer-forward": sock}})

	_, callErr := transport.CallRaw(context.Background(), "/compute-callraw-dealer-forward", []byte(`{"x":1,"y":2}`), nil)
	if callErr == nil {
		t.Fatal("CallRaw: want a non-nil error (ForwardError), got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("CallRaw error: want reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.TargetTopic != "/compute-forward-target" {
		t.Errorf("ForwardError.TargetTopic = %q, want %q", fwd.TargetTopic, "/compute-forward-target")
	}
	if string(fwd.Payload) != `{"x":9,"y":10}` {
		t.Errorf("ForwardError.Payload = %s, want %s", fwd.Payload, `{"x":9,"y":10}`)
	}
}

// dealerForwardReply builds a DEALER-socket-shaped Forward reply:
// [delimiter, statusForward, targetTopic, payload] — mirrors dealerReply's
// identical ordinary/error-reply helper (adapter_test.go).
func dealerForwardReply(targetTopic, payload string) [][]byte {
	return [][]byte{emptyDelimiter, statusForward, []byte(targetTopic), []byte(payload)}
}

// TestClient_Call_FollowsForward_EndToEndViaRealZeroMQAdapter is deep-
// dive-bug-hunt's own re-verification that reqreply.Client.Call's
// CENTRALIZED chain-following loop (api/reqreply/forward.go's
// followForward) ACTUALLY works against the REAL zeromq wire signal
// (REQ/REP), not just the synthetic forwardFakeClientTransport
// api/reqreply/forward_resolve_test.go exercises — mirrors adapters/
// mqtt5's identical end-to-end test, closing the SAME test-coverage gap
// for this adapter: every OTHER Forward test in this package stops at
// the TRANSPORT level.
func TestClient_Call_FollowsForward_EndToEndViaRealZeroMQAdapter(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	originatingHandler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(forwardTestTargetRoute, computeReq{X: req.X * 10, Y: req.Y * 10}, nil, nil)
	}
	originatingRoute := reqreply.NewRoute[computeReq, computeResp]("compute/e2e-forward-originating", computeReqCodec, computeRespCodec)
	if _, err := originatingRoute.WithHandler(originatingHandler).Register(server); err != nil {
		t.Fatalf("Register(originating): %v", err)
	}
	targetHandler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: (req.X + req.Y) * 2}, nil
	}
	if _, err := forwardTestTargetRoute.WithHandler(targetHandler).Register(server); err != nil {
		t.Fatalf("Register(target): %v", err)
	}

	originatingRepSock, originatingReqSock := newChanSocketPair()
	targetRepSock, targetReqSock := newChanSocketPair()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{
		"compute/e2e-forward-originating": originatingRepSock,
		"/compute-forward-target":         targetRepSock,
	}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()

	client := reqreply.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{
		"compute/e2e-forward-originating": originatingReqSock,
		"/compute-forward-target":         targetReqSock,
	}})); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	// forwardTestTargetRoute is ONLY ever reached as a Forward target in
	// this test — warm the registry explicitly, exactly the documented
	// use case.
	if err := client.RegisterRoute(forwardTestTargetRoute); err != nil {
		t.Fatalf("RegisterRoute(target): %v", err)
	}

	respAny, err := client.Call(context.Background(), originatingRoute, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: want the REAL wire-level forward followed end to end, got error: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("Call resp type = %T, want computeResp", respAny)
	}
	// originating forwards {X:10, Y:20} to target: (10+20)*2 = 60.
	if resp.Sum != 60 {
		t.Errorf("resp.Sum = %d, want 60 (forwarded through the REAL zeromq wire signal + chain-following loop)", resp.Sum)
	}

	cancel()
	if serveErr := <-errCh; serveErr != nil {
		t.Fatalf("Serve: %v", serveErr)
	}
}
