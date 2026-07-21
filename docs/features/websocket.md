# WebSocket Adapter — `adapters/websocket`

> See also: [`adapters/websocket` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/websocket) · [Ports — Protocol-Agnostic Wiring](ports.md) · [Metrics Observer](observer.md) · [Error Handling](error-handling.md)
>
> Runnable demo: [`examples/websocket-duplex`](https://github.com/DaniDeer/go-codex/tree/main/examples/websocket-duplex) — a `DuplexPort` served over a real loopback WebSocket: typed commands in, targeted typed replies out, `app`-supervised lifecycle, observer metrics (upgrade/frame counts + validation failures) injected once via `app.Options.Observer`.

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
| `CustomFormat` | Escape hatch for binary/custom frame formats (Gob, protobuf, …) — a pre-built `format.Format[T]`, overrides `Format` when non-nil. Applies to whichever side(s) carry the real payload type; the unused `struct{}` side of a one-directional port is unaffected. See [`ports.FilePattern.CustomFormat`](ports.md#filepattern--typed-files-as-sink-or-intermediate-io) for the full contract |
| `Opts` | `rest.RouteOpt` entries — `PathParam{...}.WithCodec(...)` etc., upgrade-time |
| `InOpts` | `ports.SocketInOpt` entries (`NewRequiredSocketInParam` / `NewOptionalSocketInParam`) — merge connection vars into inbound payloads |
| `OutOpts` | `ports.SocketOutOpt` entries (`NewRequiredSocketOutParam` / `NewOptionalSocketOutParam`) — merge connection vars into outbound payloads |

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

## One struct, one call with connection vars

WebSocket now supports declare-once merge fields for both directions. Add
`InOpts` and/or `OutOpts` on `SocketPattern`; adapters then merge
connection vars (path/query/header from upgrade request) into payload structs
automatically on each frame:

```go
patterns := []ports.Pattern{
    ports.SocketPattern{
        Path: "/live/{room}",
        Opts: []rest.RouteOpt{
            rest.PathParam{Name: "room"}.WithCodec(codex.String()),
        },
        InOpts: []ports.SocketInOpt{
            ports.NewRequiredSocketInParam("room", codex.String(),
                func(c Command) string { return c.Room },
                func(c *Command, v string) { c.Room = v }),
        },
        OutOpts: []ports.SocketOutOpt{
            ports.NewRequiredSocketOutParam("room", codex.String(),
                func(u Update) string { return u.Room },
                func(u *Update, v string) { u.Room = v }),
        },
    },
}
```

Inbound decode and outbound encode keep the same escape hatch: if no
`InOpts`/`OutOpts` are declared, payloads stay untouched.
`examples/websocket-duplex` shows both approaches side-by-side:
auto-merged room via `InOpts`/`OutOpts` and manual `hub.SessionInfo(session)`
lookup.

---

## Client-side — dial adapters

The `Dial*` family connects OUT to an external WebSocket endpoint (another
go-codex service, a partner API, a feed) and bridges it into the same ports:

| Constructor | Port | Use |
|---|---|---|
| `DialSourceAdapter[T](dialer, baseURL, vars, handle, opts)` | `SourcePort` | consume an external feed |
| `DialSinkAdapter[T](…)` | `SinkPort` | publish outbound frames |
| `DialDuplexAdapter[In,Out](…)` | `DuplexPort` | full duplex client |

`NewDialer(DialerOptions{…})` is the gorilla dial shim (same keepalive story
as the server side); the URL is `baseURL` + the handle's path template
expanded with `vars` (declared `PathParam` codecs validate each value).

**Reconnect semantics — no silent loss (by design):**

- Auto-reconnect with exponential backoff (250ms → 30s cap, reset after a
  connection that delivered traffic).
- EVERY failed dial and EVERY drop emits a `SocketError` (`Op` `"dial"` /
  `"read"`) on the port's Errors channel — consumers KNOW a gap happened
  and frames may have been missed.
- The session generation (`c1`, `c2`, …) advances per connection — a
  generation change in inbound frames is the visible reconnect marker.
- Outbound frames while the connection is down are DROPPED with
  `ErrFrameDropped` (consistent with the server slow-client policy) —
  including during initial connection establishment: pump or buffer
  upstream if the first frames matter.

---

## AsyncAPI spec — `ports.RegisterSocket`

OpenAPI cannot express WebSocket frames; **AsyncAPI can**. `RegisterSocket`
replays a port's `SocketPattern` against an `events.Builder` as a channel:

```go
b := events.NewBuilder(events.Info{Title: "Live Ops Socket", Version: "1.0.0"})
b.AddServer("prod", events.Server{URL: "live.example.com", Protocol: "ws"})
_ = ports.RegisterSocket[Command, Update](b, Live)
doc, _ := b.AsyncAPISpec()
```

- Channel name = the socket path template; `{var}` placeholders become
  channel parameters.
- Subscribe operation = frames the application RECEIVES (`In`); Publish
  operation = frames it SENDS (`Out`). One-directional ports emit only
  their live direction (the `struct{}` side is skipped).
- Built on `events.Builder.AddChannelItem` — the escape hatch for channels
  whose two directions carry different payload types.

(Supplying `PortOptions.RESTBuilder` still incidentally documents the
upgrade route in OpenAPI as a bare GET — harmless endpoint documentation,
nothing about frames.)

---

## chi variants

`adapters/chi` mirrors all three server adapters with chi-safe registration
(`chi.IngestSocketAdapter` / `chi.BroadcastSocketAdapter` /
`chi.DuplexSocketAdapter`): the swap handler is registered at CONSTRUCTOR
time (chi's Mux cannot register routes while serving) and the real handler
installs atomically at `Activate` — requests before that get 503. Behaviour
is identical; the implementations delegate to the websocket package.

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

Server-side + client-side + chi + AsyncAPI spec are shipped. Still
deferred (awaiting use cases): a `ConnectionObserver` stats extension and
dynamic subprotocol negotiation — recorded in the
[deferred roadmap](../roadmap/websocket-deferred.md).
