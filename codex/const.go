package codex

import (
	"fmt"
	"log/slog"
	"sync"
)

// ── Const ────────────────────────────────────────────────────────────────
//
// Const/Immutable are the first two consumers of [Getter]/[Setter]
// (declared in getter.go) — see that file's own doc comment for how
// this whole family relates to Codec[T]/HasCodec[T]. Nothing new is
// invented at the codec level here: both [MustConst] and
// [Immutable.Set] validate their incoming value via the SAME
// Codec[T].Validate every other part of go-codex already uses.

// Const is an immutable, validated constant of type T. The underlying
// value is unexported — Get() is the ONLY way to read it. Construct via
// [MustConst], never a struct literal (the zero value has no validated
// contents and is not a meaningful Const). Implements ONLY Getter[T].
//
// A Const is always compile-time-authored (a package-level var, e.g. a
// path-pattern template or a protocol version constant) — safe to copy
// freely, since it is fully valid for its entire lifetime.
type Const[T any] struct {
	value T
}

// MustConst validates value against codec and returns a Const[T],
// PANICKING if validation fails. A Const is always compile-time-
// authored — an invalid one is a programming error that should fail
// LOUDLY and IMMEDIATELY (at package init / first use), not be silently
// propagated as a runtime error a caller has to check. This mirrors
// [Must]/[Must2]'s existing panic-on-invalid convention for exactly this
// "the input is not really dynamic, it's authored" case.
func MustConst[T any](value T, codec Codec[T]) Const[T] {
	if err := codec.Validate(value); err != nil {
		panic(fmt.Sprintf("codex.MustConst: invalid constant %v: %v", value, err))
	}
	return Const[T]{value: value}
}

// Get returns the validated value — Const[T] satisfies Getter[T].
func (c Const[T]) Get() T { return c.value }

// String implements fmt.Stringer for ANY T via fmt.Sprint — a
// Const[string] (the common case, e.g. a path-pattern template) prints
// its plain text directly; a Const[int]/Const[MyEnum] still gets a
// sensible default representation with zero extra code.
func (c Const[T]) String() string { return fmt.Sprint(c.value) }

// ── Immutable ────────────────────────────────────────────────────────────

// Immutable is a runtime value set EXACTLY ONCE and validated against a
// Codec[T] at that one Set call — the "config/env var loaded once at
// startup, then read-only for the rest of the process" shape. Unlike
// [Const] (compile-time-authored, always valid, freely copyable),
// Immutable[T] has a genuine "not yet set" lifecycle state before the
// first Set, so it is used via a pointer and guards its internal state
// with a mutex. Construct via [NewImmutable], not a struct literal, so
// the codec is always present.
type Immutable[T any] struct {
	mu    sync.Mutex
	value T
	set   bool
	codec Codec[T]
}

// NewImmutable returns an unset Immutable[T] validated against codec —
// call [Immutable.Set] exactly once before any [Immutable.Get].
func NewImmutable[T any](codec Codec[T]) *Immutable[T] {
	return &Immutable[T]{codec: codec}
}

// Set validates value against the Immutable's codec and stores it,
// PERMANENTLY locking further Set calls. Returns
// [ImmutableAlreadySetError] if already set (an external caller's bug —
// e.g. loading config twice), or the codec's own validation error if
// value is invalid. Real runtime input (unlike Const's authored value),
// so this returns an error rather than panicking.
func (im *Immutable[T]) Set(value T) error {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.set {
		return ImmutableAlreadySetError{}
	}
	if err := im.codec.Validate(value); err != nil {
		return err
	}
	im.value, im.set = value, true
	return nil
}

// Get returns the set value — PANICS if called before any successful
// Set (a real bug: reading config before startup finished loading it).
// Satisfies Getter[T], mirroring [Const.Get]'s signature exactly.
func (im *Immutable[T]) Get() T {
	im.mu.Lock()
	defer im.mu.Unlock()
	if !im.set {
		panic("codex: Immutable[T].Get called before Set")
	}
	return im.value
}

// TryGet is Get's safe/optional counterpart — returns (value, true) if
// set, or (zero, false) if not, never panicking.
func (im *Immutable[T]) TryGet() (T, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.value, im.set
}

// ImmutableAlreadySetError is returned by [Immutable.Set] when called
// more than once on the same Immutable[T].
type ImmutableAlreadySetError struct{}

func (ImmutableAlreadySetError) Error() string { return "codex: Immutable already set" }

// LogValue implements slog.LogValuer for structured logging (empty
// group — this error carries no fields beyond the fact itself).
func (ImmutableAlreadySetError) LogValue() slog.Value { return slog.GroupValue() }
