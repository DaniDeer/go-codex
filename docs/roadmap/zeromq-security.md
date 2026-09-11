# ZeroMQ Security Mechanism — `adapters/zeromq`

> **Status:** Idea only — no driver yet, but SIGNIFICANTLY DE-RISKED.
> Spun out of [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
> escape-hatch simplification review (Escape Hatch #5), which originally
> assumed closing this gap required inventing an entirely new wire-level
> credential convention before ANY progress was possible. That
> assumption turned out to be wrong: resolving a related finding
> (Escape Hatch #6, `mqtt` v3's publish-side "no credential mechanism"
> gap) surfaced an IN-PAYLOAD message-level security mechanism —
> `PublishMW`/`SubscribeMW`'s `fn` gaining write/read access to `*T`
> (the decoded payload itself) — that needs NO wire-format change at
> all and applies to zeromq identically. This doc's remaining scope is
> now much narrower: whether zeromq ALSO wants an optional, additional
> out-of-band frame-based mechanism, plus a separate connection-level/
> CURVE question. No sequencing dependency on the pub/sub workflow
> doc's own Decision 1/2/3 — those already specify the in-payload
> mechanism directly; this doc only covers the OPTIONAL extras.
>
> **Scope extended beyond pub/sub**: this doc's in-payload finding ALSO
> resolves the analogous open question
> [ReqReply Middleware](reqreply-middleware.md) raised for zeromq's
> `reqreply` security Fn-shape (see "Implication for
> `reqreply-middleware.md`" section below) — same underlying reasoning,
> same decision, a different `api/*` pattern.
> [← Back to Roadmap](index.md)

## Confirmed current state

`adapters/zeromq` has ZERO security mechanism of any kind today —
confirmed via exhaustive grep: zero hits for
`SecurityFunc`/`CredentialFunc`/`middleware.` anywhere in
`adapters/zeromq/*.go`. `Subscribe`/`Publish` only ever see
`[topic, payload]` frames — there is no header/property slot to extract
an OUT-OF-BAND credential FROM, unlike MQTT5's User Properties or even
MQTT 3.1.1's CONNECT-time username/password.

## What's now tractable, without any wire-format change

[Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
Decision 3 gives `SubscribeMW`/`PublishMW`'s `fn` read/write access to
`*T` — the DECODED payload itself, not any transport-specific envelope.
A credential embedded as an ordinary field in the payload (validated,
merged, and encoded via the SAME codec as the rest of the message) works
identically regardless of transport, since `T` is entirely
transport-agnostic. Concretely, for zeromq:

```go
// Both directions follow the SAME shape — no raw-message-equivalent
// parameter exists (or is needed) since zeromq's [topic, payload]
// frames carry nothing beyond what's already decoded into T, unlike
// mqtt/mqtt5's subscribe-side which also gets the raw *pahomqtt.Message/
// *pahomqtt5.Publish for reading protocol-native User Properties.
func(ctx context.Context, msg *T, reqs []route.SecurityRequirement) error
```

This closes the PRACTICAL message-level security gap for zeromq without
any of the wire-level invention originally assumed necessary — a
`SubscribeMW`/`PublishMW` `fn` following this shape can already verify/
attach a credential today, using the SAME declarative attachment point
(`Subscriber[T].SubscribeMW`/`Publisher[T].PublishMW`) every other
transport uses. **This piece does not need its own roadmap item — it
is part of [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
Decision 3 directly**, once implemented there.

## What remains genuinely open (this doc's actual scope)

1. **An OPTIONAL, additional out-of-band frame-based mechanism** —
   mirroring MQTT5's User Properties via an EXTRA ZeroMQ frame (e.g.
   `[topic, credential, payload]` instead of `[topic, payload]`), kept
   SEPARATE from the payload itself. Would this add real value over the
   in-payload mechanism above? Candidate reasons it might: keeping
   credential material out of the payload's own schema/codec (cleaner
   separation of concerns, no "security field" polluting the domain
   type); allowing credential verification BEFORE the (potentially more
   expensive) full payload decode runs. Candidate reasons it might not:
   more wire-format complexity for marginal gain, when the in-payload
   mechanism already solves the core problem. Not decided — needs a
   concrete driver/use case before investing design effort here.
2. **Connection-level authentication equivalent** — ZeroMQ's base
   REQ/REP/PUB/SUB sockets have no CONNECT-time credential handshake
   analogous to MQTT's CONNECT packet (`SecuredClient`'s MQTT5/MQTT
   3.1.1 model has NO ZeroMQ equivalent to mirror). ZeroMQ's own CURVE
   security mechanism (public-key transport encryption/auth, built into
   libzmq) is the closest existing primitive, but it operates at the
   SOCKET/transport layer, entirely below go-codex's abstraction —
   worth investigating whether go-codex should expose ANY CURVE
   configuration at all, or leave it entirely to the caller's own socket
   setup (mirroring how MQTT connection-lifecycle methods —
   `Connect()`/`Disconnect()` — are deliberately NOT managed by
   go-codex either, per `docs/features/security.md`'s connection-level
   section).
3. **Is there real demand for either of the above at all**, or is "use
   the in-payload mechanism for message-level, CURVE + your own
   authorization layer entirely outside go-codex for connection-level"
   a sufficient answer on its own (mirroring `api/mcp`'s deliberate
   "security handled entirely outside go-codex" precedent)?

No implementation, no API sketch for items 1-2 — this doc exists to
hold the NARROWED-DOWN open question and scope a future investigation,
not to answer it. Item 3's answer may simply be "no further work
needed," in which case this doc's eventual resolution could be "closed,
no action" rather than a design.

## Implication for `reqreply-middleware.md` — SAME decision as pub/sub

[ReqReply Middleware](reqreply-middleware.md) — the design for
`api/reqreply`'s own `.Use()`/`HandleMW`/`ClientMW` declare/implement
split — currently defers zeromq's Fn-shape entirely, stating (carried
forward from `docs/design/d-0004-reqreply-workflow-simplification.md`
without re-checking against THIS doc's own, already-resolved finding)
that it "needs a NEW wire-level credential convention before ANY Fn
shape can be finalized." **This is stale/overly pessimistic** — found
while cross-checking the two docs against each other:

- zeromq's REQ/REP frames (`[payload]` request / `["ok"/"error",
  payload]` reply, confirmed via `adapters/zeromq/reqreply_transport.go`)
  have the EXACT SAME "no separate credential slot" shape as pub/sub's
  `[topic, payload]` frames — the SAME in-payload mechanism this doc
  already established for pub/sub (confirmed ALREADY SHIPPED:
  `adapters/zeromq/adapter.go:82,110` — `SecurityFunc func(ctx, msg *T,
  reqs) error` / `CredentialFunc func(ctx, msg *T, reqs) error`) applies
  to reqreply identically — NO new wire-level frame is needed there
  either.
- **Decision (same as the pub/sub case, confirmed)**: reqreply's zeromq
  paired security Fn shapes read/write the decoded `*Req` directly,
  mirroring the pub/sub shapes above EXACTLY (same parameter shape, same
  plain-`error` return — deliberately NOT REST's scope-grant model
  `reqreply-middleware.md` has mqtt5 adopt for ITS OWN paired Fn shape;
  each transport adapter mirrors its OWN precedent, an intentional,
  already-established per-adapter difference in that doc, not an
  inconsistency introduced here):

  ```go
  // Server-side (paired) — reads the decoded *Req before dispatch,
  // mirrors zeromq pub/sub's Subscribe security shape (SecurityFunc)
  // exactly:
  func(ctx context.Context, req *Req, reqs []route.SecurityRequirement) error

  // Client-side (paired) — writes a credential field INTO *Req before
  // publish, mirrors zeromq pub/sub's Publish security shape
  // (CredentialFunc) exactly:
  func(ctx context.Context, req *Req, reqs []route.SecurityRequirement) error
  ```

- **Implication for callers**: the APPLICATION's own domain `Req`
  struct must carry a credential field itself (e.g. `Req{..., Token
  string}`) for this to work — mirrors pub/sub's IDENTICAL implication,
  already accepted there; zeromq security remains "in-payload only," a
  documented, accepted transport limitation, not a new one introduced by
  reqreply.

**Status of this finding: documented here for follow-up, NOT yet acted
on.** `reqreply-middleware.md`'s own Phase 1 stays mqtt5-only as
originally scoped; this finding means zeromq's reqreply Fn-shape is
LIKELY tractable sooner than that doc currently assumes (no blocking
wire-level invention needed), but reconciling `reqreply-middleware.md`'s
own text (updating its "needs a NEW wire-level credential convention"
framing, deciding whether zeromq's reqreply security becomes part of a
near-term phase there rather than an indefinitely-deferred one) is left
as explicit future follow-up work, not done in this pass.

## REMINDER for zeromq's own future Fn-shape phase: also remove `SecurityFunc`/`CredentialFunc` then

`reqreply-middleware.md`'s Phase 1 (mqtt5-only, in progress) makes a
BREAKING change mirroring REST's own D-0001 precedent (confirmed via
code: `adapters/nethttp/adapter.go`'s own `"BREAKING: Observer and
SecurityFunc are REMOVED"` doc comment) — REMOVES
`mqtt5.ServeOptions.SecurityFunc`/`mqtt5.CallOptions.CredentialFunc`
ENTIRELY once `.Use`/`HandleMW`/`ClientMW`'s declared
`Implementations`/`ClientImplementations` become the sole credential
mechanism, for BOTH the escape hatch (`Serve`/`Call`/`CallHandle`) and
`Attach`-based dispatch. This reverses `reqreply-middleware.md`'s
Decision #4, which previously followed EVENTS pub/sub's precedent
(keep both mechanisms permanently) instead of REST's.

**This is NOT actioned for zeromq in mqtt5's Phase 1** — zeromq has NO
`.Use`/`HandleMW`/`ClientMW` mechanism yet (that's the Fn-shape work
THIS doc tracks as a follow-up, still not started), so removing
zeromq's `events.SubscribeOptions.SecurityFunc`/`PublishOptions.
CredentialFunc` or `reqreply.ServeOptions.SecurityFunc`/`CallOptions.
CredentialFunc` NOW would leave zeromq with ZERO security mechanism —
a pure regression, not a parity improvement.

**But when zeromq's own Fn-shape phase (this doc's own tracked
follow-up) eventually ships** `.Use`/`HandleMW`/`ClientMW` for zeromq
(both events pub/sub AND reqreply, using the Fn shapes this doc already
decided above), it should ALSO remove zeromq's `SecurityFunc`/
`CredentialFunc` fields at THAT point — mirroring mqtt5's Phase 1
exactly, for the identical reason (REST's precedent: one declarative
mechanism only, no permanent parallel imperative escape hatch living
alongside it). Do not repeat events pub/sub's "keep both forever"
choice for zeromq's reqreply OR revisit it for zeromq's own events
pub/sub mechanism without an explicit, reasoned decision to do so —
default to REMOVAL, matching REST/mqtt5's now-established precedent,
unless a genuine new reason to keep both emerges at that time.
