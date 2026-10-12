package zeromq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// ── docs/roadmap/reqreply-client-registry.md Phase 0: CallRaw ──────────────
//
// CallRaw is Phase 0 infrastructure with no real consumer yet (docs/
// roadmap/reqreply-forward-bounce.md's chain-following loop, its first
// real caller, is not implemented) — these tests exercise it directly
// against a real Server+Attach round trip (mirrors
// TestAttachServer_AttachClient_RoundTrip's/
// TestAttachRouterServer_AttachDealerClient_RoundTrip's setup), the SAME
// "stub consumer" testing strategy the roadmap doc's own "Next step"
// section describes.

// TestClientTransport_CallRaw_PropertiesParam_SafeNoop is Phase 1.5's
// own wire-level properties addendum test (docs/roadmap/
// reqreply-forward-bounce.md's "Addendum"): zeromq's reqreply transport
// has no property/header wire mechanism — CallRaw's properties
// parameter is accepted ONLY for reqreply.ClientTransport interface
// conformance and is a confirmed, deliberate no-op. This proves that
// no-op is SAFE (no panic, identical reply) rather than just an
// untested assumption, mirroring TestClientTransport_CallRaw_
// OrdinaryReply's own round trip but supplying a non-empty properties
// map.
func TestClientTransport_CallRaw_PropertiesParam_SafeNoop(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	repSock, reqSock := newChanSocketPair()

	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute": repSock}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": reqSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	route := reqreply.NewRoute[computeReq, computeResp]("/compute", computeReqCodec, computeRespCodec)
	handle := route.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	replyBytes, err := transport.CallRaw(callCtx, "/compute", payload, map[string]string{"X-Replay-Reason": "retry"})
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	resp, err := handle.DecodeResponse(replyBytes)
	if err != nil {
		t.Fatalf("DecodeResponse(CallRaw's reply): %v", err)
	}
	if resp.Sum != 7 {
		t.Fatalf("resp.Sum = %d, want 7 (properties param must not change the reply)", resp.Sum)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestClientTransport_CallRaw_OrdinaryReply(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	repSock, reqSock := newChanSocketPair()

	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute": repSock}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": reqSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	route := reqreply.NewRoute[computeReq, computeResp]("/compute", computeReqCodec, computeRespCodec)
	handle := route.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	replyBytes, err := transport.CallRaw(callCtx, "/compute", payload, nil)
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	resp, err := handle.DecodeResponse(replyBytes)
	if err != nil {
		t.Fatalf("DecodeResponse(CallRaw's reply): %v", err)
	}
	if resp.Sum != 7 {
		t.Fatalf("resp.Sum = %d, want 7", resp.Sum)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestClientTransport_CallRaw_ErrorReply_FallsBackToPlainTextError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	unrelatedErr := errors.New("unrelated failure")
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, unrelatedErr
	}
	errRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-callraw-err", computeReqCodec, computeRespCodec)
	if _, err := errRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-err": repSock}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-err": reqSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	handle := errRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := transport.CallRaw(callCtx, "/compute-callraw-err", payload, nil)
	if callErr == nil {
		t.Fatal("CallRaw: want an error for a handler failure, got nil")
	}
	var raw reqreply.ForwardRawErrorReply
	if !errors.As(callErr, &raw) {
		t.Fatalf("CallRaw error: want reqreply.ForwardRawErrorReply, got %v (%T)", callErr, callErr)
	}
	if raw.Code != "" {
		t.Errorf("ForwardRawErrorReply.Code: want \"\" (no declared ErrorPattern matched), got %q", raw.Code)
	}
	if !strings.Contains(string(raw.Body), unrelatedErr.Error()) {
		t.Errorf("ForwardRawErrorReply.Body: want plain-text fallback containing %q, got: %s", unrelatedErr.Error(), raw.Body)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

// TestClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode
// confirms CallRaw's own route-free code EXTRACTION (3-frame
// [status,code,body] reply) when a declared ErrorPattern DOES match
// server-side — CallRaw still cannot DECODE the matched pattern itself
// (no route), but must surface the raw code so a future caller holding
// the registry-resolved target RouteHandle can finish that decode via
// RouteHandle.DecodeErrorFor(Code, Body).
func TestClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveZmqConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-callraw-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-ep": repSock}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-ep": reqSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	handle := epRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := transport.CallRaw(callCtx, "/compute-callraw-ep", payload, nil)
	if callErr == nil {
		t.Fatal("CallRaw: want an error for a handler failure, got nil")
	}
	var raw reqreply.ForwardRawErrorReply
	if !errors.As(callErr, &raw) {
		t.Fatalf("CallRaw error: want reqreply.ForwardRawErrorReply, got %v (%T)", callErr, callErr)
	}
	if raw.Code != "conflict" {
		t.Errorf("ForwardRawErrorReply.Code: want %q, got %q", "conflict", raw.Code)
	}
	if len(raw.Body) == 0 {
		t.Error("ForwardRawErrorReply.Body: want non-empty encoded serveZmqErrPayload body")
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

// TestDealerClientTransport_CallRaw_PropertiesParam_SafeNoop is the
// dealer-transport sibling of TestClientTransport_CallRaw_
// PropertiesParam_SafeNoop — same confirmed no-op proof, via the
// ROUTER/DEALER socket pair instead.
func TestDealerClientTransport_CallRaw_PropertiesParam_SafeNoop(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))

	if err := server.Attach(NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{"/compute": routerSock}})); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": dealerSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	route := reqreply.NewRoute[computeReq, computeResp]("/compute", computeReqCodec, computeRespCodec)
	handle := route.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 5, Y: 6})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	replyBytes, err := transport.CallRaw(callCtx, "/compute", payload, map[string]string{"X-Replay-Reason": "retry"})
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	resp, err := handle.DecodeResponse(replyBytes)
	if err != nil {
		t.Fatalf("DecodeResponse(CallRaw's reply): %v", err)
	}
	if resp.Sum != 11 {
		t.Fatalf("resp.Sum = %d, want 11 (properties param must not change the reply)", resp.Sum)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestDealerClientTransport_CallRaw_OrdinaryReply(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))

	if err := server.Attach(NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{"/compute": routerSock}})); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": dealerSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	route := reqreply.NewRoute[computeReq, computeResp]("/compute", computeReqCodec, computeRespCodec)
	handle := route.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 5, Y: 6})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	replyBytes, err := transport.CallRaw(callCtx, "/compute", payload, nil)
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	resp, err := handle.DecodeResponse(replyBytes)
	if err != nil {
		t.Fatalf("DecodeResponse(CallRaw's reply): %v", err)
	}
	if resp.Sum != 11 {
		t.Fatalf("resp.Sum = %d, want 11", resp.Sum)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestDealerClientTransport_CallRaw_ErrorReply_FallsBackToPlainTextError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	unrelatedErr := errors.New("unrelated dealer failure")
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, unrelatedErr
	}
	errRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-callraw-dealer-err", computeReqCodec, computeRespCodec)
	if _, err := errRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	if err := server.Attach(NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-dealer-err": routerSock}})); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-dealer-err": dealerSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	handle := errRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := transport.CallRaw(callCtx, "/compute-callraw-dealer-err", payload, nil)
	if callErr == nil {
		t.Fatal("CallRaw: want an error for a handler failure, got nil")
	}
	var raw reqreply.ForwardRawErrorReply
	if !errors.As(callErr, &raw) {
		t.Fatalf("CallRaw error: want reqreply.ForwardRawErrorReply, got %v (%T)", callErr, callErr)
	}
	if raw.Code != "" {
		t.Errorf("ForwardRawErrorReply.Code: want \"\" (no declared ErrorPattern matched), got %q", raw.Code)
	}
	if !strings.Contains(string(raw.Body), unrelatedErr.Error()) {
		t.Errorf("ForwardRawErrorReply.Body: want plain-text fallback containing %q, got: %s", unrelatedErr.Error(), raw.Body)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

// TestDealerClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode
// mirrors TestClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode,
// adapted to DEALER's 4-frame [delimiter,status,code,body] matched-reply
// framing.
func TestDealerClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveZmqConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-callraw-dealer-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	if err := server.Attach(NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-dealer-ep": routerSock}})); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-callraw-dealer-ep": dealerSock}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	handle := epRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := transport.CallRaw(callCtx, "/compute-callraw-dealer-ep", payload, nil)
	if callErr == nil {
		t.Fatal("CallRaw: want an error for a handler failure, got nil")
	}
	var raw reqreply.ForwardRawErrorReply
	if !errors.As(callErr, &raw) {
		t.Fatalf("CallRaw error: want reqreply.ForwardRawErrorReply, got %v (%T)", callErr, callErr)
	}
	if raw.Code != "conflict" {
		t.Errorf("ForwardRawErrorReply.Code: want %q, got %q", "conflict", raw.Code)
	}
	if len(raw.Body) == 0 {
		t.Error("ForwardRawErrorReply.Body: want non-empty encoded serveZmqErrPayload body")
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestClientTransport_CallRaw_MissingSocket(t *testing.T) {
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{}})
	_, err := transport.CallRaw(context.Background(), "/no-such-topic", []byte("payload"), nil)
	var missingErr MissingSocketError
	if !errors.As(err, &missingErr) {
		t.Fatalf("CallRaw: want MissingSocketError, got %v (%T)", err, err)
	}
	if missingErr.Topic != "/no-such-topic" {
		t.Errorf("MissingSocketError.Topic: want %q, got %q", "/no-such-topic", missingErr.Topic)
	}
}

// ── deep-dive-bug-hunt (api/reqreply focus): CallRaw missing TraceObserver span ──

func TestClientTransport_CallRaw_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	handle := newRouteHandle()
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}
	payload, err := handle.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}, Call: CallOptions{Observer: obs}})
	if _, err := transport.CallRaw(context.Background(), handle.Topic, payload, nil); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.request" {
		t.Fatalf("expected StartSpan with 'zmq.request' (mirrors Call's own TraceObserver convention), got %v", obs.startSpanOps)
	}
}

func TestDealerClientTransport_CallRaw_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	handle := newRouteHandle()
	sock := &mockSocket{inFrames: [][][]byte{dealerReply("ok", `{"sum":7}`)}}
	payload, err := handle.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{handle.Topic: sock}, Call: CallOptions{Observer: obs}})
	if _, err := transport.CallRaw(context.Background(), handle.Topic, payload, nil); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.request" {
		t.Fatalf("expected StartSpan with 'zmq.request' (mirrors Call's own TraceObserver convention), got %v", obs.startSpanOps)
	}
}

// ── deep-dive-bug-hunt (api/reqreply focus): CallRaw ignores Capabilities ──
//
// Mirrors capability_reqreply_test.go's EXISTING Call-side coverage
// (TestCall_CapabilitiesAppliedViaExistingApplyCapabilities/
// TestDealerCall_CapabilitiesAppliedViaExistingApplyCapabilities) —
// CallRaw is a SEPARATE dispatch implementation from call() that must
// independently apply the SAME transport-level CallOptions.Capabilities
// (HWM/Conflate are socket-level, route-independent configuration; a
// caller configuring them expects them honored regardless of whether
// the call goes through Call or CallRaw).

func TestClientTransport_CallRaw_CapabilitiesApplied(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}}
	transport := NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": sock}, Call: CallOptions{Capabilities: []Capability{HWM(46), Conflate(true)}}})

	if _, err := transport.CallRaw(context.Background(), "/compute", []byte(`{"x":1,"y":2}`), nil); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}

	if sock.hwm != 46 {
		t.Errorf("want HWM(46) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
}

func TestDealerClientTransport_CallRaw_CapabilitiesApplied(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{inFrames: [][][]byte{dealerReply("ok", `{"sum":7}`)}}}
	transport := NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": sock}, Call: CallOptions{Capabilities: []Capability{HWM(47), Conflate(true)}}})

	if _, err := transport.CallRaw(context.Background(), "/compute", []byte(`{"x":1,"y":2}`), nil); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}

	if sock.hwm != 47 {
		t.Errorf("want HWM(47) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
}
