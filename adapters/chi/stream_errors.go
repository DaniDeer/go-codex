package chi

import (
	"fmt"
	"log/slog"
)

// ── HTTP stream bridge errors ─────────────────────────────────────────────────

// NoLatestValueError is passed to [Options.ErrorHandler] (status 503) by
// [HandlerLatest] when the background stream has not yet produced a value.
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
// [HandlerIngest] when the destination channel is full and the incoming
// request cannot be enqueued without blocking.
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
// [stream.Collect] returns with no values.
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
// [SSEFromHub] when writing a server-sent event to the HTTP response fails.
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
