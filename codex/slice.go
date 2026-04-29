package codex

import (
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
)

// SliceOf returns a Codec for a slice of T, using elem to encode/decode each element.
func SliceOf[T any](elem Codec[T]) Codec[[]T] {
	itemSchema := elem.Schema
	return Codec[[]T]{
		Schema: schema.Schema{Type: "array", Items: &itemSchema},
		Encode: func(vs []T) (any, error) {
			out := make([]any, len(vs))
			for i, v := range vs {
				enc, err := elem.Encode(v)
				if err != nil {
					return nil, fmt.Errorf("element %d: %w", i, err)
				}
				out[i] = enc
			}
			return out, nil
		},
		Decode: func(v any) ([]T, error) {
			raw, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array, got %T", v)
			}
			out := make([]T, len(raw))
			for i, item := range raw {
				decoded, err := elem.Decode(item)
				if err != nil {
					return nil, fmt.Errorf("element %d: %w", i, err)
				}
				out[i] = decoded
			}
			return out, nil
		},
	}
}
