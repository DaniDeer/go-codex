# `api/reqreply.Client` Registry — Idea / Investigation Only

> **Status:** Idea / investigation only — no driver, no proposal yet. The
> blocking dependency has cleared: Typed HTTP Redirects
> ([feature](../features/rest-redirects.md) · [guide](../guides/rest-redirects.md))
> has SHIPPED — `rest.Client`'s own registry is now the real, working
> precedent this doc's "next step" calls for. Spun out of that feature's
> `rest.Client` registry investigation, once a real asymmetry between
> `reqreply.Client` and `rest.Client` was noticed.
> [← Back to Roadmap](index.md)

## Why this exists

While designing Typed HTTP Redirects' client-side route registry for
`rest.Client` (now shipped — see [the feature page](../features/rest-redirects.md)),
a question was raised: should `api/reqreply.Client` get an equivalent
registry too, for symmetry? This doc captures that question — deliberately
NOT resolved, consistent with how other "idea only" docs in this roadmap
(`sse-resume-and-retry-policy.md`, `vector-store-adapter.md`,
`mutable-native-integration.md`) record a genuinely open question without
forcing a premature decision.

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
   resolution, structured errors, Observer integration) mirroring the depth
   Typed HTTP Redirects' own 4 critical-review rounds went through — not a
   trivial port of that design.
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

## Next step

The blocking dependency has cleared. Revisit this doc, get an explicit
answer on which (if any) of the 3 candidate motivations is the real goal,
and only then turn this into an actual design draft (API surface,
structured errors, Observer integration, unit test plan — the same rigor
Typed HTTP Redirects itself went through, documented in its own 4
critical-review rounds before implementation began). Until that happens,
this doc stays "idea only."
