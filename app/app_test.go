package app_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/app"
	"github.com/DaniDeer/go-codex/stats"
)

// recordingObserver captures RecordRequest calls.
type recordingObserver struct {
	stats.NoopObserver
	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	method string
	path   string
	status int
}

func (o *recordingObserver) RecordRequest(method, path string, status int, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, recordedCall{method, path, status})
}

func (o *recordingObserver) snapshot() []recordedCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]recordedCall{}, o.calls...)
}

// D1
func TestApp_ShutdownRunsHooksLIFO(t *testing.T) {
	a := app.New(app.Options{})
	var order []string
	for _, name := range []string{"first", "second", "third"} {
		n := name
		a.OnShutdown(n, func(context.Context) error {
			order = append(order, n)
			return nil
		})
	}
	if err := a.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	want := []string{"third", "second", "first"}
	for i, w := range want {
		if order[i] != w {
			t.Fatalf("want LIFO order %v, got %v", want, order)
		}
	}
}

// D2
func TestApp_HookErrorDoesNotStopLaterHooks(t *testing.T) {
	a := app.New(app.Options{})
	var ran []string
	a.OnShutdown("survivor", func(context.Context) error {
		ran = append(ran, "survivor")
		return nil
	})
	boom := errors.New("boom")
	a.OnShutdown("failing", func(context.Context) error {
		ran = append(ran, "failing")
		return boom
	})
	err := a.Shutdown()
	if len(ran) != 2 || ran[0] != "failing" || ran[1] != "survivor" {
		t.Fatalf("want failing then survivor, got %v", ran)
	}
	var he app.HookError
	if !errors.As(err, &he) {
		t.Fatalf("want HookError, got %v", err)
	}
	if he.Name != "failing" || !errors.Is(he, boom) {
		t.Errorf("want name=failing wrapping boom, got %+v", he)
	}
}

// D3
func TestApp_GoFailureCancelsApp(t *testing.T) {
	a := app.New(app.Options{})
	boom := errors.New("goroutine boom")
	a.Go("failing", func(context.Context) error { return boom })

	select {
	case <-a.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("want app ctx cancelled after goroutine failure (fail-fast)")
	}
	err := a.Shutdown()
	var ge app.GoroutineError
	if !errors.As(err, &ge) {
		t.Fatalf("want GoroutineError, got %v", err)
	}
	if ge.Name != "failing" || !errors.Is(ge, boom) {
		t.Errorf("want name=failing wrapping boom, got %+v", ge)
	}
}

// D4
func TestApp_RunParentCancelTriggersShutdown(t *testing.T) {
	a := app.New(app.Options{})
	var hookRan atomic.Bool
	a.OnShutdown("hook", func(context.Context) error {
		hookRan.Store(true)
		return nil
	})
	a.Go("worker", func(ctx context.Context) error {
		<-ctx.Done() // clean exit on cancel
		return nil
	})

	parent, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(parent) }()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("want clean shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after parent cancel")
	}
	if !hookRan.Load() {
		t.Error("want shutdown hook to have run")
	}
}

// D5
func TestApp_ShutdownIdempotent(t *testing.T) {
	a := app.New(app.Options{})
	var count atomic.Int32
	a.OnShutdown("once", func(context.Context) error {
		count.Add(1)
		return errors.New("always fails")
	})

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = a.Shutdown()
		}(i)
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Errorf("want hook run exactly once, got %d", count.Load())
	}
	for i := 1; i < 3; i++ {
		if !errors.Is(errs[i], errs[0]) && errs[i].Error() != errs[0].Error() {
			t.Errorf("want memoized result shared, got %v vs %v", errs[0], errs[i])
		}
	}
}

// D6
func TestApp_ShutdownTimeoutBoundsHooks(t *testing.T) {
	a := app.New(app.Options{ShutdownTimeout: 30 * time.Millisecond})
	var laterRan atomic.Bool
	a.OnShutdown("later", func(context.Context) error {
		laterRan.Store(true)
		return nil
	})
	a.OnShutdown("slow", func(ctx context.Context) error {
		<-ctx.Done() // respects the bounded ctx
		return ctx.Err()
	})
	err := a.Shutdown()
	var he app.HookError
	if !errors.As(err, &he) || he.Name != "slow" {
		t.Fatalf("want HookError{slow}, got %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want DeadlineExceeded in chain, got %v", err)
	}
	if !laterRan.Load() {
		t.Error("want later (LIFO-earlier-registered) hook to still run")
	}
}

// D7
func TestApp_ContextCarriesObserver(t *testing.T) {
	obs := &recordingObserver{}
	a := app.New(app.Options{Observer: obs})
	got := stats.ObserverFromContext(a.Context())
	if got != stats.Observer(obs) {
		t.Errorf("want injected observer from app ctx, got %T", got)
	}
	// Nil observer → ctx yields Noop, no panic anywhere.
	a2 := app.New(app.Options{})
	if _, ok := stats.ObserverFromContext(a2.Context()).(stats.NoopObserver); !ok {
		t.Errorf("want NoopObserver fallback, got %T", stats.ObserverFromContext(a2.Context()))
	}
	_ = a.Shutdown()
	_ = a2.Shutdown()
}

// D8
func TestApp_ObserverEvents(t *testing.T) {
	obs := &recordingObserver{}
	a := app.New(app.Options{Observer: obs})
	a.Go("ok-worker", func(context.Context) error { return nil })
	a.Go("bad-worker", func(context.Context) error { return errors.New("x") })
	a.OnShutdown("ok-hook", func(context.Context) error { return nil })
	a.OnShutdown("bad-hook", func(context.Context) error { return errors.New("y") })
	_ = a.Shutdown()

	want := map[recordedCall]bool{
		{"app.go", "ok-worker", 200}:      false,
		{"app.go", "bad-worker", 500}:     false,
		{"app.shutdown", "ok-hook", 200}:  false,
		{"app.shutdown", "bad-hook", 500}: false,
	}
	for _, c := range obs.snapshot() {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for c, seen := range want {
		if !seen {
			t.Errorf("want observer event %+v", c)
		}
	}
}

// D9
func TestGoroutineError_LogValue(t *testing.T) {
	e := app.GoroutineError{Name: "g", Err: errors.New("x")}
	assertNameErrGroup(t, e.LogValue())
	if e.Unwrap() == nil {
		t.Error("want Unwrap non-nil")
	}
}

func TestHookError_LogValue(t *testing.T) {
	e := app.HookError{Name: "h", Err: errors.New("x")}
	assertNameErrGroup(t, e.LogValue())
	if e.Unwrap() == nil {
		t.Error("want Unwrap non-nil")
	}
}

func assertNameErrGroup(t *testing.T, v slog.Value) {
	t.Helper()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	if !keys["name"] || !keys["err"] {
		t.Errorf("want name+err keys, got %v", keys)
	}
}

// D10
func TestApp_GoAfterShutdown_NoPanic(t *testing.T) {
	a := app.New(app.Options{})
	if err := a.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	var ran atomic.Bool
	a.Go("late", func(context.Context) error { ran.Store(true); return nil })
	a.OnShutdown("late-hook", func(context.Context) error { ran.Store(true); return nil })
	time.Sleep(20 * time.Millisecond)
	if ran.Load() {
		t.Error("want late Go/OnShutdown to be no-ops")
	}
}

// Example demonstrates the full lifecycle: supervised work, LIFO teardown,
// and a direct (non-signal) shutdown.
func Example() {
	a := app.New(app.Options{Logger: slog.New(slog.DiscardHandler)})

	a.OnShutdown("close-storage", func(context.Context) error {
		fmt.Println("storage closed")
		return nil
	})
	a.OnShutdown("close-server", func(context.Context) error {
		fmt.Println("server closed") // registered last → runs first
		return nil
	})

	a.Go("worker", func(ctx context.Context) error {
		<-ctx.Done() // do work until shutdown
		return nil
	})

	if err := a.Shutdown(); err != nil {
		fmt.Println("shutdown errors:", err)
	}
	// Output:
	// server closed
	// storage closed
}
