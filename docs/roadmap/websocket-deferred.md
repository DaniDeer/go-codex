# `adapters/websocket` — Deferred Items

> **Status:** Deferred — awaiting use cases.
> [← Back to Roadmap](index.md)
>
> See also: [WebSocket Adapter (Phases 1+2, shipped)](../features/websocket.md) ·
> [`adapters/websocket` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/websocket)

Shipped so far: Phase 1 — server-side adapters (`IngestSocketAdapter`,
`BroadcastSocketAdapter`, `DuplexSocketAdapter`), the sixth port type
`ports.DuplexPort[In,Out]`, `ports.SocketPattern`. Phase 2 — client-side
dial adapters (`DialSourceAdapter`/`DialSinkAdapter`/`DialDuplexAdapter`
with auto-reconnect + gap visibility), chi variants, and AsyncAPI spec
generation (`ports.RegisterSocket` over `events.Builder.AddChannelItem`).

## Deferred (awaiting a use case)

### 1. `ConnectionObserver` stats extension

Connect/disconnect events and a concurrent-session gauge are a genuinely
new lifecycle not covered by `RecordRequest`/`RecordSubscribe`/
`RecordPublish`. Deferred until a metrics use case demands the ninth
observer interface — do not add speculatively.

### 2. Subprotocol negotiation beyond a static list

`SocketPattern.Subprotocols` is a static accept-list. Dynamic negotiation
(per-connection protocol selection influencing the frame format) has no
driver yet.

## Out of scope (will not implement)

- **MQTT-over-WebSocket support in this adapter** — that is a transport
  option of the MQTT client (pass a `ws://` broker URL to paho); accepting
  MQTT clients here would mean implementing an MQTT broker.
- **A universal "StreamPattern"** covering WebSocket + Redis Streams —
  incompatible addressing and delivery semantics; verdict recorded in
  Phase 1 (see `docs/features/websocket.md` and the redis Phase 2 roadmap).
- **Outbound queueing across reconnect gaps in dial adapters** — frames
  during a gap are dropped WITH a reported error by design (at-most-once,
  no silent loss, no unbounded buffering). Upstream pumping/buffering is
  the caller's decision.
