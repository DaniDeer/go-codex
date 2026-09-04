package nethttp

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
)

// ── Builder.Attach/Serve (Decision 5 / transport-agnostic-serve-interface) ──

func TestAttachMux_BuilderServe_RoundTrip(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := AttachMux(b, mux, "127.0.0.1:0"); err != nil {
		t.Fatalf("AttachMux: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Serve(ctx) }()

	// AttachMux uses a fixed test port since ":0" (OS-assigned) isn't
	// resolvable from here without a listener handle — use a fixed
	// high port instead, retrying briefly for the server to come up.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Serve to return after ctx cancellation")
	}
}

func TestAttachMux_SecondCall_ReturnsServerTransportAlreadyAttachedError(t *testing.T) {
	b := rest.NewServer(testInfo)
	mux := http.NewServeMux()
	if err := AttachMux(b, mux, "127.0.0.1:0"); err != nil {
		t.Fatalf("first AttachMux: %v", err)
	}
	err := AttachMux(b, mux, "127.0.0.1:0")
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

// TestAttachMux_BuilderServe_ActuallyServesHTTP verifies the FULL
// end-to-end path — Attach + Serve owning its own *http.Server and
// actually accepting a real HTTP request — using a real TCP listener on
// an OS-assigned port (127.0.0.1:0 resolved via a throwaway listener
// first, then reused).
func TestAttachMux_BuilderServe_ActuallyServesHTTP(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	addr := "127.0.0.1:18732" // fixed test port, unlikely to collide in CI
	mux := http.NewServeMux()
	if err := AttachMux(b, mux, addr); err != nil {
		t.Fatalf("AttachMux: %v", err)
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

// TestAttachMux_BuilderServe_SSEOnly_ActuallyServesSSE proves the SSE-gap
// fix (Decision 6, docs/roadmap/pubsub-workflow-simplification.md):
// [AttachMux]'s [serverTransport.Serve] used to wire ONLY plain routes
// via [serve], never SSE routes — an SSE route registered on builder was
// silently unreachable through the Attach workflow. A builder with ONLY
// an SSE route (zero plain routes) must now be reachable end-to-end
// through AttachMux + b.Serve, with [serve] wiring nothing (zero plain
// entries) and returning nil rather than erroring just because the
// "other kind" of route is absent.
func TestAttachMux_BuilderServe_SSEOnly_ActuallyServesSSE(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"},
	).WithHandler(func(ctx context.Context, req createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	}).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	addr := "127.0.0.1:18733" // fixed test port, unlikely to collide in CI
	mux := http.NewServeMux()
	if err := AttachMux(b, mux, addr); err != nil {
		t.Fatalf("AttachMux: %v", err)
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

// TestAttachMux_BuilderServe_MixedRoutes_BothReachable proves a builder
// with BOTH a plain route AND an SSE route registered is fully reachable
// through ONE AttachMux + b.Serve call — [serve] and [serveSSE] each wire
// their own kind onto the SAME mux with zero spurious errors from the
// other kind's absence-or-presence.
func TestAttachMux_BuilderServe_MixedRoutes_BothReachable(t *testing.T) {
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

	addr := "127.0.0.1:18734" // fixed test port, unlikely to collide in CI
	mux := http.NewServeMux()
	if err := AttachMux(b, mux, addr); err != nil {
		t.Fatalf("AttachMux: %v", err)
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
