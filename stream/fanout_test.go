package stream_test

import (
	"context"
	"fmt"
	"testing"

	stream "github.com/DaniDeer/go-codex/stream"
)

// ── Merge ─────────────────────────────────────────────────────────────────────

func TestMerge_CombinesValues(t *testing.T) {
	ctx := context.Background()
	s1 := stream.From(ctx, chanOf(1, 2))
	s2 := stream.From(ctx, chanOf(3, 4))
	merged := stream.Merge(ctx, s1, s2)
	vals, errs := stream.Collect(ctx, merged)
	if len(vals) != 4 {
		t.Errorf("want 4 merged values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

func TestMerge_CombinesErrors(t *testing.T) {
	ctx := context.Background()
	errCh1 := make(chan error, 1)
	errCh2 := make(chan error, 1)
	valCh := make(chan int)
	errCh1 <- fmt.Errorf("e1")
	errCh2 <- fmt.Errorf("e2")
	close(errCh1)
	close(errCh2)
	close(valCh)
	valCh2 := make(chan int)
	close(valCh2)
	s1 := stream.Stream[int]{Values: valCh, Errors: errCh1}
	s2 := stream.Stream[int]{Values: valCh2, Errors: errCh2}
	merged := stream.Merge(ctx, s1, s2)
	_, errs := stream.Collect(ctx, merged)
	if len(errs) != 2 {
		t.Errorf("want 2 merged errors, got %d", len(errs))
	}
}

func TestMerge_EmptySources(t *testing.T) {
	ctx := context.Background()
	merged := stream.Merge[int](ctx)
	vals, errs := stream.Collect(ctx, merged)
	if len(vals) != 0 || len(errs) != 0 {
		t.Errorf("empty Merge should yield empty stream")
	}
}

// ── Tee ───────────────────────────────────────────────────────────────────────

func TestTee_BothCopiesReceiveAll(t *testing.T) {
	ctx := context.Background()
	src := stream.From(ctx, chanOf(10, 20, 30))
	left, right := stream.Tee(ctx, src)

	// Collect concurrently to avoid deadlock (Tee sends to both before moving on)
	leftDone := make(chan []int, 1)
	rightDone := make(chan []int, 1)
	go func() {
		vals, _ := stream.Collect(ctx, left)
		leftDone <- vals
	}()
	go func() {
		vals, _ := stream.Collect(ctx, right)
		rightDone <- vals
	}()
	l := <-leftDone
	r := <-rightDone
	if len(l) != 3 || len(r) != 3 {
		t.Errorf("both copies must receive all 3 items; got left=%d right=%d", len(l), len(r))
	}
}

func TestTee_ErrorsForwardedToBoth(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("tee error")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}
	left, right := stream.Tee(ctx, src)

	leftErrs := make(chan []error, 1)
	rightErrs := make(chan []error, 1)
	go func() { _, e := stream.Collect(ctx, left); leftErrs <- e }()
	go func() { _, e := stream.Collect(ctx, right); rightErrs <- e }()
	le := <-leftErrs
	re := <-rightErrs
	if len(le) != 1 || len(re) != 1 {
		t.Errorf("both copies must receive the error; left=%d right=%d", len(le), len(re))
	}
}
