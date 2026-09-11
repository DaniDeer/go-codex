package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
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
