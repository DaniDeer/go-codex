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
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
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
	// bearerMw declares the "bearer" scheme via .Use() (Phase 1 of
	// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) — REPLACES the OLD manual
	// WithSecurityScheme declaration, which cannot be paired against a
	// HandleMW/ClientMW implementation.
	bearerCodec := codex.String().Refine(validate.NonEmptyString)
	bearerMw := middleware.SecurityScheme("bearer", route.BearerScheme("JWT"), nil, &bearerCodec)
	acceptingImpl := func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error) {
		return map[string][]string{"bearer": nil}, nil
	}
	// pristineRoute (never .Use()'d) is used for the raw-Route dual-mode
	// call below — GlobalSecurity stays invisible to it, unaffected by
	// the SEPARATE registeredVariant's own .Use() declaration (Route is
	// immutable; .Use() returns a NEW value).
	pristineRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/add-secured-attach",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeSecuredAttach"},
	)
	registeredVariant := pristineRoute.
		Use(bearerMw).
		HandleMW(&bearerMw, acceptingImpl).
		ClientMW(&bearerMw, func(context.Context, []route.SecurityRequirement) ([]UserProperty, error) {
			return []UserProperty{{Key: "Authorization", Value: "******"}}, nil
		})
	handle, err := registeredVariant.WithHandler(handler).Register(server)
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

	// Raw, unregistered pristineRoute — GlobalSecurity is invisible
	// (ClientHandle() sources no Server, and pristineRoute was never
	// .Use()'d) — no credential attached, server rejects.
	_, err = client.Call(context.Background(), pristineRoute, computeReq{X: 1, Y: 1})
	if err == nil {
		t.Fatalf("Call with raw Route (no credential) succeeded, want a security rejection")
	}
	var credErr reqreply.SecurityCredentialError
	var callErr CallError
	if !errors.As(err, &credErr) && !errors.As(err, &callErr) {
		t.Fatalf("Call error = %v (%T), want a security-related error", err, err)
	}

	// Already-registered *RouteHandle — its ClientImplementations
	// (populated by the SAME .ClientMW() call chained above, before
	// Register) supplies the credential declaratively, no CallOptions
	// needed at Attach time at all.
	client2 := reqreply.NewClient()
	clientClient2 := &mockClient{}
	clientRouter2 := newMockRouter()
	if err := AttachClient(client2, clientClient2, clientRouter2); err != nil {
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
	// NEW: the matched pattern's Code must also be transmitted as a
	// dedicated User Property, so the client can look up which pattern
	// produced this reply.
	gotCode := errorCodeFromUserProperties(pub)
	if gotCode != "serveConflictErr" {
		t.Errorf("want error code User Property %q (default, sanitized type name), got %q", "serveConflictErr", gotCode)
	}
}

// TestAttachClient_ErrorPattern_MatchedReply_DecodesTypedError confirms
// the FULL round trip: a server publishes a matched-ErrorPattern error
// reply (code User Property + typed JSON body), and the CLIENT's Call
// decodes it into an errors.As-navigable mqtt5.ErrorPatternResponse
// (wrapped in CallError{Kind: KindHandler}) instead of the generic
// fmt.Errorf("server error: ...") fallback.
func TestAttachClient_ErrorPattern_MatchedReply_DecodesTypedError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-client", computeReqCodec, computeRespCodec,
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
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-ep-client")

	clientRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-client", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec).WithCode("conflict"),
	)
	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, callErr := client.Call(context.Background(), clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}
	var epr ErrorPatternResponse
	if !errors.As(callErr, &epr) {
		t.Fatalf("want errors.As to match mqtt5.ErrorPatternResponse, got %v", callErr)
	}
	if epr.Code != "conflict" {
		t.Errorf("want Code %q, got %q", "conflict", epr.Code)
	}
	payload, ok := epr.Value.(serveErrPayload)
	if !ok {
		t.Fatalf("want Value to be serveErrPayload, got %T", epr.Value)
	}
	if payload.Message != "duplicate" {
		t.Errorf("want Message %q, got %q", "duplicate", payload.Message)
	}
	cancel()
	<-errCh
}

// TestAttachClient_ErrorPattern_NoMatch_FallsBackToGenericError confirms
// the plain-text fallback path (no declared ErrorPattern matched
// server-side) still returns the UNCHANGED generic fmt.Errorf-wrapped
// error client-side — regression guard for callers who don't declare
// ErrorPattern at all.
func TestAttachClient_ErrorPattern_NoMatch_FallsBackToGenericError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	unrelatedErr := errors.New("unrelated failure")
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, unrelatedErr
	}
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-client-nomatch", computeReqCodec, computeRespCodec)
	if _, err := route.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-ep-client-nomatch")

	clientRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-client-nomatch", computeReqCodec, computeRespCodec)
	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, callErr := client.Call(context.Background(), clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}
	var epr ErrorPatternResponse
	if errors.As(callErr, &epr) {
		t.Fatalf("want NO match into mqtt5.ErrorPatternResponse for the plain-text fallback path, got %+v", epr)
	}
	if !strings.Contains(callErr.Error(), unrelatedErr.Error()) {
		t.Errorf("want fallback error message to contain %q, got: %v", unrelatedErr.Error(), callErr)
	}
	cancel()
	<-errCh
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
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum.
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

// ── Phase 1: declarative middleware (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) ──

// TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns confirms an
// UNPAIRED (Satisfies-empty) HandleMW decorator runs unconditionally,
// regardless of whether the route declares any security.
func TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	var ran bool
	generalMw := func(next func(*pahomqtt5.Publish)) func(*pahomqtt5.Publish) {
		return func(msg *pahomqtt5.Publish) {
			ran = true
			next(msg)
		}
	}
	route := reqreply.NewRoute[computeReq, computeResp]("compute/general-mw", computeReqCodec, computeRespCodec).
		HandleMW(nil, generalMw)
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := route.WithHandler(handler).Register(server); err != nil {
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
	serverRouter.waitHandler("compute/general-mw")

	serverRouter.dispatch("compute/general-mw", &pahomqtt5.Publish{
		Topic:   "compute/general-mw",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-general-mw"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if !ran {
		t.Fatal("want general-purpose HandleMW(nil, ...) to run unconditionally")
	}
}

// TestAttachServer_MultipleGeneralPurposeHandleMW_ComposeOutermostIn is a
// direct regression test proving TWO general-purpose HandleMW
// decorators attached to the SAME route compose in the CORRECT
// outermost-in order (the first attached runs first and returns last).
func TestAttachServer_MultipleGeneralPurposeHandleMW_ComposeOutermostIn(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	var order []string
	outerMw := func(next func(*pahomqtt5.Publish)) func(*pahomqtt5.Publish) {
		return func(msg *pahomqtt5.Publish) {
			order = append(order, "outer-in")
			next(msg)
			order = append(order, "outer-out")
		}
	}
	innerMw := func(next func(*pahomqtt5.Publish)) func(*pahomqtt5.Publish) {
		return func(msg *pahomqtt5.Publish) {
			order = append(order, "inner-in")
			next(msg)
			order = append(order, "inner-out")
		}
	}
	route := reqreply.NewRoute[computeReq, computeResp]("compute/multi-general-mw", computeReqCodec, computeRespCodec).
		HandleMW(nil, outerMw).
		HandleMW(nil, innerMw)
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := route.WithHandler(handler).Register(server); err != nil {
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
	serverRouter.waitHandler("compute/multi-general-mw")

	serverRouter.dispatch("compute/multi-general-mw", &pahomqtt5.Publish{
		Topic:   "compute/multi-general-mw",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-multi-general-mw"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	want := []string{"outer-in", "inner-in", "inner-out", "outer-out"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// TestAttachClient_MultipleGeneralPurposeClientMW_ComposeOutermostIn is
// the client-side mirror — proves TWO general-purpose ClientMW
// decorators attached to the SAME route compose outermost-in, exercising
// the reflect.MakeFunc-based wrapping in clientTransport.call directly
// (the riskiest new code path Phase 1 introduced).
func TestAttachClient_MultipleGeneralPurposeClientMW_ComposeOutermostIn(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	baseRoute := reqreply.NewRoute[computeReq, computeResp]("compute/multi-general-clientmw", computeReqCodec, computeRespCodec)
	if _, err := baseRoute.WithHandler(handler).Register(server); err != nil {
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
	serverRouter.waitHandler("compute/multi-general-clientmw")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	var order []string
	outerMw := func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(ctx context.Context, req computeReq) (computeResp, error) {
			order = append(order, "outer-in")
			resp, err := next(ctx, req)
			order = append(order, "outer-out")
			return resp, err
		}
	}
	innerMw := func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(ctx context.Context, req computeReq) (computeResp, error) {
			order = append(order, "inner-in")
			resp, err := next(ctx, req)
			order = append(order, "inner-out")
			return resp, err
		}
	}
	clientRoute := baseRoute.ClientMW(nil, outerMw).ClientMW(nil, innerMw)

	respAny, err := client.Call(context.Background(), clientRoute, computeReq{X: 5, Y: 6})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp := respAny.(computeResp)
	if resp.Sum != 11 {
		t.Fatalf("resp.Sum = %d, want 11", resp.Sum)
	}

	want := []string{"outer-in", "inner-in", "inner-out", "outer-out"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// TestAttachClient_ClientMW_AppliesToCallAsyncToo confirms a
// general-purpose ClientMW decorator runs for a CallAsync-dispatched
// call too, not just Call — see "Interaction with CallAsync/Future" in
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum.
func TestAttachClient_ClientMW_AppliesToCallAsyncToo(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	baseRoute := reqreply.NewRoute[computeReq, computeResp]("compute/clientmw-async", computeReqCodec, computeRespCodec)
	if _, err := baseRoute.WithHandler(handler).Register(server); err != nil {
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
	serverRouter.waitHandler("compute/clientmw-async")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	var ran bool
	generalMw := func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(ctx context.Context, req computeReq) (computeResp, error) {
			ran = true
			return next(ctx, req)
		}
	}
	clientRoute := baseRoute.ClientMW(nil, generalMw)

	futureAny, err := client.CallAsync(context.Background(), clientRoute, computeReq{X: 1, Y: 2})
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
	if resp.Sum != 3 {
		t.Fatalf("resp.Sum = %d, want 3", resp.Sum)
	}
	if !ran {
		t.Fatal("want general-purpose ClientMW to run for a CallAsync-dispatched call too")
	}
}

// testCtxKeyType/testCtxKey is a private context key for
// TestAttachClient_ClientMW_ContextMutationPropagatesIntoInnerCall below —
// proves a general-purpose ClientMW decorator's context mutation reaches
// the reflect.MakeFunc-built innerCall closure's OWN body (specifically
// the paired credential Fn), not just the decorator chain itself. A
// prior revision's innerCall ignored args[0] (the ctx actually passed by
// the decorator calling next) and used the STALE, pre-decorator ctx
// captured from the enclosing call — silently discarding any such
// mutation. Fixed by reading ctx from args[0] inside the closure.
type testCtxKeyType struct{}

var testCtxKey = testCtxKeyType{}

func TestAttachClient_ClientMW_ContextMutationPropagatesIntoInnerCall(t *testing.T) {
	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	var observedValue any
	credFn := func(ctx context.Context, _ []route.SecurityRequirement) ([]UserProperty, error) {
		observedValue = ctx.Value(testCtxKey)
		return nil, nil
	}
	ctxInjectingMw := func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(ctx context.Context, req computeReq) (computeResp, error) {
			ctx = context.WithValue(ctx, testCtxKey, "injected")
			return next(ctx, req)
		}
	}

	baseRoute := reqreply.NewRoute[computeReq, computeResp]("compute/ctx-propagation", computeReqCodec, computeRespCodec)
	clientRoute := baseRoute.Use(bearerAuthMw).
		ClientMW(&bearerAuthMw, credFn).
		ClientMW(nil, ctxInjectingMw)

	// No server/broker wiring needed — the credential Fn runs and
	// records observedValue BEFORE publish is ever attempted; a timeout
	// waiting for a reply (since no server answers) is expected and
	// ignored, only observedValue is asserted.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = client.Call(ctx, clientRoute, computeReq{X: 1, Y: 2})

	if observedValue != "injected" {
		t.Fatalf("expected the ClientMW decorator's context mutation to propagate into the paired credential Fn, got %v", observedValue)
	}
}

// ── docs/design/d-0003-codec-declared-middlewares.md's Addendum adapter wiring ──────

type mwPropIn struct{ TenantID string }
type mwPropOut struct{ Ack string }

var mwPropInCodec = codex.Struct[mwPropIn]()
var mwPropOutCodec = codex.Struct[mwPropOut]()

// TestAttachClient_WithRequestProperty_WritesOutgoingUserProperty verifies
// Write-side wiring Case 1: a Middleware's WithRequestProperty-declared
// value actually appears in the outgoing REQUEST's real MQTT5 User
// Properties.
func TestAttachClient_WithRequestProperty_WritesOutgoingUserProperty(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("req-prop", mwPropInCodec, mwPropOutCodec)).
		WithRequestProperty(reqreply.NewPropertyParam("X-Tenant", codex.String(),
			func(v mwPropIn) string { return v.TenantID },
			func(v *mwPropIn, s string) { v.TenantID = s }))

	rt := reqreply.ClientTransform(
		reqreply.NewRoute[computeReq, computeResp]("compute/req-prop-test", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req computeReq) (mwPropIn, error) {
			return mwPropIn{TenantID: "acme"}, nil
		},
	)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := reqreply.NewRoute[computeReq, computeResp]("compute/req-prop-test", computeReqCodec, computeRespCodec).
		WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/req-prop-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	if _, err := client.Call(context.Background(), rt, computeReq{X: 1, Y: 2}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	clientClient.mu.Lock()
	defer clientClient.mu.Unlock()
	var found bool
	for _, p := range clientClient.published {
		if p.Properties == nil {
			continue
		}
		for _, up := range p.Properties.User {
			if up.Key == "X-Tenant" && up.Value == "acme" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("want outgoing request's User Properties to carry X-Tenant=acme, got published: %+v", clientClient.published)
	}
	cancel()
	<-errCh
}

// TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty
// verifies Write-side wiring Case 3 — the MAJOR finding: a Middleware's
// WithResponseProperty-declared value actually appears in the outgoing
// REPLY's real MQTT5 User Properties.
func TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("resp-prop", mwPropInCodec, mwPropOutCodec)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
			func(v mwPropOut) string { return v.Ack },
			func(v *mwPropOut, s string) { v.Ack = s }))

	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("compute/resp-prop-test", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			return mwPropOut{Ack: "confirmed"}, nil
		},
	)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/resp-prop-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	if _, err := client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/resp-prop-test", computeReqCodec, computeRespCodec),
		computeReq{X: 1, Y: 2}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	serverClient.mu.Lock()
	defer serverClient.mu.Unlock()
	var found bool
	for _, p := range serverClient.published {
		if p.Properties == nil {
			continue
		}
		for _, up := range p.Properties.User {
			if up.Key == "X-Ack" && up.Value == "confirmed" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("want the SUCCESS reply's User Properties to carry X-Ack=confirmed, got published: %+v", serverClient.published)
	}
	cancel()
	<-errCh
}

// TestAttachServer_WithResponseProperty_ErrorReplyAlsoWritesUserProperty
// confirms the fix covers the ERROR-reply publish path too, not just the
// success path.
func TestAttachServer_WithResponseProperty_ErrorReplyAlsoWritesUserProperty(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("resp-prop-err", mwPropInCodec, mwPropOutCodec)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
			func(v mwPropOut) string { return v.Ack },
			func(v *mwPropOut, s string) { v.Ack = s }))

	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{}, errBusinessFailure
	}
	baseRoute := reqreply.NewRoute[computeReq, computeResp]("compute/resp-prop-err-test", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[businessError, string](
			codex.String(),
			func(e businessError) (string, error) { return e.msg, nil },
		),
	)
	rt := reqreply.Transform(baseRoute, mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			return mwPropOut{Ack: "err-ack"}, nil
		})

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/resp-prop-err-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, _ = client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/resp-prop-err-test", computeReqCodec, computeRespCodec),
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
			if up.Key == "X-Ack" && up.Value == "err-ack" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("want the ERROR reply's User Properties to ALSO carry X-Ack=err-ack, got published: %+v", serverClient.published)
	}
	cancel()
	<-errCh
}

type businessError struct{ msg string }

func (e businessError) Error() string { return e.msg }

var errBusinessFailure = businessError{msg: "business failure"}

// TestAttachServer_Transform_RunsAfterPairedSecurity confirms D1:
// Transform's declared middleware runs AFTER the paired security Fn.
func TestAttachServer_Transform_RunsAfterPairedSecurity(t *testing.T) {
	var order []string
	secMw := middleware.SecurityScheme("bearer2", route.BearerScheme("JWT"), nil, &bearerAuthTestCodec)
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("order-check", mwPropInCodec, mwPropOutCodec))
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		order = append(order, "handler")
		return computeResp{Sum: req.X + req.Y}, nil
	}
	baseRoute := reqreply.NewRoute[computeReq, computeResp]("compute/order-test", computeReqCodec, computeRespCodec).
		Use(secMw).
		HandleMW(&secMw, func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error) {
			order = append(order, "security")
			return map[string][]string{"bearer2": nil}, nil
		})
	rt := reqreply.Transform(baseRoute, mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			order = append(order, "middleware")
			return mwPropOut{}, nil
		})

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/order-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	authedRoute := reqreply.NewRoute[computeReq, computeResp]("compute/order-test", computeReqCodec, computeRespCodec).
		Use(secMw).
		ClientMW(&secMw, func(ctx context.Context, _ []route.SecurityRequirement) ([]UserProperty, error) {
			return []UserProperty{{Key: "Authorization", Value: "Bearer tok"}}, nil
		})
	if _, err := client.Call(context.Background(), authedRoute, computeReq{X: 1, Y: 2}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	if len(order) != 3 || order[0] != "security" || order[1] != "middleware" || order[2] != "handler" {
		t.Fatalf("want dispatch order [security middleware handler], got %v", order)
	}
	cancel()
	<-errCh
}

// TestAttachServer_MiddlewareError_WrapsAsKindMiddleware confirms
// decision #6: a Transform-attached fn's own business error surfaces
// through ServeError{Kind: KindMiddleware}, NOT KindHandler.
func TestAttachServer_MiddlewareError_WrapsAsKindMiddleware(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("fn-error", mwPropInCodec, mwPropOutCodec))
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("compute/mw-error-test", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			return mwPropOut{}, errBusinessFailure
		},
	)

	var gotKind ErrorKind
	var kindSet bool
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter, ServeOptions{
		OnError: func(e ServeError) {
			if !kindSet {
				gotKind = e.Kind
				kindSet = true
			}
		},
	}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/mw-error-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, _ = client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/mw-error-test", computeReqCodec, computeRespCodec),
		computeReq{X: 1, Y: 2})
	time.Sleep(50 * time.Millisecond)

	if !kindSet || gotKind != KindMiddleware {
		t.Fatalf("want ServeError{Kind: KindMiddleware}, got kindSet=%v kind=%v", kindSet, gotKind)
	}
	cancel()
	<-errCh
}

// TestTransform_MiddlewareError_FallsBackWhenNoErrorPatternMatch (D2):
// a route DOES declare an ErrorPattern (for a DIFFERENT error type than
// the middleware's own fn returns) — confirming the fn's business error,
// which does NOT match that pattern, still falls back to a plain-text
// reqreply.MiddlewareError response instead of being silently swallowed
// or mismatched against the wrong pattern. Distinct from
// TestAttachServer_MiddlewareError_WrapsAsKindMiddleware above (which
// covers the simpler "no ErrorPattern declared at all" case) and mirrors
// TestAttachServer_ErrorPattern_NoMatch_FallsBackToPlainText's identical
// scenario, but for the middleware axis instead of the route's own
// handler.
func TestTransform_MiddlewareError_FallsBackWhenNoErrorPatternMatch(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("fn-error-nomatch", mwPropInCodec, mwPropOutCodec))
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("compute/mw-error-nomatch-test", computeReqCodec, computeRespCodec,
			reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
				func(e serveConflictErr) (serveErrPayload, error) {
					return serveErrPayload{Code: "conflict", Message: e.msg}, nil
				},
			),
		),
		mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			return mwPropOut{}, errBusinessFailure
		},
	)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/mw-error-nomatch-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, _ = client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/mw-error-nomatch-test", computeReqCodec, computeRespCodec),
		computeReq{X: 1, Y: 2})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected error reply to be published")
	}
	if !strings.Contains(string(pub.Payload), errBusinessFailure.Error()) {
		t.Errorf("want plain-text MiddlewareError fallback containing %q, got: %s", errBusinessFailure.Error(), pub.Payload)
	}
	if strings.Contains(string(pub.Payload), "conflict") {
		t.Errorf("declared ErrorPattern for an UNRELATED error type must NOT match — got: %s", pub.Payload)
	}
	cancel()
	<-errCh
}

// TestAttachServer_Observer_ReportsMiddlewareInAndFnLocations (D5): a
// DecodeIn failure calls stats.ReportErrors(obs, "middleware:in", err),
// distinguishing it from the flat mechanism's existing "topic_var"
// string.
func TestAttachServer_Observer_ReportsMiddlewareInAndFnLocations(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("obs-loc", mwPropInCodec, mwPropOutCodec)).
		WithRequestProperty(reqreply.NewPropertyParam("X-Required", codex.String(),
			func(v mwPropIn) string { return v.TenantID },
			func(v *mwPropIn, s string) { v.TenantID = s }))
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("compute/obs-loc-test", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			return mwPropOut{}, nil
		},
	)

	obs := &testObserver{}
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter, ServeOptions{Observer: obs}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/obs-loc-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	// No X-Required property is sent — required property missing, a
	// validation-shaped MiddlewareInputError, so RecordValidationError
	// fires with location "middleware:in".
	_, _ = client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/obs-loc-test", computeReqCodec, computeRespCodec),
		computeReq{X: 1, Y: 2})
	time.Sleep(50 * time.Millisecond)

	var foundLoc bool
	for _, e := range obs.validationFull {
		if e.location == "middleware:in" {
			foundLoc = true
		}
	}
	if !foundLoc {
		t.Fatalf("want a RecordValidationError call with location \"middleware:in\", got %+v", obs.validationFull)
	}
	cancel()
	<-errCh
}

// TestAttachServer_Observer_ReportsMiddlewareOutLocation (Rest-middleware-
// conflict-detection-improvements' adapter-dispatch review): the reply's
// own EncodeOut failure (building the REPLY's Out struct, via
// WithResponseProperty) was previously collapsed into "middleware:in" —
// now reported distinctly as "middleware:out".
func TestAttachServer_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("obs-out-loc", mwPropInCodec, mwPropOutCodec)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String().Refine(validate.NonEmptyString),
			func(v mwPropOut) string { return v.Ack },
			func(v *mwPropOut, s string) { v.Ack = s }))
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("compute/obs-out-loc-test", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req *computeReq, in mwPropIn) (mwPropOut, error) {
			// Empty Ack fails the NonEmptyString refinement at
			// EncodeOut/OutCodec.Validate time, building the reply.
			return mwPropOut{Ack: ""}, nil
		},
	)

	obs := &testObserver{}
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter, ServeOptions{Observer: obs}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/obs-out-loc-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, _ = client.Call(context.Background(),
		reqreply.NewRoute[computeReq, computeResp]("compute/obs-out-loc-test", computeReqCodec, computeRespCodec),
		computeReq{X: 1, Y: 2})
	time.Sleep(50 * time.Millisecond)

	var foundLoc bool
	for _, e := range obs.validationFull {
		if e.location == "middleware:out" {
			foundLoc = true
		}
	}
	if !foundLoc {
		t.Fatalf("want a RecordValidationError call with location \"middleware:out\", got %+v", obs.validationFull)
	}
	cancel()
	<-errCh
}

// TestAttachClient_Observer_ReportsMiddlewareOutLocation (Rest-middleware-
// conflict-detection-improvements' adapter-dispatch review): a client-side
// DecodeOut failure (reading the REPLY's Out struct) was previously
// reported as "middleware:in" — now reported distinctly as
// "middleware:out", symmetric with "middleware:in" already covering the
// client-side ENCODE of the request's In struct.
func TestAttachClient_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	// Server does NOT declare WithResponseProperty at all — the reply
	// never carries X-Ack. Client's OWN mw declares it as REQUIRED
	// (NewPropertyParam), so dispatchClientMiddlewareOut's DecodeOut
	// fails reading the reply.
	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := reqreply.NewRoute[computeReq, computeResp]("compute/obs-client-out-test", computeReqCodec, computeRespCodec).
		WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/obs-client-out-test")

	clientMW := reqreply.NewMiddleware(middleware.NewDeclaration("obs-client-out-loc", mwPropInCodec, mwPropOutCodec)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
			func(v mwPropOut) string { return v.Ack },
			func(v *mwPropOut, s string) { v.Ack = s }))
	rt := reqreply.ClientTransform(
		reqreply.NewRoute[computeReq, computeResp]("compute/obs-client-out-test", computeReqCodec, computeRespCodec),
		clientMW,
		func(ctx context.Context, req computeReq) (mwPropIn, error) {
			return mwPropIn{}, nil
		},
	)

	obs := &testObserver{}
	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	callCtx := stats.WithObserver(context.Background(), obs)
	_, callErr := client.Call(callCtx, rt, computeReq{X: 1, Y: 2})
	time.Sleep(50 * time.Millisecond)

	var foundLoc bool
	for _, e := range obs.validationFull {
		if e.location == "middleware:out" {
			foundLoc = true
		}
	}
	if !foundLoc {
		t.Fatalf("want a RecordValidationError call with location \"middleware:out\", got %+v", obs.validationFull)
	}
	// The returned CallError must wrap a reqreply.MiddlewareOutputError,
	// recovering the failing middleware's Name (previously mislabeled as
	// MiddlewareInputError — see
	// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2).
	var outputErr reqreply.MiddlewareOutputError
	if !errors.As(callErr, &outputErr) {
		t.Fatalf("want errors.As to match reqreply.MiddlewareOutputError, got %v", callErr)
	}
	if outputErr.Name != "obs-client-out-loc" {
		t.Errorf("want Name %q, got %q", "obs-client-out-loc", outputErr.Name)
	}
	cancel()
	<-errCh
}
