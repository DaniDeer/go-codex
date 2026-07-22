# Pipeline Segmentation — `PipePort[T]`

> **Status:** SHIPPED — `ports/pipe_port.go`.
> [← Back to Roadmap](index.md)

## Motivation

`PipePort` is the primary tool for segmenting computation pipelines into
named, observable stages, declared flexibly at setup time and never
mutated at runtime — the same "declare, then start" lifecycle every other
port type in this package follows. `ports.ChainStream` connects stages
(with `ports.Chain` as its single-step convenience); side observers tap
into any stage via `OutputPort().Bind(...)`. IO/adapter bridging
(`InputPort`/`OutputPort` with transport adapters) is a supported
convenience wrapper, not the primary use — and PipePort itself is a thin
wrapper over existing primitives (`SourcePort`, `SinkPort`,
`gstream.Map`/`gstream.Drain`), not a new adapter model or a custom hub
reimplementation.

## API surface

```go
pp, _ := ports.NewPipePort[T]("name", codec, ports.PortOptions{Buffer: 16})

// Primary: computation stage segmentation
ports.Chain(ctx, from, fn, to)                 // ONE value-mapping function: from → fn → to
ports.ChainStream(ctx, from, transform, to)    // ANY stream transform: from → transform → to
                                                //   (the general primitive; Chain wraps a single
                                                //   gstream.Map through ChainStream internally)
s := pp.Stream(ctx)                            // lower-level: gstream.Stream[T], what Chain/ChainStream use
pp.Push(ctx, v)                                // feed items into the pipe — valid any time

// Secondary: IO/adapter bridging
in := pp.InputPort("mqtt")                     // returns *SourcePort[T], fan-in
out := pp.OutputPort("sse")                    // returns *SinkPort[T], fan-out

pp.Connect(ctx)                                // start the pipe; topology now fixed
```

## Key design decisions

- **`ChainStream[In, Out](ctx, from, transform, to)` is the general
  stage-connector; `Chain` is its single-step special case, not a separate
  mechanism.** `ChainStream` accepts any
  `func(gstream.Stream[In]) gstream.Stream[Out]` — so a multi-step
  sub-pipeline (several `Map`/`Filter` calls) connects two PipePorts with
  the SAME `(ctx, from, to)` call shape as a single-step transition. `Chain`
  is defined literally in terms of `ChainStream`:
  ```go
  func Chain[In, Out any](ctx context.Context, from *PipePort[In], fn func(In) (Out, error), to *PipePort[Out]) {
      ChainStream(ctx, from, func(s gstream.Stream[In]) gstream.Stream[Out] {
          return gstream.Map(ctx, s, fn, gstream.MapOptions{})
      }, to)
  }
  ```
  This was a design correction mid-implementation: the first draft required
  a hand-written wrapper function (with an *invented* `(ctx, from, to)`
  signature that happened to coincidentally match `Chain`'s) for any
  multi-step transition. Recognizing that coincidence as a signal — not a
  coincidence at all — led to generalizing `Chain` into `ChainStream`
  instead of leaving multi-step composition as an unofficial convention.
- **One ordering rule, not two.** Earlier drafts required `Stream()` before
  `Connect()` AND `Push()` after `Connect()` — two different, easy-to-invert
  rules. `Push` now works at any time (its channel is allocated eagerly in
  the constructor, not lazily in `Connect`) — items simply buffer until
  `Connect`'s consumer goroutine starts draining them. The only remaining
  rule: register `InputPort`/`OutputPort`/`Stream`/`Chain`/`ChainStream` for
  a pipe *before* that pipe's `Connect()`. `Chain`/`ChainStream` themselves
  only need to precede the **upstream** pipe's `Connect` — the downstream
  pipe's `Connect` may happen before or after.
- **No dynamic/runtime hot-add.** Confirmed scope: pipelines are segmented
  and wired at setup time, then run unchanged — matching `SourcePort`'s
  "Bind before Stream" and `SinkPort`'s "Bind before Feed" conventions
  elsewhere in this package. `stream.BroadcastHub` (which supports dynamic
  `Subscribe`/`Unsubscribe` at any time) was considered as an internal
  backing store but rejected — it solves a hot-add problem PipePort
  doesn't have, and would have added subscription-lifecycle complexity for
  no user-visible benefit under this scope.
- **`Connect` is idempotent-safe.** A second call is a no-op (reported via
  the observer as a failed `"port.bind"` event) instead of double-reading
  every bound `SourcePort` — a bug in the first draft.
- **No `PipePushError` type.** With `pushCh` always live from construction,
  there is no "not connected" failure mode to report — `Push` returns
  `ctx.Err()` on cancellation only, mirroring `SinkPort.Push`'s own
  contract. One fewer error type to learn.
- **Same name, same instance**: `InputPort("mqtt")` called twice returns the
  same `*SourcePort[T]`. Same for `OutputPort`.
- **Names are scoped**: `InputPort("x")` and `OutputPort("x")` are
  independent — full qualified names are `pipe/in/x` and `pipe/out/x`.
- **Fan-in on input**: bind multiple `SourceAdapter`s to same input port.
- **Fan-out on output**: bind multiple `SinkAdapter`s to same output port.
- **Zero new adapter interfaces** — reuses `SourceAdapter` and `SinkAdapter`.
- **Modular pipeline composition, unchanged by the `ChainStream` addition.**
  `BuildPipeline(ctx) PipelineIO` in `examples/pipeline-segmentation` still
  composes stage-connecting calls (`Chain`, `ChainStream`) exactly like a
  `forge`/`pipeline.Build(ctx, ...)`-style top-level builder composes pure
  functions — the difference is that a multi-step transition is now a
  direct `ChainStream` call instead of a hand-written wrapper function.
  Structure (`PipePort`/codec/type declarations) stays `var`s — no side
  effects; wiring (`Chain`, `ChainStream`, `Connect`) stays in functions
  taking `ctx` — starting goroutines is an action, not a declaration. This
  mirrors `examples/sensor-service`'s own `pipeline.Build(ctx, ...)`
  layering.

## Observer integration

Reuses `stats.Observer`: `RecordSubscribe` on input events, `RecordPublish`
on output fan-out, `RecordRequest("port.bind", ...)` on a rejected double
`Connect`. Resolved from `PortOptions.Observer` or context.

## Pipeline spec generation — fully derived (`PipelineSpec`)

A first pass proved `PipePort` could be *manually* documented with the
existing `stream.Topology`/`render/stream` machinery (`Topology.WithPort`
for each PipePort). That worked, but every name, buffer size, and adapter
identity in the spec was a hand-typed string sitting next to the real code
— nothing stopped it drifting out of sync if the wiring changed and the
description wasn't updated.

`ports.PipelineSpec(title, version string, pipes ...PipeSpecSource) gstream.TopologySpec`
closes that gap: it builds the spec by *reading* the pipes' actual state,
not by re-describing it:

- **`SourcePort.BoundAdapters()` / `SinkPort.BoundAdapters()`** (new) —
  every bound adapter's real `AdapterName()`, tracked at `Bind` time
  (previously used transiently for observer/error events, then discarded).
- **`ChainEdge{Kind, To, Func}`** (new) — recorded by `Chain`/`ChainStream`
  on their `from` pipe. `Func` is the transform's real Go function identity
  via `reflect.ValueOf(fn).Pointer()` + `runtime.FuncForPC` — e.g.
  `"main.validateReading"` for a named function, or an honestly
  closure-opaque `"main.BuildPipeline.func1"` for an inline `ChainStream`
  transform. Never fabricated.
- **`PipePort.Buffer()`, `InputAdapters()`, `OutputAdapters()`, `OutEdges()`**
  (new accessors) — expose the above as real data.
- **`PipeSpecSource` interface** (new) — `Name`/`Buffer`/`InputAdapters`/
  `OutputAdapters`/`OutEdges`, none of which reference the pipe's payload
  type `T`. `*PipePort[T]` satisfies it for any `T`, so a single
  `PipelineSpec` call accepts a heterogeneous pipeline
  (`PipePort[Raw]`, `PipePort[Validated]`, `PipePort[Calibrated]`, …) —
  something a generic `[]*PipePort[T]` slice could never hold.

Only `title`, `version`, and the pipes' *ordering* remain caller-supplied —
there is no pipe field to read a pipeline-level title from, and ordering is
a presentation choice, not a fact about any single pipe.

`examples/pipeline-segmentation/main.go` demonstrates this end-to-end:
`ports.PipelineSpec("Pipeline Segmentation Demo", "1.0.0", Raw, Valid, Calibrated)`
replaces the earlier hand-typed `WithSource`/`WithPort`/`WithSink` block
entirely. The rendered YAML shows real derived facts: `Buffer=8`, the exact
bound `ports.ChanSourceAdapter`/`ports.ChanSinkAdapter` names per port, and
`main.validateReading` as the real function behind the `Chain` edge.

## Unit tests (26 tests)

| ID | Test | Verifies |
|---|---|---|
| PP-01 | NewPipePort valid construction | name matches |
| PP-02 | InputPort same instance | same name → same SourcePort |
| PP-03 | OutputPort same instance | same name → same SinkPort |
| PP-04 | Input/Output scoped | same local name, different full names |
| PP-05 | Single input → single output | items propagate end-to-end |
| PP-06 | Fan-in | 2 inputs → 1 output gets all items |
| PP-07 | Fan-out | 1 input → 2 outputs each get all items |
| PP-08 | Fan-in + fan-out | 2 inputs → 2 outputs each get all items |
| PP-09 | Port naming | qualified names: `pipe/in/name`, `pipe/out/name` |
| PP-10 | Push before Connect buffers | Push succeeds pre-Connect; item arrives once Connect drains it |
| PP-11 | Connect no inputs | no panic, no output items |
| PP-12 | Concurrent access | InputPort/OutputPort safe under concurrent calls |
| PP-13 | Full round-trip | SourcePort→pipe→SinkPort wiring |
| PP-14 | Chain | two PipePorts wired through a transform deliver correctly |
| PP-15 | Chain order-independent downstream Connect | `to.Connect` before `from.Connect` still works |
| PP-16 | Connect double-invocation safe | second Connect call is a no-op, no duplicate delivery |
| PP-17 | ChainStream multi-step Map | 3 chained `gstream.Map` calls inside one `ChainStream` transform deliver correctly |
| PP-18 | ChainStream Filter + Map | `ChainStream` supports non-Map operators (`Filter` then `Map`), not just Map chains |
| PP-19 | Chain is ChainStream's special case | `Chain` and an equivalent hand-written `ChainStream` call produce identical output — confirms `Chain`'s behavior is unchanged after the refactor |
| PP-20 | SourcePort.BoundAdapters | reports real bound adapter names, in Bind order |
| PP-21 | SinkPort.BoundAdapters | reports real bound adapter names, in Bind order |
| PP-22 | Chain records a real ChainEdge | Kind="chain", To=destination name, Func contains the real function name |
| PP-23 | ChainStream records a real ChainEdge | Kind="chainStream", To=destination name, Func non-empty |
| PP-24 | Buffer/InputAdapters/OutputAdapters | reflect real configured buffer and bound adapter names, keyed by port name |
| PP-25 | PipelineSpec derives real data | rendered steps' Name/Description contain the real buffer, adapter names, and function identity — not placeholders |
| PP-26 | PipelineSpec heterogeneous types | `[]PipeSpecSource` holds `PipePort[int]`, `PipePort[string]`, `PipePort[cfgItem]` together; compiles and produces correct per-pipe steps |

## Files

| File | Responsibility |
|---|---|
| `ports/pipe_port.go` | `PipePort`, `NewPipePort`, `InputPort`, `OutputPort`, `Stream`, `Push`, `Connect`, `ChainStream` (general primitive), `Chain` (single-Map convenience), both recording a `ChainEdge` via the shared internal `chainWire` |
| `ports/pipeline_spec.go` | `ChainEdge`, `PipeSpecSource`, `PipelineSpec`, `funcName` (reflection-based real function identity), `PipePort.Buffer/InputAdapters/OutputAdapters/OutEdges` |
| `ports/source_port.go` | `SourcePort.BoundAdapters()` + tracking field, populated in `Bind` |
| `ports/sink_port.go` | `SinkPort.BoundAdapters()` + tracking field (reuses existing `mu`), populated in `Bind` |
| `ports/pipe_port_test.go` | 26 tests (PP-01 through PP-26) |
| `examples/pipeline-segmentation/main.go` | runnable 3-stage example: `ports.Chain` for a 1-function transition, `ports.ChainStream` with an inline 3-step `gstream.Map` transform for the multi-step transition, composed by one top-level `BuildPipeline(ctx) PipelineIO`; spec generation via a single `ports.PipelineSpec(title, version, Raw, Valid, Calibrated)` call — verified to contain real derived data (buffer sizes, adapter names, real function identities), not hand-typed placeholders |