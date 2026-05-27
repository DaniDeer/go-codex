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
				return zero, ConstraintError{
					Name:    cons.Name,
					Message: cons.Message(val),
				}
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

// RefineFunc wraps the codec with a constraint expressed as a function returning an error.
// If fn returns nil the value passes; if fn returns an error it becomes a ConstraintError.
//
// This is the idiomatic way to add cross-field constraints to a struct codec:
//
//	var rangeCodec = codex.Struct[DateRange](...).
//	    RefineFunc(func(r DateRange) error {
//	        if !r.End.After(r.Start) {
//	            return errors.New("end must be after start")
//	        }
//	        return nil
//	    })
func (c Codec[T]) RefineFunc(fn func(T) error) Codec[T] {
	return Codec[T]{
		Encode: c.Encode,
		Decode: func(v any) (T, error) {
			val, err := c.Decode(v)
			if err != nil {
				var zero T
				return zero, err
			}
			if err := fn(val); err != nil {
				var zero T
				return zero, ConstraintError{Name: "refine", Message: err.Error()}
			}
			return val, nil
		},
		Schema: c.Schema,
	}
}

// Eq wraps base with a constraint that only accepts value.
// Decode: base decodes the wire value, then equality is checked.
// Encode: equality is checked before encoding via base.
//
// Using a base codec handles wire-type coercion: Eq(Int(), 42) correctly
// accepts the JSON number 42 (arrives as float64) because Int() converts it first.
//
// The schema is inherited from base with Enum set to [value].
func Eq[T comparable](base Codec[T], value T) Codec[T] {
	return base.Refine(Constraint[T]{
		Name:  fmt.Sprintf("eq(%v)", value),
		Check: func(v T) bool { return v == value },
		Message: func(v T) string {
			return fmt.Sprintf("expected %v, got %v", value, v)
		},
		Schema: func(s schema.Schema) schema.Schema {
			s.Enum = []any{value}
			return s
		},
	})
}
