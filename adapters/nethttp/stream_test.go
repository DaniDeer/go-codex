package nethttp_test

import (
	"bufio"
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

// ── SSEFromHub ────────────────────────────────────────────────────────────────

func newSSEHandle(t *testing.T) *rest.SSERouteHandle[getReq, userResp] {
	t.Helper()
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewSSERoute[getReq, userResp]("/events",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "streamEvents"}).Register(b)
	if err != nil {
		t.Fatalf("register SSE route: %v", err)
	}
	return h
}

func readSSEEvents(t *testing.T, resp *http.Response, want int) []string {
	t.Helper()
	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			lines = append(lines, strings.TrimPrefix(line, "data: "))
		}
		if len(lines) >= want {
			break
		}
	}
	return lines
}

func TestSSEFromHub_BroadcastsToAllClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := newSSEHandle(t)

	valCh := make(chan userResp, 3)
	src := gstream.From(ctx, valCh)
	hub := gstream.NewBroadcastHub(ctx, src, 8)

	fn := nethttp.SSEFromHub[getReq, userResp](hub, nethttp.SSEStreamOptions{Topic: "/events"})
	h := nethttp.SSEHandler(handle, fn, nethttp.Options{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	type result struct {
		resp *http.Response
		err  error
	}
	ch1 := make(chan result, 1)
	ch2 := make(chan result, 1)
	connect := func(dst chan<- result) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
		r, e := http.DefaultClient.Do(req)
		dst <- result{r, e}
	}
	go connect(ch1)
	go connect(ch2)

	time.Sleep(50 * time.Millisecond)
	valCh <- userResp{ID: "u1", Name: "Alice"}
	close(valCh)

	res1 := <-ch1
	if res1.err != nil {
		t.Fatalf("client 1: %v", res1.err)
	}
	defer res1.resp.Body.Close()

	res2 := <-ch2
	if res2.err != nil {
		t.Fatalf("client 2: %v", res2.err)
	}
	defer res2.resp.Body.Close()

	if ev1 := readSSEEvents(t, res1.resp, 1); len(ev1) != 1 {
		t.Errorf("client1: want 1 event, got %d", len(ev1))
	}
	if ev2 := readSSEEvents(t, res2.resp, 1); len(ev2) != 1 {
		t.Errorf("client2: want 1 event, got %d", len(ev2))
	}
}
