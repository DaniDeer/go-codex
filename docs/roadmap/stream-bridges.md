# Stream Bridge Helpers — Remaining Work

**Status:** Mostly implemented. Three gaps remain.

> **Implemented features** are documented in the user guide:
> [Stream Bridge Guide](../guides/stream-bridges.md)

---

## What has been implemented

All bridge helpers from the original design are shipped **except** those listed in
the remaining work section below.

| Adapter | Implemented |
|---------|-------------|
| `stream` | `Single[T]` |
| `adapters/zeromq` | `SubscribeStream`, `DrainPublish`, `AsPipelineFunc`, `CallStream`, `ServeLatest` |
| `adapters/nethttp` | `HandlerLatest` / `RegisterLatest`, `HandlerIngest` / `RegisterIngest`, `PipelineHandler` / `RegisterPipeline` |
| `adapters/chi` | Same as nethttp |
| `adapters/mqtt` | `SubscribeStream`, `DrainPublish` |
| `adapters/mqtt5` | `SubscribeStream`, `DrainPublish`, `AsPipelineFunc`, `CallStream` |
| `adapters/mcpgo` | `ToolLatestHandler` / `RegisterToolLatest`, `ToolPipelineHandler` / `RegisterToolPipeline` |
| `adapters/sql` | `QueryStream`, `DrainInsert` |
| `adapters/file` | `ScanStream`, `WatchStream`, `DrainWrite` |

---

## Remaining work

### Gap 1 — `stream.BroadcastHub[T]` (prerequisite)

**Status:** Deferred — no implementation yet.

An N-subscriber fan-out primitive. Every subscriber gets its own buffered channel;
the hub fans out each item from the source stream to all subscribers independently.
Slow subscribers apply backpressure only to themselves.

```go
// stream/broadcast.go (new file)
type BroadcastHub[T any] struct { ... }

func NewBroadcastHub[T any](ctx context.Context, src Stream[T], bufPerSubscriber int) *BroadcastHub[T]
func (h *BroadcastHub[T]) Subscribe() Stream[T]
func (h *BroadcastHub[T]) Unsubscribe(s Stream[T])
```

**Used by:** `SSEFromHub` (Gap 2), future `GroupBy` operator.

**Design notes:**
- Each subscriber receives a `Stream[T]` with its own `Values` and `Errors` channels.
- The hub goroutine reads from src and fans out to all subscriber channels.
- When src closes, the hub closes all subscriber channels.
- Slow subscribers: non-blocking fan-out with a configurable overflow policy (drop or block).

---

### Gap 2 — HTTP SSE bridges (nethttp + chi)

**Status:** Deferred — error types exist, implementations missing.

Two server-side patterns and one client-side consumer:

#### `SSEFromStream` / `SSEFromHub` (server-side) — both nethttp and chi

```go
// adapters/nethttp/stream.go (additions)

// SSEFromStream: each SSE client gets its own stream from streamFactory.
func SSEFromStream[Req, Event any](
    streamFactory func(context.Context, Req) Stream[Event],
    opts SSEStreamOptions,
) SSEHandlerFunc[Req, Event]

// SSEFromHub: all SSE clients share one BroadcastHub. Depends on Gap 1.
func SSEFromHub[Req, Event any](
    hub *stream.BroadcastHub[Event],
    opts SSEStreamOptions,
) SSEHandlerFunc[Req, Event]

type SSEStreamOptions struct {
    OnError  func(error)   // called for SSEWriteError (client disconnected)
    Observer stats.Observer
}
```

Error type already exists: `SSEWriteError{Path, Err}` (implements `slog.LogValuer`).

`SSEFromStream` can be implemented independently of BroadcastHub.
`SSEFromHub` depends on Gap 1 (`BroadcastHub`).

#### `SSEClientStream` (client-side consume) — nethttp only

```go
// adapters/nethttp/stream.go (addition)

// SSEClientStream connects to an external SSE endpoint and emits each decoded event.
// Reconnects automatically with exponential backoff on connection drop.
func SSEClientStream[Event any](
    ctx context.Context,
    client *http.Client,
    handle *rest.SSERouteHandle[struct{}, Event],
    fmt format.Format[Event],
    opts SSEClientOptions,
) Stream[Event]

type SSEClientOptions struct {
    RetryDelay    time.Duration // initial reconnect wait (default 1s)
    MaxRetryDelay time.Duration // cap (default 30s)
    Observer      stats.Observer
    Buffer        int
}
```

Error types already exist: `SSEConnectError{URL, Attempt, Err}`, `SSEParseError{URL, Line, Err}`.

#### `PollStream` and `DrainCall` (HTTP polling + client sink) — nethttp only

```go
// PollStream polls a route at interval, emitting each response as a stream item.
func PollStream[Req, Resp any](
    ctx context.Context,
    client *http.Client,
    handle *rest.RouteHandle[Req, Resp],
    req Req,
    interval time.Duration,
    opts PollStreamOptions,
) Stream[Resp]

// DrainCall POSTs each stream item to handle, discarding the response.
func DrainCall[Req, Resp any](
    ctx context.Context,
    client *http.Client,
    handle *rest.RouteHandle[Req, Resp],
    src Stream[Req],
    opts DrainCallOptions,
)
```

---

### Gap 3 — `zeromq.CallDealerStream`

**Status:** Deferred — `CallStream` (REQ socket, sequential) is implemented.

Concurrent DEALER socket request streaming with sequence-number correlation.
Each outgoing request frame is tagged with a uint64 sequence number; responses
are matched by sequence number, allowing multiple in-flight requests.

```go
// adapters/zeromq/stream.go (addition)

type CallDealerStreamOptions struct {
    MaxInFlight int            // concurrent in-flight requests (default 16)
    Observer    stats.Observer
    Buffer      int
}

func CallDealerStream[Req, Resp any](
    ctx context.Context,
    sock FramedSocket,
    handle *reqreply.RouteHandle[Req, Resp],
    src Stream[Req],
    opts CallDealerStreamOptions,
) Stream[Resp]
```

Error type already exists: `CorrelationError{Seq uint64, Err error}` (implements `slog.LogValuer` + `Unwrap()`).

**Implementation notes:**
- Send goroutine: reads from src.Values, adds sequence tag `[seq_bytes, payload]`, sends.
- Receive goroutine: reads replies `[seq_bytes, status, payload]`, looks up pending request by seq.
- Correlation map: `map[uint64]chan<- result[Resp]` protected by mutex.
- Unmatched reply: emits `CorrelationError{Seq: seq}` to Stream.Errors.

---

## Implementation order (when prioritised)

| Order | Item | Dependency |
|-------|------|------------|
| 1 | `SSEFromStream` (nethttp + chi) | None — no BroadcastHub needed |
| 2 | `SSEClientStream` (nethttp) | None |
| 3 | `PollStream`, `DrainCall` (nethttp) | None |
| 4 | `stream.BroadcastHub[T]` | None (but blocks SSEFromHub) |
| 5 | `SSEFromHub` (nethttp + chi) | BroadcastHub (item 4) |
| 6 | `zeromq.CallDealerStream` | None |

`SSEFromStream`, `SSEClientStream`, `PollStream`, `DrainCall`, and `CallDealerStream`
can all be implemented independently without BroadcastHub.
