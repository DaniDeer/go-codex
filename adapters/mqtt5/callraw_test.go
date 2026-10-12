package mqtt5

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── docs/roadmap/reqreply-client-registry.md Phase 0: CallRaw ──────────────
//
// CallRaw is Phase 0 infrastructure with no real consumer yet (docs/
// roadmap/reqreply-forward-bounce.md's chain-following loop, its first
// real caller, is not implemented) — these tests exercise it directly
// against a real Server+Attach round trip (mirrors
// TestAttachServer_AttachClient_RoundTrip's setup), the SAME "stub
// consumer" testing strategy the roadmap doc's own "Next step" section
// describes.

func TestClientTransport_CallRaw_OrdinaryReply(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := computeRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add")

	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	transport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})

	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	handle := computeRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	replyBytes, err := transport.CallRaw(context.Background(), "compute/add", payload, nil)
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
	if err := <-errCh; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestClientTransport_CallRaw_ErrorReply_FallsBackToPlainTextError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	unrelatedErr := errors.New("unrelated failure")
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, unrelatedErr
	}
	errRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-callraw-err", computeReqCodec, computeRespCodec)
	if _, err := errRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-callraw-err")

	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	transport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})

	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	handle := errRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	_, callErr := transport.CallRaw(context.Background(), "compute/add-callraw-err", payload, nil)
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
	if err := <-errCh; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// TestClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode
// confirms CallRaw's own route-free code EXTRACTION (via
// errorCodeFromUserProperties) when a declared ErrorPattern DOES match
// server-side — CallRaw still cannot DECODE the matched pattern itself
// (no route), but must surface the raw code so a future caller holding
// the registry-resolved target RouteHandle can finish that decode via
// RouteHandle.DecodeErrorFor(Code, Body).
func TestClientTransport_CallRaw_ErrorReply_MatchedPattern_ReturnsRawCode(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-callraw-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-callraw-ep")

	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	transport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})

	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	handle := epRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	_, callErr := transport.CallRaw(context.Background(), "compute/add-callraw-ep", payload, nil)
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
		t.Error("ForwardRawErrorReply.Body: want non-empty encoded serveErrPayload body")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// ── deep-dive-bug-hunt (api/reqreply focus): CallRaw missing TraceObserver span ──

func TestClientTransport_CallRaw_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	handle := newRouteHandle()
	_ = testServe(ctx, client, router, handle,
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	transport := NewClientTransport(ClientTransportOptions{Client: client, Router: router, Call: CallOptions{Timeout: 2 * time.Second, Observer: obs}})
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	if _, err := transport.CallRaw(ctx, handle.Topic, payload, nil); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.request" {
		t.Fatalf("expected StartSpan 'mqtt5.request' (mirrors Call's own TraceObserver convention), got %v", obs.startSpanOps)
	}
}

// ── deep-dive-bug-hunt (api/reqreply focus): CallRaw ignores Capabilities ──
//
// Mirrors capability_reqreply_test.go's EXISTING Call-side coverage
// (TestCall_RetainedAppliedToRequestPublish) — CallRaw is a SEPARATE
// dispatch implementation from call() that must independently apply the
// SAME transport-level CallOptions.Capabilities (QoS/Retained are
// wire-attribute, route-independent configuration; a caller configuring
// them expects them honored regardless of whether the call goes through
// Call or CallRaw).
func TestClientTransport_CallRaw_RetainedAppliedToRequestPublish(t *testing.T) {
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	transport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter, Call: CallOptions{Timeout: 100 * time.Millisecond, Capabilities: []Capability{Retained(true)}}})

	go func() {
		_, _ = transport.CallRaw(context.Background(), "compute/add", []byte(`{"x":1,"y":2}`), nil)
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	var pub *pahomqtt5.Publish
	for time.Now().Before(deadline) {
		if p := clientClient.lastPublished(); p != nil && p.Topic == "compute/add" {
			pub = p
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pub == nil {
		t.Fatal("expected the outgoing request to be published")
	}
	if !pub.Retain {
		t.Error("want Retain=true on the outgoing request publish")
	}
}
