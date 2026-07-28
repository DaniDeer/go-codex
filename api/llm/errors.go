package llm

import (
	"fmt"
	"log/slog"
)

// SystemPromptFileError is returned by [Call.Register]/[Call.ClientHandle]
// when [SystemPromptFile]'s path cannot be read.
type SystemPromptFileError struct {
	// Path is the file path passed to [SystemPromptFile].
	Path string
	// Err is the underlying os.ReadFile error.
	Err error
}

func (e SystemPromptFileError) Error() string {
	return fmt.Sprintf("llm: read system prompt file %q: %v", e.Path, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e SystemPromptFileError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e SystemPromptFileError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("err", e.Err),
	)
}

// ResponseDecodeError is returned by [CallHandle.DecodeResponse] when the raw
// LLM completion content fails to decode/validate through the response codec
// — a JSON syntax error, a type mismatch, or a Refine constraint failure.
//
// This is the error [adapters/openai]'s MaxRetries loop inspects to build the
// re-prompt message: the failure means the provider's own structured-outputs
// enforcement did not (or could not) guarantee a codec-valid response —
// belt-and-suspenders local validation caught what the JSON Schema alone
// could not (e.g. cross-field Refine constraints).
type ResponseDecodeError struct {
	// Name is the Call's name, as passed to [NewCall].
	Name string
	// Raw is the raw completion content that failed to decode.
	Raw []byte
	// Err is the underlying codec Decode error.
	Err error
}

func (e ResponseDecodeError) Error() string {
	return fmt.Sprintf("llm: call %q: decode response: %v", e.Name, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to reach the underlying error.
func (e ResponseDecodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ResponseDecodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("raw", string(e.Raw)),
		slog.Any("err", e.Err),
	)
}
