package stream

import (
	"context"
	"time"

	"github.com/DaniDeer/go-codex/forge"
	"github.com/DaniDeer/go-codex/stats"
)

// ApplyOptions configures [Apply].
type ApplyOptions struct {
	// Observer receives [stats.StreamObserver.RecordStreamItem] for every item
	// processed by Apply (success or failure).
	// [stats.PipelineObserver.RecordApply] fires separately inside
	// [forge.Function.Apply] — both observers fire independently.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// Buffer is the output Values and Errors channel buffer size. Default 0.
	Buffer int
}

// Apply applies fn to every value in src using [forge.Function.ApplyContext].
//
// All forge validation — input codec Refine, optional WithRefinement, compute
// function, output codec Refine — runs per item. Successful outputs go to
// Stream.Values. Validation or compute failures are wrapped in [StreamApplyError]
// and sent to Stream.Errors. The stream continues after each error.
//
// [stats.PipelineObserver.RecordApply] fires inside forge for every item.
// If opts.Observer also implements [stats.StreamObserver], RecordStreamItem fires
// for every item with the forge function name, success flag, and duration.
//
// The forge function's own observer (set via [forge.Function.Register]) fires
// independently — both observers can be active simultaneously.
func Apply[In, Out any](
	ctx context.Context,
	src Stream[In],
	fn *forge.Function[In, Out],
	opts ApplyOptions,
) Stream[Out] {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	values := make(chan Out, opts.Buffer)
	errs := make(chan error, opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
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
				// Per-item trace span: if the observer implements TraceObserver,
				// wrap this item in a child span so distributed traces capture
				// each stream item individually.
				itemCtx := ctx
				if to, ok2 := obs.(stats.TraceObserver); ok2 {
					itemCtx = to.StartSpan(ctx, "stream.apply", fn.Spec.Name)
				}
				start := time.Now()
				result, err := fn.ApplyContext(itemCtx, v)
				dur := time.Since(start)
				if to, ok2 := obs.(stats.TraceObserver); ok2 {
					to.EndSpan(itemCtx, err)
				}
				if so, ok2 := obs.(stats.StreamObserver); ok2 {
					so.RecordStreamItem(fn.Spec.Name, err == nil, dur)
				}
				if err != nil {
					sae := StreamApplyError{Function: fn.Spec.Name, Err: err}
					select {
					case errs <- sae:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- result:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[Out]{Values: values, Errors: errs}
}

// Filter keeps value items where pred returns true.
// Value items for which pred returns false are dropped silently.
// Error items are forwarded to the output Stream.Errors unchanged.
func Filter[T any](ctx context.Context, src Stream[T], pred func(T) bool) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
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
				if !pred(v) {
					continue
				}
				select {
				case values <- v:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[T]{Values: values, Errors: errs}
}

// Tap inserts a domain event observer on the value channel without transforming items.
// onValue is called for each successful value; the value is then forwarded unchanged.
// Error items are forwarded to the output Stream.Errors unchanged.
//
// Use Tap for domain-level observation — auditing, triggering side effects, logging
// application-level events — independently from infrastructure metrics:
//
//	oeeStream = stream.Tap(ctx, oeeStream, func(oee OEE) {
//	    slog.Info("OEE computed", "value", float64(oee))
//	    businessDashboard.Publish(oee)
//	})
func Tap[T any](ctx context.Context, src Stream[T], onValue func(T)) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
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
				onValue(v)
				select {
				case values <- v:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[T]{Values: values, Errors: errs}
}

// MapErr transforms errors in Stream.Errors, enabling error recovery or reclassification.
//
// fn receives each error and returns one of:
//   - (value, true, nil) — recover: emit value to Stream.Values
//   - (zero, false, err) — reclassify: emit new error to Stream.Errors
//   - (zero, false, nil) — silence: drop the error entirely
//
// Value items pass through to the output Stream.Values unchanged.
//
// Use MapErr for dead-lettering, retry-after-transformation, or silencing
// expected transient errors:
//
//	recovered := stream.MapErr(ctx, src, func(err error) (T, bool, error) {
//	    var sde stream.StreamDecodeError
//	    if errors.As(err, &sde) && isTransient(sde.Err) {
//	        return zero, false, nil // silence transient decode errors
//	    }
//	    return zero, false, err // re-emit all other errors
//	})
func MapErr[T any](ctx context.Context, src Stream[T], fn func(error) (T, bool, error)) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
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
				select {
				case values <- v:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				v, isValue, newErr := fn(e)
				if isValue {
					select {
					case values <- v:
					case <-ctx.Done():
						return
					}
				} else if newErr != nil {
					select {
					case errs <- newErr:
					case <-ctx.Done():
						return
					}
				}
				// else: silenced — drop
			}
		}
	}()
	return Stream[T]{Values: values, Errors: errs}
}

// Retry transforms error items, enabling recovery or reclassification with
// caller-controlled retry logic. Successful value items pass through unchanged.
//
// retry receives each error and returns one of:
//   - (value, true, nil)  — recover: emit value to Stream.Values
//   - (zero, false, err)  — reclassify: emit new error to Stream.Errors
//   - (zero, false, nil)  — silence: drop the error entirely
//
// The caller's retry function controls timing and backoff. For exponential backoff:
//
//	retried := stream.Retry(ctx, src, func(err error) (T, bool, error) {
//	    var sde stream.StreamDecodeError
//	    if errors.As(err, &sde) {
//	        time.Sleep(100 * time.Millisecond) // simple backoff
//	        return zero, false, err            // reclassify for a retry queue
//	    }
//	    return zero, false, nil // silence unrecoverable errors
//	})
//
// Retry is a specialisation of [MapErr] for the common case where the retry
// function needs a concise name. Callers who need the full (value,isValue,err)
// tuple directly should use [MapErr].
func Retry[T any](ctx context.Context, src Stream[T], retry func(error) (T, bool, error)) Stream[T] {
	return MapErr(ctx, src, retry)
}

// FlatMapSlice maps each value item to a []Out slice and emits each element of
// the slice individually to the output Stream.Values. An empty slice from fn
// produces no output items (filter-like behaviour for that item).
// Errors from src pass through to Stream.Errors unchanged.
//
// Use FlatMapSlice when one incoming item should expand into multiple outgoing
// items — for example, one batch record expanding into N individual readings,
// or one sensor event triggering multiple derived measurements.
//
// Unlike a hypothetical FlatMap[In, Stream[Out]], FlatMapSlice requires no
// goroutine pool: fn is called synchronously per item.
func FlatMapSlice[In, Out any](ctx context.Context, src Stream[In], fn func(In) []Out) Stream[Out] {
	values := make(chan Out)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
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
				for _, out := range fn(v) {
					select {
					case values <- out:
					case <-ctx.Done():
						return
					}
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[Out]{Values: values, Errors: errs}
}
