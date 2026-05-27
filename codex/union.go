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
				return nil, UnknownVariantError{Tag: tag, Variant: name}
			}

			obj, err := c.Encode(v)
			if err != nil {
				return nil, VariantError{Tag: tag, Variant: name, Err: err}
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
				return zero, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}

			tagVal, _ := obj[tag].(string)
			c, ok := variants[tagVal]
			if !ok {
				return zero, UnknownVariantError{Tag: tag, Variant: tagVal}
			}

			val, err := c.Decode(obj)
			if err != nil {
				return zero, VariantError{Tag: tag, Variant: tagVal, Err: err}
			}
			return val, nil
		},

		Schema: schema.Schema{
			OneOf: oneOf,
			Discriminator: &schema.DiscriminatorSchema{
				PropertyName: tag,
			},
		},
	}
}

func buildUnionSchema[T any](tag string, variants map[string]Codec[T]) []schema.Schema {
	oneOf := make([]schema.Schema, 0, len(variants))
	for name, c := range variants {
		// Deep-copy properties and required to avoid mutating shared schema state.
		props := make([]schema.Property, len(c.Schema.Properties), len(c.Schema.Properties)+1)
		copy(props, c.Schema.Properties)
		req := make([]string, len(c.Schema.Required))
		copy(req, c.Schema.Required)

		s := c.Schema
		s.Properties = append(props, schema.Property{
			Name:   tag,
			Schema: schema.Schema{Type: "string", Enum: []any{name}},
		})
		s.Required = req

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

// UntaggedVariant pairs a name (used in schema documentation) with a Codec[T].
// The name appears in the oneOf schema to identify the branch but is NOT added
// to the encoded value — unlike TaggedUnion which writes a discriminator field.
type UntaggedVariant[T any] struct {
	Name  string
	Codec Codec[T]
}

// UntaggedUnion builds a Codec[T] that tries each variant in order during decode.
//
// Decode strategy: try variants in order; first success wins. If all fail,
// return EitherError listing all branch errors.
//
// Encode strategy: which(v) returns the index (0-based) of the variant to use.
//
// Schema: {oneOf: [...variant schemas...]} — no discriminator field.
//
// Use TaggedUnion when your values carry a type discriminator field.
// Use UntaggedUnion when the shape alone distinguishes variants.
func UntaggedUnion[T any](which func(T) int, variants ...UntaggedVariant[T]) Codec[T] {
	oneOf := make([]schema.Schema, len(variants))
	for i, v := range variants {
		oneOf[i] = v.Codec.Schema
	}

	return Codec[T]{
		Encode: func(v T) (any, error) {
			idx := which(v)
			if idx < 0 || idx >= len(variants) {
				return nil, fmt.Errorf("UntaggedUnion: which() returned out-of-range index %d (have %d variants)", idx, len(variants))
			}
			return variants[idx].Codec.Encode(v)
		},
		Decode: func(v any) (T, error) {
			var zero T
			errs := make([]error, 0, len(variants))
			for _, variant := range variants {
				val, err := variant.Codec.Decode(v)
				if err == nil {
					return val, nil
				}
				errs = append(errs, err)
			}
			return zero, EitherError{Errors: errs}
		},
		Schema: schema.Schema{OneOf: oneOf},
	}
}
