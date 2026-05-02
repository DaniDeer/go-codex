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

// MapCodecValidated creates a Codec[B] from Codec[A] and Codec[B] using two fallible mapping functions.
//
// Both directions may return an error. After mapping to B in the decode direction,
// cb.Validate is called to enforce all Refine constraints defined on cb.
// The resulting codec carries cb's schema.
//
// Use MapCodecValidated when the mapping itself can fail and the target type B has
// its own validation constraints expressed via Refine. For a simpler case where only
// the encode direction can fail and no post-mapping validation is needed, use MapCodecSafe.
func MapCodecValidated[A, B any](
	ca Codec[A],
	cb Codec[B],
	to func(A) (B, error),
	from func(B) (A, error),
) Codec[B] {
	return Codec[B]{
		Schema: cb.Schema,
		Decode: func(v any) (B, error) {
			var zero B
			a, err := ca.Decode(v)
			if err != nil {
				return zero, err
			}
			b, err := to(a)
			if err != nil {
				return zero, err
			}
			if err := cb.Validate(b); err != nil {
				return zero, err
			}
			return b, nil
		},
		Encode: func(v B) (any, error) {
			if err := cb.Validate(v); err != nil {
				return nil, err
			}
			a, err := from(v)
			if err != nil {
				return nil, err
			}
			return ca.Encode(a)
		},
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
