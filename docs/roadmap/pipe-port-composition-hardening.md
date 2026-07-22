# PipePort Composition Hardening — `ports`, `app`

> **Status:** Design draft — gaps identified against real code, fixes not yet decided.
> [← Back to Roadmap](index.md)

## Motivation

`PipePort`/`Chain`/`ChainStream`/`PipelineSpec` shipped against a purpose-built
3-stage toy example (`examples/pipeline-segmentation`). Before treating the
feature as fully hardened, it needs to survive contact with a real,
multi-boundary service — `examples/sensor-service`, go-codex's flagship
example (MQTT ingestion, SQL persistence, REST tool ports, file export, a
reactive cache, and hand-assembled OpenAPI/AsyncAPI spec generation from real
`RESTPattern`/`EventPattern`/`SQLPattern` declarations).

Reworking sensor-service's stream pipeline (`pipeline.Build`, currently a
hand-assembled `gstream.Apply` → `IOPort.Connect` → `Tap` → `Tee` → `Filter` →
`FlatMapSlice` chain) to use named `PipePort` stages connected by
`Chain`/`ChainStream`, and its hand-typed `pipeline.Topology()` to use the new
`ports.PipelineSpec`, is the concrete proving ground this doc plans. Doing
that rework surfaced five real gaps — documented below with code-level
evidence — **before** any code changed. This doc captures them as a living
design document; Phase 2 (a future session) picks fixes and does the actual
rework.

## Scope decisions

| In scope (Phase 1 — this doc) | Out of scope (Phase 2 — future implementation) |
|---|---|
| Identify and document real gaps against actual `ports`/`app` code | Implementing any fix below |
| Propose concrete fix directions per gap, with trade-offs | Reworking `examples/sensor-service` itself |
| Flag open design decisions requiring a scope choice | Adding new `stats.Observer` extensions (until a direction is chosen) |
| Sketch API surfaces for discussion | Migrating `pipeline.Topology()` to `ports.PipelineSpec` |

## Identified gaps (verified against current code)

### Gap 1 — `PipePort`'s primary data path (`Push`/`Chain`/`ChainStream`) has zero observer visibility

Re-reading `PipePort.Connect` end to end: `obs.RecordSubscribe(p.name, ...)`
fires **only** inside the goroutine draining a bound `InputPort` `SourceAdapter`
(lines merging `srcs`). The `Push`-consumer goroutine — the path **every**
`Chain`/`ChainStream` edge uses, i.e. the feature's headline use case — calls
`fanOut(v)` directly with no observer call before it. `fanOut` itself never
calls `RecordPublish` for delivery to `OutputPort` adapters or `Stream`
consumers, regardless of which path fed it.

Confirmed independently at the `gstream.Drain` level: `chainWire`'s
`gstream.Drain(ctx, out, ..., nil, gstream.DrainOptions{})` only calls
`stats.ReportErrors` on an `onValue` error — there is no success-path hook.

**Consequence:** if sensor-service's `CountingObserver` (fanned out with
logging, injected once via `app.Options.Observer`) were relied on after a
PipePort rework, item counts through `Chain`/`ChainStream` stages would
silently not appear in the observer summary — while counts through
adapter-bound `InputPort`s would. This is an inconsistency a user would only
discover by noticing missing numbers, not by an error.

**Proposed fix direction:**
```go
// Push-consumer goroutine inside Connect:
case v, ok := <-pushCh:
    if !ok {
        return
    }
    obs.RecordSubscribe(p.name, true, 0) // NEW — same visibility as InputPort-sourced items
    fanOut(v)
```
```go
// fanOut, for egress visibility on both success and (buffer-full/ctx-done) failure:
fanOut := func(v T) {
    for _, oc := range allOut {
        select {
        case oc.ch <- v:
            obs.RecordPublish(p.name, true, 0) // NEW
        case <-ctx.Done():
            obs.RecordPublish(p.name, false, 0) // NEW
            return
        }
    }
}
```
Reuses the existing `stats.Observer` interface — no new extension needed.
Location string convention: `p.name` matches what `InputPort`/`OutputPort`
already report through, keeping one name per pipe in the observer summary.

### Gap 2 — no `TraceObserver` span around a Chain/ChainStream edge

`SourcePort.Bind`/`SinkPort.Bind` wrap adapter activation in a `"port.bind"`
span via `bindWithObserver` (starts a span if `obs` implements
`stats.TraceObserver`, ends it when the wrapped call returns). `Chain`/
`ChainStream` have no equivalent — confirmed above, `gstream.Drain` has no
span hook at all.

**Consequence:** distributed tracing across a multi-stage PipePort pipeline
has a blind spot exactly at the stage boundaries — the part a trace viewer
most wants to show.

**Proposed fix direction (open — see Open design decisions):** wrapping the
**edge setup** (the `Chain`/`ChainStream` call itself) in a `"pipe.chain"`
span is cheap and matches the `"port.bind"` precedent, but only traces the
one-time wiring, not each item's transit. Wrapping **every item's
transit** in a per-item span (inside `chainWire`'s Drain loop) gives
genuine per-item traces but adds a span per item on what may be a
high-throughput hot path — a real performance trade-off that needs a
decision, not a default.

### Gap 3 — `PipelineSpec` cannot see an IOPort/ToolPort/LatestPort hop hidden inside a `ChainStream` transform closure

> **Scope note (verified):** this gap applies **only** to the new, informal
> `ports.PipelineSpec` documentation YAML — never to the real OpenAPI/AsyncAPI
> contract specs. Confirmed by re-reading `buildDualCodecPatternHandles`
> (used by `NewIOPort`/`NewToolPort`): a `RESTPattern`/`ReqReplyPattern`
> declared on an `IOPort`/`ToolPort` builds its real `RouteHandle`/
> `ReqReplyRouteHandle` and calls `Register(builder)` **at port construction
> time** — before any pipeline wiring exists. `rest.Builder.OpenAPISpec()`,
> `events.Builder.AsyncAPISpec()`, and `reqreply.Builder.AsyncAPISpec()`
> (confirmed reqreply also renders a real AsyncAPI 3.0 document, same
> mechanism as `events`) all render from that builder's accumulated
> registrations — a pure function of what was `Register()`-ed, never of
> runtime dataflow. `Chain`/`ChainStream`/`PipePort` operate entirely on
> `gstream.Stream[T]` values and have zero knowledge of `Pattern`/`Builder`/
> `Handle`. Whether an `IOPort.Connect(ctx, src)` call sits directly in a
> hand-rolled pipeline or inside a `ChainStream` transform closure, the
> OpenAPI/AsyncAPI document produced is **byte-identical either way**. So:
> moving sensor-service's `HistoryTool`/`ExportTool` (RESTPattern-carrying
> `ToolPort`s) or a hypothetical ReqReplyPattern-carrying `IOPort` inside a
> `ChainStream` transform is completely safe for the formal specs — only the
> informal `PipelineSpec` YAML documentation is affected, as detailed below.

Sensor-service's real persistence step is
`deps.Persist.Connect(ctx, params)` — an `IOPort[InsertReadingParams, db.Reading]`
carrying a `SQLPattern{Table: "readings", Op: "insert_reading"}`. The natural
PipePort rework would move this call inside a `ChainStream` transform
(alongside the pure `Tap`/`Tee`/`Filter`/`FlatMapSlice` operators already
there). But `PipelineSpec` only derives what `ChainEdge.Func` gives it — the
transform's own Go function identity via reflection — it has **no way** to
see that the transform's *body* also makes a SQL hop through a named,
`SQLPattern`-carrying port.

**Consequence:** the auto-derived spec would silently omit the most
important IO boundary in the pipeline — exactly the "intermediate IO Port
boundary" this rework set out to prove works.

**Proposed fix direction — recommended: guidance over new API.**
Rather than teaching `PipelineSpec` to introspect closures (impossible in
general Go) or adding a caller-declared `IOHop` list to `Chain`/`ChainStream`
(more API surface, more places to keep in sync), the cleaner fix may be a
**design rule**: any stage whose "transform" is really an IO hop through an
`IOPort`/`ToolPort`/`LatestPort` should be modeled as **its own PipePort
stage** (a real `Chain`/`ChainStream` edge with a dedicated `PipePort` on
each side), never buried inside another stage's transform closure. This
already matches go-codex's existing convention — forge functions stay pure;
persistence happens through a port, the pipeline "sees only the port" (see
`pipeline.Deps.Persist`'s own doc comment). Under this rule, `PipelineSpec`
sees the SQL hop naturally, as a real edge between two named pipes — no new
API needed, and the fix is a documentation/example change, not a `ports`
package change.

Two rejected alternatives, recorded for institutional memory:
- **Caller-declared `IOHop` list** passed as extra `Chain`/`ChainStream`
  args (`Kind`, `Name`, `Op` describing the hidden hop) — considered, but
  adds a second, hand-typed description surface for something already
  representable as a real edge; reintroduces the exact "manual, drift-prone"
  problem `PipelineSpec` was built to eliminate.
- **Context-based self-registration** — an `IOPort.Connect` call inside a
  "current pipe" marker in `ctx` self-reports into a shared registry.
  Rejected as too magic/implicit for go-codex's explicit-everywhere style,
  and thread-local-like context bookkeeping is exactly the kind of hidden
  global state the project avoids elsewhere.

This needs validating against sensor-service's real pipeline shape before
being finalized — see Open design decisions.

### Gap 4 — sensor-service's own spec generation is still 100% hand-typed

`pipeline.Topology(cfg, buildParams) *gstream.Topology` builds a
`stream.Topology` by hand: `WithSource("mqtt/sensors/+/data", "Raw MQTT payloads...")`,
`.WithPort("sql/readings/save", "persist via IOPort — stored row re-emitted (1→1)")`,
`.WithFilter(...)`, `.WithSink("mqtt/alerts/{sensorID}", ...)` — every name and
description is a string sitting next to the real code, exactly the drift
risk `ports.PipelineSpec` was built to eliminate in
`examples/pipeline-segmentation`. Nobody has migrated it.

**Consequence:** go-codex's own flagship example demonstrates the *stale*
pattern this session already replaced elsewhere — visible drift risk in the
project's most-referenced example.

**Proposed fix:** once Gap 3's design rule is applied (persistence becomes a
real PipePort→PipePort edge), replace `pipeline.Topology()` with a
`ports.PipelineSpec(...)` call over the reworked pipes — the natural,
concrete proof that `PipelineSpec` scales past the 3-stage toy example.

### Gap 5 — `PipePort.Connect` is fire-and-forget; `app.App.Go` expects a blocking function

`PipePort.Connect(ctx)` spawns its internal goroutines and returns
immediately — it never blocks until `ctx` is done, and there is no
`Done()`/`Wait()` method exposing when those goroutines have fully drained.
`app.App.Go(name, fn)`'s documented contract: *"fn should return when its ctx
is done"* — `Go` records `RecordRequest("app.go", name, 200, duration)` the
instant `fn` returns, treating that as "goroutine finished."

**Consequence:** calling `a.Go("pipeline", func(ctx) error { p.Connect(ctx); return nil })`
today reports the pipeline as "finished" the instant `Connect` returns —
essentially immediately — not when its goroutines have actually drained
after `ctx` cancellation. A service that needs to know "has this pipeline's
in-flight work fully stopped" (e.g. before closing a downstream connection
in a later shutdown hook) has no signal to wait on.

**Proposed fix directions (open — see Open design decisions):**
- **(a) Add `PipePort.Done() <-chan struct{}`** — closed when `Connect`'s
  internal `wg.Wait()` (already present, used today only to sequence
  channel closes) completes. Small, `ports`-only change.
- **(b) A generic `app` helper**, not PipePort-specific, for "start
  something non-blocking, then supervise its actual completion":
  ```go
  // app package — proposed, generic over any non-blocking-start component
  func (a *App) Supervise(name string, start func(ctx context.Context) (done <-chan struct{})) {
      done := start(a.Context())
      a.Go(name, func(ctx context.Context) error {
          <-done
          return nil
      })
  }
  ```
  paired with (a), used as:
  ```go
  a.Supervise("sensor-pipeline", func(ctx context.Context) <-chan struct{} {
      p.Connect(ctx)
      return p.Done()
  })
  ```
- **(c) Guidance-only, no code change:** document the hand-rolled
  equivalent of (b) inline in the sensor-service rework and stop there,
  deferring any `app`/`ports` API change until a second real use case
  demands it (YAGNI-leaning).

### Gap 6 — `PipePort.InputPort`/`OutputPort` never forward `Patterns`, so Pattern-requiring protocol adapters can't be bound through them

Found while verifying Gap 3/the OpenAPI-AsyncAPI question above. Re-reading
`InputPort`/`OutputPort` exactly:

```go
func (p *PipePort[T]) InputPort(name string) *SourcePort[T] {
    ...
    sp, _ := NewSourcePort[T](p.name+"/in/"+name, p.codec,
        PortOptions{Buffer: p.buffer, Params: p.params, Observer: p.obs})
    ...
}
```

`Patterns` is never forwarded — confirmed identical for `OutputPort`. Every
`InputPort`/`OutputPort`-constructed `SourcePort`/`SinkPort` therefore has
**zero** declared `Pattern`s, permanently. `ports.RESTHandle`/`EventHandle`/
`ReqReplyHandle` on such a port always return `(nil, false)`.

**Consequence:** the PipePort docs' own "secondary use: IO/adapter bridging"
example —
```go
ingest := Broadcast.InputPort("from-mqtt")
ingest.Bind(ctx, mqtt5.SubscribeAdapter(...))
```
— only works today for adapters that need **no** Pattern-derived handle
(`ChanSourceAdapter`/`ChanSinkAdapter`, hand-rolled test/glue adapters). Any
**real** protocol adapter needing a Pattern-derived handle —
`mqtt5.SubscribeAdapter` (needs `ports.EventHandle`, exactly like
`ioports.Sensors` in sensor-service today), `nethttp.SSEAdapter`/
`IngestAdapter` (needs `ports.RESTHandle`), `zeromq`/`file`/`redis`/
`websocket` equivalents — **cannot** be bound through `InputPort`/
`OutputPort` without constructing the handle separately and out-of-band,
which defeats the "declare once" convenience this exact PipePort doc section
promises. This is unrelated to Chain/ChainStream or spec generation
directly, but it means PipePort's secondary use case is currently narrower
than its own documentation implies.

**Proposed fix direction (open — see Open design decisions):** give
`InputPort`/`OutputPort` a way to declare `Patterns` per named sub-port —
e.g. an options parameter:
```go
// Proposed — exact shape TBD:
func (p *PipePort[T]) InputPort(name string, opts ...PortOpt) *SourcePort[T]
```
or a narrower, additive overload (`InputPortWithPatterns(name string, patterns []Pattern) *SourcePort[T]`)
to avoid widening `InputPort`'s existing zero-arg signature for every
caller. Needs a decision on which shape fits `ports`' existing
`PortOptions`-based construction convention best.

## API surface (draft — pending Open design decisions)

```go
// Gap 1/2 fix — no new exported symbols, existing stats.Observer calls added
// inside ports/pipe_port.go's Connect/fanOut.

// Gap 5(a) — new PipePort method:
func (p *PipePort[T]) Done() <-chan struct{}

// Gap 5(b) — new App method:
func (a *App) Supervise(name string, start func(ctx context.Context) (done <-chan struct{}))

// Gap 6 — proposed, exact shape TBD (see Open design decisions):
func (p *PipePort[T]) InputPort(name string, opts ...PortOpt) *SourcePort[T]
func (p *PipePort[T]) OutputPort(name string, opts ...PortOpt) *SinkPort[T]
// or, narrower/additive:
func (p *PipePort[T]) InputPortWithPatterns(name string, patterns []Pattern) *SourcePort[T]
func (p *PipePort[T]) OutputPortWithPatterns(name string, patterns []Pattern) *SinkPort[T]
```

## Structured errors

None anticipated. Gap fixes are instrumentation/lifecycle additions, not new
fallible operations. Revisit if Gap 3's resolution ends up needing a
validation step (e.g. a rejected alternative resurfaces).

## Observer integration

Gap 1/2 ARE the observer integration work — see above. No new `stats`
extension: `RecordSubscribe`/`RecordPublish` (existing `stats.Observer`
methods) and an optional `TraceObserver` span, matching `bindWithObserver`'s
existing type-assertion-guarded pattern.

## Unit test plan (once a direction is chosen per gap)

| ID | Test | Verifies |
|---|---|---|
| PCH-01 | Push path calls RecordSubscribe | item pushed via `Push` → observer sees `RecordSubscribe(pipe name, true, ...)` |
| PCH-02 | fanOut calls RecordPublish per destination | item delivered to N outputs → N `RecordPublish` calls |
| PCH-03 | fanOut records failure on ctx-done mid-delivery | cancelled ctx during fan-out → `RecordPublish(..., false, ...)` |
| PCH-04 | TraceObserver span brackets Chain/ChainStream edge setup (if that direction is chosen) | `StartSpan`/`EndSpan` called once per `Chain`/`ChainStream` call |
| PCH-05 | `PipePort.Done()` closes only after Connect's goroutines fully exit (if Gap 5(a) chosen) | closing behavior matches internal `wg.Wait()` |
| PCH-06 | `App.Supervise` reports "finished" only after `done` closes, not after `start` returns (if Gap 5(b) chosen) | `RecordRequest("app.go", ..., duration)` reflects real drain time, not `start`'s return time |
| PCH-07 | Sensor-service rework: `PipelineSpec` derived spec includes the SQL persistence edge as a real PipePort→PipePort hop | rendered spec contains a port step for the persistence pipe with real `SQLPattern`-informed description |
| PCH-08 | Sensor-service rework: OpenAPI/AsyncAPI specs unchanged after PipePort rework | `ioports.RESTBuilder.OpenAPISpec()`/`ioports.EventsBuilder.AsyncAPISpec()` byte-identical (or semantically identical) before/after the rework |
| PCH-09 | `InputPort`/`OutputPort` with Patterns builds a real handle (if Gap 6 fix chosen) | `ports.EventHandle`/`ports.RESTHandle`/`ports.ReqReplyHandle` on the returned sub-port returns `(handle, true)`, not `(nil, false)` |
| PCH-10 | A Pattern-requiring adapter binds successfully through `InputPort`/`OutputPort` (if Gap 6 fix chosen) | e.g. a fake `mqtt5`-shaped adapter requiring an `EventHandle` binds and activates without an out-of-band handle construction |

## Files likely touched (Phase 2 — not yet decided which subset)

| File | Responsibility |
|---|---|
| `ports/pipe_port.go` | Gap 1/2 observer/tracing instrumentation; Gap 5(a) `Done()` method, if chosen; Gap 6 `InputPort`/`OutputPort` Patterns forwarding, if chosen |
| `app/app.go` | Gap 5(b) `Supervise` helper, if chosen |
| `examples/sensor-service/pipeline/pipeline.go` | Rework `Build`/`Topology` to use `PipePort`/`Chain`/`ChainStream`/`ports.PipelineSpec`, applying Gap 3's "real edge, not hidden hop" rule to the persistence step |
| `examples/sensor-service/main.go` | Wiring changes to match the reworked pipeline shape; possible `app.Supervise`/`Done()` usage |
| `examples/sensor-service/demo.go` | Update spec-printing section if `pipeline.Topology()` is replaced |
| `ports/pipe_port_test.go` | New tests per chosen direction (PCH-01 through PCH-06) |
| Three doc surfaces | `.github/instructions/go-codex.instructions.md`, `docs/features/ports.md` (+ `docs/features/app.md` if `app` changes), `docs/guides/ports.md` |

## Out of scope (Phase 2 and beyond)

- New `stats.Observer` extension (e.g. a dedicated `PipeObserver`) — only if
  Gap 1/2's fixes prove the existing `stats.Observer`/`TraceObserver`
  insufficient in practice.
- Per-item tracing spans by default — expensive; would need an opt-in flag,
  not a default-on behavior.
- Generalizing `App.Supervise` beyond PipePort to other non-blocking-start
  library components (worth revisiting only once a second concrete need
  appears).

## Open design decisions

1. **Gap 1/2 scope**: implement both success (`RecordPublish`) and failure
   instrumentation in the same pass, or ship success-path visibility first
   and revisit failure-path separately? Leaning: same pass — both are small,
   and shipping only success-path visibility would itself become a
   "silent gap" of the kind this doc is trying to eliminate.
2. **Gap 2 tracing granularity**: edge-setup span only, vs. per-item span
   (opt-in via an option), vs. no tracing until a real user need appears.
   Leaning: edge-setup span only for Phase 2, matching `"port.bind"`'s
   existing cost/benefit balance; revisit per-item tracing only if a user
   asks for it.
3. **Gap 3 resolution**: adopt the "real edge, not hidden hop" design rule
   as guidance (no new API), or add a caller-declared `IOHop` descriptor
   API. Leaning: guidance first — validate it actually works cleanly against
   sensor-service's real shape before considering new API; only add
   `IOHop`-style API if the guidance proves insufficient in practice (e.g.
   if sensor-service's persistence step genuinely cannot be expressed as a
   standalone PipePort edge without awkward restructuring).
4. **Gap 5 fix**: `PipePort.Done()` + `App.Supervise` (both), `Done()` alone
   (caller hand-rolls the wait), or guidance-only with no code change.
   Leaning: implement both — the friction is real and the fix is small and
   consistent with `App`'s existing minimal-lifecycle-manager scope; a
   guidance-only fix would leave every future PipePort+App user rediscovering
   the same gap.
5. **Sensor-service rework extent**: rework only the MQTT ingestion →
   persistence → alert stream pipeline (the part `pipeline.Build` already
   isolates), or also touch the History/Export tool pipelines? Leaning:
   MQTT pipeline only for Phase 2 — it is the part with real multi-stage
   PipePort structure; History/Export are already simple 1-hop IOPort
   Connects that don't need PipePort segmentation.
6. **Gap 6 API shape**: widen `InputPort`/`OutputPort` to accept optional
   `PortOpt`s (touches every existing call site's signature, though
   backward-compatible via variadic), or add narrower additive
   `InputPortWithPatterns`/`OutputPortWithPatterns` overloads (new names,
   zero risk to existing call sites, but two ways to call `InputPort`).
   Leaning: additive overloads first — matches go-codex's general preference
   for non-breaking additive API growth over widening existing signatures;
   revisit consolidation only if the two-name split proves confusing in
   practice.
