package stream

import (
	"context"
	"time"
)

// Buffer collects up to n value items (or until maxWait elapses since the last
// emission) and emits them as a batch []T. Errors from src are forwarded to the
// output Stream.Errors immediately, without buffering.
//
// Use Buffer to integrate item-at-a-time streams with [forge.Map] (which takes []T):
//
//	batchStream := stream.Buffer(ctx, sensorStream, 10, 500*time.Millisecond)
//	batchOEE    := stream.Apply(ctx, batchStream, batchOEECalc, opts)
func Buffer[T any](ctx context.Context, src Stream[T], n int, maxWait time.Duration) Stream[[]T] {
	values := make(chan []T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var batch []T
		var timer <-chan time.Time // nil = not running; select on nil never fires
		flush := func() {
			if len(batch) == 0 {
				return
			}
			b := batch
			batch = nil
			timer = nil
			select {
			case values <- b:
			case <-ctx.Done():
			}
		}
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					flush()
					valCh = nil
					continue
				}
				batch = append(batch, v)
				if timer == nil {
					timer = time.After(maxWait)
				}
				if len(batch) >= n {
					flush()
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
			case <-timer:
				flush()
			}
		}
	}()
	return Stream[[]T]{Values: values, Errors: errs}
}

// Debounce emits a value only when src.Values is silent for at least d.
// Intermediate values during the silence window are dropped; only the last
// value before the silence elapses is emitted.
// Error items are forwarded to Stream.Errors immediately.
//
// Use Debounce when only the final value of a burst matters — for example,
// sensor readings that settle after a transient spike.
func Debounce[T any](ctx context.Context, src Stream[T], d time.Duration) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var pending *T
		var timer <-chan time.Time
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil || pending != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					// Source closed — fire pending immediately
					if pending != nil {
						timer = time.After(0)
					}
					valCh = nil
					continue
				}
				pending = &v
				timer = time.After(d)
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
			case <-timer:
				if pending != nil {
					v := *pending
					pending = nil
					timer = nil
					select {
					case values <- v:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return Stream[T]{Values: values, Errors: errs}
}

// Throttle emits at most one value per interval, dropping intermediates.
// The first value in an interval is emitted; subsequent values arriving before
// the interval elapses are dropped.
// Error items are forwarded to Stream.Errors immediately.
//
// Use Throttle to rate-limit high-frequency sources while ensuring at least
// one value is emitted per interval.
func Throttle[T any](ctx context.Context, src Stream[T], interval time.Duration) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var nextAllowed time.Time
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
				now := time.Now()
				if now.Before(nextAllowed) {
					continue // throttled — drop
				}
				nextAllowed = now.Add(interval)
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

// Window emits all values collected during each fixed-duration time window as a
// []T slice. An empty slice is emitted when no items arrived during a window.
// Errors from src are forwarded immediately.
//
// Unlike [Buffer], Window always emits at fixed calendar-aligned intervals using
// [time.NewTicker] — the emission clock never resets when items arrive. This
// gives consistent time boundaries (e.g. "all readings in the past 1 minute,
// every minute") suitable for time-series analytics.
func Window[T any](ctx context.Context, src Stream[T], duration time.Duration) Stream[[]T] {
	values := make(chan []T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		ticker := time.NewTicker(duration)
		defer ticker.Stop()
		var batch []T
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
				batch = append(batch, v)
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
			case <-ticker.C:
				b := batch
				batch = nil
				select {
				case values <- b:
				case <-ctx.Done():
					return
				}
			}
		}
		// Flush remaining items when source closes
		if len(batch) > 0 {
			select {
			case values <- batch:
			case <-ctx.Done():
			}
		}
	}()
	return Stream[[]T]{Values: values, Errors: errs}
}

// SlidingWindow emits a []T slice every step items, containing the last size items.
// The window slides forward by step items on each emission. When step == size the
// windows are non-overlapping (tumbling). Requires step > 0 and size >= step.
//
// Errors from src are forwarded to Stream.Errors immediately without affecting
// the sliding window position.
func SlidingWindow[T any](ctx context.Context, src Stream[T], size, step int) Stream[[]T] {
	if step <= 0 {
		panic("stream.SlidingWindow: step must be > 0")
	}
	if size < step {
		panic("stream.SlidingWindow: size must be >= step")
	}
	values := make(chan []T)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var buf []T
		count := 0
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
				buf = append(buf, v)
				count++
				if count == step {
					count = 0
					if len(buf) >= size {
						window := make([]T, size)
						copy(window, buf[len(buf)-size:])
						select {
						case values <- window:
						case <-ctx.Done():
							return
						}
						// advance buffer by step
						buf = buf[step:]
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
	return Stream[[]T]{Values: values, Errors: errs}
}
