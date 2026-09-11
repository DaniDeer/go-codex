package reqreply

import (
	"context"
	"log/slog"
	"sync"
)

// Future[T] is a single-slot, write-once promise for an async
// [Client.CallAsync] result — the additive, reqreply-only async
// counterpart to [Client.Call]'s blocking round trip. Needed because
// reqreply's underlying transport (MQTT5/ZeroMQ correlation-based reply
// matching) is genuinely asynchronous underneath, unlike REST's
// synchronous HTTP (whose Call has no natural async variant to offer) —
// see docs/design/d-0004-reqreply-workflow-simplification.md's Decision 5 for
// the full confirmed motivation and evidence.
//
// Construct one via [Client.CallAsync] — never directly.
type Future[T any] struct {
	done chan struct{}
	mu   sync.Mutex
	val  T
	err  error
}

// newFuture returns an unresolved Future[T].
func newFuture[T any]() *Future[T] {
	return &Future[T]{done: make(chan struct{})}
}

// resolve fulfils f exactly once with (val, err) — subsequent calls are
// no-ops (write-once, mirrors a single-slot channel's own semantics).
func (f *Future[T]) resolve(val T, err error) {
	f.mu.Lock()
	select {
	case <-f.done:
		f.mu.Unlock()
		return // already resolved — no-op, write-once semantics
	default:
	}
	f.val, f.err = val, err
	close(f.done)
	f.mu.Unlock()
}

// Wait blocks until f resolves or ctx is cancelled, whichever comes
// first. Returns a [FutureTimeoutError] wrapping ctx's error on
// cancellation — the transport-agnostic, api/reqreply-owned counterpart
// to each adapter's own timeout error kind (e.g. [mqtt5.CallError]{Kind:
// [mqtt5.KindTimeout]}); api/reqreply cannot reuse an adapter's error
// type directly (adapters depend on api/reqreply, never the reverse).
func (f *Future[T]) Wait(ctx context.Context) (T, error) {
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		var zero T
		return zero, FutureTimeoutError{Err: ctx.Err()}
	}
}

// FutureTimeoutError is returned by [Future.Wait] when ctx is cancelled
// before the future resolves.
type FutureTimeoutError struct {
	Err error
}

func (e FutureTimeoutError) Error() string {
	return "api/reqreply: Future.Wait: timed out: " + e.Err.Error()
}

func (e FutureTimeoutError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e FutureTimeoutError) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("err", e.Err))
}

// FutureFactory is implemented by *[RouteHandle][Req,Resp] (for any
// Req/Resp pair) — see [RouteHandle.NewFutureAny]'s doc comment for the
// full confirmed type-erasure-crossing mechanism this backs. An adapter's
// ClientTransport.CallAsync recovers a correctly-typed *[Future][Resp]
// and a matching resolve closure via a plain interface type assertion on
// its `route any` argument — zero reflection needed to instantiate a
// generic type at runtime.
type FutureFactory interface {
	// NewFutureAny returns (*Future[Resp] as any, resolve) — resolve is a
	// type-erased closure that fulfils the future exactly once when
	// called with (v, err); v's dynamic type must be Resp (a mismatch
	// resolves the future with an error instead of panicking).
	NewFutureAny() (any, func(any, error))
}
