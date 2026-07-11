# Stream Bridge Helpers — Adapter Integration

**Status:** Design complete  
**Package:** `adapters/mqtt`, `adapters/zeromq`, `adapters/nethttp`, `adapters/sql`, `adapters/file` (new)

---

## Motivation

The `stream` package composes reactive pipelines over typed Go channels. Every operator accepts
and returns `stream.Stream[T]{Values <-chan T, Errors <-chan error}`. The adapters (MQTT, ZeroMQ,
nethttp, SQL) are the production sources and sinks — they bring real data into the pipeline and
publish results back out.

Today, the bridge between an adapter and a stream is **manual**: the user creates a `chan []byte`,
wires the adapter's callback to push into it, then wraps with `stream.FromCodec`. This works and is
explicitly documented, but it is boilerplate that every application repeats.

**Goal:** add a thin layer of bridge helpers to each adapter package so that a user can go directly
from adapter → `stream.Stream[T]` or `stream.Stream[T]` → adapter in one call. The bridge helpers
must not introduce new dependencies — they simply wire together existing adapter functions and the
already-imported `stream` package.

---

## The existing channel bridge pattern

This pattern already works today and requires no new code. It is the foundation all bridge helpers
build on:

```go
// Manual bridge: MQTT → stream
rawCh := make(chan []byte, 64)
mqttClient.Subscribe("sensors/+/data", 1,
    mqtt.SubscribeHandler(ctx, sensorHandle,
        func(_ context.Context, raw []byte) error {
            select { case rawCh <- raw: default: } // drop on full buffer
            return nil
        }, mqtt.SubscribeOptions{Observer: obs}))

sensors := stream.FromCodec(ctx, rawCh, format.JSON(sensorCodec),
    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})
```

The bridge helpers described below eliminate the boilerplate while preserving the same semantics.

---

## HTTP request/response bridges (nethttp + chi)

HTTP request/response is synchronous: one request yields exactly one response. Three patterns make
sense as stream bridges:

| Pattern | Rationale |
|---------|-----------|
| `HandlerLatest` — GET returns latest stream value | Reactive cache: continuous pipeline, HTTP polls current state |
| `HandlerIngest` — POST/PUT feeds stream pipeline | Stream source: ingestion endpoint → `stream.From` |
| `PipelineHandler` — handler body implemented with stream operators | Declarative: Tap intermediate stages; same mental model as streaming pipelines |

The fourth possibility — "wrap a plain function call in channels per-request with no observation"
— is **not** a bridge: adds goroutine overhead with zero benefit over a direct function call.

### API shape — all bridge helpers follow the same pattern as `Handler`

For consistency, all three bridge helpers:
- Return `http.Handler` in `nethttp` / `http.HandlerFunc` in `chi` (matching their existing `Handler`)
- Take the same `Options` struct — no new option structs
- Have a `Register*` convenience variant that wires directly onto the mux/router

This mirrors the existing adapter API: `Handler(handle, fn, opts)` / `Register(mux, handle, fn, opts)`.

```
Handler          →  fn: func(ctx, Req)(Resp, error)          → http.Handler / HandlerFunc
HandlerLatest    →  src: stream.Stream[Resp]                  → http.Handler / HandlerFunc
HandlerIngest    →  dst: chan<- Req                           → http.Handler / HandlerFunc
PipelineHandler  →  fn: func(ctx, Req) stream.Stream[Resp]   → http.Handler / HandlerFunc
```

### `HandlerLatest` — reactive cache

Returns the **most recently computed value from a running stream pipeline** as an HTTP response.
A background goroutine reads from `src.Values` and atomically stores each value.

```go
// adapters/nethttp/stream.go
func HandlerLatest[Req, Resp any](
    handle *rest.RouteHandle[Req, Resp],
    src stream.Stream[Resp],
    opts Options, // reuses existing Options (Observer, ErrorHandler, SecurityFunc…)
) http.Handler

func RegisterLatest[Req, Resp any](
    mux *http.ServeMux,
    handle *rest.RouteHandle[Req, Resp],
    src stream.Stream[Resp],
    opts Options,
)

// adapters/chi/stream.go — identical API, returns http.HandlerFunc
func HandlerLatest[Req, Resp any](handle, src, opts) http.HandlerFunc
func RegisterLatest[Req, Resp any](r gochi.Router, handle, src, opts)
```

When no value is available yet, the handler calls `opts.ErrorHandler(w, r, 503, NoLatestValueError{Path: handle.Descriptor.Path})`.
The existing `ErrorHandler` in `Options` controls the response shape — no special option needed.

Errors from `src.Errors` are reported to `opts.Observer` via `stats.ReportErrors` but do not
affect the response (the background goroutine simply skips errored items).

**Usage:**
```go
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})

nethttp.RegisterLatest(mux, oeeHandle, oeeStream, nethttp.Options{Observer: obs})
// GET /oee/current → {"availability":0.94,"performance":0.82,"quality":0.97,"oee":0.75}
// (before first value: 503 with NoLatestValueError body)
```

### `HandlerIngest` — stream source via HTTP

Decodes and validates each POST/PUT request body, then **writes the decoded value to a channel**
feeding a stream pipeline. Response is always the route's configured 2xx status with a `{}`
JSON body (`struct{}` response type).

```go
// adapters/nethttp/stream.go
//
// HandlerIngest decodes and validates each request body, then writes the decoded
// value to dst. If dst is full (non-blocking send fails), the handler calls
// opts.ErrorHandler with 503 and PipelineFullError{Path, Capacity}.
// The caller owns dst — HandlerIngest never closes it.
//
// Configure the route with a 202 Accepted response using a struct{} codec:
//   ingestHandle, _ := rest.NewRoute[SensorReading, struct{}]("POST", "/sensors/readings",
//       readingCodec, codex.Struct[struct{}](), rest.RouteMeta{}).Register(b)
func HandlerIngest[Req any](
    handle *rest.RouteHandle[Req, struct{}],
    dst chan<- Req,
    opts Options,
) http.Handler

func RegisterIngest[Req any](
    mux *http.ServeMux,
    handle *rest.RouteHandle[Req, struct{}],
    dst chan<- Req,
    opts Options,
)

// chi variants return http.HandlerFunc
func HandlerIngest[Req any](handle, dst, opts) http.HandlerFunc
func RegisterIngest[Req any](r gochi.Router, handle, dst, opts)
```

**Usage:**
```go
ingestCh := make(chan SensorReading, 256)

nethttp.RegisterIngest(mux, ingestHandle, ingestCh, nethttp.Options{Observer: obs})
// POST /sensors/readings → 202 {}  (or 503 PipelineFullError if channel full)

sensorStream := stream.From(ctx, ingestCh)
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})
```

### `PipelineHandler` — declarative handler with intermediate observation

The handler function returns a `stream.Stream[Resp]` instead of `(Resp, error)`. This lets the
body be expressed with stream operators, enabling `Tap` to observe **intermediate computation
stages** declaratively — the same API as background streaming pipelines.

**Why not a plain function?**

```go
// Plain handler — observers buried inline
func(ctx, req SensorReq) (OEEResult, error) {
    norm, err := normalizeFn.ApplyContext(ctx, req)
    if err != nil { return zero, err }
    slog.Info("normalized", "v", norm)   // observer buried in logic
    result, err := oeeCalcFn.ApplyContext(ctx, norm)
    if err != nil { return zero, err }
    auditLog.Write(result)               // observer buried in logic
    return result, nil
}

// PipelineHandler — observers explicit via Tap
func(ctx, req SensorReq) stream.Stream[OEEResult] {
    s := stream.Single(ctx, req)
    s  = stream.Apply(ctx, s, normalizeFn, applyOpts)
    s  = stream.Tap(ctx, s, func(v Normalized) { slog.Info("normalized", "v", v) })
    out := stream.Apply(ctx, s, oeeCalcFn, applyOpts)
    return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
}
```

```go
// adapters/nethttp/stream.go

// PipelineHandlerFunc is the handler function type for PipelineHandler.
// It must use stream.Single(ctx, req) to wrap req as the pipeline source.
// The pipeline must emit exactly one value. If it emits zero, PipelineNoResponseError
// is returned to the ErrorHandler. If it emits more than one, only the first is used.
// Errors take precedence: if Stream.Errors fires, the first error becomes the response.
type PipelineHandlerFunc[Req, Resp any] func(ctx context.Context, req Req) stream.Stream[Resp]

// PipelineHandler wraps a PipelineHandlerFunc into an http.Handler.
// Internally adapts PipelineHandlerFunc → HandlerFunc via stream.Collect, then calls Handler.
// All codec, param, security, and observer integration is unchanged from Handler.
func PipelineHandler[Req, Resp any](
    handle *rest.RouteHandle[Req, Resp],
    fn PipelineHandlerFunc[Req, Resp],
    opts Options,
) http.Handler

func RegisterPipeline[Req, Resp any](
    mux *http.ServeMux,
    handle *rest.RouteHandle[Req, Resp],
    fn PipelineHandlerFunc[Req, Resp],
    opts Options,
)

// chi variants — return http.HandlerFunc, take gochi.Router
```

**Implementation sketch** — thin wrapper over `Handler`:

```go
func PipelineHandler[Req, Resp any](handle *rest.RouteHandle[Req, Resp],
    fn PipelineHandlerFunc[Req, Resp], opts Options) http.Handler {
    return Handler(handle, func(ctx context.Context, req Req) (Resp, error) {
        pipeline := fn(ctx, req)
        vals, errs := stream.Collect(ctx, pipeline)
        // Error takes precedence over values.
        if len(errs) > 0 {
            var zero Resp
            return zero, errs[0]
        }
        if len(vals) == 0 {
            var zero Resp
            return zero, PipelineNoResponseError{Path: handle.Descriptor.Path}
        }
        return vals[0], nil // multiple values: only first is used; extras silently discarded
    }, opts)
}
```

**Behaviour clarifications:**
- **Error vs value precedence:** `errs[0]` is returned regardless of whether `vals` is also non-empty.
- **Multiple values:** only `vals[0]` is used; excess values are silently discarded. Not an error.
- **Context cancellation mid-pipeline:** `stream.Collect` returns early → `PipelineNoResponseError`.
- **When to use:** multi-step handlers with forge functions + intermediate Tap. Use plain `Handler` for simple one-step handlers.

**Usage:**
```go
nethttp.RegisterPipeline(mux, oeeHandle,
    func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s   := stream.Single(ctx, req)
        s    = stream.Apply(ctx, s, validateFn, stream.ApplyOptions{Observer: obs})
        s    = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("request", "sensor", v.SensorID) })
        out := stream.Apply(ctx, s, oeeCalcFn, stream.ApplyOptions{Observer: obs})
        return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
    },
    nethttp.Options{Observer: obs})
```

### `stream.Single[T]` — new stream primitive (prerequisite)

```go
// stream/source.go

// Single wraps a single value as a Stream[T] that emits v once, then closes.
// The Stream.Errors channel is never written. Use as the entry point into a
// per-request pipeline inside a PipelineHandlerFunc or AsPipelineFunc.
func Single[T any](ctx context.Context, v T) Stream[T]
```

---

## HTTP SSE bridges (nethttp + chi)

Both `adapters/nethttp` and `adapters/chi` already provide server-side `SSEHandler[Req, Event]` and
`SSEHandlerFunc[Req, Event]`. The stream bridges cover two directions:

1. **Server-side (stream → SSE):** an incoming `stream.Stream[Event]` drives the SSE response
2. **Client-side (SSE → stream):** connect to an external SSE endpoint and consume events as a stream

### Server-side — `SSEFromStream` and `SSEFromHub`

Both `nethttp` and `chi` get identical bridge helpers (same pattern, different package):

**Mode A — per-connection stream** (each HTTP client gets its own stream):

```go
// adapters/nethttp/stream.go  (identical API in adapters/chi/stream.go)

// SSEFromStream returns an SSEHandlerFunc where streamFactory is called once
// per connecting client with the decoded Req. The resulting stream.Stream[Event]
// is consumed for that connection only.
//
// Use when each client receives a filtered or personalised event stream:
//
//  nethttp.RegisterSSE(mux, oeeRoute,
//      nethttp.SSEFromStream(func(_ context.Context, req OEEReq) stream.Stream[OEEResult] {
//          return stream.Filter(ctx, oeeStream, req.MatchesMachine)
//      }, nethttp.SSEStreamOptions{Observer: obs}), nethttp.Options{Observer: obs})
func SSEFromStream[Req, Event any](
    streamFactory func(context.Context, Req) stream.Stream[Event],
    opts SSEStreamOptions,
) SSEHandlerFunc[Req, Event]

type SSEStreamOptions struct {
    // OnError is called when an SSE write fails (client disconnected) or when
    // the upstream Stream.Errors fires. If nil, errors are silently dropped.
    OnError  func(error)
    Observer stats.Observer
}
```

**Mode B — shared broadcast hub** (all HTTP clients share the same event stream):

```go
// SSEFromHub returns an SSEHandlerFunc backed by a shared BroadcastHub.
// Each client that connects subscribes to the hub and receives items from that
// moment forward.
//
// Use for live dashboards where all users see the same real-time OEE stream:
//
//  hub := stream.NewBroadcastHub(ctx, oeeStream, 32)
//  nethttp.RegisterSSE(mux, oeeRoute,
//      nethttp.SSEFromHub[struct{}, OEEResult](hub, nethttp.SSEStreamOptions{Observer: obs}),
//      nethttp.Options{Observer: obs})
func SSEFromHub[Req, Event any](
    hub *stream.BroadcastHub[Event],
    opts SSEStreamOptions,
) SSEHandlerFunc[Req, Event]
```

> **Prerequisite:** `stream.BroadcastHub[T]` — N-subscriber fan-out — does not exist yet.
> Must be designed before `SSEFromHub` can ship. Add to stream-phase4.md.

### Client-side — `nethttp.SSEClientStream`

Consumes an SSE endpoint published by another service and emits each event as a stream item.
**This is a new source bridge** — the existing `nethttp.Call` covers regular HTTP, but not SSE.

```go
// adapters/nethttp/stream.go

// SSEClientStream connects to an SSE endpoint and emits each decoded event.
// The stream automatically reconnects after a configurable backoff when the
// connection drops. It terminates when ctx is cancelled.
//
// Each SSE "data:" line is decoded using fmt. Comment lines and retry hints
// are ignored. The connection uses the existing route handle for path/query
// param validation and observer integration.
//
// Use when consuming an SSE stream published by another go-codex service
// or any standards-compliant SSE server:
//
//  events := nethttp.SSEClientStream(ctx, httpClient, sseHandle,
//      format.JSON(eventCodec),
//      nethttp.SSEClientOptions{RetryDelay: 2*time.Second, Observer: obs})
//  oeeStream := stream.Apply(ctx, events, oeeCalcFn, stream.ApplyOptions{})
func SSEClientStream[Event any](
    ctx context.Context,
    client *http.Client,
    handle *rest.SSERouteHandle[struct{}, Event],
    fmt format.Format[Event],
    opts SSEClientOptions,
) stream.Stream[Event]

type SSEClientOptions struct {
    // RetryDelay is the initial reconnect wait after a dropped connection (default 1s).
    RetryDelay    time.Duration
    // MaxRetryDelay caps the exponential backoff (default 30s).
    MaxRetryDelay time.Duration
    Observer      stats.Observer
    Buffer        int
}
```

**Usage:**
```go
// Consume another service's OEE SSE stream and feed into our pipeline
events := nethttp.SSEClientStream(ctx, httpClient, remoteOEERoute,
    format.JSON(oeeCodec),
    nethttp.SSEClientOptions{RetryDelay: 2*time.Second, Observer: obs})

// Aggregate with local stream
merged := stream.Merge(ctx, localOEEStream, events)
stream.Drain(ctx, merged, handleOEE, logErr, stream.DrainOptions{})
```

---

## MQTT pub/sub bridge

Both `adapters/mqtt` (v3/v3.1.1) and `adapters/mqtt5` (v5) need the same two patterns:
a stream source from a subscription and a stream sink to a publish.

### Source — `mqtt.SubscribeStream` / `mqtt5.SubscribeStream`

The MQTT client's subscription model is callback-based: `client.Subscribe(topic, qos, handler)`.
The bridge creates a typed channel internally, wraps it with `stream.FromCodec`, and returns
both the stream and the message handler to register:

```go
// adapters/mqtt/stream.go

// SubscribeStream creates a bridge from an MQTT subscription to a typed stream.
// The caller must register the returned handler with the MQTT client to start
// receiving messages. Closing the channel in the handler is managed by ctx.
//
//  s, handler := mqtt.SubscribeStream(ctx, sensorHandle,
//      format.JSON(sensorCodec), stream.SourceOptions{Observer: obs},
//      mqtt.SubscribeOptions{Observer: obs})
//  client.Subscribe(sensorHandle.Topic, 1, handler)
//  oeeStream := stream.Apply(ctx, s, oeeCalcFn, stream.ApplyOptions{})
func SubscribeStream[T any](
    ctx context.Context,
    handle *events.ChannelHandle[T],
    fmt format.Format[T],
    srcOpts stream.SourceOptions,
    subOpts SubscribeOptions,
) (stream.Stream[T], pahomqtt.MessageHandler)
```

> MQTT5 version: same signature, using `pahomqtt5.MessageHandler` and the mqtt5 `SubscribeHandler`.

```go
// adapters/mqtt5/stream.go

func SubscribeStream[T any](
    ctx context.Context,
    handle *events.ChannelHandle[T],
    fmt format.Format[T],
    srcOpts stream.SourceOptions,
    subOpts mqtt5.SubscribeOptions,
) (stream.Stream[T], func(*pahomqtt5.Publish))
```

**Usage:**
```go
sensorStream, handler := mqtt.SubscribeStream(ctx, sensorHandle,
    format.JSON(sensorCodec),
    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs},
    mqtt.SubscribeOptions{Observer: obs})

client.Subscribe(sensorHandle.Topic, 1, handler)

oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})
```

### Sink — `mqtt.DrainPublish` / `mqtt5.DrainPublish`

```go
// adapters/mqtt/stream.go

// DrainPublish publishes each stream item to the MQTT broker using handle.
// Encode failures are sent to opts.OnError as PublishEncodeError.
// Broker publish failures are sent to opts.OnError as BrokerError (if the
// underlying Paho call fails synchronously).
// Blocks until src terminates or ctx is cancelled.
func DrainPublish[T any](
    ctx context.Context,
    client pahomqtt.Client,
    handle *events.ChannelHandle[T],
    qos byte,
    retained bool,
    src stream.Stream[T],
    opts DrainPublishOptions,
)

type DrainPublishOptions struct {
    Vars     map[string]string // topic variable substitutions
    OnError  func(error)       // PublishEncodeError or broker error
    Observer stats.Observer
}
```

> MQTT5 version: identical API shape, using `pahomqtt5.Client` and the mqtt5 publish path.

**Usage:**
```go
// Publish OEE alerts as retained MQTT messages
alertStream, archiveStream := stream.Tee(ctx, oeeStream)

go mqtt.DrainPublish(ctx, client, alertHandle, 1, true, alertStream,
    mqtt.DrainPublishOptions{Observer: obs, OnError: logErr})

go stream.Drain(ctx, archiveStream, db.InsertOEE, logErr, stream.DrainOptions{})
```

---

## MCP (mcpgo) bridge

The MCP protocol is request/response. The stream integration pattern is the **reactive cache**:
an MCP tool that answers each LLM-initiated call with the latest value from a running stream
pipeline, enabling LLM agents to query real-time computed state.

### `mcpgo.ToolLatestHandler`

```go
// adapters/mcpgo/stream.go

// ToolLatestHandler creates an MCP tool handler that replies with the most
// recently computed value from src. The In (tool arguments) are decoded and
// validated by handle.Decode; the result is always the latest Out from the stream,
// not a function of In.
//
// If the stream has not yet produced a value, the handler returns
// mcp.NewToolResultError("no value computed yet") with IsError: true.
//
// Use for "get current OEE", "get latest sensor reading", or any "current state"
// MCP tool backed by a continuous stream computation that an LLM agent can query:
//
//  tool, handler := mcpgo.ToolLatestHandler(oeeQueryHandle, oeeStream, mcpgo.Options{Observer: obs})
//  s.AddTool(tool, handler)
func ToolLatestHandler[In, Out any](
    handle *apimcp.ToolHandle[In, Out],
    src stream.Stream[Out],
    opts Options,
) (mcp.Tool, server.ToolHandlerFunc)
```

**Implementation sketch:**
- A goroutine reads from `src.Values` and stores each value in an atomic pointer (`*atomic.Pointer[Out]`)
- When the tool handler is called, it atomically loads the latest value
- If nil (no value yet): `return mcp.NewToolResultError("no value computed yet"), nil`
- If set: `handle.Encode(latest)` → `mcp.NewToolResultStructured(...), nil`

### MCP usage

```go
// OEE stream that runs continuously in the background
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})

// MCP tool: LLM agent calls "get_oee" → returns latest computed OEE
tool, handler := mcpgo.ToolLatestHandler(getOEEHandle, oeeStream, mcpgo.Options{Observer: obs})
s.AddTool(tool, handler)
// Output (to LLM): {"availability":0.94,"performance":0.82,"quality":0.97,"oee":0.75}
```

---

## MQTT5 request/reply bridge

MQTT5 `Serve[Req, Resp]` takes `fn func(context.Context, Req) (Resp, error)` — the same shape as
`nethttp.HandlerFunc`. The **same declarative pipeline pattern** applies: use `AsPipelineFunc` to
implement the handler body as a stream pipeline with intermediate `Tap` observation, then pass the
result to `Serve` without changing its API.

### Server-side — `mqtt5.AsPipelineFunc`

```go
// adapters/mqtt5/stream.go

// AsPipelineFunc converts a pipeline handler function into the plain handler
// function signature accepted by [Serve]. Internally, it calls stream.Single
// to wrap the incoming req as a one-shot stream, runs the pipeline, and
// collects the first value/error via stream.Collect.
//
// Use AsPipelineFunc when the Serve handler body benefits from Tap (intermediate
// observation), MapErr (per-step error recovery), or a composition of multiple
// forge functions with the same stream API used in background pipelines.
//
// Use a plain func(ctx, req)(Resp, error) for simple single-step handlers.
//
//  mqtt5.Serve(ctx, client, router, oeeHandle,
//      mqtt5.AsPipelineFunc(func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
//          s := stream.Single(ctx, req)
//          s = stream.Apply(ctx, s, validateFn, opts)
//          s = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("validated", "id", v.ID) })
//          out := stream.Apply(ctx, s, oeeCalcFn, opts)
//          return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
//      }),
//      mqtt5.ServeOptions{Observer: obs})
func AsPipelineFunc[Req, Resp any](
    fn func(context.Context, Req) stream.Stream[Resp],
) func(context.Context, Req) (Resp, error)
```

**Implementation sketch:**
```go
func AsPipelineFunc[Req, Resp any](fn func(context.Context, Req) stream.Stream[Resp]) func(context.Context, Req) (Resp, error) {
    return func(ctx context.Context, req Req) (Resp, error) {
        src := stream.Single(ctx, req)   // wrap req as single-item stream source
        pipeline := fn(ctx, req)         // build the pipeline (operators run lazily)
        _ = src // fn uses stream.Single internally
        vals, errs := stream.Collect(ctx, pipeline)
        if len(errs) > 0 {
            var zero Resp
            return zero, errs[0]
        }
        if len(vals) == 0 {
            var zero Resp
            return zero, fmt.Errorf("pipeline handler: no response produced")
        }
        return vals[0], nil
    }
}
```

> **Note:** `AsPipelineFunc` does not wrap `Serve` — it wraps the *handler function* passed to
> `Serve`. This keeps the `Serve` API unchanged and avoids duplicating its parameter handling,
> codec validation, observer calls, and error reply logic.

**Usage:**
```go
mqtt5.Serve(ctx, client, router, oeeHandle,
    mqtt5.AsPipelineFunc(func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s := stream.Single(ctx, req)
        s = stream.Apply(ctx, s, validateFn, opts)
        s = stream.Tap(ctx, s, func(v ValidatedReq) {
            slog.Info("request", "sensor", v.SensorID) // observe decoded request
        })
        out := stream.Apply(ctx, s, oeeCalcFn, opts)
        return stream.Tap(ctx, out, func(r OEEResult) {
            auditLog.Write(r) // observe reply before it is sent
        })
    }),
    mqtt5.ServeOptions{Observer: obs})
```

### Client-side — `mqtt5.CallStream`

### Client-side — `mqtt5.CallStream`

```go
// adapters/mqtt5/stream.go

// CallStream sends each request item from src to handle using [Call], emitting
// each decoded response to Stream.Values. Protocol errors or decode failures go
// to Stream.Errors as CallError.
//
// Each Call is issued sequentially with its own reply topic (from opts.ReplyTopic).
// For concurrent (pipelined) calls, buffer the source channel or use a higher
// concurrency wrapper around CallStream.
//
// Use CallStream to drive an MQTT5 request/reply service with a typed stream of
// requests — for example, batch sensor validation against a remote microservice.
func CallStream[Req, Resp any](
    ctx context.Context,
    client MQTTClient,
    router MQTTRouter,
    handle *reqreply.RouteHandle[Req, Resp],
    src stream.Stream[Req],
    opts CallStreamOptions,
) stream.Stream[Resp]

type CallStreamOptions struct {
    // ReplyTopic generates the per-call reply topic (default: UUIDReplyTopic("")).
    ReplyTopic ReplyTopicBuilder
    // Timeout is the maximum wait for a reply per call (default: 5s).
    Timeout    time.Duration
    // Observer receives RecordRequest per call.
    Observer   stats.Observer
    Buffer     int
}
```

**Usage:**
```go
// Validate each reading against a remote microservice via MQTT5 reqreply
rawReadings := stream.FromCodec(ctx, rawCh, format.JSON(readingCodec),
    stream.SourceOptions{Observer: obs})

validated := mqtt5.CallStream(ctx, client, router, validateHandle, rawReadings,
    mqtt5.CallStreamOptions{
        ReplyTopic: mqtt5.UUIDReplyTopic("client/validate/reply"),
        Observer:   obs,
    })

// Compute OEE only from validated readings
oeeStream := stream.Apply(ctx, validated, oeeCalcFn, stream.ApplyOptions{Observer: obs})
```

### Server-side — MQTT5 retained messages (no bridge needed)

For "publish the latest stream value so subscribers get it on connect", MQTT5's **retained message
flag** is the natural mechanism — no bridge helper needed:

```go
// Tap the stream and publish each result as a retained message.
// Any new MQTT5 subscriber immediately receives the most recent OEE value.
oeeStream = stream.Tap(ctx, oeeStream, func(oee OEEResult) {
    mqtt5.Publish(ctx, client, oeeHandle, oee,
        mqtt5.PublishOptions{Retain: true, Observer: obs})
})
```

---

## ZeroMQ bridge

### Source — `zeromq.SubscribeStream`

```go
// adapters/zeromq/stream.go

// SubscribeStream bridges a ZeroMQ SUB/PULL socket into a typed stream.
// Each incoming frame pair [topic, payload] is forwarded to rawCh;
// stream.FromCodec decodes and validates each payload using fmt.
//
// Socket errors (from RecvMessage) close the stream and are sent to
// Stream.Errors as a SocketError.
// Decode/validation failures are sent to Stream.Errors as stream.StreamDecodeError.
// The stream closes when ctx is cancelled or the socket errors.
//
// Buffer controls the internal channel buffer size (default 0).
func SubscribeStream[T any](
    ctx context.Context,
    sock FramedSocket,
    handle *events.ChannelHandle[T],
    fmt format.Format[T],
    opts stream.SourceOptions,
) stream.Stream[T]
```

**Implementation sketch:**
```go
func SubscribeStream[T any](ctx context.Context, sock FramedSocket, handle *events.ChannelHandle[T],
    f format.Format[T], opts stream.SourceOptions) stream.Stream[T] {

    rawCh := make(chan []byte, opts.Buffer)
    go func() {
        defer close(rawCh)
        if err := sock.SetSubscription(handle.Topic); err != nil {
            return
        }
        for {
            select {
            case <-ctx.Done():
                return
            default:
            }
            frames, err := sock.RecvMessage(0)
            if err != nil {
                return // socket error — closes rawCh, stream terminates
            }
            if len(frames) < 2 {
                continue
            }
            select {
            case rawCh <- []byte(frames[1]):
            case <-ctx.Done():
                return
            }
        }
    }()
    return stream.FromCodec(ctx, rawCh, f, opts)
}
```

**Usage:**
```go
sensors := zeromq.SubscribeStream(ctx, subSock, sensorHandle,
    format.JSON(sensorCodec),
    stream.SourceOptions{Name: "zmq/sensors/+", Observer: obs})

oeeStream := stream.Apply(ctx, sensors, oeeCalcFn, stream.ApplyOptions{Observer: obs})
```

### Sink — `zeromq.DrainPublish`

```go
// DrainPublish publishes each item from src to sock using handle's codec.
// Encode failures are sent to the onError callback (if non-nil) as PublishError.
// Blocks until src terminates or ctx is cancelled.
func DrainPublish[T any](
    ctx context.Context,
    sock FramedSocket,
    handle *events.ChannelHandle[T],
    src stream.Stream[T],
    fmt format.Format[T],
    opts DrainPublishOptions,
)

type DrainPublishOptions struct {
    OnError func(error)       // called for encode or send errors
    Observer stats.Observer   // RecordPublish per item
}
```

**Usage:**
```go
alerts, metrics := stream.Tee(ctx, oeeStream)
go zeromq.DrainPublish(ctx, pubSock, alertHandle, alerts, format.JSON(alertCodec),
    zeromq.DrainPublishOptions{Observer: obs})
```

### Server-side — `zeromq.AsPipelineFunc`

Same pattern as `mqtt5.AsPipelineFunc`. ZeroMQ's `Serve[Req, Resp]` and `ServeRouter[Req, Resp]`
both accept `fn func(context.Context, Req) (Resp, error)` — identical to the MQTT5 server handler shape.

```go
// adapters/zeromq/stream.go

// AsPipelineFunc converts a pipeline handler function into the plain handler
// function signature accepted by [Serve] and [ServeRouter].
// Same semantics as mqtt5.AsPipelineFunc: wraps req as stream.Single, runs the
// pipeline, collects first value/error via stream.Collect.
//
//  zeromq.Serve(ctx, repSock, oeeHandle,
//      zeromq.AsPipelineFunc(func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
//          s := stream.Single(ctx, req)
//          s = stream.Apply(ctx, s, validateFn, opts)
//          s = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("request", "id", v.ID) })
//          out := stream.Apply(ctx, s, oeeCalcFn, opts)
//          return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
//      }),
//      zeromq.ServeOptions{Observer: obs})
func AsPipelineFunc[Req, Resp any](
    fn func(context.Context, Req) stream.Stream[Resp],
) func(context.Context, Req) (Resp, error)
```

**Usage:**
```go
go zeromq.Serve(ctx, repSock, oeeHandle,
    zeromq.AsPipelineFunc(func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s := stream.Single(ctx, req)
        s = stream.Apply(ctx, s, validateFn, opts)
        s = stream.Tap(ctx, s, func(v ValidatedReq) {
            slog.Info("request", "sensor", v.SensorID)
        })
        return stream.Apply(ctx, s, oeeCalcFn, opts)
    }),
    zeromq.ServeOptions{Observer: obs})
```

### Client-side — `zeromq.CallStream` and `zeromq.CallDealerStream`

ZeroMQ offers two client patterns for request/reply: synchronous REQ (one in-flight at a time)
and asynchronous DEALER (concurrent, matched by sequence number). Both can be bridged to a stream.

```go
// adapters/zeromq/stream.go

// CallStream sends each request item from src to handle using a REQ socket.
// Requests are issued sequentially (REQ socket is inherently synchronous).
// Use CallDealerStream for concurrent pipelining.
//
// Call errors (socket, encode, decode) go to Stream.Errors as CallError.
func CallStream[Req, Resp any](
    ctx context.Context,
    sock FramedSocket,
    handle *reqreply.RouteHandle[Req, Resp],
    src stream.Stream[Req],
    opts CallStreamOptions,
) stream.Stream[Resp]

// CallDealerStream sends requests concurrently using a DEALER socket.
// Each request is tagged with a sequence number; responses are matched
// and emitted in arrival order (not send order). Concurrency is bounded by
// opts.MaxInFlight (default: 16).
func CallDealerStream[Req, Resp any](
    ctx context.Context,
    sock FramedSocket,
    handle *reqreply.RouteHandle[Req, Resp],
    src stream.Stream[Req],
    opts CallDealerStreamOptions,
) stream.Stream[Resp]

type CallStreamOptions struct {
    Observer stats.Observer
    Buffer   int
}

type CallDealerStreamOptions struct {
    MaxInFlight int            // max concurrent requests (default 16)
    Observer    stats.Observer
    Buffer      int
}
```

**Usage:**
```go
// Sequential (REQ) — simple, one request at a time
results := zeromq.CallStream(ctx, reqSock, computeHandle, requestStream,
    zeromq.CallStreamOptions{Observer: obs})

// Concurrent (DEALER) — pipelined, higher throughput
results := zeromq.CallDealerStream(ctx, dealerSock, computeHandle, requestStream,
    zeromq.CallDealerStreamOptions{MaxInFlight: 32, Observer: obs})

oeeStream := stream.Apply(ctx, results, oeePostProcess, stream.ApplyOptions{})
```

### Server-side — `zeromq.ServeLatest`

The "reactive cache" pattern: serve a REP socket by responding with the **latest value from a
running stream pipeline**. Callers query for current state; the server never blocks waiting for new
computation.

```go
// ServeLatest serves a REP socket where every incoming request receives the
// most recently computed value from the latest stream. The Req payload is
// decoded and validated but not used to compute the response (pure cache-hit).
//
// Returns ErrNoValue (a typed error) with status 503 if no value has been
// computed yet. Terminates when ctx is cancelled or the socket errors.
//
// Use ServeLatest for "get current OEE" semantics: a stream continuously
// computes OEE; clients query for the current value at any time.
func ServeLatest[Req, Resp any](
    ctx context.Context,
    sock FramedSocket,
    handle *reqreply.RouteHandle[Req, Resp],
    latest stream.Stream[Resp],
    opts ServeLatestOptions,
) error

type ServeLatestOptions struct {
    OnError  func(ServeError) // decode / no-value errors
    Observer stats.Observer
}
```

**Usage:**
```go
// Continuously compute OEE; serve latest to ZMQ clients
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})
latestOEE, archiveOEE := stream.Tee(ctx, oeeStream)

go stream.Drain(ctx, archiveOEE, db.InsertOEE, logErr, stream.DrainOptions{})
go func() {
    if err := zeromq.ServeLatest(ctx, repSock, oeeQueryHandle, latestOEE,
        zeromq.ServeLatestOptions{Observer: obs}); err != nil {
        slog.Error("zeromq serve stopped", "err", err)
    }
}()
```

---

## nethttp bridge

### Source — polling

HTTP does not push; a source must poll. The bridge helper wraps a periodic `nethttp.Call` into a
stream.

```go
// adapters/nethttp/stream.go

// PollStream calls route at the given interval, decoding each response into a
// stream item. The stream terminates when ctx is cancelled.
//
// A failed HTTP call (network error, non-2xx, decode error) is sent to
// Stream.Errors. The poll continues after each error — transient errors do not
// terminate the stream.
//
// interval is the time between the end of one successful call and the start of
// the next. Use stream.Throttle on the result to rate-limit further.
func PollStream[Req, Resp any](
    ctx context.Context,
    client *http.Client,
    handle *rest.RouteHandle[Req, Resp],
    req Req,
    interval time.Duration,
    opts PollStreamOptions,
) stream.Stream[Resp]

type PollStreamOptions struct {
    Observer stats.Observer // RecordRequest per poll
    Buffer   int
}
```

**Usage:**
```go
// Poll a metrics endpoint every 5 seconds
metricsStream := nethttp.PollStream(ctx, httpClient, metricsRoute,
    MetricsRequest{Sensor: "s1"}, 5*time.Second,
    nethttp.PollStreamOptions{Observer: obs})

windowed := stream.Window(ctx, metricsStream, 1*time.Minute)
avgStream := stream.Apply(ctx, windowed, computeAvgFn, stream.ApplyOptions{})
```

### Source — SSE (Server-Sent Events)

```go
// SSEStream reads a Server-Sent Events endpoint and emits each decoded event.
// The stream reconnects on connection drop (with exponential backoff).
// Use for real-time push from HTTP servers that support SSE.
func SSEStream[T any](
    ctx context.Context,
    client *http.Client,
    handle *rest.RouteHandle[struct{}, T], // GET endpoint
    opts SSEStreamOptions,
) stream.Stream[T]

type SSEStreamOptions struct {
    Observer       stats.Observer
    RetryDelay     time.Duration  // initial reconnect delay (default 1s)
    MaxRetryDelay  time.Duration  // cap for exponential backoff (default 30s)
    Buffer         int
}
```

### Sink — `nethttp.DrainCall`

```go
// DrainCall posts each stream item to handle (using Call) and discards the response.
// Encode/HTTP errors are forwarded to onError. Blocks until src terminates.
func DrainCall[Req, Resp any](
    ctx context.Context,
    client *http.Client,
    handle *rest.RouteHandle[Req, Resp],
    src stream.Stream[Req],
    opts DrainCallOptions,
)

type DrainCallOptions struct {
    OnError  func(error)
    Observer stats.Observer
}
```

**Usage:**
```go
// POST each OEE alert to a REST API
stream.Drain(ctx, alertStream,
    func(ctx context.Context, alert OEEAlert) error {
        _, err := nethttp.Call(ctx, httpClient, alertRoute, alert, nethttp.CallOptions{})
        return err
    }, logErr, stream.DrainOptions{Observer: obs})

// — or with the bridge helper —
nethttp.DrainCall(ctx, httpClient, alertRoute, alertStream,
    nethttp.DrainCallOptions{Observer: obs})
```

---

## File bridge (new adapter package)

The `adapters/file` package does not exist yet. It would provide codec-aware file I/O with stream
integration. No external dependencies — stdlib only (`os`, `bufio`).

### Source — scan lines

```go
// adapters/file/stream.go

// ScanStream opens path and emits each line as a decoded T.
// Decode errors go to Stream.Errors as stream.StreamDecodeError.
// The stream terminates after EOF or ctx cancellation.
func ScanStream[T any](
    ctx context.Context,
    path string,
    fmt format.Format[T],
    opts stream.SourceOptions,
) (stream.Stream[T], error)
```

**Usage:**
```go
// Stream a NDJSON log file line-by-line
s, err := file.ScanStream(ctx, "readings.ndjson",
    format.JSON(readingCodec),
    stream.SourceOptions{Name: "readings.ndjson", Observer: obs})
if err != nil {
    return err
}
vals, errs := stream.Collect(ctx, s) // bounded — EOF terminates stream
```

### Source — directory watch

```go
// WatchStream emits the path of each new file created in dir.
// Uses os.ReadDir polling at interval (stdlib only — no fsnotify dependency).
// The stream terminates when ctx is cancelled.
func WatchStream(
    ctx context.Context,
    dir string,
    interval time.Duration,
    opts stream.SourceOptions,
) stream.Stream[string] // emits file paths
```

**Usage:**
```go
newFiles := file.WatchStream(ctx, "/data/uploads", 500*time.Millisecond,
    stream.SourceOptions{Observer: obs})

// For each new file path, scan its contents
parsed := stream.FlatMapSlice(ctx, newFiles, func(path string) []Reading {
    s, err := file.ScanStream(ctx, path, format.JSON(readingCodec), stream.SourceOptions{})
    if err != nil {
        return nil
    }
    vals, _ := stream.Collect(ctx, s)
    return vals
})
```

### Sink — write lines

```go
// DrainWrite encodes each stream item using fmt and writes it as a line to w.
// Encode errors are sent to onError. Blocks until src terminates.
func DrainWrite[T any](
    ctx context.Context,
    w io.Writer,
    src stream.Stream[T],
    fmt format.Format[T],
    opts DrainWriteOptions,
)

type DrainWriteOptions struct {
    OnError func(error)
    // Separator is written after each item. Defaults to "\n".
    Separator string
}
```

**Usage:**
```go
outFile, _ := os.Create("oee-results.ndjson")
defer outFile.Close()

file.DrainWrite(ctx, outFile, oeeStream, format.JSON(oeeCodec),
    file.DrainWriteOptions{OnError: logErr})
```

---

## SQL bridge

### Source — query polling

```go
// adapters/sql/stream.go

// QueryStream polls queryFn at interval and emits each returned row as a stream
// item. Each row is validated with adapters/sql.Validate using codec c.
// Rows failing validation go to Stream.Errors as RowValidationError.
// The stream terminates when ctx is cancelled.
//
// queryFn must return all rows for one poll cycle; rows from the previous cycle
// are not deduplicated (use a cursor or timestamp filter inside queryFn).
func QueryStream[T any](
    ctx context.Context,
    c codex.Codec[T],
    queryFn func(context.Context) ([]T, error),
    interval time.Duration,
    opts QueryStreamOptions,
) stream.Stream[T]

type QueryStreamOptions struct {
    Observer stats.Observer // RecordValidation per row (SQLObserver), RecordStreamItem per emission
    Table    string         // for RowValidationError context
    Op       string         // for RowValidationError context
    Buffer   int
}
```

**Usage:**
```go
// Poll a sensor_readings table every 30 seconds
readingsStream := sql.QueryStream(ctx, readingCodec,
    func(ctx context.Context) ([]Reading, error) {
        return db.ListReadingsSince(ctx, time.Now().Add(-30*time.Second))
    },
    30*time.Second,
    sql.QueryStreamOptions{Table: "sensor_readings", Op: "list_readings_since", Observer: obs},
)

// Apply OEE computation per reading
oeeStream := stream.Apply(ctx, readingsStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})
```

### Sink — insert

```go
// DrainInsert inserts each stream item into the database using insertFn.
// Each item is validated with adapters/sql.Validate before insert.
// Validation failures go to onError as RowValidationError; the item is not inserted.
// Database errors go to onError as-is. Blocks until src terminates.
func DrainInsert[T any](
    ctx context.Context,
    c codex.Codec[T],
    src stream.Stream[T],
    insertFn func(context.Context, T) error,
    opts DrainInsertOptions,
)

type DrainInsertOptions struct {
    OnError  func(error)
    Observer stats.Observer
    Table    string
    Op       string
}
```

**Usage:**
```go
sql.DrainInsert(ctx, oeeResultCodec, oeeStream,
    func(ctx context.Context, r OEEResult) error {
        return db.InsertOEEResult(ctx, r)
    },
    sql.DrainInsertOptions{
        Table:    "oee_results",
        Op:       "insert_oee_result",
        Observer: obs,
        OnError:  logErr,
    },
)
```

---

## Structured errors

Every bridge helper must return typed, `errors.As`-navigable errors that implement
`slog.LogValuer`. Below is the complete new error inventory per package.

### Reused existing errors (no new types)

| Situation | Reused type |
|-----------|-------------|
| ZeroMQ socket failure in `SubscribeStream`/`DrainPublish`/`CallStream` | `zeromq.SocketError{Op, Err}` |
| ZeroMQ encode failure in `DrainPublish` | `zeromq.PublishEncodeError{Topic, Err}` |
| ZeroMQ call failure in `CallStream` | `zeromq.CallError{Err}` |
| MQTT5 call failure in `CallStream` | `mqtt5.CallError{Err}` |
| MQTT encode failure in `DrainPublish` | `mqtt.PublishEncodeError{Topic, Err}` / `mqtt5.PublishEncodeError{Topic, Err}` |
| MQTT broker failure in `DrainPublish` | `mqtt.BrokerError{Op, Err}` / `mqtt5.BrokerError{Op, Err}` |
| MQTT subscribe decode/validation in `SubscribeStream` | `mqtt.SubscribeError{Kind, Topic, Err}` / `mqtt5.SubscribeError{Kind, Topic, Err}` |
| nethttp call failure in `PollStream`/`DrainCall` | existing `nethttp.*` errors (`UnexpectedStatusError`, `RequestError`, …) |
| SQL row validation in `QueryStream`/`DrainInsert` | `sql.RowValidationError{Table, Op, Err}` |
| Stream decode failure in any source bridge | `stream.StreamDecodeError{Source, Err}` |

### New error types

#### `adapters/nethttp` and `adapters/chi` (request/response + SSE bridges)

Three new error types — all implement `slog.LogValuer`; none implement `Unwrap()` (no inner cause):

```go
// NoLatestValueError is passed to opts.ErrorHandler (status 503) by HandlerLatest
// when the background stream has not yet produced a value.
type NoLatestValueError struct {
    Path string // route path (from RouteHandle.Descriptor.Path)
}

func (e NoLatestValueError) Error() string {
    return fmt.Sprintf("http latest %s: no value yet", e.Path)
}
func (e NoLatestValueError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("path", e.Path))
}

// PipelineFullError is passed to opts.ErrorHandler (status 503) by HandlerIngest
// when the destination channel is full and the incoming request cannot be enqueued.
type PipelineFullError struct {
    Path     string // route path (from RouteHandle.Descriptor.Path)
    Capacity int    // cap(dst) — helps callers tune buffer sizing
}

func (e PipelineFullError) Error() string {
    return fmt.Sprintf("http ingest %s: pipeline full (capacity %d)", e.Path, e.Capacity)
}
func (e PipelineFullError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("path", e.Path),
        slog.Int("capacity", e.Capacity),
    )
}

// PipelineNoResponseError is returned by PipelineHandler when stream.Collect
// returns with no values — either the pipeline emitted nothing, or the request
// context was cancelled before the pipeline produced a result.
type PipelineNoResponseError struct {
    Path string // route path (from RouteHandle.Descriptor.Path)
}

func (e PipelineNoResponseError) Error() string {
    return fmt.Sprintf("http pipeline %s: no response produced", e.Path)
}
func (e PipelineNoResponseError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("path", e.Path))
}
```

> All three live in both `adapters/nethttp` and `adapters/chi` — identical type definitions,
> different package paths.

#### `adapters/nethttp` and `adapters/chi` (SSE bridges)

```go
// SSEWriteError is sent to SSEStreamOptions.OnError by SSEFromStream / SSEFromHub
// when writing an event to the HTTP SSE response fails (typically because
// the client disconnected mid-stream).
// Same type in both adapters/nethttp and adapters/chi.
type SSEWriteError struct {
    Path string // route path (from SSERouteHandle.Descriptor.Path)
    Err  error
}

func (e SSEWriteError) Error() string
func (e SSEWriteError) Unwrap() error { return e.Err }
func (e SSEWriteError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("path", e.Path), slog.Any("err", e.Err))
}

// SSEConnectError is sent to Stream.Errors by SSEClientStream when the HTTP
// connection attempt fails (network error, non-200 status, TLS failure).
// The stream retries after backoff; this error is informational per attempt.
type SSEConnectError struct {
    URL     string
    Attempt int   // 1-based reconnect attempt number
    Err     error
}

func (e SSEConnectError) Error() string
func (e SSEConnectError) Unwrap() error { return e.Err }
func (e SSEConnectError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("url", e.URL),
        slog.Int("attempt", e.Attempt),
        slog.Any("err", e.Err),
    )
}

// SSEParseError is sent to Stream.Errors by SSEClientStream when an SSE data
// line cannot be decoded using the provided format (malformed JSON, failed
// codec validation, etc.).
type SSEParseError struct {
    URL  string
    Line string // the raw SSE data line that failed
    Err  error
}

func (e SSEParseError) Error() string
func (e SSEParseError) Unwrap() error { return e.Err }
func (e SSEParseError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("url", e.URL),
        slog.String("line", e.Line),
        slog.Any("err", e.Err),
    )
}
```

#### `adapters/mqtt5` and `adapters/zeromq` (AsPipelineFunc)

```go
// PipelineNoResponseError is returned by AsPipelineFunc when stream.Collect
// returns with no values — either the pipeline emitted nothing, or the request
// context was cancelled.
// In MQTT5 it is passed to ServeOptions.OnError wrapped in ServeError.
// In ZeroMQ it is passed to ServeOptions.OnError wrapped in ServeError.
type PipelineNoResponseError struct {
    Topic string // route topic (from RouteHandle.Topic)
}

func (e PipelineNoResponseError) Error() string {
    return fmt.Sprintf("pipeline %s: no response produced", e.Topic)
}
func (e PipelineNoResponseError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("topic", e.Topic))
}
```

> The mqtt5 version lives in `adapters/mqtt5`, the zeromq version in `adapters/zeromq`.
> Both have identical shape but different `Topic`-vs-`Path` field names to match the adapter vocabulary.

#### `adapters/mcpgo`

No new Go error types. MCP errors are returned as `*mcp.CallToolResult{IsError: true}` following
the protocol contract. The "no value yet" case returns:
```go
return mcp.NewToolResultError("no value computed yet"), nil
```
This is consistent with how `ToolHandler` handles input errors — visible to the LLM, not a Go error.

#### `adapters/zeromq`

```go
// ServeLatestError is sent to opts.OnError by ServeLatest when a socket,
// decode, or encode operation fails during the serve loop.
//
// Op values: "recv" (socket read), "decode" (request decode),
// "encode" (response encode), "send" (socket write).
type ServeLatestError struct {
    Op  string
    Err error
}

func (e ServeLatestError) Error() string { return "zeromq serve_latest " + e.Op + ": " + e.Err.Error() }
func (e ServeLatestError) Unwrap() error { return e.Err }
func (e ServeLatestError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("op", e.Op), slog.Any("err", e.Err))
}

// NoLatestValueError is sent to opts.OnError by ServeLatest when a request
// arrives before any value has been produced by the latest stream.
// The REP socket replies with an error frame; this error is informational only.
type NoLatestValueError struct{}

func (e NoLatestValueError) Error() string { return "zeromq serve_latest: no value yet" }
func (e NoLatestValueError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("status", "no_value"))
}

// CorrelationError is sent to Stream.Errors by CallDealerStream when a
// response frame arrives with a sequence number that does not match any
// pending request. This indicates a protocol violation or a stale reply
// from a previous session.
type CorrelationError struct {
    Seq int
    Err error
}

func (e CorrelationError) Error() string
func (e CorrelationError) Unwrap() error { return e.Err }
func (e CorrelationError) LogValue() slog.Value {
    return slog.GroupValue(slog.Int("seq", e.Seq), slog.Any("err", e.Err))
}
```

#### `adapters/sql` (stream additions)

```go
// QueryStreamError is sent to Stream.Errors by QueryStream when the user's
// queryFn returns an error. Distinct from RowValidationError (codec failure):
// QueryStreamError signals a database or application-level error.
type QueryStreamError struct {
    Table string
    Op    string
    Err   error
}

func (e QueryStreamError) Error() string
func (e QueryStreamError) Unwrap() error { return e.Err }
func (e QueryStreamError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("table", e.Table),
        slog.String("op", e.Op),
        slog.Any("err", e.Err),
    )
}

// InsertStreamError is sent to opts.OnError by DrainInsert when the user's
// insertFn returns a database or application-level error (after successful
// codec validation). Distinct from RowValidationError (codec failure).
type InsertStreamError struct {
    Table string
    Op    string
    Err   error
}

func (e InsertStreamError) Error() string
func (e InsertStreamError) Unwrap() error { return e.Err }
func (e InsertStreamError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("table", e.Table),
        slog.String("op", e.Op),
        slog.Any("err", e.Err),
    )
}
```

#### `adapters/file` (new package, all errors are new)

```go
// ScanError is sent to Stream.Errors by ScanStream when opening or reading
// the file fails. When Err wraps stream.StreamDecodeError, the failure was a
// codec decode error on a specific line; otherwise it is an I/O error.
type ScanError struct {
    Path string
    Err  error
}

func (e ScanError) Error() string
func (e ScanError) Unwrap() error { return e.Err }
func (e ScanError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("path", e.Path), slog.Any("err", e.Err))
}

// WatchError is sent to Stream.Errors by WatchStream when a directory read
// fails during a poll cycle. The stream continues on the next poll interval.
type WatchError struct {
    Dir string
    Err error
}

func (e WatchError) Error() string
func (e WatchError) Unwrap() error { return e.Err }
func (e WatchError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("dir", e.Dir), slog.Any("err", e.Err))
}

// WriteError is sent to opts.OnError by DrainWrite when encoding or writing
// an item to the writer fails.
type WriteError struct {
    Path string // empty when writing to a non-file writer
    Err  error
}

func (e WriteError) Error() string
func (e WriteError) Unwrap() error { return e.Err }
func (e WriteError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("path", e.Path), slog.Any("err", e.Err))
}
```

---

## Adapters with no stream bridge

### `adapters/templ`

`adapters/templ` provides `Format[Props]` and `StreamingFormat[Props]` — wrappers that render
Templ components as `format.Format` values for content negotiation. It is a **rendering format
adapter**, not a transport.

No stream bridge is needed because:
- Templ renders Props → HTML bytes; it does not send or receive from a network transport
- Stream integration is achieved by composing existing bridges:

```go
// Stream of OEE results → SSE → streamed HTML fragments via templ
oeeSSERoute, _ := rest.NewSSERoute[struct{}, OEEResult]("/oee/stream",
    codex.Struct[struct{}](), oeeCodec, rest.RouteMeta{}).Register(b)

hub := stream.NewBroadcastHub(ctx, oeeStream, 32)
nethttp.RegisterSSE(mux, oeeSSERoute,
    nethttp.SSEFromHub[struct{}, OEEResult](hub, nethttp.SSEStreamOptions{}),
    nethttp.Options{})
```

If the SSE endpoint serves HTML fragments via Templ, the route's `ResponseFormats` would include
`templ.StreamingFormat(oeeCodec, oeeComponent)` — no new bridge needed in `adapters/templ` itself.

---

## Design decisions

| Decision | Rationale |
|----------|-----------|
| Bridge helpers live in their adapter package, not `stream/` | Keeps adapter dependencies out of `stream`. `stream` imports no adapters; adapters import `stream`. |
| `stream.FromCodec` used internally by source helpers | Reuses the existing decode+validate+error path. No duplication. |
| Source helpers return `stream.Stream[T]` | Callers can immediately chain `Apply`, `Filter`, `Tap`, etc. |
| Sink helpers accept `stream.Stream[T]` and call `stream.Drain` internally | Consistent with how user-code drains streams; observer pattern fires the same way. |
| File uses polling (not fsnotify) for `WatchStream` | Keeps adapters/file as stdlib-only. Users who need inotify can implement their own source bridge using `stream.From`. |
| SQL `QueryStream` is poll-based | SQL databases are not push sources. Logical push (LISTEN/NOTIFY in PostgreSQL) is a separate future enhancement. |
| `mqtt.SubscribeStream` returns both stream and handler | MQTT's callback model requires the caller to pass the handler to `client.Subscribe`. Returning `(stream.Stream[T], pahomqtt.MessageHandler)` keeps the client API intact while providing the stream. |
| MQTT5 server side needs no bridge | `Serve[Req, Resp]` already accepts any `fn` — the caller is free to read from a stream inside `fn`. The retained message pattern (`Publish(..., retain: true)`) is the idiomatic MQTT5 "publish latest" mechanism. |
| `nethttp.SSEClientStream` handles reconnect internally | SSE connections drop silently; automatic exponential backoff reconnect is the production-safe default. `SSEConnectError` is informational per attempt so callers can log or alert. |
| ZeroMQ `ServeLatest` is a stream-specific pattern | The server reads the latest value from a stream and replies with it. No equivalent in existing `Serve` — new behaviour specific to the stream integration. |
| `CallDealerStream` uses sequence numbers for correlation | DEALER socket is async; responses arrive out-of-order. A sequence number frame (added by the bridge) correlates each response to its request without per-call UUIDs. |
| Existing errors are reused where sufficient | `SocketError`, `CallError`, `PublishEncodeError`, `RowValidationError`, `StreamDecodeError` cover most bridge failure modes without new types. New types are added only when no existing type carries the required context. |
| All new error types implement `slog.LogValuer` | Consistent with go-codex error contract: every error must be structurally loggable, `errors.As`-navigable, and carry `Unwrap()`. |
| HTTP req/resp: three bridge patterns | `HandlerLatest` (reactive cache), `HandlerIngest` (pipeline source), `PipelineHandler` (declarative per-request computation with Tap). "Wrap plain function in channels with no observation" is explicitly not a bridge. |
| All HTTP bridge helpers reuse existing `Options` | No new option structs for `HandlerLatest` or `HandlerIngest`. Bridge-specific errors (`NoLatestValueError`, `PipelineFullError`, `PipelineNoResponseError`) are passed through the existing `ErrorHandler` in `Options`. Consistent API shape. |
| `HandlerLatest`/`HandlerIngest`/`PipelineHandler` all return `http.Handler` (nethttp) or `http.HandlerFunc` (chi) | Matches the existing `Handler` return type per adapter. Register variants mirror `Register`. |
| `HandlerIngest` rejects on full channel (503, not block) | Blocking inside an HTTP handler starves the Go HTTP server goroutine. Non-blocking send with `PipelineFullError{Path, Capacity}` lets callers tune buffer size via `Capacity` field. |
| `HandlerLatest` is Req-agnostic for the computation | Request is decoded and validated (codec runs) but not used to compute the response — always the latest stream value. Use `PipelineHandler` for per-request parameterized computation. |
| `HandlerIngest` route handle is `RouteHandle[Req, struct{}]` | The response type is `struct{}` — encodes as JSON `{}`. Route should be configured as POST/PUT returning 202 Accepted. Callers who need a different body shape configure `opts.ErrorHandler` or return a typed struct. |
| `PipelineHandler` is thin wrapper over `Handler` via `stream.Collect` | All codec, param, security, and observer integration comes from `Handler`. `PipelineHandler` only adapts the function signature. |
| Error vs value precedence in `stream.Collect` | `errs[0]` takes precedence over `vals` — if both are non-empty, error is returned. Multiple values: only `vals[0]` used; extras silently discarded. Zero values before ctx cancel → `PipelineNoResponseError`. |
| `PipelineHandler` is appropriate for multi-step observable handlers | Plain `HandlerFunc` is clearer for simple one-step handlers. Use `PipelineHandler` when multiple forge functions, intermediate Tap, or `MapErr` per-step recovery justify the declarative style. |
| `stream.Single[T]` is a new export in the `stream` package | Wraps a single value as a `Stream[T]` source. Used as the per-request pipeline entry point. Also useful for testing any stream operator with a single known input. |
| `AsPipelineFunc` wraps the handler fn, not `Serve` | Keeps `Serve`/`ServeRouter` unchanged — all codec validation, observer calls, and error reply logic remain in the adapter. No API duplication. |
| `PipelineHandler` pattern applies uniformly to all req/reply servers | HTTP (nethttp, chi), MQTT5 (`Serve`), ZeroMQ (`Serve`, `ServeRouter`) all have `fn func(ctx, Req)(Resp, error)`. Same `stream.Single + stream.Collect` approach — only adapter surface differs. |
| New error types all implement `slog.LogValuer`, none implement `Unwrap()` | All new errors (`NoLatestValueError`, `PipelineFullError`, `PipelineNoResponseError`) have no inner cause to chain — `Unwrap()` would return nil, which is misleading. Only errors that wrap a cause implement `Unwrap()`. |

---

## Circular dependency check

```
stream           → codex, forge, format, stats                             (no adapters)
adapters/chi     → api/rest, format, stats, go-chi/chi/v5                  + stream (OK)
adapters/mcpgo   → api/mcp, stats, mark3labs/mcp-go                        + stream (OK)
adapters/mqtt    → api/events, format, stats, eclipse/paho.mqtt.golang     + stream (OK)
adapters/mqtt5   → api/reqreply, format, stats, eclipse/paho.mqtt5.golang  + stream (OK)
adapters/zeromq  → api/events, api/reqreply, format, stats                 + stream (OK)
adapters/nethttp → api/rest, format, stats, net/http (stdlib)              + stream (OK)
adapters/sql     → codex, stats                                            + stream (OK)
adapters/templ   → codex, format, a-h/templ                               NOT bridged (rendering format, not transport)
adapters/file    → format, stats                                           + stream (OK, new package)
```

> **`stream.BroadcastHub[T]` prerequisite for chi bridge (Mode B):** `chi.SSEFromHub` requires a
> N-subscriber fan-out type in the `stream` package. This must be designed and implemented before
> Phase 1 of the chi bridge can ship. Add to stream-phase4.md or create a dedicated design doc.

No circular dependencies. `stream` does not import any adapter package.

---

## Implementation phases

### Phase 0 — `stream.BroadcastHub[T]` (prerequisite for SSE Mode B)
- `stream/broadcast.go` — `BroadcastHub[T]`, `NewBroadcastHub`, `Subscribe`, `Unsubscribe`
- Tests: concurrent subscriber lifecycle, slow subscriber backpressure, hub close

### Phase 1 — ZeroMQ bridge
- `adapters/zeromq/stream.go` — `SubscribeStream`, `DrainPublish`, `AsPipelineFunc`, `CallStream`, `CallDealerStream`, `ServeLatest`
- `adapters/zeromq/errors.go` additions — `ServeLatestError`, `NoLatestValueError`, `CorrelationError`, `PipelineNoResponseError{Topic string}`
- Tests: `adapters/zeromq/stream_test.go` with in-process test socket pair

### Phase 1.5 — `stream.Single[T]` (prerequisite for PipelineHandler and AsPipelineFunc)
- `stream/source.go` addition — `Single[T](ctx context.Context, v T) Stream[T]`
- `stream/source_test.go` additions — `TestSingle_EmitsOneValue`, `TestSingle_ContextCancel`, `TestSingle_ErrorsChannelNeverWritten`, `ExampleSingle`

### Phase 2 — HTTP bridges (nethttp + chi): reqresp + SSE, both directions
- `adapters/nethttp/stream.go` — `HandlerLatest`, `RegisterLatest`, `HandlerIngest`, `RegisterIngest`, `PipelineHandlerFunc`, `PipelineHandler`, `RegisterPipeline`, `SSEFromStream`, `SSEFromHub`, `SSEClientStream`, `PollStream`, `DrainCall`
- `adapters/chi/stream.go` — `HandlerLatest`, `RegisterLatest`, `HandlerIngest`, `RegisterIngest`, `PipelineHandlerFunc`, `PipelineHandler`, `RegisterPipeline`, `SSEFromStream`, `SSEFromHub`
- `adapters/nethttp/errors.go` additions — `NoLatestValueError`, `PipelineFullError`, `PipelineNoResponseError`, `SSEWriteError`, `SSEConnectError`, `SSEParseError`
- `adapters/chi/errors.go` additions — `NoLatestValueError`, `PipelineFullError`, `PipelineNoResponseError`, `SSEWriteError`
- Depends on Phase 0 (`stream.BroadcastHub`) for `SSEFromHub`; depends on Phase 1.5 (`stream.Single`) for `PipelineHandler`

### Phase 3 — MQTT pub/sub bridge (both mqtt and mqtt5) + MQTT5 reqreply pipeline
- `adapters/mqtt/stream.go` — `SubscribeStream`, `DrainPublish`
- `adapters/mqtt5/stream.go` — `SubscribeStream`, `DrainPublish`, `AsPipelineFunc`, `CallStream`
- `adapters/mqtt5/errors.go` addition — `PipelineNoResponseError{Topic string}` with `slog.LogValuer`
- Reuses existing `PublishEncodeError`, `BrokerError`, `SubscribeError` for all other failures

### Phase 4 — MCP bridge
- `adapters/mcpgo/stream.go` — `ToolLatestHandler`, `RegisterToolLatest`
- No new error types (MCP error contract uses `mcp.NewToolResultError`)

### Phase 5 — SQL bridge
- `adapters/sql/stream.go` — `QueryStream`, `DrainInsert`
- `adapters/sql/errors.go` additions — `QueryStreamError`, `InsertStreamError`
- Tests: SQLite in-memory

### Phase 6 — File bridge (new package)
- `adapters/file/` — `ScanStream`, `WatchStream`, `DrainWrite`, all three error types
- Tests: temp files with `os.MkdirTemp`

### Out of scope
- SQL LISTEN/NOTIFY — platform-specific, deferred to `adapters/pgsql`
- gRPC streaming bridge — separate adapter
