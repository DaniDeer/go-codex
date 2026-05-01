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
					return nil, fmt.Errorf("key %q: %w", k, err)
				}
				out[k] = enc
			}
			return out, nil
		},
		Decode: func(v any) (map[string]V, error) {
			raw, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected object, got %T", v)
			}
			out := make(map[string]V, len(raw))
			for k, item := range raw {
				decoded, err := value.Decode(item)
				if err != nil {
					return nil, fmt.Errorf("key %q: %w", k, err)
				}
				out[k] = decoded
			}
			return out, nil
		},
	}
}
