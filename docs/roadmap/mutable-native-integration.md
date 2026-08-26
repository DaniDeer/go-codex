# `Mutable[T]` Phase 2 — Native Security Wiring, Change Notifications & `CachingCredentialFunc` Integration — `codex`, `api/*`, `adapters/nethttp`

> **Status:** Design draft — captured ideas, not yet fully speced.
> `codex.Cacheable[T]` (see `docs/concepts/codec.md`'s `Cacheable[T]`
> subsection) has now SHIPPED, so this doc is unblocked — no strict
> technical dependency on `Cacheable[T]` ever existed, but these ideas
> remain driver-light (see each subsection below); ready to pick up
> whenever a concrete driver appears for any of the three.
> [← Back to Roadmap](index.md)
>
> This doc preserves the "Out of scope (Phase 2)" ideas from the now-
> retired `reloadable-value-containers.md`, which shipped
> `codex.Mutable[T]`/`NewConst[T]`/`codex.ReloadObserver` and had
> nothing else left to track (fully shipped — see
> `docs/concepts/codec.md`'s Getter/Setter subsection for the shipped
> design). One item from that doc's Phase 2 list is NOT carried forward
> here: a `TryGet`-style safe accessor for `Mutable[T]` was already
> resolved as unnecessary (construction guarantees a valid value always
> exists — no "unset" state to guard against, unlike `Immutable[T]`) —
> a settled non-decision, not a deferred idea. `OptionalMutable[T]`
> (`optional-mutable.md`) and `Cacheable[T]` (now shipped — see
> `docs/concepts/codec.md`'s `Cacheable[T]` subsection) already have
> their OWN dedicated roadmap docs/homes and are NOT part of this one.

## Idea 1 — Native `Builder` security wiring

Wire `Mutable[T]` NATIVELY into `Builder.AddGlobalSecurity`/
`WithSecurityScheme`'s `SecuritySchemes` map across `api/rest`/
`api/events`/`api/reqreply`/`api/mcp` — making a `Builder`'s OWN global
security or per-scheme credentials LIVE-RELOADABLE without the caller
having to hand-roll their own `SecurityFunc`/`CredentialFunc` closure
around a `Mutable[T]` themselves (which already works today, just not
as a first-class `Builder` feature).

**Why this isn't scoped yet:**

- **No concrete driver requesting this specific integration.** Today's
  driver for `Mutable[T]` (JWKS/API-key rotation) is fully served by a
  caller wiring `Mutable[T]` into their OWN `SecurityFunc`/
  `CredentialFunc` closure directly — the ergonomic gap this idea would
  close (skip writing that closure) hasn't been requested by anything
  in `examples/go-edge-models` or elsewhere.
- **Genuinely bigger, more invasive change than it first looks.** All
  four `Builder` types currently take a SNAPSHOT of their configuration
  (security schemes included) at `Register()` time — `RouteHandle`/
  `ChannelHandle`/`ToolHandle`/etc. are built as immutable VALUES from
  that snapshot. Making a scheme's credential live-reloadable without
  a re-`Register()` call means either (a) storing a `Getter[T]`-shaped
  reference INSTEAD OF a resolved value in the snapshot (a real shape
  change to every handle type that carries security), or (b) some other
  mechanism to invalidate/refresh an already-built handle — neither is
  a small, additive change.
- **Scope question**: would this apply to `AddGlobalSecurity` only, or
  also per-route/per-channel/per-tool `WithSecurityScheme` overrides?
  The two have different snapshot lifetimes today.

**What a resolved design would need to answer:** the snapshot-vs-live
shape question above, whether this becomes a NEW `Builder` method
(e.g. `AddGlobalSecurityMutable(scheme, *codex.Mutable[Credential])`)
or a change to the existing signature, and whether it applies uniformly
across all four API layers or starts with just one (`api/rest` is the
usual reference implementation for a first cut — see
`docs/concepts/api-contracts.md`).

## Idea 2 — A "subscribe to changes" notification mechanism

`codex.ReloadObserver` (shipped) covers OBSERVABILITY of a `Mutable[T]`
reload — a caller can log/measure that a reload happened. It does NOT
let OTHER code REACT to one: there's no way for a second, independent
part of a process to say "wake me up when this Mutable's value
changes" without polling `Get()` itself or being the SAME code that
called `Set()`.

**Why this isn't scoped yet:**

- **No driver.** Every known `Mutable[T]` use case (JWKS rotation, a
  `SecurityFunc`/`CredentialFunc` closure reading the current key) is
  satisfied by `Get()` alone — the caller reading the value doesn't
  need to be NOTIFIED of a change, just to always see the CURRENT one.
  A push-notification consumer hasn't materialized yet.
- **Design space is genuinely open**: a channel-based subscription
  (`Subscribe() <-chan T`, with the usual multi-subscriber/backpressure/
  unsubscribe-lifecycle questions that come with any Go pub/sub-shaped
  API), a callback registered at construction/`Set` time (simpler, but
  callback ordering/panics-in-callback semantics need defining), or
  deferring to `stream` package primitives (`stream.Stream[T]` already
  exists — could `Mutable[T]` optionally expose one instead of
  inventing a bespoke mechanism?) are all plausible starting points
  with real trade-offs, none evaluated yet.

**What a resolved design would need to answer:** which of the three
shapes above (or another), how many subscribers a single `Mutable[T]`
needs to support concurrently, and whether a missed/slow subscriber can
ever block a `Set` call (almost certainly must not, given `Set`'s
last-good-value-wins latency-sensitive contract).

## Idea 3 — Fold `NewCachingCredentialFunc` onto `Mutable[T]`

`adapters/nethttp.NewCachingCredentialFunc` already hand-rolls a
TTL-based, single-flight caching wrapper around any `CredentialFunc` —
conceptually adjacent to `Mutable[T]` (both are "the current value of
something that can change"), but implemented independently before
`Mutable[T]` existed. This idea explores refactoring
`NewCachingCredentialFunc` to be `Mutable[T]`-BACKED internally
(store its cached credential in a `*codex.Mutable[Credential]`, drive
its `Set` calls from the existing TTL/single-flight refetch logic)
instead of its own bespoke mutex-guarded cell.

**Why this isn't scoped yet:**

- **Zero external behavior change if done** — `NewCachingCredentialFunc`'s
  existing signature, TTL/single-flight semantics, and test suite would
  all stay unchanged; this is purely an INTERNAL implementation swap,
  not a new feature. Lower priority than ideas with an external-facing
  payoff.
- **Not risk-free either** — `Mutable[T]`'s `Set` always re-validates
  against a `Codec[T]`; `NewCachingCredentialFunc`'s cached value today
  has no codec-validation concept at all (it caches whatever the wrapped
  `CredentialFunc` returns, which is `http.Header`, not typically
  something with a natural `Codec[http.Header]`). Folding the two would
  need either a pass-through/no-op codec for this case or a documented
  reason `Mutable[T]`'s codec-validation requirement doesn't apply here
  — an actual design decision, not just a mechanical refactor.

**What a resolved design would need to answer:** the codec-validation
question above, and whether the refactor is worth doing at all given
"zero external behavior change" — i.e. does it reduce future maintenance
burden enough to justify touching working, tested code purely for
internal consistency.

## Sequencing

All three ideas here remain driver-light (see each subsection) — none
was ever blocked on `Cacheable[T]` technically, and `Cacheable[T]`
(see `docs/concepts/codec.md`'s `Cacheable[T]` subsection) has now
shipped, so this doc is fully unblocked. Revisit priority order
whenever a concrete driver appears for any of the three.
