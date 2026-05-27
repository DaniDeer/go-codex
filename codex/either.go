package codex

import "github.com/DaniDeer/go-codex/schema"

// Either holds exactly one of two values. Left and Right are mutually exclusive:
// a value decoded from the left branch sets Left to a non-nil pointer and
// leaves Right nil, and vice versa.
//
// Use a type switch or check Left/Right directly:
//
//	switch {
//	case e.Left != nil:
//	    // handle *e.Left (type A)
//	case e.Right != nil:
//	    // handle *e.Right (type B)
//	}
type Either[A, B any] struct {
	Left  *A
	Right *B
}

// Either2 returns a Codec[Either[A, B]] that tries ca first, then cb.
//
// Decode strategy:
//   - Try ca.Decode(v). If it succeeds, return Either{Left: &a}.
//   - Otherwise try cb.Decode(v). If it succeeds, return Either{Right: &b}.
//   - If both fail, return EitherError listing both errors.
//
// Encode strategy:
//   - If Left != nil, use ca.Encode(*Left).
//   - Otherwise use cb.Encode(*Right).
//
// Schema: {oneOf: [schemaA, schemaB]}
func Either2[A, B any](ca Codec[A], cb Codec[B]) Codec[Either[A, B]] {
	return Codec[Either[A, B]]{
		Encode: func(e Either[A, B]) (any, error) {
			if e.Left != nil {
				return ca.Encode(*e.Left)
			}
			return cb.Encode(*e.Right)
		},
		Decode: func(v any) (Either[A, B], error) {
			if a, err := ca.Decode(v); err == nil {
				return Either[A, B]{Left: &a}, nil
			} else if b, err2 := cb.Decode(v); err2 == nil {
				return Either[A, B]{Right: &b}, nil
			} else {
				return Either[A, B]{}, EitherError{Errors: []error{err, err2}}
			}
		},
		Schema: schema.Schema{
			OneOf: []schema.Schema{ca.Schema, cb.Schema},
		},
	}
}
