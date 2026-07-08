package format

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/DaniDeer/go-codex/codex"
)

// EmbeddedDecodeError is returned by [EmbeddedJSON], [EmbeddedYAML], and
// [EmbeddedTOML] when the string value cannot be parsed as the expected format.
// Codec validation errors from the inner codec propagate unchanged.
//
//	var dec format.EmbeddedDecodeError
//	if errors.As(err, &dec) {
//	    slog.Warn("embedded field parse failed", "error", dec)
//	}
type EmbeddedDecodeError struct {
	// Format is the wire format that failed to parse ("json", "yaml", "toml").
	Format string
	// Err is the underlying parse error from the format library.
	Err error
}

func (e EmbeddedDecodeError) Error() string {
	return fmt.Sprintf("embedded %s decode: %v", e.Format, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e EmbeddedDecodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e EmbeddedDecodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("format", e.Format),
		slog.Any("cause", e.Err),
	)
}

// EmbeddedEncodeError is returned by [EmbeddedJSON], [EmbeddedYAML], and
// [EmbeddedTOML] when the Go value cannot be marshalled to the target format string.
//
//	var enc format.EmbeddedEncodeError
//	if errors.As(err, &enc) {
//	    slog.Warn("embedded field marshal failed", "error", enc)
//	}
type EmbeddedEncodeError struct {
	// Format is the wire format that failed to marshal ("json", "yaml", "toml").
	Format string
	// Err is the underlying marshal error from the format library.
	Err error
}

func (e EmbeddedEncodeError) Error() string {
	return fmt.Sprintf("embedded %s encode: %v", e.Format, e.Err)
}

// Unwrap allows [errors.Is] and [errors.As] to traverse the underlying error.
func (e EmbeddedEncodeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e EmbeddedEncodeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("format", e.Format),
		slog.Any("cause", e.Err),
	)
}

// EmbeddedJSON returns a [codex.Codec][T] where the wire type is a JSON-encoded
// string. The inner codec's constraints and schema run on the decoded value.
//
// Decode path: JSON string → json.Unmarshal → map[string]any → inner.Decode → T
// Encode path: T → inner.Encode → map[string]any → json.Marshal → JSON string
//
// This pattern is common in systems where a structured value is serialised into
// a string field: CloudEvents data-as-string, database JSONB via REST APIs,
// Kafka message headers, and similar double-encoded formats.
//
// On format parse failure, returns [EmbeddedDecodeError]{Format:"json"}.
// On marshal failure, returns [EmbeddedEncodeError]{Format:"json"}.
// Codec validation errors from inner propagate unchanged.
//
// Example — CloudEvents-style event with a typed payload in a string field:
//
//	var eventCodec = codex.Struct[Event](
//	    codex.RequiredField("type",    codex.String(), ...),
//	    codex.RequiredField("payload", format.EmbeddedJSON(userCodec), ...),
//	)
//	// Wire: {"type":"user.created","payload":"{\"id\":\"123\",\"name\":\"Alice\"}"}
//	// Go:   Event{Type:"user.created", Payload:User{ID:"123", Name:"Alice"}}
func EmbeddedJSON[T any](inner codex.Codec[T]) codex.Codec[T] {
	return codex.MapCodecValidated(
		codex.String(),
		inner,
		func(s string) (T, error) {
			var raw any
			if err := json.Unmarshal([]byte(s), &raw); err != nil {
				var zero T
				return zero, EmbeddedDecodeError{Format: "json", Err: err}
			}
			return inner.Decode(raw)
		},
		func(v T) (string, error) {
			intermediate, err := inner.Encode(v)
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(intermediate)
			if err != nil {
				return "", EmbeddedEncodeError{Format: "json", Err: err}
			}
			return string(b), nil
		},
	)
}

// EmbeddedYAML returns a [codex.Codec][T] where the wire type is a YAML-encoded
// string. The inner codec's constraints run on the decoded value.
//
// Decode path: YAML string → yaml.Unmarshal → map[string]any → inner.Decode → T
// Encode path: T → inner.Encode → map[string]any → yaml.Marshal → YAML string
//
// YAML integers decode as int and YAML floats as float64 — both are handled
// correctly by the built-in codex primitives (Int, Float64, etc.).
//
// On format parse failure, returns [EmbeddedDecodeError]{Format:"yaml"}.
// On marshal failure, returns [EmbeddedEncodeError]{Format:"yaml"}.
func EmbeddedYAML[T any](inner codex.Codec[T]) codex.Codec[T] {
	return codex.MapCodecValidated(
		codex.String(),
		inner,
		func(s string) (T, error) {
			var raw any
			if err := yaml.Unmarshal([]byte(s), &raw); err != nil {
				var zero T
				return zero, EmbeddedDecodeError{Format: "yaml", Err: err}
			}
			return inner.Decode(raw)
		},
		func(v T) (string, error) {
			intermediate, err := inner.Encode(v)
			if err != nil {
				return "", err
			}
			b, err := yaml.Marshal(intermediate)
			if err != nil {
				return "", EmbeddedEncodeError{Format: "yaml", Err: err}
			}
			return string(b), nil
		},
	)
}

// EmbeddedTOML returns a [codex.Codec][T] where the wire type is a TOML-encoded
// string. The inner codec's constraints run on the decoded value.
//
// Decode path: TOML string → toml.Decode → map[string]any → inner.Decode → T
// Encode path: T → inner.Encode → map[string]any → toml.Encode → TOML string
//
// TOML integers decode as int64 and TOML floats as float64 — both are handled
// correctly by the built-in codex primitives.
//
// Note: TOML requires all keys to be strings and does not support top-level
// arrays. Use EmbeddedTOML with struct codecs (not SliceOf).
//
// On format parse failure, returns [EmbeddedDecodeError]{Format:"toml"}.
// On marshal failure, returns [EmbeddedEncodeError]{Format:"toml"}.
func EmbeddedTOML[T any](inner codex.Codec[T]) codex.Codec[T] {
	return codex.MapCodecValidated(
		codex.String(),
		inner,
		func(s string) (T, error) {
			var raw any
			if _, err := toml.Decode(s, &raw); err != nil {
				var zero T
				return zero, EmbeddedDecodeError{Format: "toml", Err: err}
			}
			return inner.Decode(raw)
		},
		func(v T) (string, error) {
			intermediate, err := inner.Encode(v)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			if err := toml.NewEncoder(&sb).Encode(intermediate); err != nil {
				return "", EmbeddedEncodeError{Format: "toml", Err: err}
			}
			return sb.String(), nil
		},
	)
}
