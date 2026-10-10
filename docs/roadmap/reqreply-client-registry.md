# `api/reqreply.Client` Registry — Idea / Investigation Only

> **Status:** Idea / investigation only — no driver, no proposal, explicitly
> deferred until [Typed HTTP Redirects](rest-typed-redirects.md) ships.
> Spun out of that doc's own `rest.Client` registry investigation, once a
> real asymmetry between `reqreply.Client` and `rest.Client` was noticed.
> [← Back to Roadmap](index.md)

## Why this exists

While designing [Typed HTTP Redirects](rest-typed-redirects.md)'s client-side
route registry for `rest.Client`, a question was raised: should
`api/reqreply.Client` get an equivalent registry too, for symmetry? This doc
captures that question — deliberately NOT resolved, consistent with how
other "idea only" docs in this roadmap (`sse-resume-and-retry-policy.md`,
`vector-store-adapter.md`, `mutable-native-integration.md`) record a
genuinely open question without forcing a premature decision.

## The asymmetry, confirmed

`api/reqreply.Client`'s own doc comment states outright:

> *"Client accumulates NO spec state — it is purely a dispatch handle, one
> [ClientTransport] attached via [Client.Attach], mirroring
> [rest.Client]/[events.Client]'s identical shape."*

That comment was accurate when written — but `rest.Client` is no longer
going to be "purely a dispatch handle" once its own registry
(`rest-typed-redirects.md`) ships. `events.Client` already has one too
(`specByTopic`/`subscriberByTopic`, pre-dating this whole discussion).
`reqreply.Client` would become the ONLY one of the three `api/*` `Client`
types with no registry at all.

## Investigated: does reqreply have a structural analog to an HTTP redirect?

**No** — confirmed directly, not assumed. `rest.Client`'s registry exists to
solve a SPECIFIC problem: an HTTP 3xx response doesn't carry its own typed
`Resp` — the client has to look up which registered route a received
`Location` corresponds to, in order to know what type to decode. reqreply
has no equivalent gap: `Call`/`CallAsync` always know their own `Resp` type
at the call site (it's the generic type parameter the caller is working
with), and `CallAsync`'s returned `*Future[Resp]` is typed at construction
too, before any response has even arrived. There is no "decode an unknown
target's response type" problem anywhere in reqreply's dispatch today.

**This means a reqreply registry, if it ever happens, would NOT be solving
the same problem `rest.Client`'s registry solves.** It would need its own,
independently-justified reason to exist.

## Candidate motivations (3 identified, none confirmed — genuinely open)

1. **A new, reqreply-native "forward/bounce" concept.** A handler could
   respond "actually, route X should handle this," and the caller
   auto-follows to THAT route's typed response — the closest reqreply
   analog to an HTTP redirect, but a wholly NEW mechanism that doesn't exist
   in any form today (no sentinel error, no dispatch hook, nothing). Would
   need its own full design pass (server-side signaling shape, client-side
   resolution, structured errors, Observer integration) mirroring
   `rest-typed-redirects.md`'s own depth — not a trivial port of that doc's
   design.
2. **Pure structural symmetry.** Every `api/*` `Client` should have the
   SAME registry shape, independent of any concrete reqreply feature need
   right now — i.e. add the registry as a forward-looking capability with
   no current consumer, purely so the 3 `Client` types stay shaped alike.
   Weakest justification of the 3 — a registry with zero current callers is
   dead code until motivation 1 or 3 (or something else) actually needs it.
3. **Tied to dead-letter/error handling.** Resolving a failed call's
   retry/requeue target via a known route, reusing (or extending) the
   existing `DeadLetter` mechanism (`docs/design/
   d-0005-error-handling.md`'s Topic 4) rather than inventing a new concept
   from scratch.

These lead to MATERIALLY DIFFERENT APIs — a new sentinel-error-driven
dispatch mechanism (1) vs. a no-op symmetry change with no behavior (2) vs.
extending an existing, already-shipped mechanism (3). Picking the wrong one
would mean designing and building something nobody needs. **Do not start
real design work here until the motivation is confirmed.**

## Why this is sequenced AFTER `rest-typed-redirects`, not in parallel

The same "defer and compare" reasoning already applied to the broader
`internal/registry` extraction question (see `rest-typed-redirects.md`'s own
"Client-side route registry" section) applies here directly: `rest.Client`'s
registry is itself a NEW, not-yet-implemented mechanism with real open
design questions of its own (ambiguous-match policy, same-routeKey
re-registration idempotency, etc.) — those will only be fully settled once
it's actually built and its own tests pass. Any reqreply design — whichever
motivation turns out to be the real one — should be informed by the
SHIPPED, working shape of `rest.Client`'s registry (what worked, what had to
change during implementation), not designed blind in parallel against a
still-moving target.

## Next step

Once `rest-typed-redirects` ships: revisit this doc, get an explicit answer
on which (if any) of the 3 candidate motivations is the real goal, and only
then turn this into an actual design draft (API surface, structured errors,
Observer integration, unit test plan — the same rigor `rest-typed-
redirects.md` itself went through). Until that happens, this doc stays
"idea only."
