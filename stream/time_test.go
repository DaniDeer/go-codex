package stream_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	stream "github.com/DaniDeer/go-codex/stream"
)

// ── Buffer ────────────────────────────────────────────────────────────────────

func TestBuffer_EmitsBatchAtSize(t *testing.T) {
	ctx := context.Background()
	src := stream.From(ctx, chanOf(1, 2, 3, 4, 5, 6))
	batched := stream.Buffer(ctx, src, 3, 10*time.Second)
	vals, errs := stream.Collect(ctx, batched)
	if len(vals) != 2 {
		t.Errorf("want 2 batches of 3, got %d batches: %v", len(vals), vals)
	}
	if len(vals[0]) != 3 || len(vals[1]) != 3 {
		t.Errorf("each batch should have 3 items, got %d and %d", len(vals[0]), len(vals[1]))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

func TestBuffer_EmitsPartialBatchOnSourceClose(t *testing.T) {
	ctx := context.Background()
	// 4 items with batch size 3: one full batch + one partial (1 item)
	src := stream.From(ctx, chanOf(1, 2, 3, 4))
	batched := stream.Buffer(ctx, src, 3, 10*time.Second)
	vals, _ := stream.Collect(ctx, batched)
	if len(vals) != 2 {
		t.Errorf("want 2 batches (full + partial), got %d: %v", len(vals), vals)
	}
	if len(vals[0]) != 3 || len(vals[1]) != 1 {
		t.Errorf("unexpected batch sizes: %d, %d", len(vals[0]), len(vals[1]))
	}
}

func TestBuffer_EmitsBatchOnTimeout(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan int, 2)
	valCh <- 1
	valCh <- 2
	// Don't close — let the timer fire instead
	src := stream.Stream[int]{Values: valCh, Errors: make(chan error)}

	// Very short timeout to trigger immediately
	batched := stream.Buffer(ctx, src, 100, 10*time.Millisecond)
	// Only read the Values channel until one batch comes
	var batch []int
	select {
	case b, ok := <-batched.Values:
		if ok {
			batch = b
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for batch")
	}
	if len(batch) != 2 {
		t.Errorf("want 2 items in timeout batch, got %d", len(batch))
	}
}

func TestBuffer_ErrorsForwardedImmediately(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	batched := stream.Buffer(ctx, src, 10, time.Second)
	_, errs := stream.Collect(ctx, batched)
	if len(errs) != 1 {
		t.Errorf("error should be forwarded immediately, got %d", len(errs))
	}
}

// ── Debounce ──────────────────────────────────────────────────────────────────

func TestDebounce_EmitsLastValueAfterSilence(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan int, 3)
	valCh <- 1
	valCh <- 2
	valCh <- 3
	close(valCh)
	errCh2 := make(chan error)
	close(errCh2)
	src := stream.Stream[int]{Values: valCh, Errors: errCh2}
	debounced := stream.Debounce(ctx, src, 20*time.Millisecond)
	vals, _ := stream.Collect(ctx, debounced)
	// Only the last value should be emitted
	if len(vals) != 1 {
		t.Errorf("debounce should emit only 1 value (the last), got %d: %v", len(vals), vals)
	}
	if vals[0] != 3 {
		t.Errorf("debounce should emit last value (3), got %d", vals[0])
	}
}

func TestDebounce_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("debounce error")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	debounced := stream.Debounce(ctx, src, 10*time.Millisecond)
	_, errs := stream.Collect(ctx, debounced)
	if len(errs) != 1 {
		t.Errorf("error should be forwarded, got %d", len(errs))
	}
}

// ── Throttle ──────────────────────────────────────────────────────────────────

func TestThrottle_EmitsAtMostOnePerInterval(t *testing.T) {
	ctx := context.Background()
	// Send 5 items instantly, interval 50ms
	valCh := make(chan int, 5)
	for i := 0; i < 5; i++ {
		valCh <- i
	}
	close(valCh)
	errCh2 := make(chan error)
	close(errCh2)
	src := stream.Stream[int]{Values: valCh, Errors: errCh2}
	throttled := stream.Throttle(ctx, src, 50*time.Millisecond)
	vals, _ := stream.Collect(ctx, throttled)
	// All 5 arrive instantly — only 1 should pass through (the first one)
	if len(vals) != 1 {
		t.Errorf("throttle should emit only 1 item from instant burst, got %d: %v", len(vals), vals)
	}
	if vals[0] != 0 {
		t.Errorf("throttle should emit first item (0), got %d", vals[0])
	}
}

func TestThrottle_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("throttle error")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	throttled := stream.Throttle(ctx, src, 10*time.Millisecond)
	_, errs := stream.Collect(ctx, throttled)
	if len(errs) != 1 {
		t.Errorf("error should be forwarded, got %d", len(errs))
	}
}

// ── Window ────────────────────────────────────────────────────────────────────

func TestWindow_EmitsAtInterval(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan int, 4)
	valCh <- 1
	valCh <- 2
	valCh <- 3
	valCh <- 4
	close(valCh)
	errCh := make(chan error)
	close(errCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}

	// Very short window to keep test fast
	windowed := stream.Window(ctx, src, 20*time.Millisecond)
	vals, errs := stream.Collect(ctx, windowed)
	if len(errs) != 0 {
		t.Errorf("Window: want 0 errors, got %d", len(errs))
	}
	// At least one window must have been emitted containing all 4 items
	total := 0
	for _, batch := range vals {
		total += len(batch)
	}
	if total != 4 {
		t.Errorf("Window: want total 4 items across windows, got %d", total)
	}
}

func TestWindow_EmitsEmptyWindowWhenNoItems(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	// Source with no items — closed immediately
	valCh := make(chan int)
	errCh := make(chan error)
	close(valCh)
	close(errCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}

	windowed := stream.Window(ctx, src, 10*time.Millisecond)
	vals, _ := stream.Collect(ctx, windowed)
	// Window should emit (possibly empty) slices; all empty
	for _, batch := range vals {
		if len(batch) != 0 {
			t.Errorf("expected empty window, got %d items", len(batch))
		}
	}
}

func TestWindow_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("window error")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}

	windowed := stream.Window(ctx, src, 50*time.Millisecond)
	_, errs := stream.Collect(ctx, windowed)
	if len(errs) != 1 {
		t.Errorf("Window: error should be forwarded, got %d", len(errs))
	}
}

// ── SlidingWindow ─────────────────────────────────────────────────────────────

func TestSlidingWindow_TumblingMode(t *testing.T) {
	ctx := context.Background()
	// size=step=2 → tumbling: (1,2), (3,4)
	src := stream.From(ctx, chanOf(1, 2, 3, 4))
	windows, _ := stream.Collect(ctx, stream.SlidingWindow(ctx, src, 2, 2))
	if len(windows) != 2 {
		t.Fatalf("want 2 windows, got %d: %v", len(windows), windows)
	}
}

func TestSlidingWindow_Overlapping(t *testing.T) {
	ctx := context.Background()
	// size=3, step=1 → (1,2,3), (2,3,4), (3,4,5)
	src := stream.From(ctx, chanOf(1, 2, 3, 4, 5))
	windows, _ := stream.Collect(ctx, stream.SlidingWindow(ctx, src, 3, 1))
	if len(windows) != 3 {
		t.Fatalf("want 3 windows, got %d: %v", len(windows), windows)
	}
}

func TestSlidingWindow_PanicsOnBadArgs(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for step=0")
		}
	}()
	stream.SlidingWindow[int](context.Background(), stream.Stream[int]{}, 3, 0)
}
