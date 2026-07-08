# `adapters/tcp` — Raw TCP Adapter

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

---

## Motivation

TCP is the lowest-common-denominator transport: any language, any platform, any environment can speak it. An `adapters/tcp` adapter lets go-codex serve and call typed, codec-validated services over raw TCP sockets — useful for embedded systems, PLCs, legacy devices, and IoT gateways that do not speak MQTT, AMQP, or ZeroMQ.

The adapter follows the same declare → register → handle → adapt workflow as `adapters/zeromq`, the closest existing adapter: both are connection-oriented, broker-free, and point-to-point.

---

## The fundamental challenge: TCP has no message boundaries

TCP is a **byte stream**. Unlike MQTT, AMQP, and ZeroMQ (which all have built-in message framing), TCP delivers bytes with no concept of where one message ends and the next begins.

The adapter solves this by introducing a `FramedConn` interface — analogous to ZeroMQ's `FramedSocket` — that wraps a `net.Conn` with a message framing layer. A built-in length-prefix framer is provided; users can implement custom framing (newline-delimited, TLV, fixed-size) by satisfying the interface.

```
TCP byte stream:   [...payload1...][...payload2...]  ← no boundaries
Length-prefix:     [4-byte len][payload1][4-byte len][payload2]  ← framed
```

---

## Comparison with ZeroMQ adapter

| Aspect | ZeroMQ (`adapters/zeromq`) | TCP (`adapters/tcp`) |
|---|---|---|
| Transport abstraction | `FramedSocket` (multi-frame `[][]byte`) | `FramedConn` (single-frame `[]byte`) |
| Framing | Built into ZMQ protocol | User-provided; `NewLengthPrefixConn` as default |
| Server setup | User creates ZMQ socket | Adapter wraps `net.Listener` |
| Client setup | User creates ZMQ socket | Adapter wraps `net.Conn` via `net.Dial` |
| Poll interval | `SetRecvTimeout` + `ErrTimeout` loop | Blocking `io.Read` — ctx cancellation via `SetDeadline` |
| Routing | ZMQ socket type (REQ/REP, PUB/SUB, DEALER/ROUTER) | None — point-to-point, single connection |
| Pub/Sub | ✅ via SUB socket | ❌ — TCP is point-to-point; streaming only |

---

## `FramedConn` — the transport abstraction

```go
// FramedConn abstracts a framed TCP connection.
// Each ReadFrame call returns exactly one complete message;
// each WriteFrame call sends exactly one complete message.
//
// Implement this interface to support custom wire framing
// (newline-delimited, TLV, fixed-size, etc.).
// Use [NewLengthPrefixConn] for the built-in 4-byte length-prefix framing.
type FramedConn interface {
    // ReadFrame reads the next complete message frame.
    // Blocks until a frame arrives or an error occurs.
    // Returns (nil, io.EOF) when the remote end closes the connection.
    ReadFrame() ([]byte, error)

    // WriteFrame sends one complete message frame.
    WriteFrame(data []byte) error

    // SetDeadline sets the read and write deadlines (used for ctx cancellation polling).
    SetDeadline(t time.Time) error

    // Close closes the underlying connection.
    Close() error
}

// NewLengthPrefixConn wraps a net.Conn with 4-byte big-endian length-prefix framing.
// Wire format: [uint32 big-endian length][payload bytes...]
// This is the recommended default framing for new TCP services.
func NewLengthPrefixConn(conn net.Conn) FramedConn
```

The design mirrors ZeroMQ's `FramedSocket` but simplified: TCP messages are single-payload (not multi-frame), so `ReadFrame`/`WriteFrame` use `[]byte`, not `[][]byte`.

---

## Supported patterns

TCP is point-to-point. Two patterns are supported:

| Pattern | API | API layer | Use case |
|---|---|---|---|
| **Request/Reply** (server) | `Serve[Req, Resp]` | `api/reqreply` | Typed RPC — one response per request |
| **Request/Reply** (client) | `Call[Req, Resp]` | `api/reqreply` | Call a remote typed service |
| **Stream** (push) | `Push[T]` | `api/events` | Server pushes messages to a connected client |
| **Stream** (pull) | `Pull[T]` | `api/events` | Client receives a stream of messages |

> **No Pub/Sub:** TCP has no broker, no topic routing, and no fan-out. Pub/Sub over TCP would require an application-level message router — that is out of scope for this adapter. For fan-out patterns, use `adapters/mqtt` or `adapters/amqp`.

---

## API surface

### Request/Reply

```go
// Serve accepts connections from listener and handles each in its own goroutine.
// For each connection:
//   1. ReadFrame → decode as Req
//   2. Call fn(ctx, req) → Resp
//   3. Encode Resp → WriteFrame
//   4. Close the connection (one request per connection model)
//
// The loop runs until ctx is cancelled (returns nil) or a listener error occurs.
// Run Serve in a dedicated goroutine.
func Serve[Req, Resp any](
    ctx context.Context,
    ln net.Listener,
    framer func(net.Conn) FramedConn, // e.g. NewLengthPrefixConn
    handle *reqreply.RouteHandle[Req, Resp],
    fn func(context.Context, Req) (Resp, error),
    opts ServeOptions,
) error

// Call dials addr, creates a FramedConn, sends the request, and reads the reply.
// The connection is closed after each call (stateless).
func Call[Req, Resp any](
    ctx context.Context,
    addr string,
    framer func(net.Conn) FramedConn,
    handle *reqreply.RouteHandle[Req, Resp],
    req Req,
    opts CallOptions,
) (Resp, error)

type ServeOptions struct {
    OnError func(ServeError)
    Observer stats.Observer
}

type CallOptions struct {
    Observer stats.Observer
    Timeout  time.Duration    // default 30s; covers dial + write + read
    Vars     map[string]string
}
```

#### One request per connection vs persistent connections

The default model is **one request per connection**: dial → send → receive → close. This is the simplest model, matching `zeromq.Call`'s semantics exactly.

A persistent-connection variant (keep-alive, pipelining) is deferred to Phase 2 — it requires connection pool management, which significantly increases complexity.

### Streaming (Push / Pull)

```go
// Push sends a stream of messages to addr.
// Each call to Push opens a connection, sends one message, and closes.
// For a persistent stream (many messages on one connection), see Phase 2.
func Push[T any](
    ctx context.Context,
    addr string,
    framer func(net.Conn) FramedConn,
    handle *events.ChannelHandle[T],
    msg T,
    opts PushOptions,
    formats ...format.Format[T],
) error

// Pull connects to addr and blocks, receiving frames and calling fn for each.
// The loop runs until ctx is cancelled or the connection closes.
// Run Pull in a dedicated goroutine.
func Pull[T any](
    ctx context.Context,
    addr string,
    framer func(net.Conn) FramedConn,
    handle *events.ChannelHandle[T],
    fn func(context.Context, T) error,
    opts PullOptions,
    formats ...format.Format[T],
) error

type PushOptions struct {
    Observer stats.Observer
    Timeout  time.Duration
}

type PullOptions struct {
    OnError  func(PullError)
    Observer stats.Observer
}
```

---

## Structured errors (all implement `slog.LogValuer`)

```go
type ErrorKind int
const (
    KindDecode  ErrorKind = iota // payload could not be decoded
    KindHandler                  // application handler returned error
    KindEncode                   // response encoding failed
    KindTimeout                  // no reply within deadline
)

// ServeError — per-connection failure on the server side.
type ServeError struct {
    Kind       ErrorKind
    RemoteAddr string  // conn.RemoteAddr().String()
    Err        error
}
// LogValue emits: {kind, remote_addr, err}

// CallError — caller-side failure: dial, write, read, decode, timeout.
type CallError struct {
    Kind ErrorKind
    Addr string
    Err  error
}
// LogValue emits: {kind, addr, err}

// PullError — per-message failure in the Pull loop.
type PullError struct {
    Kind       ErrorKind
    RemoteAddr string
    Err        error
}
// LogValue emits: {kind, remote_addr, err}

// ConnError — connection-level failure: dial failure, accept failure, frame read/write.
// Distinct from codec-level errors (ServeError, CallError).
type ConnError struct {
    Op  string // "dial" | "accept" | "read_frame" | "write_frame"
    Err error
}
// LogValue emits: {op, err}
```

`ConnError` mirrors `BrokerError` (AMQP) and `SocketError` (ZeroMQ) — it distinguishes transport failures from codec failures.

---

## Frame protocol: `NewLengthPrefixConn`

```
Wire format:
  ┌──────────────────────────┬─────────────────────────────────────────┐
  │ Length (4 bytes, big-end) │            Payload (N bytes)            │
  └──────────────────────────┴─────────────────────────────────────────┘
```

- `ReadFrame`: reads 4-byte header → allocates `[N]byte` → `io.ReadFull`
- `WriteFrame`: writes 4-byte length → writes payload
- Max frame size: `math.MaxUint32` (~4 GiB; configurable via `NewLengthPrefixConnWithMaxSize`)
- `SetDeadline` delegates directly to the underlying `net.Conn`

Custom framers (e.g. newline-delimited for text protocols, TLV for binary protocols) satisfy `FramedConn` directly — no adapter changes required.

---

## Observer integration

```
Request/Reply:
  RecordRequest("TCP-REP", handle.Topic, 200/0, dur)    — Serve: per connection handled
  RecordRequest("TCP-REQ", handle.Topic, 200/0, dur)    — Call: per call made
  RecordValidationError("body", constraint, field)      — per codec field failure

Streaming:
  RecordSubscribe(handle.Topic, success, dur)            — Pull: per frame received
  RecordPublish(handle.Topic, success, dur)              — Push: per frame sent
```

Trace operations: `"tcp.serve"`, `"tcp.call"`, `"tcp.pull"`, `"tcp.push"`.

---

## AsyncAPI spec

AsyncAPI 3.0 supports TCP as a server protocol:

```yaml
servers:
  production:
    host: "192.168.1.100:5555"
    protocol: tcp
    description: "Raw TCP endpoint with length-prefix framing."
```

No channel bindings are defined for TCP in the AsyncAPI binding specs (unlike AMQP). The channel `address` is used for observer reporting only — TCP routing is address-based, not topic-based.

---

## Files to create

| File | Contents |
|---|---|
| `adapters/tcp/conn.go` | `FramedConn` interface, `NewLengthPrefixConn`, `ConnError` |
| `adapters/tcp/adapter.go` | `Serve`, `Call`, `Push`, `Pull`, options structs |
| `adapters/tcp/errors.go` | `ServeError`, `CallError`, `PullError`, `ConnError`, `ErrorKind` |
| `adapters/tcp/doc.go` | Package overview |
| `adapters/tcp/conn_test.go` | `FramedConn` + `NewLengthPrefixConn` tests (pipe-based) |
| `adapters/tcp/adapter_test.go` | `Serve`/`Call`/`Push`/`Pull` tests (in-memory `net.Pipe`) |

No external dependencies — the adapter uses only Go stdlib (`net`, `encoding/binary`, `io`, `context`).

---

## Usage sketch

### Request/Reply

```go
import (
    "net"
    tcpadapter "github.com/DaniDeer/go-codex/adapters/tcp"
    "github.com/DaniDeer/go-codex/api/reqreply"
)

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute.add", computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
)

b := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
b.AddServer("tcp", reqreply.Server{URL: "tcp://localhost:5555", Protocol: "tcp"})
handle, _ := ComputeRoute.Register(b)

// Server
ln, _ := net.Listen("tcp", ":5555")
go tcpadapter.Serve(ctx, ln, tcpadapter.NewLengthPrefixConn, handle,
    func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
        return ComputeResp{Sum: req.X + req.Y}, nil
    },
    tcpadapter.ServeOptions{Observer: obs},
)

// Client
resp, err := tcpadapter.Call(ctx, "localhost:5555",
    tcpadapter.NewLengthPrefixConn, handle,
    ComputeReq{X: 3, Y: 4},
    tcpadapter.CallOptions{Timeout: 5 * time.Second, Observer: obs},
)
// resp.Sum == 7
```

### Streaming

```go
var SensorChannel, _ = events.NewChannel[SensorReading](
    "sensor.readings", sensorCodec,
    events.Subscribe{Summary: "Sensor readings pushed from a device."},
).Register(eventsBuilder)

// Pull — device connects and pushes; service receives
go tcpadapter.Pull(ctx, "device-gateway:6000",
    tcpadapter.NewLengthPrefixConn, SensorChannel,
    func(ctx context.Context, r SensorReading) error {
        return store.Save(ctx, r)
    },
    tcpadapter.PullOptions{Observer: obs},
)
```

---

## Phase 2 — persistent connections

Phase 1 uses one-request-per-connection semantics (simplest, no state). Phase 2 would add:

- **`ServeKeepAlive`** — persistent connection server with concurrent request dispatch
- **`Pool`** — connection pool for `Call` (avoid dial overhead on high-frequency RPC)
- **`Stream`** — bidirectional streaming on one persistent connection

These require connection lifecycle management and are out of scope for Phase 1.

---
