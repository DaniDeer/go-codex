// Package stats defines the Observer interface for codec and adapter lifecycle events.
//
// go-codex exposes two levels of observability:
//
// # Codec-level (ValidationObserver)
//
// Use [ValidationObserver] when you call codecs directly without an adapter —
// for example, validating config files, parsing binary protocols, or any
// non-HTTP/MQTT use case. Implement just [ValidationObserver.RecordValidationError]
// and call [ReportErrors] after each [codex.Codec.Decode]:
//
//	type MyObserver struct{}
//	func (o *MyObserver) RecordValidationError(location, constraint, field string) {
//	    // increment Prometheus counter, emit log, etc.
//	}
//
//	val, err := appConfigCodec.Decode(rawData)
//	stats.ReportErrors(&MyObserver{}, "config", err)
//
// # Adapter-level (Observer)
//
// Use the full [Observer] interface when wiring to an adapter. It embeds
// [ValidationObserver] and adds transport-specific hooks for HTTP and MQTT:
//
//	nethttp.Register(mux, route, handler, nethttp.Options{Observer: obs})
//
//	adaptermqtt.SubscribeHandler(ctx, ch, fn, adaptermqtt.SubscribeOptions{Observer: obs})
//	adaptermqtt.Publish(ctx, client, ch, qos, retained, msg, vars,
//	    adaptermqtt.PublishOptions{Observer: obs})
//
// [NoopObserver] is a zero-cost default used when no observer is configured.
package stats

import (
	"errors"
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

// NoopObserver discards all events. It satisfies [Observer], [ValidationObserver],
// and [PipelineObserver] and is the zero-cost default used when no observer is
// configured.
type NoopObserver struct{}

func (NoopObserver) RecordValidationError(_, _, _ string)              {}
func (NoopObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {}
func (NoopObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (NoopObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}
func (NoopObserver) RecordApply(_, _ string, _ bool, _ time.Duration)  {}

// ReportErrors extracts [codex.ValidationErrors] from err and calls
// obs.RecordValidationError for each failing field. location identifies the
// data source (e.g. "body", "query", "payload", "config"). A no-op if err
// contains no ValidationErrors.
func ReportErrors(obs ValidationObserver, location string, err error) {
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		return
	}
	for _, e := range ve {
		obs.RecordValidationError(location, ConstraintName(e.Err), e.Field)
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
