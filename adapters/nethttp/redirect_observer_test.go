package nethttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/stats"
)

// ── rest-redirect-observer-gap: every redirect hop must be observed ────────
//
// Found-and-fixed gap: adapters/nethttp/redirect.go/redirect_sse.go had
// ZERO stats.Observer calls anywhere — discovered while designing
// api/reqreply's own Forward/Bounce per-hop-observed requirement (see
// docs/design/d-0004-reqreply-workflow-simplification.md's own
// "Addendum: Client Registry, Forward/Bounce, and Dead-Letter Replay"),
// tracked as its own follow-up (rest-redirect-observer-gap) until fixed.
// These tests confirm EACH hop (not just the originating call) is now
// observed via obs.RecordRequest, using spyObserver's call-history
// (unlike testObserver, which only records the LAST call — insufficient
// to prove multiple hops were each independently observed).

func TestClientCall_AutoFollowsRedirect_ObservesBothHops(t *testing.T) {
	srv, bounceRoute, getUser := setupRedirectServer(t, http.StatusSeeOther, "POST")
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(getUser); err != nil {
		t.Fatalf("RegisterRoute(getUser): %v", err)
	}

	spy := &spyObserver{}
	_, err := client.Call(context.Background(), bounceRoute, createReq{Name: "Alice"}, rest.ClientCallOptions{Observer: spy})
	if err != nil {
		t.Fatalf("Call(bounceRoute): %v", err)
	}

	if len(spy.requests) != 2 {
		t.Fatalf("spy.requests: want 2 (originating call + 1 follow-up hop), got %d: %+v", len(spy.requests), spy.requests)
	}
	// Hop 1: the originating POST /orders call, which returns 303.
	if spy.requests[0].method != "POST" || spy.requests[0].statusCode != http.StatusSeeOther {
		t.Errorf("spy.requests[0] (originating call): want POST/303, got %+v", spy.requests[0])
	}
	// Hop 2: the auto-followed GET /users/{id} hop, which succeeds.
	if spy.requests[1].method != http.MethodGet || spy.requests[1].statusCode != http.StatusOK {
		t.Errorf("spy.requests[1] (follow-up hop): want GET/200, got %+v", spy.requests[1])
	}
}

func TestClientCall_RedirectChainTooDeep_ObservesEveryHop(t *testing.T) {
	b := rest.NewServer(testInfo)
	var selfRoute rest.Route[createReq, userResp]
	selfRoute = rest.NewRoute[createReq, userResp]("GET", "/loop", createReqCodec, userRespCodec)
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

	spy := &spyObserver{}
	_, err := client.Call(context.Background(), selfRoute, createReq{Name: "x"}, rest.ClientCallOptions{MaxRedirects: 2, Observer: spy})
	var tooDeepErr rest.RedirectChainTooDeepError
	if !errors.As(err, &tooDeepErr) {
		t.Fatalf("Call: want RedirectChainTooDeepError, got %T: %v", err, err)
	}

	// The ORIGINATING call + 2 allowed hops, all 303 (self-loop never
	// succeeds), THEN the final (3rd) hop attempt is rejected BEFORE any
	// network activity — exceeding MaxRedirects — but is STILL observed
	// (status 0, confirming the chain-too-deep early-return itself is no
	// longer silently unobserved either).
	if len(spy.requests) != 4 {
		t.Fatalf("spy.requests: want 4 (originating call + 2 hops + 1 rejected-as-too-deep), got %d: %+v", len(spy.requests), spy.requests)
	}
	for i, req := range spy.requests[:3] {
		if req.statusCode != http.StatusSeeOther {
			t.Errorf("spy.requests[%d]: want 303 (self-loop), got %+v", i, req)
		}
	}
	if spy.requests[3].statusCode != 0 {
		t.Errorf("spy.requests[3] (rejected as too-deep): want status 0, got %+v", spy.requests[3])
	}
}

func TestClientConsume_AutoFollowsRedirect_ObservesBothHops(t *testing.T) {
	srv, bounceRoute, targetRoute := setupSSERedirectServer(t)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := client.RegisterRoute(targetRoute); err != nil {
		t.Fatalf("RegisterRoute(targetRoute): %v", err)
	}

	spy := &spyObserver{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = stats.WithObserver(ctx, spy)
	var got []int
	err := client.Consume(ctx, bounceRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = append(got, e.Count)
		if len(got) >= 2 {
			cancel()
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events from the redirect target, got %v", got)
	}

	if len(spy.requests) < 2 {
		t.Fatalf("spy.requests: want at least 2 (originating connection + follow-up hop), got %d: %+v", len(spy.requests), spy.requests)
	}
	if spy.requests[0].statusCode != http.StatusSeeOther {
		t.Errorf("spy.requests[0] (originating connection): want 303, got %+v", spy.requests[0])
	}
	if spy.requests[1].statusCode != http.StatusOK {
		t.Errorf("spy.requests[1] (follow-up hop): want 200, got %+v", spy.requests[1])
	}
}
