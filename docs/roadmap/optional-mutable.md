# `codex.OptionalMutable[T]`: A Mutable Cell That May Start Unset

> **Status:** Design draft — `codex.Mutable[T]` has SHIPPED (see
> `docs/concepts/codec.md`'s Getter/Setter subsection and `codex/mutable.go`)
> — this doc's dependency is satisfied, ready to implement.
> [← Back to Roadmap](index.md)

## Motivation

`codex.Mutable[T]` (shipped — `codex/mutable.go`) requires a valid
`initial T` at construction (`NewMutable(location, initial, codec)`) —
there is no "not configured yet" state; `Get()` never panics precisely
BECAUSE construction guarantees a value always exists. This is the right
contract for a cell that always has SOMETHING to hot-reload.

Real optional features don't always have that guarantee at startup: an
auth mode enabled by config, a plugin that may not be loaded, a JWKS key
set that only exists once a certain feature is turned on. These need a
hot-reloadable cell that STARTS with nothing to reload, then behaves
exactly like `Mutable[T]` once a first value arrives.

This idea surfaced twice, from two different angles, while designing
`codex.Maybe[T]` (see `docs/concepts/codec.md`'s "`Maybe[T]`: definitive
presence tracking" subsection): once as "should `Maybe[T]` gain
`Mutable`'s revalidating `Set`" (answer: no — redundant for `Maybe[T]`'s
actual use case, `MaybeField`, where the enclosing `Struct` field's own
codec already validates), and once as "shouldn't most fields validate on
every assignment like `Mutable[T]`" (answer: no, that's deliberately
narrow and opt-in — see `docs/concepts/codec.md`'s and
`.github/instructions/go-codex.instructions.md`'s "why not validate every
field" callouts). `OptionalMutable[T]` is the concrete, narrower idea both
questions were actually pointing at: a `Mutable[T]`-shaped cell for the
specific case of an OPTIONAL, rotatable value.

## Relationship to `Mutable[T]` and `Maybe[T]`

`OptionalMutable[T]` = `Mutable[T]`'s revalidating, hot-reloadable `Set` +
`Maybe[T]`'s "may start unset" lifecycle. It is NOT a modification of
either:

- `Mutable[T]` keeps its "always valid from construction" guarantee
  unchanged — for callers who genuinely always have an `initial` value
  and want `Get()` to never need a presence check.
- `Maybe[T]` keeps its codec-agnostic, `Struct`-field-scoped design
  unchanged — for callers who need presence-tracking with validation
  OWNED by an enclosing `Struct` field's own codec, not a second
  validation layer.
- `OptionalMutable[T]` is a THIRD, standalone type for the specific case
  that needs BOTH properties at once: presence tracking (may start unset)
  AND its own repeated, revalidating `Set` (not tied to any `Struct`
  field). It is not built by combining the other two — it shares their
  SHAPE, not their code.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `codex.OptionalMutable[T]` — `GetterSetter[T]`-shaped; `TryGet() (T, bool)` for the "no value yet" case; `Set(value T) error` — codec-validated, REPEATABLE, last-good-value-wins (mirrors `Mutable[T]`'s own `Set` exactly, once a value exists) | Retrofitting `Mutable[T]`/`Maybe[T]` to be built ON TOP of `OptionalMutable[T]` internally, or vice versa — all three stay separate, standalone types |
| `NewOptionalMutable[T](location string, codec Codec[T], opts ...OptionalMutableOpt[T]) *OptionalMutable[T]` — NO initial value required (unlike `NewMutable`, which mandates one) | Any change to `Mutable[T]`'s or `Maybe[T]`'s existing, already-shipped/designed contracts |
| Reuses `stats.ReloadObserver` (already scoped for `Mutable[T]`) — `RecordReload` fires on every `Set`, success or failure, INCLUDING the very first one (there is no special-cased "initial configuration" event, just an ordinary `Set` on a previously-unset cell) | A generic "subscribe to changes" push-notification mechanism — same out-of-scope reasoning `Mutable[T]`'s own roadmap doc already gives |
| `Get()` never panics — returns `T`'s zero value if never `Set` (see Open design decisions) | A `TryGet`-less `Get()`-only API — `TryGet`/`IsSet`-style presence checks are essential here, unlike plain `Mutable[T]` |

## Open design decisions

1. **`Get()` before any `Set`: panic, or return the zero value?**
   `Immutable[T]` panics (a real bug: reading config before startup
   finished loading it). `Maybe[T]` never panics (an unset value is a
   normal, expected state). `OptionalMutable[T]` models "not configured
   yet" as a REAL, expected, ongoing state (not a startup-ordering bug —
   an optional feature may simply never be enabled for the life of a
   process) — leaning toward `Maybe[T]`'s precedent: `Get()` never
   panics, `TryGet()`/`IsSet()` are the explicit-check siblings.
2. **File placement**: a natural sibling in `codex/const.go`/
   `codex/mutable.go` (once `Mutable[T]` ships), or its own file
   (`codex/optionalmutable.go`)? Leaning: own file, since it's a distinct
   type with its own test suite, not a modification of `Mutable[T]`'s.
3. **Sequencing**: this doc is EXPLICITLY sequenced AFTER `Mutable[T]`
   ships (mirrors how `ports.RefreshingCacheable[T]` is sequenced after
   `Cacheable[T]` in `refreshing-cacheable.md`) — do not implement before
   `Mutable[T]` exists, since this type's shape and test suite will
   directly mirror `Mutable[T]`'s own closely.

## API surface (sketch — pending `Mutable[T]`'s own final shape)

```go
// OptionalMutable is Mutable[T]'s "may start unset" sibling -- a
// GetterSetter[T] cell that begins with no value (TryGet returns false),
// then behaves EXACTLY like Mutable[T] once a first Set succeeds: every
// subsequent Set revalidates against the SAME Codec[T], with
// last-good-value-wins on an invalid Set.
type OptionalMutable[T any] struct {
    mu       sync.RWMutex
    value    T
    set      bool
    codec    Codec[T]
    location string
    obs      stats.Observer
}

// NewOptionalMutable returns an OptionalMutable[T] with NO initial value
// -- unlike NewMutable, no valid `initial T` is required at construction.
func NewOptionalMutable[T any](location string, codec Codec[T], opts ...OptionalMutableOpt[T]) *OptionalMutable[T]

// Get returns the current value -- T's zero value if never Set, NEVER
// panics (see Open design decisions #1).
func (m *OptionalMutable[T]) Get() T

// TryGet is Get's explicit-presence-check sibling -- (value, true) if
// ever Set, (zero, false) otherwise.
func (m *OptionalMutable[T]) TryGet() (T, bool)

// Set validates value against m's codec; on success it atomically
// replaces the current value (marking it configured) and returns nil. On
// failure the current value is left UNCHANGED (last-good-value-wins,
// remains unset if this is the first Set attempt) and the codec's own
// validation error is returned. Fires stats.ReloadObserver.RecordReload
// on every call, success or failure -- including the first.
func (m *OptionalMutable[T]) Set(value T) error
```

## Structured errors

No NEW error types — `Set`'s only failure mode is the codec's own
existing validation error, exactly like `Mutable.Set`/`Immutable.Set`.

## Observer integration

Reuses `stats.ReloadObserver` (`RecordReload(location string, success
bool, duration time.Duration)`) unchanged — no new observer extension
needed. The FIRST successful `Set` (transitioning from unset to
configured) is not distinguished from any later `Set` in the observer
call — from `RecordReload`'s point of view, "first configuration" and
"routine reload" are the same kind of event.

## Unit test plan (mirrors `Mutable[T]`'s own test plan closely)

Same shape as `codex.Mutable[T]`'s own test table (`codex/mutable_test.go`)
(`TestNewMutable_*`, `TestMutable_Get_NeverPanics`,
`TestMutable_SetValid_ReplacesValue`,
`TestMutable_SetInvalid_KeepsPreviousValue`,
`TestMutable_SetRepeatedly_AllSucceed`,
`TestMutable_ConcurrentGetSet_NoRace`,
`TestMutable_ImplementsGetterSetterInterface`,
`TestMutable_Set_CallsReloadObserverOn{Success,Failure}`,
`TestMutable_Set_{Nil,Plain}Observer_NoPanic`), renamed for
`OptionalMutable`, PLUS:

| Test | Verifies |
|---|---|
| `TestOptionalMutable_TryGet_UnsetReturnsFalse` | `TryGet()` immediately after construction (no `Set` yet) returns `(zero, false)` |
| `TestOptionalMutable_FirstSetTransitionsToConfigured` | after one valid `Set`, `TryGet()` returns `(value, true)` |
| `TestOptionalMutable_Set_CallsReloadObserverOnFirstSetToo` | the FIRST `Set` fires `RecordReload` exactly like any later one — no special-cased "initial configuration" event |
| `TestOptionalMutable_InvalidFirstSet_StaysUnset` | an invalid FIRST `Set` leaves the cell unset (`TryGet()` still `(zero, false)`), not "unset but somehow different" |

## Files to create

| File | Responsibility |
|---|---|
| `codex/optionalmutable.go` | `OptionalMutable[T]`, `OptionalMutableOpt[T]`, `NewOptionalMutable` |
| `codex/optionalmutable_test.go` | Full unit test plan above |
| `docs/concepts/codec.md` | Extend the `Getter`/`Setter` comparison table with `OptionalMutable[T]`'s row |

## Out of scope

- Everything `Mutable[T]`'s own roadmap doc already excludes (native
  `Builder.AddGlobalSecurity` wiring, a generic change-notification
  mechanism, folding `NewCachingCredentialFunc` onto this internally) —
  same reasoning applies unchanged.
- A three-way merge of `Const`/`Immutable`/`Mutable`/`Maybe`/
  `OptionalMutable` into one configurable type — deliberately kept as
  five separate, clearly-scoped types rather than one type with modes,
  per this family's established design philosophy (see
  `.github/instructions/go-codex.instructions.md`'s "Constructor Naming
  Convention for `Getter`/`Setter`-Family Types" section).
