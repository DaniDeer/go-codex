package ports

import (
	"context"

	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// ChanSourceAdapter wraps a plain Go channel as a [SourceAdapter].
// Use in tests to feed items into a [SourcePort] without a real transport.
//
//	ch := make(chan SensorReading, 2)
//	ch <- reading1; ch <- reading2; close(ch)
//	domain.SensorReadings.Bind(ctx, ports.ChanSourceAdapter(ch))
func ChanSourceAdapter[T any](ch <-chan T) SourceAdapter[T] {
	return &chanSourceAdapter[T]{ch: ch}
}

type chanSourceAdapter[T any] struct{ ch <-chan T }

func (a *chanSourceAdapter[T]) AdapterName() string { return "ports.ChanSourceAdapter" }

func (a *chanSourceAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-a.ch:
			if !ok {
				return
			}
			select {
			case dst <- v:
			case <-ctx.Done():
				return
			}
		}
	}
}

// ChanSinkAdapter wraps a plain Go channel as a [SinkAdapter].
// Use in tests to capture [SinkPort] output without a real transport.
//
//	out := make(chan OEE, 8)
//	domain.OEEResults.Bind(ctx, ports.ChanSinkAdapter(out))
func ChanSinkAdapter[T any](ch chan<- T) SinkAdapter[T] {
	return &chanSinkAdapter[T]{ch: ch}
}

type chanSinkAdapter[T any] struct{ ch chan<- T }

func (a *chanSinkAdapter[T]) AdapterName() string { return "ports.ChanSinkAdapter" }

func (a *chanSinkAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
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
			case a.ch <- v:
			case <-ctx.Done():
				return
			}
		case _, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			// errors silently discarded in test adapter
		}
	}
}

// FuncIOAdapter wraps a plain function as an [IOAdapter].
// Use in tests to stub an [IOPort] without a real service:
//
//	domain.Calibration.Bind(ctx, ports.FuncIOAdapter(func(ctx context.Context, r SensorReading) (CalibratedReading, error) {
//	    return CalibratedReading{Reading: r, Offset: 0.0}, nil
//	}))
func FuncIOAdapter[Req, Resp any](fn func(context.Context, Req) (Resp, error)) IOAdapter[Req, Resp] {
	return &funcIOAdapter[Req, Resp]{fn: fn}
}

type funcIOAdapter[Req, Resp any] struct {
	fn func(context.Context, Req) (Resp, error)
}

func (a *funcIOAdapter[Req, Resp]) AdapterName() string { return "ports.FuncIOAdapter" }

func (a *funcIOAdapter[Req, Resp]) Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp] {
	outCh := make(chan Resp, cap(src.Values)+1)
	errCh := make(chan error, cap(src.Errors)+1)

	go func() {
		defer close(outCh)
		defer close(errCh)
		valCh := src.Values
		srcErrCh := src.Errors
		for valCh != nil || srcErrCh != nil {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				resp, err := a.fn(ctx, v)
				if err != nil {
					select {
					case errCh <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case outCh <- resp:
				case <-ctx.Done():
					return
				}
			case e, ok := <-srcErrCh:
				if !ok {
					srcErrCh = nil
					continue
				}
				select {
				case errCh <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return gstream.Stream[Resp]{Values: outCh, Errors: errCh}
}
