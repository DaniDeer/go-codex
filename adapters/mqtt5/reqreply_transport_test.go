package mqtt5

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── Server/Client + Attach round trip (Phase 1: real mqtt5) ────────────────

func TestAttachServer_AttachClient_RoundTrip(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	serverClient := &mockClient{}
	serverRouter := newMockRouter()

	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := computeRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add")

	// The client side shares the SAME in-memory broker (client+router) as
	// the server for this test — a real broker would route messages
	// between distinct client connections; the mock client/router pair
	// simulates that by dispatching Publish calls directly to registered
	// handlers via the test helper below.
	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	// Wire the two mock brokers together: publishing on one side dispatches
	// to the other side's registered router handlers, simulating a shared
	// broker.
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	respAny, err := client.Call(context.Background(), computeRoute, computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 7 {
		t.Fatalf("resp.Sum = %d, want 7", resp.Sum)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// wireBrokers makes every message published via c dispatch synchronously to
// r's registered handlers — a minimal in-test broker simulation letting two
// independent mockClient/mockRouter pairs behave like they share one real
// MQTT broker.
func wireBrokers(t *testing.T, c *mockClient, r *mockRouter) {
	t.Helper()
	go func() {
		var lastLen int
		for i := 0; i < 200; i++ {
			c.mu.Lock()
			n := len(c.published)
			var toDispatch []*pahomqtt5.Publish
			if n > lastLen {
				toDispatch = append(toDispatch, c.published[lastLen:n]...)
				lastLen = n
			}
			c.mu.Unlock()
			for _, msg := range toDispatch {
				r.dispatch(msg.Topic, msg)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
}

func TestAttachClient_DualMode_GlobalSecurity(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	server.AddGlobalSecurity(route.Require("bearer"))
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	securedRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/add-secured-attach",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeSecuredAttach"},
		reqreply.WithSecurityScheme("bearer", reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
			WithCodec(codex.String().Refine(validate.NonEmptyString))),
	)
	handle, err := securedRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(handle.GlobalSecurity) == 0 {
		t.Fatalf("handle.GlobalSecurity is empty, want non-empty (Server.AddGlobalSecurity should be visible)")
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-secured-attach")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	// Raw, unregistered Route — GlobalSecurity is invisible (ClientHandle()
	// sources no Server) — no credential attached, server rejects.
	_, err = client.Call(context.Background(), securedRoute, computeReq{X: 1, Y: 1})
	if err == nil {
		t.Fatalf("Call with raw Route (no credential) succeeded, want a security rejection")
	}
	var credErr reqreply.SecurityCredentialError
	var callErr CallError
	if !errors.As(err, &credErr) && !errors.As(err, &callErr) {
		t.Fatalf("Call error = %v (%T), want a security-related error", err, err)
	}

	// Already-registered *RouteHandle, WITH a credential — GlobalSecurity
	// enforced and satisfied.
	client2 := reqreply.NewClient()
	clientClient2 := &mockClient{}
	clientRouter2 := newMockRouter()
	if err := AttachClient(client2, clientClient2, clientRouter2, CallOptions{
		CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) ([]UserProperty, error) {
			return []UserProperty{{Key: "Authorization", Value: "Bearer valid-token"}}, nil
		},
	}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, clientClient2, serverRouter)
	wireBrokers(t, serverClient, clientRouter2)

	respAny, err := client2.Call(context.Background(), handle, computeReq{X: 2, Y: 3})
	if err != nil {
		t.Fatalf("Call with registered handle + credential: %v", err)
	}
	resp := respAny.(computeResp)
	if resp.Sum != 5 {
		t.Fatalf("resp.Sum = %d, want 5", resp.Sum)
	}
}

func TestAttachClient_CallAsync_RoundTrip(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	asyncRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/add-async-attach",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeAsyncAttach"},
	)
	if _, err := asyncRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-async-attach")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	start := time.Now()
	futureAny, err := client.CallAsync(context.Background(), asyncRoute, computeReq{X: 10, Y: 20})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	if time.Since(start) > 5*time.Millisecond {
		t.Fatalf("CallAsync blocked for %v, want near-instant return", time.Since(start))
	}
	future, ok := futureAny.(*reqreply.Future[computeResp])
	if !ok {
		t.Fatalf("future type = %T, want *reqreply.Future[computeResp]", futureAny)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	resp, err := future.Wait(waitCtx)
	if err != nil {
		t.Fatalf("future.Wait: %v", err)
	}
	if resp.Sum != 30 {
		t.Fatalf("resp.Sum = %d, want 30", resp.Sum)
	}
}

// ── Phase 0: capability parity between AttachServer/AttachClient and Serve/Call ──

// TestAttachServer_AttachClient_MergeFields_RoundTrip confirms
// NewTopicParam merge-field parity in BOTH directions through the
// Attach-based workflow in one round trip: the SERVER merges the topic
// var into the decoded Req (work item 1), and the CLIENT derives the
// topic var FROM the req to build the concrete topic (work item 4) —
// mirrors TestServe_MergeFields_AutoMergesTopicVars/
// TestCallHandle_DerivesVarsFromReq's old-escape-hatch coverage, but via
// AttachServer/AttachClient exclusively.
func TestAttachServer_AttachClient_MergeFields_RoundTrip(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	var received tenantReq
	handler := func(_ context.Context, req tenantReq) (tenantResp, error) {
		received = req
		return tenantResp{Sum: req.X + req.Y}, nil
	}
	route := reqreply.NewRoute[tenantReq, tenantResp]("compute/{tenantID}/add",
		tenantReqCodec, tenantRespCodec,
		reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(r tenantReq) string { return r.TenantID },
			func(r *tenantReq, v string) { r.TenantID = v }),
	)
	handle, err := route.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/{tenantID}/add")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	// NOTE: wireBrokers can't be used for the request leg here — it
	// dispatches using the message's OWN concrete Topic as the lookup
	// key, but the server registered its handler under the TEMPLATE key
	// "compute/{tenantID}/add" (mirrors how TestServe_MergeFields_
	// AutoMergesTopicVars dispatches manually, for the same reason). The
	// REPLY leg has no template involved (the client subscribes to its
	// own concrete, per-call reply topic), so wireBrokers works fine
	// there.
	wireBrokers(t, serverClient, clientRouter)

	type result struct {
		resp tenantResp
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		respAny, callErr := client.Call(context.Background(), handle, tenantReq{TenantID: "acme", X: 3, Y: 4})
		if callErr != nil {
			resultCh <- result{err: callErr}
			return
		}
		resp, ok := respAny.(tenantResp)
		if !ok {
			resultCh <- result{err: fmt.Errorf("resp type = %T, want tenantResp", respAny)}
			return
		}
		resultCh <- result{resp: resp}
	}()

	// Wait for the client to publish its request, then dispatch it to the
	// server's TEMPLATE-keyed handler manually.
	var reqPub *pahomqtt5.Publish
	for i := 0; i < 200; i++ {
		if reqPub = clientClient.lastPublished(); reqPub != nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if reqPub == nil {
		t.Fatal("expected a request to be published")
	}
	if reqPub.Topic != "compute/acme/add" {
		t.Errorf("client-published Topic = %q, want %q (topic-var derivation, work item 4)", reqPub.Topic, "compute/acme/add")
	}
	serverRouter.dispatch("compute/{tenantID}/add", reqPub)

	res := <-resultCh
	if res.err != nil {
		t.Fatalf("Call: %v", res.err)
	}
	if res.resp.Sum != 7 {
		t.Fatalf("resp.Sum = %d, want 7", res.resp.Sum)
	}
	if received.TenantID != "acme" {
		t.Errorf("server-side received.TenantID = %q, want %q (merge-field parity, work item 1)", received.TenantID, "acme")
	}
}

// TestAttachServer_Formats_Honored confirms AttachServer's dispatch
// consults handle.Formats (falling back to plain JSON Encode) — work
// item 2 (server-side, response direction). Mirrors
// TestCall_ResponseFormats_OverridesRouteDeclaredFormat's server-side
// setup (a YAML-declared handle), but checks the PUBLISHED payload
// directly instead of relying on a format-aware client to decode it.
func TestAttachServer_Formats_Honored(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	yamlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-yaml", computeReqCodec, computeRespCodec)
	handle, err := yamlRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	handle.WithFormats(format.YAML(computeRespCodec))

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-yaml")

	serverRouter.dispatch("compute/add-yaml", &pahomqtt5.Publish{
		Topic:   "compute/add-yaml",
		Payload: []byte(`{"x":3,"y":4}`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-yaml"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if _, jsonErr := format.JSON(computeRespCodec).Unmarshal(pub.Payload); jsonErr == nil {
		t.Errorf("reply payload decoded as JSON — want YAML (handle.Formats not honored): %s", pub.Payload)
	}
	decoded, err := format.YAML(computeRespCodec).Unmarshal(pub.Payload)
	if err != nil {
		t.Fatalf("want YAML-encoded reply payload (route-declared Formats), got %q: %v", pub.Payload, err)
	}
	if decoded.Sum != 7 {
		t.Errorf("decoded.Sum = %d, want 7", decoded.Sum)
	}
}

// TestAttachServer_ErrorPattern_MatchedReply confirms AttachServer's
// dispatch consults handle.ErrorResponseFor on handler failure — work
// item 3. Mirrors TestServe_ErrorPatternMatch_HandlerError_PublishesTypedPayload
// but via AttachServer exclusively.
func TestAttachServer_ErrorPattern_MatchedReply(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	_, err := epRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-ep")

	serverRouter.dispatch("compute/add-ep", &pahomqtt5.Publish{
		Topic:   "compute/add-ep",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-ep-attach"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if strings.Contains(string(pub.Payload), "conflict:") {
		t.Errorf("want typed JSON payload, got plain-text-looking payload: %s", pub.Payload)
	}
	if !strings.Contains(string(pub.Payload), `"code":"conflict"`) {
		t.Errorf("want typed payload with code=conflict, got: %s", pub.Payload)
	}
}

// TestAttachServer_ErrorPattern_NoMatch_FallsBackToPlainText confirms an
// unrelated handler error (no declared ErrorPattern match) still falls
// back to plain-text — negative case for work item 3.
func TestAttachServer_ErrorPattern_NoMatch_FallsBackToPlainText(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	unrelatedErr := errors.New("unrelated failure")
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, unrelatedErr
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-nomatch", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	_, err := epRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-ep-nomatch")

	serverRouter.dispatch("compute/add-ep-nomatch", &pahomqtt5.Publish{
		Topic:   "compute/add-ep-nomatch",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-ep-nomatch"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), unrelatedErr.Error()) {
		t.Errorf("want plain-text fallback containing %q, got: %s", unrelatedErr.Error(), pub.Payload)
	}
}

// TestAttachClient_Call_ClientCallOptions_ResponseFormats_Overrides
// confirms a per-call reqreply.ClientCallOptions.ResponseFormats
// override wins over the route's declared handle.Formats for THIS call
// only — work item 2 (client-side). Mirrors
// TestCall_ResponseFormats_OverridesRouteDeclaredFormat's setup (server
// declares YAML, client declares nothing) but dispatches via
// AttachServer/AttachClient exclusively.
func TestAttachClient_Call_ClientCallOptions_ResponseFormats_Overrides(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	yamlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-yaml-override", computeReqCodec, computeRespCodec)
	handle, err := yamlRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	handle.WithFormats(format.YAML(computeRespCodec)) // server replies with YAML

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-yaml-override")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	// Client's OWN route declares no Formats (defaults to JSON) — without
	// the per-call override, decoding the server's YAML reply would fail.
	clientRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-yaml-override", computeReqCodec, computeRespCodec)
	respAny, err := client.Call(context.Background(), clientRoute, computeReq{X: 3, Y: 4},
		reqreply.ClientCallOptions{
			ResponseFormats: []format.Format[computeResp]{format.YAML(computeRespCodec)},
		})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 7 {
		t.Fatalf("resp.Sum = %d, want 7 (decoded via ResponseFormats override)", resp.Sum)
	}
}

// TestAttachClient_CallAsync_AppliesClientCallOptions confirms
// CallAsync's ClientCallOptions parameter applies too, since it shares
// the SAME underlying dispatch clientTransport.Call uses — regression
// test for "Interaction with CallAsync/Future" in
// docs/roadmap/reqreply-middleware.md.
func TestAttachClient_CallAsync_AppliesClientCallOptions(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	yamlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-yaml-async", computeReqCodec, computeRespCodec)
	handle, err := yamlRoute.WithHandler(handler).Register(server)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	handle.WithFormats(format.YAML(computeRespCodec))

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-yaml-async")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	asyncClientRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-yaml-async", computeReqCodec, computeRespCodec)
	futureAny, err := client.CallAsync(context.Background(), asyncClientRoute, computeReq{X: 5, Y: 6},
		reqreply.ClientCallOptions{
			ResponseFormats: []format.Format[computeResp]{format.YAML(computeRespCodec)},
		})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	future, ok := futureAny.(*reqreply.Future[computeResp])
	if !ok {
		t.Fatalf("future type = %T, want *reqreply.Future[computeResp]", futureAny)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	resp, err := future.Wait(waitCtx)
	if err != nil {
		t.Fatalf("future.Wait: %v", err)
	}
	if resp.Sum != 11 {
		t.Fatalf("resp.Sum = %d, want 11 (decoded via ResponseFormats override, applied through CallAsync)", resp.Sum)
	}
}
