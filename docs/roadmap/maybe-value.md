# `codex.Maybe[T]`: Definitive Presence Tracking for a Single Field

> **Status:** Design complete — not yet implemented; awaiting a decision on
> the open questions below before proceeding.
> [← Back to Roadmap](index.md)

## Motivation

`codex.OmitEmptyField`/`OmitEmptyFieldFunc` (shipped) solve "omit a
zero-valued field on Encode" via a **heuristic**: compare the current value
to Go's zero value (or a custom predicate). This heuristic is fundamentally
**unable to distinguish** two states that are byte-identical in Go's memory
model:

- "This field was never touched" (a freshly-constructed `Service{}`, or a
  decode that never saw this wire key).
- "This field was explicitly set to exactly the zero-equivalent value" (a
  caller who genuinely means `Retries: 0`, `Nick: ""`).

This ambiguity isn't specific to `dockercompose` — it applies to **every**
codec-defined struct field in go-codex using `OptionalField`/`DefaultField`/
`OmitEmptyField`, any time the zero value could conceivably be a deliberate
choice rather than an absence. `PartialField`/`PartialStruct` already solve
this DEFINITIVELY via pointers (`nil` vs. non-nil is unambiguous) but
require reshaping an ENTIRE struct to all-pointer fields.

`codex.Maybe[T]` — go-codex's Go rendering of Haskell's `Maybe`/Rust's
`Option` — is a lightweight, **per-field** alternative: a small value type
that pairs a `T` with an explicit "was this ever set" bit (`Just`/`Nothing`
in Haskell's vocabulary), usable as the type of ONE struct field (not the
whole struct) — giving `OmitEmptyField`-style omission a DEFINITIVE signal
instead of a heuristic, without forcing every OTHER field in the struct to
become a pointer too.

**Design status:** `OmitEmptyField`/`OmitEmptyFieldFunc` are NOT being
replaced or deprecated by this feature. They remain the right choice for
the common case — a field whose zero-means-absent convention is already
safe and documented (as with every field in
`examples/go-edge-models/models/docker/dockercompose.Service`). `Maybe[T]`
is the PRECISE escape hatch for the genuinely ambiguous cases where that
heuristic isn't good enough.

## Difference from `Mutable[T]`

`Mutable[T]` (see `docs/roadmap/reloadable-value-containers.md`, also not
yet implemented) sounds superficially similar to `Maybe[T]` — both are "a
value that can be reassigned more than once." They solve **opposite**
lifecycle problems and are NOT alternatives to each other:

| | `Mutable[T]` (roadmapped, not yet built) | `Maybe[T]` (this doc) |
|---|---|---|
| Core guarantee | ALWAYS holds a valid value from construction onward — `NewMutable` REQUIRES a valid `initial`; there is NO "unset" state, ever | Begins UNSET (`Nothing`/zero value, no construction call required — can be a zero-initialized struct field like any other field type) |
| Problem it solves | "The current authoritative value of a long-lived, shared, hot-reloadable cell" (a rotating JWKS key set, a rotating API key) — reads always get a meaningful value | "Was this ONE struct field ever explicitly assigned, as opposed to still sitting at its Go zero-initialized default" — a presence/dirty flag, not a rotating credential |
| Typical lifetime/usage | ONE long-lived instance, typically a package-level or service-level singleton, read from many goroutines over the life of a process | ONE per struct field, typically constructed fresh with the struct itself (e.g. once per decoded `Service` value), short-lived, usually not shared across goroutines |
| Concurrency | Mutex-guarded (`RWMutex`) — reads/writes from multiple goroutines is the EXPECTED use case | Does NOT need a mutex — a struct field's type, not a shared cross-goroutine cell; see Open design decisions |
| Validates via | Its OWN `Codec[T]`, supplied at construction (`NewMutable(location, initial, codec)`) — EVERY `Set` re-validates | NO codec of its own — validation stays owned by whatever `Codec[V]` the enclosing `Struct`'s field declaration already uses (see "Why `Maybe[T]` doesn't need `Mutable`'s revalidating `Set`" below) |
| Observability | YES — new `stats.ReloadObserver` (`RecordReload`), since a failed reload is an ops-relevant event on a long-lived cell | NO — a struct field being set during ordinary construction isn't an "operational event" the way a credential rotation is |
| `Get()` before any `Set` | Impossible to observe — construction always requires a valid value | Returns `T`'s zero value (`Nothing`), never panics |
| Failure mode on invalid `Set` | Returns the codec's error, current value UNCHANGED (last-good-value-wins) | N/A — `Maybe[T]` has no codec of its own; the ENCLOSING field's codec still rejects bad wire data at `Struct.Decode` time, same as today |
| Where it plugs in | Anywhere a `GetterSetter[T]` is useful standalone — `SecurityFunc`/`CredentialFunc` closures, feature-flag cells | Primarily as a STRUCT FIELD's type, feeding a new `MaybeField` constructor consumed by `Struct`/`StrictStruct` — though also usable standalone as a general "was this optional input provided" wrapper (e.g. a function parameter struct where telling "caller omitted this" from "caller passed the zero value" matters) |

**One-line summary:** `Mutable[T]` is about a value's **currentness over
time** (is this still the latest valid value?); `Maybe[T]` is about a
value's **provenance at a point in time** (was this ever actually assigned,
or is it still exactly what Go initialized it to?). They are not
alternatives to each other and could both exist in the same codebase
serving entirely different fields/cells — a `Mutable[JWKS]` security-key
cell and a `Maybe[string]` struct field could sit side by side with zero
tension.

### Why `Maybe[T]` doesn't need `Mutable`'s revalidating `Set`

An obvious question: should `Maybe[T]` also gain `Mutable`'s "every `Set`
re-validates against a `Codec[T]`" behavior? **No — it would be redundant.**
In the `Struct[T]` decode flow, when a `MaybeField`-declared field's wire
value is decoded, the field's OWN `Codec[V]` (the SAME codec every other
field constructor already uses) performs validation exactly once per
decode — a failure surfaces as a `ValidationErrors` entry and `.Set(...)`
is simply never called. There is no scenario where a SECOND, independent
validation pass inside `Maybe[T]` itself would catch anything the field's
codec didn't already catch. Layering `Mutable`'s codec-carrying,
re-validating `Set` onto `Maybe[T]` would just be two validation paths for
the same logical field — confusing, not additive.

**The reverse combination is a different, plausible Phase 2 idea** (see
"Out of scope" below): giving `Mutable[T]` a "may start unset" mode — a
long-lived, hot-reloadable cell that tolerates "not configured yet" at
startup (unlike today's `NewMutable`, which REQUIRES a valid `initial T`).
This is coherent (an optional feature's rotating credential that might not
exist until the feature is enabled), but is flagged only, not designed in
depth here, since `Mutable[T]` itself is still unimplemented.

## Pointers (`PartialField`) vs. `Maybe[T]`: pros and cons

`PartialField`/`PartialStruct` already ship, and already solve this
ambiguity DEFINITIVELY via pointers (`*F`: `nil` = never touched, non-nil =
set). Given no backward-compatibility constraint on this codebase, it's
worth asking directly: should pointers just be considered THE answer,
rather than adding a new `Maybe[T]` type at all?

**Pros of pointers:**

- Zero new types to learn or build — `PartialField`/`PartialStruct`
  already ship today.
- Idiomatic Go — `*T` as "optional" is an extremely well-known pattern
  (stdlib, most Go JSON tooling); no unfamiliar vocabulary.
- Trivial, fast presence check — `== nil`, no method call.
- Directly composes with `encoding/json`-style tooling other Go developers
  already expect.

**Cons of pointers (why `Maybe[T]` is still worth having):**

- **`PartialField` is scoped as a SEPARATE "patch" struct today** — using
  bare pointers as the definitive GENERAL solution would mean reshaping
  the ORIGINAL domain struct itself (e.g. `Service.Nick *string` instead
  of `Service.Nick string`), not just adding a sibling patch type. That
  touches EVERY existing read site of that field (arithmetic, string
  concatenation, anything not expecting a pointer) — real, spread-out
  churn, not a contained one-file change.
- **Nil-pointer-dereference risk** — every read site must remember to
  nil-check or safely dereference; `Maybe[T]` makes the ALWAYS-safe
  operation (`Get()` → zero value, never panics) the DEFAULT, requiring
  deliberate opt-in (`TryGet()`/`IsSet()`) for the presence check —
  inverts the risk (safe-by-default vs. panic-by-forgetting).
- **Aliasing footgun** — copying a struct with pointer fields means BOTH
  copies share the same underlying value; mutating through one pointer
  affects the other silently. `Maybe[T]` carries `T` BY VALUE inside the
  wrapper — copying a `Maybe[T]`-containing struct copies the value too,
  exactly like every other value-typed field in Go, no surprise aliasing.
- **Extra heap allocation per optional field** — each non-nil pointer is a
  separate heap allocation (GC pressure); `Maybe[T]{value T; set bool}`
  stores `T` INLINE in the wrapper — no additional indirection/allocation
  beyond the struct's own layout (a real, measurable difference under load
  for structs with many optional fields, e.g. a hot decode path).
- **Naming/semantic collision with `Nullable[T]`** — go-codex ALREADY uses
  `*T` (via `codex.Nullable[T]`) for a DIFFERENT axis: "wire-level explicit
  null vs. a real value" (JSON `null`). Reusing bare pointers ALSO for
  "was the Go field ever assigned" would overload one Go pattern (`*T`)
  with two DIFFERENT meanings depending on context — a real source of
  confusion `Maybe[T]` avoids by being a distinct, differently-named type
  for the differently-scoped concern.

### A concrete, already-shipped gap this surfaces

Re-reading `docs/concepts/codec.md`'s own `OptionalField`+`Nullable`
example turns up a real, currently-documented ambiguity that directly
supports the case for `Maybe[T]`:

```go
// "note" absent  → Note == nil  (key was not in the object)
// "note": null   → Note == nil  (key was present, value was null)
// "note": "hi"   → Note == &"hi"
```

**`OptionalField` + `Nullable` cannot distinguish "absent" from "explicit
null" either** — BOTH produce `Note == nil` today. Only `RequiredField` +
`Nullable` avoids this (a required key is guaranteed present, so `nil`
unambiguously means explicit null there). This is the EXACT SAME shape of
ambiguity this whole doc is about, just on the "wire-null vs. absent" axis
instead of "zero-value vs. absent" — and it already affects a SHIPPED,
documented combinator.

**Forward-looking observation (NOT designed here):** a
`Maybe[Nullable[T]]`-style THREE-state composition (absent / present-null
/ present-value — the exact shape JSON Merge Patch, RFC 7396, needs) could
cleanly resolve this too, more cheaply than the "double-pointer" shape a
naive `PartialField(*T)` would otherwise require for the same three states.
Flagged as a genuinely interesting Phase 2 idea — out of scope for the
`dockercompose`/config-shaped use cases motivating this doc, which only
need two states (absent vs. present).

### Recommendation: keep both, clearly scoped, not competing

| Use this... | When... |
|---|---|
| `PartialField`/`PartialStruct` (pointers) | You're building a dedicated PATCH/partial-update type where EVERY field is independently optional (the all-pointer reshape is the POINT, not a cost) |
| `Maybe[T]` (this doc) | You want ONE OR A FEW fields on an ordinary, otherwise-value-typed domain struct to have definitive (not heuristic) presence-tracking, without reshaping the whole struct |
| `OmitEmptyField`/`OmitEmptyFieldFunc` (shipped) | A field's zero value is ALREADY a safe, conventional "absent" sentinel (the common case — most of `dockercompose.Service`'s fields) |
| `Nullable[T]` (shipped) | You need to distinguish wire-level explicit `null` from a real value — a DIFFERENT axis from "was this Go field ever assigned" |

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `codex.Maybe[T]` — `Just`/`Nothing` constructors, `Get`/`Set`/`IsSet`/`TryGet`, `Map`/`OrElse`/`Filter` combinators | `OptionalMutable[T]` (a `Mutable[T]` that may start unset — see "Why `Maybe[T]` doesn't need `Mutable`'s revalidating `Set`" above) — Phase 2, flagged only, not designed |
| `MaybeField[T, V any](name string, codec Codec[V], get func(T) *Maybe[V], set func(*T, Maybe[V])) FieldCodec[T]` — a new field constructor consumed by `Struct`, using `Maybe[V].IsSet()` directly (via the SAME `sparseFieldCodec[T]` mechanism `OmitEmptyField` already added) instead of any zero-value heuristic | Making `Maybe[T]` implement `Setter[T]`/`GetterSetter[T]` — `Set` stays infallible (no codec of its own), so it CANNOT satisfy `Setter[T]`'s `Set(T) error` signature — documented as an intentionally adjacent, not identical, family member |
| Mixing `MaybeField`-declared fields with `RequiredField`/`OptionalField`/`DefaultField`/`OmitEmptyField` in the SAME `Struct(...)` call | A three-state `Maybe[Nullable[T]]`-style composition (absent/null/value, RFC 7396 Merge Patch shape) — flagged as a genuinely interesting Phase 2 idea, not designed here |
| Decode: absent key → `Maybe[V]{}`/`Nothing` (unset); present key → decode via `codec`, call `.Set(...)` (`Just`) | Applying `Maybe`/`MaybeField` to any specific existing package (`dockercompose.Service` already adopted the simpler, already-shipped `OmitEmptyField` — see the sibling `omit-empty-encode` work) |
| A general-purpose `codex.Codec[Maybe[T]]` (so `Maybe[T]` could ALSO be used as an ordinary field's type via `RequiredField`/`OptionalField`, not just via `MaybeField`) — plausible, small addition once the base type ships | Retrofitting `OmitEmptyField`/`OmitEmptyFieldFunc` to be built ON TOP of `Maybe[T]` internally — they stay independent, zero-value-heuristic-based mechanisms; `Maybe`/`MaybeField` is an ADDITIONAL, more precise option, not a replacement |

## Open design decisions (must resolve before implementation)

1. **`Maybe[T]` stays codec-agnostic** (no `Codec[T]` of its own) —
   validation owned entirely by the enclosing field's `Codec[V]`, per the
   redundancy finding above. This also means `Maybe[T]` needs NO
   constructor taking a codec (unlike `Immutable`/`Mutable`, which both
   REQUIRE one) — a zero-initialized `Maybe[V]{}` (`Nothing`) is already
   meaningful on its own.
2. **No mutex.** Scoped purely as a struct field's type (not a shared,
   long-lived, cross-goroutine cell like `Mutable`), plain unsynchronized
   reads/writes are consistent with how every OTHER field on the same
   struct already behaves.
3. **`Get()` never panics** — returns `T`'s zero value when unset;
   `IsSet()`/`TryGet()` are the explicit-check siblings. Consistent with
   `Immutable[T]`'s naming (`Get`+`TryGet` coexist there too), just with
   different panic behavior on `Get`.
4. **`Map` must be a free function, not a method** — Go generic methods
   cannot introduce a NEW type parameter (`R`, distinct from `Maybe[T]`'s
   own `T`); this mirrors the same constraint already documented for
   `forge.NewFunction[In, Out]` elsewhere in this codebase. `OrElse`/
   `Filter` CAN be methods since they don't change the type parameter.

## API surface

```go
// Maybe pairs a value with an explicit "was this ever Set" bit -- Go's
// analogue of Haskell's Maybe/Rust's Option, chosen deliberately over a
// bare pointer (see "Pointers (PartialField) vs. Maybe[T]" above): unlike
// OmitEmptyField's zero-value HEURISTIC, this is a DEFINITIVE presence
// signal, and unlike *T, it carries T BY VALUE (no extra heap allocation,
// no aliasing) and doesn't collide with Nullable[T]'s existing "wire
// null" meaning for *T.
type Maybe[T any] struct {
	value T
	set   bool
}

// Just constructs an already-set Maybe[T] -- the "Just(v)" constructor.
func Just[T any](v T) Maybe[T] {
	return Maybe[T]{value: v, set: true}
}

// Nothing returns an unset Maybe[T] -- equivalent to the zero value
// Maybe[T]{}, provided as a named, self-documenting alternative (mirrors
// Haskell's Nothing).
func Nothing[T any]() Maybe[T] {
	return Maybe[T]{}
}

// Set stores value and marks this Maybe as having been set -- REPEATABLE:
// every call overwrites, no "already set" failure (contrast with
// Immutable.Set's exactly-once contract).
func (m *Maybe[T]) Set(value T) {
	m.value, m.set = value, true
}

// Get returns the current value -- T's zero value if never Set (Nothing),
// NEVER panics (contrast with Immutable.Get, which panics before the
// first Set).
func (m Maybe[T]) Get() T { return m.value }

// IsSet reports whether Set/Just has ever produced this value.
func (m Maybe[T]) IsSet() bool { return m.set }

// TryGet is Get's explicit-presence-check sibling -- (value, true) if
// ever set, (zero, false) otherwise. Mirrors Immutable.TryGet's shape.
func (m Maybe[T]) TryGet() (T, bool) { return m.value, m.set }

// Map applies fn to the contained value IF set, returning a new Maybe[R]
// -- Nothing in, Nothing out (fn is never called when unset). A free
// function, not a method: Go generic methods cannot introduce a new type
// parameter (R, distinct from Maybe[T]'s own T).
func Map[T, R any](m Maybe[T], fn func(T) R) Maybe[R] {
	if !m.IsSet() {
		return Maybe[R]{}
	}
	return Just(fn(m.Get()))
}

// OrElse returns the contained value if set, or fallback otherwise -- the
// safe-default-value idiom (Rust's unwrap_or, Haskell's fromMaybe).
func (m Maybe[T]) OrElse(fallback T) T {
	if m.set {
		return m.value
	}
	return fallback
}

// Filter returns m unchanged if set AND pred(value) is true; otherwise
// returns Nothing[T]() -- lets a caller narrow "set" down to "set AND
// satisfies some condition" without a separate IsSet+manual-check dance.
func (m Maybe[T]) Filter(pred func(T) bool) Maybe[T] {
	if m.set && pred(m.value) {
		return m
	}
	return Maybe[T]{}
}

// MaybeField declares a Struct field backed by Maybe[V] -- decodes like
// OptionalField (absent key -> Maybe[V]{}/Nothing), but Encode OMITS the
// key based on Maybe[V].IsSet() DIRECTLY -- no zero-value guessing, ever.
func MaybeField[T any, V any](
	name string, codec Codec[V],
	get func(T) *Maybe[V], set func(*T, Maybe[V]),
) FieldCodec[T]
```

## Structured errors

None needed — same reasoning as `OmitEmptyField`: errors flow through the
enclosing field's own `Codec[V]`, already `slog.LogValuer`-compliant.

## Observer integration

None needed — no I/O boundary, same as `OmitEmptyField`/`PartialField`.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestMaybe_ZeroValueIsNothing` | a zero-initialized `Maybe[T]{}` reports `IsSet() == false`, `Get()` returns `T`'s zero value |
| `TestMaybe_JustConstructsSet` | `Just(v)` reports `IsSet() == true`, `Get() == v` |
| `TestMaybe_NothingConstructsUnset` | `Nothing[T]()` behaves identically to `Maybe[T]{}` |
| `TestMaybe_SetMarksPresent` | after `Set(v)`, `IsSet() == true`, `Get() == v` |
| `TestMaybe_SetRepeatable` | multiple `Set` calls all succeed, `Get()` always returns the MOST RECENT value (contrast with `Immutable`) |
| `TestMaybe_TryGet_UnsetReturnsFalse` | `TryGet()` on a never-`Set` `Maybe[T]` returns `(zero, false)` |
| `TestMaybe_TryGet_SetReturnsTrue` | `TryGet()` after `Set`/`Just` returns `(value, true)` |
| `TestMap_AppliesFnWhenSet` | `Map(Just(v), fn)` returns `Just(fn(v))` |
| `TestMap_SkipsFnWhenNothing` | `Map(Nothing[T](), fn)` returns `Nothing[R]()`, `fn` is never called |
| `TestOrElse_ReturnsValueWhenSet` | `Just(v).OrElse(fallback)` returns `v` |
| `TestOrElse_ReturnsFallbackWhenNothing` | `Nothing[T]().OrElse(fallback)` returns `fallback` |
| `TestFilter_KeepsWhenSetAndPredicateTrue` | `Just(v).Filter(pred)` returns `Just(v)` when `pred(v)` is true |
| `TestFilter_ReturnsNothingWhenPredicateFalse` | `Just(v).Filter(pred)` returns `Nothing[T]()` when `pred(v)` is false |
| `TestFilter_ReturnsNothingWhenAlreadyNothing` | `Nothing[T]().Filter(pred)` returns `Nothing[T]()`, `pred` never called |
| `TestMaybeField_EncodeOmitsWhenNeverSet` | a `MaybeField`-declared struct field never `Set` → key absent from `Struct.Encode`'s output |
| `TestMaybeField_EncodeIncludesWhenSetToZeroValue` | **the key differentiator vs. `OmitEmptyField`**: a field EXPLICITLY `Set` to `T`'s zero value (e.g. `Set(0)`, `Set("")`) still ENCODES the key — proves the definitive-vs-heuristic distinction |
| `TestMaybeField_DecodePresentKeySetsMaybe` | present key → decoded value assigned via `.Set(...)`, `IsSet() == true` |
| `TestMaybeField_DecodeAbsentKeyLeavesNothing` | absent key → `Maybe[V]{}`, `IsSet() == false` |
| `TestStruct_MixesMaybeAndOtherFieldKinds` | one `Struct(...)` call combining `MaybeField` with `Required`/`Optional`/`Default`/`OmitEmptyField` |
| `ExampleMaybe`/`ExampleMaybeField` | pkg.go.dev-visible usage sketch |

## Files to create

| File | Responsibility |
|---|---|
| `codex/maybe.go` | `Maybe[T]`, `Just`, `Nothing`, `Map`, `MaybeField` |
| `codex/maybe_test.go` | Full unit test plan above |
| `docs/concepts/codec.md` | New subsection alongside `OmitEmptyField`'s, explicitly cross-referencing the "Difference from `Mutable[T]`" and "Pointers vs. `Maybe[T]`" comparisons |
| `.github/instructions/go-codex.instructions.md` | Document new exported symbols |

## Out of scope (Phase 2)

- `OptionalMutable[T]` — a `Mutable[T]` that may start unset, then behaves
  like `Mutable[T]` once a value is supplied (see "Why `Maybe[T]` doesn't
  need `Mutable`'s revalidating `Set`" above) — a plausible standalone
  idea, not designed in depth here since `Mutable[T]` itself isn't built.
- A three-state `Maybe[Nullable[T]]`-style composition (absent/null/value
  — the RFC 7396 JSON Merge Patch shape) — flagged as genuinely
  interesting (see "A concrete, already-shipped gap this surfaces" above),
  not designed here; current motivating use cases only need two states.
- A general-purpose `codex.Codec[Maybe[T]]` (so `Maybe[T]` could be used
  as an ORDINARY field's type via `RequiredField`/`OptionalField` too, not
  just via `MaybeField`) — plausible small Phase 2 addition.
- Retrofitting any EXISTING package (including `dockercompose.Service`,
  which instead adopted the simpler, already-shipped `OmitEmptyField`) to
  use `Maybe`/`MaybeField`.
- Any relationship to `Mutable[T]`/`Immutable[T]` beyond the comparison
  table above — no shared implementation, no shared file.
