package openai

import (
	"fmt"
	"log/slog"
)

// RequestBuildError is returned by [CallAdapter] when constructing the
// outgoing *http.Request fails (e.g. malformed BaseURL or context already
// cancelled). Mirrors [nethttp.RequestBuildError].
type RequestBuildError struct {
	// Name is the [llm.Call]'s name, as passed to [llm.NewCall] — lets
	// callers disambiguate which declared Call failed when multiple Calls
	// share the same Model.
	Name string
	// Err is the underlying error from [http.NewRequestWithContext].
	Err error
}

func (e RequestBuildError) Error() string {
	return fmt.Sprintf("openai: call %q: build request: %s", e.Name, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RequestBuildError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RequestBuildError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("cause", e.Err),
	)
}

// RequestError is returned by [CallAdapter] when executing the HTTP call
// fails (network error, DNS failure, TLS error, or context cancellation).
// Mirrors [nethttp.RequestError].
type RequestError struct {
	// Name is the [llm.Call]'s name, as passed to [llm.NewCall] — lets
	// callers disambiguate which declared Call failed when multiple Calls
	// share the same Model.
	Name string
	// Model is the model identifier the request was made against.
	Model string
	// Err is the underlying transport error from [http.Client.Do].
	Err error
}

func (e RequestError) Error() string {
	return fmt.Sprintf("openai: call %q: model %q: %s", e.Name, e.Model, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RequestError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RequestError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("model", e.Model),
		slog.Any("cause", e.Err),
	)
}

// UnexpectedStatusError is returned by [CallAdapter] when the provider
// responds with a non-2xx HTTP status code. Mirrors
// [nethttp.UnexpectedStatusError].
type UnexpectedStatusError struct {
	// Name is the [llm.Call]'s name, as passed to [llm.NewCall] — lets
	// callers disambiguate which declared Call failed when multiple Calls
	// share the same Model.
	Name string
	// Model is the model identifier the request was made against.
	Model string
	// StatusCode is the HTTP response status code returned by the provider.
	StatusCode int
	// Body is the raw response body returned by the provider (may be empty).
	Body string
}

func (e UnexpectedStatusError) Error() string {
	return fmt.Sprintf("openai: call %q: model %q: unexpected status %d", e.Name, e.Model, e.StatusCode)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnexpectedStatusError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("model", e.Model),
		slog.Int("status", e.StatusCode),
	)
}

// ResponseBodyError is returned by [CallAdapter] when reading the HTTP
// response body fails after a successful connection. Mirrors
// [nethttp.ResponseBodyError].
type ResponseBodyError struct {
	// Name is the [llm.Call]'s name, as passed to [llm.NewCall] — lets
	// callers disambiguate which declared Call failed when multiple Calls
	// share the same Model.
	Name string
	// Err is the underlying error from reading the response body.
	Err error
}

func (e ResponseBodyError) Error() string {
	return fmt.Sprintf("openai: call %q: read response body: %s", e.Name, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e ResponseBodyError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ResponseBodyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.Any("cause", e.Err),
	)
}

// NoChoicesError is returned by [CallAdapter] when the provider's response
// contains zero completion choices — a malformed or empty API response.
type NoChoicesError struct {
	// Name is the [llm.Call]'s name, as passed to [llm.NewCall] — lets
	// callers disambiguate which declared Call failed when multiple Calls
	// share the same Model.
	Name string
	// Model is the model identifier the request was made against.
	Model string
}

func (e NoChoicesError) Error() string {
	return fmt.Sprintf("openai: call %q: model %q: response contained zero completion choices", e.Name, e.Model)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e NoChoicesError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("model", e.Model),
	)
}

// RetriesExhaustedError is returned by [CallAdapter] when
// [CallAdapterOptions.MaxRetries] re-prompt attempts are exhausted without
// ever producing a codec-valid completion.
type RetriesExhaustedError struct {
	// Name is the [llm.Call]'s name, as passed to [llm.NewCall] — lets
	// callers disambiguate which declared Call failed when multiple Calls
	// share the same Model.
	Name string
	// Model is the model identifier the request was made against.
	Model string
	// Attempts is the total number of completion attempts made (1 + MaxRetries).
	Attempts int
	// LastErr is the last [llm.ResponseDecodeError] encountered.
	LastErr error
}

func (e RetriesExhaustedError) Error() string {
	return fmt.Sprintf("openai: call %q: model %q: exhausted %d attempt(s) without a valid completion: %v",
		e.Name, e.Model, e.Attempts, e.LastErr)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e RetriesExhaustedError) Unwrap() error { return e.LastErr }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RetriesExhaustedError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("model", e.Model),
		slog.Int("attempts", e.Attempts),
		slog.Any("lastErr", e.LastErr),
	)
}
