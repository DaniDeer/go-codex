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
