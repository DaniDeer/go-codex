package codex

import "fmt"

// MapCodecSafe creates a new Codec[B] from Codec[A] using two mapping functions.
// from is the encode direction and must always succeed.
// to is the decode direction and may fail.
func MapCodecSafe[A, B any](
	c Codec[A],
	to func(A) B,
	from func(B) (A, error),
) Codec[B] {
	return Codec[B]{
		Encode: func(v B) (any, error) {
			a, err := from(v)
			if err != nil {
				return nil, err
			}
			return c.Encode(a)
		},
		Decode: func(v any) (B, error) {
			a, err := c.Decode(v)
			if err != nil {
				var zero B
				return zero, err
			}
			return to(a), nil
		},
		Schema: c.Schema,
	}
}

// Downcast attempts to cast a value of type B to type A.
// Useful for tagged unions where variants share a common interface.
func Downcast[A any, B any](v B) (A, error) {
	a, ok := any(v).(A)
	if !ok {
		var zero A
		return zero, fmt.Errorf("cannot cast %T", v)
	}
	return a, nil
}
