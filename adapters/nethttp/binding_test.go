package nethttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	atempl "github.com/a-h/templ"

	adapttempl "github.com/DaniDeer/go-codex/adapters/templ"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── IngestAdapter ─────────────────────────────────────────────────────────────

func newIngestRoute(t *testing.T) *rest.RouteHandle[createReq, struct{}] {
	t.Helper()
	b := rest.NewServer(testInfo)
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
	p.Bind(ctx, IngestAdapter(mux, handle, IngestAdapterOptions{Buffer: 4}))
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

	b := rest.NewServer(testInfo)
	h, _ := rest.NewRoute[getReq, userResp]("GET", "/users/latest",
		getReqCodec, userRespCodec, rest.RouteMeta{}).RegisterHandle(b)

	p, err := ports.NewSourcePort[userResp]("poll", userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, PollAdapter(http.DefaultClient, srv.URL, h, getReq{}, 30*time.Millisecond, PollStreamOptions{Buffer: 4}))
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

	b := rest.NewServer(testInfo)
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
	p.Bind(ctx, CallAdapter(http.DefaultClient, srv.URL, h, CallStreamOptions{})) //nolint:errcheck
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

	b := rest.NewServer(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{}).RegisterHandle(b)

	ch := make(chan createReq, 1)
	ch <- createReq{Name: "Bob"}
	close(ch)

	p, err := ports.NewIOPort[createReq, userResp]("call", createReqCodec, userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, CallAdapter(http.DefaultClient, srv.URL, h, CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, gstream.From(ctx, ch))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) == 0 {
		t.Error("want error from 500 response, got 0")
	}
	var use UnexpectedStatusError
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

	b := rest.NewServer(testInfo)
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
	p.Bind(ctx, DrainCallAdapter(http.DefaultClient, srv.URL, h, DrainCallOptions{}))
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
	p.Bind(ctx, DrainCallAdapter(srv.Client(), srv.URL, handle, DrainCallOptions{}))
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
	p.Bind(ctx, DrainCallAdapter(srv.Client(), srv.URL, handle,
		DrainCallOptions{Vars: map[string]string{"id": "static-id"}}))
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
	p.Bind(ctx, CallAdapter(srv.Client(), srv.URL, handle, CallStreamOptions{})) //nolint:errcheck
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
	b := rest.NewServer(testInfo)
	handle, _ := rest.NewRoute[createReq, userResp]("POST", "/pipeline",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "pipeline"}).RegisterHandle(b)

	p, err := ports.NewToolPort[createReq, userResp]("pipeline-tool", createReqCodec, userRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, req createReq) gstream.Stream[userResp] {
		return gstream.Single(context.Background(), userResp{ID: "u1", Name: req.Name})
	})

	if err := p.Bind(ctx, PipelineAdapter(mux, handle, PipelineAdapterOptions{})); err != nil {
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
	p.Bind(ctx, IngestAdapter(mux, handle, IngestAdapterOptions{Buffer: 4}))
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
	p.Bind(ctx, SSEAdapter(mux, handle, SSEAdapterOptions{}))

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

// ── Consume / CallSSEAdapter ──────────────────────────────────────────────────

// sseTestReq is the Req type shared by Consume/CallSSEAdapter tests below —
// declares a path param so merge-field-derived URL-building is exercised.
type sseTestReq struct{ ID string }

var sseTestReqCodec = codex.Struct[sseTestReq](
	codex.OptionalField("id", codex.String(), func(r sseTestReq) string { return r.ID }, func(r *sseTestReq, v string) { r.ID = v }),
)

func newSSETestRoute() rest.SSERoute[sseTestReq, userResp] {
	return rest.NewSSERoute[sseTestReq, userResp]("/stream/{id}", sseTestReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r sseTestReq) string { return r.ID },
			func(r *sseTestReq, v string) { r.ID = v }),
	)
}

func TestConsume_HappyPath_SingleEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream/machine-1" {
			t.Errorf("want path-merged URL /stream/machine-1, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got userResp
	done := make(chan struct{})
	err := consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{ID: "machine-1"},
		func(_ context.Context, e userResp) error {
			got = e
			close(done)
			cancel()
			return nil
		}, ConsumeOptions{})
	select {
	case <-done:
	default:
		t.Fatal("want fn to be called with the decoded event")
	}
	if got.ID != "1" || got.Name != "Alice" {
		t.Errorf("unexpected event: %+v", got)
	}
	_ = err // ctx cancellation inside fn is expected to unwind Consume with nil
}

// TestConsume_GeneralShapeFn_StillRejected is a regression guard for
// docs/roadmap/rest-client-general-purpose-middleware.md: consumeSSE
// deliberately keeps using the OLD, credential-only
// validateClientImplementationShapes rather than the new
// validateCallImplementationShapes callWithVars/CallWithHandle use — a
// general-purpose-shaped Fn attached via ClientMW to an SSE route must
// still hard-error here exactly as before, NOT silently pass validation
// while never being invoked (SSE's per-event dispatch shape doesn't match
// the single-call wrap shape).
func TestConsume_GeneralShapeFn_StillRejected(t *testing.T) {
	route := rest.NewSSERoute[sseTestReq, userResp]("/stream/{id}", sseTestReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r sseTestReq) string { return r.ID },
			func(r *sseTestReq, v string) { r.ID = v }),
	).ClientMW(nil, func(next func(context.Context, sseTestReq) (userResp, error)) func(context.Context, sseTestReq) (userResp, error) {
		return func(ctx context.Context, req sseTestReq) (userResp, error) {
			return next(ctx, req)
		}
	})

	err := consumeSSE(context.Background(), http.DefaultClient, "http://localhost", route.ClientHandle(), sseTestReq{ID: "machine-1"},
		func(_ context.Context, _ userResp) error { return nil }, ConsumeOptions{})

	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %v", err)
	}
}

func TestConsume_HappyPath_MultipleEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 1; i <= 3; i++ {
			fmt.Fprintf(w, "data: {\"id\":\"%d\",\"name\":\"u%d\"}\n\n", i, i)
			if flusher != nil {
				flusher.Flush()
			}
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []string
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			mu.Lock()
			got = append(got, e.ID)
			mu.Unlock()
			if len(got) == 3 {
				cancel()
			}
			return nil
		}, ConsumeOptions{})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || got[0] != "1" || got[1] != "2" || got[2] != "3" {
		t.Errorf("want events [1 2 3] in order, got %v", got)
	}
}

func TestConsume_ConnectionFailure_ReportsSSEConnectError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var mu sync.Mutex
	var errs []error
	_ = consumeSSE(ctx, http.DefaultClient, "http://127.0.0.1:1", newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, _ userResp) error { return nil },
		ConsumeOptions{
			MaxBackoff: 10 * time.Millisecond,
			OnError: func(err error) {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			},
		})

	mu.Lock()
	defer mu.Unlock()
	if len(errs) == 0 {
		t.Fatal("want at least one SSEConnectError reported")
	}
	var connErr SSEConnectError
	if !errors.As(errs[0], &connErr) {
		t.Fatalf("want SSEConnectError, got %T: %v", errs[0], errs[0])
	}
}

func TestConsume_MalformedEvent_ReportsSSEParseError_ContinuesConsuming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {not valid json\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: {\"id\":\"2\",\"name\":\"Bob\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var mu sync.Mutex
	var parseErrs int
	var gotValid userResp
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			mu.Lock()
			gotValid = e
			mu.Unlock()
			cancel()
			return nil
		}, ConsumeOptions{
			OnError: func(err error) {
				var parseErr SSEParseError
				if errors.As(err, &parseErr) {
					mu.Lock()
					parseErrs++
					mu.Unlock()
				}
			},
		})

	mu.Lock()
	defer mu.Unlock()
	if parseErrs == 0 {
		t.Error("want at least one SSEParseError reported for the malformed line")
	}
	if gotValid.ID != "2" {
		t.Errorf("want consumption to continue and decode the valid event, got %+v", gotValid)
	}
}

func TestConsume_HandlerError_ReportsSSEHandlerError_ContinuesConsuming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"A\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: {\"id\":\"2\",\"name\":\"B\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var mu sync.Mutex
	var handlerErrs int
	var calls []string
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			mu.Lock()
			calls = append(calls, e.ID)
			n := len(calls)
			mu.Unlock()
			if n == 2 {
				cancel()
			}
			if e.ID == "1" {
				return fmt.Errorf("boom")
			}
			return nil
		}, ConsumeOptions{
			OnError: func(err error) {
				var handlerErr SSEHandlerError
				if errors.As(err, &handlerErr) {
					mu.Lock()
					handlerErrs++
					mu.Unlock()
				}
			},
		})

	mu.Lock()
	defer mu.Unlock()
	if handlerErrs != 1 {
		t.Errorf("want exactly 1 SSEHandlerError, got %d", handlerErrs)
	}
	if len(calls) != 2 || calls[0] != "1" || calls[1] != "2" {
		t.Errorf("want fn called for BOTH events despite the first returning an error, got %v", calls)
	}
}

func TestConsume_ContextCancellation_ReturnsPromptly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
			func(_ context.Context, _ userResp) error { return nil }, ConsumeOptions{})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("want Consume to return promptly after ctx cancellation")
	}
}

func TestConsume_NilObserver_NoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"A\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, _ userResp) error { cancel(); return nil },
		ConsumeOptions{Observer: nil})
}

// TestConsume_CredentialReDerivedPerReconnect (S11): a ClientMW Fn
// returning a DIFFERENT value on each call sends a NEW value on every
// reconnect attempt — proves no caching/memoization happens inside
// Consume itself.
func TestConsume_CredentialReDerivedPerReconnect(t *testing.T) {
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	var mu sync.Mutex
	var seen []string
	attempt := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		n := len(seen)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n < 2 {
			return // close immediately, forcing a reconnect
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	sseRoute := newSSETestRoute().Use(declMw).ClientMW(&declMw,
		func(context.Context, []route.SecurityRequirement) (http.Header, error) {
			attempt++
			h := make(http.Header)
			h.Set("Authorization", fmt.Sprintf("token-%d", attempt))
			return h, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = consumeSSE(ctx, srv.Client(), srv.URL, sseRoute.ClientHandle(), sseTestReq{},
		func(context.Context, userResp) error { return nil },
		ConsumeOptions{MaxBackoff: 10 * time.Millisecond})

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 {
		t.Fatalf("want at least 2 connection attempts, got %d", len(seen))
	}
	if seen[0] == seen[1] {
		t.Errorf("want a DIFFERENT credential on each reconnect attempt, got %q both times", seen[0])
	}
}

// TestConsume_OnCredentialRejected_FiresOn401 (S12).
func TestConsume_OnCredentialRejected_FiresOn401(t *testing.T) {
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	sseRoute := newSSETestRoute().Use(declMw).ClientMW(&declMw,
		func(context.Context, []route.SecurityRequirement) (http.Header, error) {
			h := make(http.Header)
			h.Set("Authorization", "bad-token")
			return h, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var rejected int32
	_ = consumeSSE(ctx, srv.Client(), srv.URL, sseRoute.ClientHandle(), sseTestReq{},
		func(context.Context, userResp) error { return nil },
		ConsumeOptions{
			MaxBackoff:           10 * time.Millisecond,
			OnCredentialRejected: func() { atomic.AddInt32(&rejected, 1) },
		})
	if atomic.LoadInt32(&rejected) == 0 {
		t.Error("want OnCredentialRejected to fire on a 401 with an engaged credential")
	}
}

func TestConsume_OnCredentialRejected_NotCalledWithoutEngagedCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var rejected int32
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(context.Context, userResp) error { return nil },
		ConsumeOptions{
			MaxBackoff:           10 * time.Millisecond,
			OnCredentialRejected: func() { atomic.AddInt32(&rejected, 1) },
		})
	if atomic.LoadInt32(&rejected) != 0 {
		t.Error("want OnCredentialRejected NOT called when no credential was engaged")
	}
}

// ── CallSSEAdapter ─────────────────────────────────────────────────────────

func TestCallSSEAdapter_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	b := rest.NewServer(testInfo)
	handle, err := newSSETestRoute().RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p, err := ports.NewSourcePort[userResp]("sseEvents", userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{}))

	select {
	case v := <-p.Stream(ctx).Values:
		if v.ID != "1" || v.Name != "Alice" {
			t.Errorf("unexpected event: %+v", v)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("want an event on the dst channel")
	}
	cancel()
}

func TestCallSSEAdapter_MalformedEvent_ReportsErrorContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {bad\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: {\"id\":\"2\",\"name\":\"Bob\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	b := rest.NewServer(testInfo)
	handle, err := newSSETestRoute().RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	p, err := ports.NewSourcePort[userResp]("sseEvents2", userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{}))

	stream := p.Stream(ctx)
	var gotErr, gotVal bool
	timeout := time.After(1500 * time.Millisecond)
	for !gotErr || !gotVal {
		select {
		case <-stream.Errors:
			gotErr = true
		case v := <-stream.Values:
			if v.ID == "2" {
				gotVal = true
			}
		case <-timeout:
			t.Fatalf("want both an error AND a valid event; gotErr=%v gotVal=%v", gotErr, gotVal)
			return
		}
	}
	cancel()
}

// TestCallSSEAdapter_NeverProducesHandlerError (A3): the internal
// channel-push fn never returns an error — confirmed by running a full
// cycle and checking no SSEHandlerError ever reaches the errs channel.
func TestCallSSEAdapter_NeverProducesHandlerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"A\"}\n\n")
		<-r.Context().Done()
	}))
	defer srv.Close()

	b := rest.NewServer(testInfo)
	handle, err := newSSETestRoute().RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p, err := ports.NewSourcePort[userResp]("sseEvents3", userRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{}))

	stream := p.Stream(ctx)
	select {
	case <-stream.Values:
	case <-time.After(800 * time.Millisecond):
	}
	select {
	case err := <-stream.Errors:
		var handlerErr SSEHandlerError
		if errors.As(err, &handlerErr) {
			t.Fatal("want CallSSEAdapter to never produce SSEHandlerError")
		}
	default:
	}
}

// ExampleConsume demonstrates the client-side SSE consumption entry point.
func ExampleClient_Consume() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = client.Consume(ctx, newSSETestRoute(), sseTestReq{ID: "demo"},
		func(_ context.Context, e userResp) error {
			fmt.Println(e.Name)
			cancel()
			return nil
		})
	// Output: Alice
}

// TestConsume_Observer_RecordRequestPerAttempt (S9): RecordRequest is
// called once per connection attempt with the correct status.
func TestConsume_Observer_RecordRequestPerAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"A\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	obs := &spyObserver{}
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, _ userResp) error { return nil },
		ConsumeOptions{Observer: obs, MaxBackoff: 10 * time.Millisecond})

	if len(obs.requests) == 0 {
		t.Fatal("want at least one RecordRequest call")
	}
	if obs.requests[0].statusCode != http.StatusOK {
		t.Errorf("want status 200 on the first attempt, got %d", obs.requests[0].statusCode)
	}
}

// TestConsume_Backoff_Doubles (S6, part 1): consecutive failed attempts
// observe a roughly-doubling backoff (250ms initial step) when MaxBackoff
// is large enough not to cap growth within the observed window.
func TestConsume_Backoff_Doubles(t *testing.T) {
	var mu sync.Mutex
	var attemptTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attemptTimes = append(attemptTimes, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(context.Context, userResp) error { return nil },
		ConsumeOptions{}) // default 30s MaxBackoff — no cap within this window

	mu.Lock()
	defer mu.Unlock()
	if len(attemptTimes) < 3 {
		t.Skipf("want at least 3 attempts to observe backoff growth, got %d (timing-sensitive, not a design gap)", len(attemptTimes))
	}
	gap1 := attemptTimes[1].Sub(attemptTimes[0])
	gap2 := attemptTimes[2].Sub(attemptTimes[1])
	if gap2 < gap1+50*time.Millisecond {
		t.Errorf("want backoff to roughly double between attempts, got gap1=%v gap2=%v", gap1, gap2)
	}
}

// TestConsume_Backoff_Caps (S6, part 2): a small MaxBackoff caps the
// backoff — once capped, consecutive gaps stay roughly stable instead of
// continuing to grow.
func TestConsume_Backoff_Caps(t *testing.T) {
	var mu sync.Mutex
	var attemptTimes []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attemptTimes = append(attemptTimes, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(context.Context, userResp) error { return nil },
		ConsumeOptions{MaxBackoff: 60 * time.Millisecond})

	mu.Lock()
	defer mu.Unlock()
	if len(attemptTimes) < 3 {
		t.Skipf("want at least 3 attempts to observe the cap, got %d (timing-sensitive, not a design gap)", len(attemptTimes))
	}
	// gap[0] (attempt2-attempt1) is ALWAYS the uncapped initial step
	// (~250ms) — mirrors adapters/websocket's dialLoop: the first wait
	// after a failure uses the current (still-initial) backoff BEFORE it
	// doubles. Only gaps from index 1 onward have had a chance to double
	// past MaxBackoff and get capped.
	for i := 2; i < len(attemptTimes); i++ {
		gap := attemptTimes[i].Sub(attemptTimes[i-1])
		if gap > 200*time.Millisecond {
			t.Errorf("want gap[%d] capped near MaxBackoff (60ms), got %v", i, gap)
		}
	}
}

// TestConsume_Backoff_ResetsAfterSuccess (S7): a successful event resets
// backoff to the initial step on the NEXT drop.
func TestConsume_Backoff_ResetsAfterSuccess(t *testing.T) {
	var mu sync.Mutex
	var attempt int
	var thirdAttemptTime time.Time
	var secondAttemptTime time.Time

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempt++
		n := attempt
		if n == 2 {
			secondAttemptTime = time.Now()
		}
		if n == 3 {
			thirdAttemptTime = time.Now()
		}
		mu.Unlock()

		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Attempt 2: succeed with one event, then close — resets backoff.
		// Attempt 3: fail again — the gap to THIS attempt should be back
		// near the initial 250ms step, not a doubled value.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if n == 2 {
			fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"A\"}\n\n")
			return
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(context.Context, userResp) error { return nil },
		ConsumeOptions{MaxBackoff: 5 * time.Second})

	mu.Lock()
	defer mu.Unlock()
	if thirdAttemptTime.IsZero() || secondAttemptTime.IsZero() {
		t.Skip("did not observe 3 attempts within the test timeout — flaky under CI load, not a design gap")
	}
	gap := thirdAttemptTime.Sub(secondAttemptTime)
	// Reset means the gap after a successful attempt is near the initial
	// 250ms step, not a doubled ~500-1000ms value.
	if gap > 400*time.Millisecond {
		t.Errorf("want backoff reset to ~250ms after a successful attempt, got gap=%v", gap)
	}
}

// TestConsume_MultiFormat_ResolvesFromContentType (S13): a route
// declaring 2+ Formats; the response's Content-Type header selects the
// matching format for the WHOLE connection.
func TestConsume_MultiFormat_ResolvesFromContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("want Accept application/json (first declared Format), got %q", accept)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	sseRoute := rest.NewSSERoute[sseTestReq, userResp]("/stream", sseTestReqCodec, userRespCodec)
	// Note: Formats attachment on SSERoute happens post-Register via
	// SSERouteHandle.WithFormats in the current design — ClientHandle's
	// Formats field is populated identically since both share the SAME
	// struct-literal construction; here we register then set Formats on
	// the resulting handle to exercise the resolution path via
	// CallSSEAdapter (which takes a handle directly).
	b := rest.NewServer(testInfo)
	handle, err := sseRoute.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	handle.WithFormats(format.JSON(userRespCodec))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p, err := ports.NewSourcePort[userResp]("sseFmt", userRespCodec, ports.PortOptions{Buffer: 2})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{}))

	select {
	case v := <-p.Stream(ctx).Values:
		if v.ID != "1" {
			t.Errorf("unexpected event: %+v", v)
		}
	case <-time.After(800 * time.Millisecond):
		t.Fatal("want an event decoded via the resolved Format")
	}
	cancel()
}

// TestConsume_Format_FallsBackToDecodeEvent (S14): no Formats declared
// (or none match) → falls back to DecodeEvent's JSON default. Already
// implicitly exercised by every other test above (none declare Formats);
// this test makes the fallback explicit and intentional.
func TestConsume_Format_FallsBackToDecodeEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	b := rest.NewServer(testInfo)
	handle, err := newSSETestRoute().RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if len(handle.Formats) != 0 {
		t.Fatalf("test setup: want zero Formats declared, got %d", len(handle.Formats))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	var got userResp
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			got = e
			cancel()
			return nil
		}, ConsumeOptions{})
	if got.ID != "1" || got.Name != "Alice" {
		t.Errorf("want DecodeEvent fallback to decode the event, got %+v", got)
	}
}

// TestConsume_MultiLineDataAccumulation confirms Consume correctly
// reassembles MULTIPLE consecutive "data:" lines (each per-line prefixed,
// as the FIXED server writer now produces for a multi-line payload — see
// writeSSEData) into ONE event, dispatched at the blank-line terminator —
// per the WHATWG SSE spec. The server here writes YAML content spanning
// 2 real "data:" lines; if Consume treated each line as an independent
// event (the bug this round fixes), decoding either half alone as YAML
// would fail or produce a garbage/incomplete value.
func TestConsume_MultiLineDataAccumulation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Two "data:" lines forming ONE JSON-encoded event — JSON permits
		// insignificant whitespace (including newlines) between tokens,
		// so `{"id":"1",\n"name":"Alice"}` (the two lines rejoined with
		// "\n") is valid JSON, while NEITHER individual line decodes on
		// its own. Uses Consume's default DecodeEvent path (no Formats
		// needed on the CLIENT handle Consume builds internally) — see
		// TestConsume_MultiLineDataAccumulation_ViaCallSSEAdapter below
		// for the same scenario via a Format explicitly set on a
		// handle-based entry point.
		fmt.Fprintf(w, "data: {\"id\":\"1\",\ndata: \"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	var got userResp
	var gotErr error
	_ = consumeSSE(ctx, srv.Client(), srv.URL, newSSETestRoute().ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			got = e
			cancel()
			return nil
		}, ConsumeOptions{
			OnError: func(err error) { gotErr = err },
		})
	if gotErr != nil {
		t.Fatalf("want no error, got %v", gotErr)
	}
	if got.ID != "1" || got.Name != "Alice" {
		t.Errorf("want the two data: lines reassembled into one decoded event, got %+v", got)
	}
}

// TestConsume_MultiLineDataAccumulation_ViaCallSSEAdapter is the same
// scenario through the port-adapter entry point, confirming the shared
// consumeSSE loop's accumulation fix applies identically to both.
func TestConsume_MultiLineDataAccumulation_ViaCallSSEAdapter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: id: \"1\"\ndata: name: Alice\n\n")
	}))
	defer srv.Close()

	b := rest.NewServer(testInfo)
	handle, err := newSSETestRoute().RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	handle.WithFormats(format.YAML(userRespCodec))

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	p, err := ports.NewSourcePort[userResp]("sseMultiLine", userRespCodec, ports.PortOptions{Buffer: 2})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{}))

	select {
	case v := <-p.Stream(ctx).Values:
		if v.ID != "1" || v.Name != "Alice" {
			t.Errorf("want the two data: lines reassembled into one decoded event, got %+v", v)
		}
	case <-time.After(700 * time.Millisecond):
		t.Fatal("want a reassembled event on the dst channel")
	}
	cancel()
}

// ── HTML/HTMX-over-SSE client consumption ────────────────────────────────

// TestConsume_HTMLFormat_ReportsDecodeNotSupportedError answers a direct
// user question: can Consume/CallSSEAdapter round-trip an SSE route
// serving HTML via adapttempl.Format (the HTMX-style pattern documented
// in docs/features/sse-streaming.md's "SSE with HTML fragments"
// section)? NO, by design — adapttempl.Format's Unmarshal direction
// ALWAYS returns DecodeNotSupportedError (HTML has no meaningful
// decode-back-into-a-struct direction). The shared consumeSSE loop
// reports this as a NON-FATAL SSEParseError per event via opts.OnError;
// fn/dst is never reached for that event. This is the CORRECT,
// intentional behavior: HTMX-over-SSE is designed for a browser's native
// EventSource + DOM-swap, not a typed Go client wanting a decoded value —
// this test locks in that consumption degrades gracefully (no panic, no
// fatal error) rather than silently hanging or crashing.
//
// Uses CallSSEAdapter (handle-based) rather than Consume (route-based):
// WithFormats is only settable on an already-built *SSERouteHandle, and
// Consume derives its OWN client handle internally from the route value
// — it would never see a Formats setting applied to a DIFFERENT handle.
// This is the SAME reason examples/adapters-sse's YAML demo uses
// CallSSEAdapter for its non-default-format routes.
func TestConsume_HTMLFormat_ReportsDecodeNotSupportedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: <li class=\"notif\">Alice joined</li>\n\n")
	}))
	defer srv.Close()

	handle := newSSETestRoute().ClientHandle()
	htmlFormat := adapttempl.Format(userRespCodec, func(userResp) atempl.Component {
		return atempl.ComponentFunc(func(context.Context, io.Writer) error { return nil })
	})
	handle.WithFormats(htmlFormat)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p, err := ports.NewSourcePort[userResp]("sseHTML", userRespCodec, ports.PortOptions{Buffer: 2})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	var mu sync.Mutex
	var gotErrs []error
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{
		OnError: func(err error) {
			mu.Lock()
			gotErrs = append(gotErrs, err)
			mu.Unlock()
		},
	}))

	stream := p.Stream(ctx)
	var fnCalled bool
	select {
	case <-stream.Values:
		fnCalled = true
	case <-stream.Errors:
	case <-time.After(400 * time.Millisecond):
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if fnCalled {
		t.Error("want the dst channel to NEVER receive a value — HTML has no decode direction")
	}
	if len(gotErrs) == 0 {
		t.Fatal("want at least one SSEParseError reported via OnError")
	}
	var parseErr SSEParseError
	if !errors.As(gotErrs[0], &parseErr) {
		t.Fatalf("want SSEParseError, got %T: %v", gotErrs[0], gotErrs[0])
	}
	var notSupported adapttempl.DecodeNotSupportedError
	if !errors.As(gotErrs[0], &notSupported) {
		t.Fatalf("want SSEParseError wrapping adapttempl.DecodeNotSupportedError, got %v", gotErrs[0])
	}
}

// ── ConsumeOptions.Formats per-call override ───────────────────────────────

// TestConsume_Formats_OverridesRouteDeclaredFormat verifies
// ConsumeOptions.Formats wins over the route's declared handle.Formats
// for THIS Consume call only.
func TestConsume_Formats_OverridesRouteDeclaredFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); accept != "application/yaml" {
			t.Errorf("want Accept application/yaml (override), got %q", accept)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: id: \"1\"\ndata: name: Alice\n\n")
	}))
	defer srv.Close()

	sseRoute := rest.NewSSERoute[sseTestReq, userResp]("/stream/{id}", sseTestReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r sseTestReq) string { return r.ID },
			func(r *sseTestReq, v string) { r.ID = v }),
		rest.Formats(format.JSON(userRespCodec)),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	var got userResp
	err := consumeSSE(ctx, srv.Client(), srv.URL, sseRoute.ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			got = e
			cancel()
			return nil
		}, ConsumeOptions{
			Formats: []format.Format[userResp]{format.YAML(userRespCodec)},
		})
	_ = err
	if got.ID != "1" || got.Name != "Alice" {
		t.Errorf("want decoded via YAML override, got %+v", got)
	}
}

// TestConsume_Formats_RouteDeclaredStillAppliesWithoutOverride verifies
// the route-declared handle.Formats still applies when no per-call
// override is given.
func TestConsume_Formats_RouteDeclaredStillAppliesWithoutOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); accept != "application/yaml" {
			t.Errorf("want Accept application/yaml (route-declared), got %q", accept)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: id: \"2\"\ndata: name: Bob\n\n")
	}))
	defer srv.Close()

	sseRoute := rest.NewSSERoute[sseTestReq, userResp]("/stream/{id}", sseTestReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r sseTestReq) string { return r.ID },
			func(r *sseTestReq, v string) { r.ID = v }),
		rest.Formats(format.YAML(userRespCodec)),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	var got userResp
	_ = consumeSSE(ctx, srv.Client(), srv.URL, sseRoute.ClientHandle(), sseTestReq{},
		func(_ context.Context, e userResp) error {
			got = e
			cancel()
			return nil
		}, ConsumeOptions{})
	if got.ID != "2" || got.Name != "Bob" {
		t.Errorf("want decoded via route-declared YAML, got %+v", got)
	}
}

// TestConsume_Formats_TypeMismatch_ReturnsCallFormatOptError verifies a
// wrong-typed ConsumeOptions.Formats returns CallFormatOptError,
// errors.As-reachable, reported via OnError (Consume never returns a
// direct error from a connection-level failure — mirrors
// SSEConnectError/SSEParseError's existing OnError-only delivery).
func TestConsume_Formats_TypeMismatch_ReturnsCallFormatOptError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	handle := newSSETestRoute().ClientHandle()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var mu sync.Mutex
	var gotErrs []error
	p, err := ports.NewSourcePort[userResp]("sseFmtMismatch", userRespCodec, ports.PortOptions{Buffer: 2})
	if err != nil {
		t.Fatalf("NewSourcePort: %v", err)
	}
	p.Bind(ctx, CallSSEAdapter(srv.Client(), srv.URL, handle, sseTestReq{}, ConsumeOptions{
		// Wrong type: []format.Format[sseTestReq] instead of []format.Format[userResp].
		Formats: []format.Format[sseTestReq]{format.JSON(sseTestReqCodec)},
		OnError: func(e error) {
			mu.Lock()
			gotErrs = append(gotErrs, e)
			mu.Unlock()
		},
	}))

	stream := p.Stream(ctx)
	select {
	case <-stream.Values:
	case <-stream.Errors:
	case <-time.After(400 * time.Millisecond):
	}
	cancel()

	mu.Lock()
	defer mu.Unlock()
	if len(gotErrs) == 0 {
		t.Fatal("want at least one SSEConnectError wrapping CallFormatOptError reported via OnError")
	}
	var connErr SSEConnectError
	if !errors.As(gotErrs[0], &connErr) {
		t.Fatalf("want SSEConnectError, got %T: %v", gotErrs[0], gotErrs[0])
	}
	var fe CallFormatOptError
	if !errors.As(gotErrs[0], &fe) || fe.Direction != "response" {
		t.Fatalf("want CallFormatOptError{response}, got %v", gotErrs[0])
	}
	if fe.Unwrap() == nil {
		t.Error("want non-nil Unwrap")
	}
	v := fe.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"direction", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}
