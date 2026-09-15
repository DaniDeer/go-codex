# WebSocket — should it gain a general-purpose declarative middleware mechanism?

> **Status:** Idea only — no code written, no spike run yet. Spun out of
> reviewing whether the pub/sub `Observability[T]` 3-way-duplication
> gap (see
> [D-0002](../design/d-0002-pubsub-workflow-simplification.md)'s
> Addendum — Decision A, now shipped)
> had an equivalent in REST req/resp, SSE, or WebSocket. REST/SSE were
> confirmed to have NO equivalent gap (both already share ONE
> `nethttp.Observability`, reused unchanged by `adapters/chi`).
> WebSocket's situation is DIFFERENT in kind, not degree — it has no
> declarative middleware-attachment mechanism at all — which this doc
> exists to scope, not to assume needs fixing.
> [← Back to Roadmap](index.md)

## Motivation

Every OTHER Layer 2 boundary go-codex ships (`api/rest`, `api/events`,
`api/reqreply`) has a declare-time, general-purpose
`.HandleMW(nil, fn)`/`.ClientMW(nil, fn)`/`.SubscribeMW(nil,
fn)`/`.PublishMW(nil, fn)` attachment point — an UNPAIRED decorator
shape used for cross-cutting concerns like observability, composable
alongside PAIRED security implementations declared via the same `.Use()`
mechanism. `adapters/websocket` has **none of this** — confirmed via
direct code search (zero matches for `.HandleMW`/`.ClientMW`/
`.SubscribeMW`/`.PublishMW`/`.Use(` anywhere under `adapters/websocket`
or any `api/events` socket-specific type).

This is NOT an oversight discovered by accident — `adapters/websocket`
is built on `ports.DuplexPort`/`ports.SocketPattern` (confirmed via
`ports/duplex_port.go`), a `ports`-owned construct, NOT `api/events`'s
`Channel`/`Subscriber`/`Publisher` builder that the `.SubscribeMW`/
`.PublishMW` mechanism belongs to. There is no `events.Channel`-style
spec object for a socket endpoint to hang a `.Use()` call off of in the
first place. Observer wiring today is a directly-configured
`Options.Observer` field (e.g. `DuplexSocketAdapterOptions.Observer`,
`BroadcastSocketAdapterOptions.Observer`, `DialAdapterOptions.Observer`)
resolved once per adapter construction (with a ctx-fallback via
`stats.ObserverFromContext` when nil) — arguably already "thin," just a
DIFFERENT mechanism than the declare-time-attached-decorator pattern
REST/events/reqreply share.

## Open question this doc exists to scope (NOT pre-answered)

Should WebSocket gain an equivalent general-purpose middleware
attachment surface — and if so, what would it even wrap? Candidates,
none evaluated in depth yet:

1. **Do nothing — current design is sufficient.** `Options.Observer`
   (plus `ErrorFrame`'s existing declarative error-payload matching,
   see `docs/features/websocket.md`) may already cover every real need;
   a decorator-shaped `.Use()`-alike could be pure ceremony with no
   behavior it doesn't already support. This is the FIRST hypothesis to
   disprove, not a foregone rejection.
2. **Per-frame decorator on `DuplexAdapter.Activate`'s dispatch loop**
   — mirrors `reqreply.Observability`'s shape
   (`func(next func(ctx, Framed[In]) error) func(ctx, Framed[In])
   error`-ish), attached at `PluginSocketPattern`/`Bind` time. Unclear
   whether this composes sensibly with per-session broadcast/targeted-
   reply semantics (`Framed.Session`) the way a request/response
   decorator composes with a single call.
3. **Connection-lifecycle decorator** (on `Upgrade`/dial, not
   per-frame) — closer to `nethttp.Observability`'s "wrap the whole
   call" shape, but a WebSocket connection isn't a single call; it's a
   long-lived session emitting many frames, so "wrap the whole call"
   has no obvious WebSocket analogue.
4. **No Security-equivalent at all is currently possible** — since
   there is no `.Use()`/`HandleMW` pairing mechanism, WebSocket has no
   way to declare "this endpoint requires implementation X" the way
   REST/events/reqreply security schemes do (upgrade-time
   auth is handled via ordinary path/query/header param validation
   instead, per `docs/features/websocket.md`). Any middleware mechanism
   design MUST account for whether it should also unlock a
   Security-equivalent, or deliberately stay observability-only forever
   (mirrors MCP's own permanent "no Security" design in
   [Declarative Middleware](declarative-middleware.md)).

## Explicitly NOT concluded by this doc

- Whether this is worth doing at all (see candidate 1 above — the
  honest null hypothesis).
- Any concrete Go signature, type name, or attachment API — all 4
  candidates above are unevaluated sketches, not proposals.
- Any relationship to [Declarative Middleware](declarative-middleware.md)'s
  own remaining MCP/`ports.File`/`Cache`/`SQL`/`Dir` scope — WebSocket is
  NOT currently listed in that doc's coverage table at all; if a driver
  is ever confirmed here, reconciling the two docs' scope (one doc vs.
  two) is a separate decision, not made here.
- Any relationship to
  [D-0002](../design/d-0002-pubsub-workflow-simplification.md)'s
  Addendum (Decision A) — that decision is about consolidating 3
  EXISTING, already-shipped duplicate implementations of the SAME
  mechanism; this doc is about whether the mechanism should exist for
  WebSocket AT ALL. Different category of question.

Not implemented — investigation/planning only. No committed scope, no
priority assigned, no driver/concrete use case identified yet.
