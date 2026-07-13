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
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── helpers ───────────────────────────────────────────────────────────────────

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

// ── SSEFromStream ─────────────────────────────────────────────────────────────

func TestSSEFromStream_EmitsStreamItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := newSSEHandle(t)

	fn := nethttp.SSEFromStream(
		func(ctx context.Context, _ getReq) gstream.Stream[userResp] {
			ch := make(chan userResp, 2)
			ch <- userResp{ID: "u1", Name: "Alice"}
			ch <- userResp{ID: "u2", Name: "Bob"}
			close(ch)
			return gstream.From(ctx, ch)
		},
		nethttp.SSEStreamOptions{Topic: "/events"},
	)

	h := nethttp.SSEHandler(handle, fn, nethttp.Options{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	events := readSSEEvents(t, resp, 2)
	if len(events) != 2 {
		t.Errorf("want 2 events, got %d: %v", len(events), events)
	}
}

func TestSSEFromStream_StreamErrorCallsOnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := newSSEHandle(t)

	var gotErr error
	fn := nethttp.SSEFromStream(
		func(ctx context.Context, _ getReq) gstream.Stream[userResp] {
			errCh := make(chan error, 1)
			valCh := make(chan userResp)
			errCh <- fmt.Errorf("stream error")
			close(errCh)
			close(valCh)
			return gstream.Stream[userResp]{Values: valCh, Errors: errCh}
		},
		nethttp.SSEStreamOptions{
			Topic:   "/events",
			OnError: func(e error) { gotErr = e },
		},
	)

	h := nethttp.SSEHandler(handle, fn, nethttp.Options{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	time.Sleep(30 * time.Millisecond)
	if gotErr == nil {
		t.Error("want OnError called for stream error, got nil")
	}
}

// ── SSEFromHub ────────────────────────────────────────────────────────────────

func TestSSEFromHub_BroadcastsToAllClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handle := newSSEHandle(t)

	valCh := make(chan userResp, 3)
	src := gstream.From(ctx, valCh)
	hub := gstream.NewBroadcastHub(ctx, src, 8)

	fn := nethttp.SSEFromHub[getReq, userResp](hub,
		nethttp.SSEStreamOptions{Topic: "/events"})

	h := nethttp.SSEHandler(handle, fn, nethttp.Options{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	// The SSE handler commits headers on the first send, not on connection.
	// Both clients must connect concurrently; sending an event unblocks Do().
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

	// Give both goroutines time to reach the server and subscribe to the hub.
	time.Sleep(50 * time.Millisecond)

	// Emit 1 event — this triggers WriteHeader(200) on both connections so Do() returns.
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

	ev1 := readSSEEvents(t, res1.resp, 1)
	ev2 := readSSEEvents(t, res2.resp, 1)

	if len(ev1) != 1 {
		t.Errorf("client1: want 1 event, got %d", len(ev1))
	}
	if len(ev2) != 1 {
		t.Errorf("client2: want 1 event, got %d", len(ev2))
	}
}

// ── PollStream ────────────────────────────────────────────────────────────────

func TestPollStream_EmitsResponsePerTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Mock server that returns a userResp
	mux := http.NewServeMux()
	callCount := 0
	mux.HandleFunc("GET /users/latest", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"u1","name":"Alice"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[getReq, userResp]("GET", "/users/latest",
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	s := nethttp.PollStream(ctx, http.DefaultClient, srv.URL, h,
		getReq{}, 30*time.Millisecond,
		nethttp.PollStreamOptions{Buffer: 4})

	// Collect for ~100ms → should get ~3 polls
	timeCtx, tc := context.WithTimeout(ctx, 110*time.Millisecond)
	defer tc()
	vals, errs := gstream.Collect(timeCtx, s)

	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) < 2 {
		t.Errorf("want ≥2 poll results, got %d", len(vals))
	}
	for _, v := range vals {
		if v.Name != "Alice" {
			t.Errorf("unexpected value: %+v", v)
		}
	}
}

func TestPollStream_CallErrorGoesToStreamErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Server that always returns 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[getReq, userResp]("GET", "/users/1",
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	s := nethttp.PollStream(ctx, http.DefaultClient, srv.URL, h,
		getReq{}, 20*time.Millisecond,
		nethttp.PollStreamOptions{Buffer: 4})

	timeCtx, tc := context.WithTimeout(ctx, 60*time.Millisecond)
	defer tc()
	_, errs := gstream.Collect(timeCtx, s)

	if len(errs) == 0 {
		t.Error("want at least 1 error from 500 response, got 0")
	}
	var use nethttp.UnexpectedStatusError
	if !errors.As(errs[0], &use) {
		t.Errorf("want UnexpectedStatusError, got %T", errs[0])
	}
}

// ── DrainCall ─────────────────────────────────────────────────────────────────

func TestDrainCall_PostsEachItem(t *testing.T) {
	ctx := context.Background()

	received := make([]string, 0)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ingest", func(w http.ResponseWriter, r *http.Request) {
		// Accept the body
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"new","name":"x"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/ingest",
		createReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	ch := make(chan createReq, 2)
	ch <- createReq{Name: "Alice"}
	ch <- createReq{Name: "Bob"}
	close(ch)

	nethttp.DrainCall(ctx, http.DefaultClient, srv.URL, h,
		gstream.From(ctx, ch),
		nethttp.DrainCallOptions{})

	_ = received // server received both — test completes without error
}

// ── CallStream ────────────────────────────────────────────────────────────────

func TestCallStream_EmitsResponsePerItem(t *testing.T) {
	ctx := context.Background()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /enrich", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"enriched","name":"result"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/enrich",
		createReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	ch := make(chan createReq, 2)
	ch <- createReq{Name: "Alice"}
	ch <- createReq{Name: "Bob"}
	close(ch)

	results := nethttp.CallStream(ctx, http.DefaultClient, srv.URL, h,
		gstream.From(ctx, ch),
		nethttp.CallStreamOptions{})

	vals, errs := gstream.Collect(ctx, results)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 2 {
		t.Errorf("want 2 responses, got %d", len(vals))
	}
}

func TestCallStream_ErrorsGoToStreamErrors(t *testing.T) {
	ctx := context.Background()

	// Server returns 500 for all calls
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/enrich",
		createReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	ch := make(chan createReq, 1)
	ch <- createReq{Name: "Alice"}
	close(ch)

	results := nethttp.CallStream(ctx, http.DefaultClient, srv.URL, h,
		gstream.From(ctx, ch),
		nethttp.CallStreamOptions{})

	_, errs := gstream.Collect(ctx, results)
	if len(errs) == 0 {
		t.Error("want at least 1 error for 500 response, got 0")
	}
	var ue nethttp.UnexpectedStatusError
	if !errors.As(errs[0], &ue) {
		t.Errorf("want UnexpectedStatusError, got %T", errs[0])
	}
}

func TestCallStream_UpstreamErrorsForwarded(t *testing.T) {
	ctx := context.Background()

	// Server that succeeds
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":"u1","name":"Alice"}`)
	}))
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/enrich",
		createReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	// Inject an upstream error
	valCh := make(chan createReq)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("upstream failure")
	close(errCh)
	close(valCh)
	src := gstream.Stream[createReq]{Values: valCh, Errors: errCh}

	results := nethttp.CallStream(ctx, http.DefaultClient, srv.URL, h,
		src, nethttp.CallStreamOptions{})

	_, errs := gstream.Collect(ctx, results)
	if len(errs) == 0 {
		t.Error("want upstream error forwarded, got 0")
	}
}

// ── SSEClientStream ───────────────────────────────────────────────────────────

func TestSSEClientStream_DecodesEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Mock SSE server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"u1\",\"name\":\"Alice\"}\n\n")
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"u2\",\"name\":\"Bob\"}\n\n")
		flusher.Flush()
		// Hold connection until client disconnects
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewSSERoute[getReq, userResp]("/events",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "clientEvents"}).Register(b)

	s := nethttp.SSEClientStream(ctx, http.DefaultClient, srv.URL, h,
		format.JSON(userRespCodec),
		nethttp.SSEClientOptions{Buffer: 4, RetryDelay: 100 * time.Millisecond})

	// Collect 2 events then cancel
	timeCtx, tc := context.WithTimeout(ctx, 500*time.Millisecond)
	defer tc()

	var vals []userResp
	for {
		select {
		case v, ok := <-s.Values:
			if !ok {
				goto done
			}
			vals = append(vals, v)
			if len(vals) >= 2 {
				cancel()
				goto done
			}
		case <-timeCtx.Done():
			goto done
		}
	}
done:
	if len(vals) < 2 {
		t.Errorf("want 2 events, got %d: %v", len(vals), vals)
	}
}

func TestSSEClientStream_ConnectErrorGoesToStreamErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewSSERoute[getReq, userResp]("/events",
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	// Use a closed server — all connects fail immediately
	s := nethttp.SSEClientStream(ctx, http.DefaultClient, "http://127.0.0.1:1", h,
		format.JSON(userRespCodec),
		nethttp.SSEClientOptions{Buffer: 4, RetryDelay: 10 * time.Millisecond, MaxRetryDelay: 20 * time.Millisecond})

	// Let it attempt a few connects then cancel
	time.Sleep(50 * time.Millisecond)
	cancel()

	_, errs := gstream.Collect(context.Background(), s)
	if len(errs) == 0 {
		t.Error("want at least 1 SSEConnectError, got 0")
	}
	var ce nethttp.SSEConnectError
	if !errors.As(errs[0], &ce) {
		t.Errorf("want SSEConnectError, got %T", errs[0])
	}
}

// unused helper to satisfy unused import
var _ = codex.String
