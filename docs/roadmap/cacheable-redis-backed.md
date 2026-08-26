# Redis-Backed `Cacheable[T]` Sibling & Multi-Key `Cacheable` — `adapters/redis`, `codex`

> **Status:** Design complete — ready to implement. Explicitly SEQUENCED
> to implement AFTER `codex.Cacheable[T]` (`codex/cacheable.go`) ships —
> that dependency is now SATISFIED (see `docs/concepts/codec.md`'s
> Getter/Setter subsection for the shipped in-memory design this doc's
> sibling type composes).
> [← Back to Roadmap](index.md)
>
> This doc preserves the "Redis-backed `Cacheable[T]`: pluggable backing
> store" section and the "generic multi-key `Cacheable`" idea from the
> now-retired `cache-parity-and-cacheable.md` (fully shipped — Part 1,
> `ports.Cache` template-unification parity, and Part 2, the in-memory
> `codex.Cacheable[T]`, both landed; nothing else was left to track).
> See also: [`ports.RefreshingCacheable[T]`](refreshing-cacheable.md)
> (auto-refresh wrapper, depends on the Redis-backed sibling here OR the
> in-memory `codex.Cacheable[T]` — designed to compose either
> interchangeably) · [Redis Cache Adapter (shipped)](../features/redis.md) ·
> [Redis — Phase 2: Pub/Sub](redis-pubsub.md) (keyspace-notification-driven
> invalidation is THAT doc's own unscoped idea, not this one — though the
> Redis-backed `Invalidate(ctx)` below would be a natural call site for it
> once both exist).

## Motivation

`codex.Cacheable[T]` (shipped) is deliberately an IN-PROCESS, single-
value memoization cell — no network, no `ctx`, no error from `Get`. Per
explicit request when that design was drafted: once it existed, adapt
`ports.Cache`/`adapters/redis` so the SAME `Cacheable[T]`-shaped behavior
(get a value with a freshness flag, set a new one, invalidate, ask
whether stale) can be backed by an ACTUAL Redis entry instead of
in-process memory — a caller picks per-instance where the value lives,
standalone in-memory `codex.Cacheable[T]` or Redis-backed, without
changing how they read it.

**This walks back `codex.Cacheable[T]`'s own framing** ("deliberately
NOT a competing implementation of `ports.Cache`/`adapters/redis`... no
dependency on `adapters/redis` at all") for the SPECIFIC case of wanting
one value's storage to be pluggable — the in-memory/local vs.
remote/shared distinction still explains why `codex.Cacheable[T]` ITSELF
stays a plain in-memory cell; this doc instead adds a SIBLING type in
`adapters/redis` satisfying an analogous (but not identical) shape.

## API surface

**Cannot have an IDENTICAL signature to `codex.Cacheable[T]`** — a real
network round trip needs a `context.Context` and can fail, so `Get` must
be `Get(ctx context.Context) (T, bool, error)`, not the in-memory
`Get() (T, bool)` (no ctx, never fails). This is a deliberate, necessary
shape difference — mirrors how `Const`/`Immutable`/`Mutable`/`Cacheable`
are already separate concrete types satisfying a shared but narrower
interface, not forcing one identical signature everywhere an I/O
boundary differs. Consequently this sibling does NOT satisfy
`codex.FreshGetter[T]` itself — only the SHAPE (Get returns a freshness
flag) carries over, not the interface.

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
// case codex.Cacheable[T] itself addresses in-memory) + a [Commands]
// client.
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

`CacheableOpt[T]` mirrors `codex.CacheableOpt[T]`'s shape (an Observer
wiring option at minimum) — reuses `codex.ReloadObserver`/
`codex.InvalidateObserver` (shipped) for `Set`/`Invalidate` events,
exactly like the in-memory type does, so a caller monitoring both the
in-memory and Redis-backed siblings needs only one Observer
implementation.

## Test plan

| Test | Verifies |
|---|---|
| `TestRedisCacheable_Get_Miss_ReturnsNotFreshNoError` | A cache miss reports `(zero, false, nil)` — NOT an error, mirroring `redis.GetAdapter`'s existing convention |
| `TestRedisCacheable_Get_Hit_ReturnsValueAndFreshness` | A hit within the Redis TTL reports `(value, true, nil)` |
| `TestRedisCacheable_Get_ExpiredTTL_ReturnsStale` | A hit past the Redis TTL reports `(value, false, nil)` (or a miss, depending on Redis's own TTL-expiry semantics — needs verifying against the fake `Commands` implementation's TTL model) |
| `TestRedisCacheable_Set_WritesWithCacheTTL` | `Set` writes via the underlying `SetAdapter` mechanism using `cache.TTL` as the Redis expiry |
| `TestRedisCacheable_Invalidate_IssuesRedisDel` | `Invalidate` issues a real Redis `DEL`; the NEXT `Get` reports a miss |
| `TestRedisCacheable_IsStale_MatchesGetFreshness` | `IsStale(ctx)` and `Get(ctx)`'s `fresh` bool never disagree, for the same underlying state |
| `TestRedisCacheable_Set_CallsReloadObserver` | Mirrors `codex.Cacheable`'s own observer test |
| `TestRedisCacheable_Invalidate_CallsInvalidateObserver` | Mirrors `codex.Cacheable`'s own observer test |
| `ExampleCacheable` (adapters/redis) | pkg.go.dev-visible usage sketch |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/redis/cacheable.go` | `redis.Cacheable[T]`, `redis.CacheableOpt[T]`, `NewCacheable`, ctx-aware `Get`/`Set`/`Invalidate`/`IsStale` |
| `adapters/redis/cacheable_test.go` | Full test plan above (miss-is-not-error, TTL-driven freshness, DEL-based invalidation, Observer wiring) |
| `docs/features/redis.md` (doc-only) | New subsection introducing the Redis-backed `Cacheable[T]` sibling alongside the existing `Cache`/adapter docs |

## Also captured here: a generic multi-key `Cacheable`

A generic multi-key `Cacheable` (an in-process map of many cached
values, e.g. keyed by ID) — **no driver yet**; `codex.Cacheable[T]`
stays a single-value cell, mirroring `Mutable[T]`'s own single-value
scope. This applies to the Redis-backed sibling too — one
`redis.Cacheable[T]` instance is one fixed key, not a templated family.
If a driver appears, this would likely need its own roadmap doc (the
design space — sharding/eviction policy for the in-memory map, whether
per-key TTLs can differ, whether the Redis-backed sibling would need a
`{var}`-templated `ports.Cache` instead of a single fixed key — is
large enough to warrant a dedicated pass, not a quick addition here).

## Out of scope

- Any change to `ports.Cache`'s PUBLIC shape — `Cache[T]` itself,
  `CacheKeyParam`/`MergedCacheKeyParam[T]`/`NewCacheKeyParam[T]`, and
  every existing adapter function signature are UNCHANGED. The
  Redis-backed `Cacheable[T]` sibling is a NEW, ADDITIVE type in
  `adapters/redis` — it does not change `ports.Cache` itself.
- Redis keyspace-notification-driven invalidation — belongs to
  `redis-pubsub.md`'s own, still-unscoped Phase 2 idea, not this doc
  (though the Redis-backed `Cacheable[T]`'s `Invalidate(ctx)` would be
  a natural call site for it, once both exist).
- Auto-refresh (fetch a fresh value automatically when stale) for
  EITHER the in-memory or Redis-backed `Cacheable[T]` — designed
  separately as its own follow-on: see
  [`ports.RefreshingCacheable[T]`](refreshing-cacheable.md).
