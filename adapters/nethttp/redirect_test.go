package nethttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ── Phase 4: CheckRedirect override + ClientAwareTransport.BindClient ──

func TestNewClientTransport_OverridesCheckRedirect_CallerClientUnmutated(t *testing.T) {
	callerClient := &http.Client{}
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: callerClient, BaseURL: "http://example.invalid"})

	ct, ok := transport.(*clientTransport)
	if !ok {
		t.Fatalf("NewClientTransport: want *clientTransport, got %T", transport)
	}
	if ct.caller.client == callerClient {
		t.Fatal("NewClientTransport: the transport's *http.Client must be a SEPARATE shallow copy, not the caller's own value")
	}
	if ct.caller.client.CheckRedirect == nil {
		t.Fatal("NewClientTransport: want CheckRedirect overridden, got nil (net/http's silent default)")
	}
	if err := ct.caller.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Errorf("CheckRedirect: want http.ErrUseLastResponse, got %v", err)
	}
	// The CALLER's own client must be completely unaffected.
	if callerClient.CheckRedirect != nil {
		t.Error("NewClientTransport: caller's own *http.Client.CheckRedirect was mutated, want untouched (nil)")
	}
}

func TestNewClientTransport_NilHTTPClient_NoPanic(t *testing.T) {
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: nil, BaseURL: "http://example.invalid"})
	ct, ok := transport.(*clientTransport)
	if !ok {
		t.Fatalf("NewClientTransport: want *clientTransport, got %T", transport)
	}
	if ct.caller.client != nil {
		t.Error("NewClientTransport: want nil *http.Client to stay nil, not a copy of nil")
	}
}

func TestClientAwareTransport_BindClient_RetainsClientReference(t *testing.T) {
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: &http.Client{}, BaseURL: "http://example.invalid"})
	ct, ok := transport.(*clientTransport)
	if !ok {
		t.Fatalf("NewClientTransport: want *clientTransport, got %T", transport)
	}
	if ct.client != nil {
		t.Fatal("clientTransport.client: want nil before Attach")
	}

	c := rest.NewClient()
	if err := c.Attach(transport); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if ct.client != c {
		t.Error("clientTransport.client: want the attached *rest.Client retained via BindClient")
	}
}

// Confirms clientTransport genuinely implements rest.ClientAwareTransport
// (compile-time-checkable, but asserted explicitly here too for an
// explicit, readable failure if this regresses).
func TestClientTransport_ImplementsClientAwareTransport(t *testing.T) {
	var _ rest.ClientAwareTransport = (*clientTransport)(nil)
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: &http.Client{}, BaseURL: "http://x"})
	if _, ok := transport.(rest.ClientAwareTransport); !ok {
		t.Fatal("NewClientTransport's returned value does not implement rest.ClientAwareTransport")
	}
}

// ── Phase 4: Client.Call auto-follow (full server+client integration) ──

type getUserReq struct{ ID string }

// getUserReqCodec is empty: ID flows through the declared PATH MERGE
// field below, not the body (mirrors client_test.go's
// newClientGetByIDRoute/getByIDReqCodec pattern) — a plain
// rest.PathParam{Name: "id"} with no merge field cannot derive a vars
// value from the request struct at Call time.
var getUserReqCodec = codex.Struct[getUserReq]()

func newGetUserRoute() rest.Route[getUserReq, userResp] {
	return rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v },
		))
}

// setupRedirectServer builds a server with a "POST /orders" route that
// ALWAYS redirects (303) to "GET /users/{id}" (the created user), plus
// the target route itself — returns the started httptest.Server and the
// (unregistered) Route values needed to build a client-side registry.
func setupRedirectServer(t *testing.T, redirectStatus int, bounceMethod string) (*httptest.Server, rest.Route[createReq, userResp], rest.Route[getUserReq, userResp]) {
	t.Helper()
	b := rest.NewServer(testInfo)

	getUser := newGetUserRoute()
	if err := getUser.WithHandler(func(ctx context.Context, req getUserReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	}).Register(b); err != nil {
		t.Fatalf("Register getUser: %v", err)
	}

	bounceRoute := rest.NewRoute[createReq, userResp](bounceMethod, "/orders",
		createReqCodec, userRespCodec)
	if err := bounceRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(redirectStatus, bounceRoute, getUser, map[string]string{"id": "f47ac10b"})
	}).Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	return httptest.NewServer(mux), bounceRoute, getUser
}

func TestClientCall_AutoFollowsRedirect_RegisteredViaPriorCall(t *testing.T) {
	srv, bounceRoute, getUser := setupRedirectServer(t, http.StatusSeeOther, "POST")
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// Warm the registry by calling getUser directly first (auto-populate).
	if _, err := client.Call(context.Background(), getUser, getUserReq{ID: "f47ac10b"}); err != nil {
		t.Fatalf("warm-up Call(getUser): %v", err)
	}

	resp, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("Call(bounceRoute): want transparent auto-follow, got error: %v", err)
	}
	if resp.ID != "f47ac10b" || resp.Name != "Alice" {
		t.Errorf("Call(bounceRoute): unexpected decoded response: %+v", resp)
	}
}

func TestClientCall_AutoFollowsRedirect_RegisteredOnlyViaRegisterRoute(t *testing.T) {
	srv, bounceRoute, getUser := setupRedirectServer(t, http.StatusSeeOther, "POST")
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Warm the registry via RegisterRoute ONLY — getUser is never called
	// directly.
	if err := client.RegisterRoute(getUser); err != nil {
		t.Fatalf("RegisterRoute(getUser): %v", err)
	}

	resp, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("Call(bounceRoute): want transparent auto-follow, got error: %v", err)
	}
	if resp.ID != "f47ac10b" {
		t.Fatalf("Call(bounceRoute): unexpected result %#v", resp)
	}
}

func TestClientCall_UnrecognizedRedirect_ReturnsTypedError(t *testing.T) {
	srv, bounceRoute, _ := setupRedirectServer(t, http.StatusSeeOther, "POST")
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Deliberately do NOT register/call getUser — the registry has no
	// match for /users/f47ac10b.
	_, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"})
	var unrecErr rest.UnrecognizedRedirectError
	if !errors.As(err, &unrecErr) {
		t.Fatalf("Call(bounceRoute): want UnrecognizedRedirectError, got %T: %v", err, err)
	}
	if unrecErr.Status != http.StatusSeeOther {
		t.Errorf("UnrecognizedRedirectError.Status: want %d, got %d", http.StatusSeeOther, unrecErr.Status)
	}
}

func TestCallWithTransport_RedirectStaysRegistryFree_ReturnsTypedRedirectError(t *testing.T) {
	srv, bounceRoute, getUser := setupRedirectServer(t, http.StatusSeeOther, "POST")
	defer srv.Close()

	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	// Deliberately NOT attached to any *rest.Client (CallWithTransport's
	// whole point, round 2 Resolved design decision #5) — even though
	// getUser is a perfectly valid target, there is no registry to
	// consult, so a typed RedirectError (not an auto-followed userResp)
	// must come back.
	handle := bounceRoute.ClientHandle()
	_, err := rest.CallWithTransport(context.Background(), transport, handle, createReq{Name: "Alice"})
	var redirErr rest.RedirectError
	if !errors.As(err, &redirErr) {
		t.Fatalf("CallWithTransport: want typed RedirectError (no auto-follow), got %T: %v", err, err)
	}
	if redirErr.Location != "/users/f47ac10b" {
		t.Errorf("RedirectError.Location: want /users/f47ac10b, got %q", redirErr.Location)
	}
	_ = getUser
}

func TestClientCall_307_ReplaysOriginalBody(t *testing.T) {
	b := rest.NewServer(testInfo)

	var receivedBody createReq
	finalRoute := rest.NewRoute[createReq, userResp]("POST", "/orders-final",
		createReqCodec, userRespCodec)
	if err := finalRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		receivedBody = req
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b); err != nil {
		t.Fatalf("Register finalRoute: %v", err)
	}
	bounceRoute := rest.NewRoute[createReq, userResp]("POST", "/orders",
		createReqCodec, userRespCodec)
	if err := bounceRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(http.StatusTemporaryRedirect, bounceRoute, finalRoute, nil)
	}).Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}
	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(finalRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	resp, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Bob"})
	if err != nil {
		t.Fatalf("Call: want 307 auto-follow, got error: %v", err)
	}
	if resp.Name != "Bob" {
		t.Fatalf("Call: unexpected result %#v", resp)
	}
	if receivedBody.Name != "Bob" {
		t.Errorf("307 follow-up: want the ORIGINAL request body (Name=Bob) replayed, got %+v", receivedBody)
	}
}

func TestClientCall_SSEOnlyTarget_ReturnsRedirectToStreamUnsupportedError(t *testing.T) {
	b := rest.NewServer(testInfo)
	streamRoute := rest.NewSSERoute[getUserReq, userResp]("/stream/{id}",
		getUserReqCodec, userRespCodec, rest.PathParam{Name: "id"})
	if err := streamRoute.WithHandler(func(ctx context.Context, req getUserReq, send func(userResp) error) error {
		return send(userResp{ID: req.ID})
	}).Register(b); err != nil {
		t.Fatalf("Register streamRoute: %v", err)
	}
	bounceRoute := rest.NewRoute[createReq, userResp]("POST", "/orders",
		createReqCodec, userRespCodec)
	if err := bounceRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.RedirectToSSE(http.StatusSeeOther, bounceRoute, streamRoute, map[string]string{"id": "abc"})
	}).Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}
	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(streamRoute); err != nil {
		t.Fatalf("RegisterRoute(streamRoute): %v", err)
	}

	_, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"})
	var streamErr rest.RedirectToStreamUnsupportedError
	if !errors.As(err, &streamErr) {
		t.Fatalf("Call: want RedirectToStreamUnsupportedError, got %T: %v", err, err)
	}
}

func TestClientCall_RedirectChain_TooDeep_ReturnsTypedError(t *testing.T) {
	b := rest.NewServer(testInfo)
	// A -> A (self-redirecting route) — an infinite chain, must be
	// capped, not loop forever.
	var selfRoute rest.Route[createReq, userResp]
	selfRoute = rest.NewRoute[createReq, userResp]("GET", "/loop",
		createReqCodec, userRespCodec)
	selfRoute = selfRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(http.StatusSeeOther, selfRoute, selfRoute, nil)
	})
	if err := selfRoute.Register(b); err != nil {
		t.Fatalf("Register selfRoute: %v", err)
	}
	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(selfRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, err := client.Call(context.Background(), selfRoute, createReq{Name: "x"})
	var tooDeepErr rest.RedirectChainTooDeepError
	if !errors.As(err, &tooDeepErr) {
		t.Fatalf("Call: want RedirectChainTooDeepError, got %T: %v", err, err)
	}
}

func TestClientCall_MaxRedirectsOption_OverridesDefault(t *testing.T) {
	b := rest.NewServer(testInfo)
	var selfRoute rest.Route[createReq, userResp]
	selfRoute = rest.NewRoute[createReq, userResp]("GET", "/loop",
		createReqCodec, userRespCodec)
	selfRoute = selfRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(http.StatusSeeOther, selfRoute, selfRoute, nil)
	})
	if err := selfRoute.Register(b); err != nil {
		t.Fatalf("Register selfRoute: %v", err)
	}
	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(selfRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, err := client.Call(context.Background(), selfRoute, createReq{Name: "x"}, rest.ClientCallOptions{MaxRedirects: 2})
	var tooDeepErr rest.RedirectChainTooDeepError
	if !errors.As(err, &tooDeepErr) {
		t.Fatalf("Call: want RedirectChainTooDeepError, got %T: %v", err, err)
	}
	if tooDeepErr.Depth != 2 {
		t.Errorf("RedirectChainTooDeepError.Depth: want 2 (the configured MaxRedirects), got %d", tooDeepErr.Depth)
	}
}

// Confirms the overridden CheckRedirect actually takes effect against a
// REAL httptest server issuing a 3xx — net/http's own Do() must return
// the 3xx response directly (StatusCode in the 300s, Location header
// intact), not silently follow it.
func TestClientTransport_CheckRedirectOverride_Integration(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusSeeOther)
	}))
	defer redirecting.Close()

	transport := NewClientTransport(ClientTransportOptions{HTTPClient: redirecting.Client(), BaseURL: redirecting.URL})
	ct := transport.(*clientTransport)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, redirecting.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := ct.caller.client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("StatusCode: want %d (redirect surfaced, not auto-followed), got %d", http.StatusSeeOther, resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != target.URL+"/final" {
		t.Errorf("Location: want %q, got %q", target.URL+"/final", loc)
	}
}

// ── Phase 5: Client.Consume auto-follow (SSE redirect-following) ──

// setupSSERedirectServer builds a server with an SSE "GET /orders-sse"
// route whose handler ALWAYS redirects (303) to "GET /sse/counter2" (a
// second, DISTINCT SSE route emitting 2 counter events) — returns the
// started httptest.Server and both SSERoute values.
func setupSSERedirectServer(t *testing.T) (*httptest.Server, rest.SSERoute[getReq, counterSSEEvent], rest.SSERoute[getReq, counterSSEEvent]) {
	t.Helper()
	s := rest.NewServer(testInfo)

	targetRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/counter2", getReqCodec, counterSSEEventCodec,
		rest.RouteMeta{OperationID: "streamCounter2"})
	targetRoute = targetRoute.WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		for i := 1; i <= 2; i++ {
			if err := send(counterSSEEvent{Count: i}); err != nil {
				return err
			}
		}
		return nil
	})
	if err := targetRoute.Register(s); err != nil {
		t.Fatalf("Register targetRoute: %v", err)
	}

	var bounceRoute rest.SSERoute[getReq, counterSSEEvent]
	bounceRoute = rest.NewSSERoute[getReq, counterSSEEvent]("/orders-sse", getReqCodec, counterSSEEventCodec,
		rest.RouteMeta{OperationID: "streamOrdersBounce"})
	bounceRoute = bounceRoute.WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		return rest.RedirectToSSE(http.StatusSeeOther, bounceRoute, targetRoute, nil)
	})
	if err := bounceRoute.Register(s); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("serveSSE: %v", err)
	}
	return httptest.NewServer(mux), bounceRoute, targetRoute
}

func TestClientConsume_AutoFollowsRedirect_ContinuesStreaming(t *testing.T) {
	srv, bounceRoute, targetRoute := setupSSERedirectServer(t)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(targetRoute); err != nil {
		t.Fatalf("RegisterRoute(targetRoute): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got []int
	err := client.Consume(ctx, bounceRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = append(got, e.Count)
		if len(got) >= 2 {
			cancel()
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: want transparent auto-follow + continued streaming, got error: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("want [1 2] (events from the REDIRECT TARGET), got %v", got)
	}
}

func TestClientConsume_AutoFollowIntoRouteOnlyTarget_ReturnsRedirectTargetNotStreamableError(t *testing.T) {
	s := rest.NewServer(testInfo)

	// targetRoute is a plain (non-SSE) Route — registered into the
	// Call-decodable registry only.
	targetRoute := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }))
	targetRoute = targetRoute.WithHandler(func(ctx context.Context, req getUserReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	})
	if err := targetRoute.Register(s); err != nil {
		t.Fatalf("Register targetRoute: %v", err)
	}

	var bounceRoute rest.SSERoute[getReq, counterSSEEvent]
	bounceRoute = rest.NewSSERoute[getReq, counterSSEEvent]("/orders-sse", getReqCodec, counterSSEEventCodec,
		rest.RouteMeta{OperationID: "streamOrdersBounceToRoute"})
	bounceRoute = bounceRoute.WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		// targetRoute is a plain Route (not an SSERoute) — use
		// rest.Redirect, not RedirectToSSE.
		return rest.Redirect(http.StatusSeeOther, bounceRoute, targetRoute, map[string]string{"id": "f47ac10b"})
	})
	if err := bounceRoute.Register(s); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("serveSSE: %v", err)
	}
	if err := serve(mux, s); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(targetRoute); err != nil {
		t.Fatalf("RegisterRoute(targetRoute): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := client.Consume(ctx, bounceRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		return nil
	})
	var notStreamableErr rest.RedirectTargetNotStreamableError
	if !errors.As(err, &notStreamableErr) {
		t.Fatalf("Consume: want RedirectTargetNotStreamableError, got %T: %v", err, err)
	}
}

// ── Part D (Router/Middleware compatibility verification) ──

// TestClientCall_AutoFollow_DoesNotDispatchTargetsOwnClientMW codifies the
// "reuse the originating call's credentials, never re-derive from the
// target's own ClientMW" design rule as a regression-tested property, not
// just prose: a redirect target with a general-purpose ClientMW (the
// SAME wrap-around shape TestAttach_ClientCall_GeneralPurposeClientMW_
// Wraps exercises directly) must NOT have that middleware invoked when
// reached via auto-follow — only when called directly.
func TestClientCall_AutoFollow_DoesNotDispatchTargetsOwnClientMW(t *testing.T) {
	b := rest.NewServer(testInfo)

	var wrapperRan bool
	getUser := newGetUserRoute().ClientMW(nil, func(next func(context.Context, getUserReq) (userResp, error)) func(context.Context, getUserReq) (userResp, error) {
		return func(ctx context.Context, req getUserReq) (userResp, error) {
			wrapperRan = true
			return next(ctx, req)
		}
	})
	if err := getUser.WithHandler(func(ctx context.Context, req getUserReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	}).Register(b); err != nil {
		t.Fatalf("Register getUser: %v", err)
	}

	bounceRoute := rest.NewRoute[createReq, userResp]("POST", "/orders", createReqCodec, userRespCodec)
	if err := bounceRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(http.StatusSeeOther, bounceRoute, getUser, map[string]string{"id": "f47ac10b"})
	}).Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(getUser); err != nil {
		t.Fatalf("RegisterRoute(getUser): %v", err)
	}

	if _, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"}); err != nil {
		t.Fatalf("Call(bounceRoute): want transparent auto-follow, got error: %v", err)
	}
	if wrapperRan {
		t.Error("auto-follow must NOT dispatch the target's own ClientMW — wrapperRan should still be false")
	}

	// Sanity check: the wrapper mechanism itself DOES work when the
	// target is called directly (not via auto-follow) — proves the
	// prior assertion is a genuine "bypassed," not "broken."
	if _, err := client.Call(context.Background(), getUser, getUserReq{ID: "f47ac10b"}); err != nil {
		t.Fatalf("Call(getUser) direct: %v", err)
	}
	if !wrapperRan {
		t.Error("direct Call(getUser) should have dispatched its own ClientMW — wrapperRan should now be true")
	}
}

// TestClientCall_AutoFollow_ReusesCodecBackedClientMiddlewareCredential is
// a regression test for a confirmed, real bug found while building
// examples/rest-api's redirect demo: auto-follow's redirectFollowUpInfo
// only ever captured the LEGACY credHeaders mechanism, silently DROPPING
// a credential delivered via a codec-backed BoundClientMiddleware (the
// mechanism examples/rest-api's own auth.BoundScopeClientMW uses) on
// every follow-up request — producing a 400 (missing required header),
// not even a 401, since the target's required Authorization header
// param failed validation before the handler ever ran. Fixed by also
// capturing/replaying the originating request's merged headerVars.
func TestClientCall_AutoFollow_ReusesCodecBackedClientMiddlewareCredential(t *testing.T) {
	type authIn struct{ Token string }
	// GrantedScopes is read via reflection by the security-grant merge
	// mechanism (CollectGrantsReflect/MergeMiddlewareHandlerGrants) —
	// REQUIRED for CheckScopes to consider "bearerAuth" satisfied, even
	// with nil required scopes (mirrors boundBearerAuthOut's identical
	// convention in adapter_test.go).
	type authOut struct{ GrantedScopes map[string][]string }

	b := rest.NewServer(testInfo)
	serverMW := rest.BoundSecurityMiddleware[getUserReq, authIn, authOut](
		"bearerAuth", rest.BearerScheme("JWT"), nil,
		func(ctx context.Context, _ *getUserReq, in authIn) (authOut, error) {
			if in.Token != "secret-token" {
				return authOut{}, errors.New("invalid token")
			}
			return authOut{GrantedScopes: map[string][]string{"bearerAuth": {}}}, nil
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in authIn) string { return in.Token },
		func(in *authIn, v string) { in.Token = v },
	))

	getUser := newGetUserRoute().HandleBoundMW(serverMW)
	if err := getUser.WithHandler(func(ctx context.Context, req getUserReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	}).Register(b); err != nil {
		t.Fatalf("Register getUser: %v", err)
	}

	bounceRoute := rest.NewRoute[createReq, userResp]("POST", "/orders", createReqCodec, userRespCodec)
	if err := bounceRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(http.StatusSeeOther, bounceRoute, getUser, map[string]string{"id": "f47ac10b"})
	}).Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	clientMW := rest.BoundSecurityClientMiddleware[createReq, authIn, authOut](
		"bearerAuth", rest.BearerScheme("JWT"), nil,
		func(context.Context, createReq) (authIn, error) {
			return authIn{Token: "secret-token"}, nil
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in authIn) string { return in.Token },
		func(in *authIn, v string) { in.Token = v },
	))
	bounceRouteClient := bounceRoute.ClientBoundMW(clientMW)

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(getUser); err != nil {
		t.Fatalf("RegisterRoute(getUser): %v", err)
	}

	resp, err := client.Call(context.Background(), bounceRouteClient, createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("Call(bounceRoute): want transparent auto-follow reusing the codec-backed credential, got error: %v", err)
	}
	if resp.ID != "f47ac10b" {
		t.Fatalf("Call(bounceRoute): unexpected result %#v", resp)
	}
}

// TestClientCall_RedirectRegistry_MismatchedRouterComposition_ReturnsUnrecognizedRedirectError
// is a genuine architecture constraint, not just a usage mistake to
// avoid: the SERVER computes the redirect Location via
// rest.Redirect(..., target, vars, rest.WithRouter(serverRouter)); the
// CLIENT separately registers the target's decode handle via its OWN,
// independently-constructed Router value. If the two Routers compose to
// DIFFERENT absolute paths, the registry lookup fails — deliberately
// typed (rest.UnrecognizedRedirectError), never a silent wrong-route
// match or a panic.
func TestClientCall_RedirectRegistry_MismatchedRouterComposition_ReturnsUnrecognizedRedirectError(t *testing.T) {
	b := rest.NewServer(testInfo)
	serverRouter := rest.NewRouter("/v1")

	getUser := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v },
		)).WithHandler(func(ctx context.Context, req getUserReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	})
	var bounceRoute rest.Route[createReq, userResp]
	bounceRoute = rest.NewRoute[createReq, userResp]("POST", "/orders", createReqCodec, userRespCodec).
		WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
			var zero userResp
			return zero, rest.Redirect(http.StatusSeeOther, bounceRoute, getUser, map[string]string{"id": "f47ac10b"}, rest.WithRouter(serverRouter))
		})
	if err := rest.NewRouter("/v1").Route(getUser).Register(b); err != nil {
		t.Fatalf("Register getUser under /v1: %v", err)
	}
	if err := bounceRoute.Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// DELIBERATE mismatch: the client registers getUser under a
	// DIFFERENT prefix ("/v2") than the server composed it with
	// ("/v1") — the registered routeKey's path ("/v2/users/{id}") will
	// never match the server's actual redirect Location ("/v1/users/f47ac10b").
	mismatchedClientRouter := rest.NewRouter("/v2")
	if err := client.RegisterRoute(getUser.ClientHandle(rest.WithRouter(mismatchedClientRouter))); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	_, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"})
	var unrecognizedErr rest.UnrecognizedRedirectError
	if !errors.As(err, &unrecognizedErr) {
		t.Fatalf("Call(bounceRoute): want UnrecognizedRedirectError (Router composition mismatch), got %T: %v", err, err)
	}
	if unrecognizedErr.Location != "/v1/users/f47ac10b" {
		t.Errorf("UnrecognizedRedirectError.Location: want the SERVER's actual composed Location %q, got %q",
			"/v1/users/f47ac10b", unrecognizedErr.Location)
	}
}

// TestClientCall_AutoFollow_TargetRequiresDifferentSecurity_Returns401
// confirms a security-scheme mismatch across a redirect hop fails
// correctly — the target's own 401 — rather than hanging, panicking, or
// silently succeeding: the originating route requires NO credential at
// all, the target requires bearer auth, and auto-follow (by design)
// NEVER fabricates or skips a credential it was never given.
func TestClientCall_AutoFollow_TargetRequiresDifferentSecurity_Returns401(t *testing.T) {
	type authIn struct{ Token string }
	type authOut struct{ GrantedScopes map[string][]string }

	b := rest.NewServer(testInfo)
	// Authorization is OPTIONAL here (not required) — a MISSING header
	// must still reach the Fn (so IT rejects with its own 401), exactly
	// like boundBearerAuthMw's identical convention in adapter_test.go;
	// a REQUIRED header param would instead fail header validation
	// BEFORE the Fn ever runs, producing 400, not the 401 this test
	// specifically wants to confirm.
	serverMW := rest.BoundSecurityMiddleware[getUserReq, authIn, authOut](
		"bearerAuth", rest.BearerScheme("JWT"), nil,
		func(ctx context.Context, _ *getUserReq, in authIn) (authOut, error) {
			if in.Token != "secret-token" {
				return authOut{}, errors.New("invalid token")
			}
			return authOut{GrantedScopes: map[string][]string{"bearerAuth": {}}}, nil
		},
	).WithRequestHeader(rest.NewOptionalHeaderParam("Authorization", codex.String(),
		func(in authIn) string { return in.Token },
		func(in *authIn, v string) { in.Token = v },
	))

	getUser := newGetUserRoute().HandleBoundMW(serverMW)
	if err := getUser.WithHandler(func(ctx context.Context, req getUserReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	}).Register(b); err != nil {
		t.Fatalf("Register getUser: %v", err)
	}

	// bounceRoute requires NO credential at all — the originating call
	// carries no Authorization header to reuse.
	bounceRoute := rest.NewRoute[createReq, userResp]("POST", "/orders", createReqCodec, userRespCodec)
	if err := bounceRoute.WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		var zero userResp
		return zero, rest.Redirect(http.StatusSeeOther, bounceRoute, getUser, map[string]string{"id": "f47ac10b"})
	}).Register(b); err != nil {
		t.Fatalf("Register bounceRoute: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, b); err != nil {
		t.Fatalf("serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(getUser); err != nil {
		t.Fatalf("RegisterRoute(getUser): %v", err)
	}

	_, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"})
	if err == nil {
		t.Fatal("Call(bounceRoute): want an error (target's own 401), got nil — a security-scheme mismatch must never silently succeed")
	}
	var unexpectedStatus UnexpectedStatusError
	if !errors.As(err, &unexpectedStatus) {
		t.Fatalf("Call(bounceRoute): want UnexpectedStatusError wrapping the target's 401, got %T: %v", err, err)
	}
	if unexpectedStatus.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode: want %d, got %d", http.StatusUnauthorized, unexpectedStatus.StatusCode)
	}
}
