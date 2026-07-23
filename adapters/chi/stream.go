package chi

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── HandlerLatest / RegisterLatest ───────────────────────────────────────────

// HandlerLatest returns an [http.HandlerFunc] that responds to every request
// with the most recently emitted value from src.
//
// A background goroutine reads src.Values and atomically stores each value.
// On the first request before any value is available, the handler calls
// opts.ErrorHandler with HTTP 503 and [NoLatestValueError].
// Errors from src.Errors are silently dropped — latest value is unaffected.
//
// # Codec coverage — all HTTP layers validated
//
// [Handler] validates all codec layers before fn fires: body codec, query
// params, cookie params, header params, path params, and security. Decoded Req
// and all param values are validated but not used — response is always from src.
// Invalid requests produce the standard 400; only well-formed requests get the cached value.
func HandlerLatest[Req, Resp any](
	handle *rest.RouteHandle[Req, Resp],
	src gstream.Stream[Resp],
	opts Options,
) http.HandlerFunc {
	var latest atomic.Pointer[Resp]
	go func() {
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
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

	wrappedOpts := opts
	wrappedOpts.ErrorHandler = chiRemapStatus(opts.ErrorHandler,
		func(err error) int {
			var nlv NoLatestValueError
			if errors.As(err, &nlv) {
				return http.StatusServiceUnavailable
			}
			return 0
		})
	return Handler(handle, func(ctx context.Context, _ Req) (Resp, error) {
		ptr := latest.Load()
		var zero Resp
		if ptr == nil {
			return zero, NoLatestValueError{Path: handle.Descriptor.Path}
		}
		return *ptr, nil
	}, wrappedOpts)
}

// RegisterLatest wires [HandlerLatest] onto a chi router. Mirrors [Register].
func RegisterLatest[Req, Resp any](
	r gochi.Router,
	handle *rest.RouteHandle[Req, Resp],
	src gstream.Stream[Resp],
	opts Options,
) {
	r.Method(handle.Descriptor.Method, handle.Descriptor.Path,
		HandlerLatest(handle, src, opts))
}

// ── PipelineHandler / RegisterPipeline ───────────────────────────────────────

// PipelineHandlerFunc is a handler function that implements its logic as a
// [gstream.Stream]. It must emit exactly one value (the HTTP response). Use
// [gstream.Single] to wrap the decoded Req as the pipeline source.
//
// Error handling:
//   - If Stream.Errors fires, the first error becomes the HTTP error response.
//   - If no value is produced before ctx is cancelled, [PipelineNoResponseError] is returned.
//   - If the pipeline emits more than one value, only the first is used.
type PipelineHandlerFunc[Req, Resp any] func(ctx context.Context, req Req) gstream.Stream[Resp]

// PipelineHandler wraps a [PipelineHandlerFunc] into an [http.HandlerFunc].
// All codec validation, param validation, security enforcement, and observer
// integration follow the same path as plain [Handler].
//
// Use PipelineHandler when the handler body benefits from [gstream.Tap] for
// declarative intermediate observation, multi-step [gstream.Apply], or
// [gstream.MapErr] for per-step typed error recovery.
//
// # Codec coverage — all HTTP layers
//
// Before fn is called, [Handler] validates body codec, all param codecs
// (query, cookie, header, path), and security. After fn returns, Handler
// validates response body, response header, and response cookie codecs.
//
// To access path/query/cookie/header param VALUES inside the pipeline, call
// [RequestFromContext] on the ctx passed to fn (params are already validated):
//
//	chi.RegisterPipeline(r, handle,
//	    func(ctx context.Context, body SensorBody) stream.Stream[OEEResult] {
//	        sensorID := gochi.URLParam(chi.MustGetRouteContext(ctx), "sensorID")
//	        s := stream.Single(ctx, body)
//	        return stream.Tap(ctx, s, func(v SensorBody) {
//	            slog.Info("request", "sensor", sensorID)
//	        })
//	    }, opts)
func PipelineHandler[Req, Resp any](
	handle *rest.RouteHandle[Req, Resp],
	fn PipelineHandlerFunc[Req, Resp],
	opts Options,
) http.HandlerFunc {
	wrappedOpts := opts
	wrappedOpts.ErrorHandler = chiRemapStatus(opts.ErrorHandler, func(err error) int {
		if status, ok := handle.PipelineErrorStatusFor(err); ok {
			return status
		}
		var pnr PipelineNoResponseError
		if errors.As(err, &pnr) {
			return http.StatusServiceUnavailable
		}
		return 0
	})
	return Handler(handle, func(ctx context.Context, req Req) (Resp, error) {
		pipeline := fn(ctx, req)
		vals, errs := gstream.Collect(ctx, pipeline)
		var zero Resp
		if len(errs) > 0 {
			return zero, errs[0]
		}
		if len(vals) == 0 {
			return zero, PipelineNoResponseError{Path: handle.Descriptor.Path}
		}
		return vals[0], nil
	}, wrappedOpts)
}

// RegisterPipeline wires [PipelineHandler] onto a chi router. Mirrors [Register].
func RegisterPipeline[Req, Resp any](
	r gochi.Router,
	handle *rest.RouteHandle[Req, Resp],
	fn PipelineHandlerFunc[Req, Resp],
	opts Options,
) {
	r.Method(handle.Descriptor.Method, handle.Descriptor.Path,
		PipelineHandler(handle, fn, opts))
}

// ── SSEFromHub ───────────────────────────────────────────────────────────────────

// SSEStreamOptions configures [SSEFromStream] and [SSEFromHub].
// Mirrors [nethttp.SSEStreamOptions].
type SSEStreamOptions struct {
	// Topic is the SSE route path used for observer reporting and error context.
	Topic string

	// OnError, when non-nil, is called for write failures ([SSEWriteError]) and
	// upstream stream errors.
	OnError func(error)

	// Observer receives per-event lifecycle events.
	// [stats.Observer.RecordSubscribe] fires for each emitted event (success=true)
	// or error (success=false). [stats.TraceObserver] spans wrap each send.
	Observer stats.Observer
}

// SSEFromStream returns an [SSEHandlerFunc] where streamFactory is called once
// per connecting SSE client with the decoded Req. Each client gets its own stream.
//
// Use SSEFromStream when each client receives a personalised or filtered stream.
func sseFromStream[Req, Event any](
	streamFactory func(context.Context, Req) gstream.Stream[Event],
	opts SSEStreamOptions,
) SSEHandlerFunc[Req, Event] {
	return func(ctx context.Context, req Req, send func(Event) error) error {
		// Resolve observer per-connection: explicit opts.Observer beats context observer.
		obs := opts.Observer
		if obs == nil {
			obs = stats.ObserverFromContext(ctx)
		}
		src := streamFactory(ctx, req)
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return nil
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				start := time.Now()
				var spanCtx = ctx
				if to, ok2 := obs.(stats.TraceObserver); ok2 {
					spanCtx = to.StartSpan(ctx, "sse.send", opts.Topic)
				}
				sendErr := send(v)
				if to, ok2 := obs.(stats.TraceObserver); ok2 {
					to.EndSpan(spanCtx, sendErr)
				}
				if sendErr != nil {
					obs.RecordSubscribe(opts.Topic, false, time.Since(start))
					we := SSEWriteError{Path: opts.Topic, Err: sendErr}
					if opts.OnError != nil {
						opts.OnError(we)
					}
					return sendErr
				}
				obs.RecordSubscribe(opts.Topic, true, time.Since(start))
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				obs.RecordSubscribe(opts.Topic, false, 0)
				if opts.OnError != nil {
					opts.OnError(e)
				}
			}
		}
		return nil
	}
}

// SSEFromHub returns an [SSEHandlerFunc] backed by a shared [gstream.BroadcastHub].
// All connecting SSE clients share the same event stream.
//
// Use SSEFromHub for live dashboards broadcasting the same stream to all users.
func SSEFromHub[Req, Event any](
	hub *gstream.BroadcastHub[Event],
	opts SSEStreamOptions,
) SSEHandlerFunc[Req, Event] {
	return func(ctx context.Context, req Req, send func(Event) error) error {
		sub := hub.Subscribe()
		defer hub.Unsubscribe(sub)
		return sseFromStream[Req, Event](func(_ context.Context, _ Req) gstream.Stream[Event] {
			return sub
		}, opts)(ctx, req, send)
	}
}

// ── internal helpers ──────────────────────────────────────────────────────────

func chiRemapStatus(
	base func(http.ResponseWriter, *http.Request, int, error),
	classifier func(error) int,
) func(http.ResponseWriter, *http.Request, int, error) {
	if base == nil {
		base = defaultErrorHandler
	}
	return func(w http.ResponseWriter, r *http.Request, status int, err error) {
		if override := classifier(err); override != 0 {
			status = override
		}
		base(w, r, status, err)
	}
}
