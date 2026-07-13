package stream_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/stats"
	stream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared forge function ─────────────────────────────────────────────────────

var doubleCodec = codex.Float64().Refine(validate.PositiveFloat).WithTitle("value")
var resultCodec = codex.Float64().WithTitle("doubled")

var doubleFn = forge.NewFunction("double", "1.0.0",
	doubleCodec, resultCodec,
	func(v float64) (float64, error) { return v * 2, nil },
)

var errorFn = forge.NewFunction("errFn", "1.0.0",
	codex.Float64().WithTitle("input"), codex.Float64().WithTitle("output"),
	func(v float64) (float64, error) {
		if v < 0 {
			return 0, fmt.Errorf("negative not allowed")
		}
		return v, nil
	},
)

// ── Apply ─────────────────────────────────────────────────────────────────────

func TestApply_HappyPath(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1.0, 2.0, 3.0)
	s := stream.Apply(ctx, stream.From(ctx, src), doubleFn, stream.ApplyOptions{})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 3 {
		t.Fatalf("want 3 values, got %d", len(vals))
	}
	if vals[0] != 2 || vals[1] != 4 || vals[2] != 6 {
		t.Errorf("unexpected values: %v", vals)
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

func TestApply_InputValidationFailure(t *testing.T) {
	ctx := context.Background()
	// doubleCodec requires positive float — negative fails codec validation
	src := chanOf(-1.0, 2.0)
	s := stream.Apply(ctx, stream.From(ctx, src), doubleFn, stream.ApplyOptions{})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 1 {
		t.Errorf("want 1 value (the positive one), got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 error (negative input), got %d", len(errs))
	}
	var sae stream.StreamApplyError
	if !errors.As(errs[0], &sae) {
		t.Errorf("want StreamApplyError, got %T", errs[0])
	}
	if sae.Function != "double" {
		t.Errorf("Function: want %q, got %q", "double", sae.Function)
	}
}

func TestApply_ComputeError(t *testing.T) {
	ctx := context.Background()
	src := chanOf(-1.0, 5.0)
	s := stream.Apply(ctx, stream.From(ctx, src), errorFn, stream.ApplyOptions{})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error, got %d", len(errs))
	}
	var sae stream.StreamApplyError
	if !errors.As(errs[0], &sae) {
		t.Errorf("want StreamApplyError, got %T", errs[0])
	}
}

func TestApply_ErrorsForwardedUnchanged(t *testing.T) {
	ctx := context.Background()
	// Build a stream that already has errors (from FromCodec bad payload)
	valCh := make(chan float64)
	errCh := make(chan error, 1)
	injected := stream.StreamDecodeError{Source: "test", Err: fmt.Errorf("injected")}
	errCh <- injected
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}

	s := stream.Apply(ctx, src, doubleFn, stream.ApplyOptions{})
	_, errs := stream.Collect(ctx, s)
	if len(errs) != 1 {
		t.Fatalf("want 1 forwarded error, got %d", len(errs))
	}
	// The original StreamDecodeError must pass through unchanged (not re-wrapped)
	var sde stream.StreamDecodeError
	if !errors.As(errs[0], &sde) {
		t.Errorf("want StreamDecodeError passed through, got %T", errs[0])
	}
}

func TestApply_StreamObserverCalled(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1.0, 2.0)
	spy := &streamItemSpy{}
	stream.Collect(ctx, stream.Apply(ctx, stream.From(ctx, src), doubleFn,
		stream.ApplyOptions{Observer: spy}))
	if spy.calls != 2 {
		t.Errorf("RecordStreamItem: want 2 calls, got %d", spy.calls)
	}
	if !spy.allSuccess {
		t.Error("RecordStreamItem: all items should be success")
	}
}

func TestApply_StreamObserverCalledOnFailure(t *testing.T) {
	ctx := context.Background()
	src := chanOf(-1.0) // fails doubleCodec (positive required)
	spy := &streamItemSpy{}
	stream.Collect(ctx, stream.Apply(ctx, stream.From(ctx, src), doubleFn,
		stream.ApplyOptions{Observer: spy}))
	if spy.calls != 1 {
		t.Errorf("RecordStreamItem: want 1 call, got %d", spy.calls)
	}
	if spy.allSuccess {
		t.Error("RecordStreamItem: failure item should not be success")
	}
}

func TestApply_NilObserver(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1.0)
	// Must not panic with nil observer
	stream.Collect(ctx, stream.Apply(ctx, stream.From(ctx, src), doubleFn,
		stream.ApplyOptions{Observer: nil}))
}

func TestApply_PlainObserver_NoSQLObserverPanic(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1.0)
	plain := &recordingObserver{} // does not implement StreamObserver
	stream.Collect(ctx, stream.Apply(ctx, stream.From(ctx, src), doubleFn,
		stream.ApplyOptions{Observer: plain}))
	// Must not panic
}

// ── Filter ────────────────────────────────────────────────────────────────────

func TestFilter_KeepsMatchingItems(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1.0, 2.0, 3.0, 4.0)
	s := stream.Filter(ctx, stream.From(ctx, src), func(v float64) bool { return v > 2 })
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 2 {
		t.Errorf("want 2 values (>2), got %d: %v", len(vals), vals)
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

func TestFilter_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}
	s := stream.Filter(ctx, src, func(v float64) bool { return true })
	_, errs := stream.Collect(ctx, s)
	if len(errs) != 1 {
		t.Errorf("want 1 forwarded error, got %d", len(errs))
	}
}

// ── Tap ───────────────────────────────────────────────────────────────────────

func TestTap_CalledForEachValue(t *testing.T) {
	ctx := context.Background()
	src := chanOf(10.0, 20.0, 30.0)
	var tapped []float64
	s := stream.Tap(ctx, stream.From(ctx, src), func(v float64) {
		tapped = append(tapped, v)
	})
	vals, _ := stream.Collect(ctx, s)
	if len(tapped) != 3 {
		t.Errorf("Tap: want 3 calls, got %d", len(tapped))
	}
	// Values must pass through unchanged
	if len(vals) != 3 || vals[0] != 10 {
		t.Errorf("Tap: values not forwarded correctly: %v", vals)
	}
}

func TestTap_DoesNotAffectErrors(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("tap test error")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}
	tapCalled := 0
	s := stream.Tap(ctx, src, func(float64) { tapCalled++ })
	_, errs := stream.Collect(ctx, s)
	if tapCalled != 0 {
		t.Error("Tap should not be called for error items")
	}
	if len(errs) != 1 {
		t.Errorf("error should be forwarded unchanged, got %d", len(errs))
	}
}

// ── MapErr ────────────────────────────────────────────────────────────────────

func TestMapErr_RecoverFromError(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("transient")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}

	s := stream.MapErr(ctx, src, func(err error) (float64, bool, error) {
		return 0.0, true, nil // recover: emit zero value
	})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 1 {
		t.Errorf("want 1 recovered value, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors after recovery, got %d", len(errs))
	}
}

func TestMapErr_SilenceError(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("ignorable")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}

	s := stream.MapErr(ctx, src, func(err error) (float64, bool, error) {
		return 0, false, nil // silence
	})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 0 || len(errs) != 0 {
		t.Errorf("silenced error should produce nothing, got %d vals %d errs", len(vals), len(errs))
	}
}

func TestMapErr_ReclassifyError(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("original")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}

	s := stream.MapErr(ctx, src, func(err error) (float64, bool, error) {
		return 0, false, fmt.Errorf("reclassified: %w", err)
	})
	_, errs := stream.Collect(ctx, s)
	if len(errs) != 1 {
		t.Fatalf("want 1 reclassified error, got %d", len(errs))
	}
	if errs[0].Error() == "original" {
		t.Error("error should be reclassified, not original")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func chanOf[T any](vals ...T) <-chan T {
	ch := make(chan T, len(vals))
	for _, v := range vals {
		ch <- v
	}
	close(ch)
	return ch
}

// recordingObserver implements stats.Observer but NOT stats.StreamObserver.
type recordingObserver struct {
	stats.NoopObserver
	valErrors []string
}

func (o *recordingObserver) RecordValidationError(location, constraint, field string) {
	o.valErrors = append(o.valErrors, location+"/"+constraint+"/"+field)
}

// streamItemSpy implements stats.StreamObserver.
type streamItemSpy struct {
	stats.NoopObserver
	calls      int
	allSuccess bool
	first      bool
}

func (s *streamItemSpy) RecordStreamItem(_ string, success bool, _ time.Duration) {
	s.calls++
	if s.calls == 1 {
		s.allSuccess = success
		s.first = true
	} else {
		s.allSuccess = s.allSuccess && success
	}
}

// ── Retry ─────────────────────────────────────────────────────────────────────

func TestRetry_RecoverFromError(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("transient decode error")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}

	retried := stream.Retry(ctx, src, func(err error) (float64, bool, error) {
		return 0.0, true, nil // recover with zero value
	})
	vals, errs := stream.Collect(ctx, retried)
	if len(vals) != 1 {
		t.Errorf("Retry: want 1 recovered value, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("Retry: want 0 errors after recovery, got %d", len(errs))
	}
}

func TestRetry_SilenceError(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan float64)
	errCh <- fmt.Errorf("ignorable")
	close(errCh)
	close(valCh)
	src := stream.Stream[float64]{Values: valCh, Errors: errCh}

	retried := stream.Retry(ctx, src, func(err error) (float64, bool, error) {
		return 0, false, nil // silence
	})
	vals, errs := stream.Collect(ctx, retried)
	if len(vals) != 0 || len(errs) != 0 {
		t.Errorf("Retry silence: want nothing, got %d vals %d errs", len(vals), len(errs))
	}
}

// ── TraceObserver span per item ────────────────────────────────────────────────

type traceApplySpy struct {
	stats.NoopObserver
	spans []string
	ended int
}

type spanKey struct{}

func (s *traceApplySpy) StartSpan(ctx context.Context, op, name string) context.Context {
	s.spans = append(s.spans, op+":"+name)
	return context.WithValue(ctx, spanKey{}, name)
}

func (s *traceApplySpy) EndSpan(_ context.Context, _ error) { s.ended++ }

func TestApply_TraceObserverSpanPerItem(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1.0, 2.0)
	spy := &traceApplySpy{}
	stream.Collect(ctx, stream.Apply(ctx, stream.From(ctx, src), doubleFn,
		stream.ApplyOptions{Observer: spy}))
	if len(spy.spans) != 2 {
		t.Errorf("StartSpan: want 2 per-item spans, got %d", len(spy.spans))
	}
	if spy.ended != 2 {
		t.Errorf("EndSpan: want 2 calls, got %d", spy.ended)
	}
	for _, s := range spy.spans {
		if s != "stream.apply:double" {
			t.Errorf("span name: want %q, got %q", "stream.apply:double", s)
		}
	}
}

// ── Example functions ─────────────────────────────────────────────────────────

func ExampleApply() {
	ctx := context.Background()
	double := forge.NewFunction("double", "1.0.0",
		codex.Float64().WithTitle("input"),
		codex.Float64().WithTitle("doubled"),
		func(v float64) (float64, error) { return v * 2, nil },
	)
	ch := make(chan float64, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)

	s := stream.Apply(ctx, stream.From(ctx, ch), double, stream.ApplyOptions{})
	vals, _ := stream.Collect(ctx, s)
	for _, v := range vals {
		fmt.Printf("%.0f\n", v)
	}
	// Output:
	// 2
	// 4
	// 6
}

func ExampleTap() {
	ctx := context.Background()
	ch := make(chan int, 3)
	ch <- 10
	ch <- 20
	ch <- 30
	close(ch)

	var observed []int
	s := stream.Tap(ctx, stream.From(ctx, ch), func(v int) {
		observed = append(observed, v)
	})
	stream.Collect(ctx, s)
	fmt.Println(len(observed), "items observed")
	// Output:
	// 3 items observed
}

// ── FlatMapSlice ──────────────────────────────────────────────────────────────

func TestFlatMapSlice_ExpandsEachItem(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1, 2, 3)
	// Each item expands to itself repeated twice
	s := stream.FlatMapSlice(ctx, stream.From(ctx, src), func(v int) []int {
		return []int{v, v * 10}
	})
	vals, errs := stream.Collect(ctx, s)
	if len(vals) != 6 {
		t.Errorf("FlatMapSlice: want 6 items (3×2), got %d: %v", len(vals), vals)
	}
	if len(errs) != 0 {
		t.Errorf("FlatMapSlice: want 0 errors, got %d", len(errs))
	}
}

func TestFlatMapSlice_EmptySliceActsAsFilter(t *testing.T) {
	ctx := context.Background()
	src := chanOf(1, 2, 3, 4)
	// Only even numbers emit items
	s := stream.FlatMapSlice(ctx, stream.From(ctx, src), func(v int) []int {
		if v%2 == 0 {
			return []int{v}
		}
		return nil // empty → filtered out
	})
	vals, _ := stream.Collect(ctx, s)
	if len(vals) != 2 {
		t.Errorf("FlatMapSlice: want 2 even items, got %d: %v", len(vals), vals)
	}
}

func TestFlatMapSlice_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("upstream err")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	s := stream.FlatMapSlice(ctx, src, func(v int) []int { return []int{v} })
	_, errs := stream.Collect(ctx, s)
	if len(errs) != 1 {
		t.Errorf("FlatMapSlice: error should be forwarded, got %d", len(errs))
	}
}

// ── T9: context observer ──────────────────────────────────────────────────────

func TestApply_ContextObserver_UsedWhenApplyOptsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intCodec := codex.Int()
	fn := forge.NewFunction("identity", "1.0", intCodec, intCodec, func(v int) (int, error) { return v, nil })

	var streamItemCalled bool
	spy := &applyStreamObserverSpy{onStreamItem: func() { streamItemCalled = true }}
	ctx = stats.WithObserver(ctx, spy)

	ch := make(chan int, 1)
	ch <- 42
	close(ch)
	s := stream.From(ctx, ch)
	out := stream.Apply(ctx, s, fn, stream.ApplyOptions{}) // no explicit Observer

	vals, errs := stream.Collect(ctx, out)
	if len(errs) != 0 || len(vals) != 1 || vals[0] != 42 {
		t.Fatalf("unexpected result: vals=%v errs=%v", vals, errs)
	}
	if !streamItemCalled {
		t.Error("want StreamObserver.RecordStreamItem called from context observer, got nothing")
	}
}

func TestApply_ExplicitObserver_BeatsContextObserver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intCodec := codex.Int()
	fn := forge.NewFunction("identity", "1.0", intCodec, intCodec, func(v int) (int, error) { return v, nil })

	var explicitCalled, contextCalled bool
	explicit := &applyStreamObserverSpy{onStreamItem: func() { explicitCalled = true }}
	ctxObs := &applyStreamObserverSpy{onStreamItem: func() { contextCalled = true }}
	ctx = stats.WithObserver(ctx, ctxObs)

	ch := make(chan int, 1)
	ch <- 1
	close(ch)
	s := stream.From(ctx, ch)
	out := stream.Apply(ctx, s, fn, stream.ApplyOptions{Observer: explicit})

	stream.Collect(ctx, out)

	if !explicitCalled {
		t.Error("want explicit observer called")
	}
	if contextCalled {
		t.Error("want context observer NOT called when explicit is set")
	}
}

type applyStreamObserverSpy struct {
	stats.NoopObserver
	onStreamItem func()
}

func (s *applyStreamObserverSpy) RecordStreamItem(_ string, _ bool, _ time.Duration) {
	if s.onStreamItem != nil {
		s.onStreamItem()
	}
}
