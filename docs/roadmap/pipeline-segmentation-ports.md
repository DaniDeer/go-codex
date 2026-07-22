# Pipeline Segmentation — `PipePort[T]`

> **Status:** SHIPPED — `ports/pipe_port.go`.
> [← Back to Roadmap](index.md)

## Motivation

`PipePort` is the primary tool for segmenting computation pipelines into
named, observable stages, declared flexibly at setup time and never
mutated at runtime — the same "declare, then start" lifecycle every other
port type in this package follows. `ports.Chain` connects stages; side
observers tap into any stage via `OutputPort().Bind(...)`. IO/adapter
bridging (`InputPort`/`OutputPort` with transport adapters) is a supported
convenience wrapper, not the primary use — and PipePort itself is a thin
wrapper over existing primitives (`SourcePort`, `SinkPort`,
`gstream.Map`/`gstream.Drain`), not a new adapter model or a custom hub
reimplementation.

## API surface

```go
pp, _ := ports.NewPipePort[T]("name", codec, ports.PortOptions{Buffer: 16})

// Primary: computation stage segmentation
ports.Chain(ctx, from, transformFn, to)       // wires from → transform → to in one call
s := pp.Stream(ctx)                           // lower-level: gstream.Stream[T] for custom Map/Filter chains
pp.Push(ctx, v)                               // feed items into the pipe — valid any time

// Secondary: IO/adapter bridging
in := pp.InputPort("mqtt")                    // returns *SourcePort[T], fan-in
out := pp.OutputPort("sse")                   // returns *SinkPort[T], fan-out

pp.Connect(ctx)                               // start the pipe; topology now fixed
```

## Key design decisions

- **`Chain[In, Out](ctx, from, fn, to)` is the headline stage-connector.**
  It wraps `from.Stream(ctx)` + `gstream.Map` + `gstream.Drain` + `to.Push`
  — the composition every multi-stage pipeline needs, written once instead
  of once per stage boundary. `Stream()` remains available directly for
  callers who need `Filter`/custom composition instead of a single `Map`.
- **One ordering rule, not two.** Earlier drafts required `Stream()` before
  `Connect()` AND `Push()` after `Connect()` — two different, easy-to-invert
  rules. `Push` now works at any time (its channel is allocated eagerly in
  the constructor, not lazily in `Connect`) — items simply buffer until
  `Connect`'s consumer goroutine starts draining them. The only remaining
  rule: register `InputPort`/`OutputPort`/`Stream`/`Chain` for a pipe
  *before* that pipe's `Connect()`. `Chain` itself only needs to precede the
  **upstream** pipe's `Connect` — the downstream pipe's `Connect` may happen
  before or after `Chain` is set up.
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
- **Modular composition is a convention, not new API.** A multi-step stage
  transition can be factored into its own function using the SAME
  `(ctx, from, to)` shape as `Chain` — e.g.
  `buildCalibrationStage(ctx, in, out)` in
  `examples/pipeline-segmentation`. A top-level `BuildPipeline(ctx)` then
  composes `Chain` calls and stage-builder calls identically; a
  hand-written multi-step sub-pipeline is indistinguishable from a `Chain`
  call to the pipeline builder that wires it in. This mirrors
  `examples/sensor-service`'s own `pipeline.Build(ctx, ...)` layering: small
  ctx-scoped builder functions assembled by one top-level builder. Structure
  (`PipePort`/codec/type declarations) stays `var`s — no side effects;
  wiring (`Chain`, stage builders, `Connect`) stays in functions taking
  `ctx` — starting goroutines is an action, not a declaration.

## Observer integration

Reuses `stats.Observer`: `RecordSubscribe` on input events, `RecordPublish`
on output fan-out, `RecordRequest("port.bind", ...)` on a rejected double
`Connect`. Resolved from `PortOptions.Observer` or context.

## Unit tests (16 tests)

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

## Files

| File | Responsibility |
|---|---|
| `ports/pipe_port.go` | `PipePort`, `NewPipePort`, `InputPort`, `OutputPort`, `Stream`, `Push`, `Connect`, `Chain` |
| `ports/pipe_port_test.go` | 16 tests (PP-01 through PP-16) |
| `examples/pipeline-segmentation/main.go` | runnable 3-stage example: `ports.Chain` for a 1-function transition, a `buildCalibrationStage(ctx, in, out)` stage builder for a 3-step `gstream.Map` sub-pipeline, composed by one top-level `BuildPipeline(ctx) PipelineIO` that `main` calls once and then only feeds/reads channels |