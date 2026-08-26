package codex

// ── Getter / Setter ─────────────────────────────────────────────────────────
//
// Getter/Setter are a NEW layer sitting on top of the existing Codec[T]/
// HasCodec[T] layers: Codec[T].Encode/Decode validate a WIRE shape on
// every call; HasCodec[T] lets a TYPE declare its own Codec; Getter[T]/
// Setter[T] describe a VALUE CONTAINER whose identity is validated and
// then frozen (or reassignable, per the container's own rules) at a
// specific point in its OWN lifecycle — not on every individual
// encode/decode call. This file is intentionally SEPARATE from any one
// container implementation ([Const]/[Immutable] in const.go are the
// first two) — any FUTURE codec-backed value container (e.g. a
// hypothetical hot-reloadable Mutable[T]) is expected to satisfy these
// same two interfaces, so the interfaces themselves stay decoupled from
// any particular implementation's file.

// Getter is implemented by any type that exposes a single, read-only
// value of type T — the minimal contract [Const] and [Immutable] both
// satisfy. Deliberately ONE method, mirroring [HasCodec]'s own
// minimalism — a caller who only needs "give me the validated T" can
// depend on Getter[T] instead of a concrete container type.
type Getter[T any] interface {
	Get() T
}

// Setter is the write-side counterpart to Getter — implemented by any
// type that accepts a validated assignment of T, fallibly (the
// assignment itself may be invalid per some Codec[T], or may be
// rejected for a lifecycle reason like "already set"). Deliberately the
// mirror image of Getter[T]'s single-method minimalism.
type Setter[T any] interface {
	Set(T) error
}

// GetterSetter is the natural combination for a caller that wants to
// depend on "a validated, readable-AND-writable cell of T" without
// naming the concrete container type. [Const] implements ONLY
// Getter[T] (a Const's value is fixed forever at construction, so there
// is no runtime "assign" to expose) — [Immutable] and [Mutable] both
// implement GetterSetter[T].
type GetterSetter[T any] interface {
	Getter[T]
	Setter[T]
}

// FreshGetter is a THIRD sibling to Getter/GetterSetter — for a
// container whose Get must also report whether the returned value is
// still trustworthy (a TTL/validity-window concept, distinct from
// Getter[T]'s "always current" contract). [Cacheable] is the first
// concrete implementation: [Cacheable.Get]'s two-value return
// (`(T, bool)`) does NOT satisfy Getter[T]'s single-value `Get() T`
// signature, so this narrower interface exists instead of forcing
// Cacheable's shape onto Getter[T] or silently dropping the freshness
// flag. Not embedded in GetterSetter — a FreshGetter container's
// write side ([Cacheable.Set]) still satisfies plain [Setter][T]
// unchanged.
type FreshGetter[T any] interface {
	Get() (T, bool)
}
