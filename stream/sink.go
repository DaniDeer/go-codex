package stream

import (
	"context"

	"github.com/DaniDeer/go-codex/stats"
)

// DrainOptions configures [Drain].
type DrainOptions struct {
	// Observer receives [stats.Observer.RecordValidationError] for errors returned
	// by onValue. Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// Drain consumes src until it terminates or ctx is cancelled.
// onValue is called for each successful item in Stream.Values.
// onError is called for each error in Stream.Errors AND for errors returned by onValue.
//
// Drain ALWAYS drains both channels concurrently using a single select loop — it is
// the safe default sink that prevents goroutine leaks. Use Drain whenever you want
// to consume a stream without building your own select loop.
//
// onError may be nil; in that case errors are silently discarded.
func Drain[T any](
	ctx context.Context,
	src Stream[T],
	onValue func(context.Context, T) error,
	onError func(error),
	opts DrainOptions,
) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
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
			if err := onValue(ctx, v); err != nil {
				stats.ReportErrors(obs, "stream", err)
				if onError != nil {
					onError(err)
				}
			}
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if onError != nil {
				onError(e)
			}
		}
	}
}

// Collect accumulates all values and errors from src until it terminates or
// ctx is cancelled, then returns two slices.
//
// Collect is primarily intended for testing and bounded streams. For long-running
// or infinite streams use [Drain] instead.
func Collect[T any](ctx context.Context, src Stream[T]) (values []T, errs []error) {
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
			values = append(values, v)
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			errs = append(errs, e)
		}
	}
	return
}
