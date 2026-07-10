# Reactive stream pipelines — `stream`

> **Status:** Implemented — `github.com/DaniDeer/go-codex/stream` shipped.
> [← Back to Roadmap](index.md)

## Motivation

go-codex users working with real-time data (sensor readings, IoT events, financial
ticks) need to compose reactive pipelines declaratively — and handle errors explicitly,
not silently. Today there is no bridge between the push-based transport adapters
(`adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq`) and `forge.Function[In, Out]`:

```go
// Today — manual goroutine boilerplate + error handling scattered everywhere:
go func() {
    for msg := range mqttMessages {
        reading, err := sensorCodec.Decode(msg.Payload())
        if err != nil { handleErr(err); continue }
        oee, err := oeeCalc.Apply(reading)
        if err != nil { handleErr(err); continue }
        publishAlert(oee)
    }
}()
```

`stream` provides a **declarative reactive pipeline** over typed Go channels:
- Codec-validated typed sources from MQTT/ZeroMQ
- `forge.Function[In, Out]` applied per-item with validation
- **Explicit error channels** — idiomatic Go, no panics, no silent drops
- Declarative pipeline composition without goroutine boilerplate
- Observer hooks for both infrastructure metrics and domain-level events

---

## Why NOT RxGo

[ReactiveX/RxGo](https://github.com/ReactiveX/RxGo) is the obvious reference, but
**not a good fit** for go-codex:

| Concern | RxGo v2 | RxGo v3 |
|---|---|---|
| Type safety | ❌ `interface{}`/`rxgo.Item` — not generic | ✅ generic `Observable[T]` |
| Maintenance | ❌ **inactive** — no commits since 2023, maintainers requested | ❌ experimental, also stalled |
| External dep | Large; uncertain future | Large; uncertain future |

The v2 type-safety problem directly contradicts go-codex's core value proposition —
compile-time-checked codec-validated types. **Go's built-in `chan T` is already a
reactive stream**: push-based, backpressure via buffered channels, context-cancellable,
type-safe by construction, zero external dependency.

---

## Core design decisions

### 1. Explicit error channels — NOT a single `Item[T]` channel

The previous draft used a combined `chan Item[T]` (value OR error in one channel).
This is the RxGo pattern. **Idiomatic Go today prefers explicit separate channels:**

```go
// Stream[T] carries typed values and errors on separate channels.
// Both channels close when the stream terminates.
// Consumers MUST drain both channels concurrently to avoid goroutine leaks.
type Stream[T any] struct {
    Values <-chan T      // successful items
    Errors <-chan error  // per-item errors; stream continues after each
}
```

Why separate channels:
- **Clarity** — the reader of the code immediately sees the error channel without
  inspecting the `Item` type
- **Selective handling** — a consumer can delegate values to one goroutine and errors
  to another (logging, dead-lettering) without a type switch on every item
- **Go idiom** — `net.Pipe()`, `(*bufio.Scanner).Err()`, `(*sql.Rows).Err()` all use
  the explicit error pattern
- **Composability** — operators forward errors downstream unchanged without touching the
  value channel, making the transformation logic much cleaner

**Drain obligation:** Any consumer that reads only from `Values` will cause goroutine
leaks if the error channel fills. `stream.Drain` handles both channels safely. Users
who compose raw channels must drain both.

### 2. Free functions — NOT a fluent builder

Go generics cannot add new type parameters to methods. A type-changing operator like
`Apply[In, Out]` (transforms `Stream[In]` to `Stream[Out]`) CANNOT be a method on
`Stream[In]`. This is a fundamental language constraint.

The consistent, ergonomic approach is **free functions throughout**:

```go
// All operators are free functions. Compose them in local variables:
sensorStream := stream.FromCodec(ctx, rawCh, sensorCodec, opts)
oeeStream    := stream.Apply(ctx, sensorStream, oeeCalcFn, opts)
alertStream  := stream.Filter(ctx, oeeStream, isAlert)
debounced    := stream.Debounce(ctx, alertStream, 500*time.Millisecond)
stream.Drain(ctx, debounced, publishAlert, logError, opts)
```

This is the same pattern as `forge.NewFunction`, `forge.Compose`, `forge.Map` — all
free functions, all type-safe, all readable. It also mirrors how `io.Pipe`,
`bufio.NewReader`, and `context.WithCancel` are composed in stdlib.

### 3. Two observer kinds — metrics vs domain events

go-codex's existing `stats.Observer` handles **infrastructure metrics**: how many
items processed, latency, validation error counts, trace spans. These are for the
operations team.

Reactive programming also calls for **domain event observation**: the ability to "tap"
into the stream and react to domain values (a new OEE computed, a sensor alert
triggered). This is what the user means by "observer listens to events in the
application."

In `stream` this is the `Tap` operator:

```go
// Tap inserts an observer function on the value channel without transforming items.
// onValue receives each successful value; errors pass through unchanged.
// Use Tap for domain-level event observation: logging, auditing, triggering
// side effects that must not transform the pipeline's values.
func Tap[T any](ctx context.Context, s Stream[T], onValue func(T)) Stream[T]
```

`Tap` is the reactive programming equivalent of `do` / `doOnNext` in RxGo/RxJava.
The infrastructure metrics observer (`stats.Observer`) wires into the `Options` structs
on individual operators. The domain event observer (`Tap`) is a first-class operator.

---

## forge vs stream — architectural evaluation

This section documents the evaluation of whether `forge` and `stream` should be a
single unified module.

### Why they are separate packages

**`forge/` and `stream/` are complementary, not competing.** They serve different
concerns with different execution models, and merging them would create a worse API
for both use cases.

| Concern | `forge/` | `stream/` |
|---|---|---|
| Execution model | Synchronous, pull-based: `Apply(In) (Out, error)` | Asynchronous, push-based: goroutine loops, `<-chan T` |
| Composition unit | `[]T` slices (batch) | `<-chan T` channels (per-item, continuous) |
| Governance | SHA-256 contract hash + Author/ApprovedBy/ApprovedAt — KPI audit trail | None |
| Spec output | `Registry` → `PipelineSpec` → YAML (governance report) | `Topology` → `TopologySpec` → YAML via `render/stream` (shipped Phase 2) |
| Use case | Governed KPI computation, industrial OEE, audit-traceable derivation chains | Real-time sensor pipelines, MQTT/ZeroMQ streams, time operators |

### Why NOT to merge

**1. Different execution models — not composable into one type**

Forge's `Function[In, Out]` is synchronous. `stream.Apply` wraps it in a goroutine
loop over a channel. A unified type would be either synchronous (losing reactive
capability) or asynchronous (losing batch simplicity). There is no neutral ground.

**2. Governance belongs on forge functions, not on stream operators**

`Debounce`, `Throttle`, `CombineLatest2` have no business with SHA-256 hashes or
`ApprovedBy` fields. Merging would make the governance model noisy and confusing.

**3. Same names, different semantics**

Both packages have `Map`/`Filter` — but forge's work over `[]T` slices, stream's work
over `<-chan T` channels. Merging them into one package would create naming collisions
requiring awkward disambiguation (`SliceMap` vs `ChanMap`).

**4. Dependency direction is one-way and correct**

```
codex  ←  forge  ←  stream
```

`stream` imports `forge` (to call `forge.Function.ApplyContext` per item). If forge
imported `stream` for an `ApplyStream` convenience method, it would create a circular
dependency. The free function design (`stream.Apply(ctx, s, fn, opts)`) keeps the
dependency acyclic.

**5. Spec outputs serve different audiences**

- `render/pipeline` YAML → KPI governance report (quality manager, auditor)
- `render/stream` YAML (`stream.TopologySpec`) → system architecture doc (architect, operator) — shipped Phase 2

### Where `stream` belongs in the module layout

`stream/` is a **top-level package**, not an adapter. The `adapters/` directory convention
in go-codex is for packages that bridge an external library (paho.mqtt, goose, mcp-go).
`stream/` uses only go-codex packages and Go stdlib — no external dependency. It was
initially placed in `adapters/stream/` and **moved to `stream/`** as part of this
evaluation.

**Conceptual model:**

```
codex/      Layer 1 — validated domain types
  ↓
forge/      Layer 3 — governed synchronous computation + KPI spec
  ↓
stream/     Layer 4 — reactive execution of forge functions over event streams
  ↑
adapters/mqtt, adapters/zeromq — supply source channels
```

- `forge/` = "what the computation **is**" (declarative, governed, signed)
- `stream/` = "how computation **runs** continuously over time" (reactive, async)

They compose: `stream.Apply(ctx, mqttStream, forgeFunction, opts)`.

### What forge provides that stream uses

| forge today | Stream usage |
|---|---|
| `Function[In, Out]` — validates In, runs compute, validates Out | The per-item operation inside `stream.Apply` |
| `Apply(in In) (Out, error)` — synchronous single-item | Called once per item; `stream.Apply` wraps in a goroutine loop |
| `Compose[A, B, Out]` — chains two functions | Composed functions work transparently in `stream.Apply` |
| `Map`, `Filter`, `Reduce` over `[]T` slices | Use with `stream.Buffer` to process windowed batches |
| `PipelineObserver.RecordApply` | Fires inside `forge.Function.Apply` — observable per-item in streams |

**Forge itself does not need to change** for `stream` to work fully.

---

## Forge enhancements (Phase 2 consideration, not Phase 1)

One minor ergonomics improvement for users building large reactive pipelines:

**`forge.Function.Spec.AsStreamPipelineStep()`** — returns a human-readable description
for stream topology YAML rendering (Phase 2). Documentation-only method; no runtime
dependency from forge on stream.

No runtime forge changes needed in Phase 1.

---

## API surface

### Primitive type

```go
// Stream[T] is a typed reactive stream with explicit error separation.
// Both channels are closed when the stream terminates (ctx cancelled or source closed).
// Consumers MUST drain both Values and Errors concurrently — use [Drain] for safety.
type Stream[T any] struct {
    Values <-chan T     // successful items
    Errors <-chan error // per-item errors — stream continues after each error
}
```

### Source operators

```go
// From wraps a typed channel as a Stream.
// Each received value becomes a value item. Channel close terminates the stream.
func From[T any](ctx context.Context, src <-chan T) Stream[T]

// FromCodec decodes raw []byte payloads from a transport channel using a codec.
// Decode failures are sent to Stream.Errors — the stream continues.
// Use with MQTT / ZeroMQ SubscribeHandlers that deliver raw payloads.
func FromCodec[T any](
    ctx context.Context,
    src <-chan []byte,
    c codex.Codec[T],
    opts SourceOptions,
) Stream[T]

type SourceOptions struct {
    // Name identifies this source in StreamDecodeError for structured logging.
    Name string
    // Observer receives RecordValidationError for per-field decode failures.
    // Defaults to stats.NoopObserver when nil.
    Observer stats.Observer
    // Buffer is the Values channel buffer size. Default 0 (unbuffered).
    Buffer int
}
```

### Transform operators

```go
// Apply applies fn to every value in src using [forge.Function.ApplyContext].
// All forge validation (input codec, refinement, compute, output codec) runs per item.
// Validation failures go to Stream.Errors; successful outputs go to Stream.Values.
// PipelineObserver.RecordApply fires for every item inside forge.
func Apply[In, Out any](
    ctx context.Context,
    src Stream[In],
    fn *forge.Function[In, Out],
    opts ApplyOptions,
) Stream[Out]

type ApplyOptions struct {
    // Observer, when also implementing [stats.StreamObserver], receives
    // RecordStreamItem per item. stats.PipelineObserver.RecordApply fires
    // separately inside forge.Function.Apply — both fire independently.
    Observer stats.Observer
    // Buffer is the output Values channel buffer size. Default 0.
    Buffer int
}

// Filter keeps value items where pred returns true. Value items failing pred
// are dropped silently. Error items pass through to Stream.Errors unchanged.
func Filter[T any](ctx context.Context, src Stream[T], pred func(T) bool) Stream[T]

// Tap inserts a domain event observer on the value channel without transforming items.
// onValue is called for each successful value; errors are forwarded unchanged.
// Use Tap for domain-level observation: auditing, triggering side effects,
// logging application-level events independently from infrastructure metrics.
func Tap[T any](ctx context.Context, src Stream[T], onValue func(T)) Stream[T]

// MapErr transforms errors in the error channel without touching values.
// The transform function can recover from an error (return a value item)
// or reclassify it (return a different error). Enables dead-lettering and retry logic.
func MapErr[T any](ctx context.Context, src Stream[T], fn func(error) (T, bool, error)) Stream[T]
// fn returns: (value, isValue, err)
// isValue=true → emit value; isValue=false, err!=nil → emit error; both zero → drop
```

### Fan-in / fan-out operators

```go
// Merge combines multiple streams into one. Items from all sources are forwarded
// as they arrive. Errors from all sources are merged into one error channel.
// The combined stream terminates when all source streams have terminated.
func Merge[T any](ctx context.Context, srcs ...Stream[T]) Stream[T]

// Tee splits src into two independent copies. Both copies receive all items and errors.
// Backpressure on either copy blocks the other — use buffered channels or Drain.
func Tee[T any](ctx context.Context, src Stream[T]) (Stream[T], Stream[T])
```

### Time operators

```go
// Buffer collects up to n values (or until maxWait elapses since last emission)
// and emits them as a slice. Enables windowed batch processing with forge.Map.
// Errors from src are forwarded immediately to Stream[[]T].Errors without buffering.
func Buffer[T any](ctx context.Context, src Stream[T], n int, maxWait time.Duration) Stream[[]T]

// Debounce emits a value only when src.Values is silent for d.
// Intermediate values during the silence window are dropped.
// Useful when only the final value of a burst matters (e.g. sensor settling).
func Debounce[T any](ctx context.Context, src Stream[T], d time.Duration) Stream[T]

// Throttle emits at most one value per interval, dropping intermediates.
// Useful for rate-limiting high-frequency sources.
func Throttle[T any](ctx context.Context, src Stream[T], interval time.Duration) Stream[T]
```

### Sink operators

```go
// Drain consumes src until it terminates or ctx is cancelled.
// onValue is called for each successful item; onError for each error item.
// Drain ALWAYS drains both channels concurrently — it is the safe default sink.
// Errors returned by onValue are forwarded to onError (not re-enqueued).
func Drain[T any](
    ctx context.Context,
    src Stream[T],
    onValue func(context.Context, T) error,
    onError func(error),
    opts DrainOptions,
)

type DrainOptions struct {
    // Observer receives RecordValidationError for errors returned by onValue.
    Observer stats.Observer
}

// Collect accumulates all values and errors until src terminates or ctx is cancelled.
// Primarily for testing and bounded streams.
func Collect[T any](ctx context.Context, src Stream[T]) (values []T, errs []error)
```

### Multi-source composition (for struct-input forge functions)

```go
// CombineLatest2 merges the latest value from two independent streams into a struct.
// A new combined value is emitted whenever either source emits a new value.
// Blocks until both sources have emitted at least one value.
// Errors from either source are forwarded to Stream[Out].Errors.
//
// Use with forge.NewFunction taking a 2-field input struct:
//   oeeIn := stream.CombineLatest2(ctx, availStream, perfStream,
//       func(a Availability, p Performance) OEEIn { return OEEIn{a, p} })
//   oeeStream := stream.Apply(ctx, oeeIn, oeeCalcFn, opts)
func CombineLatest2[A, B, Out any](
    ctx context.Context,
    a Stream[A],
    b Stream[B],
    combine func(A, B) Out,
) Stream[Out]
```

---

## Structured errors (all implement `slog.LogValuer`)

```go
// StreamDecodeError is sent to Stream.Errors by FromCodec when a payload fails
// codec decode or Refine constraints.
type StreamDecodeError struct {
    Source string // SourceOptions.Name
    Err    error  // underlying codec error (codex.ValidationErrors, etc.)
}
func (e StreamDecodeError) Error() string  { ... }
func (e StreamDecodeError) Unwrap() error  { return e.Err }
func (e StreamDecodeError) LogValue() slog.Value // → {source, err}

// StreamApplyError is sent to Stream.Errors by Apply when forge.Function.Apply fails.
// The inner Err is always a typed forge error (InputError, OutputError, ApplyError, etc.)
// and is reachable via errors.As.
type StreamApplyError struct {
    Function string // forge function name
    Err      error  // inner forge error
}
func (e StreamApplyError) Error() string  { ... }
func (e StreamApplyError) Unwrap() error  { return e.Err }
func (e StreamApplyError) LogValue() slog.Value // → {function, err}
```

No catch-all error type. Every error in `Stream.Errors` is one of these typed errors or
an error from `onValue` in `Drain`. Callers can `errors.As` down to
`codex.ValidationErrors` for per-field detail.

---

## Observer integration

### Infrastructure metrics — `stats.StreamObserver`

A new optional extension on `stats.Observer`, following the `stats.SQLObserver` pattern:

```go
// StreamObserver is an optional extension to [stats.Observer] for stream-level
// throughput metrics. Adapters type-assert the configured Observer to StreamObserver.
// Existing Observer implementations need not change.
type StreamObserver interface {
    // RecordStreamItem is called for every item that passes through Apply,
    // success or failure. function is the forge function name.
    RecordStreamItem(function string, success bool, dur time.Duration)
}
```

`NoopObserver`, `LoggingObserver`, and `fanout` all implement it (same pattern as
`SQLObserver` added in adapters/sql).

### Domain event observation — `Tap` operator

Infrastructure metrics answer "how many items, how fast, how many errors" — they go to
Prometheus / slog. Domain events answer "what business value was computed, what alert
was triggered" — they go to business logic or domain logs.

`Tap` is the operator for domain event observation. It is intentionally NOT part of
`stats.Observer` because it carries typed domain values (`T`), not opaque metrics:

```go
// Domain-level observation:
oeeStream := stream.Tap(ctx, oeeStream, func(oee OEE) {
    slog.Info("OEE computed", "oee", float64(oee), "sensor", sensorID)
    if float64(oee) < 0.65 {
        alertCounter.Inc()  // or any domain event handling
    }
})

// Infrastructure-level observation (via options):
oeeStream := stream.Apply(ctx, sensorStream, oeeCalcFn,
    stream.ApplyOptions{Observer: obs})  // RecordStreamItem + RecordApply fire
```

Both forms are composable and orthogonal — a pipeline can have both.

---

## Reactive programming paradigm summary

The complete pattern — source → transform → observe → sink:

```go
ctx := context.Background()
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger))

// ── Forge functions (governed, signed, validated) ─────────────────
oeeCalc := forge.NewFunction("oeeCalc", "1.0.0",
    oeeInCodec, oeeCodec,
    func(in OEEIn) (OEE, error) {
        return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
    },
    forge.FunctionMeta{Author: "OT Engineering", ApprovedBy: "Quality Manager"},
)

// ── MQTT source (raw bytes → typed sensor readings) ───────────────
rawCh := make(chan []byte, 64)  // filled by MQTT SubscribeHandler
sensors := stream.FromCodec(ctx, rawCh, sensorCodec,
    stream.SourceOptions{Name: "mqtt/sensors/+", Observer: obs})

// ── CombineLatest for multi-input OEE function ────────────────────
// (availability and performance arrive on separate MQTT topics)
oeeInputs := stream.CombineLatest2(ctx, availStream, perfStream,
    func(a Availability, p Performance) OEEIn { return OEEIn{a, p} })

// ── Apply forge function — validated, signed computation ──────────
oeeResults := stream.Apply(ctx, oeeInputs, oeeCalc,
    stream.ApplyOptions{Observer: obs})

// ── Domain event observation (Tap) ────────────────────────────────
oeeResults = stream.Tap(ctx, oeeResults, func(oee OEE) {
    slog.Info("OEE computed", "value", float64(oee))
    businessDashboard.Publish(oee)  // domain event — not a metric
})

// ── Filter and time operators ──────────────────────────────────────
alerts := stream.Filter(ctx, oeeResults, func(oee OEE) bool {
    return float64(oee) < 0.65
})
debounced := stream.Debounce(ctx, alerts, 30*time.Second) // alert at most every 30s

// ── Explicit error channel handling ───────────────────────────────
// Drain handles both Values and Errors concurrently — no goroutine leaks
stream.Drain(ctx, debounced,
    func(ctx context.Context, oee OEE) error {
        return publishAlert(ctx, oee)  // business logic — returns error if publish fails
    },
    func(err error) {
        // Explicit error handler — every error is a typed structured error
        var sae stream.StreamApplyError
        var sde stream.StreamDecodeError
        switch {
        case errors.As(err, &sae):
            slog.Warn("OEE computation failed", "error", sae) // slog.LogValuer fires
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

## Batch windowing with `forge.Map`

```go
// Collect readings into windows of 10 (or at most 500ms silence), then batch-process:
batchedReadings := stream.Buffer(ctx, sensors, 10, 500*time.Millisecond)

// forge.Map wraps oeeCalc to process []OEEIn → []OEE
batchOEECalc := forge.Map("batchOEE", "1.0.0", oeeCalc)
batchOEE := stream.Apply(ctx, batchedReadings, batchOEECalc,
    stream.ApplyOptions{Observer: obs})
```

`stream.Buffer` bridges item-at-a-time stream operators with forge's slice-based
collection functions (`forge.Map`, `forge.Filter`, `forge.Reduce`). This is the
primary integration point between streaming and batch computation.

---

## AsyncAPI / OpenAPI spec integration

**Phase 1: none.** The stream pipeline is infrastructure wiring, not an API surface.
The forge functions applied in the pipeline already appear in `PipelineSpec` via
`forge.Registry`. `Tap` and operator calls are Go code, not AsyncAPI operations.

**Phase 2 (shipped):** `stream.Topology` emits a YAML topology document via
`render/stream.Render(topo.Spec())`, listing sources, operators, and sinks. Forge
functions are registered via `stream.WithApply[In,Out](topo, fn)` which captures
`forge.FunctionSpec.Hash` for auditability. See `stream/topology.go` and
`render/stream/render.go`.

---

## Files created (shipped)

Import path: **`github.com/DaniDeer/go-codex/stream`**

> Package was initially placed at `adapters/stream/` and relocated to `stream/`
> after the forge vs stream architectural evaluation confirmed it is not an adapter
> (no external library dependency).

### Phase 1 files

| File | Responsibility |
|---|---|
| `stream/stream.go` | `Stream[T]{Values <-chan T, Errors <-chan error}` |
| `stream/errors.go` | `StreamDecodeError{Source,Err}`, `StreamApplyError{Function,Err}` — `Error()`, `Unwrap()`, `LogValue()` |
| `stream/source.go` | `From[T]`, `FromCodec[T](ctx, <-chan []byte, format.Format[T], SourceOptions)` — accepts any format (JSON, YAML, TOML, custom) |
| `stream/transform.go` | `Apply[In,Out]` (with `TraceObserver` per-item span), `Filter[T]`, `Tap[T]`, `MapErr[T]`, `Retry[T]`, `ApplyOptions` |
| `stream/fanout.go` | `Merge[T]`, `Tee[T]` |
| `stream/time.go` | `Buffer[T]`, `Debounce[T]`, `Throttle[T]` |
| `stream/sink.go` | `Drain[T]`, `Collect[T]`, `DrainOptions` |
| `stream/combine.go` | `CombineLatest2[A,B,Out]` |
| `stream/doc.go` | Package overview: reactive paradigm, forge integration, observer kinds |
| `stream/*_test.go` | 60+ tests + `ExampleFrom`, `ExampleFromCodec`, `ExampleApply`, `ExampleTap`, `ExampleDrain` |
| `stats/observer.go` | `StreamObserver` interface + `NoopObserver`/`LoggingObserver`/`fanout` implementations |
| `stats/observer_test.go` | Compile-time assertion + delegation tests for `StreamObserver` |
| `.github/instructions/go-codex.instructions.md` | `stream` + `render/stream` rows in Package Structure table |

### Phase 2 files (added)

| File | Responsibility |
|---|---|
| `stream/topology.go` | `Topology`, `TopologySpec`, `NewTopology`, `WithApply[In,Out]` (free function — captures `forge.FunctionSpec.Hash`), `StepKind*` constants |
| `stream/topology_test.go` | Tests for topology builder: steps, WithApply hash capture, info fields |
| `render/stream/render.go` | `Render(TopologySpec) ([]byte, error)` — YAML topology document with `streamTopology: 1.0` header |
| `render/stream/render_test.go` | Render output tests |
| `docs/features/stream.md` | Feature reference: forge vs stream, operator table, observer kinds, error types |
| `docs/guides/stream.md` | 7-step workflow guide: source → apply → tap → filter → drain → CombineLatest2 → topology |

**No external dependencies.** `stream` imports: `codex`, `format`, `forge`, `stats`,
`context`, `time`, `sync` — all already in the module.

---

## Phase 2 — delivered

- ✅ **Stream topology YAML renderer** — `stream.Topology` + `render/stream.Render`; forge function hashes captured via `stream.WithApply[In,Out]`
- ✅ **`Retry[T]`** — named alias for `MapErr` with caller-controlled timing/backoff logic
- ✅ **Per-item `TraceObserver` span in `Apply`** — child span wraps each `forge.Function.ApplyContext` call when `opts.Observer` implements `stats.TraceObserver`
- ✅ **`FromCodec` format flexibility** — accepts `format.Format[T]` (JSON, YAML, TOML, custom) instead of hardcoded JSON
- ✅ **`Example()` functions** — 5 functions for pkg.go.dev (`ExampleFrom`, `ExampleFromCodec`, `ExampleApply`, `ExampleTap`, `ExampleDrain`)
- ✅ **`docs/features/stream.md`** + **`docs/guides/stream.md`** — feature page and step-by-step guide
- ✅ **`sensor-service` example** updated to use `stream.FromCodec` + `stream.Apply` + `stream.Tap` + `stream.Filter` + `stream.Drain` + `stream.Topology`

## Phase 3 — delivered

- ✅ **`Window[T]`** — fixed-interval tumbling windows via `time.NewTicker`; emits `[]T` per tick (even empty); complements `Buffer` for calendar-aligned time slot boundaries
- ✅ **`SlidingWindow[T]`** — count-based overlapping windows: every `step` items, emit last `size` items; `step==size` = tumbling
- ✅ **`FlatMapSlice[In,Out]`** — each In → `[]Out`; items emitted individually; empty slice acts as filter; no goroutine pool needed
- ✅ **`CombineLatest3[A,B,C,Out]`** — 3-source CombineLatest; ideal for OEE (Availability × Performance × Quality on separate MQTT topics)
- ✅ **`CombineLatest4[A,B,C,D,Out]`** — 4-source CombineLatest
- ✅ **`Zip[A,B,Out]`** — pairs items by position from two streams; waits for matched n-th items (unlike CombineLatest which uses latest values)

## Dropped from plan (Phase 3 evaluation)

- **`RetryWithBackoff[T]`** — design incompatible with current error model: `Stream.Errors` carries `error`, not the original item, so retry can't re-invoke the computation. Correct pattern: wrap the forge function in a retry loop before passing to `stream.Apply`. Current `Retry[T]` with caller-controlled `time.Sleep` is sufficient.
- **Integration test (real MQTT broker)** — CI infrastructure concern, not a library item.

## Out of scope (Phase 4)

- `FlatMap[In,Out]` (sub-stream variant) — each In emits a `Stream[Out]`; requires goroutine pool + concurrency limit; `FlatMapSlice` covers most cases
- `GroupBy[T,K]` — dynamically created sub-streams have lifecycle ambiguity (when does a per-key stream close?); needs real-world use case validation before API design
- `CombineLatest5+` — use nested `CombineLatest2` + struct intermediate for N>4

## Resolved design decisions

1. **Single `Item[T]` channel vs separate `Values`/`Errors` channels:** ✅ **Separate channels** — more idiomatic Go, clearer API, selective handling, avoids type switches on every item.

2. **Fluent builder vs free functions:** ✅ **Free functions** — Go generics cannot add new type parameters to methods; free functions are the correct pattern (same as `forge.NewFunction`, `forge.Compose`, `forge.Map`).

3. **Error propagation in Apply:** ✅ **Error to `Stream.Errors`, stream continues** — one bad sensor reading must not terminate a continuous monitoring pipeline. `MapErr` and `Drain`'s `onError` give the user full control over error handling.

4. **Forge changes needed:** ✅ **None in Phase 1** — `stream` imports `forge` and wraps `Function.ApplyContext`. Circular dependency avoided. Forge stays as the governed synchronous computation layer.

5. **Two observer kinds:** ✅ **`stats.StreamObserver` for infrastructure metrics + `Tap` for domain events** — orthogonal, composable, correct separation of concerns.

6. **Package placement (`adapters/stream` vs `stream/`):** ✅ **Top-level `stream/`** — the `adapters/` convention requires an external library dependency (paho.mqtt, goose, mcp-go, etc.); `stream/` uses only go-codex packages and stdlib. Relocated from `adapters/stream` to `stream/` after post-implementation architectural review. Import path: `github.com/DaniDeer/go-codex/stream`.

7. **Unify `forge` and `stream` into one module?** ✅ **No — keep separate** — different execution models (sync batch vs async push), different governance model (forge has SHA-256/Author/Approval, stream has none), overlapping names with different semantics (`Map`/`Filter` over `[]T` vs `<-chan T`). Full evaluation documented in the "forge vs stream" section above.

8. **`FromCodec` format parameter type:** ✅ **`format.Format[T]` (not `codex.Codec[T]`)** — accepts any format (JSON, YAML, TOML, custom); consistent with MQTT adapters; caller wraps: `format.JSON(codec)`. Changed in Phase 2.

9. **Stream topology API design:** ✅ **`Topology` builder + `WithApply[In,Out]` free function** — `WithApply` must be a free function (Go generics cannot add type params to methods); captures `forge.FunctionSpec.Hash` for auditability; `render/stream.Render(topo.Spec())` produces `streamTopology: 1.0` YAML.

10. **`Retry[T]` vs `RetryWithBackoff[T]`:** ✅ **Simple `Retry[T]` alias for `MapErr`** — caller's retry closure controls timing and backoff; keeps the operator minimal; `RetryWithBackoff` deferred to Phase 3 if a common pattern emerges.
