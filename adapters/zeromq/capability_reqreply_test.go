package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// This file closes docs/roadmap/capability-requirement-composition.md's
// Phase 2 plumbing gap for zeromq: [ServeOptions]/[CallOptions].
// Capabilities must reach the socket via the EXISTING [applyCapabilities]
// helper at ALL FOUR real dispatch implementations
// (serverTransport.Serve, routerServerTransport.Serve,
// clientTransport.call, dealerClientTransport.call) — not just the
// REQ/REP pair, since ROUTER/DEALER are entirely separate
// implementations that don't delegate to the REQ/REP pair at all.

// TestServe_CapabilitiesAppliedViaExistingApplyCapabilities confirms
// [serverTransport.Serve] (REQ/REP server) applies ServeOptions.
// Capabilities to the socket via the existing [applyCapabilities] helper.
func TestServe_CapabilitiesAppliedViaExistingApplyCapabilities(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	sock := &capableSocket{mockSocket: &mockSocket{}}

	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute": sock}, Serve: ServeOptions{Capabilities: []Capability{HWM(42), Conflate(true)}}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = server.Serve(ctx)

	if sock.hwm != 42 {
		t.Errorf("want HWM(42) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
}

// TestRouterServe_CapabilitiesAppliedViaExistingApplyCapabilities
// confirms [routerServerTransport.Serve] (ROUTER server) ALSO applies
// ServeOptions.Capabilities — a SEPARATE, non-delegating implementation
// from [serverTransport.Serve], not covered by the test above.
func TestRouterServe_CapabilitiesAppliedViaExistingApplyCapabilities(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	sock := &capableSocket{mockSocket: &mockSocket{}}

	if err := server.Attach(NewRouterServerTransport(RouterServerTransportOptions{Sockets: map[string]FramedSocket{"/compute": sock}, Serve: ServeOptions{Capabilities: []Capability{HWM(43), Conflate(true)}}})); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = server.Serve(ctx)

	if sock.hwm != 43 {
		t.Errorf("want HWM(43) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
}

// TestCall_CapabilitiesAppliedViaExistingApplyCapabilities confirms
// [clientTransport.call] (REQ client, behind Call/CallAsync) applies
// CallOptions.Capabilities — the timeout/error from the call itself is
// irrelevant here, since applyCapabilities runs BEFORE the send attempt.
func TestCall_CapabilitiesAppliedViaExistingApplyCapabilities(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{}}
	client := reqreply.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": sock}, Call: CallOptions{Capabilities: []Capability{HWM(44), Conflate(true)}}})); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = client.Call(ctx, route, computeReq{X: 1, Y: 2}) // expected to time out — no responder

	if sock.hwm != 44 {
		t.Errorf("want HWM(44) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
}

// TestDealerCall_CapabilitiesAppliedViaExistingApplyCapabilities
// confirms [dealerClientTransport.call] (DEALER client, behind
// Call/CallAsync) ALSO applies CallOptions.Capabilities — a SEPARATE,
// non-delegating implementation from [clientTransport.call], not
// covered by the test above.
func TestDealerCall_CapabilitiesAppliedViaExistingApplyCapabilities(t *testing.T) {
	sock := &capableSocket{mockSocket: &mockSocket{}}
	client := reqreply.NewClient()
	if err := client.Attach(NewDealerClientTransport(DealerClientTransportOptions{Sockets: map[string]FramedSocket{"/compute": sock}, Call: CallOptions{Capabilities: []Capability{HWM(45), Conflate(true)}}})); err != nil {
		t.Fatalf("AttachDealerClient: %v", err)
	}
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = client.Call(ctx, route, computeReq{X: 1, Y: 2}) // expected to time out — no responder

	if sock.hwm != 45 {
		t.Errorf("want HWM(45) applied to socket, got %d", sock.hwm)
	}
	if !sock.conflate {
		t.Error("want Conflate(true) applied to socket")
	}
}
