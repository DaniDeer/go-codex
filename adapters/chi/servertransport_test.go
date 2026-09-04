package chi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	gochi "github.com/go-chi/chi/v5"
)

// ── Builder.Attach/Serve (Decision 5 / transport-agnostic-serve-interface) ──

func TestAttachRouter_SecondCall_ReturnsServerTransportAlreadyAttachedError(t *testing.T) {
	b := rest.NewServer(testInfo)
	r := gochi.NewRouter()
	if err := AttachRouter(b, r, "127.0.0.1:0"); err != nil {
		t.Fatalf("first AttachRouter: %v", err)
	}
	err := AttachRouter(b, r, "127.0.0.1:0")
	var attachedErr rest.ServerTransportAlreadyAttachedError
	if !errors.As(err, &attachedErr) {
		t.Fatalf("want ServerTransportAlreadyAttachedError, got %v (%T)", err, err)
	}
}

func TestBuilderServe_NoTransportAttached_ReturnsNoServerTransportAttachedError(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := b.Serve(context.Background())
	var noTransportErr rest.NoServerTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoServerTransportAttachedError, got %v (%T)", err, err)
	}
}

// TestAttachRouter_BuilderServe_ActuallyServesHTTP mirrors
// adapters/nethttp's identical end-to-end test.
func TestAttachRouter_BuilderServe_ActuallyServesHTTP(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	addr := "127.0.0.1:18733" // fixed test port, unlikely to collide in CI
	r := gochi.NewRouter()
	if err := AttachRouter(b, r, addr); err != nil {
		t.Fatalf("AttachRouter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- b.Serve(ctx) }()

	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Post("http://"+addr+"/users", "application/json", strings.NewReader(`{"name":"Alice"}`))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("POST /users: %v (server never became reachable)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got userResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Alice" || got.ID != "1" {
		t.Errorf("unexpected response: %+v", got)
	}

	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for graceful shutdown after ctx cancellation")
	}
}

// TestAttachRouter_BuilderServe_SSEOnly_ActuallyServesSSE proves the gap
// fix: a builder with ONLY an SSE route registered is fully reachable
// through AttachRouter + b.Serve — [serverTransport.Serve] must wire SSE
// routes via [serveSSE], not just plain ones via [serve].
func TestAttachRouter_BuilderServe_SSEOnly_ActuallyServesSSE(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"},
	).WithHandler(func(ctx context.Context, req createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	addr := "127.0.0.1:18735" // fixed test port, unlikely to collide in CI
	r := gochi.NewRouter()
	if err := AttachRouter(b, r, addr); err != nil {
		t.Fatalf("AttachRouter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- b.Serve(ctx) }()

	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + addr + "/events")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET /events: %v (server never became reachable)", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "hello") {
		t.Errorf("body = %q, want to contain %q", body, "hello")
	}

	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for graceful shutdown after ctx cancellation")
	}
}

// TestAttachRouter_BuilderServe_MixedRoutes_BothReachable proves a builder
// with BOTH a plain route AND an SSE route registered is fully reachable
// through ONE AttachRouter + b.Serve call — [serve] and [serveSSE] each
// wire their own kind onto the SAME router with zero spurious errors from
// the other kind's absence-or-presence.
func TestAttachRouter_BuilderServe_MixedRoutes_BothReachable(t *testing.T) {
	b := rest.NewServer(testInfo)
	if err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b); err != nil {
		t.Fatalf("Register plain route: %v", err)
	}
	if err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"},
	).WithHandler(func(ctx context.Context, req createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	}).Register(b); err != nil {
		t.Fatalf("Register SSE route: %v", err)
	}

	addr := "127.0.0.1:18736" // fixed test port, unlikely to collide in CI
	r := gochi.NewRouter()
	if err := AttachRouter(b, r, addr); err != nil {
		t.Fatalf("AttachRouter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- b.Serve(ctx) }()

	var postResp *http.Response
	var postErr error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		postResp, postErr = http.Post("http://"+addr+"/users", "application/json", strings.NewReader(`{"name":"Alice"}`))
		if postErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if postErr != nil {
		t.Fatalf("POST /users: %v (server never became reachable)", postErr)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /users status = %d, want 201", postResp.StatusCode)
	}

	sseResp, err := http.Get("http://" + addr + "/events")
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer sseResp.Body.Close()
	if sseResp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", sseResp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(sseResp.Body)
	if err != nil {
		t.Fatalf("read SSE body: %v", err)
	}
	if !strings.Contains(string(body), "hello") {
		t.Errorf("SSE body = %q, want to contain %q", body, "hello")
	}

	cancel()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for graceful shutdown after ctx cancellation")
	}
}
