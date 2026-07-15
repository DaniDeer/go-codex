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
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "ingest"}).Register(b)
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
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

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
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).Register(b)

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
		createReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

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
		createReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

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

// ── PipelineAdapter ───────────────────────────────────────────────────────────

func TestPipelineAdapter_RegistersAndHandlesRequests(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	b := rest.NewBuilder(testInfo)
	handle, _ := rest.NewRoute[createReq, userResp]("POST", "/pipeline",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "pipeline"}).Register(b)

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
