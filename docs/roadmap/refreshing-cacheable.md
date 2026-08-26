# `ports.RefreshingCacheable[T]` — an auto-refreshing wrapper over `codex.Cacheable[T]`

> **Status:** Design complete — `codex.Cacheable[T]` has now shipped,
> ready to implement.
> **Depends on:** `codex.Cacheable[T]` (see `docs/concepts/codec.md`'s
> `Cacheable[T]` subsection and `codex/cacheable.go`) — SHIPPED; this
> doc composes a real `*codex.Cacheable[T]` instance.
> [← Back to Roadmap](index.md)

## Motivation

`codex.Cacheable[T]` (designed in the companion doc) is deliberately
PASSIVE: `Get()` never performs I/O, and a caller is entirely
responsible for calling `Set()`/`Invalidate()` when a value changes or
expires. That's the right default for a `codex`-level primitive (`codex`
has no I/O dependency at all, and `Cacheable[T]`'s own doc is explicit
that it stays that way).

But `adapters/nethttp.NewCachingCredentialFunc` already proves a
DIFFERENT, equally real pattern is wanted: TTL-based, single-flighted,
auto-refresh-on-expiry — "give me a valid value, and if it's stale,
fetch a fresh one for me, sharing that one fetch across every
concurrently-blocked caller." That function is narrowly scoped to one
HTTP-client credential use case today. A generic version, built ON TOP
of `codex.Cacheable[T]` (composition, not a change to `Cacheable[T]`
itself), would let ANY adapter/port — Redis, MQTT5 security, MCP
resources, or an application's own expensive computation — get the same
convenience without hand-rolling single-flight+TTL logic per call site.

This is a **port-level** concern (I/O, concurrency policy, `ctx`
propagation), not a `codex`-level one — it lives in `ports/`, mirroring
where `ports.Cache`/`ports.File` already live as the I/O-aware layer on
top of `codex`'s I/O-free primitives.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `ports.RefreshingCacheable[T]` — composes an internal `*codex.Cacheable[T]` + a caller-supplied refresh function | Any change to `codex.Cacheable[T]` itself — it stays passive; this wrapper adds behavior around it, not inside it |
| `ports.RefreshFunc[T] func(ctx context.Context) (T, error)` — the caller-supplied fetch | Distributed/cross-process refresh coordination — single-flight here is PER-PROCESS only, the SAME limitation `adapters/nethttp.NewCachingCredentialFunc` already has and documents |
| `GetOrRefresh(ctx) (T, error)` — the ONLY I/O-triggering method; single-flights refresh on staleness | Rebasing `adapters/nethttp.NewCachingCredentialFunc` onto this wrapper — noted as a plausible FUTURE follow-up, not required; that function's existing behavior/tests are unaffected either way |
| Passthrough `Get()`/`Invalidate()` — delegate to the embedded `Cacheable[T]` unchanged, still I/O-free | A generic "retry with backoff" policy on refresh failure — no driver yet; `GetOrRefresh` fails or falls back exactly once per call, no internal retry loop |
| A new Observer event for refresh attempts | — |

## API surface

```go
package ports

import (
	"context"
	"sync"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// RefreshFunc fetches a fresh T — typically a network call (an HTTP
// request, a JWKS endpoint fetch, a Redis round trip). Called by
// [RefreshingCacheable.GetOrRefresh] at most ONCE per staleness window,
// no matter how many concurrent callers observe the same staleness —
// every such caller shares the SAME in-flight call (single-flight),
// mirroring [adapters/nethttp.NewCachingCredentialFunc]'s existing
// hand-rolled join point.
type RefreshFunc[T any] func(ctx context.Context) (T, error)

// RefreshingCacheableOpt configures a [RefreshingCacheable] at
// construction time.
type RefreshingCacheableOpt[T any] func(*RefreshingCacheable[T])

// WithFailHardOnRefreshError changes GetOrRefresh's default failure
// policy: by default, a failed refresh returns the STALE value with a
// nil error (stale-while-revalidate — see "Open design decisions" for
// why this is the default). WithFailHardOnRefreshError makes a failed
// refresh return the zero value and the refresh error instead, even
// when a stale value is available.
func WithFailHardOnRefreshError[T any]() RefreshingCacheableOpt[T]

// WithRefreshObserver sets the [stats.Observer] whose refresh-attempt
// extension (if implemented) receives an event on every GetOrRefresh
// call that triggers an actual refresh (a cache hit — value still
// fresh — fires NO event, mirroring
// [stats.CredentialCacheObserver.RecordCredentialCacheHit]'s "only
// report the interesting case" convention).
func WithRefreshObserver[T any](obs stats.Observer) RefreshingCacheableOpt[T]

// RefreshingCacheable wraps a [codex.Cacheable] with an auto-refresh
// policy: [RefreshingCacheable.GetOrRefresh] triggers refresh, calling
// [RefreshFunc], is the ONLY method that performs I/O — [Get]/
// [Invalidate] stay pass-through, I/O-free delegations to the embedded
// Cacheable, unchanged from calling them on it directly.
type RefreshingCacheable[T any] struct {
	cache    *codex.Cacheable[T]
	refresh  RefreshFunc[T]
	failHard bool
	obs      stats.Observer

	mu       sync.Mutex   // guards inflight — the single-flight join point
	inflight *refreshCall[T]
}

// refreshCall represents an in-flight [RefreshFunc] invocation shared by
// every concurrent [RefreshingCacheable.GetOrRefresh] caller that
// observes staleness at the same time — mirrors
// [adapters/nethttp.credentialCacheCall] exactly (hand-rolled
// channel/done join, no external dependency).
type refreshCall[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// NewRefreshingCacheable validates initial against codec (via the
// embedded [codex.NewCacheable] — same fallible construction, same
// reasoning: initial is real runtime input) and returns a
// *RefreshingCacheable[T] wrapping it with refresh.
func NewRefreshingCacheable[T any](
	location string, initial T, codec codex.Codec[T], ttl time.Duration,
	refresh RefreshFunc[T], opts ...RefreshingCacheableOpt[T],
) (*RefreshingCacheable[T], error)

// GetOrRefresh returns the current value, refreshing it first if
// [codex.Cacheable.IsStale] is true. Concurrent stale callers share ONE
// [RefreshFunc] call (single-flight). On a successful refresh, the new
// value is [codex.Cacheable.Set] and returned. On a FAILED refresh, the
// default policy returns the STALE value with a nil error
// (stale-while-revalidate) — pass [WithFailHardOnRefreshError] to
// return the refresh error instead. A fresh value never triggers
// refresh at all — GetOrRefresh degrades to a plain [codex.Cacheable.Get]
// in that case, at the same (near-zero) cost.
func (r *RefreshingCacheable[T]) GetOrRefresh(ctx context.Context) (T, error)

// Get is a pass-through to the embedded [codex.Cacheable.Get] — no I/O,
// no refresh triggered. Use this when a caller wants the CURRENT value
// (fresh or stale) without ever blocking on a fetch.
func (r *RefreshingCacheable[T]) Get() (value T, fresh bool)

// Invalidate is a pass-through to the embedded [codex.Cacheable.Invalidate].
func (r *RefreshingCacheable[T]) Invalidate()
```

No new structured error type — `GetOrRefresh`'s only failure mode is
whatever `RefreshFunc` itself returns, propagated unchanged (or
swallowed in favor of the stale value, per the default policy above).

## Observer integration

**NEW** — a refresh-attempt event, distinct from `Cacheable`'s own
`stats.ReloadObserver` (which fires on `Set`, not on the DECISION to
refresh). Working shape, closely mirroring the existing
`stats.CredentialCacheObserver`:

```go
// RefreshObserver is an optional extension to Observer for
// [ports.RefreshingCacheable] refresh-attempt events. Only fires when
// GetOrRefresh actually triggers a refresh (a fresh-value hit fires
// nothing, mirroring CredentialCacheObserver's convention).
type RefreshObserver interface {
	RecordRefreshAttempt(location string, success bool, duration time.Duration)
}
```

Open question: reuse `stats.CredentialCacheObserver` itself (same
shape, arguably the SAME concept generalized beyond HTTP credentials) or
add this as its own new interface — resolve at implementation time by
checking whether `CredentialCacheObserver`'s existing doc comment and
name would still read sensibly for a non-HTTP-credential caller (a
JWKS refresh, a Redis-backed value, etc.).

## Unit test plan

| Test | Verifies |
|---|---|
| `TestNewRefreshingCacheable_ValidInitial_ReturnsWrapper` | Valid `initial` + codec → non-nil wrapper |
| `TestNewRefreshingCacheable_InvalidInitial_ReturnsError` | Invalid `initial` → error, mirrors `NewCacheable` |
| `TestGetOrRefresh_Fresh_NoRefreshCalled` | A fresh value → `GetOrRefresh` returns it without invoking `RefreshFunc` at all |
| `TestGetOrRefresh_Stale_CallsRefreshOnce` | A stale value → `RefreshFunc` invoked exactly once, new value returned and `Set` |
| `TestGetOrRefresh_ConcurrentStale_SingleFlights` | N concurrent `GetOrRefresh` calls on a stale value → `RefreshFunc` invoked EXACTLY once, all N callers receive the same result |
| `TestGetOrRefresh_RefreshFails_DefaultReturnsStaleValue` | Failed refresh (default policy) → returns the stale value, nil error |
| `TestGetOrRefresh_RefreshFails_FailHardOptReturnsError` | Failed refresh with `WithFailHardOnRefreshError` → returns zero value + the refresh error |
| `TestGet_NeverTriggersRefresh` | Plain `Get()` on a stale value returns it with `fresh=false`, `RefreshFunc` never called |
| `TestInvalidate_PassesThroughToCacheable` | `Invalidate()` makes the NEXT `GetOrRefresh` treat the value as stale |
| `TestGetOrRefresh_CallsRefreshObserverOnRefresh` | A triggered refresh fires `RecordRefreshAttempt` |
| `TestGetOrRefresh_FreshHit_NoObserverCall` | A fresh hit fires NO `RecordRefreshAttempt` (mirrors `CredentialCacheObserver`'s hit/refresh split) |
| `ExampleRefreshingCacheable` | pkg.go.dev-visible usage sketch — JWKS-style auto-refresh |

## Files to create

| File | Responsibility |
|---|---|
| `ports/refreshing_cacheable.go` | `RefreshFunc[T]`, `RefreshingCacheableOpt[T]`, `WithFailHardOnRefreshError`, `WithRefreshObserver`, `RefreshingCacheable[T]`, `NewRefreshingCacheable`, `GetOrRefresh`, `Get`, `Invalidate` |
| `ports/refreshing_cacheable_test.go` | Full unit test plan above |
| `stats/observer.go` | `RefreshObserver` extension (or generalize `CredentialCacheObserver` — see "Open question" above) |
| `docs/features/ports.md` (doc-only) | New subsection introducing `RefreshingCacheable[T]` alongside the existing `Cache`/`File` port docs |
| `examples/refreshing-cacheable` (or extend `examples/mutable-security-keys`) | Runnable auto-refresh demo |

## Relationship to the Redis-backed `Cacheable[T]` sibling

[`cacheable-redis-backed.md`](cacheable-redis-backed.md) designs a
Redis-backed sibling (`adapters/redis.Cacheable[T]`, composing
`ports.Cache[T]` + a `Commands` client, with a necessarily different,
ctx-aware, fallible `Get` signature). Once BOTH that sibling and this
wrapper exist, a
natural forward-looking possibility is `RefreshingCacheable[T]`
composing EITHER backing (in-memory `codex.Cacheable[T]` or the
Redis-backed sibling) interchangeably, refreshing whichever one is
stale via the same `RefreshFunc[T]` mechanism. **Not a requirement of
this doc** — flagged only so the two designs are known to compose
cleanly if/when both ship.

## Out of scope

- Any change to `codex.Cacheable[T]` — this wrapper only composes it.
- Rebasing `adapters/nethttp.NewCachingCredentialFunc` onto this
  wrapper — a plausible future refactor, not required; that function's
  existing behavior and tests are unaffected regardless.
- Distributed/cross-process single-flight (e.g. via Redis `SETNX` as a
  distributed lock) — per-process only, matching
  `NewCachingCredentialFunc`'s existing, documented limitation.
- Retry-with-backoff on refresh failure — `GetOrRefresh` attempts
  exactly once per staleness window; a caller wanting backoff wraps
  their own `RefreshFunc` with retry logic.

## Open design decisions

- **Stale-while-revalidate vs. fail-hard as the DEFAULT policy.**
  Leaning stale-while-revalidate (matches `NewCachingCredentialFunc`'s
  own behavior is NOT quite analogous — that function has no
  "keep serving old creds on failure" fallback today, it just returns
  the error) — this is actually a genuinely OPEN question, not a
  precedent to copy. A concrete driving example (e.g. "if a JWKS
  refresh fails, would you rather keep verifying against the
  last-known-good keys, or reject all requests until refresh
  succeeds?") should settle this before implementation, and the answer
  may reasonably be "it depends," in which case keeping BOTH behaviors
  available via `WithFailHardOnRefreshError` (as designed above) is the
  right call regardless of which is the default.
- **Reuse `stats.CredentialCacheObserver` or add a new `RefreshObserver`?**
  See "Observer integration" above.
- **Should refresh calls have their own timeout, separate from the
  caller's `ctx`?** Not designed above — `GetOrRefresh(ctx)` passes ctx
  straight through to `RefreshFunc`; a caller wanting an independent
  refresh timeout wraps their own `RefreshFunc` with `context.WithTimeout`.
  Revisit if this proves insufficient in practice.
