# Ports & Pipeline Ergonomics — `ports`, `forge`

> **Status:** Design complete — all open design decisions resolved. Not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

The `PipePort`/`Chain`/`ChainStream`/`PipelineSpec` feature line (shipped and
hardened across several prior sessions — see the now-deleted
`pipe-port-composition-hardening.md` history) proved the declarative
ports/pipeline model is fully CAPABLE: computation-stage segmentation,
IO-bridging, observer/tracing instrumentation, lifecycle supervision, and
auto-derived spec generation all work and are tested. But capability isn't
the same as ergonomics — `examples/sensor-service`, go-codex's own flagship
example, is the concrete evidence that the DECLARATIVE workflow has grown
complex and verbose for the person actually using it. This doc gathers
concrete evidence (not impressions) and proposes a simplification direction.
Breaking changes are explicitly in scope — go-codex currently has a single
consumer (this project's own examples), so there is no external compatibility
constraint holding back a cleaner design.

### Evidence — real code, not speculation

**A. The handle-derivation "ok-check dance" repeats 6 times verbatim in
`examples/sensor-service/main.go` alone** (lines 168, 188, 260, 309, 343, 369
as of this writing):

```go
readingHandle, ok := ports.EventHandle[domain.MQTTPayload](sensorsSrc)
if !ok {
    must(errors.New("ioports.Raw: no EventPattern declared on sensors sub-port"), "derive reading channel handle")
}
```

Four lines of ceremony per port, every time, purely to convert a
`(handle, bool)` return into "panic if the pattern was never declared" — a
condition that, if it ever triggers, is ALWAYS a programming error (a
`Pattern` was omitted or the wrong port was passed), never a runtime
condition a caller should handle differently case-by-case. This exact
pattern does not appear in simpler examples that skip the `ports.Pattern`
system and call `api/events`/`api/rest` builders directly instead
(`examples/adapters-mqtt`, `examples/event-driven`) — meaning the
ok-check dance is a cost specifically introduced by the ports/Pattern
convenience layer, without a matching convenience for the failure path.

**B. `PipePort`'s IO-bridging overload is measurably MORE verbose than
plain `SourcePort`/`SinkPort` for the identical MQTT use case.** Confirmed
via `git diff` between the commit before this session's PipePort rework
and after:

```go
// BEFORE — plain SourcePort, one call:
ioports.Sensors.Bind(ctx, adaptermqtt.SubscribeAdapter(...))
sensors := ioports.Sensors.Stream(ctx)

// AFTER — PipePort.InputPortWithPatterns, an extra FALLIBLE call + err-check:
sensorsSrc, err := ioports.Raw.InputPortWithPatterns("mqtt", ioports.SensorsPattern)
must(err, "derive sensors sub-port")
sensorsSrc.Bind(ctx, adaptermqtt.SubscribeAdapter(...))
```

This directly confirms the user-observed instinct: using `PipePort` as an
IO-bridging boundary is a NET ERGONOMICS LOSS versus a plain `SourcePort`/
`SinkPort` doing the identical job — an extra fallible call, an extra error
check, and a sub-port name (`"mqtt"`) to invent, for zero behavioral gain
over the plain port. The `PipePort` ordering rule (`OutputPort`
registrations must precede that pipe's own `Connect`) also forced
`main.go`'s alert-binding code to move to an earlier, disconnected location
in the file — real sequencing overhead the plain-port version never had.

> **Important refinement (user feedback after the first draft):** the
> ANNOYING part of `InputPortWithPatterns` was never the two-step SHAPE
> (declare the `Pattern` standalone, plug it into a port at a later,
> different point in the code) — it was the extra sub-port, the invented
> name (`"mqtt"`), and a fallible call for a case that didn't need a NEW
> way to fail. The two-step shape ITSELF, as embodied by
> `ioports.SensorsPattern`/`ioports.AlertsPattern` (standalone
> package-level `Pattern` values, declared once in `ioports.go`,
> independent of any specific port-construction call), was specifically
> called out as something genuinely liked during the rework — worth
> preserving and GENERALIZING, not discarding along with
> `InputPortWithPatterns`. See "The Pattern-plugin model" below — this is
> now the central new proposal of this document. It fully REPLACES (not
> supplements) both `PortOptions.Patterns` and the `MustXxxHandle`/
> `EventHandle`-lookup idea from the first draft — see Open Design
> Decision 6, resolved after a second round of user feedback: no legacy
> mechanism is kept alongside it.

**C. `forge.Function` wrapping costs 9 lines of ceremony for 1 line of
actual logic, and is not even required by `Chain`/`ChainStream`:**

```go
func NewBuildInsertParams() *forge.Function[domain.MQTTPayload, db.InsertReadingParams] {
    return forge.NewFunction(
        "buildInsertParams", "1.0.0",
        domain.MQTTPayloadCodec, domain.InsertParamsCodec,
        func(payload domain.MQTTPayload) (db.InsertReadingParams, error) {
            return domain.BuildInsertParamsFromMQTT(payload), nil
        },
        forge.FunctionMeta{Description: "Map MQTT sensor payload to SQL insert params (assigns ID + RecordedAt)."},
    )
}
// used as: ports.Chain(ctx, raw, buildParams.Apply, Params)
```

`ports.Chain`'s `fn` parameter is `func(In) (Out, error)` — a plain Go
function type. `forge.Function` is NOT required. Worse: since
`ports.PipelineSpec`'s `ChainEdge.Func` already captures real function
identity via reflection, wrapping in `forge.Function` makes the
auto-derived spec LESS readable — it shows `forge.(*Function[...]).Apply-fm`
(a bound-method name) instead of the plain function's own clear name (e.g.
`domain.BuildInsertParamsFromMQTT`). `forge.Function`'s contract-hash/
signing machinery is valuable for governed, published KPI calculations —
paying that cost (name, version, `FunctionMeta`, hash computation) for
every internal glue-mapping step inside a pipeline is disproportionate.

**D. Ordering rules add real sequencing cognitive load with no compiler
help.** `Chain`/`ChainStream`/`Stream`/`InputPort`/`OutputPort`
registrations must all precede that pipe's own `Connect`; a 4-stage
pipeline's `Connect` calls must be manually sequenced (or at least
manually verified) by the caller. The only runtime safety net is "a second
`Connect` call on the same pipe is a no-op, logged as a failed
`port.bind` event" — there is no way to ask "have I registered everything
I need to before calling Connect?" ahead of time.

## Scope decisions

| In scope (this round of design) | Out of scope |
|---|---|
| **The Pattern-plugin model — the ONE way, no legacy path kept**: `Pattern`s declared as standalone values (already the case for `ioports.SensorsPattern`-style code); ports declared with NO patterns baked in; `PortOptions.Patterns` REMOVED entirely (breaking); a new `PluginXxxPattern` method call at wiring time both registers the Pattern AND returns the typed handle in one step — applies uniformly across ALL SIX port types (`SourcePort`/`SinkPort`/`IOPort`/`ToolPort`/`LatestPort`/`DuplexPort`) | Removing the `Pattern`/`Handle` CONCEPT itself — Patterns stay the primary declaration surface, only the ATTACH TIMING changes |
| Scope `PipePort` back to computation-segmentation ONLY; **remove** `InputPortWithPatterns`/`OutputPortWithPatterns` (breaking change — confirmed acceptable) | Rewriting `api/rest`/`api/events`/`api/reqreply`/`api/mcp` |
| Generalize `Chain`/`ChainStream` so `from`/`to` can be EITHER a `PipePort` OR a boundary port (`SourcePort` as `from`, `SinkPort` as `to`) — making `SourcePort -> Chain -> PipePort -> ChainStream -> SinkPort` a single, visible, top-to-bottom declaration | Auto-inferring codecs/patterns from Go struct tags (a much larger, separate design) |
| Guidance (not new API): stop wrapping pipeline-internal pure maps in `forge.Function` when they don't need contract-hash governance — pass plain functions to `Chain`/`ChainStream` directly | Changing `forge.Function`'s core contract-hash/signing behavior, or its use for genuinely governed KPI calculations |
| Additional candidate (smaller, independent, additive — no breaking change): `gstream.LogOnError(logger, context)` shared default `OnError` helper, found while cross-checking the "one-struct-one-call" precedent | A full pipeline DSL/parser, reactive scheduling, or a visual pipeline editor |
| Explore (open decision, not yet committed): a deferred/auto `Connect`-ordering helper | — |

## API surface (draft — pending Open design decisions)

### 1. The Pattern-plugin model — declare `Pattern` standalone, plug in at wiring time

This is the CENTRAL new proposal of this document, directly generalizing
what already felt good about `ioports.SensorsPattern`/`ioports.AlertsPattern`
during the sensor-service rework — but applied to PLAIN boundary ports
(`SourcePort`/`SinkPort`/`IOPort`/`ToolPort`), not `PipePort`'s
now-removed IO-bridging overload.

Three DISTINCT, separately-timed declarations, matching the user's own
description ("define the pipelines and their boundaries... then
insert/plugin those SensorPattern objects... then binding a concrete
adapter to it when it comes to wiring"):

```go
// ── 1. Port declared with NO patterns baked in — structural shape +
//       shared builder reference only. This can live in ioports.go,
//       right where it does today, unchanged in spirit:
var Sensors = codex.Must(ports.NewSourcePort[domain.MQTTPayload](
    "mqtt/sensors/+/data", domain.MQTTPayloadCodec,
    ports.PortOptions{Buffer: 64, EventBuilder: EventsBuilder}))

// ── 2. Pattern declared standalone — a reusable, self-contained,
//       independently documented value. This IS exactly
//       ioports.SensorsPattern's existing shape — nothing changes here:
var SensorsPattern = ports.EventPattern{
    Topic: "sensors/{sensorID}/data",
    Opts: []events.ChannelOpt{
        events.ChannelMeta{Description: "Sensor readings published by the sensor network."},
        events.Subscribe{Summary: "Receive sensor reading"},
        events.TopicParam{Name: "sensorID", Description: "UUID of the publishing sensor"},
    },
}

// ── 3. main.go, at wiring time — ONE call both plugs in the Pattern
//       (registers it against the port's already-stored EventBuilder)
//       AND returns the typed handle directly:
readingHandle := codex.Must(Sensors.PluginEventPattern(SensorsPattern))
Sensors.Bind(ctx, adaptermqtt.SubscribeAdapter(mqttClient, readingHandle, 0,
    format.JSON(domain.MQTTPayloadCodec),
    adaptermqtt.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"}))
```

Sketch of the method itself:

```go
// PluginEventPattern registers pattern against the port's EventBuilder
// (supplied at construction via PortOptions.EventBuilder, or a private
// single-use builder if nil — same fallback NewSourcePort/NewSinkPort use
// today) and returns the resulting typed handle in ONE call — there is no
// later "look up the handle" step that can fail with "was a Pattern ever
// declared here?" ambiguity, because you just plugged it in, synchronously,
// in this same call.
//
// Calling PluginEventPattern twice on the same port is an error
// (PatternRegisterError — duplicate registration), matching how
// PortOptions.Patterns already behaves for multiple EventPatterns today.
func (p *SourcePort[T]) PluginEventPattern(pattern EventPattern) (*events.ChannelHandle[T], error)
func (p *SinkPort[T]) PluginEventPattern(pattern EventPattern) (*events.ChannelHandle[T], error)

// Mirrors for the remaining Pattern/Handle kinds, on the port types that
// can carry them:
func (p *SourcePort[T]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[T, struct{}], error)
func (p *SinkPort[T]) PluginRESTPattern(pattern RESTPattern) (*rest.SSERouteHandle[struct{}, T], error)
func (p *IOPort[Req, Resp]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[Req, Resp], error)
func (p *ToolPort[In, Out]) PluginRESTPattern(pattern RESTPattern) (*rest.RouteHandle[In, Out], error)
func (p *IOPort[Req, Resp]) PluginReqReplyPattern(pattern ReqReplyPattern) (*reqreply.RouteHandle[Req, Resp], error)
func (p *ToolPort[In, Out]) PluginMCPPattern(pattern MCPPattern) (*apimcp.ToolHandle[In, Out], error)
func (p *SinkPort[T]) PluginFilePattern(pattern FilePattern) (File[T], error)
```

This REPLACES the first draft's separate `MustXxxHandle` family idea
entirely (not kept as a fallback — see Open Design Decision 6) — since
`PluginEventPattern` returns the handle directly at the point of
plugging-in, the whole "derive a handle from a port that might or might not
have a Pattern" lookup family (`EventHandle[T](port) (handle, bool)`,
`RESTHandle[...]`, etc., and any `Must`-wrapper around it) is REMOVED.
Evidence A's 4-line ok-check dance disappears ENTIRELY — not shortened to
1 line via a `Must` wrapper, but removed as a separate step altogether.

`SQLPattern` fits the SAME `Plugin` call shape despite being metadata-only
(no Handle) — see the per-port-type table in Open Design Decision 7.

### 2. Generalized `Chain`/`ChainStream`

Two narrow interfaces capture exactly what `Chain`/`ChainStream` need from
`from` and `to`, satisfied naturally by the EXISTING port types — no new
methods needed on `SourcePort`/`SinkPort`, only a small addition to
`PipePort`:

```go
// chainSource is anything Chain/ChainStream can read a stream FROM.
// *SourcePort[T] and *PipePort[T] both already implement this (Stream
// already exists on both).
type chainSource[T any] interface {
    Name() string
    Stream(ctx context.Context) gstream.Stream[T]
}

// chainSink is anything Chain/ChainStream can drain a transformed stream
// INTO. *SinkPort[T] already implements this (Feed already exists).
// *PipePort[T] needs a NEW Feed method (thin wrapper draining src into
// per-item Push calls) to satisfy this — symmetric with SourcePort's
// Stream-only, inbound-only shape and SinkPort's Feed-only,
// outbound-only shape.
type chainSink[T any] interface {
    Name() string
    Feed(ctx context.Context, src gstream.Stream[T])
}

func ChainStream[In, Out any](
    ctx context.Context,
    from chainSource[In],
    transform func(gstream.Stream[In]) gstream.Stream[Out],
    to chainSink[Out],
)

func Chain[In, Out any](ctx context.Context, from chainSource[In], fn func(In) (Out, error), to chainSink[Out])
```

This makes the ENTIRE sensor-service MQTT pipeline visible as one
top-to-bottom sequence of `Chain`/`ChainStream` calls, with boundary ports
at each end instead of PipePort sub-ports:

```go
var Params = codex.Must(ports.NewPipePort[db.InsertReadingParams]("params", ...))
var Saved  = codex.Must(ports.NewPipePort[db.Reading]("saved", ...))

// main.go, after ioports.Sensors/ioports.Alerts are bound to mqtt adapters
// exactly like every OTHER boundary port in the service (no InputPortWithPatterns
// detour):
ports.Chain(ctx, ioports.Sensors, buildInsertParams, Params)
ports.ChainStream(ctx, Params, persistTransform, Saved)
ports.ChainStream(ctx, Saved, alertTransform, ioports.Alerts)
```

`PipelineSpec`'s `PipeSpecSource` interface already tolerates
heterogeneous pipe types (see `pipeline_spec.go`'s doc comment).

> **✅ Resolved (user confirmed) — Decision 3.** Rather than a SEPARATE
> `PipelineSpec` overload/variant for boundary ports, `PipeSpecSource` is
> WIDENED to its minimal required shape, and the PipePort-specific detail
> becomes OPTIONAL via type-assertion — the SAME convention already used
> for `stats.Observer` extensions (`TraceObserver`/`FileObserver`/etc.)
> elsewhere in this codebase:
>
> ```go
> // PipeSpecSource is now minimal — only Name() is required. *SourcePort[T]
> // and *SinkPort[T] already implement this (Name already exists on both).
> type PipeSpecSource interface {
>     Name() string
> }
>
> // PipelineSpec type-asserts for OPTIONAL extra detail, exactly like an
> // Observer extension — present on *PipePort[T] (Buffer/InputAdapters/
> // OutputAdapters/OutEdges) and *SourcePort[T]/*SinkPort[T]
> // (BoundAdapters, which they ALREADY implement today):
> type pipeSpecBuffered interface { Buffer() int }
> type pipeSpecInputAdapters interface { InputAdapters() map[string][]string }
> type pipeSpecOutputAdapters interface { OutputAdapters() map[string][]string }
> type pipeSpecEdges interface { OutEdges() []ChainEdge }
> type pipeSpecBoundAdapters interface { BoundAdapters() []string } // SourcePort/SinkPort
> ```
>
> A `SourcePort`/`SinkPort` at a pipeline's edge gets a real, if reduced,
> spec line automatically — same `ports.PipelineSpec(title, version,
> ioports.Sensors, Params, Saved, ioports.Alerts)` call as before, no new
> function name, no separate code path. This is a strictly ADDITIVE change
> to `PipelineSpec`'s existing type-assertion pattern (it already
> distinguishes pipe capabilities this way internally) — no breaking
> change to `PipelineSpec` itself, only a narrowing of what
> `PipeSpecSource` REQUIRES.

### 3. `PipePort` scope-back

`InputPortWithPatterns`/`OutputPortWithPatterns` are REMOVED (breaking).
Plain `InputPort`/`OutputPort` (no Patterns) are KEPT — they remain useful
for a genuinely different case: a side-observer tap that needs no handle
at all (`Raw.OutputPort("log").Bind(ctx, ports.ChanSinkAdapter(logCh))`,
already shown in `PipePort`'s own doc comment) — this is NOT an IO
boundary in the Pattern/spec sense, just an internal debugging/observation
hook, and stays cheap and Pattern-free on purpose.

## Structured errors

`PluginXxxPattern` methods reuse the EXISTING `PatternRegisterError`
(already returned by `NewSourcePort`/`NewSinkPort`/etc. for an invalid
Pattern, and already implementing `Error()`/`Unwrap()`/`LogValue()`) — no
new error type needed for the plugin path itself, since plugging in a
Pattern late is the SAME registration codepath as plugging one in at
construction time via `PortOptions.Patterns`, just called at a different
moment. `Plugin` additionally returns `PatternRegisterError` for a
DUPLICATE plugin (calling `PluginEventPattern` twice on the same port) —
a new, clearly-named case within the existing error type (add a `Reason`
or reuse its existing `Err` field to wrap a plain "already plugged in"
sentinel — exact shape TBD during implementation).

No `HandleNotDeclaredError`/`MustXxxHandle` needed at all — with
`PortOptions.Patterns` fully replaced (Decision 6), there is no port state
where "a handle was expected but no Pattern was ever declared" can arise:
`Plugin` either succeeds and hands back the handle synchronously, or fails
loudly with `PatternRegisterError` at the exact point of plugging in. The
whole "was a Pattern declared on this port?" question this error type
existed to answer no longer has anywhere to be asked from.

## Observer integration

No new observer hooks needed. The generalized `Chain`/`ChainStream` keep their existing `RecordSubscribe`/
`RecordPublish`/`"pipe.chain"` `TraceObserver` span instrumentation
unchanged (Side quest 43); boundary ports (`SourcePort`/`SinkPort`) already
have their own `port.bind`/stream-drain observer calls independently.

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| PPE-01 | `PluginEventPattern`/`PluginRESTPattern`/etc. happy path on `SourcePort`/`SinkPort`/`IOPort`/`ToolPort` | registers against the port's construction-time builder, returns the real typed handle, no separate lookup needed |
| PPE-02 | `PluginXxxPattern` with an invalid Pattern (bad topic/path template, codec mismatch) | returns `PatternRegisterError`, `errors.As` reaches it |
| PPE-03 | `PluginXxxPattern` called TWICE on the same port | second call returns an error (duplicate plugin) — exact error shape per the resolved Structured Errors design |
| PPE-04 | `PluginXxxPattern` registers against the SAME shared builder every other Pattern-carrying declaration uses | the plugged-in route/channel appears in that builder's rendered `OpenAPISpec()`/`AsyncAPISpec()` — same verification style as this session's `TestPipePort_InputPortWithPatterns_UsesSharedRESTBuilder` |
| PPE-05 | `Chain` with a `SourcePort` `from` and a `PipePort` `to` | items flow source → pipe correctly, `ChainEdge` recorded with the source port's real name |
| PPE-06 | `ChainStream` with a `PipePort` `from` and a `SinkPort` `to` | items flow pipe → sink via `Feed`, no manual per-item Push loop needed |
| PPE-07 | `Chain`/`ChainStream` with `SourcePort` `from` AND `SinkPort` `to` directly (no PipePort in between) | trivial pass-through pipeline works, confirming the interfaces are genuinely general |
| PPE-08 | `PortOptions.Patterns`/`EventHandle`/`RESTHandle`/etc. lookup family removed | compile-time proof — no such symbols exist post-removal |
| PPE-09 | `PluginSQLPattern`/`PluginCachePattern` on `IOPort`/`SinkPort` | `SQLMetaFromContext` sees the plugged-in metadata; `Cache[T]` handle returned correctly |
| PPE-10 | `InputPortWithPatterns`/`OutputPortWithPatterns` removed | compile-time proof — no such symbols exist post-removal |
| PPE-11 | `examples/sensor-service` rewritten to the new shape (Pattern-plugin model + generalized Chain/ChainStream) | full demo scenario passes identically (readings saved, alert published, history/latest/export all correct) — same bar as Side quest 43's verification |
| PPE-12 | `ports.PipelineSpec` over a pipeline with boundary ports at each end | renders correctly, still shows real edges — confirms the boundary-port/PipePort mix doesn't break spec derivation |
| PPE-13 | `gstream.LogOnError(logger, context)` (additional candidate) | logs `StreamApplyError`/`StreamDecodeError` distinctly from generic errors, matching the sensor-service inline callback's behavior exactly |
| PPE-14 | `NewRestPort`/`NewReqReplyPort`/`NewMCPPort`/`NewSQLPort`/`NewRestToolPort`/`NewMCPToolPort` happy path | returns the same port+handle as manually calling `NewIOPort`/`NewToolPort` then the matching `PluginXxxPattern` — proves the constructors are pure sugar, not a separate mechanism |
| PPE-15 | A `ToolPort` with BOTH `PluginRESTPattern` AND `PluginMCPPattern` called on the same plain `NewToolPort` | both Patterns register correctly, both handles retrievable — proves the multi-Pattern case the named constructors deliberately don't cover |

## Files likely touched (Phase 2 — not yet decided which subset)

| File | Responsibility |
|---|---|
| `ports/io_param.go` | Remove `PortOptions.Patterns` field entirely |
| `ports/source_port.go` | Remove construction-time Pattern handling; add `PluginEventPattern`/`PluginRESTPattern`/`PluginFilePattern` methods |
| `ports/sink_port.go` | Remove construction-time Pattern handling; add `PluginEventPattern`/`PluginRESTPattern`/`PluginFilePattern`/`PluginCachePattern` methods |
| `ports/io_port.go` | Remove construction-time Pattern handling; add `PluginRESTPattern`/`PluginReqReplyPattern`/`PluginMCPPattern`/`PluginSQLPattern`/`PluginCachePattern` methods; add `NewRestPort`/`NewReqReplyPort`/`NewMCPPort`/`NewSQLPort` convenience constructors (thin wrappers: `NewIOPort` + one `Plugin` call, single-Pattern common case) |
| `ports/tool_port.go` | Remove construction-time Pattern handling; add `PluginRESTPattern`/`PluginMCPPattern` methods; add `NewRestToolPort`/`NewMCPToolPort` convenience constructors |
| `ports/latest_port.go` | Remove construction-time Pattern handling; add `PluginRESTPattern`/`PluginReqReplyPattern`/`PluginMCPPattern` methods; convenience constructors mirror `IOPort`'s if a common single-Pattern `LatestPort` declaration proves worth the ceremony (lower priority — `LatestPort` declarations in practice tend to have exactly one `RESTPattern`, already reasonably compact) |
| `ports/duplex_port.go` | Remove construction-time Pattern handling; add `PluginSocketPattern` method |
| `ports/pipe_port.go` | Remove `InputPortWithPatterns`/`OutputPortWithPatterns`; generalize `Chain`/`ChainStream` signatures to `chainSource[In]`/`chainSink[Out]`; add `PipePort.Feed` |
| `ports/handle.go` | Remove the now-unneeded `EventHandle`/`RESTHandle`/`ReqReplyHandle`/`MCPHandle`/`FileHandle`/`SSEHandle`/`CacheHandle`/`SocketHandle` lookup family (superseded by `Plugin*`'s direct handle return) — `buildEventPatternHandles`/`buildDualCodecPatternHandles`/`buildDuplexPatternHandles` internals are REUSED by the new `Plugin*` methods, just invoked later and per-Pattern instead of once for the whole `Patterns` slice |
| `ports/pipeline_spec.go` | Handle boundary ports (no `Buffer()`/`InputAdapters()`/etc.) appearing at the start/end of a `PipelineSpec` call — needs a resolved design (see Open Design Decisions) |
| `stream/` (new file, additional candidate) | `LogOnError(logger, context) func(error)` shared default `OnError` helper |
| `examples/sensor-service/*` | Rewrite `ioports.go` to declare ports with NO `Patterns` option + standalone Pattern vars (already the shape for `SensorsPattern`/`AlertsPattern` — extend to ALL boundary ports); `main.go` to call `PluginXxxPattern` at wiring time; adopt generalized `Chain`/`ChainStream` with boundary ports directly; drop `forge.Function` wrapping for `buildInsertParams`; adopt `gstream.LogOnError` for the alert-publish `OnError` |
| Every OTHER example using `PortOptions.Patterns` (~dozen, per grep) | Migrate to the three-step `Plugin` model — real, non-trivial migration scope |
| Four doc surfaces | `.github/instructions/go-codex.instructions.md`, `docs/features/ports.md`, `docs/guides/ports.md`, `examples/sensor-service/README.md` |

## Out of scope (this round)

- Auto-inferring `Pattern`s or codecs from Go struct tags/reflection.
- Rewriting `api/rest`/`api/events`/`api/reqreply`/`api/mcp` builder internals.
- A full pipeline DSL, visual editor, or reactive scheduling engine.
- Removing `forge.Function`'s contract-hash/signing machinery — it stays
  valuable for genuinely governed calculations; this doc only proposes NOT
  reaching for it by default for internal pipeline glue.

## Open design decisions

1. **✅ Resolved (user confirmed) — Generalized `Chain`/`ChainStream`
   typing: the interface approach.** `chainSource[T]`/`chainSink[T]`
   (small, unexported structural interfaces), a single generic `Chain`/
   `ChainStream` function per primitive (matching today's shape) rather
   than a combinatorial set of named overloads (`ChainFromSource`,
   `ChainToSink`, etc.). Keeps `SourcePort`/`SinkPort`/`PipePort` decoupled
   from a shared base type; the narrow, unexported interface surface makes
   the "accidentally satisfied by an unrelated type" risk negligible in
   practice (callers only ever pass existing port types).
2. **✅ Resolved (user confirmed) — plain `InputPort`/`OutputPort` (no
   Patterns) are KEPT.** They serve a genuinely different, still-useful,
   already-documented use case (a side-observer tap needing no handle at
   all, e.g. `Raw.OutputPort("log").Bind(ctx, ChanSinkAdapter(logCh))`) —
   unrelated to the IO-boundary complaint this doc addresses. Only
   `InputPortWithPatterns`/`OutputPortWithPatterns` are removed.
3. **✅ Resolved (user confirmed) — `PipelineSpec` boundary-port handling:
   option (a), widened `PipeSpecSource`.** `PipeSpecSource` narrows to its
   minimal required shape (`Name() string`); PipePort-specific detail
   (`Buffer`/`InputAdapters`/`OutputAdapters`/`OutEdges`) and boundary-port
   detail (`BoundAdapters`, already implemented by `SourcePort`/`SinkPort`)
   become OPTIONAL, type-asserted extras — the SAME pattern already used
   for `stats.Observer` extensions. Full sketch above in the API surface
   section. `SourcePort`/`SinkPort` at a pipeline's edge get a real,
   reduced spec line automatically, in the SAME `PipelineSpec` call — no
   separate overload/variant, no extra PipePort needed purely for
   spec-visibility.
4. **✅ Resolved (user confirmed) — Connect-ordering ergonomics: DEFERRED**
   to a separate follow-up. This round stays focused on the Pattern-attach
   model + generalized `Chain`/`ChainStream`; a `PipelineBuilder`-style
   auto-ordering helper is a distinct concern, revisited only once this
   round's simplifications ship and the ordering-rule friction is felt
   again in practice under the NEW shape.
5. **✅ Resolved (user confirmed) — `forge.Function` de-emphasis:
   guidance-only, no new API.** Update docs/examples to stop reaching for
   `forge.Function` by default for ungoverned pipeline glue; pass plain
   functions to `Chain`/`ChainStream` directly. No `forge.Pure(fn)` or
   similar lighter-weight alternative — avoids fragmenting `forge`'s
   single governance story. Revisit only if removing the wrapper from
   examples proves to lose something genuinely valued in practice.
6. **✅ Resolved (user feedback, second refinement round) — `PluginXxxPattern`
   FULLY REPLACES `PortOptions.Patterns`, no coexistence.** Explicit
   direction: "We really shouldn't introduce this while keeping other
   legacy ways, because we want to simplify things." `PortOptions.Patterns`
   is REMOVED (breaking, confirmed acceptable) from every port
   constructor's options struct. `PluginXxxPattern` becomes the ONE way to
   give ANY boundary port a communication pattern, always as an explicit,
   separately-timed step — one consistent declarative story (port shape →
   Pattern attached → adapter bound) everywhere, not two parallel
   mechanisms. This also means `MustXxxHandle`/`HandleNotDeclaredError`/the
   `EventHandle`/`RESTHandle`/etc. ok-check-returning lookup family are ALL
   REMOVED too (not kept as a fallback) — `PluginXxxPattern` returns the
   handle directly at the point of plugging in, so there is no later
   "does this port have a Pattern?" question left to ask. This is now the
   single biggest simplification in this document: it collapses THREE
   previously-separate mechanisms (construction-time `Patterns` field,
   `EventHandle`/etc. lookup, `Must`-wrapper) into ONE `Plugin` call per
   port. Real, non-trivial migration scope: every example currently using
   `PortOptions.Patterns` (not just sensor-service — grep confirms
   `ports.NewSourcePort`/`NewSinkPort`/`NewIOPort`/`NewToolPort`/
   `NewLatestPort`/`NewDuplexPort` with `Patterns:` in their options appear
   across roughly a dozen examples) needs rewriting to the three-step
   shape. Accepted as in-scope for implementation.

7. **✅ Resolved — the three-step model (declare port → plug in Pattern →
   bind adapter) applies uniformly across ALL SIX port types**, confirmed
   by reading `ports/handle.go`'s pattern-dispatch functions directly (all
   six constructors already route `opts.Patterns` through the SAME
   `buildEventPatternHandles`/`buildDualCodecPatternHandles`/
   `buildDuplexPatternHandles` switch statements — the mechanism is
   already unified internally, only the CALL TIMING changes):

   | Port type | Pattern kinds it accepts | `PluginXxxPattern` methods |
   |---|---|---|
   | `SourcePort[T]` | `EventPattern`, `RESTPattern` (HTTP ingest), `FilePattern` | `PluginEventPattern`, `PluginRESTPattern`, `PluginFilePattern` |
   | `SinkPort[T]` | `EventPattern`, `RESTPattern` (SSE), `FilePattern`, `CachePattern` | `PluginEventPattern`, `PluginRESTPattern`, `PluginFilePattern`, `PluginCachePattern` |
   | `IOPort[Req,Resp]` | `RESTPattern`, `ReqReplyPattern`, `MCPPattern`, `SQLPattern`, `CachePattern` | `PluginRESTPattern`, `PluginReqReplyPattern`, `PluginMCPPattern`, `PluginSQLPattern`, `PluginCachePattern` |
   | `ToolPort[In,Out]` | `RESTPattern`, `MCPPattern` (no `CachePattern` — rejected explicitly by `handle.go`: "a cache is not a tool surface") | `PluginRESTPattern`, `PluginMCPPattern` |
   | `LatestPort[T]` | `RESTPattern`, `ReqReplyPattern`, `MCPPattern` | `PluginRESTPattern`, `PluginReqReplyPattern`, `PluginMCPPattern` |
   | `DuplexPort[In,Out]` | `SocketPattern` only | `PluginSocketPattern` |
   | `PipePort[T]` | none (computation-only per this doc's scope-back) | — |

   `SQLPattern` fits the SAME `Plugin` call shape despite being
   metadata-only (no Handle): `PluginSQLPattern(pattern SQLPattern) error`
   — its Table/Op metadata becomes available via the EXISTING
   `SQLMetaFromContext`/`WithSQLMeta` mechanism unchanged, just populated
   at `Plugin` time instead of construction time. `CachePattern` similarly
   fits via `PluginCachePattern(pattern CachePattern) (Cache[T], error)`.

8. **✅ Resolved — the third step ("bind a concrete adapter WITH adapter
   options, e.g. QoS") needs NO new API.** This is already how EVERY
   adapter constructor in the codebase works today —
   `adaptermqtt.SubscribeAdapter(client, handle, qos, format, opts)`,
   `sqladapter.QueryEachAdapter(codec, fn, opts)`, etc. — each adapter
   constructor already takes its own `Options` struct (QoS, timeouts,
   retry policy, `OnError`, ...) as a normal Go value, separate from the
   Pattern/Handle concern entirely. The three-step model composes cleanly
   with this UNCHANGED: `Plugin` (Pattern → Handle) is step 2, adapter
   construction-with-options is step 3's INPUT, `Bind(ctx, adapter)` is
   step 3's call. No design gap here — confirmed by re-reading
   `adaptermqtt.SubscribeAdapter`'s existing signature.

9. **✅ Resolved (user confirmed) — Naming: `Plugin`.** `PluginEventPattern`,
   `PluginRESTPattern`, `PluginReqReplyPattern`, `PluginMCPPattern`,
   `PluginSQLPattern`, `PluginFilePattern`, `PluginCachePattern`,
   `PluginSocketPattern` — "we plug in this pattern," the user's own
   framing. Doesn't collide with the existing `With*` naming convention
   used for functional options elsewhere in the codebase
   (`events.WithTopicConstraints`, `rest.WithPathConstraints`).

10. **✅ Resolved (user confirmed, after a deeper design discussion) —
    `IOPort`/`ToolPort` get thin, protocol-named CONVENIENCE constructors,
    not new port TYPES.** The discussion that led here: the user first
    asked whether `ReqReplyPattern` has a `SourcePort`/`SinkPort`
    equivalent (answer: no — confirmed via `ports/handle.go`'s dispatch,
    `ReqReplyPattern` is exclusively `buildDualCodecPatternHandles`
    territory, used only by dual-codec ports `IOPort`/`ToolPort`/
    `LatestPort`, because request/reply always carries a REAL payload on
    BOTH sides — splitting it into one-directional `SourcePort`+`SinkPort`
    legs would expose the correlation-ID pairing to the caller, undoing
    the "one call" convenience `ReqReplyPattern` gives today). This
    surfaced an asymmetry: `RESTPattern` DOES have `SourcePort`/`SinkPort`
    forms — but they are DEGENERATE, one-directional uses of REST (HTTP
    ingest: `Route[T, struct{}]`, response always empty; SSE:
    `SSERoute[struct{}, T]`, request always empty) — confirmed via the
    exact `case RESTPattern:`/`roleSource`/`roleSink` branches in
    `handle.go`. `ReqReplyPattern`/REST-on-`IOPort` are the "both sides
    real" case, hence dual-codec-only.

    This led to the user proposing named, protocol-specific port TYPES
    (`RestPort`, `ReqReplyPort`, etc.) instead of the generic `IOPort` +
    `Plugin` model. Confirmed first that `IOPort` is genuinely
    backend-agnostic: `IOPort.Bind(ctx, a IOAdapter[Req,Resp])` takes a
    fully generic adapter interface, completely decoupled from Pattern
    kind — a SQL adapter, a REST call adapter, an MCP adapter, or a
    ReqReply-over-any-transport adapter can ALL bind to the identical
    `IOPort[Req,Resp]` declaration; the Pattern only shapes what
    metadata/handle the adapter CONSTRUCTOR needs, never `Bind`/`Connect`
    itself. Renaming `IOPort` to protocol-specific types would lose this
    genuine backend-agnosticism for no real gain.

    **Resolution — middle ground**: `IOPort`/`ToolPort` stay as the single,
    generic, backend-agnostic port types. Thin convenience CONSTRUCTORS
    wrap `NewIOPort`/`NewToolPort` + an immediate `PluginXxxPattern` call
    into one function, for the common case where exactly one Pattern of
    one kind is known upfront — full consistency across every Pattern kind
    `IOPort`/`ToolPort` can carry (not just REST/ReqReply):

    ```go
    // ports/io_port.go — convenience constructors, all thin wrappers:
    func NewRestPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
        pattern RESTPattern, opts PortOptions) (*IOPort[Req, Resp], *rest.RouteHandle[Req, Resp], error)
    func NewReqReplyPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
        pattern ReqReplyPattern, opts PortOptions) (*IOPort[Req, Resp], *reqreply.RouteHandle[Req, Resp], error)
    func NewMCPPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
        pattern MCPPattern, opts PortOptions) (*IOPort[Req, Resp], *apimcp.ToolHandle[Req, Resp], error)
    func NewSQLPort[Req, Resp any](name string, reqCodec codex.Codec[Req], respCodec codex.Codec[Resp],
        pattern SQLPattern, opts PortOptions) (*IOPort[Req, Resp], error) // metadata-only, no handle

    // ports/tool_port.go — same idea, ToolPort's Pattern kinds (REST, MCP):
    func NewRestToolPort[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out],
        pattern RESTPattern, opts PortOptions) (*ToolPort[In, Out], *rest.RouteHandle[In, Out], error)
    func NewMCPToolPort[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out],
        pattern MCPPattern, opts PortOptions) (*ToolPort[In, Out], *apimcp.ToolHandle[In, Out], error)
    ```

    Each constructor's body is literally `p, err := NewIOPort(...); if err
    != nil { return nil, nil, err }; h, err := p.PluginRESTPattern(pattern);
    return p, h, err` — pure sugar, ALWAYS expressible by unwrapping into
    plain `NewIOPort` + a separate `PluginXxxPattern` call. This is not a
    parallel/legacy mechanism (satisfying the earlier "no legacy path"
    resolution): there remains exactly ONE underlying mechanism
    (`IOPort`/`ToolPort` + `Plugin`), and these constructors are named,
    ergonomic entry points into that SAME mechanism for the single-Pattern
    common case. The plain `NewIOPort`/`NewToolPort` + explicit
    `PluginXxxPattern` calls stay available (and NECESSARY) for: late
    Pattern binding (the Pattern declared as a standalone value elsewhere,
    plugged in only at wiring time — the ORIGINAL ergonomic this whole
    document set out to generalize), and multi-Pattern ports (e.g. a
    `ToolPort` exposed via BOTH `RESTPattern` AND `MCPPattern`
    simultaneously — two `Plugin` calls on one plain `NewToolPort`, which
    no single named convenience constructor could express in one call
    without a combinatorial explosion of two-Pattern-kind constructor
    names).

    `codex.Must` only unwraps a single `(T, error)` pair — these
    constructors return `(port, handle, error)`, a 3-tuple, so callers
    either handle the error explicitly (matching the existing `must(err,
    "...")` convention already used throughout every example) or a NEW
    `codex.Must2`-shaped helper is added for the common
    `(A, B, error) -> (A, B)` unwrap — flagged as an implementation-time
    detail, not a blocking design question.

## Cross-check against the "one-struct, one-call" precedent

The user specifically invoked the ALREADY-SHIPPED "one-struct-one-call"
principle (`docs/roadmap/vars-codec-merge.md`'s legacy, now shipped as
role-aware merge fields in `api/rest` — see plan.md history "Side quest
41/42") as the reference bar for a GOOD simplification: reduce ceremony to
one call for the common case, while keeping an explicit escape hatch for
special cases. Checking the `Plugin` proposal against that bar:

- **One call for the common case**: `handle := codex.Must(port.PluginEventPattern(pattern))`
  — one Pattern struct in, one typed handle out, one call. ✅ Matches.
- **Escape hatch preserved**: adapter-level `Options` structs (QoS,
  timeouts, `OnError`, retry policy) are UNTOUCHED — every adapter's own
  configuration surface stays exactly as expressive as it is today, this
  proposal only touches the Pattern-plugin step, not adapter
  construction. ✅ Matches.
- **No parallel/legacy mechanism kept**: `PortOptions.Patterns` is
  removed, not deprecated-but-present — per Decision 6 above. ✅ Matches
  the explicit ask.

### Additional simplification opportunity found while cross-checking: shared `OnError` boilerplate

While verifying the adapter-options escape hatch (Decision 8), a SEPARATE,
smaller "one-struct-one-call" opportunity was found: at least 9 adapter
packages accept an `OnError func(error)` callback in their `Options`
struct (`adapters/chi/stream.go`, `adapters/mqtt5/binding.go`,
`adapters/mqtt/binding.go`, `adapters/nethttp/stream.go`,
`adapters/redis/binding.go`, `adapters/websocket/binding.go`,
`adapters/zeromq/binding.go`, and their `stream.go` counterparts), and
`examples/sensor-service/main.go`'s alert-publish `OnError` is a real,
representative 10-line inline callback:

```go
OnError: func(err error) {
    var sae gstream.StreamApplyError
    var sde gstream.StreamDecodeError
    switch {
    case errors.As(err, &sae):
        logger.Warn("stream apply error", "error", sae)
    case errors.As(err, &sde):
        logger.Warn("stream decode error", "error", sde)
    default:
        logger.Warn("alert publish error", "error", err)
    }
},
```

No shared helper for "log a stream error, distinguishing Apply/Decode
kinds" exists anywhere in `stream`/`ports` today (confirmed via grep — zero
hits for `DefaultOnError`/`LogStreamError`-shaped names). A small,
optional helper —

```go
// stream.LogOnError returns an OnError callback that logs err at logger,
// distinguishing StreamApplyError/StreamDecodeError from other errors —
// the common case every adapter's OnError ends up hand-rolling. context
// is a short label (e.g. "alert publish") included in the log message.
func LogOnError(logger *slog.Logger, context string) func(error)
```

— would collapse the 10-line inline callback to one line
(`OnError: gstream.LogOnError(logger, "alert publish")`) at every one of
the ~9 call sites that want this common behavior, while leaving the
`OnError func(error)` field itself as the escape hatch for callers who
need custom handling. This is a smaller, separate, additive proposal (no
breaking change — `OnError` stays a plain function field) — flagged here
as an ADDITIONAL candidate for this round's scope, not a hard requirement,
since it's independent of the Pattern-attach/Chain-generalization work.
