# Stream Adapter — reactive pipelines over Go channels — `adapters/stream`

> **Status:** Design complete — not yet implemented.
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

`adapters/stream` provides a **declarative reactive pipeline** over typed Go channels:
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

In `adapters/stream` this is the `Tap` operator:

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

## What forge already provides (and what needs no change)

Forge's batch/synchronous design is correct and complete for its purpose:

| forge today | Streaming role |
|---|---|
| `Function[In, Out]` — validates In, runs compute, validates Out | The per-item operation inside `stream.Apply` |
| `Apply(in In) (Out, error)` — synchronous, single item | Called once per item; `stream.Apply` wraps this in a goroutine loop |
| `Compose[A, B, Out]` — chain two functions | Functions composed with Compose work transparently in `stream.Apply` |
| `Map`, `Filter`, `Reduce` over `[]T` slices | Use with `stream.Buffer` to process windowed batches |
| `Registry` + `PipelineSpec` | Forge functions registered with the Registry also document the streaming pipeline's computation |
| `PipelineObserver.RecordApply` | Fires inside `forge.Function.Apply` — already observable per-item |

**Forge itself does NOT need to change.** `adapters/stream` imports `forge` and wraps it:

```
codex  ←  forge  ←  adapters/stream
```

If forge imported `adapters/stream` for a streaming `ApplyStream` method, it would
create a circular dependency (stream imports forge, forge imports stream). The free
function design (`stream.Apply(ctx, s, fn, opts)`) avoids this entirely.

---

## Forge enhancements (Phase 2 consideration, not Phase 1)

While forge needs no changes for the stream adapter to work, one enhancement would
improve ergonomics for users building large reactive pipelines:

**`forge.Function.Spec.AsStreamPipelineStep()`** — returns a human-readable description
of this function as a pipeline step, for use in stream topology YAML rendering (Phase 2).
This is a documentation method only, not a runtime dependency.

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

**Phase 2:** A `stream.Topology` type could emit a YAML topology document analogous to
`render/pipeline.Render(reg.Spec())`, listing sources, operators, and sinks. This
would require forge functions to be registered with the stream topology before calling
`Apply` — a small API addition deferred to Phase 2.

---

## Files to create

| File | Responsibility |
|---|---|
| `adapters/stream/stream.go` | `Stream[T]` type, helper constructors |
| `adapters/stream/errors.go` | `StreamDecodeError`, `StreamApplyError` — `Error()`, `Unwrap()`, `LogValue()` |
| `adapters/stream/source.go` | `From[T]`, `FromCodec[T]`, `SourceOptions` |
| `adapters/stream/transform.go` | `Apply[In,Out]`, `Filter[T]`, `Tap[T]`, `MapErr[T]`, `ApplyOptions` |
| `adapters/stream/fanout.go` | `Merge[T]`, `Tee[T]` |
| `adapters/stream/time.go` | `Buffer[T]`, `Debounce[T]`, `Throttle[T]` |
| `adapters/stream/sink.go` | `Drain[T]`, `Collect[T]`, `DrainOptions` |
| `adapters/stream/combine.go` | `CombineLatest2[A,B,Out]` |
| `adapters/stream/doc.go` | Package overview: reactive paradigm, explicit error channels, forge integration |
| `adapters/stream/errors_test.go` | `StreamDecodeError`/`StreamApplyError` LogValue, Unwrap, errors.As |
| `adapters/stream/source_test.go` | `From`, `FromCodec` happy path + error propagation |
| `adapters/stream/transform_test.go` | `Apply` happy/error/observer, `Filter`, `Tap` called, `MapErr` recovery |
| `adapters/stream/time_test.go` | `Buffer`, `Debounce`, `Throttle` with fake time |
| `adapters/stream/sink_test.go` | `Drain` both channels concurrent, `Collect`, ctx cancellation |
| `adapters/stream/combine_test.go` | `Merge`, `Tee`, `CombineLatest2` |
| `stats/observer.go` | Add `StreamObserver` interface; update `NoopObserver`, `LoggingObserver`, `fanout` |
| `stats/observer_test.go` | Compile-time assertion + delegation test for `StreamObserver` |
| `.github/instructions/go-codex.instructions.md` | Add `adapters/stream` row |

**No external dependencies.** `adapters/stream` imports: `codex`, `forge`, `stats`,
`time`, `context`, `sync` — all already in the module.

---

## Out of scope (Phase 2)

- Stream topology YAML renderer (analogous to `render/pipeline`)
- `Retry[T]` with exponential backoff and jitter
- `Zip[A,B,Out]` — pair items by position (not latest value)
- `CombineLatest3` and beyond — require separate generic functions
- `Window[T]` — emit overlapping windows (sliding vs tumbling)
- `FlatMap[In,Out]` — each In emits a Stream[Out] (concurrency control needed)
- `GroupBy[T,K]` — split stream by key into sub-streams
- Context propagation for per-item trace spans (each item gets a child span from the stream context)
- Integration test with real MQTT broker in CI

## Resolved design decisions

1. **Single `Item[T]` channel vs separate `Values`/`Errors` channels:** ✅ **Separate channels** — more idiomatic Go, clearer API, selective handling, avoids type switches on every item.

2. **Fluent builder vs free functions:** ✅ **Free functions** — Go generics cannot add new type parameters to methods; free functions are the correct pattern (same as `forge.NewFunction`, `forge.Compose`, `forge.Map`).

3. **Error propagation in Apply:** ✅ **Error to `Stream.Errors`, stream continues** — one bad sensor reading must not terminate a continuous monitoring pipeline. `MapErr` and `Drain`'s `onError` give the user full control over error handling.

4. **Forge changes needed:** ✅ **None in Phase 1** — `adapters/stream` imports `forge` and wraps `Function.ApplyContext`. Circular dependency avoided. Forge stays as the governed synchronous computation layer.

5. **Two observer kinds:** ✅ **`stats.StreamObserver` for infrastructure metrics + `Tap` for domain events** — orthogonal, composable, correct separation of concerns.
