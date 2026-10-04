package chi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
)

// Gap-1 review fix regression tests — chi mirror of
// adapters/nethttp/gap1_ports_middleware_dispatch_test.go. See that file's
// doc comment for the full rationale
// (docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A
// review): handlerFunc/sseHandlerFunc previously never dispatched
// handle.MiddlewareHandlers at all through chi's ports-facing binding
// adapters (IngestAdapter/LatestAdapter/SSEAdapter/stream.go) — only
// Server.Attach's serve.go/serve_sse.go reflect loop did.

// ── IngestAdapter + Security-shaped HandleMW ────────────────────────────────

func TestChiIngestAdapter_HandleMWSecurity_EnforcesCredential(t *testing.T) {
	decl := middleware.NewDeclaration("bearer-ingest-policy", tdInCodec, bearerAuthOutCodec)
	decl.Security = middleware.NewSecurityDeclaration("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	mw := rest.NewMiddleware(decl).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	handle, err := rest.NewRoute[createReq, struct{}]("POST", "/secure-ingest",
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "secureIngest"},
	).HandleMW(mw, func(ctx context.Context, req *createReq, in tdIn) (bearerAuthOut, error) {
		if in.Key != "valid-token" {
			return bearerAuthOut{}, errors.New("invalid bearer token")
		}
		return bearerAuthOut{GrantedScopes: map[string][]string{"bearerAuth": nil}}, nil
	}).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := gochi.NewRouter()
	p, err := ports.NewSourcePort[createReq]("secure-ingest", createReqCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, IngestAdapter(r, handle, IngestAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)

	srv := httptest.NewServer(r)
	defer srv.Close()
	time.Sleep(20 * time.Millisecond) // supervised Activate registers the handler

	// Invalid (present but wrong) token: BEFORE the Gap-1 fix, handlerFunc
	// never dispatched handle.MiddlewareHandlers at all, so this
	// Security-shaped middleware's Fn never ran and the request would have
	// silently succeeded (201) with no credential check whatsoever.
	badReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/secure-ingest", strings.NewReader(`{"name":"Eve"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.Header.Set("Authorization", "wrong-token")
	resp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatalf("POST (invalid token): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token: want 401, got %d — handle.MiddlewareHandlers must dispatch through IngestAdapter, not just Server.Attach", resp.StatusCode)
	}

	// Valid token: full end-to-end success, item reaches the stream.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/secure-ingest", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "valid-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST (valid token): %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("valid token: want 201, got %d", resp2.StatusCode)
	}

	select {
	case v, ok := <-s.Values:
		if !ok || v.Name != "Alice" {
			t.Fatalf("want delivered item Alice, got %+v (ok=%v)", v, ok)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for item in stream")
	}
}

// ── LatestAdapter + ordinary (non-Security) HandleMW ────────────────────────

func TestChiLatestAdapter_HandleMW_OrdinaryMiddleware_SetsResponseHeader(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("latest-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Applied", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))

	b := rest.NewServer(testInfo)
	handle, err := rest.NewRoute[struct{}, userResp]("GET", "/latest-mw",
		codex.Struct[struct{}](), userRespCodec, rest.RouteMeta{OperationID: "latestMW"},
	).HandleMW(mw, func(ctx context.Context, req *struct{}, in tdIn) (tdOut, error) {
		return tdOut{Value: "applied:" + in.Key}, nil
	}).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	r := gochi.NewRouter()
	adapter := LatestAdapter(r, handle, Options{})
	if err := adapter.Serve(context.Background(), func() (userResp, bool) {
		return userResp{ID: "1", Name: "Alice"}, true
	}); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/latest-mw", nil)
	req.Header.Set("X-Api-Key", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	// BEFORE the Gap-1 fix, handle.MiddlewareHandlers was never dispatched
	// through LatestAdapter at all, so this response header would have been
	// silently absent.
	if got := resp.Header.Get("X-Policy-Applied"); got != "applied:secret" {
		t.Errorf("want X-Policy-Applied=%q, got %q", "applied:secret", got)
	}
}
