# Stream — FlatMap (sub-stream per item) — `stream`

> **Status:** Awaiting use case — deferred until a concrete driver appears.
> [← Back to Roadmap](index.md)
>
> See also: [Feature: Reactive Streams](../features/stream.md) · [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream)

This is the surviving item from the retired Phase-4 stream roadmap. The other
Phase-4 items have shipped or been resolved:

- **GroupBy + Switch routing** — implemented in `stream/route.go`
  (`GroupBy`, `Switch`, `SwitchKey`, `OfType`, `SwitchType2/3`, `SplitEither`);
  see the [routing section of the stream guide](../guides/stream.md#step-4b--route-with-switch-and-groupby).
- **CombineLatest5+** — resolved via nested composition, documented in the
  [stream guide](../guides/stream.md#step-6--multi-source-with-combinelatest2).

---

## `FlatMap[In, Out]` — sub-stream per input item

### Why deferred

`FlatMapSlice[In,Out]` covers the common case: one input item expands
synchronously into `[]Out`. The sub-stream variant is different: each input
item spawns a `Stream[Out]` that may emit items over time, potentially
concurrently.

The main real-world driver has been absorbed elsewhere: **"per item, produce
N results through IO" is now `ports.IOPort` + a 1→N adapter**
(`sql.QueryEachAdapter`, `file.ReadAdapter`, `nethttp.CallAdapter`, …) —
sequential per item, with codec validation and observer integration built in.
What remains for FlatMap is the narrower "concurrent sub-streams of pure
computation" case, which no example or user has needed yet.

### Proposed API

```go
// FlatMap maps each value item to a Stream[Out], then merges all sub-streams
// into a single output stream. maxConcurrency limits how many sub-streams can
// be active simultaneously (0 = unbounded, which is rarely safe).
//
// Errors from src and from active sub-streams all propagate to Stream.Errors.
//
// Use FlatMapSlice for the simpler synchronous one-to-many case.
func FlatMap[In, Out any](
    ctx context.Context,
    src Stream[In],
    fn func(In) Stream[Out],
    maxConcurrency int,
) Stream[Out]
```

### Implementation notes

- A semaphore channel (`make(chan struct{}, maxConcurrency)`) controls concurrency
- Each sub-stream goroutine acquires a semaphore slot, forwards values and errors, then releases
- When `maxConcurrency == 0`: launch without limit (document as footgun)
- `sync.WaitGroup` to close output channels after all sub-streams finish
- **Merge order decision: unordered** (first sub-stream to produce wins) —
  simpler and faster; ordered merge requires buffering with no known consumer

### Trigger for implementation

A concrete use case that needs concurrent per-item sub-streams of pure
computation (IO-driven cases should use `ports.IOPort` + 1→N adapters instead).

---

## Out of scope (will not implement)

- **`RetryWithBackoff[T]`** — design incompatible with the error model:
  `Stream.Errors` carries `error`, not the original item, so retry can't
  re-invoke the computation. Correct pattern: wrap the forge function in a
  retry loop before `stream.Apply`.
- **Integration test with real MQTT broker in CI** — CI infrastructure
  concern; not a library feature.
