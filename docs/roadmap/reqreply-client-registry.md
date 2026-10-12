# `api/reqreply.Client` Registry — Design Complete, Not Yet Implemented

> **Status:** Design complete — API surface, registry shape, and
> collision-detection behavior are all settled. **This is the FIRST of 3
> sequenced reqreply roadmap docs to implement** — see "Relationship to
> the other 2 reqreply roadmap docs" below. Unlike the other 2, this
> phase has NO blocking dependency; it can be implemented, tested, and
> shipped standalone now. An in-depth review pass closed 2 further
> findings beyond the original design: `routesOrder` dropped as confirmed
> dead weight (reqreply's lookups are always exact-string, never
> ambiguous), and a registry collision-detection mechanism added
> (`DuplicateTopicTypeMismatchError` + a lazily-surfaced "poisoned entry"
> marker) — a genuine divergence from `rest.Client`'s own
> first-registered-wins policy, since reqreply has no structural
> guarantee re-registrations of the same topic are "the same route."
> Implementation is a distinct, future step requiring its own explicit
> go-ahead (unit test plan). [← Back to Roadmap](index.md)

## Relationship to the other 2 reqreply roadmap docs

This doc was split out of a single combined doc (originally covering all
3 phases) once all 3 reached "design complete," so each phase can track
its own implementation status independently. The 3 docs are sequenced,
not independent:

1. **This doc** (Phase 0) — registry infrastructure. Ships FIRST,
   standalone — no blocking dependency.
2. **[`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)**
   (Phase 1) — forward/bounce. The highest-risk, highest-complexity
   phase; this doc's registry is its FIRST real consumer. Blocked on
   this doc shipping first.
3. **[`reqreply-dead-letter-replay.md`](reqreply-dead-letter-replay.md)**
   (Phase 2) — dead-letter replay. This doc's registry is its SECOND
   consumer. Needs zero new wire mechanism of its own, reusing Phase 1's
   low-level primitive directly. Blocked on Phase 1 shipping first.

## Why this exists

While designing Typed HTTP Redirects' client-side route registry for
`rest.Client` (now shipped — see [the feature page](../features/rest-redirects.md)),
a question was raised: should `api/reqreply.Client` get an equivalent
registry too, for symmetry? This doc originally captured that question
deliberately UNRESOLVED, the same way other "idea only" docs in this
roadmap (`sse-resume-and-retry-policy.md`, `vector-store-adapter.md`,
`mutable-native-integration.md`) record a genuinely open question without
forcing a premature decision. **That question is now resolved** — see
"Candidate motivations" below for the confirmed answer and "Phase 0"
below for the full settled design that followed from it.

## The asymmetry, confirmed

`api/reqreply.Client`'s own doc comment states outright:

> *"Client accumulates NO spec state — it is purely a dispatch handle, one
> [ClientTransport] attached via [Client.Attach], mirroring
> [rest.Client]/[events.Client]'s identical shape."*

That comment was accurate when written — but `rest.Client` is no longer
"purely a dispatch handle" now that its own registry (see
[the feature page](../features/rest-redirects.md)) has shipped.
`events.Client` already has one too (`specByTopic`/`subscriberByTopic`,
pre-dating this whole discussion). `reqreply.Client` is now the ONLY one of
the three `api/*` `Client` types with no registry at all.

## Investigated: does reqreply have a structural analog to an HTTP redirect?

**Not an EXISTING one — confirmed directly, not assumed — but
[Phase 1](reqreply-forward-bounce.md) designs a NEW one, purpose-built
for reqreply.** `rest.Client`'s registry exists to solve a SPECIFIC
problem: an HTTP 3xx response doesn't carry its own typed `Resp` — the
client has to look up which registered route a received `Location`
corresponds to, in order to know what type to decode. reqreply had no
equivalent gap BEFORE this design: `Call`/`CallAsync` always know their
own `Resp` type at the call site (the generic type parameter the caller
is working with), and `CallAsync`'s returned `*Future[Resp]` is typed at
construction too, before any response has even arrived.

**This means a reqreply registry does NOT solve the same problem
`rest.Client`'s registry solves — it solves a DIFFERENT, NEW problem
[Phase 1's](reqreply-forward-bounce.md) `Forward` mechanism itself
introduces** (a forward reply's target route is NOT known at the
original call site, unlike an ordinary `Call`/`CallAsync` target) — not
a retrofit of REST's own motivation, a genuinely new one this design
creates and then immediately needs to solve for itself.

## Candidate motivations (3 identified) — RESOLVED

1. **A new, reqreply-native "forward/bounce" concept — CONFIRMED as the
   real driver.** A handler can respond "actually, route X should handle
   this" (with a brand NEW request value it constructs itself — see
   [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)), and the
   caller auto-follows to THAT route's typed response. Full design
   settled there.
2. **Pure structural symmetry — REFRAMED, not actually a separate
   motivation.** Originally considered the weakest-justified of the 3 (a
   registry with zero consumers). Once motivation 1 was confirmed as the
   real driver, this stopped being "symmetry for its own sake" and became
   motivation 1's OWN REQUIRED INFRASTRUCTURE: a forward reply has to
   tell the CLIENT "ask topic X instead" (pub/sub has no broker-level
   redirect the way HTTP does), and the client then needs to resolve
   topic X to a registered `RouteHandle` to know what `Resp` type to
   decode — exactly the registry's job. See "Phase 0" below.
3. **Tied to dead-letter/error handling — CONFIRMED as a SECOND,
   sequenced-after driver (not the primary one).** Re-examined in detail:
   the shipped `DeadLetter` mechanism
   (`docs/design/d-0005-error-handling.md`'s Topic 4) is
   SERVER/ADAPTER-SIDE ONLY (publishes a `DeadLetterEnvelope` to a
   destination topic on an unmatched failure) — it does NOT need a
   client-side registry by itself. The REAL, concrete motivation-3 use
   case is narrower: an ops/REPLAY tool consuming a `DeadLetterEnvelope`
   needs to resolve `SourceTopic` back to the ORIGINAL route (for its
   `Req` codec) to decode `Payload` and reissue the `Call` — this DOES
   need this registry, as a consumer. See
   [`reqreply-dead-letter-replay.md`](reqreply-dead-letter-replay.md).

These are no longer competing, mutually-exclusive choices — they are
SEQUENCED phases of one connected design: this doc (registry) is Phase
1's prerequisite; Phase 2 reuses this registry for a second, narrower
purpose once Phase 1 has settled the registry's final accessor shape.

## Why this was sequenced AFTER Typed HTTP Redirects, not in parallel

`rest.Client`'s registry (`api/rest/client_registry.go`) is now a SHIPPED,
tested mechanism, resolving its own open design questions along the way
(ambiguous-match policy — first-registered-wins, tracked via an
insertion-order-preserving slice since a plain Go map has no defined
iteration order; same-routeKey re-registration idempotency; a
credential-identity rule for auto-follow that reuses the ORIGINATING
call's own credentials, never re-derived from the registered target). Any
reqreply design — whichever motivation turns out to be the real one —
should be informed by this SHIPPED, working shape (what worked, what had to
change during implementation), not designed blind in parallel against a
still-moving target.

## Phase 0 — Registry infrastructure (design settled)

Reqreply has no REST-style `{Method, Path}` pair — a route's identity is
just its `Topic` string (already how `api/reqreply/route.go`'s own
server-side `DuplicateRouteError{Topic}` keys registration). Reqreply
also has no SSE/streaming route concept — only ONE registry map is
needed, not `rest.Client`'s `routes`+`sseRoutes` pair.

- `Client` gains `routes map[string]*registeredRoute` (keyed by `Topic`)
  — a PLAIN map, no insertion-order slice (REVIEWED and DROPPED — see
  "No `routesOrder`" below).
- `Client.RegisterRoute(route any) error` — dual-mode
  (`Route[Req,Resp]`/`*RouteHandle[Req,Resp]`, reflection-discriminated
  the SAME way `rest.Client.RegisterRoute` does, via pkgPath +
  type-name-prefix checks), reusing the EXISTING
  `TransportTypeMismatchError` (`api/reqreply/client.go`) for a plain
  shape mismatch — no new error type needed for THAT part (see below for
  the SEPARATE, genuine-content-mismatch case).
- `Call`/`CallAsync` each gain a `c.registerAutoFromCall(route)` call —
  auto-populate on first use, mirrors `rest.Client.Call`/`Consume`
  exactly.
- **No `routesOrder` — REVIEWED and DROPPED, a genuine simplification
  over REST's own shape.** `rest.Client`'s `routesOrder` exists SOLELY to
  deterministically resolve AMBIGUOUS `{var}`-template matches (more than
  one registered template can match the SAME concrete path; a plain Go
  map has no defined iteration order). reqreply's `TargetTopic` always
  arrives ALREADY CONCRETE (resolved server-side via `BuildTopic`) and is
  used as a literal map key — there is no template-matching step and
  therefore no ambiguity `routesOrder` could ever help resolve. A plain
  `map[string]*registeredRoute` is sufficient.
- **Registry collision detection — a GENUINE DIVERGENCE from REST's own
  policy, not a straight port.** REST's "first-registered-wins" is safe
  ONLY because every re-registration of the SAME key is GUARANTEED (by
  construction) to come from the SAME underlying `Route` value, just
  wrapped with a different credential-bound `ClientHandle` — same `Resp`
  type, always. reqreply has NO such guarantee: two GENUINELY DIFFERENT
  `Route[ReqA,RespA]`/`Route[ReqB,RespB]` values could collide on the
  SAME `Topic` string (typo, coincidence, two unrelated features
  choosing the same name) — silently keeping whichever registered first
  would let a LATER `Forward`/`Replay` resolving that topic decode using
  the WRONG `Resp` type, a confusing runtime failure invisible at compile
  time (everything here is `any`-erased). Resolved as follows:
  - `Client.RegisterRoute` (explicit, already returns `error`): on
    re-registration of an already-present `Topic`, compares the NEW
    entry's recovered `Req`/`Resp` reflect types against the EXISTING
    entry's stored types. A genuine mismatch returns a NEW
    `DuplicateTopicTypeMismatchError{Topic, ExistingType, NewType}`
    immediately, instead of silently keeping the first.
  - `registerAutoFromCall` (best-effort — `Call`/`CallAsync`'s OWN
    contract requires it "never errors," pure bookkeeping — cannot start
    returning an error through them without a much bigger, unrelated
    contract change): on a detected conflict, marks the registry entry as
    POISONED (recording both candidate types) WITHOUT surfacing anything
    through `Call`/`CallAsync` itself.
  - The poisoned marker surfaces LAZILY, exactly where it actually
    matters: [Phase 1's](reqreply-forward-bounce.md) forward-resolution
    and [Phase 2's](reqreply-dead-letter-replay.md) replay-resolution
    (the ONLY real consumers of a registry lookup) check for a poisoned
    entry right where they already return `UnrecognizedForwardError`/
    `UnrecognizedDeadLetterSourceError` for a MISSING entry — a poisoned
    entry returns the SAME `DuplicateTopicTypeMismatchError` at THAT point
    instead, right before a wrong-type decode would otherwise happen.
    `Call`/`CallAsync`'s own contract stays completely unchanged.
- **Credential-identity rule** (direct port, not reqreply-specific):
  whatever consumes this registry ([Phase 1's](reqreply-forward-bounce.md)
  forward-following dispatch) reuses the ORIGINATING call's own
  already-computed credentials, NEVER re-derived from the registered
  target. Getting this backwards would mean a forwarded call silently
  authenticates as the WRONG identity — a security regression, not just
  a correctness bug.
- **Security-scheme mismatch check — a GENUINE DIVERGENCE from REST's own
  more permissive behavior, not a straight port.** Fully designed as part
  of [Phase 1](reqreply-forward-bounce.md) (it is Forward's own
  client-side dispatch that performs the check, using this registry's
  resolved target `RouteHandle`) — summarized here because it depends on
  this registry existing: a mismatch between the originating route's and
  the resolved target's declared security-scheme NAME(S) fails fast,
  client-side, BEFORE the network round-trip, with a NEW
  `ForwardSecuritySchemeMismatchError`. See Phase 1's doc for the full
  rationale and design.
- **Chain-depth cap**: direct port of `MaxRedirects`'s mechanics
  (recursive-call counter, default 10) — reqreply's own option name
  TBD at implementation time (`ClientCallOptions.MaxForwards`, most
  likely). Lives with [Phase 1](reqreply-forward-bounce.md) (the only
  phase that chains calls), noted here only because the registry is what
  makes each hop's resolution possible.

## Known future consumers (why a registry with no consumer in THIS doc is still worth building now)

This phase, taken alone, adds a registry with no shipped caller yet — the
registry's only consumers are the other 2 roadmap docs:

- **[`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)** (Phase
  1) — resolves a forward reply's `TargetTopic` to recover the registered
  `*RouteHandle[TargetReq,TargetResp]` needed to decode the second round
  trip's reply, and to compare security schemes before republishing.
- **[`reqreply-dead-letter-replay.md`](reqreply-dead-letter-replay.md)**
  (Phase 2) — resolves a `DeadLetterEnvelope`'s `SourceTopic` back to the
  original route to decode/replay it.

This is unlike the originally-considered (and rejected) "pure symmetry,
zero consumers" framing of this registry — both consumers are confirmed,
designed, and sequenced; only their OWN implementation is what's blocked
on this phase shipping first, not the registry's justification for
existing.

**What this phase's implementation should keep flexible for those future
consumers**: the registry's PUBLIC surface (`RegisterRoute`'s dual-mode
reflection discrimination, its immediate
`DuplicateTopicTypeMismatchError`) is already fully specified above and
should NOT need to change once Phase 1 starts consuming it. The ONE
thing to deliberately keep unexported/flexible is the exact shape of the
poisoned-marker/lazy-surfacing mechanism — Phase 1 is the first real
CONSUMER of "resolve a Topic, detect a poisoned entry," so its precise
internal plumbing (how the poisoned flag is stored, how the "both
candidate types" are recorded) should stay free to adjust based on
Phase 1's real implementation experience, even though the error TYPE
callers see (`DuplicateTopicTypeMismatchError`) is already locked.

## Next step

The registry has a SETTLED design, further hardened by an in-depth
review pass (`routesOrder` dropped, collision detection added) — none of
it is implemented yet. **This is the first of the 3 reqreply roadmap docs
intended to be implemented**, with no blocking dependency of its own.
Turning this into an actual implementation plan (unit test plan,
including `RegisterRoute`'s dual-mode discrimination, the
`DuplicateTopicTypeMismatchError` collision case, and the poisoned-entry
marker's lazy-surfacing contract — exercised via a stub consumer, since
Phase 1's real consumer doesn't exist yet) is a distinct, future step
requiring its own explicit go-ahead — the same rigor Typed HTTP
Redirects itself went through across its own critical-review rounds
before implementation began. No code should be written against this
design until that implementation plan exists and is approved. Once this
phase ships and is verified, [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)
(Phase 1) is unblocked.
