# Stream Guide — reactive pipelines

> See also: [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream) · [Feature: Reactive Streams](../features/stream.md) · [Ports Guide](ports.md) · [Constraints & Refinements](validation.md) · [Forge Pipelines](../concepts/pipelines.md) · [Observer Examples](observer.md)
>
> **Runnable demos:**
> - [`examples/stream-pipeline`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-pipeline) — comprehensive showcase of all operators (8 sections); run with `go run ./examples/stream-pipeline`
> - [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — flagship port showcase: `mqtt.SubscribeAdapter`/`PublishAdapter`, `sql.QueryEachAdapter`, `file.DrainWriteFileAdapter`, `nethttp.PipelineAdapter`, `nethttp.HandlerLatest` in one coherent use case

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

// Fill rawCh via the mqtt adapter's spec-free, handle-based Decision 7 call surface
// (docs/roadmap/pubsub-workflow-simplification.md):
sub := rawChannel.WithSubscribe(events.Subscribe{})
transport := adaptermqtt.NewSubscribeTransport[[]byte](mqttClient, 0, adaptermqtt.SubscribeOptions{})
go func() {
    _ = events.SubscribeHandle(ctx, sub, transport,
        func(_ context.Context, raw []byte) error {
            select { case rawCh <- raw: default: } // drop if pipeline is saturated
            return nil
        })
}()

// Decode with any format — JSON, YAML, TOML, or custom:
sensors := stream.FromCodec(ctx, rawCh, format.JSON(sensorCodec),
    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})
```

Decode failures go to `Stream.Errors` as `StreamDecodeError`; the stream continues.

> This manual channel-wiring pattern is what `ports.SourcePort` + `mqtt.SubscribeAdapter`
> do internally. For production pipelines, prefer the `ports` package — it also keeps
> the transport choice out of your pipeline code. See the [Ports Guide](ports.md).

### One-shot / per-request source

`Single` emits one value and closes — the entry point for running a single
request through the same operators as a continuous pipeline:

```go
s := stream.Single(ctx, req)                   // req → 1-item stream
out := stream.Apply(ctx, s, computeFn, opts)   // same governed operators
resp, errs := stream.Collect(ctx, out)         // stream ends after one item
```

`Single` takes a value you already have — nothing can fail, so `Stream.Errors`
is never written (same reasoning as `From`; only `FromCodec` has an error
path, because bytes→T decode can fail). If producing the value can fail, do
that *before* calling `Single`. This is how `PipelineHandlerFunc` and
`AsPipelineFunc` turn HTTP request/response handling into a stream pipeline.

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

// Better: reuse a named codex.Constraint as the predicate — the same value
// can refine a codec, document itself in the spec, and label the topology.
// See the Constraints & Refinements guide (validation.md).
// alerts := stream.Filter(ctx, oeeStream, domain.LowOEE.Check)

// Rate-limit alerts to one per 30 seconds:
debounced := stream.Debounce(ctx, alerts, 30*time.Second)

// Or collect into batches of 10 readings (or 500ms silence):
batchStream := stream.Buffer(ctx, sensors, 10, 500*time.Millisecond)
batchOEE := stream.Apply(ctx, batchStream, batchOEECalc, opts)
```

---

## Step 4b — Route with Switch and GroupBy

`Switch` routes each item to the FIRST matching named case. Outputs are
positional (`out[i]` pairs with `cases[i]`); non-matches and source errors
go only to the rest stream — single error ownership, no duplicate handling:

```go
outs, rest := stream.Switch(ctx, readings,
    []stream.Case[Reading]{
        {Name: "alert",   When: func(r Reading) bool { return r.Value >= 50 }},
        {Name: "warning", When: func(r Reading) bool { return r.Value >= 40 }},
    },
    stream.SwitchOptions{})

alerts, warnings, archive := outs[0], outs[1], rest
```

Reuse a named `codex.Constraint` as a case — the routing predicate, the codec
refinement, and the spec documentation are then ONE declaration (see the
[Constraints & Refinements guide](validation.md)):

```go
outs, rest := stream.Switch(ctx, readings,
    []stream.Case[Reading]{
        stream.CaseConstraint("hot", domain.HotReading(cfg.Threshold)),
    },
    stream.SwitchOptions{})
```

Malformed cases (empty or duplicate `Name`, nil `When`) panic — they are
programming errors, caught at wiring time, not runtime failures.

`SwitchKey` is the keyed variant. Share a `TaggedUnion`'s named discriminator
function so the wire format and the router can never drift:

```go
kindOf := func(e Event) string { return e.Kind } // also TaggedUnion's selectVariant
outs, rest := stream.SwitchKey(ctx, events,
    []string{"created", "cancelled"}, kindOf, stream.SwitchOptions{})
```

`GroupBy` splits a stream into dynamic per-key sub-streams. The callback runs
on the dispatch goroutine — start consumers there, don't run them inline:

```go
stream.GroupBy(ctx, readings,
    func(r Reading) string { return r.SensorID },
    func(sensorID string, sub stream.Stream[Reading]) {
        go consumeSensor(sensorID, sub) // start, don't run
    },
    stream.GroupByOptions{Buffer: 16})
// GroupBy blocks until readings closes; sub-streams close with the parent.
```

Keys are unbounded — one goroutine + channel pair per distinct key lives
until the parent closes. Bound cardinality upstream if keys are attacker- or
user-controlled.

For sum-typed (interface) streams, `OfType`/`SwitchType2`/`SwitchType3` route
by dynamic type, and `SplitEither` totally splits a `Stream[codex.Either[A,B]]`
into two typed branches with no rest stream (closed sum):

```go
created  := stream.OfType[OrderCreated](ctx, events)
lefts, rights := stream.SplitEither(ctx, unionStream, stream.SwitchOptions{})
```

---

## Step 5 — Drain with explicit error handling

`Drain` is the safe default sink. It drains both `Values` and `Errors` channels
concurrently in a single select loop — no goroutine leaks:

```go
alertTransport := adaptermqtt5.NewPublishTransport[Alert](mqttClient, 0, false, adaptermqtt5.PublishOptions[Alert]{Observer: obs})
stream.Drain(ctx, debounced,
    func(ctx context.Context, oee OEE) error {
        return events.PublishHandle(ctx, alertPub, alertTransport, buildAlert(oee))
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

`CombineLatest3` and `CombineLatest4` cover three and four heterogeneous
sources. **For more than 4 sources, compose the combinators** — Go generics
cannot express variadic type parameters, and nesting is type-safe with no
new API:

```go
// Six sources: combine 3 + 3, then merge the two intermediates.
left := stream.CombineLatest3(ctx, a, b, c,
    func(a A, b B, c C) Left { return Left{a, b, c} })
right := stream.CombineLatest3(ctx, d, e, f,
    func(d D, e E, f F) Right { return Right{d, e, f} })
combined := stream.CombineLatest2(ctx, left, right,
    func(l Left, r Right) Combined { return Combined{l, r} })
```

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

This section covers stream-level recovery operators. For full placement
(adapter hooks vs ports vs stream drain), use the unified map in
[Guide: Error Handling](error-handling.md#where-to-handle-errors-adapters-ports-pipelines).

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

The examples above use raw channels as sources. For production use, wire pipelines to
transports through the **[`ports`](../features/ports.md)** package — a protocol-agnostic
binding layer that keeps transport imports out of pipeline code:

- **`ports.SourcePort[T]`** (fan-in): `mqtt5.SubscribeAdapter`, `mqtt.SubscribeAdapter`, `nethttp.IngestAdapter`/`PollAdapter`, `chi.IngestAdapter`, `zeromq.SubscribeAdapter`, `sql.QueryAdapter`, `file.ScanAdapter`/`WatchAdapter`
- **`ports.SinkPort[T]`** (fan-out): `mqtt5.PublishAdapter`, `mqtt.PublishAdapter`, `nethttp.SSEAdapter`/`DrainCallAdapter`, `chi.SSEAdapter`, `zeromq.PublishAdapter`, `sql.DrainInsertAdapter`, `file.DrainWriteAdapter`/`DrainWriteFileAdapter`
- **`ports.IOPort[Req,Resp]`** (one adapter, request/response transform): `nethttp.CallAdapter`, `mqtt5.CallAdapter`, `zeromq.CallAdapter`, `sql.QueryEachAdapter`, `file.ReadEachAdapter`
- **`ports.ToolPort[In,Out]`** (one pipeline, N transports): `mcpgo.ToolPipelineAdapter`, `nethttp.PipelineAdapter`, `chi.PipelineAdapter`, `zeromq.ServeAdapter`, `mqtt5.ServeAdapter`
- **`ports.LatestPort[T]`** (one cache, N serving transports): `nethttp.LatestAdapter`, `zeromq.LatestAdapter`, `mcpgo.LatestAdapter` — "current state" endpoints served from a continuously updated atomic cell

→ **[Ports Guide](ports.md)**
