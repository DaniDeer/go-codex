package nethttp

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── HandlerLatest / RegisterLatest ───────────────────────────────────────────

// HandlerLatest returns an [http.Handler] that responds to every request with
// the most recently emitted value from src.
//
// A background goroutine reads src.Values and atomically stores each value.
// On the first request before any value is available, the handler calls
// opts.ErrorHandler with HTTP 503 and [NoLatestValueError].
// Errors from src.Errors are reported to opts.Observer but do not affect responses.
//
// Use HandlerLatest for "get current OEE", "get latest sensor reading", or any
// "current state" REST endpoint backed by a continuously running stream pipeline.
//
// # Codec coverage — all HTTP layers validated
//
// [Handler] validates all codec layers before the fn fires: body codec, query
// params, cookie params, header params, path params, and security. The decoded
// [Req] value and all param values are validated but not used for computation —
// the response is always the latest stream value. This ensures only well-formed
// requests receive a cached response; invalid requests produce the standard 400.
func HandlerLatest[Req, Resp any](
	handle *rest.RouteHandle[Req, Resp],
	src gstream.Stream[Resp],
	opts Options,
) http.Handler {
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
				// errors from src are silently dropped
			}
		}
	}()

	// Wrap opts.ErrorHandler so that NoLatestValueError maps to 503.
	wrappedOpts := opts
	wrappedOpts.ErrorHandler = remapStatus(opts.ErrorHandler,
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

// RegisterLatest wires [HandlerLatest] onto mux using the route's method and
// path. Mirrors [Register].
func RegisterLatest[Req, Resp any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[Req, Resp],
	src gstream.Stream[Resp],
	opts Options,
) {
	mux.Handle(handle.Descriptor.Method+" "+handle.Descriptor.Path,
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

// PipelineHandler wraps a [PipelineHandlerFunc] into an [http.Handler].
// All codec validation, param validation, security enforcement, and observer
// integration follow the same path as plain [Handler] — PipelineHandler is a
// thin wrapper that adapts the function signature and collects the result via
// [gstream.Collect].
//
// Use PipelineHandler when the handler body benefits from:
//   - [gstream.Tap] for declarative intermediate observation (log/metrics/audit)
//   - [gstream.Apply] for multi-step forge function composition
//   - [gstream.MapErr] for per-step typed error recovery
//
// For simple one-step handlers, use plain [Handler] for lower overhead.
//
// # Codec coverage — all HTTP layers
//
// Before fn is called, [Handler] has already validated and decoded:
//   - Request body (→ req Req)
//   - Query, cookie, header, path params (all registered [rest.Param] codecs)
//   - Security credentials + SecurityFunc
//
// After fn returns, [Handler] validates:
//   - Response body (handle.Encode)
//   - Response header and cookie params (ValidateResponseHeaders / ValidateResponseCookies)
//
// # Accessing path/query/cookie/header param values inside the pipeline
//
// The decoded [Req] value (body) is passed directly to fn. To access path,
// query, cookie, or header param values inside the pipeline (already codec-
// validated by [Handler]), call [RequestFromContext] on the ctx passed to fn:
//
//	nethttp.RegisterPipeline(mux, handle,
//	    func(ctx context.Context, body SensorBody) stream.Stream[OEEResult] {
//	        r, _ := nethttp.RequestFromContext(ctx)
//	        sensorID := r.PathValue("sensorID") // already validated
//	        s := stream.Single(ctx, body)
//	        s = stream.Tap(ctx, s, func(v SensorBody) {
//	            slog.Info("request", "sensor", sensorID, "value", v.Value)
//	        })
//	        return stream.Apply(ctx, s, oeeCalcFn, opts)
//	    }, opts)
//
// # Response headers and cookies inside the pipeline
//
// Call [WithResponseHeaders] or [WithResponseCookies] anywhere inside the
// pipeline fn (including within [gstream.Tap] or forge functions). The maps
// are reference types stored in ctx — writes in the pipeline goroutines are
// visible to [Handler] after [gstream.Collect] returns. This is safe for
// sequential pipelines (Single → Apply chain). Parallel pipelines that write
// to response headers concurrently should use a mutex or avoid this pattern.
func PipelineHandler[Req, Resp any](
	handle *rest.RouteHandle[Req, Resp],
	fn PipelineHandlerFunc[Req, Resp],
	opts Options,
) http.Handler {
	wrappedOpts := opts
	wrappedOpts.ErrorHandler = remapStatus(opts.ErrorHandler, func(err error) int {
		if status, ok := handle.ErrorStatusFor(err); ok {
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
		// Errors take precedence.
		if len(errs) > 0 {
			return zero, errs[0]
		}
		if len(vals) == 0 {
			return zero, PipelineNoResponseError{Path: handle.Descriptor.Path}
		}
		return vals[0], nil // multiple values: only first used; extras silently discarded
	}, wrappedOpts)
}

// RegisterPipeline wires [PipelineHandler] onto mux. Mirrors [Register].
func RegisterPipeline[Req, Resp any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[Req, Resp],
	fn PipelineHandlerFunc[Req, Resp],
	opts Options,
) {
	mux.Handle(handle.Descriptor.Method+" "+handle.Descriptor.Path,
		PipelineHandler(handle, fn, opts))
}

// ── SSEFromHub ───────────────────────────────────────────────────────────────────

// SSEStreamOptions configures [SSEFromStream] and [SSEFromHub].
type SSEStreamOptions struct {
	// Topic is the SSE route path used for observer reporting and error context.
	// Set this to handle.Descriptor.Path when wiring via SSEHandler/RegisterSSE.
	Topic string

	// OnError, when non-nil, is called for write failures ([SSEWriteError]) and
	// any errors forwarded from the upstream stream.
	OnError func(error)

	// Observer receives per-event lifecycle events.
	// [stats.Observer.RecordSubscribe] is called with success=true on each
	// emitted event and success=false on write or stream errors.
	// [stats.TraceObserver] spans wrap each send attempt when implemented.
	Observer stats.Observer
}

// sseFromStream returns an SSEHandlerFunc where streamFactory is called once
// per connecting SSE client with the decoded Req. The resulting
// [gstream.Stream] is consumed for that connection only.
//
// Use SSEFromStream when each client receives a personalised or filtered stream:
//
//	nethttp.RegisterSSE(mux, dashboardRoute,
//	    nethttp.SSEFromStream(func(_ context.Context, req DashboardReq) gstream.Stream[OEEResult] {
//	        return stream.Filter(ctx, sharedOEEStream, req.MatchesMachine)
//	    }, nethttp.SSEStreamOptions{Topic: dashboardRoute.Descriptor.Path, Observer: obs}),
//	    nethttp.Options{Observer: obs})
//
// When the client disconnects, ctx is cancelled and the returned fn exits,
// terminating the per-connection pipeline.
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
					return sendErr // client disconnected — terminate
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
// Each connecting client subscribes to the hub and receives items from that
// moment forward; subscriptions are cleaned up on disconnect.
//
// Use SSEFromHub for live dashboards broadcasting the same stream to all users:
//
//	hub := stream.NewBroadcastHub(ctx, oeeStream, 32)
//	nethttp.RegisterSSE(mux, dashboardRoute,
//	    nethttp.SSEFromHub[struct{}, OEEResult](hub,
//	        nethttp.SSEStreamOptions{Topic: dashboardRoute.Descriptor.Path, Observer: obs}),
//	    nethttp.Options{Observer: obs})
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

// remapStatus returns an ErrorHandler that overrides the HTTP status code when
// classifier returns non-zero, then delegates to base (or defaultErrorHandler).
func remapStatus(
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
