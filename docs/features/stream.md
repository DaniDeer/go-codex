# Reactive Stream Pipelines — `stream`

> See also: [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream) · [Stream Guide](../guides/stream.md) · [Stream Bridge Guide](../guides/stream-bridges.md) · [Observer Pattern](observer.md) · [Forge Pipelines](../concepts/pipelines.md)
>
> **Runnable demos:**
> - [`examples/stream-pipeline`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-pipeline) — all operators showcased: `From`, `Apply`, `Tap`, `Filter`, `CombineLatest2`, `Tee`, `Merge`, `FlatMapSlice`, `Debounce`, `Throttle`, `Buffer`, `Window`, `MapErr`, `Topology` + YAML render
> - [`examples/stream-oee`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-oee) — forge + stream integration: governed OEE from machine events (Window → governed forge chain → alert); governance YAML with SHA-256 hashes per function
> - [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — **stream bridge showcase**: `mqtt.SubscribeStream`, `mqtt.DrainPublish`, `nethttp.HandlerLatest` (reactive cache GET /readings/latest), and `sql.QueryStream` — all wired together with a single shared observer

The `stream` package provides a declarative reactive pipeline over typed Go channels,
bridging push-based transport adapters (MQTT, ZeroMQ) with governed
[`forge.Function`](../concepts/pipelines.md) computations.

---

## forge vs stream — complementary, not competing

`forge/` and `stream/` solve different problems:

| | `forge/` | `stream/` |
|---|---|---|
| Execution | Synchronous `Apply(In) → (Out, error)` — pull/batch | Asynchronous goroutine loops — push/reactive |
| Composition unit | `[]T` slices | `<-chan T` channels (per-item, continuous) |
| Governance | SHA-256 hash + Author/ApprovedBy — KPI audit trail | None |
| Spec output | `PipelineSpec` → YAML via `render/pipeline` | `TopologySpec` → YAML via `render/stream` |

They compose: `stream.Apply(ctx, mqttStream, forgeFunction, opts)` — the forge function's
validated computation runs per-item inside the reactive pipeline.

`stream/` is a **top-level package** (not an adapter) because it has no external library
dependency — only go-codex packages and Go stdlib.

---

## `Stream[T]` — explicit error channels

```go
type Stream[T any] struct {
    Values <-chan T     // successful items
    Errors <-chan error // per-item errors — stream continues after each
}
```

Values and errors travel on separate channels — idiomatic Go, no `interface{}` boxing,
no type switches. A failing item (bad sensor payload, forge validation failure) does not
terminate the pipeline: it goes to `Errors` and the pipeline continues.

**Consumers MUST drain both channels concurrently.** Use [`Drain`](#drain) as the safe
default sink — it handles both in a single select loop.

---

## Operators

### Sources

| Operator | What it does |
|---|---|
| `From[T](ctx, <-chan T) Stream[T]` | Wraps a typed channel. Errors channel is always empty. |
| `FromCodec[T](ctx, <-chan []byte, format.Format[T], opts) Stream[T]` | Decodes raw bytes using any format (JSON, YAML, TOML, custom). Decode/validation failures → `StreamDecodeError` on `Errors`. |
| `Single[T](ctx, v T) Stream[T]` | Emits `v` once and closes. Never writes to `Errors`. Used as per-request pipeline entry point in `PipelineHandlerFunc` and `AsPipelineFunc`. |
| `BroadcastHub[T]` | N-subscriber fan-out. `NewBroadcastHub(ctx, src, bufPerSubscriber)` starts the hub. Each `Subscribe()` returns a new `Stream[T]` with a private buffered channel; `Unsubscribe(s)` removes the subscriber. Non-blocking fan-out: slow subscribers drop items. Hub closes all subscriber channels when `ctx` is cancelled or `src` closes. Used internally by `SSEFromHub` and any fan-out scenario. |

### Transforms

| Operator | What it does |
|---|---|
| `Apply[In,Out](ctx, Stream[In], *forge.Function[In,Out], opts) Stream[Out]` | Applies a forge function per-item. Validation/compute failures → `StreamApplyError` on `Errors`. Fires `stats.StreamObserver.RecordStreamItem` + `stats.TraceObserver` span per item. |
| `Filter[T](ctx, Stream[T], pred func(T) bool) Stream[T]` | Drops items where pred returns false. Errors pass through. |
| `Tap[T](ctx, Stream[T], onValue func(T)) Stream[T]` | Domain event observation — calls `onValue` without transforming. Distinct from infrastructure metrics. |
| `MapErr[T](ctx, Stream[T], fn func(error) (T, bool, error)) Stream[T]` | Recover / reclassify / silence errors. |
| `Retry[T](ctx, Stream[T], fn func(error) (T, bool, error)) Stream[T]` | Alias for `MapErr` with a descriptive name for the retry pattern. |
| `FlatMapSlice[In,Out](ctx, Stream[In], func(In) []Out) Stream[Out]` | Each value maps to a slice; elements emitted individually. Empty slice = filter. |

### Fan-in / fan-out

| Operator | What it does |
|---|---|
| `Merge[T](ctx, ...Stream[T]) Stream[T]` | Combines multiple streams into one. |
| `Tee[T](ctx, Stream[T]) (Stream[T], Stream[T])` | Splits one stream into two copies. |
| `CombineLatest2[A,B,Out]` | Emits combined value whenever either source emits (after both have emitted once). |
| `CombineLatest3[A,B,C,Out]` | 3-source variant — ideal for OEE (Availability × Performance × Quality). |
| `CombineLatest4[A,B,C,D,Out]` | 4-source variant. |
| `Zip[A,B,Out](ctx, Stream[A], Stream[B], func(A,B) Out) Stream[Out]` | Pairs items by position: (a[0],b[0]), (a[1],b[1]), ... — unlike CombineLatest, waits for matched pairs. |

### Time operators

| Operator | What it does |
|---|---|
| `Buffer[T](ctx, Stream[T], n int, maxWait) Stream[[]T]` | Batches up to n items or until maxWait silence. Triggered by item arrival. |
| `Window[T](ctx, Stream[T], duration) Stream[[]T]` | Fixed-interval ticker; emits all items collected per window (even empty slices). Consistent time slot boundaries. |
| `SlidingWindow[T](ctx, Stream[T], size, step int) Stream[[]T]` | Overlapping windows: every `step` items, emit last `size` items. `step==size` = tumbling. |
| `Debounce[T](ctx, Stream[T], d) Stream[T]` | Emits only after silence of d — useful for sensor settling. |
| `Throttle[T](ctx, Stream[T], interval) Stream[T]` | At most one item per interval. |

### Sinks

| Operator | What it does |
|---|---|
| `Drain[T](ctx, Stream[T], onValue, onError, opts)` | Consumes both channels in a single select loop. Safe default sink. |
| `Collect[T](ctx, Stream[T]) ([]T, []error)` | Accumulates all items. For bounded streams and tests. |

### Topology (documentation)

| Type/Function | What it does |
|---|---|
| `Topology` | Declarative pipeline descriptor — `NewTopology(...).WithSource().WithApply(fn).WithSink()` |
| `WithApply[In,Out](topo, fn)` | Free function (required for generic type params) — captures forge function hash |
| `TopologySpec` | Machine-readable pipeline description |
| `render/stream.Render(spec)` | Serialises `TopologySpec` as YAML |

---

## Two observer kinds

### Infrastructure metrics — `stats.StreamObserver`

```go
type StreamObserver interface {
    RecordStreamItem(function string, success bool, dur time.Duration)
}
```

Type-asserted from `ApplyOptions.Observer`. Existing `Observer` implementations
need not change. `stats.NoopObserver`, `stats.LoggingObserver`, and `stats.NewFanout`
all implement it.

### Domain event observation — `Tap`

`Tap` is a first-class operator for observing typed business values flowing through the
pipeline — independent from infrastructure metrics:

```go
oeeStream = stream.Tap(ctx, oeeStream, func(oee OEE) {
    slog.Info("OEE computed", "value", float64(oee))
    dashboard.Publish(oee)   // domain event — not a counter
})
```

---

## Structured errors (all implement `slog.LogValuer`)

| Type | Returned by | Key fields |
|---|---|---|
| `StreamDecodeError` | `FromCodec` — payload decode/validation failure | `Source string`, `Err error` |
| `StreamApplyError` | `Apply` — forge function failure | `Function string`, `Err error` (inner `forge.InputError` etc.) |

Both implement `Error()`, `Unwrap()`, and `LogValue()`. Use `errors.As` to reach the
inner forge error from a `StreamApplyError`:

```go
var sae stream.StreamApplyError
if errors.As(err, &sae) {
    slog.Warn("apply failed", "error", sae) // structured: {function, err}
    var ie forge.InputError
    if errors.As(err, &ie) {
        // per-field validation detail
    }
}
```

---

## Design rationale — why `forge` and `stream` are separate packages

`forge/` and `stream/` were deliberately kept separate after an architectural
evaluation during development. The key arguments:

| Concern | `forge/` | `stream/` |
|---|---|---|
| Execution | Synchronous `Apply(In) → (Out, error)` — pull/batch | Async goroutine loops — push/reactive |
| Composition unit | `[]T` slices | `<-chan T` channels (per-item, continuous) |
| Governance | SHA-256 hash + Author/ApprovedBy — KPI audit trail | None |
| Spec output | `PipelineSpec` → YAML via `render/pipeline` | `TopologySpec` → YAML via `render/stream` |
| Same-named ops | `Map`/`Filter` over `[]T` | `Filter`/`FlatMapSlice` over `<-chan T` |

**Why not merge:**
1. Different execution models — a unified type would lose either batch simplicity or reactive capability
2. Governance belongs on forge functions (signed computations), not on stream operators (`Debounce`, `Throttle`)
3. Same names, different semantics — merging would require awkward disambiguation
4. Dependency direction is one-way and correct: `stream` imports `forge`; the reverse would create a circular dependency

**The correct conceptual model:**

```
codex/      Layer 1 — validated domain types
forge/      Layer 3 — governed synchronous computation + KPI spec
stream/     Layer 4 — reactive execution of forge functions over event streams
adapters/   Transport bridges (MQTT, ZeroMQ) supply source channels to stream/
```

- `forge/` = "what the computation **is**" (declarative, governed, signed)
- `stream/` = "how computation **runs** continuously over time" (reactive, async)

They compose: `stream.Apply(ctx, mqttStream, forgeFunction, opts)` — the forge function's
governed computation runs per-item inside the reactive pipeline.

---

## Stream bridge helpers

Each adapter package provides bridge helpers that connect the transport directly to a
`Stream[T]` — eliminating the boilerplate of creating raw channels, registering handlers,
and wiring `FromCodec`.

> Full examples and patterns: **[Stream Bridge Guide](../guides/stream-bridges.md)**

### `stream.Single[T]` — one-shot source

```go
s := stream.Single(ctx, req)    // emits req once, closes
```

Used as the entry point for per-request pipelines inside [`PipelineHandlerFunc`](#http-pipeline-handler).

### HTTP — `adapters/nethttp` and `adapters/chi`

Seven patterns bridge HTTP and the stream pipeline:

| Helper | Direction | Use case |
|--------|-----------|----------|
| `HandlerLatest[Req,Resp]` | stream → every HTTP GET | Returns latest value from a running pipeline ("get current OEE") |
| `HandlerIngest[Req]` | HTTP POST/PUT → stream | Feeds a channel → `stream.From` pipeline (webhook receiver) |
| `PipelineHandler[Req,Resp]` | per-request pipeline | Handler body uses `Tap`, `Apply`, `MapErr` |
| `SSEFromStream[Req,Event]` | stream → SSE (server) | Each client gets its own stream from a factory fn |
| `SSEFromHub[Req,Event]` | BroadcastHub → SSE (server) | All clients share one hub — live dashboard broadcast |
| `PollStream[Req,Resp]` | periodic HTTP GET → stream | Turns a polling endpoint into a continuous stream source |
| `DrainCall[Req,Resp]` | stream → HTTP POST/PUT | Posts each stream item to an external endpoint |
| `SSEClientStream[Req,Event]` | external SSE → stream | Consumes an upstream SSE feed; reconnects automatically |

`nethttp` returns `http.Handler`; `chi` returns `http.HandlerFunc`. Both packages provide
all helpers. `SSEClientStream`, `PollStream`, and `DrainCall` are nethttp-only (client-side).

```go
// Per-request pipeline
nethttp.RegisterPipeline(mux, handle,
    func(ctx context.Context, req SensorReq) stream.Stream[OEEResult] {
        s := stream.Single(ctx, req)
        s = stream.Apply(ctx, s, validateFn, opts)
        s = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("validated", "id", v.ID) })
        return stream.Apply(ctx, s, oeeCalcFn, opts)
    }, nethttp.Options{Observer: obs})

// SSE broadcast to all clients via hub
hub := stream.NewBroadcastHub(ctx, oeeStream, 32)
nethttp.RegisterSSE(mux, dashRoute,
    nethttp.SSEFromHub[struct{}, OEEResult](hub,
        nethttp.SSEStreamOptions{Topic: "/dashboard/sse", Observer: obs}),
    nethttp.Options{Observer: obs})

// Poll external service every 30s
sensorStream := nethttp.PollStream(ctx, httpClient, "http://sensor-api",
    sensorHandle, sensorReq{}, 30*time.Second,
    nethttp.PollStreamOptions{Vars: map[string]string{"id": "s-001"}, Observer: obs})

// Consume external SSE feed
events := nethttp.SSEClientStream(ctx, httpClient, "http://upstream",
    eventHandle, format.JSON(eventCodec),
    nethttp.SSEClientOptions{RetryDelay: 2*time.Second, Observer: obs})
```

New error types: `NoLatestValueError{Path}` (503), `PipelineFullError{Path,Capacity}` (503),
`PipelineNoResponseError{Path}` (500), `SSEWriteError{Path,Err}`, `SSEConnectError{URL,Attempt,Err}`,
`SSEParseError{URL,Line,Err}` — all implement `slog.LogValuer`.

### MQTT — `adapters/mqtt` and `adapters/mqtt5`

```go
// Source bridge: returns stream + handler to register with MQTT client
s, handler := mqtt.SubscribeStream(ctx, handle, format.JSON(codec), srcOpts, subOpts)
client.Subscribe(handle.Topic, 1, handler)

// Sink bridge: publish each stream item
mqtt.DrainPublish(ctx, client, handle, src, format.JSON(codec), opts)

// Pipeline handler for MQTT5 Serve (declarative handler with Tap)
mqtt5.Serve(ctx, client, router, handle,
    mqtt5.AsPipelineFunc(func(ctx context.Context, req Req) stream.Stream[Resp] {
        s := stream.Single(ctx, req)
        return stream.Apply(ctx, s, computeFn, opts)
    }), serveOpts)

// Client streaming: drive a request/reply service with a stream of requests
responses := mqtt5.CallStream(ctx, client, router, handle, requestStream, callOpts)
```

### ZeroMQ — `adapters/zeromq`

```go
// Source / sink
s := zeromq.SubscribeStream(ctx, sock, handle, format.JSON(codec), srcOpts)
zeromq.DrainPublish(ctx, sock, handle, src, format.JSON(codec), opts)

// Pipeline handler for Serve / ServeRouter
zeromq.Serve(ctx, sock, handle, zeromq.AsPipelineFunc(fn), serveOpts)

// Client streaming
responses := zeromq.CallStream(ctx, sock, handle, requestStream, opts)

// Reactive cache server: reply with latest pipeline value
zeromq.ServeLatest(ctx, sock, handle, oeeStream, serveLatestOpts)
```

### MCP — `adapters/mcpgo`

```go
// Reactive cache: returns the latest stream value to every LLM call
mcpgo.RegisterToolLatest(s, getOEEHandle, oeeStream, opts)

// Reactive trigger: each tool call runs a fresh pipeline (MCP ≡ nethttp.PipelineHandler)
mcpgo.RegisterToolPipeline(s, analyzeHandle,
    func(ctx context.Context, in OEEQuery) stream.Stream[OEEResult] {
        s  := stream.Single(ctx, in)
        s   = stream.Apply(ctx, s, validateFn, opts)
        s   = stream.Tap(ctx, s, func(v ValidatedQuery) { auditLog.Write(v) })
        return stream.Apply(ctx, s, oeeCalcFn, opts)
    }, opts)
```

### SQL — `adapters/sql`

```go
// Source: poll DB at interval, validate each row
s := sql.QueryStream(ctx, rowCodec, func(ctx context.Context) ([]Row, error) {
    return db.ListReadingsSince(ctx, time.Now().Add(-interval))
}, 30*time.Second, sql.QueryStreamOptions{Table: "readings", Op: "list"})

// Sink: validate + insert each stream item
sql.DrainInsert(ctx, rowCodec, src, db.InsertReading,
    sql.DrainInsertOptions{Table: "readings", Op: "insert", OnError: logErr})
```

### File — `adapters/file`

```go
// Source: decode NDJSON file line-by-line (bounded stream)
s, err := file.ScanStream(ctx, "readings.ndjson", format.JSON(codec), srcOpts)

// Source: watch directory, emit new file paths
paths := file.WatchStream(ctx, "/data/uploads", 500*time.Millisecond, srcOpts)

// Sink: write each item as a NDJSON line
file.DrainWrite(ctx, outFile, src, format.JSON(codec),
    file.DrainWriteOptions{Path: "out.ndjson", OnError: logErr})
```
