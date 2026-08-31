package nethttp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── IngestAdapter ─────────────────────────────────────────────────────────────

func newIngestRoute(t *testing.T) *rest.RouteHandle[createReq, struct{}] {
	t.Helper()
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, struct{}]("POST", "/ingest",
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "ingest"}).RegisterHandle(b)
	if err != nil {
		t.Fatalf("register route: %v", err)
	}
	return h
}

func TestIngestAdapter_DeliversToPipelineSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	handle := newIngestRoute(t)

	p, err := ports.NewSourcePort[createReq]("ingest", createReqCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, nethttp.IngestAdapter(mux, handle, nethttp.IngestAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Give Activate goroutine time to register the handler with mux.
	time.Sleep(20 * time.Millisecond)

	resp, err := http.Post(srv.URL+"/ingest", "application/json", strings.NewReader(`{"name":"Alice"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("want 201, got %d", resp.StatusCode)
	}
	var got createReq
	select {
	case v, ok := <-s.Values:
		if ok {
			got = v
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for item in stream")
	}
	cancel()
	if got.Name != "Alice" {
		t.Errorf("want Alice, got %q", got.Name)
	}
}

// ── PollAdapter ───────────────────────────────────────────────────────────────

func TestPollAdapter_EmitsResponsePerTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"u1","name":"Alice"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[getReq, userResp]("GET", "/users/latest",
		getReqCodec, userRespCodec, rest.RouteMeta{}).RegisterHandle(b)

	p, err := ports.NewSourcePort[userResp]("poll", userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, nethttp.PollAdapter(http.DefaultClient, srv.URL, h, getReq{}, 30*time.Millisecond, nethttp.PollStreamOptions{Buffer: 4}))
	s := p.Stream(ctx)

	timeCtx, tc := context.WithTimeout(ctx, 110*time.Millisecond)
	defer tc()
	vals, errs := gstream.Collect(timeCtx, s)

	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) < 2 {
		t.Errorf("want ≥2 poll results, got %d", len(vals))
	}
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

func TestCallAdapter_EmitsResponsePerItem(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"u1","name":"Alice"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).RegisterHandle(b)

	ch := make(chan createReq, 1)
	ch <- createReq{Name: "Alice"}
	close(ch)
	src := gstream.From(ctx, ch)

	p, err := ports.NewIOPort[createReq, userResp]("call", createReqCodec, userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, nethttp.CallAdapter(http.DefaultClient, srv.URL, h, nethttp.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, src)
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %v", errs)
	}
	if len(vals) != 1 || vals[0].Name != "Alice" {
		t.Errorf("want Alice, got %v", vals)
	}
}

func TestCallAdapter_ErrorsGoToStreamErrors(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{}).RegisterHandle(b)

	ch := make(chan createReq, 1)
	ch <- createReq{Name: "Bob"}
	close(ch)

	p, err := ports.NewIOPort[createReq, userResp]("call", createReqCodec, userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, nethttp.CallAdapter(http.DefaultClient, srv.URL, h, nethttp.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, gstream.From(ctx, ch))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) == 0 {
		t.Error("want error from 500 response, got 0")
	}
	var use nethttp.UnexpectedStatusError
	if !errors.As(errs[0], &use) {
		t.Errorf("want UnexpectedStatusError, got %T", errs[0])
	}
}

// ── DrainCallAdapter ──────────────────────────────────────────────────────────

func TestDrainCallAdapter_PostsEachItem(t *testing.T) {
	ctx := context.Background()
	var received []string
	var mu strings.Builder

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf strings.Builder
		fmt.Fscan(r.Body, &buf)
		received = append(received, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ok","name":"ok"}`)
	}))
	defer srv.Close()
	_ = mu

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/notify",
		createReqCodec, userRespCodec, rest.RouteMeta{}).RegisterHandle(b)

	ch := make(chan createReq, 2)
	ch <- createReq{Name: "A"}
	ch <- createReq{Name: "B"}
	close(ch)

	p, err := ports.NewSinkPort[createReq]("drain", createReqCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, nethttp.DrainCallAdapter(http.DefaultClient, srv.URL, h, nethttp.DrainCallOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if len(received) != 2 {
		t.Errorf("want 2 POST calls, got %d", len(received))
	}
}

// ── G1: per-item vars derivation (shipped) ───────────────────────────────────

// G1-1: DrainCallAdapter derives path vars PER-ITEM from each item's own
// merge fields when opts.Vars is nil — two items with different IDs must
// resolve to two different concrete paths.
func TestDrainCallAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx := context.Background()
	var gotPaths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Alice"}`))
	}))
	defer srv.Close()

	handle := newClientActivityRoute()
	ch := make(chan getUserActivityReq, 2)
	ch <- getUserActivityReq{ID: "u1", Filter: "logins"}
	ch <- getUserActivityReq{ID: "u2", Filter: "posts"}
	close(ch)

	p, err := ports.NewSinkPort[getUserActivityReq]("drain-merge", codex.Struct[getUserActivityReq](), ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// opts.Vars left nil -> per-item derivation via CallHandle.
	p.Bind(ctx, nethttp.DrainCallAdapter(srv.Client(), srv.URL, handle, nethttp.DrainCallOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if len(gotPaths) != 2 {
		t.Fatalf("want 2 requests, got %d: %v", len(gotPaths), gotPaths)
	}
	if gotPaths[0] != "/users/u1/activity" || gotPaths[1] != "/users/u2/activity" {
		t.Errorf("want per-item resolved paths, got %v", gotPaths)
	}
}

// G1-2: an explicit (non-nil) DrainCallOptions.Vars still wins — regression
// guard matching today's static-vars behavior when set.
func TestDrainCallAdapter_ExplicitVarsStillWins(t *testing.T) {
	ctx := context.Background()
	var gotPaths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Alice"}`))
	}))
	defer srv.Close()

	handle := newClientActivityRoute()
	ch := make(chan getUserActivityReq, 2)
	ch <- getUserActivityReq{ID: "ignored-1", Filter: "logins"}
	ch <- getUserActivityReq{ID: "ignored-2", Filter: "posts"}
	close(ch)

	p, err := ports.NewSinkPort[getUserActivityReq]("drain-static", codex.Struct[getUserActivityReq](), ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// Explicit static Vars: every item resolves to the SAME path, regardless
	// of the item's own ID field.
	p.Bind(ctx, nethttp.DrainCallAdapter(srv.Client(), srv.URL, handle,
		nethttp.DrainCallOptions{Vars: map[string]string{"id": "static-id"}}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if len(gotPaths) != 2 {
		t.Fatalf("want 2 requests, got %d: %v", len(gotPaths), gotPaths)
	}
	for _, p := range gotPaths {
		if p != "/users/static-id/activity" {
			t.Errorf("want static path for every item, got %q", p)
		}
	}
}

// G1-3 (nethttp side): CallAdapter (IOAdapter) derives vars PER-ITEM when
// opts.Vars is nil, mirroring DrainCallAdapter's fix.
func TestCallAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx := context.Background()
	var gotPaths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Alice"}`))
	}))
	defer srv.Close()

	handle := newClientActivityRoute()
	ch := make(chan getUserActivityReq, 2)
	ch <- getUserActivityReq{ID: "u1", Filter: "logins"}
	ch <- getUserActivityReq{ID: "u2", Filter: "posts"}
	close(ch)

	p, err := ports.NewIOPort[getUserActivityReq, userRespWithMeta]("call-merge",
		codex.Struct[getUserActivityReq](), userRespWithMetaBodyCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, nethttp.CallAdapter(srv.Client(), srv.URL, handle, nethttp.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, gstream.From(ctx, ch))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(gotPaths) != 2 || gotPaths[0] != "/users/u1/activity" || gotPaths[1] != "/users/u2/activity" {
		t.Errorf("want per-item resolved paths, got %v", gotPaths)
	}
}

// ── PipelineAdapter ───────────────────────────────────────────────────────────

func TestPipelineAdapter_RegistersAndHandlesRequests(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	b := rest.NewBuilder(testInfo)
	handle, _ := rest.NewRoute[createReq, userResp]("POST", "/pipeline",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "pipeline"}).RegisterHandle(b)

	p, err := ports.NewToolPort[createReq, userResp]("pipeline-tool", createReqCodec, userRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, req createReq) gstream.Stream[userResp] {
		return gstream.Single(context.Background(), userResp{ID: "u1", Name: req.Name})
	})

	if err := p.Bind(ctx, nethttp.PipelineAdapter(mux, handle, nethttp.PipelineAdapterOptions{})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/pipeline", "application/json", strings.NewReader(`{"name":"Alice"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("want 201, got %d", resp.StatusCode)
	}
}

// ── Phase C: pattern-derived ingest + SSE handles ─────────────────────────────

// TestIngestAdapter_ViaRESTPattern is the ingest end-to-end proof: the route
// is declared ONCE on the SourcePort (ports.RESTPattern); ports.RESTHandle
// derives the RouteHandle[T, struct{}] the existing adapter accepts unchanged.
func TestIngestAdapter_ViaRESTPattern(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	p, err := ports.NewSourcePort[createReq]("ingest-pattern", createReqCodec, ports.PortOptions{
		Buffer: 4,
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, err := p.PluginRESTPattern(ports.RESTPattern{Method: "POST", Path: "/ingest2", Opts: []rest.RouteOpt{
		rest.RouteMeta{OperationID: "ingestPattern"},
	}})
	if err != nil {
		t.Fatalf("want pattern-derived ingest handle, got err %v", err)
	}
	p.Bind(ctx, nethttp.IngestAdapter(mux, handle, nethttp.IngestAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	time.Sleep(20 * time.Millisecond)

	// Valid body → 201 + item on the stream.
	resp, err := http.Post(srv.URL+"/ingest2", "application/json", strings.NewReader(`{"name":"Bob"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("want 201, got %d", resp.StatusCode)
	}
	select {
	case v := <-s.Values:
		if v.Name != "Bob" {
			t.Errorf("want Bob, got %q", v.Name)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for ingested item")
	}

	// Invalid body → 400, never reaches the stream.
	resp2, err := http.Post(srv.URL+"/ingest2", "application/json", strings.NewReader(`{"name":""}`))
	if err != nil {
		t.Fatalf("POST bad: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for invalid body, got %d", resp2.StatusCode)
	}
	cancel()
}

// TestSSEAdapter_ViaRESTPattern is the SSE end-to-end proof: the SSE route is
// declared ONCE on the SinkPort (ports.RESTPattern, always GET);
// ports.SSEHandle derives the SSERouteHandle[struct{}, Event] the existing
// adapter accepts unchanged.
func TestSSEAdapter_ViaRESTPattern(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	p, err := ports.NewSinkPort[createReq]("sse-pattern", createReqCodec, ports.PortOptions{
		Buffer: 4,
	})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, err := p.PluginRESTPattern(ports.RESTPattern{Path: "/events2", Opts: []rest.RouteOpt{
		rest.RouteMeta{OperationID: "ssePattern"},
	}})
	if err != nil {
		t.Fatalf("want pattern-derived SSE handle, got err %v", err)
	}
	p.Bind(ctx, nethttp.SSEAdapter(mux, handle, nethttp.SSEAdapterOptions{}))

	srv := httptest.NewServer(mux)
	defer srv.Close()
	time.Sleep(20 * time.Millisecond)

	// Pump events through the port's Push lifecycle in the background —
	// SSEHandler commits response headers on the FIRST event, so the client's
	// Do() below only returns once an event has been broadcast.
	p.Start(ctx)
	go func() {
		for i := 0; ; i++ {
			if err := p.Push(ctx, createReq{Name: "Eve"}); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	reqCtx, cancelReq := context.WithTimeout(ctx, 3*time.Second)
	defer cancelReq()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/events2", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("want SSE content type, got %q", ct)
	}

	buf := make([]byte, 256)
	var received string
	for !strings.Contains(received, "Eve") {
		n, err := resp.Body.Read(buf) // bounded by reqCtx timeout
		if n > 0 {
			received += string(buf[:n])
		}
		if err != nil {
			break
		}
	}
	if !strings.Contains(received, "Eve") {
		t.Errorf("want event containing Eve, got %q", received)
	}
	cancel()
}
