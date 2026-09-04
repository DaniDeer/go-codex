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
