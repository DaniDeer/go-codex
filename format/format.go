// Package format bridges Codec[T] to concrete serialization formats (JSON, YAML, TOML).
//
// A codec works with an intermediate representation (map[string]any) that is
// format-agnostic. Format wraps that intermediate layer so the same codec can
// read and write multiple wire formats without any changes to the codec itself.
package format

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/BurntSushi/toml"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// ErrNotStreamable is returned by [Format.MarshalTo] when the format was not
// created with [NewStreamed] and therefore does not support streaming output.
var ErrNotStreamable = errors.New("format: not streamable — use NewStreamed to create a streaming format")

// Format binds a Codec[T] to a specific serialization format.
// Use JSON, YAML, or TOML to construct one. For formats that operate on the
// typed value directly (e.g. HTML rendering), use [NewTyped]. For streaming
// output (write to io.Writer without buffering), use [NewStreamed].
type Format[T any] struct {
	codec          codex.Codec[T]
	marshal        func(any) ([]byte, error)
	unmarshal      func([]byte) (any, error)
	marshalTyped   func(T) ([]byte, error)
	unmarshalTyped func([]byte) (T, error)
	marshalTo      func(T, io.Writer) error
	contentType    string
}

// New creates a Format from a codec and custom marshal/unmarshal functions.
// Use this to integrate formats not covered by the built-in constructors.
// ContentType is empty by default; call [Format.WithContentType] to set it.
func New[T any](c codex.Codec[T], marshal func(any) ([]byte, error), unmarshal func([]byte) (any, error)) Format[T] {
	return Format[T]{codec: c, marshal: marshal, unmarshal: unmarshal}
}

// NewTyped creates a Format where the marshal and unmarshal functions operate
// on the typed value directly, rather than on the intermediate representation.
// The codec is still used for validation: [Format.Marshal] runs all Refine
// constraints on the value before calling marshal, and [Format.Validate] works
// as normal.
//
// Use this when the wire format cannot be represented via a map[string]any
// intermediate — for example, rendering HTML via a templ component:
//
//	htmlFormat := format.NewTyped(
//	    propsCodec,
//	    func(props Props) ([]byte, error) {
//	        var buf bytes.Buffer
//	        err := component(props).Render(context.Background(), &buf)
//	        return buf.Bytes(), err
//	    },
//	    func([]byte) (Props, error) {
//	        var zero Props
//	        return zero, errors.New("HTML is not decodable")
//	    },
//	    "text/html; charset=utf-8",
//	)
func NewTyped[T any](c codex.Codec[T], marshal func(T) ([]byte, error), unmarshal func([]byte) (T, error), contentType string) Format[T] {
	return Format[T]{
		codec:          c,
		marshalTyped:   marshal,
		unmarshalTyped: unmarshal,
		contentType:    contentType,
	}
}

// NewStreamed creates a Format where responses are written directly to an
// [io.Writer] without buffering to a []byte intermediate. This enables chunked
// or streaming responses for large payloads (HTML pages, JSON arrays, CSV exports).
//
// The codec is used for validation: [Format.MarshalTo] runs all Refine
// constraints on the value before calling marshalTo, so invalid data is
// rejected before any bytes are written.
//
// unmarshal is used for [Format.Unmarshal] (reading streaming formats is rarely
// needed; pass a function that returns an error when not applicable).
//
// Use [Format.IsStreamable] to detect streaming formats. The adapter calls
// [Format.MarshalTo] instead of [Format.Marshal] and writes response headers
// before streaming, so partial output is never flushed on validation failure.
//
// Example — streaming a templ component without buffering:
//
//	streamFmt := format.NewStreamed(
//	    propsCodec,
//	    func(props Props, w io.Writer) error {
//	        return component(props).Render(context.Background(), w)
//	    },
//	    func([]byte) (Props, error) {
//	        var zero Props
//	        return zero, errors.New("HTML is not decodable")
//	    },
//	    "text/html; charset=utf-8",
//	)
func NewStreamed[T any](c codex.Codec[T], marshalTo func(T, io.Writer) error, unmarshal func([]byte) (T, error), contentType string) Format[T] {
	return Format[T]{
		codec:          c,
		marshalTo:      marshalTo,
		unmarshalTyped: unmarshal,
		contentType:    contentType,
	}
}

// ContentType returns the MIME type associated with this format (e.g. "application/json").
// Empty string means the format has no registered content type.
func (f Format[T]) ContentType() string { return f.contentType }

// WithContentType returns a copy of the format with the given MIME content type set.
// Use this when registering custom formats for content negotiation.
func (f Format[T]) WithContentType(ct string) Format[T] {
	f.contentType = ct
	return f
}

// IsStreamable reports whether this format supports streaming output via [Format.MarshalTo].
// Streaming formats are created with [NewStreamed].
func (f Format[T]) IsStreamable() bool { return f.marshalTo != nil }

// Marshal encodes v to bytes using the codec and then the format serializer.
// If the format was created with [NewTyped], the codec validates v first and
// then the typed marshal function is called directly (bypassing the intermediate).
// For streaming formats created with [NewStreamed], use [Format.MarshalTo] instead.
func (f Format[T]) Marshal(v T) ([]byte, error) {
	if f.marshalTyped != nil {
		// Validate via codec (runs all Refine constraints) before rendering.
		if _, err := f.codec.Encode(v); err != nil {
			return nil, err
		}
		return f.marshalTyped(v)
	}
	intermediate, err := f.codec.Encode(v)
	if err != nil {
		return nil, err
	}
	return f.marshal(intermediate)
}

// MarshalTo validates v via the codec and then writes the serialized form
// directly to w without buffering to a []byte intermediate. Use this with
// formats created via [NewStreamed]; call [Format.IsStreamable] first.
// Returns [ErrNotStreamable] if the format has no streaming marshal function.
// Validation errors are returned before any bytes are written to w.
func (f Format[T]) MarshalTo(v T, w io.Writer) error {
	if f.marshalTo == nil {
		return ErrNotStreamable
	}
	// Validate via codec before writing any bytes — ensures headers are not
	// yet committed when validation fails (adapter writes headers after this).
	if _, err := f.codec.Encode(v); err != nil {
		return err
	}
	return f.marshalTo(v, w)
}

// Unmarshal deserializes data into an intermediate and then decodes it via the codec.
// If the format was created with [NewTyped], the typed unmarshal function is used directly.
func (f Format[T]) Unmarshal(data []byte) (T, error) {
	if f.unmarshalTyped != nil {
		return f.unmarshalTyped(data)
	}
	intermediate, err := f.unmarshal(data)
	if err != nil {
		var zero T
		return zero, err
	}
	return f.codec.Decode(intermediate)
}

// Validate checks v against the codec's constraints without serializing to bytes.
// It delegates to Codec.Validate — see its documentation for the rationale.
func (f Format[T]) Validate(v T) error {
	return f.codec.Validate(v)
}

// Schema returns the schema.Schema from the underlying codec.
func (f Format[T]) Schema() schema.Schema {
	return f.codec.Schema
}

// JSON returns a Format that reads and writes JSON.
// ContentType is "application/json".
func JSON[T any](c codex.Codec[T]) Format[T] {
	return Format[T]{
		codec:       c,
		contentType: "application/json",
		marshal:     func(v any) ([]byte, error) { return json.Marshal(v) },
		unmarshal: func(data []byte) (any, error) {
			var v any
			return v, json.Unmarshal(data, &v)
		},
	}
}

// YAML returns a Format that reads and writes YAML.
// ContentType is "application/yaml".
func YAML[T any](c codex.Codec[T]) Format[T] {
	return Format[T]{
		codec:       c,
		contentType: "application/yaml",
		marshal:     func(v any) ([]byte, error) { return yaml.Marshal(v) },
		unmarshal: func(data []byte) (any, error) {
			var v any
			return v, yaml.Unmarshal(data, &v)
		},
	}
}

// TOML returns a Format that reads and writes TOML.
// ContentType is "application/toml".
func TOML[T any](c codex.Codec[T]) Format[T] {
	return Format[T]{
		codec:       c,
		contentType: "application/toml",
		marshal: func(v any) ([]byte, error) {
			var buf bytes.Buffer
			if err := toml.NewEncoder(&buf).Encode(v); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		},
		unmarshal: func(data []byte) (any, error) {
			var v any
			_, err := toml.Decode(string(data), &v)
			if err != nil {
				return nil, fmt.Errorf("toml: %w", err)
			}
			return v, nil
		},
	}
}
