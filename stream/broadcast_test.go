package stream_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	stream "github.com/DaniDeer/go-codex/stream"
)

func TestBroadcastHub_SingleSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := stream.From(ctx, chanOf(1, 2, 3))
	hub := stream.NewBroadcastHub(ctx, src, 8)
	sub := hub.Subscribe()

	vals, errs := stream.Collect(ctx, sub)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
	if len(vals) != 3 {
		t.Errorf("want 3 values, got %d: %v", len(vals), vals)
	}
}

func TestBroadcastHub_MultipleSubscribers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	valCh := make(chan int, 3)
	valCh <- 10
	valCh <- 20
	valCh <- 30
	close(valCh)
	src := stream.From(ctx, valCh)

	hub := stream.NewBroadcastHub(ctx, src, 8)
	sub1 := hub.Subscribe()
	sub2 := hub.Subscribe()

	done1 := make(chan []int, 1)
	done2 := make(chan []int, 1)
	go func() { vals, _ := stream.Collect(ctx, sub1); done1 <- vals }()
	go func() { vals, _ := stream.Collect(ctx, sub2); done2 <- vals }()

	v1 := <-done1
	v2 := <-done2

	if len(v1) != 3 {
		t.Errorf("sub1: want 3 values, got %d: %v", len(v1), v1)
	}
	if len(v2) != 3 {
		t.Errorf("sub2: want 3 values, got %d: %v", len(v2), v2)
	}
}

func TestBroadcastHub_ErrorsFanOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	valCh := make(chan int)
	errCh <- fmt.Errorf("hub error")
	close(errCh)
	close(valCh)
	src := stream.Stream[int]{Values: valCh, Errors: errCh}

	hub := stream.NewBroadcastHub(ctx, src, 4)
	sub1 := hub.Subscribe()
	sub2 := hub.Subscribe()

	_, errs1 := stream.Collect(ctx, sub1)
	_, errs2 := stream.Collect(ctx, sub2)

	if len(errs1) != 1 {
		t.Errorf("sub1: want 1 error, got %d", len(errs1))
	}
	if len(errs2) != 1 {
		t.Errorf("sub2: want 1 error, got %d", len(errs2))
	}
}

func TestBroadcastHub_Unsubscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	valCh := make(chan int, 4)
	src := stream.From(ctx, valCh)

	hub := stream.NewBroadcastHub(ctx, src, 4)
	sub1 := hub.Subscribe()
	sub2 := hub.Subscribe()

	// Unsubscribe sub1 before any items arrive
	hub.Unsubscribe(sub1)

	valCh <- 1
	valCh <- 2
	close(valCh)

	// sub2 should still receive
	vals2, _ := stream.Collect(ctx, sub2)
	if len(vals2) != 2 {
		t.Errorf("sub2: want 2 values after unsubscribe of sub1, got %d", len(vals2))
	}
}

func TestBroadcastHub_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	valCh := make(chan int) // infinite, never closes
	src := stream.From(ctx, valCh)
	hub := stream.NewBroadcastHub(ctx, src, 4)
	sub := hub.Subscribe()

	done := make(chan struct{})
	go func() {
		stream.Collect(ctx, sub) // must terminate on cancel
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("subscriber did not terminate after context cancel")
	}
}

func ExampleBroadcastHub() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)

	hub := stream.NewBroadcastHub(ctx, stream.From(ctx, ch), 8)
	sub1 := hub.Subscribe()
	sub2 := hub.Subscribe()

	done1 := make(chan []int, 1)
	done2 := make(chan []int, 1)
	go func() { v, _ := stream.Collect(ctx, sub1); done1 <- v }()
	go func() { v, _ := stream.Collect(ctx, sub2); done2 <- v }()

	<-done1
	<-done2
	// Output:
}
