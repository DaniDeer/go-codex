# Stream Guide — reactive pipelines

> See also: [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream) · [Feature: Reactive Streams](../features/stream.md) · [Stream Bridge Guide](stream-bridges.md) · [Forge Pipelines](../concepts/pipelines.md) · [Observer Examples](observer.md)
>
> **Runnable demos:**
> - [`examples/stream-pipeline`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-pipeline) — comprehensive showcase of all operators (8 sections); run with `go run ./examples/stream-pipeline`
> - [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — bridge showcase: `mqtt.SubscribeStream`, `nethttp.HandlerLatest`, `mqtt.DrainPublish`, `sql.QueryStream`

The `stream` package turns `forge.Function[In,Out]` computations into continuous
reactive pipelines over typed Go channels. Each operator is a free function that takes
and returns a `Stream[T]` — compose them like Unix pipes.

---

## Step 1 — Create a typed source

### From a typed channel

```go
ch := make(chan SensorReading, 64)
// fill ch from anywhere — a goroutine, a test, etc.

sensors := stream.From(ctx, ch)
```

### From raw bytes (MQTT / ZeroMQ)

```go
rawCh := make(chan []byte, 64)

// Fill rawCh from MQTT SubscribeHandler:
mqttClient.Subscribe("sensors/+/data", 0,
    adaptermqtt.SubscribeHandler(ctx, channelHandle,
        func(_ context.Context, raw []byte) error {
            select { case rawCh <- raw: default: } // drop if pipeline is saturated
            return nil
        }, adaptermqtt.SubscribeOptions{}))

// Decode with any format — JSON, YAML, TOML, or custom:
sensors := stream.FromCodec(ctx, rawCh, format.JSON(sensorCodec),
    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})
```

Decode failures go to `Stream.Errors` as `StreamDecodeError`; the stream continues.

---

## Step 2 — Apply a forge function

```go
oeeCalc := forge.NewFunction("oeeCalc", "1.0.0",
    oeeInCodec, oeeCodec,
    func(in OEEIn) (OEE, error) {
        return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
    },
    forge.FunctionMeta{Author: "OT Engineering", ApprovedBy: "Quality Manager"},
)

oeeStream := stream.Apply(ctx, sensors, oeeCalc,
    stream.ApplyOptions{Observer: obs})
```

All forge validation — input codec `Refine`, `WithRefinement`, compute, output codec —
runs per item. Failures go to `Stream.Errors` as `StreamApplyError`.

---

## Step 3 — Observe domain events with Tap

Two observation mechanisms exist in a stream pipeline, and they serve different concerns:

| | `stream.Tap` | `ApplyOptions.Observer` |
|--|-------------|------------------------|
| **What** | Business-significant values — typed, per-item domain events | Infrastructure metrics — counts, latencies, spans |
| **Type** | Full generic type (`func(oee OEE)`) | `stats.Observer` interfaces — fixed signatures |
| **Where** | Explicit Tap call in pipeline | Wired at startup via opts or context |
| **Fires when** | Every value emitted by the upstream stream | Every `stream.Apply` item (RecordStreamItem) and forge apply (RecordApply) |

**`stream.Tap` — domain event observation:**

```go
oeeStream = stream.Tap(ctx, oeeStream, func(oee OEE) {
    // Business logic observation — full type safety, no interface
    slog.Info("OEE computed", "value", float64(oee))
    dashboard.Publish(oee)   // real-time business event
    if float64(oee) < 0.65 {
        alertBus.Send(OEEAlert{Value: oee}) // domain rule, not infrastructure
    }
})
```

`Tap` does not transform the stream — items pass through unchanged.

**`ApplyOptions.Observer` — infrastructure observation:**

```go
// RecordStreamItem fires per item in stream.Apply
// RecordApply fires inside each forge function call
// TraceObserver spans wrap the apply per-item
oeeStream := stream.Apply(ctx, sensors, oeeCalcFn,
    stream.ApplyOptions{}) // observer from ctx — RecordStreamItem + RecordApply
```

Pass the infrastructure observer via `ApplyOptions.Observer` (or via context —
see Observer Integration below). It fires `stats.StreamObserver.RecordStreamItem`
and, inside the forge function, `stats.PipelineObserver.RecordApply`.

---

## Step 4 — Filter, time-window, and route

```go
// Keep only below-threshold OEE values:
alerts := stream.Filter(ctx, oeeStream, func(oee OEE) bool {
    return float64(oee) < 0.65
})

// Rate-limit alerts to one per 30 seconds:
debounced := stream.Debounce(ctx, alerts, 30*time.Second)

// Or collect into batches of 10 readings (or 500ms silence):
batchStream := stream.Buffer(ctx, sensors, 10, 500*time.Millisecond)
batchOEE := stream.Apply(ctx, batchStream, batchOEECalc, opts)
```

---

## Step 5 — Drain with explicit error handling

`Drain` is the safe default sink. It drains both `Values` and `Errors` channels
concurrently in a single select loop — no goroutine leaks:

```go
stream.Drain(ctx, debounced,
    func(ctx context.Context, oee OEE) error {
        return adaptermqtt5.Publish(ctx, mqttClient, alertHandle, 0, false,
            buildAlert(oee), nil, adaptermqtt5.PublishOptions{Observer: obs})
    },
    func(err error) {
        // Explicit error handler — every error is typed
        var sae stream.StreamApplyError
        var sde stream.StreamDecodeError
        switch {
        case errors.As(err, &sae):
            slog.Warn("OEE computation failed", "error", sae)
        case errors.As(err, &sde):
            slog.Warn("sensor decode failed", "error", sde)
        default:
            slog.Error("publish failed", "error", err)
        }
    },
    stream.DrainOptions{Observer: obs},
)
```

---

## Step 6 — Multi-source with CombineLatest2

When a forge function takes a struct input from two independent streams:

```go
// Availability and Performance arrive on separate MQTT topics:
oeeInputs := stream.CombineLatest2(ctx, availStream, perfStream,
    func(a Availability, p Performance) OEEIn { return OEEIn{a, p} })

oeeStream := stream.Apply(ctx, oeeInputs, oeeCalcFn, opts)
```

Emits whenever either source emits (after both have emitted at least once).

---

## Step 7 — Document the pipeline with Topology

```go
topo := stream.NewTopology("Sensor OEE Pipeline", "1.0.0").
    WithDescription("Real-time OEE from MQTT sensor readings.").
    WithSource("mqtt/sensors/+/data", "Decoded sensor readings").
    WithFilter("oee < 0.65 — low-OEE threshold").
    WithDebounce("30s — alert rate limit").
    WithSink("mqtt/alerts/oee", "Low-OEE alerts")
stream.WithApply(topo, oeeCalcFn) // free function — captures forge function hash for auditability

yamlBytes, err := streamrender.Render(topo.Spec())
// → YAML describing the complete pipeline topology
```

---

## Error handling patterns

### Silence transient errors

```go
stream.Retry(ctx, sensors, func(err error) (SensorReading, bool, error) {
    var sde stream.StreamDecodeError
    if errors.As(err, &sde) && isTransientNetworkError(sde.Err) {
        return SensorReading{}, false, nil // silence; will retry on next message
    }
    return SensorReading{}, false, err // re-emit permanent errors
})
```

### Dead-letter queue

```go
good, bad := stream.Tee(ctx, stream.Filter(ctx, src, func(SensorReading) bool { return true }))
// send bad to dead-letter storage, process good normally
```

### Recovery with default value

```go
stream.MapErr(ctx, oeeStream, func(err error) (OEE, bool, error) {
    var sae stream.StreamApplyError
    if errors.As(err, &sae) {
        return OEE(0), true, nil // emit zero OEE as sentinel
    }
    return OEE(0), false, err
})
```

---

## Observer integration

**Option A — set once via context (recommended for new services):**

```go
obs := stats.NewFanout(
    metrics,                                          // stats.StreamObserver.RecordStreamItem
    stats.NewLoggingObserver(slog.Default()),          // logs every event via slog
    otelTracer,                                       // stats.TraceObserver per item in Apply
)
// All stream operators resolve obs automatically when Options.Observer is nil:
ctx := stats.WithObserver(context.Background(), obs)

sensors  := stream.FromCodec(ctx, rawCh, format.JSON(codec), stream.SourceOptions{})
oeeData  := stream.Apply(ctx, sensors, oeeCalcFn, stream.ApplyOptions{})
stream.Drain(ctx, oeeData, publish, logErr, stream.DrainOptions{})
```

**Option B — pass explicitly per operator:**

```go
sensors  := stream.FromCodec(ctx, rawCh, format.JSON(codec), stream.SourceOptions{Observer: obs})
oeeData  := stream.Apply(ctx, sensors, oeeCalcFn, stream.ApplyOptions{Observer: obs})
stream.Drain(ctx, oeeData, publish, logErr, stream.DrainOptions{Observer: obs})
```

**For forge.Registry — always explicit (no context integration):**

```go
// Registry is long-lived startup config; explicit builder is the right API:
reg := forge.NewRegistry("OEE Pipeline", "1.0.0").WithObserver(obs)
```

`forge.Function.ApplyContext(ctx, in)` uses the forge observer (wired via
`Register(reg)`), not the context observer — two independent observers can be active
simultaneously on the same apply call.

---

## Next: connecting adapters to streams

The examples above use raw channels as sources. For production use, each adapter
provides bridge helpers that eliminate the channel boilerplate:

- **MQTT / MQTT5:** `SubscribeStream` returns a stream + handler; `DrainPublish` publishes each item
- **ZeroMQ:** `SubscribeStream`, `DrainPublish`, `ServeLatest` (reactive cache), `AsPipelineFunc` for Serve
- **HTTP (nethttp / chi):** `HandlerLatest` (GET returns latest value), `HandlerIngest` (POST feeds pipeline), `PipelineHandler` (declarative handler body with Tap)
- **MCP:** `ToolLatestHandler` (LLM tool backed by latest stream value)
- **SQL:** `QueryStream` (poll DB), `DrainInsert` (validate + insert)
- **File:** `ScanStream` (NDJSON), `WatchStream` (directory), `DrainWrite`

→ **[Stream Bridge Guide](stream-bridges.md)**
