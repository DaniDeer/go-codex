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

// maybeFieldCodec wraps inner so Decode produces Just(value) and Encode
// unwraps via .Get() — Encode is only ever reached for a Just, since
// sparseField.encodeSparse already skips codec.Encode entirely when
// isEmpty (i.e. Nothing) is true. Deliberately UNEXPORTED for now — this
// is 90% of a general-purpose public codex.Codec[Maybe[T]], intentionally
// deferred (see docs/roadmap/maybe-nullable-and-codec.md).
func maybeFieldCodec[V any](inner Codec[V]) Codec[Maybe[V]] {
	return Codec[Maybe[V]]{
		Schema: inner.Schema,
		Encode: func(m Maybe[V]) (any, error) {
			return inner.Encode(m.Get())
		},
		Decode: func(v any) (Maybe[V], error) {
			val, err := inner.Decode(v)
			if err != nil {
				return Maybe[V]{}, err
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
// guide for the full comparison.
func MaybeField[T any, V any](
	name string, codec Codec[V],
	get func(T) Maybe[V], set func(*T, Maybe[V]),
) FieldCodec[T] {
	return sparseField[T, Maybe[V]]{
		name:  name,
		codec: maybeFieldCodec(codec),
		get:   get,
		set:   set,
		isEmpty: func(m Maybe[V]) bool {
			return !m.IsSet()
		},
	}
}
