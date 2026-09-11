package reqreply_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/route"
)

// ── shared routes/security for Server/Client tests ──────────────────────────

var (
	ComputeRoute  = reqreply.NewRoute[computeReq, computeResp]("compute/add", reqCodec, respCodec)
	ComputeRoute2 = reqreply.NewRoute[computeReq, computeResp]("compute/add2", reqCodec, respCodec)
	ComputeRoute3 = reqreply.NewRoute[computeReq, computeResp]("compute/add3", reqCodec, respCodec)
	ComputeRoute4 = reqreply.NewRoute[computeReq, computeResp]("compute/add4", reqCodec, respCodec)
	ComputeRoute5 = reqreply.NewRoute[computeReq, computeResp]("compute/add5", reqCodec, respCodec)

	bearerReq = route.Require("bearer")
)

// ── fake ServerTransport/ClientTransport test doubles ─────────────────────

// fakeServerTransport lets tests control whether Serve blocks (mirroring
// adapters/zeromq.Serve) or returns immediately (mirroring
// adapters/mqtt5.Serve), and records concurrency via an active-route
// high-water mark — the same technique the roadmap doc's own throwaway
// prototype used to confirm Server.Serve's concurrent dispatch.
type fakeServerTransport struct {
	blocking bool
	// active/highWater track concurrently-running Serve calls.
	active    int32
	highWater int32
	mu        sync.Mutex
	// serveErr, if set, is returned by the Nth Serve call (by
	// registration order); errRouteIdx selects which call.
	serveErr    error
	errRouteIdx int
	callIdx     int32
}

func (t *fakeServerTransport) Serve(ctx context.Context, route any, fn any) error {
	idx := atomic.AddInt32(&t.callIdx, 1) - 1
	n := atomic.AddInt32(&t.active, 1)
	t.mu.Lock()
	if n > t.highWater {
		t.highWater = n
	}
	t.mu.Unlock()
	defer atomic.AddInt32(&t.active, -1)

	if t.serveErr != nil && int(idx) == t.errRouteIdx {
		return t.serveErr
	}
	if t.blocking {
		<-ctx.Done()
		return nil
	}
	return nil // non-blocking transport (mqtt5-style): returns immediately
}

// fakeClientTransport records Call/CallAsync invocations and lets tests
// assert on the dynamic type of the route argument (raw Route vs.
// *RouteHandle) — the confirmed dual-mode dispatch mechanism.
type fakeClientTransport struct {
	lastRouteType string
	credInvoked   bool
}

func (t *fakeClientTransport) Call(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	switch v := routeAny.(type) {
	case reqreply.Route[computeReq, computeResp]:
		t.lastRouteType = "Route"
		t.credInvoked = false
		req := reqAny.(computeReq)
		return computeResp{Sum: req.X + req.Y}, nil
	case *reqreply.RouteHandle[computeReq, computeResp]:
		t.lastRouteType = "RouteHandle"
		// GlobalSecurity would be enforced here in a real adapter; record
		// whether it's visible to confirm the dual-mode finding.
		t.credInvoked = len(v.Security) > 0 || len(v.GlobalSecurity) > 0
		req := reqAny.(computeReq)
		return computeResp{Sum: req.X + req.Y}, nil
	default:
		return nil, reqreply.TransportTypeMismatchError{Want: "Route or *RouteHandle", Got: fmt.Sprintf("%T", routeAny)}
	}
}

func (t *fakeClientTransport) CallAsync(ctx context.Context, routeAny any, reqAny any, opts ...reqreply.ClientCallOptions) (any, error) {
	ff, ok := routeAny.(reqreply.FutureFactory)
	if !ok {
		return nil, reqreply.TransportTypeMismatchError{Want: "FutureFactory", Got: fmt.Sprintf("%T", routeAny)}
	}
	future, resolve := ff.NewFutureAny()
	req := reqAny.(computeReq)
	go func() {
		time.Sleep(5 * time.Millisecond) // simulate async network round trip
		resolve(computeResp{Sum: req.X + req.Y}, nil)
	}()
	return future, nil
}

// ── Server/Client + Attach error taxonomy ──────────────────────────────────

func TestServer_Serve_NoTransportAttached(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	err := s.Serve(context.Background())
	var wantErr reqreply.NoServerTransportAttachedError
	if !errors.As(err, &wantErr) {
		t.Fatalf("Serve() error = %v, want NoServerTransportAttachedError", err)
	}
}

func TestClient_Call_NoTransportAttached(t *testing.T) {
	c := reqreply.NewClient()
	_, err := c.Call(context.Background(), ComputeRoute, computeReq{X: 1, Y: 2})
	var wantErr reqreply.NoClientTransportAttachedError
	if !errors.As(err, &wantErr) {
		t.Fatalf("Call() error = %v, want NoClientTransportAttachedError", err)
	}
}

func TestServer_Attach_AlreadyAttached(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	if err := s.Attach(&fakeServerTransport{}); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	err := s.Attach(&fakeServerTransport{})
	var wantErr reqreply.ServerTransportAlreadyAttachedError
	if !errors.As(err, &wantErr) {
		t.Fatalf("second Attach() error = %v, want ServerTransportAlreadyAttachedError", err)
	}
}

func TestClient_Attach_AlreadyAttached(t *testing.T) {
	c := reqreply.NewClient()
	if err := c.Attach(&fakeClientTransport{}); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	err := c.Attach(&fakeClientTransport{})
	var wantErr reqreply.ClientTransportAlreadyAttachedError
	if !errors.As(err, &wantErr) {
		t.Fatalf("second Attach() error = %v, want ClientTransportAlreadyAttachedError", err)
	}
}

// ── Server/Builder unification ──────────────────────────────────────────────

func TestServer_AddGlobalSecurity_VisibleOnRegisteredHandle(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	s.AddGlobalSecurity(bearerReq)
	handle, err := ComputeRoute.Register(s)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(handle.GlobalSecurity) == 0 {
		t.Fatalf("handle.GlobalSecurity = %v, want non-empty (Server.AddGlobalSecurity should be visible with NO intermediate Builder)", handle.GlobalSecurity)
	}
}

// ── Route.WithHandler + Register fluent chain ───────────────────────────────

func TestRoute_WithHandler_Register_DispatchesOnServe(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	var called int32
	handlerFn := func(ctx context.Context, req computeReq) (computeResp, error) {
		atomic.AddInt32(&called, 1)
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := ComputeRoute.WithHandler(handlerFn).Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	ft := &fakeServerTransport{blocking: false}
	if err := s.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = s.Serve(ctx) // blocks until ctx cancelled (non-blocking transport)
	if atomic.LoadInt32(&ft.callIdx) != 1 {
		t.Fatalf("ServerTransport.Serve called %d times, want 1 (one call per registered route, no separate registration step)", ft.callIdx)
	}
}

// ── dual-mode Client.Call ───────────────────────────────────────────────────

func TestClient_Call_DualMode_RawRoute_GlobalSecurityInvisible(t *testing.T) {
	c := reqreply.NewClient()
	ct := &fakeClientTransport{}
	_ = c.Attach(ct)
	_, err := c.Call(context.Background(), ComputeRoute, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if ct.lastRouteType != "Route" {
		t.Fatalf("lastRouteType = %q, want Route", ct.lastRouteType)
	}
	if ct.credInvoked {
		t.Fatalf("credInvoked = true for raw Route, want false (GlobalSecurity must be invisible)")
	}
}

func TestClient_Call_DualMode_RegisteredHandle_GlobalSecurityEnforced(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	s.AddGlobalSecurity(bearerReq)
	handle, err := ComputeRoute.Register(s)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	c := reqreply.NewClient()
	ct := &fakeClientTransport{}
	_ = c.Attach(ct)
	_, err = c.Call(context.Background(), handle, computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if ct.lastRouteType != "RouteHandle" {
		t.Fatalf("lastRouteType = %q, want RouteHandle", ct.lastRouteType)
	}
	if !ct.credInvoked {
		t.Fatalf("credInvoked = false for registered *RouteHandle with GlobalSecurity set, want true")
	}
}

func TestClient_Call_TransportTypeMismatch(t *testing.T) {
	c := reqreply.NewClient()
	_ = c.Attach(&fakeClientTransport{})
	_, err := c.Call(context.Background(), "not-a-route", computeReq{X: 1, Y: 2})
	var wantErr reqreply.TransportTypeMismatchError
	if !errors.As(err, &wantErr) {
		t.Fatalf("Call() error = %v, want TransportTypeMismatchError", err)
	}
}

// ── Server.Serve concurrent dispatch ────────────────────────────────────────

func TestServer_Serve_NonBlockingTransport_BlocksUntilCtxCancelled(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	for i, rt := range []reqreply.Route[computeReq, computeResp]{ComputeRoute, ComputeRoute2, ComputeRoute3} {
		_ = i
		if _, err := rt.WithHandler(func(ctx context.Context, r computeReq) (computeResp, error) {
			return computeResp{Sum: r.X + r.Y}, nil
		}).Register(s); err != nil {
			t.Fatalf("Register route %d: %v", i, err)
		}
	}
	ft := &fakeServerTransport{blocking: false}
	if err := s.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := s.Serve(ctx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if elapsed < 25*time.Millisecond {
		t.Fatalf("Serve returned early (elapsed=%v) — must block until ctx cancelled even when every route's Serve call returns instantly (non-blocking transport)", elapsed)
	}
}

func TestServer_Serve_BlockingTransport_RunsAllRoutesConcurrently(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	routes := []reqreply.Route[computeReq, computeResp]{ComputeRoute, ComputeRoute2, ComputeRoute3, ComputeRoute4, ComputeRoute5}
	for i, rt := range routes {
		if _, err := rt.WithHandler(func(ctx context.Context, r computeReq) (computeResp, error) {
			return computeResp{Sum: r.X + r.Y}, nil
		}).Register(s); err != nil {
			t.Fatalf("Register route %d: %v", i, err)
		}
	}
	ft := &fakeServerTransport{blocking: true} // zeromq-style: Serve blocks until ctx cancelled
	if err := s.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := s.Serve(ctx); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if int(ft.highWater) != len(routes) {
		t.Fatalf("high-water mark = %d, want %d (a sequential-loop regression would show 1, proving starvation)", ft.highWater, len(routes))
	}
}

func TestServer_Serve_RouteError_PropagatesPromptlyAndCancelsOthers(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	routes := []reqreply.Route[computeReq, computeResp]{ComputeRoute, ComputeRoute2, ComputeRoute3}
	for i, rt := range routes {
		if _, err := rt.WithHandler(func(ctx context.Context, r computeReq) (computeResp, error) {
			return computeResp{Sum: r.X + r.Y}, nil
		}).Register(s); err != nil {
			t.Fatalf("Register route %d: %v", i, err)
		}
	}
	wantErr := errors.New("boom")
	ft := &fakeServerTransport{blocking: true, serveErr: wantErr, errRouteIdx: 1}
	if err := s.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err := s.Serve(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Serve() error = %v, want %v", err, wantErr)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Serve took %v to return the error — must propagate PROMPTLY, not wait for ctx to be cancelled", elapsed)
	}
}

// ── Server.RegisteredTopics / Topical ────────────────────────────────────────

func TestServer_RegisteredTopics(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1.0.0"})
	if _, err := ComputeRoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := ComputeRoute2.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	topics := s.RegisteredTopics()
	if len(topics) != 2 {
		t.Fatalf("RegisteredTopics() = %v, want 2 entries", topics)
	}
}

// ── CallAsync / Future ───────────────────────────────────────────────────────

func TestClient_CallAsync_ReturnsImmediatelyAndResolvesLater(t *testing.T) {
	c := reqreply.NewClient()
	_ = c.Attach(&fakeClientTransport{})
	handle := ComputeRoute.ClientHandle()

	start := time.Now()
	futureAny, err := c.CallAsync(context.Background(), handle, computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	if time.Since(start) > 2*time.Millisecond {
		t.Fatalf("CallAsync blocked for %v, want near-instant return", time.Since(start))
	}

	// Do OTHER independent work — a second, independent CallAsync — before
	// awaiting the first, confirming "send here, resolve elsewhere".
	future2Any, err := c.CallAsync(context.Background(), handle, computeReq{X: 10, Y: 20})
	if err != nil {
		t.Fatalf("second CallAsync: %v", err)
	}

	future1 := futureAny.(*reqreply.Future[computeResp])
	future2 := future2Any.(*reqreply.Future[computeResp])

	// Await from a call site distinct from the one that issued CallAsync —
	// via a separate goroutine + channel, mirroring the confirmed prototype.
	type result struct {
		resp computeResp
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := future1.Wait(context.Background())
		resultCh <- result{resp, err}
	}()

	r := <-resultCh
	if r.err != nil {
		t.Fatalf("future1.Wait: %v", r.err)
	}
	if r.resp.Sum != 7 {
		t.Fatalf("future1 resp = %+v, want Sum=7", r.resp)
	}

	resp2, err := future2.Wait(context.Background())
	if err != nil {
		t.Fatalf("future2.Wait: %v", err)
	}
	if resp2.Sum != 30 {
		t.Fatalf("future2 resp = %+v, want Sum=30", resp2)
	}
}

func TestFuture_Wait_TimeoutOnCancelledContext(t *testing.T) {
	c := reqreply.NewClient()
	_ = c.Attach(&fakeClientTransport{})
	handle := ComputeRoute.ClientHandle()

	futureAny, err := c.CallAsync(context.Background(), handle, computeReq{X: 1, Y: 1})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	future := futureAny.(*reqreply.Future[computeResp])

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled — Wait must return a timeout error immediately
	_, err = future.Wait(ctx)
	var wantErr reqreply.FutureTimeoutError
	if !errors.As(err, &wantErr) {
		t.Fatalf("Wait() error = %v, want FutureTimeoutError", err)
	}
}

func TestFutureFactory_NewFutureAny_TypeErasureCrossing(t *testing.T) {
	handle := ComputeRoute.ClientHandle()
	var ff reqreply.FutureFactory = handle
	futureAny, resolve := ff.NewFutureAny()
	future, ok := futureAny.(*reqreply.Future[computeResp])
	if !ok {
		t.Fatalf("NewFutureAny() future type = %T, want *reqreply.Future[computeResp]", futureAny)
	}
	resolve(computeResp{Sum: 42}, nil)
	resp, err := future.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if resp.Sum != 42 {
		t.Fatalf("resp.Sum = %d, want 42", resp.Sum)
	}
}
