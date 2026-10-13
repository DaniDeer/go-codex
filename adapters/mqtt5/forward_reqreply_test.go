package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests docs/design/d-0004-reqreply-workflow-simplification.md's Phase 1.3
// mqtt5 wire signal: a handler returning reqreply.Forward(...) publishes
// the reserved forward ContentType+User Property, and the CLIENT's
// call()/CallRaw() recognize it as reqreply.ForwardError — BEFORE
// ErrorPattern/DeadLetter are ever consulted.

// targetForwardRoute is the Forward's destination route — a brand new
// route distinct from the originating route, mirroring Forward's own
// "target's Req/Resp are free to differ" documented rationale.
var targetForwardRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/forward-target",
	computeReqCodec, computeRespCodec,
)

// TestAttachServer_Forward_PublishesReservedSignal_SkipsErrorPatternAndDeadLetter
// confirms the server-publish side: a handler returning a
// reqreply.ForwardError (via reqreply.Forward) publishes the reserved
// forwardContentType + forwardTopicPropertyKey + the forward's own
// Payload, and — even though the route ALSO declares an ErrorPattern AND
// a DeadLetter rule — neither is ever consulted (a Forward is a
// deliberate instruction, not a failure).
func TestAttachServer_Forward_PublishesReservedSignal_SkipsErrorPatternAndDeadLetter(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: req.X + 1, Y: req.Y + 1}, nil, nil)
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("compute/forward-src", computeReqCodec, computeRespCodec,
		// Declared specifically to prove the skip is REAL, not just
		// "never tested with one declared".
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
		reqreply.DeadLetter("compute/forward-src/dlq"),
	)
	if _, err := fwdRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var gotKind ErrorKind
	var gotErr error
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	transport := NewServerTransport(ServerTransportOptions{
		Client: serverClient, Router: serverRouter,
		Serve: ServeOptions{OnError: func(se ServeError) {
			gotKind = se.Kind
			gotErr = se.Err
		}},
	})
	if err := server.Attach(transport); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/forward-src")

	serverRouter.dispatch("compute/forward-src", &pahomqtt5.Publish{
		Topic:   "compute/forward-src",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-fwd"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	serverClient.mu.Lock()
	published := append([]*pahomqtt5.Publish(nil), serverClient.published...)
	serverClient.mu.Unlock()
	if len(published) != 1 {
		t.Fatalf("want exactly 1 publish (the forward signal only, no ErrorPattern/DeadLetter publish), got %d", len(published))
	}
	pub := published[0]
	if pub.Topic != "replies/client-1" {
		t.Errorf("want publish to reply topic %q, got %q", "replies/client-1", pub.Topic)
	}
	if pub.Properties == nil || pub.Properties.ContentType != forwardContentType {
		t.Fatalf("want ContentType %q, got %+v", forwardContentType, pub.Properties)
	}
	if !isForwardReply(pub) {
		t.Error("want isForwardReply(pub) == true")
	}
	gotTopic := forwardTopicFromUserProperties(pub)
	if gotTopic != "compute/forward-target" {
		t.Errorf("want forward topic %q, got %q", "compute/forward-target", gotTopic)
	}
	handle := targetForwardRoute.ClientHandle()
	decoded, err := handle.Decode(pub.Payload)
	if err != nil {
		t.Fatalf("Decode(forward payload): %v", err)
	}
	if decoded.X != 4 || decoded.Y != 5 {
		t.Errorf("want forwarded req {X:4 Y:5}, got %+v", decoded)
	}

	if gotKind != KindForward {
		t.Errorf("want OnError Kind %v, got %v", KindForward, gotKind)
	}
	var fwd reqreply.ForwardError
	if !errors.As(gotErr, &fwd) {
		t.Fatalf("want OnError's Err to be a reqreply.ForwardError, got %v (%T)", gotErr, gotErr)
	}
}

// TestAttachServer_Forward_WithProperties_RoundTripsOverWire is Phase
// 1.5's own wire-level properties addendum test (docs/design/d-0004-reqreply-workflow-simplification.md's "Addendum"): a handler's
// reqreply.Forward(target, req, vars, properties) call's Properties
// MUST appear on the published forward reply's User Properties
// (write side), AND the client's CallRaw MUST decode them back into
// ForwardError.Properties (read side) — a real wire-level credential/
// header, not just the Payload, needs to survive the round trip.
func TestAttachServer_Forward_WithProperties_RoundTripsOverWire(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: req.X, Y: req.Y},
			nil, map[string]string{"X-Replay-Reason": "retry", "X-Tenant": "acme"})
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("compute/forward-with-props", computeReqCodec, computeRespCodec)
	if _, err := fwdRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/forward-with-props")

	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	transport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	handle := fwdRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	_, callErr := transport.CallRaw(context.Background(), "compute/forward-with-props", payload, nil)
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("want CallRaw to return a reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.Properties["X-Replay-Reason"] != "retry" || fwd.Properties["X-Tenant"] != "acme" {
		t.Fatalf("want ForwardError.Properties to carry the Forward call's properties, got %+v", fwd.Properties)
	}

	// Write-side proof, independent of CallRaw's own decode: the raw
	// published message's User Properties carry the SAME values under
	// [forwardPropertyKeyPrefix], confirmed via the read-side helper
	// directly against the captured publish.
	serverClient.mu.Lock()
	published := append([]*pahomqtt5.Publish(nil), serverClient.published...)
	serverClient.mu.Unlock()
	var found bool
	for _, p := range published {
		if props := forwardPropertiesFromUserProperties(p); props["X-Replay-Reason"] == "retry" && props["X-Tenant"] == "acme" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a published forward reply carrying X-Replay-Reason/X-Tenant under %q, got: %+v", forwardPropertyKeyPrefix, published)
	}

	cancel()
	<-errCh
}

// TestAttachClient_Call_RecognizesForwardReply_BeforeErrorPattern confirms
// the client-recognize side of call(): a server replies with the
// reserved forward wire signal, and the transport's own Call (invoked
// directly via [reqreply.CallWithTransport], bypassing [*reqreply.
// Client]'s own chain-following loop which is OUT OF SCOPE here) returns
// reqreply.ForwardError{TargetTopic, Payload} via errors.As — even
// though the client route ALSO declares an ErrorPattern (proving Forward
// is recognized BEFORE ErrorPattern matching).
func TestAttachClient_Call_RecognizesForwardReply_BeforeErrorPattern(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: 9, Y: 9}, nil, nil)
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("compute/forward-call-client", computeReqCodec, computeRespCodec)
	if _, err := fwdRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/forward-call-client")

	clientHandle := reqreply.NewRoute[computeReq, computeResp]("compute/forward-call-client", computeReqCodec, computeRespCodec,
		// Declared to prove Forward is checked BEFORE ErrorPattern.
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	).ClientHandle()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	clientTransport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, callErr := reqreply.CallWithTransport(context.Background(), clientTransport, clientHandle, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("want errors.As to match reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.TargetTopic != "compute/forward-target" {
		t.Errorf("want TargetTopic %q, got %q", "compute/forward-target", fwd.TargetTopic)
	}
	handle := targetForwardRoute.ClientHandle()
	decoded, err := handle.Decode(fwd.Payload)
	if err != nil {
		t.Fatalf("Decode(fwd.Payload): %v", err)
	}
	if decoded.X != 9 || decoded.Y != 9 {
		t.Errorf("want forwarded req {X:9 Y:9}, got %+v", decoded)
	}
	cancel()
	<-errCh
}

// TestClientTransport_CallRaw_ForwardReply confirms CallRaw's
// client-recognize side returns the SAME reqreply.ForwardError a route-
// aware Call would, for a reply carrying the reserved forward wire
// signal — mirrors TestClientTransport_CallRaw_OrdinaryReply's harness
// (callraw_test.go).
func TestClientTransport_CallRaw_ForwardReply(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: 5, Y: 6}, nil, nil)
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("compute/forward-callraw", computeReqCodec, computeRespCodec)
	if _, err := fwdRoute.WithHandler(handler).Register(server); err != nil {
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
	serverRouter.waitHandler("compute/forward-callraw")

	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	transport := NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})

	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	handle := fwdRoute.ClientHandle()
	payload, err := handle.EncodeRequest(computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	_, callErr := transport.CallRaw(context.Background(), "compute/forward-callraw", payload, nil)
	if callErr == nil {
		t.Fatal("CallRaw: want an error for a forward reply, got nil")
	}
	var fwd reqreply.ForwardError
	if !errors.As(callErr, &fwd) {
		t.Fatalf("CallRaw error: want reqreply.ForwardError, got %v (%T)", callErr, callErr)
	}
	if fwd.TargetTopic != "compute/forward-target" {
		t.Errorf("want TargetTopic %q, got %q", "compute/forward-target", fwd.TargetTopic)
	}
	targetHandle := targetForwardRoute.ClientHandle()
	decoded, err := targetHandle.Decode(fwd.Payload)
	if err != nil {
		t.Fatalf("Decode(fwd.Payload): %v", err)
	}
	if decoded.X != 5 || decoded.Y != 6 {
		t.Errorf("want forwarded req {X:5 Y:6}, got %+v", decoded)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// TestMQTT5CallAdapter_ForwardError_PropagatesUnwrapped confirms Phase
// 1.6's "ports.Pattern passthrough" verification: mqtt5.CallAdapter
// dispatches via [reqreply.CallWithTransport] directly (registry-free by
// design, mirroring rest.CallWithTransport's own documented behavior for
// ANY error) — a forward reply surfacing through THIS path returns
// reqreply.ForwardError UNWRAPPED, exactly as CallWithTransport's own
// real implementation already guarantees for any transport.Call error.
// No new production code is expected to make this pass — pure
// verification, per docs/design/d-0004-reqreply-workflow-simplification.md's dispatch-
// path inventory.
func TestMQTT5CallAdapter_ForwardError_PropagatesUnwrapped(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: 1, Y: 1}, nil, nil)
	}
	fwdRoute := reqreply.NewRoute[computeReq, computeResp]("compute/forward-call-adapter", computeReqCodec, computeRespCodec)
	handle, err := fwdRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/forward-call-adapter")

	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	p, err := ports.NewIOPort[computeReq, computeResp]("test", computeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 1})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, CallAdapter(clientClient, clientRouter, handle, CallOptions{})) //nolint:errcheck

	reqCh := make(chan computeReq, 1)
	reqCh <- computeReq{X: 1, Y: 2}
	close(reqCh)
	out := p.Connect(ctx, gstream.From(ctx, reqCh))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 forwarded error, got %d: %v", len(errs), errs)
	}
	var fwd reqreply.ForwardError
	if !errors.As(errs[0], &fwd) {
		t.Fatalf("want errors.As to match reqreply.ForwardError UNWRAPPED, got %v (%T)", errs[0], errs[0])
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// TestAttachServer_WithResponseProperty_ForwardReplyAlsoWritesUserProperty
// is deep-dive-bug-hunt's own re-verification of docs/design/
// d-0003-codec-declared-middlewares.md's Addendum "Write-side wiring"
// Case 3 against the NEW Forward dispatch path: a Middleware's
// WithResponseProperty-declared value must appear on EVERY reply this
// server ever publishes for the route — success (confirmed,
// TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty) AND
// error (confirmed, TestAttachServer_WithResponseProperty_
// ErrorReplyAlsoWritesUserProperty) — Forward is a THIRD reply kind this
// same server publishes, previously untested against this convention.
func TestAttachServer_WithResponseProperty_ForwardReplyAlsoWritesUserProperty(t *testing.T) {
	mw := reqreply.NewBoundMiddleware[computeReq](reqreply.NewDeclaration("resp-prop-fwd", mwPropInCodec, mwPropOutCodec),
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			return mwPropOut{Ack: "fwd-ack"}, nil
		}).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
			func(v mwPropOut) string { return v.Ack },
			func(v *mwPropOut, s string) { v.Ack = s }))

	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: 7, Y: 8}, nil, nil)
	}
	baseRoute := reqreply.NewRoute[computeReq, computeResp]("compute/resp-prop-fwd-test", computeReqCodec, computeRespCodec)
	rt := baseRoute.HandleBoundMW(mw)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/resp-prop-fwd-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, _ = client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/resp-prop-fwd-test", computeReqCodec, computeRespCodec),
		computeReq{X: 1, Y: 2})

	time.Sleep(50 * time.Millisecond)
	serverClient.mu.Lock()
	defer serverClient.mu.Unlock()
	var found bool
	for _, p := range serverClient.published {
		if p.Properties == nil {
			continue
		}
		for _, up := range p.Properties.User {
			if up.Key == "X-Ack" && up.Value == "fwd-ack" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("want the FORWARD reply's User Properties to ALSO carry X-Ack=fwd-ack, got published: %+v", serverClient.published)
	}
	cancel()
	<-errCh
}

// TestClient_Call_FollowsForward_EndToEndViaRealMQTT5Adapter is deep-
// dive-bug-hunt's own re-verification that reqreply.Client.Call's
// CENTRALIZED chain-following loop (api/reqreply/forward.go's
// followForward) ACTUALLY works against the REAL mqtt5 wire signal, not
// just the synthetic forwardFakeClientTransport api/reqreply/
// forward_resolve_test.go exercises — closes a genuine test-coverage
// gap: every other Forward test in this package stops at the
// TRANSPORT level (confirming ClientRaw/call recognize+return
// ForwardError), none exercise the FULL reqreply.Client.Call dispatch
// that actually follows it end to end through a real server+client pair.
func TestClient_Call_FollowsForward_EndToEndViaRealMQTT5Adapter(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	originatingHandler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{}, reqreply.Forward(targetForwardRoute, computeReq{X: req.X * 10, Y: req.Y * 10}, nil, nil)
	}
	originatingRoute := reqreply.NewRoute[computeReq, computeResp]("compute/e2e-forward-originating", computeReqCodec, computeRespCodec)
	if _, err := originatingRoute.WithHandler(originatingHandler).Register(server); err != nil {
		t.Fatalf("Register(originating): %v", err)
	}
	targetHandler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: (req.X + req.Y) * 2}, nil
	}
	if _, err := targetForwardRoute.WithHandler(targetHandler).Register(server); err != nil {
		t.Fatalf("Register(target): %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/e2e-forward-originating")
	serverRouter.waitHandler("compute/forward-target")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	// targetForwardRoute is ONLY ever reached as a Forward target in
	// this test — warm the registry explicitly, exactly the documented
	// use case.
	if err := client.RegisterRoute(targetForwardRoute); err != nil {
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
		t.Errorf("resp.Sum = %d, want 60 (forwarded through the REAL mqtt5 wire signal + chain-following loop)", resp.Sum)
	}

	cancel()
	if serveErr := <-errCh; serveErr != nil {
		t.Fatalf("Serve: %v", serveErr)
	}
}
