# Async/Refreshable Credential Caching — `adapters/nethttp`

> **Status:** Implemented (Round 104 — see `.github/skills/review-go-codex/references/history.md`).
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
| `nethttp.NewCachingCredentialFunc(inner CredentialFunc, opts CachingCredentialFuncOptions) (fn CredentialFunc, invalidate func())` — wraps any `CredentialFunc`, adding TTL-based caching | Any specific auth PROTOCOL implementation (OAuth2 token refresh endpoints, JWT expiry parsing, etc.) — the wrapper is protocol-agnostic; `inner` does the actual protocol work, exactly like `docker/registry`'s `authenticate()` does today |
| `CallOptions.OnCredentialRejected func()` — a new, purely notificational hook on `Call`/`CallHandle`, invoked when the server responds 401 AND `CredentialFunc` was actually used for this call; wire `NewCachingCredentialFunc`'s returned `invalidate` here so the NEXT call fetches a fresh credential | Automatic retry of the CURRENT call inside `Call` itself — `Call` stays single-attempt with no hidden control flow (matches the "small, focused hooks" principle used everywhere else in go-codex); the retry-the-call-once pattern is a simple, explicit, documented 3-line snippet the CALLER writes (call, check for 401, call again) — see "API surface" below |
| Concurrency safety — concurrent callers during a cache miss/refresh must NOT all invoke `inner` simultaneously (a "thundering herd" on the auth server); exactly one refresh in flight, others wait for its result — implemented via a HAND-ROLLED generation-counter + `sync.Once`-per-generation (NOT `x/sync/singleflight` — keeps `adapters/nethttp` dependency-free) | Configurable jitter/backoff on refresh failure — Phase 1 ships a single-attempt refresh; a caller wanting backoff wraps `inner` itself with their own retry logic before passing it to `NewCachingCredentialFunc` |
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
// adapters/nethttp/client.go — additions to the EXISTING file:

// CredentialFunc names the function type already used by
// CallOptions.CredentialFunc — a TYPE ALIAS (not a new defined type), so
// CallOptions.CredentialFunc's field type is completely unchanged. Lets
// credential_cache.go (and any caller) name the shape instead of repeating
// the inline function type everywhere — docker/registry's own
// package-level credentialFunc type alias (added in Round 92) is the
// precedent this mirrors.
type CredentialFunc = func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)

// CallOptions gains:
//   OnCredentialRejected func()
// Called by Call/CallHandle when the server responds 401 AND
// opts.CredentialFunc was non-nil for this call (mirrors the existing
// "only if the credential mechanism actually engaged" gating used for the
// symmetric client-side format check). Purely a notification hook — Call
// does NOT retry automatically. Wire NewCachingCredentialFunc's second
// return value here to invalidate the cache so the NEXT call gets a fresh
// credential.
```

```go
// adapters/nethttp/credential_cache.go (new file)

// CachingCredentialFuncOptions configures NewCachingCredentialFunc.
type CachingCredentialFuncOptions struct {
    // TTL is how long a successfully-obtained credential is reused before
    // inner is invoked again. Required — a zero TTL means "never cache"
    // (every call invokes inner), which is a valid but unusual choice.
    TTL time.Duration

    // Observer, when non-nil, receives cache hit/refresh events — see
    // "Observer integration" below. Defaults to stats.NoopObserver.
    Observer stats.Observer
}

// NewCachingCredentialFunc wraps inner with TTL-based caching: inner is
// invoked at most once per TTL window, with concurrent callers during a
// cache miss sharing the SAME in-flight call (hand-rolled generation-based
// single-flight — a sync.Once scoped to each cache generation — no
// thundering herd on the auth server, no external dependency).
//
// Returns (fn, invalidate): fn is a CredentialFunc suitable for
// CallOptions.CredentialFunc; invalidate immediately expires the cached
// credential — wire it to CallOptions.OnCredentialRejected so a 401
// causes the NEXT call to fetch a fresh credential:
//
//	credFn, invalidate := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{TTL: time.Hour})
//	callOpts := nethttp.CallOptions{CredentialFunc: credFn, OnCredentialRejected: invalidate}
//	resp, err := nethttp.CallHandle(ctx, client, url, handle, req, callOpts)
//	var statusErr nethttp.UnexpectedStatusError
//	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
//	    resp, err = nethttp.CallHandle(ctx, client, url, handle, req, callOpts) // fresh credential now
//	}
//
// One NewCachingCredentialFunc instance = one cache entry. Construct a
// separate instance per credential scope (e.g. per host/registry) if a
// caller's routes need independently-cached credentials.
func NewCachingCredentialFunc(inner CredentialFunc, opts CachingCredentialFuncOptions) (fn CredentialFunc, invalidate func())
```

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
    // without invoking inner. duration is the (near-zero) lookup cost —
    // included for consistency with CacheObserver.RecordCacheHit, which
    // always includes duration even on a hit.
    RecordCredentialCacheHit(location string, duration time.Duration)
    // RecordCredentialCacheRefresh is called when inner is invoked (cache
    // miss, TTL expiry, or a refresh after invalidate) — success indicates
    // whether inner returned without error. duration is inner's own call
    // duration.
    RecordCredentialCacheRefresh(location string, success bool, duration time.Duration)
}
```

`NoopObserver`, `LoggingObserver`, and `fanout` all need to implement it,
following the exact pattern `SQLObserver`/`CacheObserver` established (see
`stats/observer.go`'s existing extensions table) — including the two
doc-comment lists enumerating which extensions `LoggingObserver` and
`NewFanout` implement, which both need `[CredentialCacheObserver]` added.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestNewCachingCredentialFunc_CachesWithinTTL` | `inner` invoked once across N calls within the TTL window |
| `TestNewCachingCredentialFunc_RefreshesAfterTTL` | `inner` invoked again after TTL elapses |
| `TestNewCachingCredentialFunc_ConcurrentCallsDuringMiss_SingleInnerInvocation` | N concurrent callers during a cache miss result in exactly ONE `inner` call, all callers get the same result (hand-rolled single-flight correctness) |
| `TestNewCachingCredentialFunc_InnerError_NotCached` | An `inner` error is NOT cached — the next call retries `inner` immediately, not after the full TTL |
| `TestNewCachingCredentialFunc_Invalidate_ForcesRefreshOnNextCall` | Calling the returned `invalidate func()` causes the NEXT `fn` call to invoke `inner` again, even within the TTL window |
| `TestNewCachingCredentialFunc_Observer_RecordsHitAndRefresh` | `CredentialCacheObserver` methods fire with correct `location`/`success`/`duration` values |
| `TestNewCachingCredentialFunc_NilObserver_NoPanic` | Nil `Observer` behaves like `NoopObserver` |
| `TestCall_OnCredentialRejected_FiresOn401` | `Call`/`CallHandle` invokes `opts.OnCredentialRejected` exactly once when the server responds 401 AND `CredentialFunc` was non-nil for this call |
| `TestCall_OnCredentialRejected_NotCalledWhenCredentialFuncNil` | No `CredentialFunc` configured → `OnCredentialRejected` never fires even on a 401 |
| `TestCall_OnCredentialRejected_NotCalledOnNon401Status` | A non-401 non-2xx status (e.g. 403, 500) does NOT fire `OnCredentialRejected` |

## Files to create

| File | Responsibility |
|---|---|
| `adapters/nethttp/client.go` | Add `CredentialFunc` type alias; add `CallOptions.OnCredentialRejected func()` field; wire the 401-detection call site |
| `adapters/nethttp/client_test.go` | New tests: the 3 `TestCall_OnCredentialRejected_*` tests above |
| `adapters/nethttp/credential_cache.go` | `NewCachingCredentialFunc`, `CachingCredentialFuncOptions`, internal cache-entry/generation tracking |
| `adapters/nethttp/credential_cache_test.go` | The 7 wrapper unit tests above |
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

## Resolved design decisions

- **How does `OnCredentialRejected` actually observe the 401?**
  `CredentialFunc` runs BEFORE the network call — it has no visibility
  into the response. Resolved as option (b) from the original two
  candidates: `adapters/nethttp.Call`/`CallHandle` grow a new opt-in hook,
  `CallOptions.OnCredentialRejected func()`, invoked when the response is
  401 AND `CredentialFunc` was non-nil for this call. This hook is PURELY
  notificational — it only invalidates the cache (via
  `NewCachingCredentialFunc`'s returned `invalidate` function); `Call`
  itself does NOT retry automatically, keeping it single-attempt with no
  hidden control flow (consistent with go-codex's existing "small, focused
  hooks" principle). The retry-the-call-once behavior originally sketched
  as `CachingCredentialFuncOptions.RetryOn401 bool` is DROPPED as a config
  option — it's now a simple, explicit, documented 3-line caller pattern
  (see "API surface" above) rather than a hidden mechanism, since
  constructing a caching wrapper always implies "yes, invalidate on
  rejection" (the bool would have been redundant).
- **Is a hand-rolled single-flight sufficient, or is `x/sync/singleflight`
  worth the dependency?** Resolved: hand-rolled (a generation counter +
  `sync.Once` scoped to each generation, so concurrent callers during a
  miss block on the SAME `Once.Do`) — keeps `adapters/nethttp`
  dependency-free, matching its existing zero-external-dependency posture.
- **Does this belong in `adapters/nethttp` at all, or should it be a new,
  transport-agnostic package?** Resolved: stays `adapters/nethttp`-scoped
  for Phase 1 (this remains the only package with a concrete, proven need)
  — revisit a shared/generic package only if `adapters/mqtt5`'s
  `CredentialFunc`s actually grow a caching need worth extracting.
