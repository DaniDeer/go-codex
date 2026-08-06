package stats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/DaniDeer/go-codex/codex"
)

// ValidationObserver is the codec-level observability hook.
// Implement this interface when using codecs directly (without an adapter) to
// receive per-field validation error events. Use [ReportErrors] to extract
// [codex.ValidationErrors] from a decode error and call [RecordValidationError]
// for each failing field.
//
// [Observer] embeds ValidationObserver for use with adapters.
type ValidationObserver interface {
	// RecordValidationError is called for each field that fails codec
	// validation. location identifies the data source (e.g. "body", "query",
	// "payload", "config"), constraintName is the constraint identifier (e.g.
	// "minLen(3)", "non-negative-int", "email", "type-mismatch", "required"),
	// field is the field or parameter name from the structured error.
	RecordValidationError(location, constraintName, field string)
}

// Observer receives lifecycle events emitted by codec adapters.
// It embeds [ValidationObserver] for per-field validation errors and adds
// transport hooks for request/response cycles, subscriptions, and publishes.
// All methods must be safe for concurrent use.
type Observer interface {
	ValidationObserver

	// RecordRequest is called after every request/response cycle completes,
	// regardless of transport. method describes the transport and operation;
	// values vary by adapter:
	//   - HTTP adapters (nethttp, chi): uppercase HTTP method ("GET", "POST", …)
	//   - ZeroMQ adapters: "ZMQ-REP", "ZMQ-REQ", "ZMQ-ROUTER", "ZMQ-DEALER"
	//   - MCP adapter (mcpgo): "tool", "resource", "prompt"
	//   - MQTT5 request/reply: "MQTT5-REQ" (caller), "MQTT5-REP" (server)
	//
	// path is the route pattern or topic template (e.g. "/users/{id}",
	// "sensors/{sensorID}/data"), not the concrete URL or resolved topic.
	// statusCode follows HTTP conventions: 200 success, 400 client error,
	// 500 server/encode error; 0 means no request reached the transport (e.g.
	// pre-flight validation failure or context already cancelled).
	// duration is the total round-trip time including encode/decode.
	RecordRequest(method, path string, statusCode int, duration time.Duration)

	// RecordSubscribe is called after every inbound message or event is fully
	// processed. topic is the concrete incoming value (not a template).
	// success is false when decode or the application handler failed.
	// duration is the total processing time.
	//
	// Used by:
	//   - MQTT adapters (mqtt, mqtt5): called per incoming message
	//   - SSE stream bridges (nethttp, chi): called per emitted SSE event
	//     (success=true on send, success=false on write error or stream error)
	RecordSubscribe(topic string, success bool, duration time.Duration)

	// RecordPublish is called after every outbound message is sent.
	// topic is the resolved publish topic (after template substitution).
	// success is false when encode failed, the broker returned an error, or
	// the context was cancelled. duration covers encode + broker acknowledgement.
	//
	// Note: RecordPublish is not called when topic template substitution itself
	// fails — the message never reached the encode/publish stage in that case.
	//
	// Used by: MQTT adapters (mqtt, mqtt5), ZeroMQ Publish.
	RecordPublish(topic string, success bool, duration time.Duration)
}

// PipelineObserver receives lifecycle events from forge.Function*.Apply calls.
// It is a separate interface from [Observer] because forge is a domain computation
// layer, not a transport adapter. Implement alongside [Observer] when you want
// telemetry for both transport and KPI computation in the same process.
type PipelineObserver interface {
	// RecordApply is called after every forge.Function*.Apply completes.
	// name is the function name, version is its version string, success is false
	// when any validation or computation step returned an error, duration is the
	// total Apply time (input validation + computation + output validation).
	RecordApply(name, version string, success bool, duration time.Duration)
}

// SecurityObserver is an optional extension to [Observer] for security rejection
// events. Adapters type-assert the configured Observer to SecurityObserver before
// calling RecordSecurityRejection, so implementing this interface is purely
// additive — existing Observer implementations need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordSecurityRejection(location, scheme string) {
//	    // increment a Prometheus counter, emit a log line, etc.
//	}
type SecurityObserver interface {
	// RecordSecurityRejection is called when a security check rejects a request
	// or message. location is the route path (HTTP) or topic (MQTT). scheme is
	// the first declared security scheme name for the operation.
	RecordSecurityRejection(location, scheme string)
}

// TraceObserver is an optional extension to [Observer] for distributed tracing.
// Adapters type-assert the configured Observer to TraceObserver before calling
// StartSpan/EndSpan, so implementing this interface is purely additive — existing
// Observer implementations need not change.
//
//	type MyTracer struct{ ... }
//	func (t *MyTracer) StartSpan(ctx context.Context, operation, name string) context.Context {
//	    span, ctx := otel.Tracer("go-codex").Start(ctx, operation,
//	        otel.WithAttributes(attribute.String("name", name)),
//	    )
//	    return ctx
//	}
//	func (t *MyTracer) EndSpan(ctx context.Context, err error) {
//	    span := trace.SpanFromContext(ctx)
//	    if err != nil {
//	        span.RecordError(err)
//	        span.SetStatus(codes.Error, err.Error())
//	    }
//	    span.End()
//	}
//
// The pattern at each adapter call site mirrors the [SecurityObserver] guard:
//
//	if to, ok := obs.(TraceObserver); ok {
//	    ctx = to.StartSpan(ctx, op, name)
//	    defer func() { to.EndSpan(ctx, err) }()
//	}
//
// Where err is the eventual operation error (nil on success).
//
// operation values follow a convention: "http.request", "mqtt.subscribe",
// "mqtt.publish", "forge.apply", "file.read", "file.write", "mcp.tool",
// "mcp.resource", "mcp.prompt".
// name is the concrete identifier (route path template, topic, function name,
// file path).
type TraceObserver interface {
	// StartSpan starts a new trace span for the named operation and returns
	// a context.Context containing the new span for child propagation.
	// The returned context should be passed to the application's handler
	// function so that business logic can create child spans.
	StartSpan(ctx context.Context, operation, name string) context.Context

	// EndSpan ends the span associated with ctx, recording err as an error
	// event when non-nil.
	EndSpan(ctx context.Context, err error)
}

// FileObserver is an optional extension to [Observer] for file I/O lifecycle
// events. [ports.File] type-asserts the configured observer to FileObserver
// before calling its methods, so implementing this interface is purely additive
// — existing Observer implementations need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordFileRead(path string, success bool, d time.Duration) {
//	    // increment a Prometheus counter, emit a log line, etc.
//	}
//	func (o *MyObserver) RecordFileWrite(path string, success bool, d time.Duration) { ... }
type FileObserver interface {
	// RecordFileRead is called after every [ports.File.Read], [ports.File.Update],
	// or [ports.File.Patch] attempt (read phase). path is the concrete file path
	// (after template substitution), success is false on any error including
	// decode/validation failures.
	RecordFileRead(path string, success bool, duration time.Duration)

	// RecordFileWrite is called after every [ports.File.Write], [ports.File.Update],
	// or [ports.File.Patch] attempt (write phase). success is false on any
	// encode or filesystem error.
	RecordFileWrite(path string, success bool, duration time.Duration)
}

// SQLObserver is an optional extension to [Observer] for SQL adapter lifecycle
// events. [adapters/sql.Validate] and [adapters/sql.Migrator] type-assert the
// configured Observer to SQLObserver before calling its methods — existing
// Observer implementations need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordValidation(table, op string, d time.Duration, err error) {
//	    // record metrics, emit a log line, etc.
//	}
//	func (o *MyObserver) RecordMigration(op, name string, version int64, d time.Duration, err error) { ... }
type SQLObserver interface {
	// RecordValidation is called after every [adapters/sql.Validate] call,
	// success or failure. table and op mirror ValidateOptions.Table/Op.
	// err is nil on success.
	RecordValidation(table, op string, duration time.Duration, err error)

	// RecordMigration is called once per applied or rolled-back migration file
	// during Migrator.Up or Migrator.Down. op is "up" or "down".
	RecordMigration(op, name string, version int64, duration time.Duration, err error)
}

// CacheObserver is an optional extension to [Observer] for cache adapter
// lifecycle events (adapters/redis). Cache adapters type-assert the configured
// Observer to CacheObserver before calling its methods — existing Observer
// implementations need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordCacheHit(key string, d time.Duration)  { hits.Inc() }
//	func (o *MyObserver) RecordCacheMiss(key string, d time.Duration) { misses.Inc() }
//	func (o *MyObserver) RecordCacheWrite(key, op string, success bool, d time.Duration) { ... }
type CacheObserver interface {
	// RecordCacheHit is called for every cache lookup that found and decoded
	// a value. key is the expanded cache key (e.g. "user:42").
	RecordCacheHit(key string, duration time.Duration)

	// RecordCacheMiss is called for every cache lookup that found no value.
	RecordCacheMiss(key string, duration time.Duration)

	// RecordCacheWrite is called for every cache write or delete, success or
	// failure. op is "set" or "del".
	RecordCacheWrite(key, op string, success bool, duration time.Duration)
}

// CredentialCacheObserver is an optional extension to [Observer] for
// credential-cache lifecycle events (adapters/nethttp's
// NewCachingCredentialFunc). Adapters type-assert the configured Observer
// to CredentialCacheObserver before calling its methods — existing
// Observer implementations need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordCredentialCacheHit(location string, d time.Duration)             { hits.Inc() }
//	func (o *MyObserver) RecordCredentialCacheRefresh(location string, success bool, d time.Duration) { ... }
type CredentialCacheObserver interface {
	// RecordCredentialCacheHit is called when a cached credential is reused
	// without invoking the wrapped CredentialFunc. duration is the
	// (near-zero) lookup cost — included for consistency with
	// [CacheObserver.RecordCacheHit], which always includes duration even
	// on a hit.
	RecordCredentialCacheHit(location string, duration time.Duration)

	// RecordCredentialCacheRefresh is called when the wrapped CredentialFunc
	// is invoked (cache miss, TTL expiry, or a refresh after invalidation)
	// — success indicates whether it returned without error. duration is
	// its own call duration.
	RecordCredentialCacheRefresh(location string, success bool, duration time.Duration)
}

// StreamObserver is an optional extension to [Observer] for stream-level throughput
// metrics. [stream.Apply] type-asserts the configured Observer to
// StreamObserver before calling its methods — existing Observer implementations
// need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordStreamItem(function string, success bool, d time.Duration) {
//	    // record per-item throughput, latency, etc.
//	}
type StreamObserver interface {
	// RecordStreamItem is called for every item that passes through [stream.Apply],
	// success or failure. function is the forge function name.
	// success is false when forge.Function.Apply returned an error.
	RecordStreamItem(function string, success bool, duration time.Duration)
}

// LoggingObserver logs every observer event as a structured slog message.
// It implements all observer interfaces except [TraceObserver]:
// [Observer] (embeds [ValidationObserver]), [PipelineObserver],
// [SecurityObserver], [FileObserver], [SQLObserver], [StreamObserver],
// [CacheObserver], and [CredentialCacheObserver].
//
// [TraceObserver] is intentionally not implemented — slog has no concept of
// distributed trace spans. Use [stats.NewFanout] to combine a LoggingObserver
// with a separate [TraceObserver] implementation (e.g. OpenTelemetry).
//
// Configure the logger's handler for your environment:
//   - [slog.NewTextHandler] for development
//   - [slog.NewJSONHandler] for structured log aggregation
//   - An OpenTelemetry slog bridge for distributed traces
//
// Combine with a metrics observer via [NewFanout] for both metrics and logging:
//
//	obs := stats.NewFanout(
//	    metricsObserver,
//	    stats.NewLoggingObserver(slog.Default().With("component", "api")),
//	)
type LoggingObserver struct {
	logger *slog.Logger
}

// NewLoggingObserver returns a [LoggingObserver] backed by logger.
func NewLoggingObserver(logger *slog.Logger) *LoggingObserver {
	return &LoggingObserver{logger: logger}
}

func (o *LoggingObserver) RecordValidationError(location, constraint, field string) {
	o.logger.Warn("codec validation error",
		"location", location, "constraint", constraint, "field", field)
}

func (o *LoggingObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
	o.logger.Info("request",
		"method", method, "path", path, "status", statusCode, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordSubscribe(topic string, success bool, d time.Duration) {
	o.logger.Debug("subscribe",
		"topic", topic, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordPublish(topic string, success bool, d time.Duration) {
	o.logger.Debug("publish",
		"topic", topic, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordApply(name, version string, success bool, d time.Duration) {
	o.logger.Debug("pipeline apply",
		"function", name, "version", version, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordSecurityRejection(location, scheme string) {
	o.logger.Warn("security rejection", "location", location, "scheme", scheme)
}

func (o *LoggingObserver) RecordFileRead(path string, success bool, d time.Duration) {
	o.logger.Debug("file read", "path", path, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordFileWrite(path string, success bool, d time.Duration) {
	o.logger.Debug("file write", "path", path, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordValidation(table, op string, d time.Duration, err error) {
	o.logger.Debug("sql validate", "table", table, "op", op, "ms", d.Milliseconds(), "err", err)
}

func (o *LoggingObserver) RecordMigration(op, name string, version int64, d time.Duration, err error) {
	o.logger.Info("sql migration", "op", op, "name", name, "version", version, "ms", d.Milliseconds(), "err", err)
}

func (o *LoggingObserver) RecordStreamItem(function string, success bool, d time.Duration) {
	o.logger.Debug("stream item", "function", function, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordCacheHit(key string, d time.Duration) {
	o.logger.Debug("cache hit", "key", key, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordCacheMiss(key string, d time.Duration) {
	o.logger.Debug("cache miss", "key", key, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordCacheWrite(key, op string, success bool, d time.Duration) {
	o.logger.Debug("cache write", "key", key, "op", op, "success", success, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordCredentialCacheHit(location string, d time.Duration) {
	o.logger.Debug("credential cache hit", "location", location, "ms", d.Milliseconds())
}

func (o *LoggingObserver) RecordCredentialCacheRefresh(location string, success bool, d time.Duration) {
	o.logger.Debug("credential cache refresh", "location", location, "success", success, "ms", d.Milliseconds())
}

// NewFanout returns an [Observer] that fans out all calls to each provided observer.
// The returned value also implements [FileObserver], [SecurityObserver],
// [PipelineObserver], [SQLObserver], [StreamObserver], [CacheObserver],
// [CredentialCacheObserver], and [TraceObserver] — delegating each to the
// inner observers that satisfy those
// interfaces, so composing
// a metrics-only observer with a [LoggingObserver] works without any
// type-assertion boilerplate.
//
//	obs := stats.NewFanout(
//	    metricsObserver,
//	    stats.NewLoggingObserver(slog.Default()),
//	)
func NewFanout(observers ...Observer) Observer {
	return &fanout{observers: observers}
}

// fanout fans out all observer calls to multiple observers.
type fanout struct{ observers []Observer }

func (f *fanout) RecordValidationError(location, constraint, field string) {
	for _, o := range f.observers {
		o.RecordValidationError(location, constraint, field)
	}
}

func (f *fanout) RecordRequest(method, path string, statusCode int, d time.Duration) {
	for _, o := range f.observers {
		o.RecordRequest(method, path, statusCode, d)
	}
}

func (f *fanout) RecordSubscribe(topic string, success bool, d time.Duration) {
	for _, o := range f.observers {
		o.RecordSubscribe(topic, success, d)
	}
}

func (f *fanout) RecordPublish(topic string, success bool, d time.Duration) {
	for _, o := range f.observers {
		o.RecordPublish(topic, success, d)
	}
}

// RecordFileRead implements [FileObserver] — delegates to inner observers that also
// implement [FileObserver].
func (f *fanout) RecordFileRead(path string, success bool, d time.Duration) {
	for _, o := range f.observers {
		if fo, ok := o.(FileObserver); ok {
			fo.RecordFileRead(path, success, d)
		}
	}
}

// RecordFileWrite implements [FileObserver].
func (f *fanout) RecordFileWrite(path string, success bool, d time.Duration) {
	for _, o := range f.observers {
		if fo, ok := o.(FileObserver); ok {
			fo.RecordFileWrite(path, success, d)
		}
	}
}

// RecordSecurityRejection implements [SecurityObserver].
func (f *fanout) RecordSecurityRejection(location, scheme string) {
	for _, o := range f.observers {
		if so, ok := o.(SecurityObserver); ok {
			so.RecordSecurityRejection(location, scheme)
		}
	}
}

// RecordApply implements [PipelineObserver].
func (f *fanout) RecordApply(name, version string, success bool, d time.Duration) {
	for _, o := range f.observers {
		if po, ok := o.(PipelineObserver); ok {
			po.RecordApply(name, version, success, d)
		}
	}
}

// RecordValidation implements [SQLObserver].
func (f *fanout) RecordValidation(table, op string, d time.Duration, err error) {
	for _, o := range f.observers {
		if so, ok := o.(SQLObserver); ok {
			so.RecordValidation(table, op, d, err)
		}
	}
}

// RecordMigration implements [SQLObserver].
func (f *fanout) RecordMigration(op, name string, version int64, d time.Duration, err error) {
	for _, o := range f.observers {
		if so, ok := o.(SQLObserver); ok {
			so.RecordMigration(op, name, version, d, err)
		}
	}
}

// RecordStreamItem implements [StreamObserver].
func (f *fanout) RecordStreamItem(function string, success bool, d time.Duration) {
	for _, o := range f.observers {
		if so, ok := o.(StreamObserver); ok {
			so.RecordStreamItem(function, success, d)
		}
	}
}

// RecordCacheHit implements [CacheObserver].
func (f *fanout) RecordCacheHit(key string, d time.Duration) {
	for _, o := range f.observers {
		if co, ok := o.(CacheObserver); ok {
			co.RecordCacheHit(key, d)
		}
	}
}

// RecordCacheMiss implements [CacheObserver].
func (f *fanout) RecordCacheMiss(key string, d time.Duration) {
	for _, o := range f.observers {
		if co, ok := o.(CacheObserver); ok {
			co.RecordCacheMiss(key, d)
		}
	}
}

// RecordCacheWrite implements [CacheObserver].
func (f *fanout) RecordCacheWrite(key, op string, success bool, d time.Duration) {
	for _, o := range f.observers {
		if co, ok := o.(CacheObserver); ok {
			co.RecordCacheWrite(key, op, success, d)
		}
	}
}

// RecordCredentialCacheHit implements [CredentialCacheObserver].
func (f *fanout) RecordCredentialCacheHit(location string, d time.Duration) {
	for _, o := range f.observers {
		if co, ok := o.(CredentialCacheObserver); ok {
			co.RecordCredentialCacheHit(location, d)
		}
	}
}

// RecordCredentialCacheRefresh implements [CredentialCacheObserver].
func (f *fanout) RecordCredentialCacheRefresh(location string, success bool, d time.Duration) {
	for _, o := range f.observers {
		if co, ok := o.(CredentialCacheObserver); ok {
			co.RecordCredentialCacheRefresh(location, success, d)
		}
	}
}

// StartSpan implements [TraceObserver].
func (f *fanout) StartSpan(ctx context.Context, operation, name string) context.Context {
	for _, o := range f.observers {
		if to, ok := o.(TraceObserver); ok {
			ctx = to.StartSpan(ctx, operation, name)
		}
	}
	return ctx
}

// EndSpan implements [TraceObserver].
func (f *fanout) EndSpan(ctx context.Context, err error) {
	for _, o := range f.observers {
		if to, ok := o.(TraceObserver); ok {
			to.EndSpan(ctx, err)
		}
	}
}

// NoopObserver discards all events. It satisfies all observer interfaces —
// [Observer] (embeds [ValidationObserver]), [PipelineObserver],
// [SecurityObserver], [FileObserver], [SQLObserver], [StreamObserver],
// [CacheObserver], and [TraceObserver] — and is the zero-cost default used
// when no observer is configured.
type NoopObserver struct{}

func (NoopObserver) RecordValidationError(_, _, _ string)                           {}
func (NoopObserver) RecordRequest(_, _ string, _ int, _ time.Duration)              {}
func (NoopObserver) RecordSubscribe(_ string, _ bool, _ time.Duration)              {}
func (NoopObserver) RecordPublish(_ string, _ bool, _ time.Duration)                {}
func (NoopObserver) RecordApply(_, _ string, _ bool, _ time.Duration)               {}
func (NoopObserver) RecordSecurityRejection(_, _ string)                            {}
func (NoopObserver) RecordFileRead(_ string, _ bool, _ time.Duration)               {}
func (NoopObserver) RecordFileWrite(_ string, _ bool, _ time.Duration)              {}
func (NoopObserver) RecordValidation(_, _ string, _ time.Duration, _ error)         {}
func (NoopObserver) RecordMigration(_, _ string, _ int64, _ time.Duration, _ error) {}
func (NoopObserver) RecordStreamItem(_ string, _ bool, _ time.Duration)             {}
func (NoopObserver) RecordCacheHit(_ string, _ time.Duration)                       {}
func (NoopObserver) RecordCacheMiss(_ string, _ time.Duration)                      {}
func (NoopObserver) RecordCacheWrite(_, _ string, _ bool, _ time.Duration)          {}
func (NoopObserver) RecordCredentialCacheHit(_ string, _ time.Duration)             {}
func (NoopObserver) RecordCredentialCacheRefresh(_ string, _ bool, _ time.Duration) {}
func (NoopObserver) StartSpan(ctx context.Context, _, _ string) context.Context     { return ctx }
func (NoopObserver) EndSpan(_ context.Context, _ error)                             {}

// ReportErrors walks err and calls obs.RecordValidationError for every codec
// validation failure it finds. location identifies the data source (e.g. "body",
// "query", "payload", "config"). A no-op if err is nil or contains no recognisable
// validation errors.
//
// Handled error types:
//   - [codex.ValidationErrors]  — each entry reports its field and constraint.
//   - [codex.KeyError]          — reports the failing map key as the field.
//   - [codex.ElementError]      — reports the slice index as the field (e.g. "[2]").
//
// For all three, the walker recurses into the wrapped cause so nested errors
// (e.g. a KeyError whose cause is a ValidationErrors) are fully reported.
// Any other wrapped error is unwrapped and recursed into silently.
func ReportErrors(obs ValidationObserver, location string, err error) {
	walkErrors(obs, location, err)
}

func walkErrors(obs ValidationObserver, location string, err error) {
	if err == nil {
		return
	}
	var ve codex.ValidationErrors
	if errors.As(err, &ve) {
		for _, e := range ve {
			obs.RecordValidationError(location, ConstraintName(e.Err), e.Field)
			walkErrors(obs, location, e.Err)
		}
		return
	}
	var ke codex.KeyError
	if errors.As(err, &ke) {
		obs.RecordValidationError(location, ConstraintName(ke.Err), ke.Key)
		walkErrors(obs, location, ke.Err)
		return
	}
	var ee codex.ElementError
	if errors.As(err, &ee) {
		obs.RecordValidationError(location, ConstraintName(ee.Err), fmt.Sprintf("[%d]", ee.Index))
		walkErrors(obs, location, ee.Err)
		return
	}
	// Unwrap and recurse for any other wrapping error (e.g. forge.InputError).
	if u := errors.Unwrap(err); u != nil {
		walkErrors(obs, location, u)
	}
}

// ConstraintName extracts the constraint identifier from a field-level error:
//   - [codex.ConstraintError].Name when the error is a constraint failure
//   - "type-mismatch" for [codex.TypeMismatchError]
//   - "required" for [codex.ErrMissingField]
//   - "" for any other error type
func ConstraintName(err error) string {
	var ce codex.ConstraintError
	if errors.As(err, &ce) {
		return ce.Name
	}
	var te codex.TypeMismatchError
	if errors.As(err, &te) {
		return "type-mismatch"
	}
	if errors.Is(err, codex.ErrMissingField) {
		return "required"
	}
	return ""
}
