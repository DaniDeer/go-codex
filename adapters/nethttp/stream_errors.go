package nethttp

import (
	"fmt"
	"log/slog"
)

// ── HTTP stream bridge errors ─────────────────────────────────────────────────

// NoLatestValueError is passed to [Options.ErrorHandler] (status 503) by
// [HandlerLatest] when the background stream has not yet produced a value.
//
// Use [errors.As] to distinguish this from other handler errors:
//
//	opts.ErrorHandler = func(w http.ResponseWriter, r *http.Request, status int, err error) {
//	    var nlv nethttp.NoLatestValueError
//	    if errors.As(err, &nlv) {
//	        http.Error(w, "service warming up", http.StatusServiceUnavailable)
//	        return
//	    }
//	    // default handling …
//	}
type NoLatestValueError struct {
	// Path is the route path (from RouteHandle.Descriptor.Path).
	Path string
}

func (e NoLatestValueError) Error() string {
	return fmt.Sprintf("http latest %s: no value yet", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoLatestValueError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("path", e.Path))
}

// PipelineFullError is passed to [Options.ErrorHandler] (status 503) by
// [HandlerIngest] when the destination channel is full and the incoming request
// cannot be enqueued without blocking.
//
// The Capacity field reports cap(dst) to help callers tune buffer sizing.
type PipelineFullError struct {
	// Path is the route path (from RouteHandle.Descriptor.Path).
	Path string
	// Capacity is cap(dst) at the time of the rejection.
	Capacity int
}

func (e PipelineFullError) Error() string {
	return fmt.Sprintf("http ingest %s: pipeline full (capacity %d)", e.Path, e.Capacity)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e PipelineFullError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Int("capacity", e.Capacity),
	)
}

// PipelineNoResponseError is returned by [PipelineHandler] when
// [stream.Collect] returns with no values — either the pipeline emitted nothing,
// or the request context was cancelled before the pipeline produced a result.
type PipelineNoResponseError struct {
	// Path is the route path (from RouteHandle.Descriptor.Path).
	Path string
}

func (e PipelineNoResponseError) Error() string {
	return fmt.Sprintf("http pipeline %s: no response produced", e.Path)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e PipelineNoResponseError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("path", e.Path))
}

// SSEWriteError is passed to [SSEStreamOptions.OnError] by [SSEFromStream] and
// [SSEFromHub] when writing a server-sent event to the HTTP response fails —
// typically because the client disconnected.
type SSEWriteError struct {
	// Path is the route path (from SSERouteHandle.Descriptor.Path).
	Path string
	// Err is the underlying write error.
	Err error
}

func (e SSEWriteError) Error() string {
	return fmt.Sprintf("http sse write %s: %v", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e SSEWriteError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SSEWriteError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// SSEConnectError is sent to opts.OnError by [CallSSEAdapter] when an
// HTTP connection attempt to the SSE endpoint fails. Consumption retries
// after backoff; this error is informational per reconnect attempt.
type SSEConnectError struct {
	// URL is the SSE endpoint URL.
	URL string
	// Attempt is the 1-based reconnect attempt number.
	Attempt int
	// Err is the underlying connection error (network, TLS, non-200 status, etc.).
	Err error
}

func (e SSEConnectError) Error() string {
	return fmt.Sprintf("http sse connect %s (attempt %d): %v", e.URL, e.Attempt, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e SSEConnectError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SSEConnectError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("url", e.URL),
		slog.Int("attempt", e.Attempt),
		slog.Any("err", e.Err),
	)
}

// SSEParseError is sent to opts.OnError by [CallSSEAdapter] when an SSE
// data line cannot be decoded using the route's event codec — malformed
// JSON, failed codec validation, or other decode failure. Consumption
// continues; only the one failing event is dropped.
type SSEParseError struct {
	// URL is the SSE endpoint URL.
	URL string
	// Line is the raw SSE "data:" line that failed (without the "data: " prefix).
	Line string
	// Err is the underlying decode error.
	Err error
}

func (e SSEParseError) Error() string {
	return fmt.Sprintf("http sse parse %s: %v", e.URL, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e SSEParseError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SSEParseError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("url", e.URL),
		slog.String("line", e.Line),
		slog.Any("err", e.Err),
	)
}

// SSEHandlerError wraps a per-event handler error for consumeSSE's
// internal fn callback — mirrors
// [mqtt5.SubscribeError]/[zeromq.SubscribeError]'s existing
// handler-error-is-non-fatal convention. Consumption continues with the
// next event. Never occurs for [CallSSEAdapter] — its internal fn (a
// channel push) never returns an error; [rest.Client.Consume] also never
// surfaces it (no OnError hook — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4). Retained for the
// shape's own documentation/errors.As completeness.
type SSEHandlerError struct {
	// URL is the SSE endpoint URL.
	URL string
	// Err is fn's returned error.
	Err error
}

func (e SSEHandlerError) Error() string {
	return fmt.Sprintf("http sse handler %s: %v", e.URL, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e SSEHandlerError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SSEHandlerError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("url", e.URL),
		slog.Any("err", e.Err),
	)
}
