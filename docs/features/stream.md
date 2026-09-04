# Reactive Stream Pipelines — `stream`

> See also: [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream) · [Stream Guide](../guides/stream.md) · [Ports Guide](../guides/ports.md) · [Ports Feature](ports.md) · [Observer Pattern](observer.md) · [Forge Pipelines](../concepts/pipelines.md)
>
> **Runnable demos:**
> - [`examples/stream-pipeline`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-pipeline) — all operators showcased: `From`, `Apply`, `Tap`, `Filter`, `CombineLatest2`, `Tee`, `Merge`, `FlatMapSlice`, `Debounce`, `Throttle`, `Buffer`, `Window`, `MapErr`, `Switch`, `GroupBy`, `Topology` + YAML render
> - [`examples/stream-oee`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-oee) — forge + stream integration: governed OEE from machine events (Window → governed forge chain → alert); governance YAML with SHA-256 hashes per function
> - [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — **flagship port showcase**: one coherent use case (MQTT ingest → SQL persist → threshold alert → REST time series → REST-triggered file export) where every IO hop is a port — `mqtt.SubscribeAdapter`/`PublishAdapter`, `sql.QueryEachAdapter`, `file.DrainWriteFileAdapter`, `nethttp.PipelineAdapter`, `nethttp.HandlerLatest` — with a single shared observer

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
| `Map[In,Out](ctx, Stream[In], fn func(In) (Out, error), opts) Stream[Out]` | Typed 1→1 transform with an error path — lighter than `Apply` when no forge governance is needed. Failures → `StreamMapError` on `Errors`. |
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

More than 4 sources: nest CombineLatest calls (see the [stream guide](../guides/stream.md)).

### Routing

| Operator | What it does |
|---|---|
| `Switch[T](ctx, Stream[T], []Case[T], opts) ([]Stream[T], Stream[T])` | Routes each item to the FIRST matching named case (`out[i]` ↔ `cases[i]`); non-matches and src errors go ONLY to the rest stream. Panics on malformed cases (empty/duplicate `Name`, nil `When`) — a programming error, not a runtime failure. |
| `Case[T]{Name, When}` / `CaseConstraint[T](name, codex.Constraint[T])` | Named predicate case; `CaseConstraint` reuses a codec constraint's `Check` as the predicate — validation vocabulary doubles as routing vocabulary. |
| `SwitchKey[T,K](ctx, Stream[T], keys []K, keyOf func(T) K, opts)` | Keyed static variant — share a `TaggedUnion`'s named discriminator function so wire format, spec, and routing use ONE declaration. |
| `GroupBy[T,K](ctx, Stream[T], key func(T) K, onKey, opts)` | Dynamic per-key sub-streams; `onKey(k, sub)` fires once per new key on the dispatch goroutine (start consumers, don't run them inline). Blocks until src closes; sub-streams close with the parent. Keys are unbounded. Errors fan out non-blocking to all active keys. |
| `OfType[U,T](ctx, Stream[T]) Stream[U]` | Typed filter over an interface (sum-typed) stream; other types dropped silently, errors forwarded. Observer from ctx, location `"oftype"`. |
| `SwitchType2[A,B,T]` / `SwitchType3[A,B,C,T]` | Typed multi-case routing over a sum-typed stream: typed case streams + untyped rest. First match wins; errors → rest. |
| `SplitEither[A,B](ctx, Stream[codex.Either[A,B]], opts) (Stream[A], Stream[B])` | TOTAL split of a codec-native binary union — no rest stream (closed sum); errors fan out to both branches. |

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
| `Topology.WithSwitch(desc)` / `Topology.WithGroupBy(desc)` | Document routing steps (`StepKindSwitch` / `StepKindGroupBy`) |
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

## Connecting streams to transports — the `ports` package

Rather than importing a transport package directly into pipeline code, go-codex wires
streams to the outside world through **[`ports`](ports.md)** — a protocol-agnostic
binding layer. A pipeline declares a typed port; the transport is bound separately
(typically in `main.go`), so swapping MQTT for HTTP never touches domain logic.

> Full patterns and adapter catalogue: **[Ports Guide](../guides/ports.md)** · **[Ports Feature](ports.md)**

### `stream.Single[T]` — one-shot source

```go
s := stream.Single(ctx, req)    // emits req once, closes
```

Used as the entry point for per-request pipelines inside a `ports.ToolPort` pipeline
function, or any `AsPipelineFunc`-wrapped handler.

### The four port types, in one line each

| Port | Direction | Example adapters |
|------|-----------|------------------|
| `ports.SourcePort[T]` | external → stream (fan-in) | `mqtt5.SubscribeAdapter`, `nethttp.IngestAdapter`, `sql.QueryAdapter`, `file.ScanAdapter`/`WatchAdapter` |
| `ports.SinkPort[T]` | stream → external (fan-out) | `mqtt5.PublishAdapter`, `nethttp.SSEAdapter`, `sql.DrainInsertAdapter`, `file.DrainWriteAdapter` |
| `ports.IOPort[Req,Resp]` | stream ↔ external (1 adapter) | `nethttp.CallAdapter`, `mqtt5.CallAdapter`, `sql.QueryEachAdapter`, `file.ReadEachAdapter` |
| `ports.ToolPort[In,Out]` | external request → pipeline → response (N adapters) | `mcpgo.ToolPipelineAdapter`, `nethttp.PipelineAdapter`, `zeromq.ServeAdapter`, `mqtt5.ServeAdapter` |

```go
// domain/pipeline.go — no adapter imports
var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", sensorCodec, ports.PortOptions{})
var OEEResults = ports.NewSinkPort[OEE]("oee-results", oeeCodec, ports.PortOptions{})

func StartPipeline(ctx context.Context) {
    sensors := SensorReadings.Stream(ctx)
    oeeStream := stream.Apply(ctx, sensors, oeeCalcFn, stream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — bind concrete transports here
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, format.JSON(sensorCodec), opts))
domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, format.JSON(oeeCodec), publishOpts))
domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, nethttp.SSEAdapterOptions{})) // fan-out
```

### Request/response pipelines with `ToolPort`

`ToolPort[In,Out]` sets the pipeline function once and can bind it to multiple transports
simultaneously — the same domain logic serves MCP, HTTP, and ZeroMQ:

```go
var OEETool = ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeCodec, ports.PortOptions{})

func init() {
    OEETool.SetPipeline(func(ctx context.Context, req OEEIn) stream.Stream[OEEResult] {
        s := stream.Single(ctx, req)
        s = stream.Apply(ctx, s, validateFn, stream.ApplyOptions{})
        s = stream.Tap(ctx, s, func(v ValidatedReq) { slog.Info("validated", "id", v.ID) })
        return stream.Apply(ctx, s, oeeCalcFn, stream.ApplyOptions{})
    })
}

domain.OEETool.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, mcpToolHandle, mcpgo.Options{}))
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))
domain.OEETool.Bind(ctx, zeromq.ServeAdapter(repSock, zmqHandle, zeromq.ServeOptions{}))
```

New error types: `ports.PortBindError{Port,Adapter,Err}`, `ports.PortNoAdapterError{Port}`,
`ports.PortNoPipelineError{Port}` — all implement `slog.LogValuer`. Transport-level errors
(`NoLatestValueError`, `PipelineFullError`, `SSEWriteError`, …) are unchanged and still
surface through the same adapters.

---

## I/O role taxonomy — source, intermediate, sink

Every I/O operation in a stream pipeline has a role. Recognising the role guides which
API to use:

| Role | Character | API pattern | Examples |
|------|-----------|------------|---------|
| **Source** | Emits items FROM an external system into the stream | `ports.SourcePort[T]` + `transport.XxxAdapter` (fan-in) | MQTT messages, SQL rows, HTTP ingest, NDJSON file |
| **Intermediate** (transform) | Sends each item AS a request; receives a response; emits the response | `ports.IOPort[Req,Resp]` + `transport.CallAdapter`/`QueryEachAdapter`/`ReadEachAdapter` | ZeroMQ REQ/REP, MQTT5 request-reply, HTTP call, per-item SQL/file lookup |
| **Side-effect I/O** | Fires I/O alongside the pipeline WITHOUT consuming a response | `stream.Tap` + `Publish`/`Write`/`Patch` | MQTT alert publish, JSON file write, audit log |
| **Sink** | Consumes items FROM the stream into an external system | `ports.SinkPort[T]` + `transport.XxxAdapter` (fan-out) | MQTT publish, SQL insert, HTTP SSE/POST, NDJSON append |
| **Request/response** | External request triggers the pipeline; a response is returned | `ports.ToolPort[In,Out]` + `transport.XxxAdapter` (N transports) | MCP tool call, HTTP endpoint, ZeroMQ REP, MQTT5 request-reply server |
| **Pure computation** | Transforms items with zero I/O | `stream.Apply` + `forge.Function[In,Out]` | OEE calculation, normalisation, validation |

### The fundamental rule: forge functions are pure — zero I/O

`stream.Apply` accepts `*forge.Function[In, Out]`. These functions are **governed domain
computations** — their only job is to transform typed inputs into typed outputs. They
must have no I/O.

```go
// ✅ Correct: forge function is pure
oeeCalcFn := forge.NewFunction("oeeCalc", "1.0.0", oeeInCodec, oeeCodec,
    func(in OEEIn) (OEE, error) {
        // Pure calculation — no file reads, no HTTP calls, no database queries
        return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
    })

// ❌ Wrong: I/O inside a forge function violates the design principle
// func(in InputData) (Out, error) {
//     cfg, _ := configFile.Read(...)  // ← I/O inside forge
//     return combine(in, cfg), nil
// }
```

I/O that a forge function needs (e.g. a config lookup) must arrive as **typed input** —
declared in the input codec and flowing from the stream layer:

```go
// Config is an explicit input — loaded by the stream layer, not by the function:
type EnrichInput struct {
    Data   InputData
    Config ThresholdConfig   // explicit input — arrives via CombineLatest2
}

enrichFn := forge.NewFunction("enrich", "1.0.0", enrichInputCodec, outputCodec,
    func(in EnrichInput) (Out, error) {
        return combine(in.Data, in.Config), nil // pure: all inputs explicit
    })

// I/O stays in the stream layer:
configs := stream.Single(ctx, preloadedConfig)  // or a SourcePort bound to file.WatchAdapter for dynamic reload
combined := stream.CombineLatest2(ctx, dataStream, configs,
    func(d InputData, c ThresholdConfig) EnrichInput { return EnrichInput{d, c} })
enriched := stream.Apply(ctx, combined, enrichFn, stream.ApplyOptions{})
```

### When to use each I/O container

**Use `ports.IOPort[Req,Resp]`** when the step's whole purpose is an I/O call — send a
request, receive a response, forward the response. Bind exactly one adapter; swapping
the adapter (HTTP → SQL → file) never touches the pipeline:

```go
var Enrichment = ports.NewIOPort[Raw, Enriched]("enrichment", rawCodec, enrichedCodec, ports.PortOptions{})

// main.go — pick one:
domain.Enrichment.Bind(ctx, zeromq.CallAdapter(sock, enrichHandle, zeromq.CallStreamOptions{}))
// domain.Enrichment.Bind(ctx, mqtt5.CallAdapter(client, router, enrichHandle, mqtt5.CallOptions{}))
// domain.Enrichment.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, enrichHandle, nethttp.CallStreamOptions{}))
// domain.Enrichment.Bind(ctx, sql.QueryEachAdapter(enrichedCodec, queryFn, sql.QueryEachStreamOptions{}))

enriched := domain.Enrichment.Connect(ctx, rawStream)
```

I/O is explicit in the pipeline declaration. Errors are transport-typed, not wrapped in
`forge.ApplyError`.

**Use `stream.Tap`** for side-effect I/O that produces no value for the pipeline —
publishing, writing, logging:

```go
results = stream.Tap(ctx, results, func(r OEEResult) {
    events.PublishHandle(ctx, alertPub, alertTransport, r) // fire-and-forget (alertTransport: mqtt.NewPublishTransport[OEEResult])
    resultFile.Write(nil, r, ports.FileOptions{})          // whole-file write
})
```

**Use `stream.FlatMapSlice`** for ad-hoc transforms that have I/O and are not governed
(no forge hash, no schema validation — use when governance is not required):

```go
// I/O in stream.FlatMapSlice is in the stream layer, not forge:
enriched := stream.FlatMapSlice(ctx, ids, func(id string) []Data {
    v, err := someFile.Read(map[string]string{"id": id}, opts)
    if err != nil { return nil } // errors silently dropped — use ports.IOPort for typed errors
    return []Data{v}
})
```

### All transports have an intermediate transform operator

Every transport with a request/response protocol provides an `IOAdapter` for
`ports.IOPort` — HTTP, ZeroMQ, MQTT5, SQL, and File all support per-item enrichment:

```
zeromq.CallAdapter      ✅  — send each item as REQ, emit each REP response
mqtt5.CallAdapter       ✅  — send each item as MQTT5 request-reply, emit responses
nethttp.CallAdapter     ✅  — full HTTP codec machinery (path vars, query/cookie/header
                              params, security, structured errors) per item
sql.QueryEachAdapter    ✅  — parameterized SQL query per item (1:N rows)
file.ReadEachAdapter    ✅  — per-item file read with path template vars
```

See the [Ports Guide](../guides/ports.md) for the complete adapter catalogue.
