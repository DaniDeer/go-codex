package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/stats"
)

// spyObserver records whether any observer method was called.
type spyObserver struct{ called bool }

func (s *spyObserver) RecordValidationError(_, _, _ string)              { s.called = true }
func (s *spyObserver) RecordRequest(_, _ string, _ int, _ time.Duration) { s.called = true }
func (s *spyObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) { s.called = true }
func (s *spyObserver) RecordPublish(_ string, _ bool, _ time.Duration)   { s.called = true }

// ── T1 — round-trip ───────────────────────────────────────────────────────────

func TestWithObserver_StoresAndRetrieves(t *testing.T) {
	spy := &spyObserver{}
	ctx := stats.WithObserver(context.Background(), spy)

	got := stats.ObserverFromContext(ctx)
	got.RecordRequest("GET", "/", 200, 0)

	if !spy.called {
		t.Error("want ObserverFromContext to return the stored observer; RecordRequest not called")
	}
}

// ── T2 — no observer stored → NoopObserver ───────────────────────────────────

func TestObserverFromContext_NoObserverStored_ReturnsNoop(t *testing.T) {
	obs := stats.ObserverFromContext(context.Background())
	if _, isNoop := obs.(stats.NoopObserver); !isNoop {
		t.Errorf("want NoopObserver, got %T", obs)
	}
	// Must not panic.
	obs.RecordRequest("GET", "/", 200, 0)
}

// ── T3 — wrong type stored → NoopObserver ────────────────────────────────────

func TestObserverFromContext_WrongTypeStored_ReturnsNoop(t *testing.T) {
	// Store a non-Observer value under the same key pattern.
	// context.WithValue with a different key won't interfere; we just verify
	// that an unrelated value doesn't get miscast.
	ctx := context.WithValue(context.Background(), struct{ name string }{"other"}, "unrelated")
	obs := stats.ObserverFromContext(ctx)
	if _, isNoop := obs.(stats.NoopObserver); !isNoop {
		t.Errorf("want NoopObserver when no Observer stored, got %T", obs)
	}
}

// ── T4 — nested context overrides outer observer ──────────────────────────────

func TestWithObserver_NestedContextOverrides(t *testing.T) {
	outer := &spyObserver{}
	inner := &spyObserver{}

	outerCtx := stats.WithObserver(context.Background(), outer)
	innerCtx := stats.WithObserver(outerCtx, inner)

	// Inner context must return the inner observer.
	got := stats.ObserverFromContext(innerCtx)
	got.RecordRequest("GET", "/", 200, 0)
	if !inner.called {
		t.Error("want inner observer called")
	}
	if outer.called {
		t.Error("want outer observer NOT called when inner overrides")
	}

	// Outer context still returns the outer observer.
	stats.ObserverFromContext(outerCtx).RecordRequest("POST", "/", 201, 0)
	if !outer.called {
		t.Error("want outer observer still reachable from outer ctx")
	}
}

// ── T5 — ExampleWithObserver ──────────────────────────────────────────────────

func ExampleWithObserver() {
	obs := stats.NewLoggingObserver(nil) // nil logger is replaced by slog.Default() in real code
	ctx := stats.WithObserver(context.Background(), obs)

	// Retrieve anywhere downstream — adapters do this internally.
	retrieved := stats.ObserverFromContext(ctx)
	_ = retrieved
	// Output:
}
