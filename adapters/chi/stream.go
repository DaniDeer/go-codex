package chi

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
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

// ── HandlerIngest / RegisterIngest ───────────────────────────────────────────

// HandlerIngest returns an [http.HandlerFunc] that decodes and validates each
// incoming request body, then writes the decoded value to dst without blocking.
//
// If dst is full (non-blocking send fails), the handler calls opts.ErrorHandler
// with HTTP 503 and [PipelineFullError].
//
// # Codec coverage
//
// All HTTP codec layers are validated before the item is pushed: body codec,
// query params, cookie params, header params, path params, and security.
// Only the body-decoded [Req] value is pushed to dst. Path, query, cookie, and
// header param VALUES (though validated) are NOT included in the channel item.
// For routes where param values must accompany the body, use [Handler] directly:
//
//	r.Method("POST", "/sensors/{sensorID}/readings", http.HandlerFunc(
//	    chi.Handler(handle, func(ctx context.Context, body SensorBody) (struct{}, error) {
//	        sensorID := gochi.URLParam(r, "sensorID") // already validated
//	        select {
//	        case dst <- SensorReading{SensorID: sensorID, Value: body.Value}:
//	            return struct{}{}, nil
//	        default:
//	            return struct{}{}, chi.PipelineFullError{Path: "/...", Capacity: cap(dst)}
//	        }
//	    }, opts)))
//
// The caller owns dst — HandlerIngest never closes it.
func HandlerIngest[Req any](
	handle *rest.RouteHandle[Req, struct{}],
	dst chan<- Req,
	opts Options,
) http.HandlerFunc {
	wrappedOpts := opts
	wrappedOpts.ErrorHandler = chiRemapStatus(opts.ErrorHandler,
		func(err error) int {
			var pfe PipelineFullError
			if errors.As(err, &pfe) {
				return http.StatusServiceUnavailable
			}
			return 0
		})
	return Handler(handle, func(_ context.Context, req Req) (struct{}, error) {
		select {
		case dst <- req:
			return struct{}{}, nil
		default:
			return struct{}{}, PipelineFullError{
				Path:     handle.Descriptor.Path,
				Capacity: cap(dst),
			}
		}
	}, wrappedOpts)
}

// RegisterIngest wires [HandlerIngest] onto a chi router. Mirrors [Register].
func RegisterIngest[Req any](
	r gochi.Router,
	handle *rest.RouteHandle[Req, struct{}],
	dst chan<- Req,
	opts Options,
) {
	r.Method(handle.Descriptor.Method, handle.Descriptor.Path,
		HandlerIngest(handle, dst, opts))
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
	}, opts)
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
