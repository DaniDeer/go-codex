package stream

import (
	"fmt"
	"log/slog"
)

// StreamDecodeError is sent to [Stream.Errors] by [FromCodec] when a raw payload
// fails codec decode or Refine constraints.
//
// StreamDecodeError implements [slog.LogValuer] for structured logging:
//
//	slog.Warn("decode failed", "error", sde)
//	// → {source:"mqtt/sensors/+", err:{...}}
type StreamDecodeError struct {
	// Source identifies the stream source (from [SourceOptions.Name]).
	Source string

	// Err is the underlying codec error (e.g. [codex.ValidationErrors]).
	Err error
}

func (e StreamDecodeError) Error() string {
	return fmt.Sprintf("stream: decode from %q: %v", e.Source, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the wrapped codec error.
func (e StreamDecodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e StreamDecodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("source", e.Source),
		slog.Any("err", e.Err),
	)
}

// StreamApplyError is sent to [Stream.Errors] by [Apply] when
// [forge.Function.Apply] fails. The inner Err is always a typed forge error
// ([forge.InputError], [forge.OutputError], [forge.ApplyError], etc.) and
// is reachable via [errors.As].
//
// StreamApplyError implements [slog.LogValuer] for structured logging:
//
//	slog.Warn("apply failed", "error", sae)
//	// → {function:"oeeCalc", err:{...}}
type StreamApplyError struct {
	// Function is the forge function name (from [forge.FunctionSpec.Name]).
	Function string

	// Err is the inner forge error. Use errors.As to reach
	// forge.InputError, forge.OutputError, or forge.ApplyError.
	Err error
}

func (e StreamApplyError) Error() string {
	return fmt.Sprintf("stream: apply %q: %v", e.Function, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the inner forge error.
func (e StreamApplyError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e StreamApplyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("function", e.Function),
		slog.Any("err", e.Err),
	)
}

// StreamMapError is sent to [Stream.Errors] by [Map] when the mapping
// function returns an error. Name is the [MapOptions.Name] (default "map").
//
// StreamMapError implements [slog.LogValuer] for structured logging:
//
//	slog.Warn("map failed", "error", sme)
//	// → {name:"buildResult", err:{...}}
type StreamMapError struct {
	// Name identifies the mapping step (from [MapOptions.Name]).
	Name string

	// Err is the error returned by the mapping function.
	Err error
}

func (e StreamMapError) Error() string {
	return fmt.Sprintf("stream: map %q: %v", e.Name, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the inner error.
func (e StreamMapError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e StreamMapError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("err", e.Err),
	)
}
