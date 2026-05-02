package codex

import "github.com/DaniDeer/go-codex/schema"

// Codec encodes values of type T to an intermediate representation,
// decodes that representation back to T, and describes the schema.
type Codec[T any] struct {
	Encode func(T) (any, error)
	Decode func(any) (T, error)
	Schema schema.Schema
}

// WithDescription returns a new Codec with Schema.Description set to desc.
func (c Codec[T]) WithDescription(desc string) Codec[T] {
	c.Schema.Description = desc
	return c
}

// WithTitle returns a new Codec with Schema.Title set to title.
func (c Codec[T]) WithTitle(title string) Codec[T] {
	c.Schema.Title = title
	return c
}

// New validates v and returns it if all constraints pass.
//
// It is a single-call smart constructor: call New to create a validated instance
// of T without separating construction from validation.
// On success it returns (v, nil); on failure it returns (zero, err) where err
// contains the first constraint that failed.
//
// New delegates to Validate internally, so the same Refine constraints and
// encode-direction checks apply.
func (c Codec[T]) New(v T) (T, error) {
	if err := c.Validate(v); err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

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
