package codex

import "fmt"

// Constraint is a named validation predicate applied during decoding.
type Constraint[T any] struct {
	Name    string
	Check   func(T) bool
	Message func(T) string
}

// Refine wraps the codec with a single constraint checked during Decode.
func (c Codec[T]) Refine(cons Constraint[T]) Codec[T] {
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
		Schema: c.Schema,
	}
}

// Refine applies multiple constraints to a codec.
func Refine[T any](c Codec[T], constraints ...Constraint[T]) Codec[T] {
	for _, cons := range constraints {
		c = c.Refine(cons)
	}
	return c
}
