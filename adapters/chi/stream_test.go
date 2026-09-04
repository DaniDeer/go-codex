package chi

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

	"github.com/DaniDeer/go-codex/api/rest"
	gstream "github.com/DaniDeer/go-codex/stream"
)

type chiPipelineConflictError struct{ msg string }

func (e chiPipelineConflictError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return "pipeline conflict"
}

// ── shared helpers ────────────────────────────────────────────────────────────

func newChiGetHandle() (*rest.RouteHandle[getReq, userResp], error) {
	b := rest.NewServer(testInfo)
	return rest.NewRoute[getReq, userResp]("GET", "/latest",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "chiGetLatest"}).RegisterHandle(b)
}

func newChiPipelineHandle() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewServer(testInfo)
	return rest.NewRoute[createReq, userResp]("POST", "/pipeline",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "chiPipeline"}).RegisterHandle(b)
}

func newChiPipelineHandleWithMappedErrorStatus() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewServer(testInfo)
	return rest.NewRoute[createReq, userResp]("POST", "/pipeline-mapped",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "chiPipelineMapped"},
		rest.ErrorStatus[chiPipelineConflictError](http.StatusConflict),
	).RegisterHandle(b)
}

func newChiPipelineHandleWithNoResponseOverride(status int) (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewServer(testInfo)
	return rest.NewRoute[createReq, userResp]("POST", "/pipeline-noresp-override",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "chiPipelineNoRespOverride"},
		rest.ErrorStatus[PipelineNoResponseError](status),
	).RegisterHandle(b)
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

	h := HandlerLatest(handle, src, Options{})
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
	h := HandlerLatest(handle, src, Options{
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
	var nlv NoLatestValueError
	if !errors.As(capturedErr, &nlv) {
		t.Errorf("want NoLatestValueError, got %T", capturedErr)
	}
}

func readChiSSEEvents(t *testing.T, resp *http.Response, want int) []string {
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

// ── PipelineHandler ───────────────────────────────────────────────────────────

func TestChiPipelineHandler_ReturnsFirstValue(t *testing.T) {
	handle, err := newChiPipelineHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	h := PipelineHandler(handle,
		func(ctx context.Context, req createReq) gstream.Stream[userResp] {
			return gstream.Single(ctx, userResp{ID: "u1", Name: req.Name})
		}, Options{})

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

	h := PipelineHandler(handle,
		func(ctx context.Context, _ createReq) gstream.Stream[userResp] {
			errCh := make(chan error, 1)
			valCh := make(chan userResp)
			errCh <- fmt.Errorf("compute failed")
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		}, Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline",
		strings.NewReader(`{"name":"Eve"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", rec.Code)
	}
}

func TestChiPipelineHandler_PipelineErrorRouteMappingReturnsDeclaredStatus(t *testing.T) {
	handle, err := newChiPipelineHandleWithMappedErrorStatus()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}
	h := PipelineHandler(handle,
		func(context.Context, createReq) gstream.Stream[userResp] {
			errCh := make(chan error, 1)
			valCh := make(chan userResp)
			errCh <- chiPipelineConflictError{msg: "duplicate"}
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		}, Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline-mapped",
		strings.NewReader(`{"name":"Eve"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("want 409, got %d", rec.Code)
	}
}

func TestChiPipelineHandler_TapObservation(t *testing.T) {
	handle, err := newChiPipelineHandle()
	if err != nil {
		t.Fatalf("build route: %v", err)
	}

	var tapFired bool
	var tapped string
	h := PipelineHandler(handle,
		func(ctx context.Context, req createReq) gstream.Stream[userResp] {
			s := gstream.Single(ctx, req)
			s = gstream.Tap(ctx, s, func(v createReq) { tapFired = true; tapped = v.Name })
			return gstream.FlatMapSlice(ctx, s, func(v createReq) []userResp {
				return []userResp{{ID: "u1", Name: v.Name}}
			})
		}, Options{})

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
	h := PipelineHandler(handle,
		func(ctx context.Context, _ createReq) gstream.Stream[userResp] {
			errCh := make(chan error)
			valCh := make(chan userResp)
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		}, Options{
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

	var pnr PipelineNoResponseError
	if !errors.As(capturedErr, &pnr) {
		t.Errorf("want PipelineNoResponseError, got %T", capturedErr)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", rec.Code)
	}
}

func TestChiPipelineHandler_NoValueRouteMappingOverridesDefault503(t *testing.T) {
	handle, err := newChiPipelineHandleWithNoResponseOverride(http.StatusGatewayTimeout)
	if err != nil {
		t.Fatalf("build route: %v", err)
	}
	h := PipelineHandler(handle,
		func(context.Context, createReq) gstream.Stream[userResp] {
			errCh := make(chan error)
			valCh := make(chan userResp)
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		}, Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pipeline-noresp-override",
		strings.NewReader(`{"name":"Grace"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("want 504, got %d", rec.Code)
	}
}

func TestChiSSEFromHub_BroadcastsToAllClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	valCh := make(chan userResp, 3)
	src := gstream.From(ctx, valCh)
	hub := gstream.NewBroadcastHub(ctx, src, 8)

	fn := SSEFromHub[getReq, userResp](hub,
		SSEStreamOptions{Topic: "/events"})

	route := rest.NewSSERoute[getReq, userResp]("/events",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "streamEvents"}).WithHandler(fn)
	h := mustServeSSE(t, route, rest.NewServer(testInfo))
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Connect both clients concurrently — SSEHandler commits headers on first send.
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

	// Give both goroutines time to subscribe before emitting.
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

	ev1 := readChiSSEEvents(t, res1.resp, 1)
	ev2 := readChiSSEEvents(t, res2.resp, 1)

	if len(ev1) != 1 {
		t.Errorf("client1: want 1 event, got %d", len(ev1))
	}
	if len(ev2) != 1 {
		t.Errorf("client2: want 1 event, got %d", len(ev2))
	}
}
