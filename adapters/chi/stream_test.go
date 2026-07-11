package chi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── shared helpers ────────────────────────────────────────────────────────────

func newChiGetHandle() (*rest.RouteHandle[getReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[getReq, userResp]("GET", "/latest",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "chiGetLatest"}).Register(b)
}

func newChiIngestHandle() (*rest.RouteHandle[createReq, struct{}], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[createReq, struct{}]("POST", "/ingest",
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "chiIngest"}).Register(b)
}

func newChiPipelineHandle() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[createReq, userResp]("POST", "/pipeline",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "chiPipeline"}).Register(b)
}

// ── HandlerLatest ─────────────────────────────────────────────────────────────

func TestChiHandlerLatest_ReturnsLatestValue(t *testing.T) {
	handle, err := newChiGetHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	valCh := make(chan userResp, 1)
	valCh <- userResp{ID: "u1", Name: "Alice"}
	errCh := make(chan error)
	close(errCh)
	src := gstream.Stream[userResp]{Values: valCh, Errors: errCh}

	h := chiadapter.HandlerLatest(handle, src, chiadapter.Options{})
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

func TestChiHandlerLatest_NoValueReturns503(t *testing.T) {
	handle, err := newChiGetHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	valCh := make(chan userResp)
	errCh := make(chan error)
	close(valCh)
	close(errCh)
	src := gstream.Stream[userResp]{Values: valCh, Errors: errCh}

	var capturedErr error
	h := chiadapter.HandlerLatest(handle, src, chiadapter.Options{
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
	var nlv chiadapter.NoLatestValueError
	if !errors.As(capturedErr, &nlv) {
		t.Errorf("want NoLatestValueError, got %T", capturedErr)
	}
}

// ── HandlerIngest ─────────────────────────────────────────────────────────────

func TestChiHandlerIngest_WritesToChannel(t *testing.T) {
	handle, err := newChiIngestHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	dst := make(chan createReq, 1)
	h := chiadapter.HandlerIngest(handle, dst, chiadapter.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ingest",
		strings.NewReader(`{"name":"Bob"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	// POST defaults to 201 Created
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-dst:
		if got.Name != "Bob" {
			t.Errorf("Name: want Bob, got %q", got.Name)
		}
	default:
		t.Error("want item in channel, got empty")
	}
}

func TestChiHandlerIngest_FullChannelReturns503(t *testing.T) {
	handle, err := newChiIngestHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	dst := make(chan createReq, 1)
	dst <- createReq{Name: "existing"} // fill the channel

	var capturedErr error
	h := chiadapter.HandlerIngest(handle, dst, chiadapter.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
			capturedErr = e
			http.Error(w, e.Error(), status)
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ingest",
		strings.NewReader(`{"name":"Carol"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rec.Code)
	}
	var pfe chiadapter.PipelineFullError
	if !errors.As(capturedErr, &pfe) {
		t.Errorf("want PipelineFullError, got %T", capturedErr)
	}
	if pfe.Capacity != 1 {
		t.Errorf("Capacity: want 1, got %d", pfe.Capacity)
	}
}

// ── PipelineHandler ───────────────────────────────────────────────────────────

func TestChiPipelineHandler_ReturnsFirstValue(t *testing.T) {
	handle, err := newChiPipelineHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	h := chiadapter.PipelineHandler(handle,
		func(ctx context.Context, req createReq) gstream.Stream[userResp] {
			return gstream.Single(ctx, userResp{ID: "u1", Name: req.Name})
		}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Dave"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Dave") {
		t.Errorf("want Dave in body, got: %s", rec.Body.String())
	}
}

func TestChiPipelineHandler_PipelineErrorReturns500(t *testing.T) {
	handle, err := newChiPipelineHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	h := chiadapter.PipelineHandler(handle,
		func(ctx context.Context, _ createReq) gstream.Stream[userResp] {
			errCh := make(chan error, 1)
			valCh := make(chan userResp)
			errCh <- fmt.Errorf("compute failed")
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Eve"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", rec.Code)
	}
}

func TestChiPipelineHandler_TapObservation(t *testing.T) {
	handle, err := newChiPipelineHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	var tapFired bool
	var tapped string
	h := chiadapter.PipelineHandler(handle,
		func(ctx context.Context, req createReq) gstream.Stream[userResp] {
			s := gstream.Single(ctx, req)
			s = gstream.Tap(ctx, s, func(v createReq) { tapFired = true; tapped = v.Name })
			return gstream.FlatMapSlice(ctx, s, func(v createReq) []userResp {
				return []userResp{{ID: "u1", Name: v.Name}}
			})
		}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Frank"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if !tapFired {
		t.Error("Tap should have fired")
	}
	if tapped != "Frank" {
		t.Errorf("tapped: want Frank, got %q", tapped)
	}
}

func TestChiPipelineHandler_NoValueReturnsPipelineNoResponseError(t *testing.T) {
	handle, err := newChiPipelineHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	var capturedErr error
	h := chiadapter.PipelineHandler(handle,
		func(ctx context.Context, _ createReq) gstream.Stream[userResp] {
			errCh := make(chan error)
			valCh := make(chan userResp)
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		}, chiadapter.Options{
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
				capturedErr = e
				http.Error(w, e.Error(), status)
			},
		})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Grace"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	var pnr chiadapter.PipelineNoResponseError
	if !errors.As(capturedErr, &pnr) {
		t.Errorf("want PipelineNoResponseError, got %T", capturedErr)
	}
}
