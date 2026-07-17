# WebSocket Adapter — `adapters/websocket`

> See also: [`adapters/websocket` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/websocket) · [Ports — Protocol-Agnostic Wiring](ports.md) · [Metrics Observer](observer.md) · [Error Handling](error-handling.md)
>
> Runnable demo: [`examples/websocket-duplex`](https://github.com/DaniDeer/go-codex/tree/main/examples/websocket-duplex) — a `DuplexPort` served over a real loopback WebSocket: typed commands in, targeted typed replies out, `app`-supervised lifecycle.

`adapters/websocket` serves **typed, codec-validated frame streams** over
persistent bidirectional connections — completing the HTTP story
(`IngestAdapter` = client→pipeline, `SSEAdapter` = pipeline→client,
WebSocket = both at once). Endpoints are declared once with
`ports.SocketPattern`; every frame passes through the port's codec.

---

## Delivery semantics (read first)

| Property | WebSocket (this adapter) | MQTT | SSE |
|---|---|---|---|
| Delivery | at-most-once, fire-and-forget | QoS 0/1/2 | at-most-once |
| Retained / replay | none | retained messages | none |
| Offline clients | frames LOST | QoS 1+ queued | lost |
| Direction | bidirectional | bidirectional (broker) | server → client only |

- A client that is offline or reconnecting **loses frames**; a late joiner
  sees nothing until the next frame. Pair with a `LatestPort` when "current
  state on connect" matters.
- **Slow clients**: each session has a buffered outbound queue (default 16).
  A full queue **drops the frame for that session only** — reported as a
  `SocketError` wrapping `ErrFrameDropped` — so one lagging browser tab
  never blocks the pipeline or other sessions.

---

## Declare the endpoint — `ports.SocketPattern`

```go
var Live = codex.Must(ports.NewDuplexPort[Command, Update]("live",
    commandCodec, updateCodec, ports.PortOptions{
        Patterns: []ports.Pattern{ports.SocketPattern{Path: "/live/{room}"}},
        Buffer:   8,
    }))
```

| Field | Meaning |
|---|---|
| `Path` | HTTP upgrade path template (`{var}` placeholders, validated once per connection through the rest machinery) |
| `Subprotocols` | Acceptable `Sec-WebSocket-Protocol` values (empty = any) |
| `Format` | Frame wire format from the port's codec: JSON (default), YAML, TOML |
| `Opts` | `rest.RouteOpt` entries — `PathParam{...}.WithCodec(...)` etc., upgrade-time |

Port-type acceptance: `SourcePort` (inbound-only), `SinkPort`
(broadcast-only — the WS sibling of SSE), `DuplexPort` (full duplex).
Rejected on `IOPort`/`LatestPort`/`ToolPort` — per-message request/reply
over a socket is an RPC discipline (`ReqReplyPattern` territory).

---

## Adapters

| Constructor | Port | What it does |
|---|---|---|
| `IngestSocketAdapter[T](mux, hub, upgrader, handle, opts) ports.SourceAdapter[T]` | `SourcePort` | Inbound-only: frames from ALL connected clients feed the port |
| `BroadcastSocketAdapter[T](mux, hub, upgrader, handle, opts) ports.SinkAdapter[T]` | `SinkPort` | Broadcast-only: every port item pushed to all clients |
| `DuplexSocketAdapter[In,Out](mux, hub, upgrader, handle, opts) ports.DuplexAdapter[In,Out]` | `DuplexPort` | Full duplex: session-tagged inbound + targeted/broadcast outbound |

All three share:
- **Hub** (`NewHub(buffer)`) — the session registry, constructed in main and
  passed to the adapter. `hub.SessionInfo(session)` exposes upgrade-time
  path vars (which `{room}` a session joined); `hub.Sessions()` lists peers.
- **Upgrader** (`NewUpgrader(UpgraderOptions{...})`) — the gorilla shim.
  Keepalive is adapter-owned: ping every 30s, pong wait 60s, 1 MiB read
  limit (override via `PingInterval`/`ReadLimit`; `CheckOrigin` for CORS).
- Frame decode failures go to the port's Errors channel — **the connection
  stays open**; one bad frame does not disconnect a client.

### Targeted replies (DuplexPort)

```go
replies := stream.Map(ctx, Live.Inbound(ctx),
    func(f ports.Framed[Command]) (ports.Framed[Update], error) {
        return ports.Framed[Update]{
            Session: f.Session,           // reply to the sender only
            Payload: process(f.Payload),
        }, nil
    }, stream.MapOptions{Name: "ack"})
go Live.Feed(ctx, replies)                // zero Session = broadcast
```

---

## Narrow client interface

Adapters accept `Upgrader`/`Socket` — small interfaces — never a gorilla
type. `NewUpgrader` adapts [gorilla/websocket](https://github.com/gorilla/websocket)
(already an indirect dependency via mcp-go — zero new modules); `socket.go`
is the only file importing it. Unit tests run against hand-written fakes.

---

## Errors

```go
type SocketError struct {
    Path    string        // declared path template
    Session ports.Session // empty for upgrade failures
    Op      string        // "upgrade", "read", "write", "close"
    Err     error
}
var ErrFrameDropped = ...  // sentinel inside SocketError for slow-client drops
```

`Error()`, `Unwrap()`, `slog.LogValuer` — `errors.Is(err, ErrFrameDropped)`
works through the chain. Frame validation failures wrap the codec chain
(per-field observer reports, location `"payload"`).

---

## Observer

No new stats extension — the transport-agnostic hooks fit:

- `RecordRequest("GET", path, 101|4xx, dur)` once per upgrade attempt
- `RecordSubscribe(path, success, dur)` per inbound frame
- `RecordPublish(path, success, dur)` per outbound frame
- Path-var failures → `RecordValidationError("path", …)`; frame validation
  → `"payload"`. Nil observer resolves from ctx.

---

## Not MQTT-over-WebSocket

MQTT clients connecting via `ws://` tunnel MQTT frames inside WebSocket —
a transport option of the MQTT **client**, already supported by passing a
`ws://` broker URL to paho (`adapters/mqtt`/`adapters/mqtt5`). This adapter
is go-codex itself as the WS server speaking its own typed frames; it is
not an MQTT broker.

## Scope

Server-side only. Client-side WS (dialing external feeds), chi variants,
and a `ConnectionObserver` extension are recorded in the
[Phase 2 roadmap](../roadmap/websocket-phase2.md).
