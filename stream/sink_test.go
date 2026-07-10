package stream_test

import (
	"context"
	"fmt"
	"testing"

	stream "github.com/DaniDeer/go-codex/stream"
)

// ── Drain ─────────────────────────────────────────────────────────────────────

func TestDrain_CallsOnValueAndOnError(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan int, 2)
	errCh := make(chan error, 1)
	valCh <- 1
	valCh <- 2
	errCh <- fmt.Errorf("oops")
	close(valCh)
	close(errCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}

	var vals []int
	var errs []error
	stream.Drain(ctx, src,
		func(_ context.Context, v int) error { vals = append(vals, v); return nil },
		func(e error) { errs = append(errs, e) },
		stream.DrainOptions{},
	)
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error, got %d", len(errs))
	}
}

func TestDrain_OnValueErrorForwardedToOnError(t *testing.T) {
	ctx := context.Background()
	src := stream.From(ctx, chanOf(42))
	var errors []error
	stream.Drain(ctx, src,
		func(_ context.Context, _ int) error { return fmt.Errorf("sink error") },
		func(e error) { errors = append(errors, e) },
		stream.DrainOptions{},
	)
	if len(errors) != 1 {
		t.Errorf("want 1 error from onValue, got %d", len(errors))
	}
}

func TestDrain_NilOnError_DoesNotPanic(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("error")
	close(errCh)
	valCh := make(chan int)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	// Must not panic with nil onError
	stream.Drain(ctx, src,
		func(_ context.Context, _ int) error { return nil },
		nil,
		stream.DrainOptions{},
	)
}

func TestDrain_ContextCancel_Stops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Infinite source — must be stopped by cancel
	valCh := make(chan int)
	errCh := make(chan error)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}

	done := make(chan struct{})
	go func() {
		stream.Drain(ctx, src,
			func(_ context.Context, _ int) error { return nil },
			nil,
			stream.DrainOptions{},
		)
		close(done)
	}()
	cancel()
	<-done // must complete after cancel
}

// ── Collect ───────────────────────────────────────────────────────────────────

func TestCollect_AllValuesAndErrors(t *testing.T) {
	ctx := context.Background()
	valCh := make(chan string, 2)
	errCh := make(chan error, 1)
	valCh <- "a"
	valCh <- "b"
	errCh <- fmt.Errorf("e1")
	close(valCh)
	close(errCh)
	src := stream.Stream[string]{Values: valCh, Errors: errCh}
	vals, errs := stream.Collect(ctx, src)
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 error, got %d", len(errs))
	}
}

func TestCollect_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	valCh := make(chan int) // never closed
	errCh := make(chan error)
	close(errCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	cancel()
	vals, errs := stream.Collect(ctx, src)
	if len(vals) != 0 || len(errs) != 0 {
		t.Errorf("cancelled context should yield empty, got %d vals %d errs", len(vals), len(errs))
	}
}

// ── Example functions ─────────────────────────────────────────────────────────

func ExampleDrain() {
	ctx := context.Background()
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)

	var sum int
	stream.Drain(ctx, stream.From(ctx, ch),
		func(_ context.Context, v int) error {
			sum += v
			return nil
		},
		nil,
		stream.DrainOptions{},
	)
	fmt.Println("sum:", sum)
	// Output:
	// sum: 6
}
