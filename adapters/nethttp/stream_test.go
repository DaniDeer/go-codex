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
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── HandlerLatest ─────────────────────────────────────────────────────────────

func newGetHandle() (*rest.RouteHandle[getReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[getReq, userResp]("GET", "/latest",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "getLatest"}).Register(b)
}

func TestHandlerLatest_ReturnsLatestValue(t *testing.T) {
	handle, err := newGetHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	valCh := make(chan userResp, 1)
	valCh <- userResp{ID: "u1", Name: "Alice"}
	errCh := make(chan error)
	close(errCh)
	src := gstream.Stream[userResp]{Values: valCh, Errors: errCh}

	h := nethttp.HandlerLatest(handle, src, nethttp.Options{})
	// Give background goroutine time to populate latest.
	time.Sleep(20 * time.Millisecond)
	close(valCh)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/latest", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Alice") {
		t.Errorf("want Alice in body, got: %s", rec.Body.String())
	}
}

func TestHandlerLatest_NoValueReturns503(t *testing.T) {
	handle, err := newGetHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	// Empty stream — no values.
	valCh := make(chan userResp)
	errCh := make(chan error)
	close(valCh)
	close(errCh)
	src := gstream.Stream[userResp]{Values: valCh, Errors: errCh}

	var capturedErr error
	h := nethttp.HandlerLatest(handle, src, nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
			capturedErr = e
			http.Error(w, e.Error(), status)
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/latest", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rec.Code)
	}
	var nlv nethttp.NoLatestValueError
	if !errors.As(capturedErr, &nlv) {
		t.Errorf("want NoLatestValueError, got %T", capturedErr)
	}
}

// ── HandlerIngest ─────────────────────────────────────────────────────────────

func newIngestHandle() (*rest.RouteHandle[createReq, struct{}], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[createReq, struct{}]("POST", "/ingest",
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "ingest"}).Register(b)
}

func TestHandlerIngest_WritesToChannel(t *testing.T) {
	handle, err := newIngestHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	dst := make(chan createReq, 1)
	h := nethttp.HandlerIngest(handle, dst, nethttp.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ingest",
		strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	// POST routes default to 201 Created.
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-dst:
		if got.Name != "Alice" {
			t.Errorf("want Alice, got %q", got.Name)
		}
	default:
		t.Error("want item in channel, got empty")
	}
}

func TestHandlerIngest_FullChannelReturns503(t *testing.T) {
	handle, err := newIngestHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	dst := make(chan createReq, 1)
	dst <- createReq{Name: "existing"} // fill the channel

	var capturedErr error
	h := nethttp.HandlerIngest(handle, dst, nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
			capturedErr = e
			http.Error(w, e.Error(), status)
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ingest",
		strings.NewReader(`{"name":"Bob"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rec.Code)
	}
	var pfe nethttp.PipelineFullError
	if !errors.As(capturedErr, &pfe) {
		t.Errorf("want PipelineFullError, got %T", capturedErr)
	}
	if pfe.Capacity != 1 {
		t.Errorf("Capacity: want 1, got %d", pfe.Capacity)
	}
}

// ── PipelineHandler ───────────────────────────────────────────────────────────

func newPipelineRoute() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[createReq, userResp]("POST", "/pipeline",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "pipeline"}).Register(b)
}

func TestPipelineHandler_ReturnsFirstValue(t *testing.T) {
	handle, err := newPipelineRoute()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	h := nethttp.PipelineHandler(handle,
		func(ctx context.Context, req createReq) gstream.Stream[userResp] {
			return gstream.Single(ctx, userResp{ID: "u1", Name: req.Name})
		},
		nethttp.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Bob"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	// POST route defaults to 201 Created.
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Bob") {
		t.Errorf("want Bob in body, got: %s", rec.Body.String())
	}
}

func TestPipelineHandler_PipelineErrorReturns500(t *testing.T) {
	handle, err := newPipelineRoute()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	h := nethttp.PipelineHandler(handle,
		func(ctx context.Context, _ createReq) gstream.Stream[userResp] {
			errCh := make(chan error, 1)
			valCh := make(chan userResp)
			errCh <- fmt.Errorf("compute failed")
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		},
		nethttp.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", rec.Code)
	}
}

func TestPipelineHandler_NoValueReturnsPipelineNoResponseError(t *testing.T) {
	handle, err := newPipelineRoute()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	var capturedErr error
	h := nethttp.PipelineHandler(handle,
		func(ctx context.Context, _ createReq) gstream.Stream[userResp] {
			errCh := make(chan error)
			valCh := make(chan userResp)
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		},
		nethttp.Options{
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
				capturedErr = e
				http.Error(w, e.Error(), status)
			},
		})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	var pnr nethttp.PipelineNoResponseError
	if !errors.As(capturedErr, &pnr) {
		t.Errorf("want PipelineNoResponseError, got %T", capturedErr)
	}
}

func TestPipelineHandler_WithTapObservation(t *testing.T) {
	handle, err := newPipelineRoute()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	var tapFired bool
	var tapped string
	h := nethttp.PipelineHandler(handle,
		func(ctx context.Context, req createReq) gstream.Stream[userResp] {
			s := gstream.Single(ctx, req)
			s = gstream.Tap(ctx, s, func(v createReq) { tapFired = true; tapped = v.Name })
			return gstream.FlatMapSlice(ctx, s, func(v createReq) []userResp {
				return []userResp{{ID: "u1", Name: v.Name}}
			})
		},
		nethttp.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Carol"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if !tapFired {
		t.Error("Tap should have fired during pipeline execution")
	}
	if tapped != "Carol" {
		t.Errorf("tapped: want Carol, got %q", tapped)
	}
}
