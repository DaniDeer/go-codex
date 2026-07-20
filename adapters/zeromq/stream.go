package zeromq

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// AsPipelineFunc converts a pipeline handler function into the plain handler
// function signature accepted by [Serve] and [ServeRouter].
//
// Internally: wraps req as [gstream.Single], calls fn to build the pipeline,
// then collects the result via [gstream.Collect]. Errors take precedence over
// values. If the pipeline emits no value, [PipelineNoResponseError] is returned.
//
// Use AsPipelineFunc when the handler body benefits from [gstream.Tap] for
// declarative intermediate observation, [gstream.Apply] for multi-step forge
// function composition, or [gstream.MapErr] for per-step error recovery:
//
//	zeromq.Serve(ctx, sock, oeeHandle,
//	    zeromq.AsPipelineFunc(func(ctx context.Context, req SensorReq) gstream.Stream[OEEResult] {
//	        s  := gstream.Single(ctx, req)
//	        s   = gstream.Apply(ctx, s, validateFn, gstream.ApplyOptions{Observer: obs})
//	        s   = gstream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("valid", "id", v.ID) })
//	        out := gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{Observer: obs})
//	        return gstream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
//	    }),
//	    zeromq.ServeOptions{Observer: obs})
//
// For simple single-step handlers, use a plain fn directly with [Serve].
func AsPipelineFunc[Req, Resp any](
	fn func(context.Context, Req) gstream.Stream[Resp],
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		pipeline := fn(ctx, req)
		vals, errs := gstream.Collect(ctx, pipeline)
		var zero Resp
		if len(errs) > 0 {
			return zero, errs[0]
		}
		if len(vals) == 0 {
			return zero, PipelineNoResponseError{Topic: ""}
		}
		return vals[0], nil
	}
}

// ServeLatestOptions configures [ServeLatest].
type ServeLatestOptions struct {
	// OnError, when non-nil, is called for socket errors ([ServeLatestError]),
	// no-value conditions ([NoLatestValueError]), or encode failures.
	OnError func(error)

	// Observer receives per-request lifecycle events via [stats.Observer.RecordRequest].
	Observer stats.Observer
}

// ServeLatest runs a blocking REP loop that replies to every incoming request
// with the most recently emitted value from src.
//
// A background goroutine reads src.Values and atomically stores each new value.
// When a request arrives but no value has been produced yet, the REP socket
// sends an error reply and opts.OnError is called with [NoLatestValueError].
//
// The Req payload is decoded and validated by handle (standard [Serve] behaviour)
// but is not used to compute the response — the response is always the latest value.
//
// Returns nil when ctx is cancelled, or a [SocketError] on socket failure.
//
// Use ServeLatest for "get current OEE", "get latest sensor reading", or any
// "current state" ZMQ endpoint backed by a continuous stream computation.
func ServeLatest[Req, Resp any](
	ctx context.Context,
	sock FramedSocket,
	handle *reqreply.RouteHandle[Req, Resp],
	src gstream.Stream[Resp],
	opts ServeLatestOptions,
) error {
	var latest atomic.Pointer[Resp]
	go func() {
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
				v2 := v
				latest.Store(&v2)
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				}
			}
		}
	}()

	onErr := opts.OnError
	serveOpts := ServeOptions{Observer: opts.Observer}
	if onErr != nil {
		serveOpts.OnError = func(se ServeError) {
			var nv NoLatestValueError
			if errors.As(se.Err, &nv) {
				onErr(nv)
			} else {
				onErr(se)
			}
		}
	}

	return Serve(ctx, sock, handle, func(ctx context.Context, _ Req) (Resp, error) {
		ptr := latest.Load()
		if ptr == nil {
			var zero Resp
			return zero, fmt.Errorf("%w", NoLatestValueError{Topic: handle.Topic})
		}
		return *ptr, nil
	}, serveOpts)
}
