# Cache Template Parity + `codex.Cacheable[T]` — `ports`, `adapters/redis`, `codex`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [Reloadable Value Containers — `Mutable`, `NewConst`](reloadable-value-containers.md)
> (Part 2 below builds directly on that doc's not-yet-implemented `Mutable[T]`)
> · [`ports.RefreshingCacheable[T]`](refreshing-cacheable.md) (auto-refresh
> wrapper, depends on this doc's `Cacheable[T]` shipping first) ·
> [Redis Cache Adapter (shipped)](../features/redis.md) ·
> [Redis — Phase 2: Pub/Sub](redis-pubsub.md)

## Motivation

Two related questions came up together: (1) does `ports.Cache` — Redis
cache keys declared as `{var}`-templated paths, exactly the same shape
`rest`/`events`/`reqreply`/`ports.File`/`ports.Dir` all use — actually
share the SAME underlying template-substitution primitive those
boundaries were unified onto (`codex.BuildFromParams`/
`internal/templatematch.Build`)? And (2) now that `Mutable[T]` is
designed (reloadable, re-validated cell — see the companion roadmap
doc), what about a value container with an explicit notion of
**validity** — a TTL, or an explicit "this is now stale" signal — the
in-process analogue of what `ports.Cache`'s own `TTL` field already
means for a REMOTE Redis-backed cache entry?

This doc answers both, as two independent but related phases in one
package review.

## Part 1 — `ports.Cache` template-unification parity audit

**Audited against**: `rest.PathParam`/`events.TopicParam`/
`reqreply.TopicParam` (all now thin wrappers over `codex.Param`/
`MergedParam[T]`, `Build`/`Validate` delegating to
`codex.BuildFromParams`/`ValidateParams`), and `ports.FilePathParam`/
`ports.DirPathParam` (same delegation, kept as their own error types at
the boundary). Result: **`ports.Cache` was NOT included in that
unification pass** — two concrete, confirmed gaps, both cheap and
low-risk to close, with an exact precedent already shipped for each:

1. **`Cache.BuildKey`/`Cache.ValidateKeyVars` hand-roll their own
   `{var}` substitution loop** (a manual `strings.IndexByte('{')`/
   `IndexByte('}')` scan in `BuildKey`) instead of delegating to
   `codex.BuildFromParams`/`codex.ValidateParams` — the ONE shared
   engine (`internal/templatematch.Build`) every other boundary's
   template substitution now goes through. This means Cache key
   templates don't benefit from any future `templatematch` fix/feature
   for free, and are the only boundary with a bespoke, untested-against-
   the-same-fixture substitution path. Fix: rewrite both methods to
   build a `[]codex.Param` from `c.params` (mirroring
   `ports/file.go`'s existing `toCodexFileParams`-style helper) and
   delegate to `codex.BuildFromParams`/`codex.ValidateParams`,
   translating the shared `codex.MissingParamError`/`codex.ParamError`
   back into Cache's own `CacheKeyError`/`CacheKeyParamError` at the
   boundary — the EXACT pattern `ports.File`'s `convertFileParamErr`/
   `ports.Dir`'s `convertDirParamErr` already established. `CacheKeyError`/
   `CacheKeyParamError` stay their own distinct types (same rationale as
   File/Dir: their `LogValue()` keys differ from `codex.ParamError`'s,
   and existing tests assert the exact keys) — only the INTERNAL
   substitution engine changes, not Cache's public error shape.
2. **`ports.Cache` has no reusable-shape escape hatch.** Every other
   boundary now has one — `rest.Path`+`NewRouteFromPath`,
   `events.Topic`+`NewChannelFromTopic`, `reqreply.Topic`+
   `NewRouteFromTopic`, `mcp`'s Template+`NewResourceFromTemplate`,
   `ports.FilePathTemplate`+`NewFileFromPathTemplate`,
   `ports.DirPathTemplate`+`NewDirFromPathTemplate` — for the case
   where the SAME key template and `CacheKeyParam`s are shared by two or
   more `Cache[T]` declarations of DIFFERENT value types (e.g. one Redis
   key family, `"session:{id}"`, caching both a `SessionMeta` value under
   one call site and a `SessionToken` value under another, same `{id}`
   variable). Cache is now the ONLY boundary missing this. Fix: add
   `ports.CacheKeyTemplate` (mirrors `FilePathTemplate` exactly:
   `Template string`, `Params []CacheKeyParam`, `BuildKey`/
   `ValidateKeyVars` methods) + `ports.NewCacheKeyTemplate(template
   string, params ...CacheKeyParam) CacheKeyTemplate` +
   `ports.NewCacheFromKeyTemplate[T any](t CacheKeyTemplate, f
   format.Format[T], opts ...CacheOpt) Cache[T]`.

**Not a gap, confirmed while auditing:** `ports.Cache` has no
`ValidateDeclaredParams`-style "a declared param name doesn't appear in
the template" check at construction time — but neither do `ports.File`/
`ports.Dir`, for the same reason: that check only exists for `rest`/
`events`/`reqreply` because THEY have a `.Register(builder)` step to run
it at; `File`/`Dir`/`Cache` are all "no builder, no spec" boundaries
(see `docs/concepts/declaring-apis-and-ports.md`), so there is no
natural point to run this check other than at `BuildKey`/`ValidateKeyVars`
time, where an unknown var is already harmless (silently ignored, exactly
matching `File`/`Dir`'s existing behavior). No change proposed here —
this is consistent, not a Cache-specific gap.

**`adapters/redis` requires NO changes.** `binding.go`'s `GetAdapter`/
`SetAdapter`/`DrainSetAdapter`/`Seed` all call `cache.BuildKey(vars)` and
never touch the substitution internals directly — the Part 1 fix is
fully transparent to the adapter layer, exactly like the `ports.File`/
`ports.Dir` Phase 5 migration was transparent to `adapters/file`.

### Part 1 test plan

| Test | Verifies |
|---|---|
| `TestCache_BuildKey_DelegatesToBuildFromParams` | `BuildKey` output is byte-identical before/after the refactor for a representative template+params fixture |
| `TestCache_BuildKey_MissingVar_ReturnsCacheKeyError` | Missing var still returns `CacheKeyError` (translated from `codex.MissingParamError`) with the SAME `Key`/`Var` fields as before |
| `TestCache_BuildKey_CodecFailure_ReturnsCacheKeyParamError` | Codec failure still returns `CacheKeyParamError` (translated from `codex.ParamError`) with the SAME fields, `Unwrap` reaching the underlying error |
| `TestCache_ValidateKeyVars_Unchanged` | `ValidateKeyVars`'s existing behavior/error shape is unchanged |
| `TestNewCacheKeyTemplate_BuildKey_RoundTrip` | `CacheKeyTemplate.BuildKey` substitutes correctly |
| `TestNewCacheFromKeyTemplate_ProducesIdenticalCacheToNewCache` | A `Cache[T]` built via `NewCacheFromKeyTemplate` is byte-for-byte identical (same `Key`, same `BuildKey` output) to one built via `NewCache` with the same template+params inline |
| `TestCacheKeyError_LogValue_KeysUnchanged` | Regression guard: `CacheKeyError`/`CacheKeyParamError`'s `LogValue()` keys are UNCHANGED after the internal refactor (existing tests already assert this — this test just makes the invariant explicit for THIS change) |

## Part 2 — `codex.Cacheable[T]`: a validated cell with TTL/staleness

Extends the (not yet implemented) `Const`/`Immutable`/`Mutable` family
from `reloadable-value-containers.md` with a 4th sibling. Where
`Mutable[T]` is a plain "read current / explicitly set new" cell with no
notion of time, `Cacheable[T]` adds the ONE thing `ports.Cache`'s own
`TTL` field already means for a remote Redis entry — a validity window —
to an IN-PROCESS value cell, plus an explicit invalidation signal for
callers who know a value is stale before its TTL naturally expires
(e.g. an upstream webhook, or a Redis keyspace-notification, tells the
process "this changed").

**This is deliberately an in-process, single-value memoization cell —
NOT a competing implementation of `ports.Cache`/`adapters/redis`.**
`ports.Cache` is about a REMOTE, keyed, potentially-shared cache backed
by an actual Redis server; `Cacheable[T]` is about ONE local Go value a
single process wants to avoid recomputing/refetching too often, with no
network, no key template, and no sharing across processes at all. The
natural pairing: a process's `SecurityFunc`/`CredentialFunc` (or any
expensive, infrequently-changing computation) wraps its result in a
`Cacheable[T]`, `Get()` returns the cached value AND whether it's still
fresh, and the caller decides what to do with a stale-but-present value
(serve it anyway with a background refresh — the common "stale-while-
revalidate" pattern — or block and refresh synchronously).

### API surface

```go
package codex

// Cacheable is an in-process value cell with an explicit validity
// window — a TTL, an explicit [Cacheable.Invalidate] call, or both.
// Builds on the SAME re-validated-cell shape as [Mutable] (every [Set]
// re-validates against the container's Codec[T] and replaces the
// current value on success) and adds exactly one new concept: IsStale.
//
// Unlike [Mutable], Get returns (T, bool) — the second value is false
// when the cached value has expired (TTL elapsed) OR been explicitly
// [Cacheable.Invalidate]d — so a caller can implement "stale-while-
// revalidate" (serve the stale value immediately, trigger a background
// Set) instead of the all-or-nothing [Mutable.Get].
type Cacheable[T any] struct {
	mu        sync.RWMutex
	value     T
	codec     Codec[T]
	ttl       time.Duration // zero = never expires from TTL alone
	expiresAt time.Time
	invalid   bool // explicit Invalidate() flag, independent of TTL
	location  string
	obs       stats.Observer
}

// CacheableOpt configures a [Cacheable] at construction time.
type CacheableOpt[T any] func(*Cacheable[T])

// WithCacheableReloadObserver mirrors [WithReloadObserver] — reuses the
// SAME [stats.ReloadObserver] extension [Mutable] uses, so a caller
// monitoring both containers needs only one Observer implementation.
func WithCacheableReloadObserver[T any](obs stats.Observer) CacheableOpt[T]

// NewCacheable validates initial against codec and returns a
// *Cacheable[T] whose value is fresh for ttl (zero = never expires
// from TTL alone — only [Cacheable.Invalidate] can make it stale).
// Mirrors [NewMutable]'s fallible construction exactly.
func NewCacheable[T any](location string, initial T, codec Codec[T], ttl time.Duration, opts ...CacheableOpt[T]) (*Cacheable[T], error)

// Get returns the current value and whether it is still fresh (true)
// or stale (false — TTL elapsed, or [Cacheable.Invalidate] was called
// since the last successful [Cacheable.Set]). NEVER panics — like
// [Mutable], construction guarantees a valid value always exists; a
// stale value is still returned, just flagged, for stale-while-
// revalidate callers who prefer a stale value over none at all.
func (c *Cacheable[T]) Get() (value T, fresh bool)

// Set validates value, and on success replaces the current value,
// resets the TTL window, and clears any prior [Cacheable.Invalidate].
// On failure the current value/freshness is UNCHANGED (last-good-
// value-wins, exactly like [Mutable.Set]) and the codec's own
// validation error is returned. Fires [stats.ReloadObserver.RecordReload]
// exactly like [Mutable.Set].
func (c *Cacheable[T]) Set(value T) error

// Invalidate marks the current value stale immediately, independent of
// the TTL window — for callers who learn a value changed out-of-band
// (a webhook, a Redis keyspace notification — see "Open design
// decisions"). The value itself is NOT cleared — [Cacheable.Get] still
// returns it, with fresh=false — so a caller can still serve it
// (stale-while-revalidate) while triggering a refresh.
func (c *Cacheable[T]) Invalidate()

// IsStale reports whether the current value is stale (TTL elapsed or
// Invalidate called) WITHOUT reading the value itself — for a caller
// that wants to decide whether to refresh before paying for a Get.
func (c *Cacheable[T]) IsStale() bool
```

No new structured error type — `Set`'s only failure mode is the
codec's own existing validation error, exactly like `Mutable.Set`.

### Relationship to the whole family

| | `Const[T]` | `Immutable[T]` | `Mutable[T]` | `Cacheable[T]` (NEW) |
|---|---|---|---|---|
| Re-`Set`-able? | Never | Once | Unlimited | Unlimited (same as `Mutable`) |
| Invalid `Set` | Panics (`MustConst`) — or returns an error (`NewConst`) | Returns a typed/codec error | Current value UNCHANGED, returns codec error | Same as `Mutable` — last-good-value-wins |
| **Notion of time/staleness?** | No | No | **No** — a value is just "current," forever, until replaced | **Yes** — TTL and/or explicit `Invalidate()`; a value can be *present but stale* |
| `Get()` return shape | `T` | `T` (panics if unset) | `T` (never panics) | **`(T, bool)`** — value + freshness flag, never panics |
| Interface satisfied | `Getter[T]` only | `GetterSetter[T]` | `GetterSetter[T]` (same as `Immutable[T]`) | **Does NOT satisfy `Getter[T]`** as designed — the 2-value return breaks the 1-value contract; see "Open design decisions" |
| Observer | None (no I/O boundary) | None | `stats.ReloadObserver.RecordReload` on `Set` | **Reuses the same** `ReloadObserver` for `Set`; `Invalidate()` may need its own event (`RecordInvalidate`) — open question |
| Typical driver | Path/topic pattern constants | Config/env var loaded once at startup | Rotating security credentials (JWKS, API keys) — "give me whatever is current" | Stale-while-revalidate memoization — "give me what you have, tell me if it's still good" |

**In one sentence:** `Mutable[T]` answers *"what's the current value?"*
— `Cacheable[T]` answers *"what's the current value, and can I still
trust it?"* Every `Mutable[T]` is functionally a `Cacheable[T]` with an
infinite TTL and no invalidation; `Cacheable[T]` adds a validity window
on top of `Mutable[T]`'s shape, not a separate mechanism underneath.

**Documentation note:** copy this table (or a close variant) into
`docs/concepts/codec.md`'s Getter/Setter subsection verbatim once BOTH
`Mutable[T]` and `Cacheable[T]` are implemented — it's a complete,
already-verified comparison across the whole 4-container family and
shouldn't be redrafted from scratch at documentation time.

### Observer integration

Reuses `stats.ReloadObserver` from `reloadable-value-containers.md`
unchanged (`RecordReload(location, success, duration)`) for `Set` calls.
**Open question:** does `Invalidate()` also deserve its own event (e.g.
a `RecordInvalidate(location string)` on a NEW, separate extension, or
folded into `ReloadObserver` as `RecordReload(location, false, 0)` with
duration=0 signaling "this was an explicit invalidation, not a failed
Set")? Leaning toward a SEPARATE `RecordInvalidate` call (an
invalidation is not a failed reload — conflating the two would make
"how many Set calls failed validation" and "how many times was this
invalidated" indistinguishable in a dashboard) — flagged as an open
design decision below, not resolved here.

### Possible future tie-in: Redis keyspace notifications

`docs/roadmap/redis-pubsub.md` already flags "Keyspace notifications...
interesting for cache invalidation, needs its own review" as an
unscoped idea. If Phase 2 of that doc ever ships keyspace-notification
support, a natural pairing would be: a subscriber on
`__keyspace@*__:<pattern>` calling `cacheable.Invalidate()` whenever the
corresponding Redis key changes — the REMOTE `ports.Cache` entry and a
LOCAL `Cacheable[T]` memoizing its decoded value staying in sync without
polling. **Not scoped here** — purely a note for whoever picks up
`redis-pubsub.md`'s Phase 2.

### Redis-backed `Cacheable[T]`: pluggable backing store

**Sequenced as a follow-up phase right after `codex.Cacheable[T]` itself
ships** (same dependency ordering already used for Part 1 vs. Part 2 —
this sub-phase depends on Part 2, not the other way round). Per explicit
request: once `codex.Cacheable[T]` exists, adapt `ports.Cache`/
`adapters/redis` so the SAME Cacheable[T]-shaped behavior (get a value
with a freshness flag, set a new one, invalidate, ask whether stale) can
be backed by an ACTUAL Redis entry instead of in-process memory — a
caller picks per-instance where the value lives, standalone in-memory
`codex.Cacheable[T]` or Redis-backed, without changing how they read it.

**This walks back this doc's earlier framing** ("deliberately NOT a
competing implementation of `ports.Cache`/`adapters/redis`... no
dependency on `adapters/redis` at all") for the SPECIFIC case of wanting
one value's storage to be pluggable — the in-memory/local vs.
remote/shared distinction described earlier still explains why
`Cacheable[T]` ITSELF stays a plain in-memory cell (no network, no
`ctx`, no error from `Get`); this subsection instead adds a SIBLING type
in `adapters/redis` satisfying an analogous (but not identical) shape.

**Resolves Part 2's open "does `Cacheable[T]` satisfy `Getter[T]`"
question as a prerequisite.** Introduce a new interface —
`codex.FreshGetter[T] interface { Get() (T, bool) }` — instead of
forcing `Getter[T]`'s single-value shape onto `Cacheable[T]`.
`codex.Cacheable[T]` implements it directly. This decision now has a
SECOND consumer (the Redis-backed sibling below), which strengthens the
case for resolving it this way rather than leaving `Cacheable[T]`
outside the interface family entirely.

**The Redis-backed sibling cannot have an IDENTICAL signature to
`codex.Cacheable[T]`** — a real network round trip needs a
`context.Context` and can fail, so its `Get` must be
`Get(ctx context.Context) (T, bool, error)`, not the in-memory
`Get() (T, bool)` (no ctx, never fails). This is documented as a
deliberate, necessary shape difference — mirrors how `Const`/
`Immutable`/`Mutable` are already separate concrete types satisfying a
shared but narrower interface, not forcing one identical signature
everywhere an I/O boundary differs.

Proposed shape:

```go
package redis // adapters/redis

// Cacheable is a Redis-backed sibling to [codex.Cacheable] — same
// freshness-aware Get/Set/Invalidate/IsStale SHAPE, but Get is
// ctx-aware and fallible (a real network round trip), and Invalidate
// issues an actual Redis DEL instead of an in-process flag flip.
//
// Composes a SINGLE, already-built [ports.Cache] entry (a fixed key —
// NOT a {var}-templated one; templated keys stay ports.Cache[T]'s own
// direct Get/Set API, this type is for the "one specific cached value"
// case Cacheable[T] itself addresses in-memory) + a [Commands] client.
type Cacheable[T any] struct { /* ... */ }

// NewCacheable composes cache (a single, already-BuildKey'd entry —
// vars supplies whatever {var}s cache.Key still has, typically none)
// with client into a Redis-backed Cacheable[T].
func NewCacheable[T any](client Commands, cache ports.Cache[T], vars map[string]string, opts ...CacheableOpt[T]) (*Cacheable[T], error)

// Get performs a real Redis GET (via redis.GetAdapter's underlying
// mechanism) and reports freshness from the Redis TTL — a MISS reports
// (zero, false, nil), NOT an error (mirrors ports.Cache/redis.GetAdapter's
// existing miss-is-not-an-error convention).
func (c *Cacheable[T]) Get(ctx context.Context) (value T, fresh bool, err error)

// Set writes value via redis.SetAdapter's underlying mechanism, using
// cache.TTL as the Redis expiry.
func (c *Cacheable[T]) Set(ctx context.Context, value T) error

// Invalidate issues a Redis DEL on the entry's key — the NEXT Get
// reports a miss (fresh=false) until the next Set.
func (c *Cacheable[T]) Invalidate(ctx context.Context) error

// IsStale performs a lightweight Redis TTL/EXISTS check without
// decoding the value — analogous to codex.Cacheable.IsStale, but this
// one is also ctx-aware and fallible.
func (c *Cacheable[T]) IsStale(ctx context.Context) (bool, error)
```

### Test plan

| Test | Verifies |
|---|---|
| `TestNewCacheable_ValidInitial_ReturnsCacheable` | Valid `initial` → non-nil `*Cacheable[T]`, `Get()` returns `(initial, true)` |
| `TestNewCacheable_InvalidInitial_ReturnsError` | Invalid `initial` → `(nil, err)` |
| `TestCacheable_Get_FreshBeforeTTLElapses` | `Get()` within the TTL window returns `fresh=true` |
| `TestCacheable_Get_StaleAfterTTLElapses` | `Get()` after the TTL window returns the SAME value with `fresh=false` |
| `TestCacheable_ZeroTTL_NeverExpiresFromTTLAlone` | `ttl=0` → `Get()` stays fresh indefinitely until an explicit `Invalidate` |
| `TestCacheable_Invalidate_MarksStaleImmediately` | `Invalidate()` then `Get()` returns `fresh=false` even within the TTL window |
| `TestCacheable_SetValid_ResetsFreshnessAndTTL` | `Set` after `Invalidate`/TTL-expiry makes the NEXT `Get()` fresh again |
| `TestCacheable_SetInvalid_KeepsPreviousValueAndFreshness` | Invalid `Set` leaves both the value AND freshness state unchanged |
| `TestCacheable_IsStale_MatchesGetFreshness` | `IsStale()` and `Get()`'s `fresh` bool never disagree |
| `TestCacheable_ConcurrentGetSetInvalidate_NoRace` | `go test -race` clean |
| `TestCacheable_ImplementsGetterInterface` | Compile-time assertion — `Get()`'s two-value return means `Cacheable[T]` does NOT satisfy `Getter[T]`/`GetterSetter[T]` as-is (see "Open design decisions") |
| `TestCacheable_Set_CallsReloadObserver` | Mirrors `Mutable`'s observer tests |
| `ExampleCacheable` | pkg.go.dev-visible usage sketch — stale-while-revalidate memoization |

### Files to create

| File | Responsibility |
|---|---|
| `codex/const.go` (or `codex/mutable.go`/`codex/cacheable.go` — same open file-placement question as `reloadable-value-containers.md`) | `Cacheable[T]`, `CacheableOpt[T]`, `WithCacheableReloadObserver`, `NewCacheable` |
| `codex/getter.go` | New `FreshGetter[T]` interface (`Get() (T, bool)`) — `Cacheable[T]` implements it |
| `codex/const_test.go` (or split) | Full Part 2 test plan |
| `adapters/redis/cacheable.go` (follow-up phase, after `codex.Cacheable[T]` ships) | `redis.Cacheable[T]`, `NewCacheable`, ctx-aware `Get`/`Set`/`Invalidate`/`IsStale` |
| `adapters/redis/cacheable_test.go` | Redis-backed sibling's own test plan (miss-is-not-error, TTL-driven freshness, DEL-based invalidation) |
| `docs/concepts/codec.md` (doc-only) | Extend the Getter/Setter subsection's comparison table with `Cacheable[T]`'s row (see "Documentation note" above) |
| `docs/features/redis.md` (doc-only) | New subsection introducing the Redis-backed `Cacheable[T]` sibling alongside the existing `Cache`/adapter docs |
| `examples/mutable-security-keys` (extend, or a new `examples/cacheable-memoization`) | Runnable stale-while-revalidate demo |

## Out of scope

- Any change to `ports.Cache`'s PUBLIC shape beyond the two Part 1
  additions (`CacheKeyTemplate`/`NewCacheFromKeyTemplate`) — `Cache[T]`
  itself, `CacheKeyParam`/`MergedCacheKeyParam[T]`/`NewCacheKeyParam[T]`,
  and every existing adapter function signature are UNCHANGED. The
  Redis-backed `Cacheable[T]` sibling is a NEW, ADDITIVE type in
  `adapters/redis` — it does not change `ports.Cache` itself.
- Redis keyspace-notification-driven invalidation — belongs to
  `redis-pubsub.md`'s own, still-unscoped Phase 2 idea, not this doc
  (though the Redis-backed `Cacheable[T]`'s `Invalidate(ctx)` would be
  a natural call site for it, once both exist).
- A generic multi-key `Cacheable` (an in-process map of many cached
  values, e.g. keyed by ID) — no driver yet; `Cacheable[T]` stays a
  single-value cell, mirroring `Mutable[T]`'s own single-value scope.
  This applies to the Redis-backed sibling too — one `redis.Cacheable[T]`
  instance is one fixed key, not a templated family.
- Auto-refresh (fetch a fresh value automatically when stale) for
  EITHER the in-memory or Redis-backed `Cacheable[T]` — designed
  separately as its own follow-on: see
  [`ports.RefreshingCacheable[T]`](refreshing-cacheable.md).

## Open design decisions

- **Does `Cacheable[T]` satisfy `Getter[T]`/`GetterSetter[T]`?** As
  designed above, `Get() (T, bool)` does NOT match `Getter[T]`'s
  `Get() T` signature — `Cacheable[T]` would NOT satisfy the existing
  interface. Options: (a) leave it as its own shape, not part of the
  `Getter`/`Setter` interface family at all (simplest, but breaks the
  "any future container satisfies these interfaces" promise
  `getter.go`'s own doc comment makes); (b) add a THIRD interface,
  `FreshGetter[T] interface { Get() (T, bool) }`, that `Cacheable[T]`
  satisfies instead; (c) keep `Get() T` (panics or returns the stale
  value silently) and add a SEPARATE `Fresh() bool` method instead of
  bundling freshness into `Get`'s return. **Leaning toward (b)** — the
  Redis-backed sibling subsection above gives `FreshGetter[T]` a SECOND
  consumer (even though the Redis-backed type's `Get` needs its own
  ctx/error-aware variant, not `FreshGetter[T]` itself, the SHAPE
  precedent — "Get returns a freshness flag" — is shared), which
  strengthens the case for resolving this as a real interface rather
  than a one-off method shape. Still needs a final decision before
  implementation — this is the single biggest open question in Part 2.
- **Should `Invalidate()` fire its own Observer event, or reuse
  `RecordReload`?** See "Observer integration" above — leaning toward a
  new `RecordInvalidate(location string)`, not finalized.
- **File placement** — same question `reloadable-value-containers.md`
  already carries forward (`const.go` vs. a split file); `Cacheable[T]`
  makes a 4th type in the family, which may tip the balance toward
  splitting regardless of what's decided for `Mutable[T]` alone.
- **Should Part 1 (Cache template parity) ship independently of Part 2
  (`Cacheable[T]`), given Part 1 is small/precedented and Part 2 is
  genuinely new design?** Likely yes — nothing in Part 2 depends on
  Part 1 shipping first, and Part 1's risk profile is low enough it
  could reasonably be implemented as soon as this doc is reviewed,
  without waiting for Part 2's open questions to resolve.
