# `adapters/websocket` — Server-Side WebSocket Adapter + Duplex Ports

> **Status:** Design evaluation — pattern/port-type decision drafted, awaiting review.
> [← Back to Roadmap](index.md)

## Motivation

The HTTP story today is one-directional per endpoint: `IngestAdapter`
(SourcePort, client → pipeline) and `SSEAdapter` (SinkPort, pipeline →
client). WebSocket completes it with a **bidirectional, persistent**
connection: live dashboards that also send commands, device gateways that
push telemetry AND receive setpoints, collaborative UIs. The same evaluation
was triggered by Redis Streams (Phase 3 of the redis adapter): "streaming
boundary" kept coming up — this doc settles what go-codex's declaration
surface for streaming boundaries should be.

## Scope decisions

| In scope (Phase 1) | Out of scope |
|---|---|
| SERVER-side WS: accept connections on an HTTP path (nethttp `mux` + chi) | CLIENT-side WS (dial out to external feeds) → Phase 2 |
| `IngestSocketAdapter` (SourcePort — inbound frames from all clients) | Per-message compression tuning, custom ping/pong policies (defaults only) |
| `BroadcastSocketAdapter` (SinkPort — fan-out to all clients, WS sibling of SSE) | WS subprotocol negotiation beyond a static declared list |
| **`DuplexPort[In,Out]` — new sixth port type** + `DuplexSocketAdapter` | Binary vs text auto-detection (declared per pattern, not sniffed) |
| **`SocketPattern`** — new Pattern for path-addressed duplex sockets | A universal "StreamPattern" covering Redis Streams too — see verdict below |

## Not this doc: MQTT-over-WebSocket

MQTT clients that connect via `ws://` tunnel MQTT protocol frames inside WS
binary frames — that is a **transport option of the MQTT client**, already
supported today with zero go-codex changes: pass a `ws://host:9001/mqtt`
broker URL to paho (`adapters/mqtt`) or a WS dialer to paho.golang
(`adapters/mqtt5`) at client construction in main.go. The adapters, the
`EventPattern` declarations, and the port bindings never see the transport.
This roadmap is the opposite layer: go-codex ITSELF as the WS server,
speaking its own codec-validated typed frames. Accepting MQTT clients on
that server would mean implementing an MQTT broker — permanently out of
scope.

## The streaming-pattern verdict (the question this doc answers)

**One universal streaming pattern is the wrong abstraction.** The two
"streaming" boundaries that motivated this evaluation have incompatible
addressing and delivery semantics:

| | WebSocket | Redis Streams |
|---|---|---|
| Addressing | HTTP path (`/live/{room}`) | stream KEY (`events:{tenant}`) + consumer group |
| Connection | persistent duplex socket per client | client polls/blocks on a shared log |
| Delivery | at-most-once, per-connected-client | at-least-once, XACK, replay from ID |
| Natural port | duplex (or Source+Sink pair) | SourcePort with ack semantics / SinkPort append |

Verdict:
- **WebSocket → new `SocketPattern`** (below). Reusing `RESTPattern` is not
  possible: its role meanings are already taken (`SourcePort`+REST = HTTP
  ingest, `SinkPort`+REST = SSE) — overloading the same pattern with a third
  role-dependent meaning would make declarations ambiguous.
- **Redis Streams → NOT this pattern.** Key-addressed, persistent,
  ack-driven — it will want an `EventPattern`-or-own declaration with
  consumer-group options when the redis Phase 3 roadmap lands. Recording
  this here prevents a future "make SocketPattern fit XREADGROUP" mistake.

## New Pattern — `SocketPattern`

```go
// SocketPattern declares a path-addressed duplex socket endpoint for a port
// bound to a websocket adapter. Path mirrors RESTPattern.Path (same {var}
// placeholders, validated with the same machinery).
//
//	ports.SocketPattern{Path: "/live/{room}", Format: ports.FileFormatJSON}
type SocketPattern struct {
    // Path is the HTTP upgrade path template (e.g. "/live/{room}").
    Path string
    // Subprotocols lists acceptable Sec-WebSocket-Protocol values.
    // Empty = accept any.
    Subprotocols []string
    // Format selects the frame wire format applied to the port's codec(s):
    // JSON (default), YAML, TOML — same enum and reasoning as FilePattern.
    Format FileFormatKind
    // Opts carries rest.RouteOpt path params etc. for the upgrade request
    // (PathParam, HeaderParam, security requirements — validated at upgrade
    // time, once per connection, not per frame).
    Opts []rest.RouteOpt
}
```

Port-type acceptance:

| Port type | Meaning | Codec(s) used |
|---|---|---|
| `SourcePort[T]` | inbound-only socket (clients send, server never pushes) | payload codec decodes frames |
| `SinkPort[T]` | broadcast-only socket (WS sibling of SSE) | payload codec encodes frames |
| `DuplexPort[In,Out]` | full duplex | In decodes inbound, Out encodes outbound |
| `IOPort`, `LatestPort`, `ToolPort` | **rejected** (`PatternRegisterError{Kind:"socket"}`) — per-message request/reply over WS is an RPC discipline (correlation IDs), which is `ReqReplyPattern` territory, not a socket property |

Handle: `SocketHandle[In, Out]` (upgrade-request validator from `Opts` +
`format.Format[In]`/`format.Format[Out]` + path template), accessor
`ports.SocketHandle[In, Out](port) (SocketHandle[In, Out], bool)`.
One-directional ports use `struct{}` for the unused side (the established
`SSERouteHandle[struct{}, T]` convention).

## New port type — `DuplexPort[In, Out]` (full evaluation)

### Alternatives considered

**(a) Composition: SourcePort[In] + SinkPort[Out] sharing one endpoint.**
Two ports declared separately, both bound to adapters that share a
connection registry (a `*websocket.Hub` constructed in main). Works with
zero ports changes — but: the shared-hub plumbing is exactly the
protocol-specific wiring ports exist to hide; the two declarations can
drift (different paths for one socket); and **targeted replies are
impossible** — the SinkPort broadcast has no session identity, so
"answer the client that sent this" cannot be expressed. Kept as the
fallback if DuplexPort is rejected in review; broadcast-only use cases can
use plain `SinkPort` + `SocketPattern` without waiting for DuplexPort.

**(b) ToolPort[In,Out] per message.** Forces request/reply correlation onto
a transport that has none — every inbound frame would need exactly one
outbound response. Real WS traffic is asymmetric (N inbound commands, M
outbound updates). Rejected.

**(c) New `DuplexPort[In, Out]` — chosen.** The sixth port type: a typed,
protocol-agnostic **bidirectional session boundary**.

### Sketch

```go
// Session identifies one connected peer for the lifetime of its connection.
type Session string

// Framed pairs a payload with the session it came from / goes to.
type Framed[T any] struct {
    Session Session // zero Session on outbound = broadcast to all
    Payload T
}

// DuplexPort[In, Out] is a typed bidirectional boundary: external peers
// send In frames and receive Out frames over persistent sessions.
type DuplexPort[In, Out any] struct{ ... }

func NewDuplexPort[In, Out any](name string, inCodec codex.Codec[In],
    outCodec codex.Codec[Out], opts PortOptions) (*DuplexPort[In, Out], error)

// Inbound returns the stream of decoded inbound frames (session-tagged).
// Like SourcePort.Stream — errors carry decode/validation failures.
func (p *DuplexPort[In, Out]) Inbound() gstream.Stream[Framed[In]]

// Feed drains src, delivering each frame to its session (or broadcasting
// when Framed.Session is zero). Like SinkPort.Feed — blocks until src ends.
func (p *DuplexPort[In, Out]) Feed(ctx context.Context, src gstream.Stream[Framed[Out]])

// DuplexAdapter is implemented by transport binding constructors
// (websocket.DuplexSocketAdapter; later: tcp, client-side WS).
type DuplexAdapter[In, Out any] interface {
    // Activate runs the endpoint: inbound decoded frames -> dst,
    // outbound frames consumed from outbound(). Must not close dst/errs.
    Activate(ctx context.Context,
        dst chan<- Framed[In], errs chan<- error,
        outbound func() gstream.Stream[Framed[Out]]) error
    AdapterName() string
}
```

Session-routing in the pipeline is exactly what the stream routing
operators already provide: `stream.GroupBy(ctx, port.Inbound(), by-session,
…)` gives per-client sub-streams; `stream.Map` from `Framed[In]` to
`Framed[Out]` preserves the session for targeted replies.

### Why a port type and not just a pattern

A pattern declares *addressing*; a port type declares *flow shape*. Duplex
is a new flow shape (two typed directions, session identity) that none of
the five existing port types can express — the same reason `LatestPort`
became a fifth type instead of an option on `SinkPort`. TCP (existing
roadmap) and client-side WS (Phase 2) reuse `DuplexPort` unchanged — the
port type is transport-agnostic from day one.

## Toolchain decision

**Chosen: `github.com/gorilla/websocket` v1.5.3** — ALREADY in go.sum
(indirect via mcp-go), so it adds zero new modules; unarchived and
maintained since 2023; ubiquitous.

| Rejected | Why |
|---|---|
| `github.com/coder/websocket` (ex-nhooyr) | Nicer context-first API, but a brand-new direct dependency for no capability gain |
| `golang.org/x/net/websocket` | Deprecated, lacks ping/pong control |
| stdlib-only upgrade | Hand-rolling RFC 6455 framing/masking/close handshakes is the TCP-adapter argument inverted — real protocol surface a maintained lib already owns |

**Narrow-interface rule applies**: adapters accept a small `Socket`
interface (`ReadMessage(ctx) ([]byte, error)`, `WriteMessage(ctx, []byte)
error`, `Close(code, reason) error`) + an `Upgrader` interface; the gorilla
shim lives in one file; unit tests use in-memory fakes (`httptest` +
gorilla loopback allowed for one integration-style test, same rule as
nethttp).

## Structured errors

```go
// SocketError wraps a websocket operation failure.
type SocketError struct {
    Path    string // declared path template
    Session Session
    Op      string // "upgrade", "read", "write", "close"
    Err     error
}
```

`Error()`, `Unwrap()`, `LogValue()` as always. Frame decode failures reuse
the codec chain (`ValidationErrors` → port Errors, per-field ReportErrors
location `"payload"`; upgrade-time path/header validation reuses the rest
machinery and its locations).

## Observer integration

No new stats extension for messaging — the transport-agnostic hooks fit:
- `RecordRequest(method, path, status, dur)` once per upgrade (101 or
  rejection status)
- `RecordSubscribe(path, success, dur)` per inbound frame
- `RecordPublish(path, success, dur)` per outbound frame
- Open decision: a `ConnectionObserver` extension (connect/disconnect,
  concurrent-session gauge) — genuinely new lifecycle, but wait for a
  metrics use case before adding a ninth interface.

## Unit test plan (fake Socket/Upgrader; one gorilla loopback test)

| ID | Test | Verifies |
|---|---|---|
| W1 | Upgrade + inbound frame → Inbound() | happy path, session tag present |
| W2 | Upgrade rejection (bad path var / security) | RecordRequest with 4xx, no session |
| W3 | Frame decode failure | Errors channel + "payload" reports; connection STAYS open |
| W4 | Targeted Feed frame reaches only its session | two fake sessions, one addressed frame |
| W5 | Broadcast frame (zero Session) reaches all | fan-out |
| W6 | Slow client policy | per-session buffer full → drop + SocketError (non-blocking fan-out, BroadcastHub precedent) |
| W7 | Client disconnect mid-Feed | frames to dead session dropped with SocketError, others unaffected |
| W8 | ctx cancel closes all sessions | close handshake attempted, Activate returns |
| W9 | SocketError.LogValue / errors.As | KindGroup, all keys; chain reaches inner |
| W10 | SocketPattern acceptance matrix | Source/Sink/Duplex accept; IOPort/LatestPort/ToolPort reject |
| W11 | Observer per path (nil observer safe) | all hooks, both outcomes |
| WL | gorilla loopback round-trip | shim correctness (single integration-style test) |

## Files to create / touch

| File | Responsibility |
|---|---|
| `ports/duplex_port.go` | `DuplexPort`, `DuplexAdapter`, `Framed`, `Session` |
| `ports/pattern.go` / `handle.go` / `pattern_errors.go` | `SocketPattern`, `SocketHandle`, acceptance validation |
| `adapters/websocket/doc.go` | paradigm, port mapping, semantics warnings |
| `adapters/websocket/socket.go` | `Socket`/`Upgrader` narrow interfaces + gorilla shim |
| `adapters/websocket/hub.go` | session registry (register/unregister/targeted/broadcast) |
| `adapters/websocket/binding.go` | `IngestSocketAdapter`, `BroadcastSocketAdapter`, `DuplexSocketAdapter` |
| `adapters/websocket/errors.go` | `SocketError` |
| `adapters/chi/` | chi variants (same swap-handler constructor-time registration — R54) |
| `examples/websocket-duplex/` | fake-backed runnable example |
| docs/features + guides + instructions + review-skill | usual three-surface + R-history sync |

## Open design decisions

1. **Slow-client policy default** — drop-with-error per frame (BroadcastHub
   precedent, leaning) vs disconnect the lagging session (WS norm in some
   ecosystems). Both need the per-session buffer size in `PortOptions` or
   adapter options.
2. **Session metadata** — expose upgrade-time path vars / headers per
   session (e.g. `{room}`) to the pipeline? Leaning: yes, as
   `SessionInfo(Session) (map[string]string, bool)` on the duplex adapter's
   hub — routing by room is the first thing every consumer will want.
3. **Ping/pong & read deadlines** — adapter-owned keepalive defaults
   (gorilla requires explicit pong handlers); which knobs surface in
   options vs stay hardcoded.
4. **`ConnectionObserver`** — see Observer section; wait for a use case.
5. **DuplexPort in `app` supervision** — `Feed` blocks like SinkPort.Feed;
   confirm the `app.Go` wiring story in the example.
