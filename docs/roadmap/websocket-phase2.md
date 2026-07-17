# `adapters/websocket` — Phase 2: Client-Side WS + chi

> **Status:** Deferred — awaiting use case.
> [← Back to Roadmap](index.md)
>
> See also: [WebSocket Adapter (Phase 1, shipped)](../features/websocket.md) ·
> [`adapters/websocket` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/websocket)

Phase 1 shipped server-side WS (`IngestSocketAdapter`, `BroadcastSocketAdapter`,
`DuplexSocketAdapter`), the sixth port type `ports.DuplexPort[In,Out]`
(session-tagged `Framed[T]`, targeted replies + broadcast), and
`ports.SocketPattern`. Recorded design history: the "universal streaming
pattern" was rejected — Redis Streams (key-addressed, at-least-once, XACK)
gets its own declaration when the redis Phase 3 roadmap lands.

## Deferred items

### 1. Client-side WebSocket (dial out to external feeds)

`DialAdapter`-family: connect to an external `ws://` endpoint and bridge it
into ports — `SourcePort` (consume a feed), `SinkPort` (publish), or
`DuplexPort` (full duplex client). `ports.DuplexPort` and
`ports.SocketPattern` are transport-agnostic and reuse unchanged; only the
adapter side is new (gorilla `Dialer` behind the existing narrow `Socket`
interface, reconnect policy = the open design question — silent reconnect
loses frames, so gap visibility must be surfaced like the redis pub/sub
roadmap's decision 1).

### 2. chi variants

`chi.IngestSocketAdapter`/`BroadcastSocketAdapter`/`DuplexSocketAdapter` —
same API surface, but chi's Mux is NOT registration-safe while serving:
apply the swap-handler constructor-time registration pattern (review-skill
history R54). Mechanical once a chi consumer appears.

### 3. `ConnectionObserver` stats extension

Connect/disconnect events and a concurrent-session gauge are a genuinely
new lifecycle not covered by `RecordRequest`/`RecordSubscribe`/
`RecordPublish`. Deferred until a metrics use case demands the ninth
observer interface — do not add speculatively.

### 4. Subprotocol negotiation beyond a static list

`SocketPattern.Subprotocols` is a static accept-list. Dynamic negotiation
(per-connection protocol selection influencing the frame format) has no
driver yet.

### 5. AsyncAPI spec generation (ws bindings)

OpenAPI cannot express WebSocket (request/response only — today, supplying
`PortOptions.RESTBuilder` incidentally documents the upgrade route as a bare
`GET` with empty request/response, nothing about frames). **AsyncAPI** has
first-class WebSocket bindings (`ws`): `SocketPattern.Path` maps to a
channel, the port's In/Out codec schemas (`codec.Schema()` — the same
source the MQTT AsyncAPI spec uses) map to subscribe/publish message
payloads. Proposed shape, mirroring the existing spec-from-binding family:

```go
// RegisterSocket replays a bound port's SocketPattern against an events
// Builder as an AsyncAPI channel with ws bindings — In frames as the
// publish message, Out frames as the subscribe message.
func RegisterSocket[In, Out any](b *events.Builder, port any) error
```

Open sub-questions: whether the events Builder grows binding support or a
dedicated renderer is cleaner; how upgrade-time PathParams map to AsyncAPI
channel parameters (direct fit); one-directional ports emit only one
message direction.

## Out of scope (will not implement)

- **MQTT-over-WebSocket support in this adapter** — that is a transport
  option of the MQTT client (pass a `ws://` broker URL to paho); accepting
  MQTT clients here would mean implementing an MQTT broker.
- **A universal "StreamPattern"** covering WebSocket + Redis Streams —
  incompatible addressing and delivery semantics; verdict recorded in
  Phase 1 (see `docs/features/websocket.md` and the redis Phase 2 roadmap).
