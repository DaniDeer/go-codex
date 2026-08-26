# Reloadable & Fallible Value Containers — `Mutable[T]`, `NewConst[T]` — `codex`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [`codex.Cacheable[T]`](cache-parity-and-cacheable.md) — a 4th
> sibling, EXPLICITLY SEQUENCED to depend on THIS doc's `Mutable[T]`
> shipping first (do not implement `Cacheable[T]` before `Mutable[T]`
> exists — same rule as `OptionalMutable[T]` below), adding a
> TTL/`Invalidate()` validity window on top of this doc's `Mutable[T]`
> design (that doc also has the full `Const`/`Immutable`/`Mutable`/
> `Cacheable` comparison table, and a Redis-backed `Cacheable[T]`
> sibling; that doc's own Part 1, `ports.Cache` template-unification
> parity, has ALREADY shipped independently of this dependency) · and
> [`ports.RefreshingCacheable[T]`](refreshing-cacheable.md) (an
> auto-refresh wrapper depending on `Cacheable[T]` shipping first).

## Motivation

`docs/roadmap/validated-const-getter.md` shipped `codex.Getter[T]`/
`Setter[T]`/`GetterSetter[T]` and their first two implementations:
`Const[T]` (compile-time-authored, panics if invalid) and `Immutable[T]`
(runtime-supplied, validated exactly once, panics on `Get` before the
first `Set`). Its own "Out of scope (Phase 3)" section named two natural
follow-ons, deferred at the time for lack of a concrete driver:

1. A non-panicking `NewConst[T](value, codec) (Const[T], error)` variant.
2. A `Mutable[T]` allowing reassignment/hot-reload, with observer hooks.

A driver now exists for both, and — reviewed together — they turn out to
share almost the entire skeleton of `Const`/`Immutable` already in
`codex/const.go`, so scoping them together (rather than as two unrelated
features) keeps the file coherent.

**`Mutable[T]`'s driver:** go-codex's request/message-security story
(`route.SecurityRequirement`, `rest`/`events`/`reqreply`/`mcp`'s
`WithSecurityScheme`, and the adapter-level `SecurityFunc`/
`CredentialFunc` closures in `adapters/nethttp`/`adapters/mqtt5`) is
entirely BYOK (bring your own key material) — go-codex calls the
caller's closure and has no opinion on where the signing key, JWKS key
set, or shared API key that closure checks against comes from. Today, a
caller who wants to support **key rotation without a process restart**
(a real, common production need — JWKS endpoints rotate signing keys on
a schedule; shared API keys get rotated after an incident) has to
hand-roll their own mutex-guarded "current key material" cell inside
their `SecurityFunc`/`CredentialFunc` closure — there's no ready-made,
validated, thread-safe building block for it anywhere in go-codex.
`Mutable[T]` is exactly that building block: a `Getter[T]`+`Setter[T]`
cell that, unlike `Immutable[T]`, can be `Set` more than once — each
`Set` re-validates against the same `Codec[T]` and atomically swaps the
current value, with a NEW observer hook so rotations become visible the
same way every other lifecycle event in go-codex already is.

This is DELIBERATELY narrower than `adapters/nethttp.NewCachingCredentialFunc`
(TTL-based, single-flight, auto-refetches by calling an `inner()`
function again on expiry) — `Mutable[T]` has NO built-in scheduling or
refetch logic; a caller (e.g. a background goroutine on a ticker, or a
webhook triggered by a key-rotation event) is entirely responsible for
calling `.Set()` when a new value is available. `Mutable[T]` is the
lower-level primitive `NewCachingCredentialFunc`-style caching COULD be
rebuilt on in the future, not a replacement for it — the two solve
different problems (TTL/refetch-on-demand vs. explicitly-pushed update).

**`NewConst[T]`'s driver:** reviewing `Mutable[T]`'s design turned up
that `Mutable[T]`'s own constructor needs to validate an `initial T`
value and return an error, not panic, since the initial value usually
comes from the SAME runtime source (an env var, a config file, a first
JWKS fetch) that would make `Immutable[T]`'s panic-free error return the
right choice. Once that fallible-construction path exists for
`Mutable[T]`, giving `Const[T]` the identical non-panicking option is a
small, consistent addition — for the narrower case of a caller building
several `Const`-like frozen values from a trusted-but-fallible startup
source (e.g. a YAML manifest of path/topic templates) who wants Const's
plain-value, no-mutex ergonomics without a panic on one bad entry.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `codex.Mutable[T]` — a `GetterSetter[T]` cell that can be `Set` more than once, each call re-validating against the same `Codec[T]` and atomically swapping the current value | Wiring `Mutable[T]` NATIVELY into `rest`/`events`/`reqreply`/`mcp`'s `Builder.AddGlobalSecurity`/`WithSecurityScheme`/`SecuritySchemes` (i.e. making global security or scheme credentials themselves live-reloadable without a caller writing their own `SecurityFunc`) — no concrete driver yet, deferred to a "Phase 2" exactly as `validated-const-getter.md` deferred ITS Phase 2 |
| `codex.NewMutable[T](location string, initial T, codec Codec[T], opts ...MutableOpt[T]) (*Mutable[T], error)` — fallible construction (mirrors why `Immutable`'s `Set` returns an error, not a panic: `initial` is real runtime input) | Auto-scheduling/refetch (TTL, polling, single-flight) inside `Mutable[T]` itself — that's `adapters/nethttp.NewCachingCredentialFunc`'s job; `Mutable[T]` stays a plain cell |
| A NEW `stats.ReloadObserver` extension (`RecordReload(location string, success bool, duration time.Duration)`) — the first observable lifecycle event this whole container family has needed | A generic "subscribe to changes" callback/notification mechanism on `Mutable[T]` (push notifications to interested code elsewhere) — no driver yet |
| `codex.NewConst[T](value T, codec Codec[T]) (Const[T], error)` — non-panicking sibling to the existing `MustConst`; `MustConst` itself is UNCHANGED | Any change to `Immutable[T]`'s existing "set exactly once, panics on repeat `Set`" semantics — `Mutable[T]` is a SEPARATE type, not a relaxed `Immutable[T]` |
| A runnable example (JWKS-style key-rotation snippet for a `SecurityFunc` closure, likely `examples/mutable-security-keys` or added to an existing security-focused example) | Any change to `Const[T]`'s panic-on-invalid `MustConst` — that stays the only constructor for the "authored, always-valid" case; `NewConst` is additive |

## Relationship to `Const[T]`/`Immutable[T]`

Extending the 3-way comparison from `validated-const-getter.md`:

| | `Const[T]` | `Immutable[T]` | `Mutable[T]` (NEW) |
|---|---|---|---|
| Value origin | Compile-time-authored | Runtime-supplied, exactly once | Runtime-supplied, REPEATEDLY |
| Validation timing | Eager, at construction | Lazy, at the ONE `Set` call | Eager at construction (`initial`), then again at EVERY `Set` |
| Invalid value | Panics (`MustConst`) — or returns an error (`NewConst`, new) | Returns a typed/codec error | Returns the codec's own error; current value is UNCHANGED (last-good-value-wins) |
| Concurrency | None needed — value type | Mutex-guarded, pointer-based | Mutex-guarded (`RWMutex` — reads vastly outnumber reloads), pointer-based |
| Re-assignment | N/A — never mutates | A second `Set` fails (`ImmutableAlreadySetError`) | Every valid `Set` succeeds and replaces the current value — no "already set" concept at all |
| `Get()` before any `Set` | N/A (always valid) | PANICS (`TryGet` for safe access) | Never possible — construction REQUIRES a valid `initial` value, so `Get()` never panics and there is no `TryGet` |
| Interface | `Getter[T]` only | `GetterSetter[T]` | `GetterSetter[T]` (same interface as `Immutable[T]` — a caller depending on `GetterSetter[T]` cannot tell them apart by type, only by re-`Set`-ability) |
| Observable? | No | No (no I/O boundary — single call, typically package init) | **Yes** — the first container in this family with an observable lifecycle event (a reload can fail, and ops needs to know) |

`Mutable[T]` is NOT "`Immutable[T]` with a relaxed `Set`" implemented by
loosening `Immutable`'s existing `set bool` guard — it is its own type
in `codex/const.go` (or `codex/mutable.go` — see "Open design
decisions"), because its `Get()` has a materially different contract
(never panics, no "unset" state ever exists) and because giving it an
observer hook while leaving `Immutable[T]` without one is a real,
worth-explaining asymmetry, not a shared implementation detail.

## API surface

```go
package codex

// ── NewConst: non-panicking sibling to MustConst ────────────────────────

// NewConst validates value against codec and returns a Const[T], or an
// error if validation fails — the non-panicking sibling to [MustConst].
// Use NewConst when value comes from a trusted-but-fallible startup
// source (e.g. a manifest file read once at process start) rather than
// a literal Go source constant, and a caller wants Const's plain-value,
// no-mutex ergonomics without a panic on one bad entry. [MustConst]
// remains the right choice — and stays unchanged — for genuinely
// compile-time-authored constants, where an invalid value IS a
// programming error that should fail loudly and immediately.
func NewConst[T any](value T, codec Codec[T]) (Const[T], error) {
	if err := codec.Validate(value); err != nil {
		return Const[T]{}, err
	}
	return Const[T]{value: value}, nil
}

// ── Mutable[T]: a reloadable, validated value cell ──────────────────────

// Mutable is a runtime value that can be replaced more than once, each
// replacement validated against the SAME Codec[T] used at construction
// — the "config that can be hot-reloaded without a restart" shape (a
// rotating JWKS key set, a rotating shared API key, a live-tunable
// feature flag). Unlike [Immutable] (set EXACTLY once, panics on a
// second Set), Mutable always holds a valid value from construction
// onward — Get() never panics, and every valid Set replaces the
// current value; an invalid Set leaves the current value UNCHANGED
// (last-good-value-wins) and returns the codec's own validation error.
//
// Reads (Get) vastly outnumber writes (Set) for this container's
// intended use (every request/message reads the current key material;
// reloads happen on a schedule measured in minutes/hours) — guarded by
// sync.RWMutex rather than sync.Mutex for that reason.
type Mutable[T any] struct {
	mu       sync.RWMutex
	value    T
	codec    Codec[T]
	location string
	obs      stats.Observer
}

// MutableOpt configures a [Mutable] at construction time.
type MutableOpt[T any] func(*Mutable[T])

// WithReloadObserver sets the [stats.Observer] whose
// [stats.ReloadObserver] extension (if implemented) receives a
// RecordReload event on every [Mutable.Set] call, success or failure.
// Defaults to [stats.NoopObserver] — Mutable works with no Observer at
// all, same as every other codex container.
func WithReloadObserver[T any](obs stats.Observer) MutableOpt[T] {
	return func(m *Mutable[T]) { m.obs = obs }
}

// NewMutable validates initial against codec and returns a *Mutable[T],
// or an error if initial fails validation — mirrors why [Immutable]'s
// Set returns an error rather than panicking: initial is real runtime
// input (e.g. the FIRST JWKS fetch at startup), not an authored
// constant. location identifies this cell in [stats.ReloadObserver]
// events (e.g. "jwks-signing-keys") — one Mutable instance = one
// location, mirroring how one [adapters/nethttp.NewCachingCredentialFunc]
// instance is documented as "one cache entry."
func NewMutable[T any](location string, initial T, codec Codec[T], opts ...MutableOpt[T]) (*Mutable[T], error) {
	if err := codec.Validate(initial); err != nil {
		return nil, err
	}
	m := &Mutable[T]{value: initial, codec: codec, location: location, obs: stats.NoopObserver{}}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// Get returns the current value — never panics (construction guarantees
// a valid value always exists). Satisfies [Getter][T].
func (m *Mutable[T]) Get() T {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.value
}

// Set validates value against m's codec; on success it atomically
// replaces the current value and returns nil. On failure the current
// value is left UNCHANGED (last-good-value-wins) and the codec's own
// validation error is returned. Either way, if m's Observer implements
// [stats.ReloadObserver], RecordReload(m.location, success, duration)
// fires. Satisfies [Setter][T].
func (m *Mutable[T]) Set(value T) error {
	start := time.Now()
	err := m.codec.Validate(value)
	if err == nil {
		m.mu.Lock()
		m.value = value
		m.mu.Unlock()
	}
	if ro, ok := m.obs.(stats.ReloadObserver); ok {
		ro.RecordReload(m.location, err == nil, time.Since(start))
	}
	return err
}
```

No new structured error type: `Mutable.Set`'s only failure mode is the
codec's own existing validation error (`ValidationError`/
`ValidationErrors`/`ConstraintError`/etc.), unchanged and already
`errors.As`-navigable — exactly like `Immutable.Set`'s "invalid value"
path. There is no "already set" failure mode to name, since repeat
`Set` calls are Mutable's entire purpose.

## Structured errors (all implement `slog.LogValuer`)

No NEW error types. `NewConst`/`Mutable.Set`/`NewMutable`'s only failure
mode is the codec's own existing validation error — reused unchanged,
`errors.As`-navigable exactly as it already is for every other
`Codec[T].Validate` call in go-codex.

## Observer integration

**NEW** — the first container in this family with an observable
lifecycle event. Add `stats.ReloadObserver` to `stats/observer.go`,
following the existing `CredentialCacheObserver`/`CacheObserver`
pattern exactly:

```go
// ReloadObserver is an optional extension to Observer for value-container
// reload events ([codex.Mutable]). Callers type-assert the configured
// Observer to ReloadObserver before calling its methods — existing
// Observer implementations need not change.
type ReloadObserver interface {
	// RecordReload is called on every [codex.Mutable.Set] call, success
	// or failure. location identifies the Mutable instance (set via
	// [codex.NewMutable]'s location parameter, e.g. "jwks-signing-keys").
	// success is false when the new value failed codec validation (the
	// PREVIOUS value remains in effect). duration is the validation
	// call's own cost.
	RecordReload(location string, success bool, duration time.Duration)
}
```

- `NoopObserver`, `LoggingObserver`, `fanout` all gain a
  `RecordReload` implementation, mirroring every other extension.
- `codex.Mutable[T]` type-asserts its configured Observer to
  `ReloadObserver` before calling it — never embeds it directly (same
  guard rule as `FileObserver`/`SQLObserver`/`SecurityObserver`).
- No location string convention exists yet for reload events — this
  introduces one (a caller-chosen identifier, not a fixed vocabulary
  like `"body"`/`"payload"`, since Mutable instances are inherently
  application-specific).

## Unit test plan

| Test | Verifies |
|---|---|
| `TestNewConst_ValidValue_ReturnsConstNoError` | Valid value + codec → `(Const[T], nil)`, `Get()` returns it unchanged |
| `TestNewConst_InvalidValue_ReturnsError` | Invalid value + codec → `(Const[T]{}, err)`, no panic |
| `TestMustConst_Unchanged_StillPanicsOnInvalid` | Regression guard: `MustConst` still panics (unaffected by `NewConst`'s addition) |
| `TestNewMutable_ValidInitial_ReturnsMutable` | Valid `initial` + codec → non-nil `*Mutable[T]`, `Get()` returns `initial` |
| `TestNewMutable_InvalidInitial_ReturnsError` | Invalid `initial` + codec → `(nil, err)` |
| `TestMutable_Get_NeverPanics` | `Get()` immediately after construction returns `initial`, no panic (contrast with `Immutable.Get`) |
| `TestMutable_SetValid_ReplacesValue` | Valid `Set` → subsequent `Get()` returns the NEW value |
| `TestMutable_SetInvalid_KeepsPreviousValue` | Invalid `Set` → returns codec's own error, subsequent `Get()` STILL returns the value from before the failed `Set` |
| `TestMutable_SetRepeatedly_AllSucceed` | Multiple valid `Set` calls in sequence all succeed (contrast with `Immutable`'s second-`Set`-fails behavior) |
| `TestMutable_ConcurrentGetSet_NoRace` | `go test -race`: concurrent `Get`/`Set` calls are race-free |
| `TestMutable_ImplementsGetterSetterInterface` | Compile-time assertion: `var _ GetterSetter[string] = (*Mutable[string])(nil)` |
| `TestMutable_Set_CallsReloadObserverOnSuccess` | A `WithReloadObserver`-configured Mutable calls `RecordReload(location, true, ...)` on a valid `Set` |
| `TestMutable_Set_CallsReloadObserverOnFailure` | ...calls `RecordReload(location, false, ...)` on an invalid `Set` |
| `TestMutable_Set_NilObserver_NoPanic` | No `WithReloadObserver` option → `Set` still works, no panic (defaults to `NoopObserver`) |
| `TestMutable_Set_PlainObserver_NoPanic` | An `Observer` NOT implementing `ReloadObserver` → `Set` still works, guard skips the call |
| `ExampleNewConst` | pkg.go.dev-visible usage sketch |
| `ExampleMutable` (or `Example_mutableSecurityKeyRotation`) | pkg.go.dev-visible usage sketch — a JWKS-style key rotation snippet feeding a `SecurityFunc` closure |
| `TestReloadObserver_LoggingObserver_NoPanic` (in `stats`) | `LoggingObserver.RecordReload` runs without panicking |
| `TestReloadObserver_Fanout_CallsAll` (in `stats`) | `fanout.RecordReload` calls every registered observer's `RecordReload` |

## Files to create

| File | Responsibility |
|---|---|
| `codex/const.go` (or split into `codex/mutable.go` — see "Open design decisions") | `NewConst`, `Mutable[T]`, `MutableOpt[T]`, `WithReloadObserver`, `NewMutable` |
| `codex/const_test.go` (or `codex/mutable_test.go`) | Full unit test plan above (Mutable + NewConst + interface satisfaction + Example funcs) |
| `stats/observer.go` | `ReloadObserver` interface + `NoopObserver`/`LoggingObserver`/`fanout` implementations |
| `stats/observer_test.go` | Compile-time assertions + `LoggingObserver`/`fanout` `RecordReload` tests |
| `docs/concepts/codec.md` (doc-only) | Extend the existing "Getter/Setter: validated value containers" subsection with `Mutable[T]`'s row in the 3-way comparison table |
| `examples/mutable-security-keys/main.go` (or extend an existing security example) | Runnable JWKS-style key-rotation demo: background reload loop calling `Set`, a `SecurityFunc` closure calling `Get()` |

## Out of scope (Phase 2 — no driver yet)

- Wiring `Mutable[T]` natively into `Builder.AddGlobalSecurity`/
  `WithSecurityScheme`'s `SecuritySchemes` map across `api/rest`/
  `api/events`/`api/reqreply`/`api/mcp` (making a Builder's OWN global
  security or scheme credentials live-reloadable without the caller
  writing their own `SecurityFunc`/`CredentialFunc`) — a real possible
  future step, but a genuinely bigger, more invasive change to four
  Builder types + their `RouteHandle`/`ChannelHandle`/`ToolHandle`
  snapshotting behavior (currently a COPY taken at `Register()` time).
  No concrete driver requesting this yet; ships as its own follow-on
  roadmap item if one appears — exactly how `validated-const-getter.md`
  deferred ITS Phase 2 until `go-edge-models` needed it.
- A generic "subscribe to changes" callback/notification mechanism on
  `Mutable[T]` (push a change event to other interested code elsewhere
  in a process) — `ReloadObserver` covers OBSERVABILITY of a reload,
  not REACTING to one. No driver yet.
- Folding `adapters/nethttp.NewCachingCredentialFunc`'s TTL/single-flight
  auto-refetch logic to be `Mutable[T]`-backed internally — a plausible
  future refactor once `Mutable[T]` ships, not scoped here (that
  function's existing behavior and tests are unaffected either way).
- A `TryGet`-style safe accessor for `Mutable[T]` — unnecessary, since
  construction guarantees a valid value always exists (no "unset"
  state to guard against, unlike `Immutable[T]`).
- **`OptionalMutable[T]`** — a `Mutable[T]` that may START unset (unlike
  today's `NewMutable`, which REQUIRES a valid `initial T`), becoming a
  normal `Mutable[T]` once a first value is supplied — e.g. an optional
  feature's rotating credential that might not exist until the feature is
  enabled. Now has its OWN dedicated design draft:
  [`docs/roadmap/optional-mutable.md`](optional-mutable.md) — promoted
  from a one-line flagged idea once the need surfaced a second time (see
  that doc's Motivation section). Explicitly sequenced to depend on THIS
  doc's `Mutable[T]` shipping first — do not implement `OptionalMutable[T]`
  before `Mutable[T]` exists.
- **`codex.Cacheable[T]`** — a 4th sibling adding a TTL/explicit-
  `Invalidate()` validity window on top of `Mutable[T]`'s re-validating
  `Set` shape (`Get()` returns `(T, bool)` instead of `Mutable[T]`'s plain
  `T`, the second value reporting freshness). Has its OWN dedicated design
  draft: [`docs/roadmap/cache-parity-and-cacheable.md`](cache-parity-and-cacheable.md)
  (that doc's Part 1, unrelated `ports.Cache` template-unification parity,
  has already shipped independently — only its `Cacheable[T]` design
  remains). Explicitly sequenced to depend on THIS doc's `Mutable[T]`
  shipping first — do not implement `Cacheable[T]` before `Mutable[T]`
  exists, same rule as `OptionalMutable[T]` above. Also inherits THIS
  doc's open `codex`↔`stats` import-cycle question unchanged (`Cacheable[T]`
  needs the same `stats.ReloadObserver`-shaped field `Mutable[T]` does).

## Open design decisions

- **File placement: extend `codex/const.go` or split into
  `codex/mutable.go`?** `getter.go` was split out from `const.go`
  specifically so the two-method `Getter`/`Setter` interfaces stayed
  decoupled from any one container implementation. `Mutable[T]` is a
  third CONTAINER (like `Const`/`Immutable`), so it arguably belongs
  alongside them in `const.go` — but `const.go` growing a third,
  meaningfully-different-shaped type (the only one with an Observer
  dependency) may be the point at which splitting into `mutable.go`
  reads better. Low-stakes; decide at implementation time, matching how
  `validated-const-getter.md` itself deferred an analogous naming
  question.
- **Does `Mutable[T]` need its own `stats` import inside `codex`?**
  `codex` currently has NO dependency on `stats` at all (deliberately —
  codec construction/validation has never needed observability before
  now). Confirm this import direction is acceptable (`stats` already
  depends on `codex` for `ReportErrors`/`codex.ValidationErrors`, so
  `codex` importing `stats` back would be a NEW, circular-looking
  dependency the two packages must not actually form — check whether
  `stats.Observer`/`ReloadObserver` need to move to a lower-level
  shared package, or whether `codex.Mutable[T]`'s Observer field should
  instead be a minimal LOCAL interface in `codex` (just
  `RecordReload(string, bool, time.Duration)`, no `stats` import at
  all) that `stats.Observer`-based callers satisfy structurally. This
  is the single biggest open question before implementation starts.
- **Should `NewMutable`'s `location` be required, or optional
  (defaulting to some generic string) for callers who don't care about
  observability?** Leaning required (it's one extra constructor
  argument, and a Mutable used in production long enough to need
  rotation is a Mutable worth naming) — revisit if it proves annoying
  in practice.
