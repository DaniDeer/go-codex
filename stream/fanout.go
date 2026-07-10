package stream

import (
	"context"
	"sync"
)

// Merge combines multiple streams into one. Items and errors from all source
// streams are forwarded as they arrive. The output stream terminates when all
// source streams have terminated.
//
// Use Merge to combine readings from multiple sensors or topics into a single
// processing pipeline.
func Merge[T any](ctx context.Context, srcs ...Stream[T]) Stream[T] {
	values := make(chan T)
	errs := make(chan error)
	var wg sync.WaitGroup
	for _, src := range srcs {
		wg.Add(1)
		go func(s Stream[T]) {
			defer wg.Done()
			valCh := s.Values
			errCh := s.Errors
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
					select {
					case errs <- e:
					case <-ctx.Done():
						return
					}
				}
			}
		}(src)
	}
	go func() {
		wg.Wait()
		close(values)
		close(errs)
	}()
	return Stream[T]{Values: values, Errors: errs}
}

// Tee splits src into two independent copies. Both copies receive all items and
// errors. Backpressure on either copy blocks the other — use buffered channels
// (via [SourceOptions.Buffer] on the original source) or [Drain] if the two
// consumers run at different speeds.
//
// Use Tee when you need the same stream for two independent purposes (e.g.
// storing to a database while also computing KPIs):
//
//	store, compute := stream.Tee(ctx, sensorStream)
//	go stream.Drain(ctx, store, saveToDB, logErr, opts)
//	oeeStream := stream.Apply(ctx, compute, oeeCalcFn, opts)
func Tee[T any](ctx context.Context, src Stream[T]) (Stream[T], Stream[T]) {
	values1 := make(chan T)
	values2 := make(chan T)
	errs1 := make(chan error)
	errs2 := make(chan error)
	go func() {
		defer close(values1)
		defer close(values2)
		defer close(errs1)
		defer close(errs2)
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
				// Send to both — if either blocks, both block (sync tee semantics).
				select {
				case values1 <- v:
				case <-ctx.Done():
					return
				}
				select {
				case values2 <- v:
				case <-ctx.Done():
					return
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs1 <- e:
				case <-ctx.Done():
					return
				}
				select {
				case errs2 <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return Stream[T]{Values: values1, Errors: errs1}, Stream[T]{Values: values2, Errors: errs2}
}
