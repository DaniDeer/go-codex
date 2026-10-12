# `api/reqreply` Dead-Letter Replay — Design Complete, Not Yet Implemented

> **Status:** Design complete — API surface and structured error are
> settled, and the dispatch-path inventory is confirmed trivial (no
> `ports.Pattern` binding equivalent to verify, unlike
> [Phase 1](reqreply-forward-bounce.md)). **Blocked on
> [`reqreply-forward-bounce.md`](reqreply-forward-bounce.md) (Phase 1)
> shipping first** — this doc needs zero new wire mechanism of its own,
> reusing Phase 1's own chain-following loop (built on `CallRaw`, Phase
> 0's single-hop primitive) directly; it cannot be implemented before
> that loop exists. [← Back to Roadmap](index.md)

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
dispatch already needs. Concretely, `ReplayDeadLetter` goes through the
EXACT SAME chain-following LOOP Phase 1's own forward-following uses
(NOT a single, raw `ClientTransport.CallRaw(ctx, topic, payload)
([]byte, error)` call directly — found during a further re-review:
`CallRaw` is only a SINGLE-hop primitive; if the replayed message's own
handler ALSO forwards, only the LOOP built on top of `CallRaw` — see
[Phase 0's "Adapter↔registry bridge"](reqreply-client-registry.md) and
[Phase 1's "Client-side resolution"](reqreply-forward-bounce.md) for its
exact shape — correctly continues the chain, decrementing the SAME
`MaxForwards` counter). `ReplayDeadLetter` invokes that loop with
`(SourceTopic, Payload)` as its starting point instead of a recognized
forward reply, decoding the FINAL raw reply via the registry-resolved
target's `Resp` codec in `api/reqreply.Client` itself. This means
**Phase 2 needs ZERO new wire mechanism and ZERO new adapter-side code**
— both `CallRaw` and the chain-following loop are implemented ONCE, by
Phase 0/1, and reused unchanged here. This also means Phase 2 benefits
AUTOMATICALLY, for free, from `CallRaw`'s `ForwardRawErrorReply{Code,
Body}` enrichment ([Phase 0, shipped](reqreply-client-registry.md)): if
a replayed message's reply matches a declared `ErrorPattern`,
`ReplayDeadLetter` gets the SAME structured decode (via the loop's
already-resolved target `*RouteHandle.DecodeErrorFor`) an ordinary
forward-following `Call` would — no separate work needed here either.

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
same access anyway. **`ReplayDeadLetterAsync` reuses the SAME
goroutine-wraps-its-own-synchronous-call pattern
[Phase 1's](reqreply-forward-bounce.md) restructured `Client.CallAsync`
uses** (confirmed, after Phase 1's own review found the ORIGINAL
"delegate to `ClientTransport.CallAsync`" design structurally broken —
see that doc's "`CallAsync`/`Future`" section): spawn a goroutine
wrapping the synchronous `ReplayDeadLetter` (which already runs the
chain-following loop), resolve a `Future` with its result. `Replay
DeadLetterAsync` was ALREADY correctly scoped as a `Client`-level-ONLY
method with no `ClientTransport` involvement at all, so it has NO
equivalent structural issue to resolve — this note just keeps the two
docs' terminology consistent now that Phase 1's own design changed.

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

Dead-letter replay has a SETTLED design — none of it is implemented yet,
and it is explicitly BLOCKED on
[`reqreply-forward-bounce.md`](reqreply-forward-bounce.md) (Phase 1)
shipping first (this doc reuses Phase 1's own low-level primitive
directly; there is nothing for Phase 2 to implement until that primitive
exists). Once Phase 1 ships, turning this into an actual implementation
plan (unit test plan covering `UnrecognizedDeadLetterSourceError`, the
poisoned-registry-entry case, and the goroutine-wraps-synchronous-call
async pattern reuse) is a distinct, future step requiring its own
explicit go-ahead. No code
should be written against this design until that implementation plan
exists and is approved.
