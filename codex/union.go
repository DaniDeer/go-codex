package codex

import (
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
)

// TaggedUnion builds a Codec[T] for a discriminated union identified by a tag field.
func TaggedUnion[T any](
	tag string,
	variants map[string]Codec[T],
	selectVariant func(T) (string, error),
) Codec[T] {
	oneOf := buildUnionSchema(tag, variants)

	return Codec[T]{
		Encode: func(v T) (any, error) {
			name, err := selectVariant(v)
			if err != nil {
				return nil, fmt.Errorf("selecting variant: %w", err)
			}

			c, ok := variants[name]
			if !ok {
				return nil, fmt.Errorf("unknown variant %q", name)
			}

			obj, err := c.Encode(v)
			if err != nil {
				return nil, fmt.Errorf("encoding variant %q: %w", name, err)
			}

			m, ok := obj.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("variant %q encoder must return an object, got %T", name, obj)
			}

			result := make(map[string]any, len(m)+1)
			for k, v := range m {
				result[k] = v
			}
			result[tag] = name
			return result, nil
		},

		Decode: func(v any) (T, error) {
			var zero T

			obj, ok := v.(map[string]any)
			if !ok {
				return zero, fmt.Errorf("expected object, got %T", v)
			}

			tagVal, _ := obj[tag].(string)
			c, ok := variants[tagVal]
			if !ok {
				return zero, fmt.Errorf("field %s: unknown variant %q", tag, tagVal)
			}

			val, err := c.Decode(obj)
			if err != nil {
				return zero, fmt.Errorf("decoding variant %q: %w", tagVal, err)
			}
			return val, nil
		},

		Schema: schema.Schema{OneOf: oneOf},
	}
}

func buildUnionSchema[T any](tag string, variants map[string]Codec[T]) []schema.Schema {
	oneOf := make([]schema.Schema, 0, len(variants))
	for name, c := range variants {
		// Deep-copy properties and required to avoid mutating shared schema state.
		props := make(map[string]schema.Schema, len(c.Schema.Properties)+1)
		for k, v := range c.Schema.Properties {
			props[k] = v
		}
		req := make([]string, len(c.Schema.Required))
		copy(req, c.Schema.Required)

		s := c.Schema
		s.Properties = props
		s.Required = req

		s.Properties[tag] = schema.Schema{
			Type: "string",
			Enum: []any{name},
		}
		found := false
		for _, r := range s.Required {
			if r == tag {
				found = true
				break
			}
		}
		if !found {
			s.Required = append(s.Required, tag)
		}
		oneOf = append(oneOf, s)
	}
	return oneOf
}
