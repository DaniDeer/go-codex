package codex

import (
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
)

// StringMap returns a Codec for map[string]V, using value to encode/decode each entry.
// The generated schema is an object with additionalProperties set to the value codec's schema.
func StringMap[V any](value Codec[V]) Codec[map[string]V] {
	valueSchema := value.Schema
	return Codec[map[string]V]{
		Schema: schema.Schema{
			Type:                       "object",
			AdditionalPropertiesSchema: &valueSchema,
		},
		Encode: func(m map[string]V) (any, error) {
			out := make(map[string]any, len(m))
			for k, v := range m {
				enc, err := value.Encode(v)
				if err != nil {
					return nil, KeyError{Key: k, Err: err}
				}
				out[k] = enc
			}
			return out, nil
		},
		Decode: func(v any) (map[string]V, error) {
			raw, ok := v.(map[string]any)
			if !ok {
				return nil, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
			out := make(map[string]V, len(raw))
			for k, item := range raw {
				decoded, err := value.Decode(item)
				if err != nil {
					return nil, KeyError{Key: k, Err: err}
				}
				out[k] = decoded
			}
			return out, nil
		},
	}
}

// EntrySlice[K, V, R any] decodes a JSON/YAML/TOML object by merging each entry's decoded
// key and value into a single element of type R. The result is Codec[[]R].
//
// keyCodec decodes each string key on the wire into K and validates it.
// valueCodec decodes each object value into V.
// merge combines a decoded (K, V) pair into R — called on decode. It is infallible:
// (K, V) are already validated by their codecs. Chain [Codec.RefineFunc] on the result
// if you need cross-field constraints on R.
// split extracts (K, V) from R — called on encode. keyCodec.Encode(K) must produce
// a string; if it does not, the entry is reported as [KeyError].
//
// Typical use case — a JSON object whose key encodes a domain identifier:
//
//	{"properties.desired.modules.cv-writer": {"image": "...", "status": "running"}}
//
// With EntrySlice, this decodes directly into []Container where Container.Name is
// extracted from the key by the key codec (e.g. via [MapCodecValidated] to strip a prefix).
//
// All key errors are reported as [KeyError]{Key, Err}. The order of elements in the
// decoded slice is non-deterministic (JSON/YAML/TOML object key order is not guaranteed).
// Sort the slice after decode if order matters.
//
// Wire format compatibility: works with JSON, YAML (quoted keys), and TOML (quoted
// table headers). TOML bare dotted keys (e.g. [properties.desired]) produce nested
// map objects, not flat string keys — use quoted headers instead.
//
// Schema: identical to [Map] — an object schema with propertyNames for the key
// constraint and additionalProperties for the value schema.
func EntrySlice[K comparable, V, R any](
	keyCodec Codec[K],
	valueCodec Codec[V],
	merge func(K, V) R,
	split func(R) (K, V),
) Codec[[]R] {
	keySchema := keyCodec.Schema
	valueSchema := valueCodec.Schema
	return Codec[[]R]{
		Schema: schema.Schema{
			Type:                       "object",
			PropertyNames:              &keySchema,
			AdditionalPropertiesSchema: &valueSchema,
		},
		Decode: func(v any) ([]R, error) {
			raw, ok := v.(map[string]any)
			if !ok {
				return nil, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
			out := make([]R, 0, len(raw))
			for strKey, item := range raw {
				k, err := keyCodec.Decode(strKey)
				if err != nil {
					return nil, KeyError{Key: strKey, Err: err}
				}
				val, err := valueCodec.Decode(item)
				if err != nil {
					return nil, KeyError{Key: strKey, Err: err}
				}
				out = append(out, merge(k, val))
			}
			return out, nil
		},
		Encode: func(slice []R) (any, error) {
			out := make(map[string]any, len(slice))
			for _, r := range slice {
				k, v := split(r)
				rawKey, err := keyCodec.Encode(k)
				if err != nil {
					return nil, KeyError{Key: fmt.Sprintf("%v", k), Err: err}
				}
				strKey, ok := rawKey.(string)
				if !ok {
					return nil, KeyError{
						Key: fmt.Sprintf("%v", k),
						Err: fmt.Errorf("key codec must encode to string, got %T", rawKey),
					}
				}
				enc, err := valueCodec.Encode(v)
				if err != nil {
					return nil, KeyError{Key: strKey, Err: err}
				}
				out[strKey] = enc
			}
			return out, nil
		},
	}
}

// Map returns a Codec for map[K]V, using keyCodec to validate/encode/decode map keys and
// valueCodec for values. K must encode to a string — JSON and YAML require string map keys.
//
// Key errors are reported as KeyError{Key, Err}. Non-string key encoding produces a clear error.
// The generated schema uses "propertyNames" for the key constraint and "additionalProperties"
// for the value schema.
func Map[K comparable, V any](keyCodec Codec[K], valueCodec Codec[V]) Codec[map[K]V] {
	keySchema := keyCodec.Schema
	valueSchema := valueCodec.Schema
	return Codec[map[K]V]{
		Schema: schema.Schema{
			Type:                       "object",
			PropertyNames:              &keySchema,
			AdditionalPropertiesSchema: &valueSchema,
		},
		Encode: func(m map[K]V) (any, error) {
			out := make(map[string]any, len(m))
			for k, v := range m {
				rawKey, err := keyCodec.Encode(k)
				if err != nil {
					return nil, KeyError{Key: fmt.Sprintf("%v", k), Err: err}
				}
				strKey, ok := rawKey.(string)
				if !ok {
					return nil, KeyError{
						Key: fmt.Sprintf("%v", k),
						Err: fmt.Errorf("key codec must encode to string, got %T", rawKey),
					}
				}
				enc, err := valueCodec.Encode(v)
				if err != nil {
					return nil, KeyError{Key: strKey, Err: err}
				}
				out[strKey] = enc
			}
			return out, nil
		},
		Decode: func(v any) (map[K]V, error) {
			raw, ok := v.(map[string]any)
			if !ok {
				return nil, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
			out := make(map[K]V, len(raw))
			for strKey, item := range raw {
				decodedKey, err := keyCodec.Decode(strKey)
				if err != nil {
					return nil, KeyError{Key: strKey, Err: err}
				}
				decodedVal, err := valueCodec.Decode(item)
				if err != nil {
					return nil, KeyError{Key: strKey, Err: err}
				}
				out[decodedKey] = decodedVal
			}
			return out, nil
		},
	}
}
