# Reactive Stream Pipelines — `stream`

> See also: [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream) · [Stream Guide](../guides/stream.md) · [Observer Pattern](observer.md) · [Forge Pipelines](../concepts/pipelines.md)
>
> **Runnable demos:**
> - [`examples/stream-pipeline`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-pipeline) — all operators showcased: `From`, `Apply`, `Tap`, `Filter`, `CombineLatest2`, `Tee`, `Merge`, `FlatMapSlice`, `Debounce`, `Throttle`, `Buffer`, `Window`, `MapErr`, `Topology` + YAML render
> - [`examples/stream-oee`](https://github.com/DaniDeer/go-codex/tree/main/examples/stream-oee) — forge + stream integration: governed OEE from machine events (Window → governed forge chain → alert); governance YAML with SHA-256 hashes per function
> - [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — multi-adapter integration (MQTT + SQL + HTTP) using `stream.FromCodec` + `stream.Apply` + `stream.Drain`

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
