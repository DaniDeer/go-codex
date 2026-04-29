package codex

import (
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
)

// Constraint is a named validation predicate applied during decoding.
//
// The optional Schema field annotates the codec's schema when the constraint
// is applied via Refine. Set it to propagate constraint metadata (e.g. minimum
// length, numeric bounds) into the schema for renderers such as render/openapi.
// Leaving Schema nil is a no-op and keeps all existing constraints unchanged.
type Constraint[T any] struct {
	Name    string
	Check   func(T) bool
	Message func(T) string
	Schema  func(schema.Schema) schema.Schema // optional: mutates schema when Refine is applied
}

// Refine wraps the codec with a single constraint checked during Decode.
// If cons.Schema is non-nil, it is applied to the codec's schema.
func (c Codec[T]) Refine(cons Constraint[T]) Codec[T] {
	s := c.Schema
	if cons.Schema != nil {
		s = cons.Schema(s)
	}
	return Codec[T]{
		Encode: c.Encode,
		Decode: func(v any) (T, error) {
			val, err := c.Decode(v)
			if err != nil {
				var zero T
				return zero, err
			}
			if !cons.Check(val) {
				var zero T
				return zero, fmt.Errorf(
					"constraint failed (%s): %s",
					cons.Name,
					cons.Message(val),
				)
			}
			return val, nil
		},
		Schema: s,
	}
}

// Refine applies multiple constraints to a codec.
func Refine[T any](c Codec[T], constraints ...Constraint[T]) Codec[T] {
	for _, cons := range constraints {
		c = c.Refine(cons)
	}
	return c
}
