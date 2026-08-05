# Async/Refreshable Credential Caching — `adapters/nethttp`

> **Status:** Design draft — awaiting a concrete driving use case before implementation.
> [← Back to Roadmap](index.md)
>
> See also: [Security & Authentication](../features/security.md) (`api/rest`'s shipped symmetric security model)

## Motivation

`nethttp.CallOptions.CredentialFunc` is called once per `Call`/`CallHandle`
invocation — there is no generic, reusable mechanism for a credential that
must be:

- **Cached across calls** for its natural lifetime (e.g., a Bearer token
  with a known TTL), instead of re-running an expensive auth flow (network
  round trip(s)) on every single request.
- **Transparently refreshed** once it expires, without the CALLER having to
  notice or handle it.
- **Retried once after a 401**, in case the cached credential expired
  slightly earlier than its advertised TTL (clock skew, server-side early
  revocation, etc.) — a single silent retry, not an unbounded loop.

Today, callers who want this build it themselves per-case — see
`examples/go-edge-models/docker/registry`'s `NewAuthCredentialFunc`, which
memoizes via `sync.Once` for the lifetime of ONE returned closure (correct
for "cache for one `GetImageMetadata` call," but NOT a general-purpose,
reusable, TTL-aware, refresh-on-expiry mechanism — `sync.Once` never resets,
so a long-lived `NewAuthCredentialFunc` value would cache its token FOREVER,
never refreshing, which is fine for that package's actual usage pattern
(one `CredentialFunc` per top-level call) but would be wrong for a
long-lived client that calls the SAME secured route thousands of times over
hours).

This doc sketches a generic, opt-in wrapper that any `CredentialFunc` can be
wrapped with, independent of the specific auth PROTOCOL (Bearer challenge,
OAuth2 client-credentials flow, static API key rotation, etc.) — the wrapper
only handles CACHING/REFRESH POLICY, not how the credential itself is
obtained.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `nethttp.NewCachingCredentialFunc(inner CredentialFunc, opts CachingCredentialFuncOptions) CredentialFunc` — wraps any `CredentialFunc`, adding TTL-based caching | Any specific auth PROTOCOL implementation (OAuth2 token refresh endpoints, JWT expiry parsing, etc.) — the wrapper is protocol-agnostic; `inner` does the actual protocol work, exactly like `docker/registry`'s `authenticate()` does today |
| `RetryOn401 bool` option — on receiving `nethttp.UnexpectedStatusError{StatusCode: 401}` from the WRAPPED call, invalidate the cache and invoke `inner` exactly ONCE more before giving up | Automatic retry on any OTHER status code (403, 5xx, etc.) — 401 specifically means "your credential was rejected," which is the one case this wrapper can meaningfully react to; other statuses are the caller's own business logic to retry (or not) |
| Concurrency safety — concurrent callers during a cache miss/refresh must NOT all invoke `inner` simultaneously (a "thundering herd" on the auth server); exactly one refresh in flight, others wait for its result | Configurable jitter/backoff on refresh failure — Phase 1 ships a single-attempt refresh; a caller wanting backoff wraps `inner` itself with their own retry logic before passing it to `NewCachingCredentialFunc` |
| TTL is CALLER-SUPPLIED (`CachingCredentialFuncOptions.TTL time.Duration`) — this wrapper does NOT parse a JWT's `exp` claim or otherwise infer TTL from the credential itself (would require a JWT dependency, violating go-codex's "no crypto/JWT library" rule for `adapters/nethttp`) | Auto-detecting TTL from a JWT's `exp` claim, from a `Cache-Control` response header, or any other credential-format-specific signal |

## Toolchain / dependency decisions

**Stdlib only** — `sync.Mutex`/`sync.Once`-style primitives (likely a
`sync.RWMutex`-guarded cache entry plus a `singleflight`-style dedup for the
"exactly one refresh in flight" requirement; `golang.org/x/sync/singleflight`
is the natural fit, but it is an `x/` package, not stdlib — decide during
implementation whether it's worth the dependency or whether a
hand-rolled `sync.Once`-per-generation approach is simpler and sufficient;
lean toward hand-rolled to keep `adapters/nethttp` dependency-free, matching
its existing zero-external-dependency posture).

## API surface

```go
// adapters/nethttp/credential_cache.go (new file)

// CachingCredentialFuncOptions configures NewCachingCredentialFunc.
type CachingCredentialFuncOptions struct {
    // TTL is how long a successfully-obtained credential is reused before
    // inner is invoked again. Required — a zero TTL means "never cache"
    // (every call invokes inner), which is a valid but unusual choice.
    TTL time.Duration

    // RetryOn401, when true, invalidates the cached credential and invokes
    // inner exactly ONCE more when the wrapped Call returns
    // nethttp.UnexpectedStatusError{StatusCode: 401} — see "Scope decisions"
    // for why only 401 triggers this. Requires the caller to route the
    // Call's error back into this wrapper (see "Open design decisions" —
    // this is the trickiest part of the API to get right ergonomically,
    // since NewCachingCredentialFunc only wraps CredentialFunc, which runs
    // BEFORE the network call, not after).
    RetryOn401 bool

    // Observer, when non-nil, receives cache hit/miss/refresh events — see
    // "Observer integration" below. Defaults to stats.NoopObserver.
    Observer stats.Observer
}

// NewCachingCredentialFunc wraps inner with TTL-based caching: inner is
// invoked at most once per TTL window, with concurrent callers during a
// cache miss sharing the SAME in-flight call (no thundering herd on the
// auth server). Returns a CredentialFunc suitable for
// nethttp.CallOptions.CredentialFunc.
func NewCachingCredentialFunc(inner CredentialFunc, opts CachingCredentialFuncOptions) CredentialFunc
```

`CredentialFunc` here refers to the existing inline function type already
used by `nethttp.CallOptions.CredentialFunc` — `docker/registry`'s own
package-level `CredentialFunc` type alias (added in Round 92) is a useful
precedent for naming this shared shape if `adapters/nethttp` doesn't already
have one exported (check during implementation; add one if missing, since
this new file would otherwise need to repeat the inline function type
everywhere).

## Structured errors (all implement `slog.LogValuer`)

No new error types needed for the happy path — `NewCachingCredentialFunc`
returns whatever `inner` returns, unchanged, on a cache miss. Consider
whether a `CredentialCacheError{Op string, Err error}` wrapper is needed to
distinguish "the CACHE mechanism itself failed" (e.g., a `singleflight`
panic-recovery edge case) from "the underlying `inner` auth flow failed" —
likely NOT needed in Phase 1, since the cache is a thin pass-through with
no failure modes of its own beyond what `inner` already produces.

## Observer integration

New optional `stats.Observer` extension needed — a genuine new kind of
lifecycle event not covered by `RecordRequest`/`RecordSecurityRejection`:

```go
// stats/observer.go

// CredentialCacheObserver is an optional extension to Observer for
// credential-cache lifecycle events. Adapters type-assert the configured
// Observer to CredentialCacheObserver before calling these methods.
type CredentialCacheObserver interface {
    // RecordCredentialCacheHit is called when a cached credential is reused
    // without invoking inner.
    RecordCredentialCacheHit(location string)
    // RecordCredentialCacheRefresh is called when inner is invoked (cache
    // miss, TTL expiry, or a 401-triggered retry) — success indicates
    // whether inner returned without error.
    RecordCredentialCacheRefresh(location string, success bool)
}
```

`NoopObserver`, `LoggingObserver`, and `fanout` all need to implement it,
following the exact pattern `SQLObserver` established (see
`stats/observer.go`'s existing extensions table).

## Unit test plan

| Test | Verifies |
|---|---|
| `TestNewCachingCredentialFunc_CachesWithinTTL` | `inner` invoked once across N calls within the TTL window |
| `TestNewCachingCredentialFunc_RefreshesAfterTTL` | `inner` invoked again after TTL elapses |
| `TestNewCachingCredentialFunc_ConcurrentCallsDuringMiss_SingleInnerInvocation` | N concurrent callers during a cache miss result in exactly ONE `inner` call, all callers get the same result |
| `TestNewCachingCredentialFunc_InnerError_NotCached` | An `inner` error is NOT cached — the next call retries `inner` immediately, not after the full TTL |
| `TestNewCachingCredentialFunc_RetryOn401_InvalidatesAndRetriesOnce` | The 401-triggered retry path invokes `inner` exactly once more, not in an unbounded loop |
| `TestNewCachingCredentialFunc_Observer_RecordsHitAndRefresh` | `CredentialCacheObserver` methods fire with correct `location`/`success` values |
| `TestNewCachingCredentialFunc_NilObserver_NoPanic` | Nil `Observer` behaves like `NoopObserver` |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/nethttp/credential_cache.go` | `NewCachingCredentialFunc`, `CachingCredentialFuncOptions`, internal cache-entry/generation tracking |
| `adapters/nethttp/credential_cache_test.go` | Full unit test matrix above |
| `stats/observer.go` | `CredentialCacheObserver` interface; `NoopObserver`/`LoggingObserver`/`fanout` implementations |
| `stats/observer_test.go` | Compile-time assertion that the three implementations satisfy the new interface |

## Out of scope (deferred indefinitely, pending a concrete need)

- Auto-detecting TTL from the credential itself (JWT `exp`, etc.) —
  protocol-specific, would need a JWT dependency `adapters/nethttp`
  currently avoids entirely.
- Backoff/jitter on refresh failure — caller's own responsibility, achieved
  by wrapping `inner` before passing it to `NewCachingCredentialFunc`.
- A server-side equivalent (caching `SecurityFunc` results) — this doc is
  scoped to the CLIENT side only; server-side credential verification
  results are typically NOT safe to cache the same way (the server must
  re-verify per request in most security models), and no concrete need has
  been identified for it.
- Migrating `docker/registry`'s `NewAuthCredentialFunc` to use this wrapper
  — its current `sync.Once` memoization is CORRECT for its actual usage
  pattern (one `CredentialFunc` per top-level `GetTags`/`GetImageMetadata`
  call, never reused across hours of long-lived operation); adopting this
  wrapper there would be a no-op improvement at best, not a bug fix.

## Open design decisions (to resolve before/during implementation)

- **How does `RetryOn401` actually observe the 401?** `CredentialFunc` runs
  BEFORE the network call — it has no visibility into the RESPONSE. Two
  options: (a) `NewCachingCredentialFunc`'s returned `CredentialFunc` could
  wrap the ENTIRE `nethttp.Call` invocation instead of just the credential
  step (a bigger API — `func(ctx, callFn func() (Resp, error)) (Resp, error)`-shaped,
  not a drop-in `CredentialFunc` replacement anymore); or (b) `adapters/nethttp.Call`
  itself could grow a NEW opt-in hook (`CallOptions.OnCredentialRejected
  func()`) that the caching wrapper subscribes to, invalidating its cache
  when invoked — smaller API surface change, but requires a new `Call`-level
  hook, not just a wrapper function. Option (b) seems more consistent with
  go-codex's existing pattern of small, focused hooks — but this needs to
  be resolved concretely before implementation, since it changes whether
  this feature touches `adapters/nethttp.Call`'s signature at all or stays
  fully self-contained in the new wrapper file.
- **Is a hand-rolled single-flight sufficient, or is `x/sync/singleflight`
  worth the dependency?** Lean toward hand-rolled (keeps `adapters/nethttp`
  dependency-free) but revisit if the hand-rolled version proves fragile
  under test.
- **Does this belong in `adapters/nethttp` at all, or should it be a new,
  transport-agnostic package** (since the SAME caching/refresh need could
  apply to a future `adapters/events`-style publish-side `CredentialFunc`,
  per [events-reqreply-mcp-security-scheme.md](events-reqreply-mcp-security-scheme.md)'s
  Phase 2)? Lean toward keeping it `adapters/nethttp`-scoped for Phase 1
  (concrete, proven need there) and revisiting a shared/generic package
  only if a second transport actually grows a `CredentialFunc`-equivalent
  worth caching.
