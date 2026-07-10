package stream

import "context"

// CombineLatest2 merges the latest value from two independent streams using combine.
// A new combined value is emitted whenever either source emits a new value.
// The output stream blocks until both sources have emitted at least one value.
// Errors from either source are forwarded to the output Stream.Errors.
//
// Use CombineLatest2 to feed a [forge.Function] that takes a 2-field input struct,
// where the two fields arrive on independent streams:
//
//	oeeInputs := stream.CombineLatest2(ctx, availStream, perfStream,
//	    func(a Availability, p Performance) OEEIn { return OEEIn{a, p} })
//	oeeStream := stream.Apply(ctx, oeeInputs, oeeCalcFn, opts)
func CombineLatest2[A, B, Out any](
	ctx context.Context,
	a Stream[A],
	b Stream[B],
	combine func(A, B) Out,
) Stream[Out] {
	values := make(chan Out)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var latestA *A
		var latestB *B
		aCh := a.Values
		bCh := b.Values
		aErrCh := a.Errors
		bErrCh := b.Errors
		for aCh != nil || bCh != nil || aErrCh != nil || bErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-aCh:
				if !ok {
					aCh = nil
					continue
				}
				latestA = &v
				if latestB != nil {
					out := combine(*latestA, *latestB)
					select {
					case values <- out:
					case <-ctx.Done():
						return
					}
				}
			case v, ok := <-bCh:
				if !ok {
					bCh = nil
					continue
				}
				latestB = &v
				if latestA != nil {
					out := combine(*latestA, *latestB)
					select {
					case values <- out:
					case <-ctx.Done():
						return
					}
				}
			case e, ok := <-aErrCh:
				if !ok {
					aErrCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			case e, ok := <-bErrCh:
				if !ok {
					bErrCh = nil
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

// CombineLatest3 merges the latest value from three independent streams using
// combine. A new combined value is emitted whenever any source emits a new value,
// after all three sources have emitted at least one value.
// Errors from any source are forwarded to the output Stream.Errors.
//
// Use CombineLatest3 to feed a forge function with a 3-field input struct where
// each field arrives on a separate stream (e.g. OEE = Availability × Performance × Quality):
//
//	oeeInputs := stream.CombineLatest3(ctx, availStream, perfStream, qualStream,
//	    func(a Availability, p Performance, q Quality) OEEIn {
//	        return OEEIn{Availability: a, Performance: p, Quality: q}
//	    })
//	oeeStream := stream.Apply(ctx, oeeInputs, oeeCalcFn, opts)
func CombineLatest3[A, B, C, Out any](
	ctx context.Context,
	a Stream[A],
	b Stream[B],
	c Stream[C],
	combine func(A, B, C) Out,
) Stream[Out] {
	values := make(chan Out)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var latestA *A
		var latestB *B
		var latestC *C
		aCh, bCh, cCh := a.Values, b.Values, c.Values
		aErrCh, bErrCh, cErrCh := a.Errors, b.Errors, c.Errors
		emit := func() {
			if latestA == nil || latestB == nil || latestC == nil {
				return
			}
			out := combine(*latestA, *latestB, *latestC)
			select {
			case values <- out:
			case <-ctx.Done():
			}
		}
		for aCh != nil || bCh != nil || cCh != nil || aErrCh != nil || bErrCh != nil || cErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-aCh:
				if !ok {
					aCh = nil
					continue
				}
				latestA = &v
				emit()
			case v, ok := <-bCh:
				if !ok {
					bCh = nil
					continue
				}
				latestB = &v
				emit()
			case v, ok := <-cCh:
				if !ok {
					cCh = nil
					continue
				}
				latestC = &v
				emit()
			case e, ok := <-aErrCh:
				if !ok {
					aErrCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			case e, ok := <-bErrCh:
				if !ok {
					bErrCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			case e, ok := <-cErrCh:
				if !ok {
					cErrCh = nil
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

// CombineLatest4 merges the latest value from four independent streams.
// Same semantics as [CombineLatest3] extended to four sources.
func CombineLatest4[A, B, C, D, Out any](
	ctx context.Context,
	a Stream[A],
	b Stream[B],
	c Stream[C],
	d Stream[D],
	combine func(A, B, C, D) Out,
) Stream[Out] {
	values := make(chan Out)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var latestA *A
		var latestB *B
		var latestC *C
		var latestD *D
		aCh, bCh, cCh, dCh := a.Values, b.Values, c.Values, d.Values
		aErrCh, bErrCh, cErrCh, dErrCh := a.Errors, b.Errors, c.Errors, d.Errors
		emit := func() {
			if latestA == nil || latestB == nil || latestC == nil || latestD == nil {
				return
			}
			out := combine(*latestA, *latestB, *latestC, *latestD)
			select {
			case values <- out:
			case <-ctx.Done():
			}
		}
		fwdErr := func(e error) bool {
			select {
			case errs <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for aCh != nil || bCh != nil || cCh != nil || dCh != nil ||
			aErrCh != nil || bErrCh != nil || cErrCh != nil || dErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-aCh:
				if !ok {
					aCh = nil
					continue
				}
				latestA = &v
				emit()
			case v, ok := <-bCh:
				if !ok {
					bCh = nil
					continue
				}
				latestB = &v
				emit()
			case v, ok := <-cCh:
				if !ok {
					cCh = nil
					continue
				}
				latestC = &v
				emit()
			case v, ok := <-dCh:
				if !ok {
					dCh = nil
					continue
				}
				latestD = &v
				emit()
			case e, ok := <-aErrCh:
				if !ok {
					aErrCh = nil
					continue
				}
				if !fwdErr(e) {
					return
				}
			case e, ok := <-bErrCh:
				if !ok {
					bErrCh = nil
					continue
				}
				if !fwdErr(e) {
					return
				}
			case e, ok := <-cErrCh:
				if !ok {
					cErrCh = nil
					continue
				}
				if !fwdErr(e) {
					return
				}
			case e, ok := <-dErrCh:
				if !ok {
					dErrCh = nil
					continue
				}
				if !fwdErr(e) {
					return
				}
			}
		}
	}()
	return Stream[Out]{Values: values, Errors: errs}
}

// Zip pairs items from two streams by position: (a[0],b[0]), (a[1],b[1]), ...
// A combined item is emitted when both sources have emitted their n-th value.
// If one source emits faster, the faster source's items are buffered internally.
// Errors from either source are forwarded to the output Stream.Errors immediately.
//
// Unlike [CombineLatest2], which emits on every update using the latest values,
// Zip waits for matched pairs in order.
func Zip[A, B, Out any](
	ctx context.Context,
	a Stream[A],
	b Stream[B],
	combine func(A, B) Out,
) Stream[Out] {
	values := make(chan Out)
	errs := make(chan error)
	go func() {
		defer close(values)
		defer close(errs)
		var queueA []A
		var queueB []B
		aCh, bCh := a.Values, b.Values
		aErrCh, bErrCh := a.Errors, b.Errors
		tryEmit := func() bool {
			for len(queueA) > 0 && len(queueB) > 0 {
				out := combine(queueA[0], queueB[0])
				queueA = queueA[1:]
				queueB = queueB[1:]
				select {
				case values <- out:
				case <-ctx.Done():
					return false
				}
			}
			return true
		}
		for aCh != nil || bCh != nil || aErrCh != nil || bErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-aCh:
				if !ok {
					aCh = nil
					continue
				}
				queueA = append(queueA, v)
				if !tryEmit() {
					return
				}
			case v, ok := <-bCh:
				if !ok {
					bCh = nil
					continue
				}
				queueB = append(queueB, v)
				if !tryEmit() {
					return
				}
			case e, ok := <-aErrCh:
				if !ok {
					aErrCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			case e, ok := <-bErrCh:
				if !ok {
					bErrCh = nil
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
