# `Mutable[T]`/`Cacheable[T]` — Subscribe-to-Changes — `codex`

> **Status:** Idea only — no driver yet.
> [← Back to Roadmap](index.md)
>
> This doc originally captured 3 "Phase 2" ideas preserved from the
> now-shipped/retired `Mutable[T]` and `Cacheable[T]` roadmap docs. Two
> of the three are now RESOLVED, with no code change needed or wanted —
> see "Resolved ideas" below. Only Idea 2 remains open.

## Idea 2 — A "subscribe to changes" notification mechanism (open)

`codex.ReloadObserver`/`codex.InvalidateObserver` (both shipped) cover
OBSERVABILITY of a `Mutable[T]`/`Cacheable[T]` change — a caller can
log/measure that a reload or invalidation happened. Neither lets OTHER
code REACT to one: there's no way for a second, independent part of a
process to say "wake me up when this value changes" without polling
`Get()` itself or being the SAME code that called `Set()`/`Invalidate()`.

**Why this isn't scoped yet:**

- **No driver.** Every known use case (JWKS/credential rotation, TTL
  memoization — see `docs/concepts/codec.md`'s "Composing with adapters"
  subsection) is satisfied by `Get()` alone — the caller reading the
  value doesn't need to be NOTIFIED of a change, just to always see the
  CURRENT one. A push-notification consumer hasn't materialized yet.
- **Design space is genuinely open**: a channel-based subscription
  (`Subscribe() <-chan T`, with the usual multi-subscriber/backpressure/
  unsubscribe-lifecycle questions that come with any Go pub/sub-shaped
  API), a callback registered at construction/`Set` time (simpler, but
  callback ordering/panics-in-callback semantics need defining), or
  deferring to `stream` package primitives (`stream.Stream[T]` already
  exists — could `Mutable[T]`/`Cacheable[T]` optionally expose one
  instead of inventing a bespoke mechanism?) are all plausible starting
  points with real trade-offs, none evaluated yet.

**What a resolved design would need to answer:** which of the three
shapes above (or another), how many subscribers a single container
needs to support concurrently, and whether a missed/slow subscriber can
ever block a `Set`/`Invalidate` call (almost certainly must not, given
`Set`'s last-good-value-wins latency-sensitive contract).

## Resolved ideas (kept for historical rationale)

**Idea 1 — Native `Builder` security wiring: RESOLVED, no code needed.**
`SecurityFunc`/`CredentialFunc` (`adapters/nethttp`/`chi`/`mqtt`/`mqtt5`)
are already plain closures — a caller can already capture a
`*codex.Mutable[T]`/`*codex.Cacheable[T]` and call `.Get()` inside one,
with ZERO Builder/Handle/adapter API change. `SecurityScheme` correctly
stays spec-metadata-only (declared once, snapshotted immutably at
`Register()`); the runtime credential lives entirely in the caller's
closure, which is ALREADY evaluated per-call. Adding a dedicated
`SecurityFuncFromMutable`-style wrapper constructor was considered and
explicitly rejected — it would save at most 1-2 lines over the closure
itself while adding new, permanently-maintained API surface for
something that already works. See `docs/concepts/codec.md`'s "Composing
with adapters" subsection and `examples/mutable-security-keys` for the
proof (a real `nethttp.Options.SecurityFunc` reading a live
`Mutable[T]`, and a real `nethttp.CallOptions.CredentialFunc` reading a
live `Cacheable[T]`).

**Idea 3 — Fold `NewCachingCredentialFunc` onto a container type:
redirected, not scheduled.** `NewCachingCredentialFunc`'s TTL +
single-flight-fetch-on-miss shape does NOT match `Mutable[T]` (a pure
push/`Set`-driven cell with no fetch-on-demand concept) — it matches
`ports.RefreshingCacheable[T]`'s `GetOrRefresh(ctx) (T, error)` design
(single-flighted fetch-when-stale, composing `codex.Cacheable[T]`) far
better. `RefreshingCacheable[T]` is a separate, already-fully-speced
roadmap item (`refreshing-cacheable.md`) — revisit folding
`NewCachingCredentialFunc` onto it AFTER that ships, not before. Even
then this stays a low-priority, purely-internal refactor: zero external
behavior change, and `NewCachingCredentialFunc` caches `http.Header`,
which has no natural `Codec[http.Header]` — a passthrough codec (see the
`headerCodec` precedent in `examples/go-edge-models/internal/registry`)
would be needed to satisfy `Cacheable[T]`'s `Codec[T]` requirement — a
real, if small, wrinkle to resolve at that time, not now.
