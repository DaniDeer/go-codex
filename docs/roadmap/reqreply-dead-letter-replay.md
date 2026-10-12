# `api/reqreply` Dead-Letter Replay — Design Complete, Phase 1 Dependency SATISFIED, 1 Cross-Cutting Addendum Pending

> **Status:** Design complete — API surface and structured error are
> settled, and the dispatch-path inventory is confirmed trivial (no
> `ports.Pattern` binding equivalent to verify, unlike
> [Phase 1](reqreply-forward-bounce.md)). **The blocking dependency on
> [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md) (Phase 1)
> is SATISFIED — Phase 1 has SHIPPED.**
>
> **A review round, grounded against the REAL shipped Phase 0/1 code for
> the first time, found and resolved 2 real design gaps**: (1)
> `ReplayDeadLetter`'s hop 1 CANNOT naively enter the shared
> `followForward` loop as a prior revision of this doc claimed — its
> unconditional security-scheme check would reject replay against any
> secured route; RESOLVED by giving Replay its own loop-free hop-1
> dispatch, handing off to the unchanged shared loop only from hop 2
> onward (see "The key simplification" below). (2) `DeadLetterEnvelope`
> never captured the original request's wire-level properties, making a
> header-delivered credential unrecoverable at replay time — traced to a
> LATENT gap `CallRaw` itself (Phase 0) already has, which Phase 1's own
> `Forward` also inherits; RESOLVED as a confirmed, CROSS-CUTTING
> addendum (breaking `CallRaw` signature change + `ForwardError`/
> `DeadLetterEnvelope` extensions) — see "Addendum" below, mirrored in
> [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md).
>
> **A further re-review made the addendum's own ORDER explicit — a
> direct question surfaced that the previous round left it only as
> "sequenced together," never actually committing to which comes
> first.** Traced the real dependency to confirm the order is FORCED,
> not a preference: **Phase 1.5** (`CallRaw` signature + `ForwardError.
> Properties`, documented in `reqreply-forward-bounce.md`, MUST ship
> first) → **Phase 2.0** (`DeadLetterEnvelope.Properties` + adapter
> capture, THIS doc's own first step) → **Phase 2.1**
> (`ReplayDeadLetter`/`ReplayDeadLetterAsync` themselves, THIS doc's own
> second/main step). See "Addendum" below for the full, now explicitly
> ordered breakdown.
>
> This doc still needs ZERO new wire mechanism of its OWN beyond the
> shared addendum above — `ReplayDeadLetter` reuses Phase 1's shipped
> `followForward` (from hop 2 onward) and `CallRaw` (Phase 0's single-hop
> primitive, once extended by Phase 1.5) directly. Ready to plan Phase
> 2.0/2.1's own implementation next — a distinct, future step requiring
> its own explicit go-ahead, the same rigor Phase 1 itself went through.
> [← Back to Roadmap](index.md)

## Relationship to the other 2 reqreply roadmap docs

This doc was split out of a single combined
`reqreply-client-registry.md` doc once all 3 phases reached "design
complete." The 3 docs are sequenced, not independent:

1. **[`reqreply-client-registry.md`](reqreply-client-registry.md)**
   (Phase 0) — registry infrastructure. This doc's replay-resolution step
   is the registry's SECOND consumer (after Phase 1's forward
   resolution).
2. **[`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)**
   (Phase 1) — forward/bounce. Must ship FIRST; this doc reuses its
   low-level republish-and-decode primitive directly.
3. **This doc** (Phase 2) — dead-letter replay. The narrowest and
   lowest-risk of the 3 phases — zero new wire mechanism, zero new
   adapter-side code.

## Why this exists

Re-examined in detail during the combined doc's original design
discussion: the shipped `DeadLetter` mechanism
(`docs/design/d-0005-error-handling.md`'s Topic 4) is SERVER/ADAPTER-SIDE
ONLY (publishes a `DeadLetterEnvelope` to a destination topic on an
unmatched failure) — it does NOT need a client-side registry by itself.
The REAL, concrete motivation here is narrower: an ops/REPLAY tool
consuming a `DeadLetterEnvelope` needs to resolve `SourceTopic` back to
the ORIGINAL route (for its `Req` codec) to decode `Payload` and reissue
the `Call` — this DOES need
[Phase 0's registry](reqreply-client-registry.md), as a consumer.

## The key simplification — reuses Phase 1's own primitive, zero new wire mechanism

Builds on [Phase 0's registry](reqreply-client-registry.md) directly: an
ops/replay consumer of a `DeadLetterEnvelope{SourceTopic, Payload, Error,
Timestamp}` resolves `SourceTopic` against the registry and reissues the
original request.

`DeadLetter` fires in TWO cases
(`docs/design/d-0005-error-handling.md`'s Topic 4): a DECODE failure, OR
an unmatched business error. If Replay tried to RE-DECODE `Payload` as
the route's own `Req` type before republishing, it would fail
IDENTICALLY for the decode-failure case — replay would be impossible for
exactly the case most likely to need it.

**Replay therefore republishes `Payload` VERBATIM** (no decode/re-encode
step) — exactly the same "publish raw bytes to topic X, await reply,
decode via the registry-resolved target's `Resp` codec" mechanic
[Phase 1's](reqreply-forward-bounce.md) forward-following client
dispatch already needs.

**CORRECTED, after Phase 1 actually shipped and `followForward`'s REAL
signature could finally be checked (not assumed) — `ReplayDeadLetter`
does NOT hand its FIRST hop to the shared loop unchanged.** A prior
revision of this doc claimed `ReplayDeadLetter` "goes through the EXACT
SAME chain-following LOOP Phase 1's own forward-following uses" for its
ENTIRE dispatch, including hop 1. Traced the REAL, shipped
`followForward(ctx, t, originatingRoute any, fwd ForwardError, opts...)`
signature: it UNCONDITIONALLY resolves `originatingRoute`'s own declared
security requirements and compares them against every hop's target
(`checkForwardSecurityScheme` — the mechanism `ForwardSecuritySchemeMismatchError`
exists for). This has no answer for Replay's FIRST hop — there is no
"originating call" at all, `ReplayDeadLetter` starts COLD from a
`DeadLetterEnvelope`, with no prior authenticated context to compare
against. Naively forcing the shared loop to run from hop 1 would make
`checkForwardSecurityScheme`'s real logic (confirmed: target-requires-
security + no originating scheme to match against is ALWAYS a mismatch,
by design, for ordinary Forward) reject EVERY replay against a route
declaring ANY security — almost certainly the MOST common real-world
case a dead-letter replay tool exists to recover.

**Resolution (settled)**: `ReplayDeadLetter` performs its OWN first-hop
registry lookup and dispatch directly — its OWN distinct
`UnrecognizedDeadLetterSourceError` on a miss (see "Structured error"
below), `DuplicateTopicTypeMismatchError` on a poisoned entry, NO
security-scheme check at all (nothing exists yet to compare against on
a cold start), then a single `ClientTransport.CallRaw(ctx, SourceTopic,
Payload)` call, classified the SAME 3-way way the shared loop's own body
does. ONLY if THAT first reply is ITSELF a `ForwardError` does
`ReplayDeadLetter` hand off to the EXISTING, UNCHANGED `followForward`
loop for hop 2 onward — at that point the just-resolved `SourceTopic`
route legitimately becomes the "most recently authenticated hop" the
security-scheme check compares subsequent targets against, exactly
mirroring an ordinary forward chain's own hop-2+ semantics. This mirrors
`Client.Call`'s OWN shape exactly: hop 1 (`t.Call(...)`) always happens
OUTSIDE `followForward`'s loop; only a RECOGNIZED forward reply ever
enters it. `ReplayDeadLetter`'s hop 1 is the direct structural analogue
of `Client.Call`'s own hop 1, not of the loop's own hop 2+ — a prior
revision of this doc conflated the two.

**This ALSO fixes a `MaxForwards` accounting asymmetry as a free side
effect**: had hop 1 been pushed into the shared loop directly (the
prior, incorrect design), it would have consumed 1 unit of the
`MaxForwards` budget an ordinary `Call`'s OWN hop 1 never does (since
that one lives outside the loop too) — a replay resolving on its very
first hop would have counted as "depth 1" against the SAME cap an
equivalent ordinary `Call` counts as "depth 0." Under the corrected
design above, Replay's hop 1 is ALSO outside the loop, so this asymmetry
does not arise; no separate decision was needed.

Phase 2 still benefits AUTOMATICALLY, for free, from `CallRaw`'s
`ForwardRawErrorReply{Code, Body}` enrichment ([Phase 0,
shipped](reqreply-client-registry.md)): if `ReplayDeadLetter`'s own hop-1
reply matches a declared `ErrorPattern`, it decodes it the SAME way the
shared loop's body already does (via the resolved target
`*RouteHandle.DecodeErrorFor`) — this reuse is unaffected by the
resolution above.

## API surface — CONFIRMED

```go
// api/reqreply — both synchronous and async variants, from the start
// (mirrors Call/CallAsync's own pairing).
func (c *Client) ReplayDeadLetter(ctx context.Context, envelope DeadLetterEnvelope, opts ...ClientCallOptions) (any, error)
func (c *Client) ReplayDeadLetterAsync(ctx context.Context, envelope DeadLetterEnvelope, opts ...ClientCallOptions) (any, error) // *Future[any] as any
```

Methods on `Client` (not free functions) — the registry lives on
`Client` (unexported fields), so a method is the natural, no-extra-
surface choice; a free function would just be a thin wrapper needing the
same access anyway. **`ReplayDeadLetterAsync` reuses `Client.CallAsync`'s
RESTRUCTURED goroutine-wraps-a-synchronous-call SHAPE, but NOT its exact
`FutureFactory`-resolution STEP — a distinction a prior revision of this
doc glossed over.** `Client.CallAsync` resolves `FutureFactory` from the
`route` VALUE its own caller passes in (`recoverRegistryEntry(route)`,
then a type assertion). `ReplayDeadLetterAsync` has no such value to
work with — only `envelope.SourceTopic` (a string). Its `FutureFactory`
must instead come from the SAME registry-resolved handle
`ReplayDeadLetter`'s own hop-1 lookup already recovers
(`c.routes[envelope.SourceTopic].Handle`, type-asserted to
`FutureFactory`) — a genuinely different resolution PATH, even though
the surrounding goroutine-launch SHAPE is identical. Spelled out
precisely here so the eventual implementation doesn't have to rediscover
it mid-coding.

## Structured error — a DISTINCT type, not a reuse

```go
type UnrecognizedDeadLetterSourceError struct {
    SourceTopic string
}
```

Returned when `envelope.SourceTopic` isn't registered. Deliberately NOT a
reuse of `UnrecognizedForwardError` ([Phase 1](reqreply-forward-bounce.md))
— even though the underlying condition (a registry miss) is mechanically
identical to Phase 1's own case, a replay caller was never "forwarding"
anything, and a distinct type keeps the error message/semantics clear for
that caller.

A registry entry POISONED by [Phase 0's](reqreply-client-registry.md)
collision detection (two genuinely different routes colliding on the
same `Topic`) surfaces as `DuplicateTopicTypeMismatchError` at this same
resolution point, exactly as it does for Phase 1's forward resolution —
see Phase 0's doc for the full rationale.

## What falls out for free — no new decision needed

If a REPLAYED call's handler itself forwards or dead-letters again, it
is dispatched through the EXACT SAME underlying primitive as an ordinary
`Call` — [Phase 1's](reqreply-forward-bounce.md) `MaxForwards`
chain-depth cap already covers this automatically; Replay needs no
additional cap of its own.

## Operational caution (not a design bug — found during review)

Replaying a PERMANENTLY-broken dead-lettered message (e.g. one that will
ALWAYS fail the same decode/business check) could flood the dead-letter
topic with repeat entries if an operator's tooling auto-replays without
limit. Not something the API itself needs to prevent — the same
ops-tooling/caller-discipline concern as retrying any failed operation
blindly — but `ReplayDeadLetter`/`ReplayDeadLetterAsync` are NOT safe to
loop unconditionally against a message that will never succeed; any
automated replay tooling built on top of this should track attempt
counts or apply its own backoff/giveup policy.

## Addendum — wire-level properties/credentials are lost today; a SHARED fix with Phase 1 (cross-cutting, not Phase-2-only)

**Found during this round's re-review, grounded against the REAL,
shipped `DeadLetterEnvelope`/`CallRaw` code — a genuine, significant gap,
not just polish.** `DeadLetterEnvelope`'s shipped, FIXED schema
(`docs/design/d-0005-error-handling.md`'s Topic 4, predates this phase
entirely) is:

```go
type DeadLetterEnvelope struct {
    SourceTopic string
    Payload     []byte // the original, UNDECODED request bytes ONLY
    Error       string
    Timestamp   time.Time
}
```

There is no field for the original request's wire-level
properties/headers (mqtt5 User Properties; zeromq has no equivalent
mechanism at all, confirmed via `grep` — `adapters/zeromq`'s reqreply
transport has ZERO header/property-param concept today, read OR write
side). **If the ORIGINAL dead-lettered request's security credential was
delivered via a header/property rather than embedded in the body — the
common case for most real security schemes — that credential is
PERMANENTLY LOST once dead-lettered.** `ReplayDeadLetter` republishing
`Payload` verbatim cannot reconstruct it; replay would either fail the
target's own security check or (worse, depending on how a given scheme
enforces absence) silently retry with no credential at all.

**This is NOT a Phase-2-only problem — it is inherited from a LATENT gap
in `CallRaw` itself (Phase 0) that Phase 1's own `Forward` ALSO has,
just less likely to be noticed there.** `ClientTransport.CallRaw`'s
shipped signature — `CallRaw(ctx context.Context, topic string, payload
[]byte) ([]byte, error)` — has NO parameter for wire-level properties at
all. `Forward()` only ever encodes the BODY (`target.
EncodeRequestWithFormats(newReq)`); an ordinary forward to a target
requiring a property/header-delivered credential or a required header
param has EXACTLY the same unsolved problem Replay does, just rarer in
practice (Replay's whole premise — recovering a FAILED message — makes
hitting a secured route disproportionately likely; an ordinary Forward
chain is more often within a single trust domain).

**Resolution (confirmed, cross-cutting — implemented as a SHARED
addendum with [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md),
not duplicated independently in each doc), with an EXPLICIT phase order
— a further re-review found the FIRST version of this addendum said the
2 docs' plans should be "sequenced together" without ever committing to
an actual ORDER; traced the real dependency chain (`ReplayDeadLetter`
can only pass `envelope.Properties` into `CallRaw` once `CallRaw`
already accepts one, and `DeadLetterEnvelope` already has a field to
read it from) to confirm the order below is FORCED by the code shape,
not a style preference**:

- **Phase 1.5 (documented primarily in [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md),
  MUST ship before Phase 2 begins)**:
  1. **`ClientTransport.CallRaw` gains a new `properties
     map[string]string` parameter** — a BREAKING change to the
     already-shipped interface, touching all 3 implementations:
     `adapters/mqtt5`'s `clientTransport.CallRaw` (threads `properties`
     into the outgoing `PublishProperties.User`, mirroring the EXISTING
     `userPropertiesFromMap` conversion `Serve`'s own write-side wiring
     already uses) and `adapters/zeromq`'s `clientTransport.CallRaw`/
     `dealerClientTransport.CallRaw` (accept the parameter for interface
     symmetry; zeromq has nowhere on the wire to put it today — a
     confirmed, accepted no-op there, not a partial implementation,
     since zeromq's reqreply transport has no property mechanism of its
     own to extend).
  2. **`ForwardError` gains a new `Properties map[string]string` field**
     (mirrors `Payload`'s existing shape) — `Forward()` needs a new,
     EXPLICIT way for the calling handler to supply properties to carry
     through (an opts-style parameter, matching this library's existing
     declarative `ClientHandleOpt`/`RouteOpt` philosophy — NOT invisible
     context-threading magic); exact shape TBD at implementation-
     planning time. `Client.followForward` threads `fwd.Properties`
     through its own `CallRaw` calls.
- **Phase 2.0 (THIS doc's own first implementation step — depends on
  Phase 1.5 having landed first, since it needs `CallRaw` to already
  accept properties)**:
  3. **`DeadLetterEnvelope` gains a new `Properties map[string]string`
     field**, capturing the ORIGINAL request's properties at
     dead-letter time — confirmed a reqreply-package-LOCAL change only:
     `api/events.DeadLetterEnvelope` is a SEPARATE, independently-
     defined type in a different package (mirrored SHAPE, not a shared
     type), so this does NOT require touching `api/events` at all.
     Every reqreply adapter's dead-letter-PUBLISH call site needs
     updating to CAPTURE the incoming request's real properties into
     this new field — relevant to `adapters/mqtt5` only (confirmed
     `adapters/zeromq` has no property mechanism to capture from, a
     no-op there too, consistent with Phase 1.5's point 1 above).
- **Phase 2.1 (THIS doc's own second/main implementation step — depends
  on Phase 2.0, is the NET NEW `Client.ReplayDeadLetter`/
  `ReplayDeadLetterAsync` client-facing surface itself)**:
  4. **`ReplayDeadLetter` passes `envelope.Properties` through to its
     own hop-1 `CallRaw` call** (see "The key simplification" above for
     hop 1's now-corrected, loop-free shape) — no further design
     decision needed beyond threading the new field through, now that
     Phase 2.0 guarantees it is populated.

This is a genuinely large, multi-part change (a breaking interface
change + 3 adapter implementations + 2 structured-type extensions,
spanning Phase 0/1/2's own already-shipped code) — explicitly scoped as
its OWN implementation-planning pass, NOT folded silently into either
phase's existing plan. [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)
carries the IDENTICAL addendum text for Phase 1.5 (its own
responsibility to implement first); Phase 2.0/2.1 are THIS doc's own
responsibility, in that order — read both docs together, not in
isolation, since all 3 phases consume the same underlying `CallRaw`
change.

## Dispatch-path inventory — confirmed much smaller than Phase 1's

`ReplayDeadLetter`/`ReplayDeadLetterAsync` are `Client`-level-ONLY
methods — there is no `ports.Pattern` binding equivalent to
independently verify (an ops/replay tool invokes `Client` directly; it is
not a domain port a `ports.Pattern` binding would ever wrap). Confirmed
clean via this structural reasoning, not silently skipped — this is a
genuine, structural reason Phase 2 avoids
[Phase 1's](reqreply-forward-bounce.md) 6-point wire-signal dispatch
inventory, not an oversight matching Round 201's own lesson.

## Next step

Dead-letter replay has a SETTLED design, hardened across this round's
re-review against the REAL, shipped Phase 0/1 code for the first time —
**the Phase 1 blocking dependency is SATISFIED** (shipped), and this
round's findings (hop-1's loop-free dispatch shape; the cross-cutting
`CallRaw`/`ForwardError`/`DeadLetterEnvelope` properties addendum, NOW
WITH AN EXPLICIT ORDER) are both resolved at the DESIGN level above.
None of it is implemented yet. The implementation sequence is now
UNAMBIGUOUS:

1. **Phase 1.5** — `CallRaw`'s breaking signature change +
   `ForwardError.Properties` — [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md)'s
   own responsibility to plan and implement FIRST; THIS doc has nothing
   to implement until it lands.
2. **Phase 2.0** — `DeadLetterEnvelope.Properties` + each adapter's
   dead-letter-publish capture (mqtt5 only) — THIS doc's own first step,
   depends on Phase 1.5.
3. **Phase 2.1** — `Client.ReplayDeadLetter`/`ReplayDeadLetterAsync`
   themselves (`UnrecognizedDeadLetterSourceError`, the
   poisoned-registry-entry case, the hop-1-outside-the-loop dispatch
   shape, the distinct `FutureFactory` resolution path, threading
   `envelope.Properties` through) — THIS doc's own second/main step,
   depends on Phase 2.0.

Turning this into an actual implementation plan (unit test plan per
phase, SQL todos with dependencies matching the 1.5 → 2.0 → 2.1 order
above) is a distinct, future step requiring its own explicit go-ahead,
the same rigor Phase 1 itself went through. No code should be written
against this design until that implementation plan exists and is
approved.
