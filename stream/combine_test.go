package stream_test

import (
	"context"
	"fmt"
	"testing"

	stream "github.com/DaniDeer/go-codex/stream"
)

// ── CombineLatest2 ────────────────────────────────────────────────────────────

type pair struct{ A, B int }

func TestCombineLatest2_EmitsOnEveryUpdate(t *testing.T) {
	ctx := context.Background()
	// a: 1, 2  b: 10
	// After both have emitted once, emit on every update:
	//   a=1 → wait for b
	//   b=10 → emit (1,10)
	//   a=2 → emit (2,10)
	aCh := make(chan int, 2)
	bCh := make(chan int, 1)
	aCh <- 1
	aCh <- 2
	bCh <- 10
	close(aCh)
	close(bCh)

	aErrCh2 := make(chan error)
	close(aErrCh2)
	bErrCh2 := make(chan error)
	close(bErrCh2)
	aStream := stream.Stream[int]{Values: aCh, Errors: aErrCh2}
	bStream := stream.Stream[int]{Values: bCh, Errors: bErrCh2}

	combined := stream.CombineLatest2(ctx, aStream, bStream,
		func(a, b int) pair { return pair{a, b} })
	vals, errs := stream.Collect(ctx, combined)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
	// At least one pair must be emitted
	if len(vals) == 0 {
		t.Fatal("want at least 1 combined value, got 0")
	}
	// All emitted pairs must have B=10
	for _, v := range vals {
		if v.B != 10 {
			t.Errorf("B must always be 10 (latest), got %d", v.B)
		}
	}
}

func TestCombineLatest2_BlocksUntilBothEmit(t *testing.T) {
	ctx := context.Background()
	// b never emits — should produce no pairs
	aCh := make(chan int, 1)
	bCh := make(chan int) // empty, will be closed immediately
	aCh <- 1
	close(aCh)
	close(bCh)

	aErrCh3 := make(chan error)
	close(aErrCh3)
	bErrCh3 := make(chan error)
	close(bErrCh3)
	aStream := stream.Stream[int]{Values: aCh, Errors: aErrCh3}
	bStream := stream.Stream[int]{Values: bCh, Errors: bErrCh3}

	combined := stream.CombineLatest2(ctx, aStream, bStream,
		func(a, b int) pair { return pair{a, b} })
	vals, _ := stream.Collect(ctx, combined)
	if len(vals) != 0 {
		t.Errorf("no pair should emit when b never emits; got %d pairs: %v", len(vals), vals)
	}
}

func TestCombineLatest2_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	aErrCh := make(chan error, 1)
	aErrCh <- fmt.Errorf("a error")
	close(aErrCh)

	aCh := make(chan int)
	bCh := make(chan int)
	bErrCh := make(chan error)
	close(aCh)
	close(bCh)
	close(bErrCh)

	aStream := stream.Stream[int]{Values: aCh, Errors: aErrCh}
	bStream := stream.Stream[int]{Values: bCh, Errors: bErrCh}

	combined := stream.CombineLatest2(ctx, aStream, bStream,
		func(a, b int) pair { return pair{a, b} })
	_, errs := stream.Collect(ctx, combined)
	if len(errs) != 1 {
		t.Errorf("error from a should be forwarded, got %d", len(errs))
	}
}

func TestCombineLatest2_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Infinite sources
	aCh := make(chan int)
	bCh := make(chan int)
	aStream := stream.Stream[int]{Values: aCh, Errors: make(chan error)}
	bStream := stream.Stream[int]{Values: bCh, Errors: make(chan error)}

	combined := stream.CombineLatest2(ctx, aStream, bStream,
		func(a, b int) pair { return pair{a, b} })

	done := make(chan struct{})
	go func() {
		stream.Collect(ctx, combined)
		close(done)
	}()
	cancel()
	<-done // must stop after cancel
}

// ── CombineLatest3 ────────────────────────────────────────────────────────────

type triple struct{ A, B, C int }

func TestCombineLatest3_EmitsWhenAllHaveEmitted(t *testing.T) {
	ctx := context.Background()
	aCh := make(chan int, 1)
	bCh := make(chan int, 1)
	cCh := make(chan int, 1)
	aCh <- 1
	bCh <- 10
	cCh <- 100
	close(aCh)
	close(bCh)
	close(cCh)

	aErrCh := make(chan error)
	bErrCh := make(chan error)
	cErrCh := make(chan error)
	close(aErrCh)
	close(bErrCh)
	close(cErrCh)

	a := stream.Stream[int]{Values: aCh, Errors: aErrCh}
	b := stream.Stream[int]{Values: bCh, Errors: bErrCh}
	c := stream.Stream[int]{Values: cCh, Errors: cErrCh}

	combined := stream.CombineLatest3(ctx, a, b, c,
		func(a, b, c int) triple { return triple{a, b, c} })
	vals, errs := stream.Collect(ctx, combined)
	if len(errs) != 0 {
		t.Errorf("CombineLatest3: want 0 errors, got %d", len(errs))
	}
	if len(vals) == 0 {
		t.Fatal("CombineLatest3: want at least 1 combined value, got 0")
	}
	// Last value must have all three fields set from latest values
	last := vals[len(vals)-1]
	if last.A != 1 || last.B != 10 || last.C != 100 {
		t.Errorf("CombineLatest3: want {1,10,100}, got %+v", last)
	}
}

func TestCombineLatest3_BlocksUntilAllEmit(t *testing.T) {
	ctx := context.Background()
	// c never emits — no pair should be produced
	aCh := make(chan int, 1)
	bCh := make(chan int, 1)
	cCh := make(chan int)
	aCh <- 1
	bCh <- 10
	close(aCh)
	close(bCh)
	close(cCh)

	aErrCh := make(chan error)
	bErrCh := make(chan error)
	cErrCh := make(chan error)
	close(aErrCh)
	close(bErrCh)
	close(cErrCh)

	a := stream.Stream[int]{Values: aCh, Errors: aErrCh}
	b := stream.Stream[int]{Values: bCh, Errors: bErrCh}
	c := stream.Stream[int]{Values: cCh, Errors: cErrCh}

	combined := stream.CombineLatest3(ctx, a, b, c,
		func(a, b, c int) triple { return triple{a, b, c} })
	vals, _ := stream.Collect(ctx, combined)
	if len(vals) != 0 {
		t.Errorf("CombineLatest3 should not emit when c never emits; got %d", len(vals))
	}
}

// ── CombineLatest4 ────────────────────────────────────────────────────────────

type quad struct{ A, B, C, D int }

func TestCombineLatest4_EmitsWhenAllHaveEmitted(t *testing.T) {
	ctx := context.Background()
	makeIntStream := func(v int) stream.Stream[int] {
		ch := make(chan int, 1)
		ch <- v
		close(ch)
		errCh := make(chan error)
		close(errCh)
		return stream.Stream[int]{Values: ch, Errors: errCh}
	}

	combined := stream.CombineLatest4(ctx,
		makeIntStream(1), makeIntStream(2), makeIntStream(3), makeIntStream(4),
		func(a, b, c, d int) quad { return quad{a, b, c, d} })
	vals, errs := stream.Collect(ctx, combined)
	if len(errs) != 0 {
		t.Errorf("CombineLatest4: want 0 errors, got %d", len(errs))
	}
	if len(vals) == 0 {
		t.Fatal("CombineLatest4: want at least 1 combined value, got 0")
	}
	last := vals[len(vals)-1]
	if last.A != 1 || last.B != 2 || last.C != 3 || last.D != 4 {
		t.Errorf("CombineLatest4: want {1,2,3,4}, got %+v", last)
	}
}

// ── Zip ───────────────────────────────────────────────────────────────────────

func TestZip_PairsItemsByPosition(t *testing.T) {
	ctx := context.Background()
	aS := stream.From(ctx, chanOf(1, 2, 3))
	bS := stream.From(ctx, chanOf(10, 20, 30))
	zipped := stream.Zip(ctx, aS, bS, func(a, b int) pair { return pair{a, b} })
	vals, errs := stream.Collect(ctx, zipped)
	if len(errs) != 0 {
		t.Errorf("Zip: want 0 errors, got %d", len(errs))
	}
	if len(vals) != 3 {
		t.Fatalf("Zip: want 3 pairs, got %d: %v", len(vals), vals)
	}
	for i, want := range []pair{{1, 10}, {2, 20}, {3, 30}} {
		if vals[i] != want {
			t.Errorf("Zip[%d]: want %+v, got %+v", i, want, vals[i])
		}
	}
}

func TestZip_StopsWhenShorterStreamEnds(t *testing.T) {
	ctx := context.Background()
	// a has 3 items, b has 2 — should emit 2 pairs
	aS := stream.From(ctx, chanOf(1, 2, 3))
	bS := stream.From(ctx, chanOf(10, 20))
	zipped := stream.Zip(ctx, aS, bS, func(a, b int) pair { return pair{a, b} })
	vals, _ := stream.Collect(ctx, zipped)
	if len(vals) != 2 {
		t.Errorf("Zip: want 2 pairs (shorter stream), got %d", len(vals))
	}
}

func TestZip_ErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("zip error")
	close(errCh)
	close(valCh)
	aS := stream.Stream[int]{Values: valCh, Errors: errCh}
	bS := stream.From(ctx, chanOf(1))
	zipped := stream.Zip(ctx, aS, bS, func(a, b int) pair { return pair{a, b} })
	_, errs := stream.Collect(ctx, zipped)
	if len(errs) != 1 {
		t.Errorf("Zip: error should be forwarded, got %d", len(errs))
	}
}
