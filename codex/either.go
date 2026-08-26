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

// Left constructs an Either[A, B] holding a left value.
func Left[A, B any](v A) Either[A, B] {
	return Either[A, B]{Left: &v}
}

// Right constructs an Either[A, B] holding a right value.
func Right[A, B any](v B) Either[A, B] {
	return Either[A, B]{Right: &v}
}

// IsLeft reports whether e holds a Left value. A zero-value Either{} (no
// constructor used, no successful decode) reports false for BOTH IsLeft
// and IsRight — an implicitly invalid state per this type's own "Left and
// Right are mutually exclusive" contract; always construct via
// [Left]/[Right]/a successful [Either2] decode.
func (e Either[A, B]) IsLeft() bool { return e.Left != nil }

// IsRight reports whether e holds a Right value. See [Either.IsLeft]'s
// doc comment for the zero-value edge case.
func (e Either[A, B]) IsRight() bool { return e.Right != nil }

// Swap returns a new Either with Left and Right exchanged — a Left value
// becomes a Right value (same underlying data, new position) and vice
// versa. Legal as a method (not a free function like [EitherMapLeft]):
// it reorders the RECEIVER's existing A/B type parameters, introducing no
// new one.
func (e Either[A, B]) Swap() Either[B, A] {
	return Either[B, A]{Left: e.Right, Right: e.Left}
}

// EitherFold reduces e to a single value R by calling onLeft or onRight,
// whichever branch e actually holds — the "pattern match, must handle
// both sides" idiom other FP-flavored Go libraries call Match or Fold. A
// free function, not a method: introduces a new type parameter R the
// receiver's A/B don't have (same constraint documented on [MaybeMap]).
//
// PANICS (nil-pointer dereference) if e is the zero value Either[A,B]{}
// — unlike [Either.IsLeft]/[Either.IsRight], which safely report
// false/false for that same input. Always construct e via [Left]/[Right]/
// a successful [Either2] decode before calling EitherFold.
func EitherFold[A, B, R any](e Either[A, B], onLeft func(A) R, onRight func(B) R) R {
	if e.Left != nil {
		return onLeft(*e.Left)
	}
	return onRight(*e.Right)
}

// EitherMapLeft applies fn to e's Left value (if present), producing a
// new Either[C, B] — a Right value passes through UNTOUCHED (fn is never
// called). A free function: introduces a new type parameter C for the
// transformed Left, which a method on Either[A,B] cannot do.
func EitherMapLeft[A, B, C any](e Either[A, B], fn func(A) C) Either[C, B] {
	if e.Left != nil {
		return Left[C, B](fn(*e.Left))
	}
	return Either[C, B]{Right: e.Right}
}

// EitherMapRight applies fn to e's Right value (if present), producing a
// new Either[A, C] — a Left value passes through UNTOUCHED (fn is never
// called). A free function: introduces a new type parameter C for the
// transformed Right, which a method on Either[A,B] cannot do.
func EitherMapRight[A, B, C any](e Either[A, B], fn func(B) C) Either[A, C] {
	if e.Right != nil {
		return Right[A, C](fn(*e.Right))
	}
	return Either[A, C]{Left: e.Left}
}

// EitherField declares a Struct field of type Either[A, B] — a
// convenience matching [MaybeField]'s call-site shape (name + codec(s) +
// get/set -> FieldCodec[T]), literal sugar for
// RequiredField(name, Either2(ca, cb), get, set).
//
// Always REQUIRED, unlike [MaybeField] — a valid Either always holds
// EXACTLY one of Left/Right (see [Either]'s own "mutually exclusive"
// contract), so there is no natural "absent" state to make optional the
// way Maybe's Nothing is. For an OPTIONAL Either field (may be absent,
// OR Left, OR Right), compose [MaybeField] with [Either2] directly:
// MaybeField(name, Either2(ca, cb), get, set) — Maybe[Either[A,B]].
func EitherField[T any, A, B any](
	name string, ca Codec[A], cb Codec[B],
	get func(T) Either[A, B], set func(*T, Either[A, B]),
) FieldCodec[T] {
	return RequiredField(name, Either2(ca, cb), get, set)
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
