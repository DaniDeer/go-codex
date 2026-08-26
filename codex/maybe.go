package codex

// This file implements codex's presence-tracking primitive: Maybe[T] —
// go-codex's Go rendering of Haskell's Maybe/Rust's Option — plus the
// MaybeField constructor that plugs it into Struct. See
// docs/concepts/codec.md's "Maybe[T]: definitive presence tracking"
// subsection for the full design rationale, including why OmitEmptyField
// stays the right default for the common case and why PartialField's
// pointer-based approach isn't simply reused here.
//
// MaybeField is built directly on omitempty.go's sparseField[T,F] —
// Maybe[V].IsSet() is a perfect isEmpty predicate, so no changes to
// object.go/Struct are needed at all; the sparseFieldCodec type-assertion
// hook added for OmitEmptyField already covers this constructor too.

// Maybe pairs a value with an explicit "was this ever Set" bit — unlike
// [OmitEmptyField]'s zero-value HEURISTIC, this is a DEFINITIVE presence
// signal, and unlike a bare pointer (as [PartialField] uses), it carries
// T BY VALUE inside the wrapper — no extra heap allocation, no aliasing,
// and no collision with [Nullable]'s existing "wire null" meaning for
// *T. Construct via [Just]/[Nothing], or simply declare a zero-initialized
// Maybe[T] field (equivalent to [Nothing]).
type Maybe[T any] struct {
	value T
	set   bool
}

// Just constructs an already-set Maybe[T] — the "Just(v)" constructor.
func Just[T any](v T) Maybe[T] {
	return Maybe[T]{value: v, set: true}
}

// Nothing returns an unset Maybe[T] — equivalent to the zero value
// Maybe[T]{}, provided as a named, self-documenting alternative (mirrors
// Haskell's Nothing).
func Nothing[T any]() Maybe[T] {
	return Maybe[T]{}
}

// Set stores value and marks this Maybe as having been set — REPEATABLE:
// every call overwrites, no "already set" failure exists (contrast with
// [Immutable.Set]'s exactly-once contract).
func (m *Maybe[T]) Set(value T) {
	m.value, m.set = value, true
}

// Get returns the current value — T's zero value if never Set (Nothing),
// NEVER panics (contrast with [Immutable.Get], which panics before the
// first Set).
func (m Maybe[T]) Get() T { return m.value }

// IsSet reports whether Set/Just has ever produced this value.
func (m Maybe[T]) IsSet() bool { return m.set }

// TryGet is Get's explicit-presence-check sibling — (value, true) if
// ever set, (zero, false) otherwise. Mirrors [Immutable.TryGet]'s shape.
func (m Maybe[T]) TryGet() (T, bool) { return m.value, m.set }

// MaybeMap applies fn to the contained value IF set, returning a new
// Maybe[R] — Nothing in, Nothing out (fn is never called when unset). A
// free function, not a method: Go generic methods cannot introduce a new
// type parameter (R, distinct from Maybe[T]'s own T) — the same
// constraint documented elsewhere in this codebase for
// forge.NewFunction[In, Out]. Named MaybeMap (not the shorter Map) to
// avoid colliding with the existing codex.Map[K,V] map-codec
// constructor.
func MaybeMap[T, R any](m Maybe[T], fn func(T) R) Maybe[R] {
	if !m.IsSet() {
		return Maybe[R]{}
	}
	return Just(fn(m.Get()))
}

// MaybeFlatMap applies fn to the contained value IF set, returning
// whatever Maybe[R] fn itself produces — Nothing in, Nothing out; a Just
// in can still produce Nothing out if fn itself does (the "chain a
// possibly-failing transformation" case MaybeMap alone can't express,
// since MaybeMap's fn always returns a plain R, never a Maybe[R]).
// Haskell's >>=/Rust's and_then. A free function, not a method — same
// constraint as MaybeMap (Go generic methods cannot introduce a new type
// parameter R).
func MaybeFlatMap[T, R any](m Maybe[T], fn func(T) Maybe[R]) Maybe[R] {
	if !m.IsSet() {
		return Maybe[R]{}
	}
	return fn(m.Get())
}

// OrElse returns the contained value if set, or fallback otherwise — the
// safe-default-value idiom (Rust's unwrap_or, Haskell's fromMaybe).
func (m Maybe[T]) OrElse(fallback T) T {
	if m.set {
		return m.value
	}
	return fallback
}

// Filter returns m unchanged if set AND pred(value) is true; otherwise
// returns Nothing[T]() — lets a caller narrow "set" down to "set AND
// satisfies some condition" without a separate IsSet+manual-check dance.
func (m Maybe[T]) Filter(pred func(T) bool) Maybe[T] {
	if m.set && pred(m.value) {
		return m
	}
	return Maybe[T]{}
}

// MaybeCodec derives a Codec[Maybe[T]] from inner — Decode wraps a
// successful inner.Decode as Just(value); Encode unwraps via .Get()
// (ALWAYS calling inner.Encode, even for a Nothing — Nothing's Get()
// simply returns T's zero value, so it renders as inner's own zero-value
// wire form, exactly like OptionalField's existing "always shown"
// contract for any type). This is the SAME symmetric role [Either2]
// already plays for [Either][A, B] — a plain codec derived from inner
// codec(s), usable with ANY composer (RequiredField, OptionalField,
// SliceOf, Map, or standalone), not just [MaybeField].
//
// Trade-off vs. [MaybeField]: MaybeCodec alone does NOT omit a Nothing's
// key on Encode — use [MaybeField] instead when you specifically want
// the omit-on-Nothing behavior. In fact, MaybeField(name, codec, get,
// set) is EXACTLY EQUIVALENT to:
//
//	OmitEmptyFieldFunc(name, MaybeCodec(codec), get, set,
//	    func(m Maybe[V]) bool { return !m.IsSet() })
//
// (documented here, not literally rewritten that way internally — both
// forms are proven identical by TestMaybeField_EquivalentToOmitEmptyFieldFuncPlusMaybeCodec).
func MaybeCodec[T any](inner Codec[T]) Codec[Maybe[T]] {
	return Codec[Maybe[T]]{
		Schema: inner.Schema,
		Encode: func(m Maybe[T]) (any, error) {
			return inner.Encode(m.Get())
		},
		Decode: func(v any) (Maybe[T], error) {
			val, err := inner.Decode(v)
			if err != nil {
				return Maybe[T]{}, err
			}
			return Just(val), nil
		},
	}
}

// MaybeField declares a Struct field backed by Maybe[V] — decodes like
// OptionalField (absent key -> Maybe[V]{}/Nothing), but Encode OMITS the
// key based on Maybe[V].IsSet() DIRECTLY — no zero-value guessing, ever.
// Use this instead of [OmitEmptyField]/[OmitEmptyFieldFunc] ONLY when you
// genuinely need to distinguish "never set" from "deliberately set to the
// zero value" for this field — see docs/concepts/codec.md's decision
// guide for the full comparison. See [MaybeCodec]'s own doc comment for
// the exact composition MaybeField is equivalent to.
func MaybeField[T any, V any](
	name string, codec Codec[V],
	get func(T) Maybe[V], set func(*T, Maybe[V]),
) FieldCodec[T] {
	return sparseField[T, Maybe[V]]{
		name:  name,
		codec: MaybeCodec(codec),
		get:   get,
		set:   set,
		isEmpty: func(m Maybe[V]) bool {
			return !m.IsSet()
		},
	}
}
