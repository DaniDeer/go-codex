package chi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	gochi "github.com/go-chi/chi/v5"
)

// --- Serve (regular routes) ---

func TestChiServe_HappyPath(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := gochi.NewRouter()
	if err := serve(r, b); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var got userResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "Alice" || got.ID != "1" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestChiServe_SkipsSpecOnlyRoutes(t *testing.T) {
	b := rest.NewServer(testInfo)
	if err := rest.NewRoute[createReq, userResp]("POST", "/spec-only",
		createReqCodec, userRespCodec,
	).Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := gochi.NewRouter()
	if err := serve(r, b); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/spec-only", strings.NewReader(`{"name":"Alice"}`))
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 for a spec-only route never wired, got %d", rec.Code)
	}
}

func TestChiServe_DuplicateRoute(t *testing.T) {
	b := rest.NewServer(testInfo)
	mkRoute := func() error {
		return rest.NewRoute[createReq, userResp]("POST", "/dup",
			createReqCodec, userRespCodec,
		).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
			return userResp{}, nil
		}).Register(b)
	}
	if err := mkRoute(); err != nil {
		t.Fatal(err)
	}
	if err := mkRoute(); err != nil {
		t.Fatal(err)
	}

	r := gochi.NewRouter()
	err := serve(r, b)
	if err == nil {
		t.Fatal("want error for duplicate route, got nil")
	}
	var multiErr MultiRouteError
	if !errors.As(err, &multiErr) {
		t.Fatalf("want MultiRouteError, got %T: %v", err, err)
	}
	var dupErr DuplicateRouteError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateRouteError inside MultiRouteError, got %v", err)
	}
}

// --- ServeOne ---

func TestChiServeOne_HappyPath(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})

	h, err := serveOne(route)
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Bob"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// --- ServeSSE ---

func TestChiServeSSE_HappyPath(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"},
	).WithHandler(func(ctx context.Context, req createReq, send func(sseEvent) error) error {
		if err := send(sseEvent{Message: "hello"}); err != nil {
			return err
		}
		return send(sseEvent{Message: "world"})
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := gochi.NewRouter()
	if err := serveSSE(r, b); err != nil {
		t.Fatalf("ServeSSE: %v", err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 data lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "hello") {
		t.Errorf("line 0 = %q, want to contain 'hello'", lines[0])
	}
	if !strings.Contains(lines[1], "world") {
		t.Errorf("line 1 = %q, want to contain 'world'", lines[1])
	}
}

func TestChiServeSSE_SkipsSpecOnlyRoutes(t *testing.T) {
	b := rest.NewServer(testInfo)
	if err := rest.NewSSERoute[createReq, sseEvent]("/spec-only-sse",
		createReqCodec, sseEventCodec,
	).Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	r := gochi.NewRouter()
	if err := serveSSE(r, b); err != nil {
		t.Fatalf("ServeSSE: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/spec-only-sse", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 for a spec-only SSE route never wired, got %d", rec.Code)
	}
}
