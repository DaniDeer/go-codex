package nethttp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/format"
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

// ── HandlerIngest / RegisterIngest ───────────────────────────────────────────

// HandlerIngest returns an [http.Handler] that decodes and validates each
// incoming request body, then writes the decoded value to dst without blocking.
//
// If dst is full (non-blocking send fails), the handler calls opts.ErrorHandler
// with HTTP 503 and [PipelineFullError]. Codec validation failures are handled
// by the standard [Handler] machinery (HTTP 400).
//
// The response is always the route's configured 2xx status with a JSON {} body
// (struct{} response). Configure the route with a 202 Accepted response:
//
//	ingestHandle, _ := rest.NewRoute[SensorReading, struct{}]("POST", "/ingest",
//	    readingCodec, codex.Struct[struct{}](), rest.RouteMeta{}).Register(b)
//
// # Codec coverage
//
// All HTTP codec layers are validated before the item is pushed to dst: body
// codec, query params, cookie params, header params, path params, and security.
// Validation errors produce the standard HTTP 400/401 responses.
//
// However, only the body-decoded [Req] value is pushed to dst. Path, query,
// cookie, and header param VALUES (though validated) are NOT included in the
// channel item. For routes where param values must accompany the body (e.g.
// POST /sensors/{sensorID}/readings where sensorID must reach the pipeline),
// use [Handler] directly with a custom [HandlerFunc] that calls
// [RequestFromContext] to extract path values:
//
//	nethttp.Register(mux, ingestHandle, func(ctx context.Context, body SensorBody) (struct{}, error) {
//	    r, _ := nethttp.RequestFromContext(ctx)
//	    sensorID := r.PathValue("sensorID") // already codec-validated by Handler
//	    select {
//	    case dst <- SensorReading{SensorID: sensorID, Value: body.Value}:
//	        return struct{}{}, nil
//	    default:
//	        return struct{}{}, nethttp.PipelineFullError{Path: handle.Descriptor.Path, Capacity: cap(dst)}
//	    }
//	}, opts)
//
// The caller owns dst — HandlerIngest never closes it.
func HandlerIngest[Req any](
	handle *rest.RouteHandle[Req, struct{}],
	dst chan<- Req,
	opts Options,
) http.Handler {
	// Wrap opts.ErrorHandler so that PipelineFullError maps to 503.
	wrappedOpts := opts
	wrappedOpts.ErrorHandler = remapStatus(opts.ErrorHandler,
		func(err error) int {
			var pfe PipelineFullError
			if errors.As(err, &pfe) {
				return http.StatusServiceUnavailable
			}
			return 0
		})
	return Handler(handle, handlerIngestFn(handle, dst), wrappedOpts)
}

func handlerIngestFn[Req any](handle *rest.RouteHandle[Req, struct{}], dst chan<- Req) HandlerFunc[Req, struct{}] {
	return func(_ context.Context, req Req) (struct{}, error) {
		select {
		case dst <- req:
			return struct{}{}, nil
		default:
			return struct{}{}, PipelineFullError{
				Path:     handle.Descriptor.Path,
				Capacity: cap(dst),
			}
		}
	}
}

// RegisterIngest wires [HandlerIngest] onto mux. Mirrors [Register].
func RegisterIngest[Req any](
	mux *http.ServeMux,
	handle *rest.RouteHandle[Req, struct{}],
	dst chan<- Req,
	opts Options,
) {
	mux.Handle(handle.Descriptor.Method+" "+handle.Descriptor.Path,
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
	}, opts)
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

// ── SSEFromStream / SSEFromHub ────────────────────────────────────────────────

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

// SSEFromStream returns an [SSEHandlerFunc] where streamFactory is called once
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
func SSEFromStream[Req, Event any](
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
		return SSEFromStream[Req, Event](func(_ context.Context, _ Req) gstream.Stream[Event] {
			return sub
		}, opts)(ctx, req, send)
	}
}

// ── PollStream / DrainCall ────────────────────────────────────────────────────

// PollStreamOptions configures [PollStream].
type PollStreamOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's
	// path template via [rest.RouteHandle.BuildPath]. The same map is used for
	// every poll (static path vars only). For routes with no path params, omit Vars.
	// For per-poll path var substitution, use [gstream.Drain] with [Call] directly.
	Vars map[string]string

	// Observer receives per-poll lifecycle events via the internal [Call] call.
	// [stats.Observer.RecordRequest] fires for every poll attempt.
	Observer stats.Observer

	// Buffer is the output stream channel buffer size. Default 0.
	Buffer int
}

// PollStream polls handle at interval by calling [Call] with req and emitting
// each response to the returned [gstream.Stream]. Call errors go to
// [gstream.Stream.Errors] as-is (all typed: [UnexpectedStatusError], etc.).
// The stream terminates when ctx is cancelled.
//
// Use PollStream to turn a periodic REST fetch into a continuous stream source.
func PollStream[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	req Req,
	interval time.Duration,
	opts PollStreamOptions,
) gstream.Stream[Resp] {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	values := make(chan Resp, opts.Buffer)
	errs := make(chan error, opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				resp, err := Call(ctx, client, baseURL, handle, req, opts.Vars,
					CallOptions{Observer: obs})
				if err != nil {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[Resp]{Values: values, Errors: errs}
}

// DrainCallOptions configures [DrainCall].
type DrainCallOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's
	// path template via [rest.RouteHandle.BuildPath]. The same map is applied
	// to every item (static path vars only). For routes with no path params,
	// omit Vars. For per-item path var substitution (e.g. {sensorID} from each
	// payload), use [gstream.Drain] with [Call] directly.
	Vars map[string]string

	// OnError, when non-nil, is called for [Call] errors and upstream stream errors.
	OnError func(error)

	// CallOpts are passed to [Call] for each item — including Observer, credential
	// func, and param maps. [stats.Observer.RecordRequest] fires for every call.
	CallOpts CallOptions
}

// DrainCall posts each item from src to handle using [Call], discarding the
// response. Call errors and upstream stream errors are forwarded to opts.OnError.
// Blocks until src terminates or ctx is cancelled.
func DrainCall[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	src gstream.Stream[Req],
	opts DrainCallOptions,
) {
	onErr := opts.OnError
	gstream.Drain(ctx, src,
		func(ctx context.Context, item Req) error {
			if _, err := Call(ctx, client, baseURL, handle, item, opts.Vars, opts.CallOpts); err != nil {
				if onErr != nil {
					onErr(err)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{Observer: opts.CallOpts.Observer},
	)
}

// ── CallStream ────────────────────────────────────────────────────────────────

// CallStreamOptions configures [CallStream].
type CallStreamOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the route's
	// path template via [rest.RouteHandle.BuildPath]. The same map is used for
	// every request (static path vars only). For routes with no path vars,
	// omit Vars. For per-item path var substitution, encode the path variable
	// into the Req body codec and use a route without path params.
	Vars map[string]string

	// CallOpts are forwarded to [Call] for each item — including Observer,
	// CredentialFunc, QueryParams, CookieParams, HeaderParams, and ExtraHeaders.
	// [stats.Observer.RecordRequest] fires for every call attempt.
	// If CallOpts.Observer is nil, the observer is resolved from ctx.
	CallOpts CallOptions

	// Buffer is the output Stream channel buffer size. Default 0.
	Buffer int
}

// CallStream sends each request item from src to handle using [Call], emitting
// each decoded response to the returned [gstream.Stream]. This is the HTTP
// equivalent of [zeromq.CallStream] and [mqtt5.CallStream] — a declarative
// intermediate I/O step that keeps forge functions pure (no nethttp.Call inside
// a forge function body).
//
// All codec validation runs per item via [Call]: path vars, query/cookie/header
// params, security credentials, request body encode, response body decode.
// Call errors ([UnexpectedStatusError], [RequestError], [ResponseBodyError], etc.)
// are sent to [gstream.Stream.Errors]. The stream terminates when src closes or
// ctx is cancelled.
//
// Requests are issued sequentially. Use multiple parallel pipelines feeding the
// same handle for concurrent throughput.
//
// Usage:
//
//	enrichHandle, _ := rest.NewRoute[IntermediaryData, EnrichedData](
//	    "POST", "/enrich", intermediaryCodec, enrichedCodec, rest.RouteMeta{},
//	).Register(b)
//
//	enriched := nethttp.CallStream(ctx, httpClient, "http://enrichment-svc:8080",
//	    enrichHandle, intermediaryStream,
//	    nethttp.CallStreamOptions{CallOpts: nethttp.CallOptions{Observer: obs}})
func CallStream[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	src gstream.Stream[Req],
	opts CallStreamOptions,
) gstream.Stream[Resp] {
	values := make(chan Resp, opts.Buffer)
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
			case req, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				resp, err := Call(ctx, client, baseURL, handle, req, opts.Vars, opts.CallOpts)
				if err != nil {
					select {
					case errs <- err:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case values <- resp:
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
	return gstream.Stream[Resp]{Values: values, Errors: errs}
}

// ── SSEClientStream ───────────────────────────────────────────────────────────

// SSEClientOptions configures [SSEClientStream].
type SSEClientOptions struct {
	// Vars, when non-nil, substitutes {varName} placeholders in the SSE route's
	// path template via [rest.SSERouteHandle.BuildPath]. The same map is used
	// for every reconnect attempt (static path vars only). For SSE routes with
	// no path params, omit Vars.
	Vars map[string]string

	// RetryDelay is the initial reconnect wait after a dropped connection (default 1s).
	RetryDelay time.Duration
	// MaxRetryDelay caps the exponential backoff (default 30s).
	MaxRetryDelay time.Duration

	// Observer receives per-connect lifecycle events:
	//   [stats.Observer.RecordRequest] is called for every connect attempt.
	//   [stats.TraceObserver] spans wrap each connection session.
	Observer stats.Observer

	// Buffer is the output stream channel buffer size. Default 0.
	Buffer int
}

// SSEClientStream connects to an SSE endpoint and emits each decoded event as a
// stream item. The stream reconnects automatically with exponential backoff when
// the connection drops. It terminates when ctx is cancelled.
//
// Connection failures are sent to Stream.Errors as [SSEConnectError].
// Data line decode failures are sent to Stream.Errors as [SSEParseError].
//
// Use SSEClientStream to consume a server-sent events feed from another service:
//
//	events := nethttp.SSEClientStream(ctx, httpClient, baseURL, eventHandle,
//	    format.JSON(eventCodec),
//	    nethttp.SSEClientOptions{RetryDelay: 2*time.Second, Observer: obs})
//	oeeStream := stream.Apply(ctx, events, oeeCalcFn, opts)
func SSEClientStream[Req, Event any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.SSERouteHandle[Req, Event],
	fmt format.Format[Event],
	opts SSEClientOptions,
) gstream.Stream[Event] {
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = time.Second
	}
	if opts.MaxRetryDelay <= 0 {
		opts.MaxRetryDelay = 30 * time.Second
	}
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	values := make(chan Event, opts.Buffer)
	errs := make(chan error, opts.Buffer)

	go func() {
		defer close(values)
		defer close(errs)

		path, pathErr := handle.BuildPath(opts.Vars)
		if pathErr != nil {
			select {
			case errs <- SSEConnectError{URL: baseURL, Attempt: 1, Err: pathErr}:
			default:
			}
			return
		}
		url := baseURL + path
		delay := opts.RetryDelay
		attempt := 0

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			attempt++
			start := time.Now()

			req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if reqErr != nil {
				obs.RecordRequest(http.MethodGet, handle.Descriptor.Path, 0, time.Since(start))
				select {
				case errs <- SSEConnectError{URL: url, Attempt: attempt, Err: reqErr}:
				case <-ctx.Done():
					return
				}
				return // context error — no retry
			}
			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Cache-Control", "no-cache")

			resp, doErr := client.Do(req)
			if doErr != nil {
				obs.RecordRequest(http.MethodGet, handle.Descriptor.Path, 0, time.Since(start))
				select {
				case errs <- SSEConnectError{URL: url, Attempt: attempt, Err: doErr}:
				case <-ctx.Done():
					return
				}
			} else if resp.StatusCode != http.StatusOK {
				_ = resp.Body.Close()
				obs.RecordRequest(http.MethodGet, handle.Descriptor.Path, resp.StatusCode, time.Since(start))
				connErr := SSEConnectError{URL: url, Attempt: attempt,
					Err: fmt2.Errorf("unexpected status %d", resp.StatusCode)}
				select {
				case errs <- connErr:
				case <-ctx.Done():
					return
				}
			} else {
				// Connected — read events until disconnect
				obs.RecordRequest(http.MethodGet, handle.Descriptor.Path, resp.StatusCode, time.Since(start))
				disconnect := sseReadEvents(ctx, resp.Body, url, fmt, obs, values, errs)
				_ = resp.Body.Close()
				if !disconnect {
					return // ctx cancelled
				}
				// Connection dropped — retry after backoff
				delay = resetDelay(delay, opts.RetryDelay, opts.MaxRetryDelay)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
				continue
			}

			// Back-off before retry
			delay = resetDelay(delay, opts.RetryDelay, opts.MaxRetryDelay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
	}()
	return gstream.Stream[Event]{Values: values, Errors: errs}
}

// sseReadEvents reads SSE data lines from body, decodes each, and sends to
// values/errs. Returns true if the connection dropped (caller should retry),
// false if ctx was cancelled.
func sseReadEvents[Event any](
	ctx context.Context,
	body io.Reader,
	url string,
	fmt format.Format[Event],
	obs stats.Observer,
	values chan<- Event,
	errs chan<- error,
) bool {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue // skip comment/retry/id lines
		}
		data := strings.TrimPrefix(line, "data: ")
		event, decErr := fmt.Unmarshal([]byte(data))
		if decErr != nil {
			stats.ReportErrors(obs, "sse_data", decErr)
			select {
			case errs <- SSEParseError{URL: url, Line: data, Err: decErr}:
			case <-ctx.Done():
				return false
			}
			continue
		}
		select {
		case values <- event:
		case <-ctx.Done():
			return false
		}
	}
	return true // scanner.Scan() returned false → connection dropped
}

func resetDelay(current, initial, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		next = max
	}
	if next < initial {
		next = initial
	}
	return next
}

// fmt2 is an alias for the standard fmt package to avoid shadowing the fmt
// parameter in SSEClientStream.
var fmt2 = struct{ Errorf func(string, ...any) error }{
	Errorf: fmt.Errorf,
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
