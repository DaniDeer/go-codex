package stats

import (
	"errors"
	"fmt"
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
// transport-specific hooks for HTTP and MQTT.
// All methods must be safe for concurrent use.
type Observer interface {
	ValidationObserver

	// RecordRequest is called after every HTTP request completes.
	// method is uppercase (GET, POST, …), path is the route pattern (e.g.
	// "/users/{id}", not the concrete URL), statusCode is the HTTP status
	// written, duration is the total handler time including encode/decode.
	RecordRequest(method, path string, statusCode int, duration time.Duration)

	// RecordSubscribe is called after every MQTT message is fully processed
	// by a SubscribeHandler. topic is the concrete incoming topic (not a
	// template), success is false when decode or the application handler
	// failed, duration is total processing time.
	RecordSubscribe(topic string, success bool, duration time.Duration)

	// RecordPublish is called after every Publish call completes.
	// topic is the resolved publish topic (after BuildTopic if vars were
	// provided), success is false when encode failed, the broker returned an
	// error, or the context was cancelled. duration covers encode + broker
	// acknowledgement wait.
	// Note: RecordPublish is not called when BuildTopic itself fails (the
	// message never reached the encode/publish stage).
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

// FileObserver is an optional extension to [Observer] for file I/O lifecycle
// events. [format.File] type-asserts the configured observer to FileObserver
// before calling its methods, so implementing this interface is purely additive
// — existing Observer implementations need not change.
//
//	type MyObserver struct{ ... }
//	func (o *MyObserver) RecordFileRead(path string, success bool, d time.Duration) {
//	    // increment a Prometheus counter, emit a log line, etc.
//	}
//	func (o *MyObserver) RecordFileWrite(path string, success bool, d time.Duration) { ... }
type FileObserver interface {
	// RecordFileRead is called after every [format.File.Read], [format.File.Update],
	// or [format.File.Patch] attempt (read phase). path is the concrete file path
	// (after template substitution), success is false on any error including
	// decode/validation failures.
	RecordFileRead(path string, success bool, duration time.Duration)

	// RecordFileWrite is called after every [format.File.Write], [format.File.Update],
	// or [format.File.Patch] attempt (write phase). success is false on any
	// encode or filesystem error.
	RecordFileWrite(path string, success bool, duration time.Duration)
}

// NoopObserver discards all events. It satisfies [Observer], [ValidationObserver],
// [PipelineObserver], and [SecurityObserver] and is the zero-cost default used
// when no observer is configured.
type NoopObserver struct{}

func (NoopObserver) RecordValidationError(_, _, _ string)              {}
func (NoopObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {}
func (NoopObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (NoopObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}
func (NoopObserver) RecordApply(_, _ string, _ bool, _ time.Duration)  {}
func (NoopObserver) RecordSecurityRejection(_, _ string)               {}
func (NoopObserver) RecordFileRead(_ string, _ bool, _ time.Duration)  {}
func (NoopObserver) RecordFileWrite(_ string, _ bool, _ time.Duration) {}

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
