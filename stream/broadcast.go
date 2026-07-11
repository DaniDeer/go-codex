package stream

import (
	"context"
	"sync"
)

// BroadcastHub fans out a single [Stream] source to N independent subscribers.
// Each subscriber receives its own [Stream] with a private buffered channel.
// Slow subscribers apply backpressure only to themselves — items are dropped for
// a full subscriber buffer rather than blocking the hub goroutine.
//
// Create a hub and subscribe before the source begins emitting:
//
//	hub := stream.NewBroadcastHub(ctx, oeeStream, 32)
//	sub1 := hub.Subscribe()
//	sub2 := hub.Subscribe()
//
// Unsubscribe when a subscriber is no longer needed:
//
//	hub.Unsubscribe(sub1)
//
// The hub runs until ctx is cancelled or the source stream terminates.
// All subscriber channels are closed when the hub exits.
type BroadcastHub[T any] struct {
	mu   sync.Mutex
	buf  int
	subs map[int]*subscriber[T]
	next int
}

type subscriber[T any] struct {
	values chan T
	errs   chan error
}

// NewBroadcastHub creates a [BroadcastHub] that reads from src and fans out every
// value and error to all current subscribers.
//
// bufPerSubscriber is the buffer size for each subscriber's Values and Errors
// channels. A buffer of 0 is unbuffered — the hub blocks until the subscriber
// reads each item. Use a positive buffer to prevent slow subscribers from
// stalling each other.
//
// The hub goroutine terminates when ctx is cancelled or src closes. All
// subscriber channels are then closed so downstream consumers drain cleanly.
func NewBroadcastHub[T any](ctx context.Context, src Stream[T], bufPerSubscriber int) *BroadcastHub[T] {
	h := &BroadcastHub[T]{
		buf:  bufPerSubscriber,
		subs: make(map[int]*subscriber[T]),
	}
	go h.run(ctx, src)
	return h
}

// Subscribe adds a new subscriber and returns its [Stream].
// The returned stream's channels are buffered with the hub's configured
// bufPerSubscriber size. The channels are closed when the hub exits.
// Subscribe is safe to call concurrently.
func (h *BroadcastHub[T]) Subscribe() Stream[T] {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.next
	h.next++
	sub := &subscriber[T]{
		values: make(chan T, h.buf),
		errs:   make(chan error, h.buf),
	}
	h.subs[id] = sub
	return Stream[T]{Values: sub.values, Errors: sub.errs}
}

// Unsubscribe removes a subscriber. The subscriber's channels are not closed by
// Unsubscribe — they close when the hub exits. After Unsubscribe the hub stops
// sending to those channels.
func (h *BroadcastHub[T]) Unsubscribe(s Stream[T]) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, sub := range h.subs {
		if sub.values == s.Values {
			delete(h.subs, id)
			return
		}
	}
}

func (h *BroadcastHub[T]) run(ctx context.Context, src Stream[T]) {
	defer func() {
		h.mu.Lock()
		for _, sub := range h.subs {
			close(sub.values)
			close(sub.errs)
		}
		h.mu.Unlock()
	}()

	valCh := src.Values
	errCh := src.Errors
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			h.fanoutValue(v)
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			h.fanoutError(e)
		}
	}
}

func (h *BroadcastHub[T]) fanoutValue(v T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		select {
		case sub.values <- v:
		default: // drop on full buffer — slow subscriber
		}
	}
}

func (h *BroadcastHub[T]) fanoutError(e error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		select {
		case sub.errs <- e:
		default: // drop on full buffer
		}
	}
}
