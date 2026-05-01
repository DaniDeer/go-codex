// Package format bridges Codec[T] to concrete serialization formats (JSON, YAML, TOML).
//
// A codec works with an intermediate representation (map[string]any) that is
// format-agnostic. Format wraps that intermediate layer so the same codec can
// read and write multiple wire formats without any changes to the codec itself.
package format

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// Format binds a Codec[T] to a specific serialization format.
// Use JSON, YAML, or TOML to construct one.
type Format[T any] struct {
	codec     codex.Codec[T]
	marshal   func(any) ([]byte, error)
	unmarshal func([]byte) (any, error)
}

// New creates a Format from a codec and custom marshal/unmarshal functions.
// Use this to integrate formats not covered by the built-in constructors.
func New[T any](c codex.Codec[T], marshal func(any) ([]byte, error), unmarshal func([]byte) (any, error)) Format[T] {
	return Format[T]{codec: c, marshal: marshal, unmarshal: unmarshal}
}

// Marshal encodes v to bytes using the codec and then the format serializer.
func (f Format[T]) Marshal(v T) ([]byte, error) {
	intermediate, err := f.codec.Encode(v)
	if err != nil {
		return nil, err
	}
	return f.marshal(intermediate)
}

// Unmarshal deserializes data into an intermediate and then decodes it via the codec.
func (f Format[T]) Unmarshal(data []byte) (T, error) {
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
func JSON[T any](c codex.Codec[T]) Format[T] {
	return New(c,
		func(v any) ([]byte, error) { return json.Marshal(v) },
		func(data []byte) (any, error) {
			var v any
			return v, json.Unmarshal(data, &v)
		},
	)
}

// YAML returns a Format that reads and writes YAML.
func YAML[T any](c codex.Codec[T]) Format[T] {
	return New(c,
		func(v any) ([]byte, error) { return yaml.Marshal(v) },
		func(data []byte) (any, error) {
			var v any
			return v, yaml.Unmarshal(data, &v)
		},
	)
}

// TOML returns a Format that reads and writes TOML.
func TOML[T any](c codex.Codec[T]) Format[T] {
	return New(c,
		func(v any) ([]byte, error) {
			var buf bytes.Buffer
			if err := toml.NewEncoder(&buf).Encode(v); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		},
		func(data []byte) (any, error) {
			var v any
			_, err := toml.Decode(string(data), &v)
			if err != nil {
				return nil, fmt.Errorf("toml: %w", err)
			}
			return v, nil
		},
	)
}
