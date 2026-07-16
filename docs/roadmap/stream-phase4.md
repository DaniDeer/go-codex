# Reactive stream pipelines — Phase 4 — `stream`

> **Status:** Awaiting use case — `FlatMap` and `GroupBy` stay deferred until
> a concrete driver appears; the third original item (`CombineLatest5+`) is
> **resolved** via nested composition, now documented in the
> [stream guide](../guides/stream.md#step-6--multi-source-with-combinelatest2)
> (review pass, 2026-07-16).
> [← Back to Roadmap](index.md)
>
> See also: [Feature: Reactive Streams](../features/stream.md) · [`stream` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stream)

Phase 4 contains operators that were deferred because they require either
concurrency infrastructure design or a validated real-world API shape before
implementation. The existing stream package (`From`, `FromCodec`, `Single`,
`Apply`, `Map`, `Filter`, `Tap`, `MapErr`, `Retry`, `FlatMapSlice`, `Merge`,
`Tee`, `BroadcastHub`, `CombineLatest2/3/4`, `Zip`, `Buffer`, `Window`,
`SlidingWindow`, `Debounce`, `Throttle`, `Drain`, `Collect`) covers the vast
majority of real-world reactive pipeline use cases.

---

## 1. `FlatMap[In, Stream[Out]]` — sub-stream per input item

### Why deferred

`FlatMapSlice[In,Out]` (Phase 3) covers the common case: one input item expands
synchronously into `[]Out`. The sub-stream variant is different: each input item
spawns a `Stream[Out]` that may emit items over time, potentially concurrently.

The main real-world driver has since been absorbed elsewhere: **"per item,
produce N results through IO" is now `ports.IOPort` + a 1→N adapter**
(`sql.QueryEachAdapter`, `file.ReadAdapter`, `nethttp.CallAdapter`, …) —
sequential per item, with codec validation and observer integration built in.
What remains for FlatMap is the narrower "concurrent sub-streams of pure
computation" case, which no example or user has needed yet.

This requires:
- A goroutine pool or concurrency limit (unbounded goroutines = resource leak)
- A strategy for merging the sub-streams: ordered (zip-like), unordered (merge-like), or windowed
- Error channel routing: do errors from sub-streams propagate to the parent stream?

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

### Design decision needed

**Merge order:** should `FlatMap` emit items in sub-stream completion order (unordered
merge = `Merge`) or preserve the input order of the parent items (ordered merge =
interleave at completion boundaries)? Unordered is simpler and faster; ordered requires
buffering. **Default: unordered (first sub-stream to produce wins).**

---

## 2. `GroupBy[T, K]` — split stream by key into sub-streams

### Why deferred

`GroupBy` needs to expose dynamically-created sub-streams when new keys are first seen.
Two unresolved design questions block implementation:

**Question 1 — How are sub-streams exposed?**

Option A: `<-chan KeyedStream[K, T]` — caller receives new `KeyedStream` values as
keys appear; must drain the meta-channel AND each sub-stream concurrently.
```go
type KeyedStream[K comparable, T any] struct { Key K; Stream Stream[T] }
func GroupBy[T any, K comparable](ctx, src, key func(T) K) <-chan KeyedStream[K, T]
```

Option B: Callback — user provides an `onNewKey func(K, Stream[T])` that is called
once per new key; simpler for consumers.
```go
func GroupBy[T any, K comparable](ctx, src Stream[T], key func(T) K,
    onNewKey func(K, Stream[T]))
```

**Question 2 — When does a per-key sub-stream close?**

- When the parent stream closes? — simple, but sub-streams may go idle for long periods
- After a silence window per key? — requires per-key timers
- Never (caller controls lifetime via context)? — simplest; caller cancels when done

### Proposed API (pending question resolution)

```go
// GroupBy splits src into per-key sub-streams. onKey is called once for each
// new key seen, with a Stream[T] that receives only items with that key.
// The per-key stream closes when src closes or ctx is cancelled.
func GroupBy[T any, K comparable](
    ctx context.Context,
    src Stream[T],
    key func(T) K,
    onKey func(K, Stream[T]),
)
```

This uses Option B (callback) and closes on parent-stream-close. It's the simplest
design that avoids goroutine leaks.

### Trigger for implementation

When a concrete use case requires per-sensor-ID pipelines with different forge
functions per key, revisit this API with a real example to validate the callback shape.

---

## 3. `CombineLatest5+` and variadic `CombineLatestN` — ✅ resolved

> **Resolved (2026-07-16):** Option B (nested composition) adopted and
> documented in the [stream guide](../guides/stream.md#step-6--multi-source-with-combinelatest2)
> with a six-source example. Code-generated `CombineLatest5/6` remain a
> possibility ONLY if a concrete use case demands them — none has.

### Why it was deferred

Go generics cannot express variadic type parameters. `CombineLatest2`, `CombineLatest3`,
and `CombineLatest4` are implemented (Phase 3). For N > 4:

**Option A — Code-generate `CombineLatest5`, `CombineLatest6`, ...`CombineLatest8`**
- Tedious but type-safe; follows the same pattern as `CombineLatest3/4`
- Each function is ~60 lines; 4 more functions = ~240 lines

**Option B — Nested composition**
- `CombineLatest2(CombineLatest3(a,b,c,f1), CombineLatest2(d,e,f2), merge)` 
- No new code; works for any N; slightly verbose at call site

**Option C — `CombineLatestN` via `[]Stream[T]` + reflection (type-unsafe)**
- Requires all sources to be the same type `T`
- Emits `[]T` (one per source) — loses individual type information
- Useful for N identical sensors; not useful for heterogeneous inputs

### Recommendation

**Option B is sufficient for Phase 4.** Document it in the guide. Add code-generated
`CombineLatest5` and `CombineLatest6` only if a concrete use case requests > 4
heterogeneous sources (6-factor OEE, N-sensor fusion).

---

## Out of scope (will not implement)

- **`RetryWithBackoff[T]`** — design incompatible with the error model: `Stream.Errors`
  carries `error`, not the original item, so retry can't re-invoke the computation.
  Correct pattern: wrap the forge function in a retry loop before `stream.Apply`.

- **Integration test with real MQTT broker in CI** — CI infrastructure concern; not a
  library feature.
