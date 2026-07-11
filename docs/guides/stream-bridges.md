# Stream Bridge Guide

> See also: [Feature: Reactive Streams](../features/stream.md) · [Stream Guide](stream.md) · [Observer Pattern](observer.md) · [Roadmap](../roadmap/index.md)
>
> **Runnable demos:**
> - [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — **primary bridge showcase** — `mqtt.SubscribeStream`, `mqtt.DrainPublish`, `nethttp.HandlerLatest`, `sql.QueryStream` all in one service; run with `go run ./examples/sensor-service`
> - [`examples/stream-pipeline`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-pipeline) — comprehensive stream operator showcase
> - [`examples/stream-oee`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-oee) — forge + stream OEE integration

Stream bridge helpers connect each adapter's transport to a `Stream[T]` in one call,
eliminating the boilerplate of raw channels and manual handler wiring.

Every bridge adapter imports `stream` — `stream` imports no adapters (no circular deps).

---

## `stream.Single[T]` — one-shot pipeline source

Before the bridges: a new primitive that makes per-request pipelines clean.

```go
s := stream.Single(ctx, req)
```

Emits `req` once, closes, never writes to `Errors`. Use it as the source inside
`PipelineHandlerFunc` and `AsPipelineFunc` when you want to run the full stream
operator API on a single request value:

```go
func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
    s   := stream.Single(ctx, req)
    s    = stream.Apply(ctx, s, validateFn, applyOpts)
    s    = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("validated", "id", v.ID) })
    out := stream.Apply(ctx, s, oeeCalcFn, applyOpts)
    return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
}
```

---

## HTTP bridges — `adapters/nethttp` and `adapters/chi`

Three bridge patterns cover the full HTTP ↔ stream lifecycle. All helpers take the
existing `Options` struct — no new option structs — and return `http.Handler`
(nethttp) or `http.HandlerFunc` (chi), matching the existing `Handler` API.

### Codec coverage — all HTTP layers applied by every bridge

All three bridges wrap the existing `Handler` function. Before calling the bridge fn,
`Handler` runs the full HTTP codec validation stack in this order:

| Layer | Codec applied | Error response |
|-------|--------------|----------------|
| Request body | `handle.Decode(body)` — body codec | 400 Bad Request |
| Query params | `ValidateQuery` — per-`QueryParam.Codec[string]` | 400 |
| Cookie params | `ValidateCookies` — per-`CookieParam.Codec[string]` | 400 |
| Header params | `ValidateHeaders` — per-`HeaderParam.Codec[string]` | 400 |
| Path params | `ValidatePathParams` — per-`PathParam.Codec[string]` | 400 |
| Security | credential codec + `SecurityFunc` | 401 |
| Response body | `handle.Encode(resp)` | 500 |
| Response headers | `ValidateResponseHeaders` | 500 |
| Response cookies | `ValidateResponseCookies` | 500 |

**All codec validation errors produce the correct HTTP status** regardless of which bridge is used.

**Accessing param values inside the bridge fn:**

| Bridge | Body `Req` | Path/query/cookie/header VALUES |
|--------|-----------|--------------------------------|
| `HandlerLatest` | Decoded + validated, then **discarded** | Not needed — fn returns cached value |
| `HandlerIngest` | Decoded + validated, **pushed to channel** | Validated but **not in channel item** — see below |
| `PipelineHandler` | Decoded + validated, **passed to fn** | Via `RequestFromContext(ctx)` inside fn |

### Pattern 1 — `HandlerLatest` / `RegisterLatest`

**Direction:** running `stream.Stream[Resp]` → every HTTP request

**Use case:** "get current OEE", "get latest sensor reading" — any REST endpoint backed
by a continuously running pipeline.

```go
// Start a continuous OEE pipeline:
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})

// Wire it to a GET endpoint:
nethttp.RegisterLatest(mux, oeeHandle, oeeStream, nethttp.Options{Observer: obs})
// GET /oee/current → {"availability":0.94,"performance":0.82,"quality":0.97,"oee":0.75}
// Before first value → 503 Service Unavailable
```

A background goroutine atomically stores each emitted value. Incoming HTTP requests
read the latest atomically — zero contention with the pipeline goroutine.

Errors from `Stream.Errors` are silently dropped by the background goroutine — the
latest value is unaffected. If the pipeline has not yet produced a value, the handler
calls `opts.ErrorHandler` with HTTP **503** and `nethttp.NoLatestValueError{Path}`.

Customise the "not ready" response by setting `opts.ErrorHandler`:

```go
nethttp.RegisterLatest(mux, oeeHandle, oeeStream, nethttp.Options{
    ErrorHandler: func(w http.ResponseWriter, r *http.Request, status int, err error) {
        var nlv nethttp.NoLatestValueError
        if errors.As(err, &nlv) {
            http.Error(w, `{"status":"warming_up"}`, http.StatusServiceUnavailable)
            return
        }
        // default handling for other errors
        http.Error(w, err.Error(), status)
    },
    Observer: obs,
})
```

### Pattern 2 — `HandlerIngest` / `RegisterIngest`

**Direction:** HTTP POST/PUT → `chan<- Req` → `stream.From` pipeline

**Use case:** HTTP ingestion endpoint (webhook receiver, batch upload trigger) that feeds
a reactive stream pipeline.

```go
// Create the channel and pipeline first:
ingestCh := make(chan SensorReading, 256)
sensorStream := stream.From(ctx, ingestCh)
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, stream.ApplyOptions{Observer: obs})

// Register the HTTP ingestion endpoint:
ingestHandle, _ := rest.NewRoute[SensorReading, struct{}]("POST", "/sensors/readings",
    readingCodec, codex.Struct[struct{}](), rest.RouteMeta{}).Register(b)

nethttp.RegisterIngest(mux, ingestHandle, ingestCh, nethttp.Options{Observer: obs})
// POST /sensors/readings → 201 {}
// When channel is full → 503 Service Unavailable (PipelineFullError)
```

The caller owns `ingestCh` — `HandlerIngest` never closes it. The send is non-blocking:
if the channel is at capacity, the handler calls `opts.ErrorHandler` with HTTP **503**
and `nethttp.PipelineFullError{Path, Capacity}`. Tune buffer size based on `Capacity`
from the error log.

**Including path/query/cookie/header param values in the channel item:**

`HandlerIngest` pushes only the body-decoded `Req` to the channel. Path params, query
params, cookies, and headers are codec-validated (errors produce 400) but their values
are not included in what's pushed. For a route like `POST /sensors/{sensorID}/readings`
where `sensorID` must reach the pipeline, use `Handler` directly with a custom fn:

```go
// Use Handler when param values must be included in the channel item:
nethttp.Register(mux, ingestHandle,
    func(ctx context.Context, body SensorBody) (struct{}, error) {
        r, _ := nethttp.RequestFromContext(ctx)
        sensorID := r.PathValue("sensorID") // already codec-validated by Handler
        select {
        case ingestCh <- SensorReading{SensorID: sensorID, Value: body.Value}:
            return struct{}{}, nil
        default:
            return struct{}{}, nethttp.PipelineFullError{
                Path:     ingestHandle.Descriptor.Path,
                Capacity: cap(ingestCh),
            }
        }
    }, nethttp.Options{Observer: obs})
```

`HandlerIngest` is the convenience shortcut for body-only ingestion.

### Pattern 3 — `PipelineHandler` / `RegisterPipeline`

**Direction:** per-request pipeline (each HTTP request builds and runs a mini-stream)

**Use case:** multi-step handlers where you want `Tap` to declaratively observe
intermediate computation stages — the same API as background pipelines.

**Why not a plain `HandlerFunc`?** In a plain handler, observers are inline side effects
visually mixed with computation. With `PipelineHandler`, the `Tap` calls are structurally
separated — a quick glance shows what observes vs what computes:

```go
// Plain handler — hard to see where computation ends and observation begins
func(ctx, req SensorReq) (OEEResult, error) {
    norm, err := normalizeFn.ApplyContext(ctx, req)
    if err != nil { return zero, err }
    slog.Info("normalized", "v", norm)   // observer buried in logic
    result, err := oeeCalcFn.ApplyContext(ctx, norm)
    metrics.Record(result)               // observer buried in logic
    return result, nil
}

// PipelineHandler — observers are explicit via Tap
nethttp.RegisterPipeline(mux, oeeHandle,
    func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s   := stream.Single(ctx, req)
        s    = stream.Apply(ctx, s, normalizeFn, applyOpts)
        s    = stream.Tap(ctx, s, func(v Normalized) { slog.Info("normalized", "v", v) })
        out := stream.Apply(ctx, s, oeeCalcFn, applyOpts)
        return stream.Tap(ctx, out, func(r OEEResult) { metrics.Record(r) })
    },
    nethttp.Options{Observer: obs})
```

`PipelineHandler` is a thin wrapper over `Handler` — all codec validation, param
validation, security enforcement, and observer calls follow the same path as a plain
`Handler`. Internally it calls `stream.Collect` and returns the first value.

**Semantics:**
- Error takes precedence over values (`Stream.Errors` first → HTTP 500).
- Multiple emitted values: only the first is used; extras silently discarded.
- Context cancelled before any value: `PipelineNoResponseError{Path}` → HTTP 500.

**Accessing path/query/cookie/header param values inside the pipeline:**

The fn receives `(ctx, req)`. The body `req` is fully decoded. To access path, query,
cookie, or header param values (all already codec-validated by `Handler`), call
`RequestFromContext(ctx)` anywhere in the fn — including inside `Tap` or forge functions:

```go
nethttp.RegisterPipeline(mux, handle,
    func(ctx context.Context, body SensorBody) stream.Stream[OEEResult] {
        r, _ := nethttp.RequestFromContext(ctx)
        sensorID := r.PathValue("sensorID") // already validated as UUID by PathParam codec

        s := stream.Single(ctx, body)
        s = stream.Tap(ctx, s, func(v SensorBody) {
            slog.Info("request", "sensor", sensorID, "value", v.Value)
        })
        return stream.Apply(ctx, s, oeeCalcFn, applyOpts)
    }, nethttp.Options{Observer: obs})
```

**Setting response headers/cookies from within the pipeline:**

Call `nethttp.WithResponseHeaders(ctx, h)` or `nethttp.WithResponseCookies(ctx, ...)` anywhere
inside the pipeline fn. The maps stored in `ctx` are reference types — writes from stream
goroutines are visible to `Handler` after `stream.Collect` returns. Safe for sequential
pipelines (`Single` → `Apply` chain). Avoid concurrent writes to response headers from
parallel operators (`CombineLatest`, `Merge`).

```go
return stream.Tap(ctx, resultStream, func(r OEEResult) {
    h := make(http.Header)
    h.Set("X-OEE-Sensor", r.SensorID) // set response header from pipeline
    nethttp.WithResponseHeaders(ctx, h)
})
```

### Pattern 4 — `SSEFromStream` / `SSEFromHub`

**Direction:** `stream.Stream[Event]` → streaming SSE response (server → browser/client)

**Use case:** live dashboards, real-time feeds, streaming computation results to browser
clients. The SSE helper adapts a Go stream into the `SSEHandlerFunc` type accepted by
`SSEHandler` and `RegisterSSE`.

#### Per-client personalised stream — `SSEFromStream`

Each connecting SSE client calls `streamFactory(ctx, req)` — the fn receives the decoded
`Req` so each client can get a filtered or personalised view:

```go
// Each client gets OEE filtered to the machines they own:
nethttp.RegisterSSE(mux, dashboardRoute,
    nethttp.SSEFromStream(
        func(ctx context.Context, req DashboardReq) stream.Stream[OEEResult] {
            return stream.Filter(ctx, sharedOEEStream, req.MatchesMachine)
        },
        nethttp.SSEStreamOptions{
            Topic:    dashboardRoute.Descriptor.Path,
            Observer: obs,
            OnError:  logErr,
        }),
    nethttp.Options{Observer: obs})
```

When the client disconnects, `ctx` is cancelled and the factory's stream goroutines
terminate.

#### Shared broadcast hub — `SSEFromHub`

All clients share one `stream.BroadcastHub[T]`. Each client subscribes on connect and
is automatically unsubscribed on disconnect. Non-blocking fan-out: slow clients drop
items rather than blocking the hub.

```go
// Create the hub once from the shared OEE stream:
hub := stream.NewBroadcastHub(ctx, oeeStream, 32)

// Wire the hub to the SSE endpoint — all clients receive every event:
nethttp.RegisterSSE(mux, dashboardRoute,
    nethttp.SSEFromHub[struct{}, OEEResult](hub,
        nethttp.SSEStreamOptions{
            Topic:    dashboardRoute.Descriptor.Path,
            Observer: obs,
        }),
    nethttp.Options{Observer: obs})
```

`SSEStreamOptions` configures both helpers:

| Field | Purpose |
|-------|---------|
| `Topic string` | SSE route path — used in observer calls and `SSEWriteError` context |
| `OnError func(error)` | Called for `SSEWriteError` (client disconnect) and upstream stream errors |
| `Observer stats.Observer` | `RecordSubscribe` per event; `TraceObserver` spans wrap each send |

**`SSEHandler` header timing:** response headers (`WriteHeader(200)`) are committed on
the **first `send` call**, not at connection time. Staged response headers/cookies
(`WithResponseHeaders`, `WithResponseCookies`) must be set BEFORE calling `send`. When
writing integration tests that connect multiple SSE clients sequentially, connect them
in goroutines so the first event unblocks all `Do()` calls simultaneously.

Both `nethttp` and `chi` provide identical `SSEFromStream` and `SSEFromHub` helpers.

### Pattern 5 — `PollStream`

**Direction:** periodic HTTP GET → `stream.Stream[Resp]`

**Use case:** turn a polling REST endpoint into a continuous stream source without
external triggers.

```go
// Poll GET /sensors/latest every 30 seconds:
sensorStream := nethttp.PollStream(ctx, httpClient, "http://sensor-api:8080",
    sensorHandle,
    sensorReq{},
    30*time.Second,
    nethttp.PollStreamOptions{
        Vars:     map[string]string{"sensorID": "s-001"}, // static path vars
        Observer: obs,
        Buffer:   8,
    })

oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, applyOpts)
```

`Call` errors (network, non-2xx status, codec failure) go to `Stream.Errors` as typed
errors (`UnexpectedStatusError`, `RequestError`, etc.). The stream runs indefinitely
until `ctx` is cancelled.

**`Vars` field:** `PollStreamOptions.Vars` substitutes `{varName}` placeholders in the
route path template (same static-vars limitation as `DrainPublish` — one map for all
polls). For routes without path vars, omit `Vars`.

### Pattern 6 — `DrainCall`

**Direction:** `stream.Stream[Req]` → HTTP POST/PUT (client sink)

**Use case:** publish each computed stream item to an external HTTP endpoint — the
stream equivalent of a manual `Call` loop.

```go
// Post each OEE result to an external aggregation service:
nethttp.DrainCall(ctx, httpClient, "http://aggregator:9090",
    oeePostHandle,
    oeeStream,
    nethttp.DrainCallOptions{
        Vars:    map[string]string{"tenantID": "acme"},
        OnError: logErr,
        CallOpts: nethttp.CallOptions{Observer: obs},
    })
```

`Vars` is static — same map applied to every item. For per-item path var substitution
(e.g. `{sensorID}` extracted from each payload), use `stream.Drain` with `Call` directly.

### Pattern 7 — `SSEClientStream`

**Direction:** external SSE endpoint → `stream.Stream[Event]` (client source)

**Use case:** consume a server-sent events feed from another service — the stream
receives every decoded event and reconnects automatically on disconnect.

```go
events := nethttp.SSEClientStream(ctx, httpClient,
    "http://upstream-service:8080",
    eventHandle,
    format.JSON(eventCodec),
    nethttp.SSEClientOptions{
        Vars:          map[string]string{"machineID": "m-42"},
        RetryDelay:    2 * time.Second,   // initial reconnect wait (default 1s)
        MaxRetryDelay: 30 * time.Second,  // exponential backoff cap (default 30s)
        Observer:      obs,
        Buffer:        16,
    })

oeeStream := stream.Apply(ctx, events, oeeCalcFn, applyOpts)
```

The URL is built using `handle.BuildPath(opts.Vars)` — `{varName}` placeholders in the
SSE route path are substituted before connecting. A build failure emits
`SSEConnectError` immediately and terminates the stream.

Reconnect behaviour:
- On connection drop: exponential backoff from `RetryDelay`, capped at `MaxRetryDelay`
- Each connect attempt increments the `Attempt` counter in `SSEConnectError`
- `ctx` cancellation terminates the stream cleanly

### HTTP bridge error types

All implement `slog.LogValuer`. Use `errors.As` in a custom `ErrorHandler`:

| Error | Status | Trigger |
|-------|--------|---------|
| `NoLatestValueError{Path}` | 503 | `HandlerLatest` before first value |
| `PipelineFullError{Path, Capacity}` | 503 | `HandlerIngest` channel full |
| `PipelineNoResponseError{Path}` | 500 | `PipelineHandler` no value produced |
| `SSEWriteError{Path, Err}` | — | `SSEFromStream`/`SSEFromHub` write to disconnected client |
| `SSEConnectError{URL, Attempt, Err}` | — | `SSEClientStream` connect failure or path build error |
| `SSEParseError{URL, Line, Err}` | — | `SSEClientStream` SSE data line decode failure |

---

## `stream.BroadcastHub[T]`

`BroadcastHub[T]` fans out one source `Stream[T]` to N independent subscribers. Each
subscriber gets its own buffered channel — slow subscribers drop items rather than
blocking each other or the hub goroutine.

```go
hub := stream.NewBroadcastHub(ctx, oeeStream, 32) // 32-item buffer per subscriber

sub1 := hub.Subscribe() // returns Stream[T] — call before hub emits
sub2 := hub.Subscribe()

// Subscribe on disconnect:
defer hub.Unsubscribe(sub1)
```

| Concept | Behaviour |
|---------|-----------|
| Fan-out | Non-blocking: full subscriber buffer → item dropped silently |
| Error fan-out | Both `Values` and `Errors` are fanned out to all subscribers |
| Hub exit | `ctx` cancel or source closes → hub closes all subscriber channels |
| Subscribe after hub exits | Returns already-closed channels — safe to use |

`SSEFromHub` uses `BroadcastHub` internally: one `Subscribe()` per SSE client,
`Unsubscribe()` on disconnect. The hub is independent of the SSE layer and can back
any fan-out scenario.

---

## MQTT bridge — `adapters/mqtt`

MQTT v3/v3.1.1 (Paho). Returns both the stream AND the message handler (MQTT's
callback model requires the caller to register the handler with the MQTT client).

### Source — `SubscribeStream`

```go
s, handler := mqtt.SubscribeStream(ctx, sensorHandle,
    format.JSON(sensorCodec),
    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs},
    mqtt.SubscribeOptions{Observer: obs})

// Register with the MQTT client before messages can flow:
client.Subscribe(sensorHandle.Topic, 1, handler)

// Now compose the pipeline:
oeeStream := stream.Apply(ctx, s, oeeCalcFn, stream.ApplyOptions{Observer: obs})
```

Decode/validation failures go to `Stream.Errors` as `StreamDecodeError`. The stream
terminates when `ctx` is cancelled. The caller owns the subscription lifecycle —
`SubscribeStream` never calls `Unsubscribe`.

### Sink — `DrainPublish`

```go
alertStream, archiveStream := stream.Tee(ctx, oeeStream)

go mqtt.DrainPublish(ctx, client, alertHandle, alertStream,
    format.JSON(alertCodec),
    mqtt.MQTTDrainPublishOptions{
        QoS:      1,
        Retained: true,    // subscribers always get the latest OEE on connect
        OnError:  logErr,
        Observer: obs,
    })
```

---

## MQTT5 bridge — `adapters/mqtt5`

MQTT v5 (paho.golang). Adds `AsPipelineFunc` for the server side and `CallStream`
for streaming request/reply.

### Source — `SubscribeStream`

```go
s, rawHandler := mqtt5.SubscribeStream(ctx, sensorHandle,
    format.JSON(sensorCodec),
    stream.SourceOptions{Name: "mqtt5/sensors/+", Observer: obs},
    mqtt5.SubscribeOptions{Observer: obs})

router.RegisterHandler(sensorHandle.Topic, rawHandler)
```

### Sink — `DrainPublish`

```go
mqtt5.DrainPublish(ctx, client, alertHandle, alertStream,
    format.JSON(alertCodec),
    mqtt5.MQTT5DrainPublishOptions{
        QoS:      1,
        Retained: true,
        OnError:  logErr,
        Observer: obs,
    })
```

### Server pipeline — `AsPipelineFunc`

Use `AsPipelineFunc` when the `Serve` handler body benefits from `Tap`, multi-step
`Apply`, or `MapErr`. It wraps a `func(ctx, Req) stream.Stream[Resp]` as a plain
`func(ctx, Req) (Resp, error)` accepted by `Serve` — no changes to the `Serve` API.

```go
mqtt5.Serve(ctx, client, router, oeeHandle,
    mqtt5.AsPipelineFunc(func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s  := stream.Single(ctx, req)
        s   = stream.Apply(ctx, s, validateFn, applyOpts)
        s   = stream.Tap(ctx, s, func(v ValidatedReq) {
            slog.Info("request validated", "sensor", v.SensorID)
        })
        out := stream.Apply(ctx, s, oeeCalcFn, applyOpts)
        return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
    }),
    mqtt5.ServeOptions{Observer: obs})
```

For simple single-step handlers, use a plain `func` with `Serve` directly — no overhead.

### Client streaming — `CallStream`

Stream many requests through a request/reply service and collect responses as a stream:

```go
rawReadings := stream.FromCodec(ctx, rawCh, format.JSON(readingCodec), srcOpts)

validated := mqtt5.CallStream(ctx, client, router, validateHandle,
    rawReadings,
    mqtt5.CallOptions{ReplyTopicBuilder: mqtt5.UUIDReplyTopic("reply"), Observer: obs})

oeeStream := stream.Apply(ctx, validated, oeeCalcFn, applyOpts)
```

New error: `PipelineNoResponseError{Topic}` — when `AsPipelineFunc` pipeline emits no value.

---

## ZeroMQ bridge — `adapters/zeromq`

ZeroMQ via the `FramedSocket` interface (no CGO in the adapter; wrap `pebbe/zmq4`
or any other library). All five bridge patterns are supported.

### Source — `SubscribeStream`

```go
s := zeromq.SubscribeStream(ctx, subSock, handle, format.JSON(codec),
    stream.SourceOptions{Name: "zmq/sensors", Observer: obs})

oeeStream := stream.Apply(ctx, s, oeeCalcFn, applyOpts)
```

Socket errors terminate the goroutine; the stream closes and all downstream operators
drain naturally.

### Sink — `DrainPublish`

```go
go zeromq.DrainPublish(ctx, pubSock, alertHandle, alertStream,
    format.JSON(alertCodec),
    zeromq.DrainPublishOptions{Observer: obs, OnError: logErr})
```

### Server pipeline — `AsPipelineFunc`

Works with both `Serve` (REP) and `ServeRouter` (ROUTER/DEALER):

```go
go zeromq.Serve(ctx, repSock, oeeHandle,
    zeromq.AsPipelineFunc(func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s  := stream.Single(ctx, req)
        s   = stream.Apply(ctx, s, validateFn, applyOpts)
        s   = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("valid", "id", v.ID) })
        return stream.Apply(ctx, s, oeeCalcFn, applyOpts)
    }),
    zeromq.ServeOptions{Observer: obs})
```

### Client streaming — `CallStream`

REQ socket (sequential — one request at a time):

```go
responses := zeromq.CallStream(ctx, reqSock, handle, requestStream,
    zeromq.CallStreamOptions{Observer: obs})
```

### Reactive cache server — `ServeLatest`

The "reactive cache" pattern: a REP socket that answers every request with the most
recently computed value from a running stream pipeline. No computation per request —
just an atomic load of the latest value.

```go
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, applyOpts)
latestOEE, archiveOEE := stream.Tee(ctx, oeeStream)

go stream.Drain(ctx, archiveOEE, db.InsertOEE, logErr, stream.DrainOptions{})

go func() {
    if err := zeromq.ServeLatest(ctx, repSock, oeeQueryHandle, latestOEE,
        zeromq.ServeLatestOptions{Observer: obs,
            OnError: func(e error) {
                var nv zeromq.NoLatestValueError
                if errors.As(e, &nv) {
                    slog.Warn("no value yet", "topic", nv.Topic)
                }
            },
        }); err != nil {
        slog.Error("serve stopped", "err", err)
    }
}()
```

New error types: `ServeLatestError{Op,Err}`, `NoLatestValueError{Topic}`,
`CorrelationError{Seq,Err}`, `PipelineNoResponseError{Topic}`.

---

## MCP bridge — `adapters/mcpgo`

Two patterns connect stream pipelines to MCP tools. Both expose the computation to
LLM agents; they differ in WHEN the pipeline runs:

| | `ToolLatestHandler` | `ToolPipelineHandler` |
|---|---|---|
| Pattern | Reactive CACHE | Reactive TRIGGER |
| Pipeline lifecycle | Runs continuously in background | Fresh run per tool call |
| Response | Latest pre-computed value | Value produced by this call's pipeline |
| Use case | "get current OEE" | "compute OEE for this specific input" |

### `ToolLatestHandler` — reactive cache tool

```go
// Continuously compute OEE in the background:
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn, applyOpts)

// Expose as an MCP tool:
mcpgo.RegisterToolLatest(s, getOEEHandle, oeeStream, mcpgo.Options{Observer: obs})

// LLM call result:
// {"availability":0.94,"performance":0.82,"quality":0.97,"oee":0.75}
// Before first value: IsError=true, "no value computed yet"
```

The "no value yet" response uses `mcp.NewToolResultError` (`IsError: true`) — the LLM
sees the message in the tool result, consistent with how `ToolHandler` reports input
errors. Not a Go error.

### `ToolPipelineHandler` — reactive trigger tool

The MCP equivalent of [`PipelineHandler`](#pattern-3----pipelinehandler--registerpipeline)
for HTTP. Each tool call **triggers a fresh pipeline run**: the tool arguments flow
through stream operators, and the first emitted value becomes the tool response.

**Why not a plain `ToolHandler`?** Same reason as `PipelineHandler` for HTTP: in a plain
handler, observers are inline side effects mixed with computation. With
`ToolPipelineHandler`, `Tap` calls are structurally separated:

```go
// Plain ToolHandler — observers buried in logic
mcpgo.RegisterTool(s, analyzeHandle, func(ctx context.Context, in OEEQuery) (OEEResult, error) {
    validated, err := validateFn.ApplyContext(ctx, in)
    if err != nil { return zero, err }
    slog.Info("validated", "query", validated) // observer buried in logic
    result, err := oeeCalcFn.ApplyContext(ctx, validated)
    auditLog.Write(result)                     // observer buried in logic
    return result, nil
}, opts)

// ToolPipelineHandler — observers explicit via Tap
mcpgo.RegisterToolPipeline(s, analyzeHandle,
    func(ctx context.Context, in OEEQuery) stream.Stream[OEEResult] {
        s  := stream.Single(ctx, in)
        s   = stream.Apply(ctx, s, validateFn, applyOpts)
        s   = stream.Tap(ctx, s, func(v ValidatedQuery) { slog.Info("validated", "query", v) })
        out := stream.Apply(ctx, s, oeeCalcFn, applyOpts)
        return stream.Tap(ctx, out, func(r OEEResult) { auditLog.Write(r) })
    }, mcpgo.Options{Observer: obs})
```

**Semantics:**
- Error from `Stream.Errors` → `mcp.NewToolResultError(err.Error())` with `IsError: true`
- No value produced → `mcp.NewToolResultError("tool pipeline produced no result")`
- Multiple values → only the first is used; extras silently discarded
- Input validation by `handle.Decode` runs before `fn` is called (same as `ToolHandler`)

**Cross-transport symmetry:** the same `func(ctx, In) stream.Stream[Out]` pipeline fn
works for HTTP via `nethttp.PipelineHandler`, for MQTT5 via `AsPipelineFunc`, for ZeroMQ
via `AsPipelineFunc`, and for MCP via `ToolPipelineHandler`:

```go
// One pipeline fn — same Tap observers for all transports
pipeline := func(ctx context.Context, in OEEQuery) stream.Stream[OEEResult] {
    s  := stream.Single(ctx, in)
    s   = stream.Apply(ctx, s, validateFn, applyOpts)
    s   = stream.Tap(ctx, s, func(v ValidatedQuery) { auditLog.Write(v) })
    return stream.Apply(ctx, s, oeeCalcFn, applyOpts)
}

// HTTP endpoint: POST /oee/compute
nethttp.RegisterPipeline(mux, httpHandle, pipeline, nethttp.Options{Observer: obs})

// MCP tool: "compute_oee"
mcpgo.RegisterToolPipeline(s, mcpHandle, pipeline, mcpgo.Options{Observer: obs})

// MQTT5: for each incoming request, run the pipeline and reply
mqtt5.Serve(ctx, client, router, mqtt5Handle,
    mqtt5.AsPipelineFunc(pipeline), mqtt5.ServeOptions{Observer: obs})
```

The forge function `oeeCalcFn`, Tap observers, and error handling are identical
across all three transports. Only the transport wrapper differs.

---

## SQL bridge — `adapters/sql`

Connects SQL query results and inserts to stream pipelines. Each row is validated
through the codec before entering or leaving the stream.

### Source — `QueryStream`

Poll a query at a fixed interval. Each result set is validated per-row; validation
failures go to `Stream.Errors` as `RowValidationError`; database errors as
`QueryStreamError`.

```go
readingsStream := sql.QueryStream(ctx, readingCodec,
    func(ctx context.Context) ([]Reading, error) {
        // Use a timestamp cursor to avoid re-emitting old rows:
        return db.ListReadingsSince(ctx, time.Now().Add(-30*time.Second))
    },
    30*time.Second,
    sql.QueryStreamOptions{
        Table:    "sensor_readings",
        Op:       "list_readings_since",
        Observer: obs,
    })

oeeStream := stream.Apply(ctx, readingsStream, oeeCalcFn, applyOpts)
```

### Sink — `DrainInsert`

Validate + insert each stream item. Codec validation failures go to `OnError` as
`RowValidationError`; insert errors as `InsertStreamError`.

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
    })
```

New error types: `QueryStreamError{Table,Op,Err}`, `InsertStreamError{Table,Op,Err}`.

---

## File bridge — `adapters/file`

Stdlib-only (no external dependencies). Three helpers for file-based sources and sinks.

### Source — `ScanStream`

Reads a newline-delimited file (NDJSON, CSV, TSV, …) line by line and decodes each
line. The stream is **bounded** — it terminates after EOF. Use `stream.Collect` for
one-shot aggregation or chain into other operators for processing.

```go
s, err := file.ScanStream(ctx, "readings.ndjson", format.JSON(readingCodec),
    stream.SourceOptions{Name: "readings.ndjson", Observer: obs})
if err != nil {
    // ScanError{Path, Err} — file could not be opened
    return err
}

vals, errs := stream.Collect(ctx, s)
// errs contains StreamDecodeError for any malformed lines
```

### Source — `WatchStream`

Polls a directory and emits the absolute path of each new file using `os.ReadDir`.
**No external fsnotify dependency.** Already-seen files are tracked in memory and not
re-emitted. The stream runs indefinitely until `ctx` is cancelled.

```go
newFiles := file.WatchStream(ctx, "/data/uploads", 500*time.Millisecond,
    stream.SourceOptions{Observer: obs})

// FlatMapSlice: for each new file path, scan + collect its contents
parsed := stream.FlatMapSlice(ctx, newFiles, func(path string) []Reading {
    s, err := file.ScanStream(ctx, path, format.JSON(readingCodec), stream.SourceOptions{})
    if err != nil {
        return nil // could not open file — skip
    }
    vals, _ := stream.Collect(ctx, s)
    return vals
})
```

`WatchError{Dir,Err}` is sent to `Stream.Errors` on `ReadDir` failure; the stream
continues on the next poll interval.

### Sink — `DrainWrite`

Encodes each stream item and writes it as a line to any `io.Writer`. Default separator
is `"\n"` (NDJSON). Encode or write failures go to `opts.OnError` as `WriteError`.

```go
outFile, _ := os.Create("oee-results.ndjson")
defer outFile.Close()

file.DrainWrite(ctx, outFile, oeeStream, format.JSON(oeeCodec),
    file.DrainWriteOptions{
        Path:    "oee-results.ndjson",
        OnError: logErr,
    })
```

---

## Bridge error reference

All bridge errors implement `slog.LogValuer` for zero-effort structured logging.
Errors with an inner `Err` field implement `Unwrap()` for `errors.As` chain traversal.
Terminal errors (no inner cause) do not implement `Unwrap()`.

| Error | Package | Fields | `Unwrap` |
|-------|---------|--------|---------|
| `NoLatestValueError` | nethttp, chi | `Path string` | — |
| `PipelineFullError` | nethttp, chi | `Path string`, `Capacity int` | — |
| `PipelineNoResponseError` | nethttp, chi | `Path string` | — |
| `PipelineNoResponseError` | mqtt5, zeromq | `Topic string` | — |
| `SSEWriteError` | nethttp, chi | `Path string`, `Err error` | ✓ |
| `SSEConnectError` | nethttp | `URL string`, `Attempt int`, `Err error` | ✓ |
| `SSEParseError` | nethttp | `URL string`, `Line string`, `Err error` | ✓ |
| `ServeLatestError` | zeromq | `Op string`, `Err error` | ✓ |
| `NoLatestValueError` | zeromq | `Topic string` | — |
| `CorrelationError` | zeromq | `Seq uint64`, `Err error` | ✓ |
| `QueryStreamError` | sql | `Table string`, `Op string`, `Err error` | ✓ |
| `InsertStreamError` | sql | `Table string`, `Op string`, `Err error` | ✓ |
| `ScanError` | file | `Path string`, `Err error` | ✓ |
| `WatchError` | file | `Dir string`, `Err error` | ✓ |
| `WriteError` | file | `Path string`, `Err error` | ✓ |

Log any bridge error with slog:

```go
slog.Error("bridge error", "error", err) // slog calls LogValue() — fully structured
```

---

## Combining bridges: end-to-end example

> **Full runnable version:** [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — uses `mqtt.SubscribeStream`, `nethttp.HandlerLatest`, `mqtt.DrainPublish`, and `sql.QueryStream` in one service. Run with `go run ./examples/sensor-service`.

MQTT5 sensor readings → OEE pipeline → HTTP "current OEE" endpoint + file archive:

```go
// 1. MQTT5 source
s, rawHandler := mqtt5.SubscribeStream(ctx, sensorHandle,
    format.JSON(sensorCodec), stream.SourceOptions{Observer: obs}, subOpts)
router.RegisterHandler(sensorHandle.Topic, rawHandler)

// 2. Pipeline
oeeStream := stream.Apply(ctx, s, oeeCalcFn, stream.ApplyOptions{Observer: obs})
oeeStream  = stream.Tap(ctx, oeeStream, func(r OEEResult) {
    slog.Info("OEE computed", "oee", r.OEE)
})

// 3. Fan-out: HTTP latest + file archive
latestOEE, archiveOEE := stream.Tee(ctx, oeeStream)

// HTTP endpoint: GET /oee/current → latest OEE
nethttp.RegisterLatest(mux, oeeHandle, latestOEE, nethttp.Options{Observer: obs})

// File archive: append each OEE result to NDJSON
archiveFile, _ := os.Create("oee-archive.ndjson")
go file.DrainWrite(ctx, archiveFile, archiveOEE, format.JSON(oeeResultCodec),
    file.DrainWriteOptions{Path: "oee-archive.ndjson", OnError: logErr})
```
