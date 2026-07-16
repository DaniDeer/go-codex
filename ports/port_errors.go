package ports

import (
	"fmt"
	"log/slog"
)

// PortBindError is returned by [SourcePort.Bind] or [IOPort.Bind] when adapter
// activation fails (e.g. broker subscription rejected, mux route conflict), or
// when [IOPort.Bind] is called more than once on the same port.
type PortBindError struct {
	// Port is the name passed to [NewSourcePort] / [NewIOPort].
	Port string
	// Adapter is the adapter descriptor (e.g. "mqtt5.SubscribeAdapter").
	Adapter string
	// Err is the underlying error.
	Err error
}

func (e PortBindError) Error() string {
	return fmt.Sprintf("port %q bind (%s): %v", e.Port, e.Adapter, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e PortBindError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e PortBindError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", e.Port),
		slog.String("adapter", e.Adapter),
		slog.Any("err", e.Err),
	)
}

// PortNoAdapterError is returned by [IOPort.Connect] when no adapter has been
// bound via [IOPort.Bind]. This is a programming error — bind an [IOAdapter]
// before starting the pipeline.
type PortNoAdapterError struct {
	// Port is the name passed to [NewIOPort].
	Port string
}

func (e PortNoAdapterError) Error() string {
	return fmt.Sprintf("port %q: no adapter bound — call Bind before Connect", e.Port)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e PortNoAdapterError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("port", e.Port))
}

// PortNoPipelineError is returned by [ToolPort.Bind] when [ToolPort.SetPipeline]
// was not called before Bind. This is a programming error — set the pipeline
// function before binding to any transport.
type PortNoPipelineError struct {
	// Port is the name passed to [NewToolPort].
	Port string
}

func (e PortNoPipelineError) Error() string {
	return fmt.Sprintf("port %q: no pipeline set — call SetPipeline before Bind", e.Port)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e PortNoPipelineError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("port", e.Port))
}

// PortNotStartedError is returned by [SinkPort.Push] before [SinkPort.Start],
// after [SinkPort.Close], or when the port is Feed-driven — the stream-fed
// (Feed) and request-fed (Start/Push/Close) lifecycles are mutually exclusive.
type PortNotStartedError struct {
	// Port is the name passed to [NewSinkPort].
	Port string
	// Op is the rejected call ("push").
	Op string
}

func (e PortNotStartedError) Error() string {
	return fmt.Sprintf("port %q: %s rejected — port not started (call Start before Push; not after Close or on a Feed-driven port)", e.Port, e.Op)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e PortNotStartedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", e.Port),
		slog.String("op", e.Op),
	)
}
