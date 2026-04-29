package codex

import "github.com/DaniDeer/go-codex/schema"

// Codec encodes values of type T to an intermediate representation,
// decodes that representation back to T, and describes the schema.
type Codec[T any] struct {
	Encode func(T) (any, error)
	Decode func(any) (T, error)
	Schema schema.Schema
}

// Validate checks v against the codec's constraints without persisting the result.
//
// It encodes v to the intermediate representation and decodes it back, running
// all Refine constraints defined on the codec. This reuses the exact same
// constraint logic as Decode — builtin constraints (via validate.*) and any
// self-defined Constraint[T] values work without modification.
//
// The encode direction is intentionally unconstrained (you constructed the value
// yourself). Call Validate explicitly when you want bidirectional enforcement.
func (c Codec[T]) Validate(v T) error {
	intermediate, err := c.Encode(v)
	if err != nil {
		return err
	}
	_, err = c.Decode(intermediate)
	return err
}
