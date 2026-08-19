# Forge Pipelines

> See also: [`forge` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/forge) · [`render/pipeline` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/pipeline)
>
> Runnable demos: [`examples/forge-oee`](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-oee) · [`examples/forge-collection`](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-collection) · [`examples/oee-chain`](https://github.com/DaniDeer/go-codex/tree/main/examples/oee-chain)

`forge` is the third layer of go-codex. It adds **named, versioned, and governance-tracked computation** on top of the validated domain types from Layer 1 and the event/REST channels from Layer 2.

## Programming model: inside-out development

go-codex is designed for **inside-out development**: you start from the domain and
work outward to the application boundary, not the other way around.

```
Step 1 — Domain core (inside, zero adapter imports)
    codex.Codec[T]                       ← validated domain types
    forge.NewFunction[In, Out](...)      ← governed pure computation
    ports.NewSourcePort / SinkPort /     ← protocol-agnostic IO enforcement points
        IOPort / ToolPort

Step 2 — Application boundary (outside, main.go only)
    domain.SomePort.Bind(ctx, transport.SomeAdapter(...))  ← wire to a concrete transport
```

**Why this order matters:**

The domain core is **transport-independent**. Ports are declared with a codec and a
name — no transport import required. The same pipeline can be bound to MQTT messages,
HTTP requests, ZeroMQ frames, or a plain Go channel in a test — without changing a
single line of domain logic. See [`ports` package documentation](../features/ports.md)
for the complete API.

```go
// domain/pipeline.go — zero adapter imports
var oeeCalcFn = forge.NewFunction("oeeCalc", "1.0.0", oeeInCodec, oeeCodec,
    func(in OEEIn) (OEE, error) {
        return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
    },
)

var SensorReadings = codex.Must(ports.NewSourcePort[OEEIn]("sensor-readings", oeeInCodec, ports.PortOptions{}))
var OEEResults = codex.Must(ports.NewSinkPort[OEE]("oee-results", oeeCodec, ports.PortOptions{}))

func StartPipeline(ctx context.Context) {
    sensors := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, sensors, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — all transport decisions here

// Wire A: MQTT source → domain pipeline → MQTT sink
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, fmt, opts))
domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, fmt, publishOpts))

// Wire B: HTTP trigger → domain pipeline → HTTP response (ToolPort, request/response)
var OEETool = codex.Must(ports.NewToolPort[OEEIn, OEE]("oee-tool", oeeInCodec, oeeCodec, ports.PortOptions{}))
OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEE] {
    return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
})
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))

// Wire C: test — plain Go channel, no transport at all
ch := make(chan OEEIn, 1)
ch <- OEEIn{Availability: 0.9, Performance: 0.85, Quality: 0.95}
close(ch)
domain.SensorReadings.Bind(ctx, ports.ChanSourceAdapter(ch))
out := make(chan OEE, 1)
domain.OEEResults.Bind(ctx, ports.ChanSinkAdapter(out))
```

**The application boundary** is where the domain meets the outside world. go-codex
ports define this boundary using **four positions**:

| Position | Declarative pattern | Direction |
|----------|-------------------|-----------|
| **Source** | `ports.NewSourcePort[T](...)` + `.Bind(ctx, transport.XxxAdapter(...))` | External world → domain pipeline (fan-in: multiple adapters merge) |
| **Sink** | `ports.NewSinkPort[T](...)` + `.Bind(ctx, transport.XxxAdapter(...))` | Domain pipeline → external world (fan-out: broadcast to all adapters) |
| **Intermediate I/O** | `ports.NewIOPort[Req,Resp](...)` + `.Bind(ctx, transport.XxxAdapter(...))` | Domain pipeline ↔ external service/store (exactly one adapter) |
| **Tool (request/response)** | `ports.NewToolPort[In,Out](...)` + `.SetPipeline(fn)` + `.Bind(ctx, transport.XxxAdapter(...))` | External request → pipeline → response; same pipeline can serve MCP + HTTP + ZeroMQ simultaneously |

Each port carries the payload codec (and optional `IOParam` routing parameters). Adapters
map the port to protocol-specific concerns (topic, path, security) at `Bind` time. The
adapter handles codec validation, error routing, and observer calls automatically.

**Development order in practice:**

```
1. Define domain types:        var oeeCalcFn = forge.NewFunction("oeeCalc", ...)
2. Test in isolation:          domain.SensorReadings.Bind(ctx, ports.ChanSourceAdapter(testCh))
3. Connect to triggers:        domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(...))
4. Connect to sinks:           domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(...))
5. Add intermediate I/O:       domain.Calibration.Bind(ctx, nethttp.CallAdapter(...))
6. Expose as request/response: domain.OEETool.Bind(ctx, mcpgo.ToolPipelineAdapter(...))
```

Steps 1–2 require no transport dependencies at all. Steps 3–6 plug the domain core
into the outside world declaratively, without changing the forge functions. See the
[ports feature guide](../features/ports.md) for the full adapter catalogue.

## Three-layer architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  LAYER 1 — codex: validated domain types                            │
│                                                                     │
│  PlannedTime, Downtime, Availability, OEE …                         │
│  codex.MapCodecSafe(float64 → PlannedTime)  ← wire-type bridging   │
│  codex.Struct[AvailabilityIn].RefineFunc    ← cross-field rules    │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 2 — api/events: transport contracts                          │
│                                                                     │
│  events.NewChannel[SensorReading](...).Register(b)                  │
│  b.AsyncAPISpec()                                                    │
├─────────────────────────────────────────────────────────────────────┤
│  LAYER 3 — forge: governed KPI computation                          │
│                                                                     │
│  forge.NewFunction("availabilityCalc", "1.0.0", …)                 │
│  forge.Registry → pipeline YAML spec + graph inference              │
│  stats.PipelineObserver → per-Apply telemetry                       │
└─────────────────────────────────────────────────────────────────────┘
```

## MapCodecSafe vs forge.Function

| Aspect | `codex.MapCodecSafe` | `forge.Function[In, Out]` |
|---|---|---|
| **Purpose** | Structural type mapping (wire bridging) | Named, governed domain computation |
| **Direction** | Bidirectional (encode + decode) | Unidirectional: `In → Out` only |
| **Identity** | None — anonymous | name + version + SHA-256 contract hash |
| **Governance** | None | `FunctionMeta{Author, ApprovedBy, …}` |
| **Spec output** | No | `Registry.Spec()` → pipeline YAML |
| **Telemetry** | None | `PipelineObserver.RecordApply` |
| **Error types** | codec errors | `InputError`, `OutputError`, `ApplyError`, `RefinementError` |

**Rule of thumb:**
- `codex.Map*` answers: "How do I represent `float64` as `PlannedTime`?" — structural, bidirectional, anonymous.
- `forge.Function` answers: "What named computation derives `Availability` from `AvailabilityIn`?" — business logic, unidirectional, governed.

## Why Function is a value, not a bare closure

A bare `func(In) (Out, error)` closure cannot participate in governance. The
`*forge.Function[In, Out]` value is a thin wrapper that adds **identity** to the closure:

| What the value carries | Why a closure can't provide it |
|------------------------|-------------------------------|
| `Spec.Name`, `Spec.Version` | Go closures have no name at runtime |
| `Spec.Hash` (SHA-256) | Go functions are not comparable; hashing a closure is impossible. The hash is computed from the **codec schemas** — the contract, not the bytecode. |
| `inputCodec` / `outputCodec` | Input/output schema for pipeline YAML, OpenAPI, AsyncAPI |
| `observer` (injected by `Register`) | Inversion of control — the Registry wires the observer, not the function |

The bare `func(In) (Out, error)` lives inside `Function.apply` — it IS a free function.
The `Function` value is the governance envelope around it.

**Composition is always via free functions.** The caller composes, the value carries identity:

```
Free function operators:    stream.Apply, stream.Filter, forge.Compose
        ↓ compose over ↓
Identified values:          *forge.Function[In, Out]
        ↓ registered in ↓
Registry:                   forge.NewRegistry(...).WithObserver(obs)
```

`stream.Apply(ctx, src, oeeCalcFn, opts)` — free function at the composition layer,
`*forge.Function[In, Out]` at the identity layer. Both are needed; neither alone is sufficient.

**Forge functions are pure domain computations.** They receive typed inputs, return typed
outputs and errors, and have no knowledge of observers, transports, or streams. The
`stream.Apply` operator and the `Registry` are where observability (PipelineObserver,
StreamObserver) attaches. Functions neither call nor require an observer in their body.

**Zero I/O inside forge functions.** A forge function body must not perform I/O:

```go
// ✅ Correct: pure transformation
func(in OEEIn) (OEE, error) {
    return OEE(float64(in.Availability) * float64(in.Performance) * float64(in.Quality)), nil
}

// ❌ Wrong: I/O inside forge function violates the design
// func(in InputData) (Out, error) {
//     cfg, _ := configFile.Read(...)  ← file I/O
//     resp, _ := nethttp.Call(...)    ← HTTP call
//     row, _ := db.Query(...)         ← database query
//     return combine(in, cfg, resp), nil
// }
```

If a forge function needs data from an external source (config, lookup table, enrichment
service), that data must arrive as a **typed input** in the input codec — loaded by the
stream layer (via `WatchStream`, `QueryStream`, `CombineLatest2`) before the function
is called:

```go
// ✅ Correct: external data flows IN as typed input
type EnrichInput struct {
    Sensor SensorReading   // from MQTT stream
    Config ThresholdConfig // from config file stream
}

enrichFn := forge.NewFunction("applyThresholds", "1.0.0",
    enrichInputCodec, alertCodec,
    func(in EnrichInput) (Alert, error) {
        // Pure: both inputs are already validated and available
        if float64(in.Sensor.Value) > in.Config.MaxValue {
            return Alert{Sensor: in.Sensor.ID, Exceeded: true}, nil
        }
        return Alert{}, nil
    },
)

// I/O stays in the stream layer — forge function receives the result:
configs := /* file.WatchStream + FlatMapSlice + ports.File.Read */
combined := stream.CombineLatest2(ctx, sensorStream, configs,
    func(s SensorReading, c ThresholdConfig) EnrichInput { return EnrichInput{s, c} })
alerts := stream.Apply(ctx, combined, enrichFn, stream.ApplyOptions{})
```

This separation is the design intent: **adapters and stream bridges handle I/O; forge
functions handle computation.** The stream layer wires them together.

## Defining a function

```go
import "github.com/DaniDeer/go-codex/forge"

// forge.NewFunction is infallible — panics only on empty name or version.
var availabilityCodec = codex.Float64().WithTitle("availability")

availabilityCalc := forge.NewFunction(
    "availabilityCalc", "1.0.0",
    availabilityInCodec,  // Codec[AvailabilityIn] — validates inputs
    availabilityCodec,    // Codec[Availability]   — validates output
    func(in AvailabilityIn) (Availability, error) {
        return Availability(
            (float64(in.PlannedTime) - float64(in.Downtime)) / float64(in.PlannedTime),
        ), nil
    },
    forge.FunctionMeta{
        Description: "Computes availability as (plannedTime - downtime) / plannedTime.",
        Author:      "oee-team",
    },
)

// Apply — input and output are codec-validated; errors are structured.
avail, err := availabilityCalc.Apply(AvailabilityIn{PlannedTime: 8.0, Downtime: 1.0})
var ie forge.InputError
if errors.As(err, &ie) {
    fmt.Printf("input failed: %v\n", ie.Err)
}
```

## Validation sequence

When `Apply` is called:
1. Input codec decodes and validates → `InputError` on failure
2. Optional cross-input refinement runs → `RefinementError` on failure
3. User function executes → `ApplyError` on failure
4. Output codec validates → `OutputError` on failure

## Multi-input functions (struct input codec)

```go
type AvailabilityIn struct {
    PlannedTime PlannedTime
    Downtime    Downtime
}

// Cross-field constraint: downtime cannot exceed planned time
var availabilityInCodec = codex.Struct[AvailabilityIn](
    codex.RequiredField("plannedTime", plannedTimeCodec, ...),
    codex.RequiredField("downtime", downtimeCodec, ...),
).RefineFunc(func(a AvailabilityIn) error {
    if float64(a.Downtime) > float64(a.PlannedTime) {
        return fmt.Errorf("downtime exceeds plannedTime")
    }
    return nil
})
```

## Governance metadata

```go
forge.NewFunction("calc", "1.0.0", inCodec, outCodec, fn,
    forge.FunctionMeta{
        Description: "Human-readable description",
        Author:      "team-name",
        ApprovedBy:  "reviewer",
        ApprovedAt:  "2024-03-01",
    },
)
```

## Composing functions

```go
// Compose chains f1: A→B and f2: B→Out into Function[A, Out].
// Type-safe: Out of f1 must match In of f2.
combined := forge.Compose("combined", "1.0.0", f1, f2,
    forge.FunctionMeta{Description: "chained pipeline"},
    forge.WithRefinement(func(a A) error { /* pre-compose constraint */ return nil }),
)
```

## Registry and pipeline spec

```go
reg := forge.NewRegistry("OEE Pipeline", "1.0.0").
    WithAuthor("engineering@example.com").
    WithApproval("quality-board", "2024-01-15").
    WithObserver(myObserver)

reg = availabilityCalc.Register(reg)
reg = performanceCalc.Register(reg)
reg = oeeCalc.Register(reg)

// Registry infers graph edges by matching input port names to output port names.
// Port names come from codec.Schema.Title (set via .WithTitle).
spec, err := pipeline.Render(reg.Spec())  // YAML pipeline document
fmt.Println(string(spec))
```

## PipelineObserver telemetry

```go
type myObserver struct{}

func (myObserver) RecordApply(name, version string, success bool, d time.Duration) {
    log.Printf("[forge] %s@%s ok=%v dur=%v", name, version, success, d)
}
```

## Structured errors

| Error type | When |
|---|---|
| `forge.InputError{Err}` | Input codec validation failed |
| `forge.RefinementError{Function, Err}` | Cross-input `RefineFunc` or `WithRefinement` failed |
| `forge.ApplyError{Function, Err}` | Compute function returned an error |
| `forge.OutputError{Err}` | Output codec validation failed |
| `forge.CollectionElementError{Index, Function, Err}` | Slice collection op failed at element |
| `forge.CollectionKeyError{Key, Function, Err}` | Map collection op failed at key |

## Collection operations

| Constructor | Signature | Kind in YAML |
|---|---|---|
| `forge.Map` | `Function[In,Out]` → `Function[[]In, []Out]` | `map` |
| `forge.Filter` | predicate `func(T) bool` → `Function[[]T, []T]` | `filter` |
| `forge.Reduce` | step `func(Acc,T) Acc` → `Function[[]T, Acc]` | `reduce` |
| `forge.MapValues` | `Function[In,Out]` → `Function[map[string]In, map[string]Out]` | `mapValues` |
| `forge.MapValuesK` | `Codec[K]` + `Function[In,Out]` → `Function[map[K]In, map[K]Out]` | `mapValues` |

All four return `*Function[_,_]` — composable with `Compose`, registerable in a `Registry`, and represented in pipeline YAML with `kind`/`wraps` fields:

```yaml
- name: mapToCelsius
  version: 1.0.0
  kind: map
  wraps: rawToCelsius
  hash: sha256:...
```

`forge.Map` and `forge.MapValues`/`forge.MapValuesK` wrap an existing `*Function` and delegate per-element `Apply`. `forge.Filter` and `forge.Reduce` accept raw predicates / step functions plus an explicit element codec.

```go
// Lift scalar function over slice
mapToCelsius := forge.Map("mapToCelsius", "1.0.0", rawToCelsius,
    forge.WithRefinement(func(readings []RawReading) error {
        if len(readings) == 0 {
            return fmt.Errorf("batch must contain at least one reading")
        }
        return nil
    }),
)

// Errors attributed to element or key
_, err := mapToCelsius.Apply(batch)
var ce forge.CollectionElementError
if errors.As(err, &ce) {
    fmt.Printf("element %d failed in %q: %v\n", ce.Index, ce.Function, ce.Err)
}
```

`MapValuesK` validates all keys atomically before processing any value — one bad key returns `InputError → KeyError → ConstraintError` immediately.

## Applying a patch: `forge.Patch`

`forge.Patch` extends the same "governed wrapper around an existing primitive" idea to [`codex.ApplyPatch`](codec.md#applying-a-patch-applypatch-flat-vs-applydottedpatch-dotted-path) — applying a `PartialStruct`-built patch onto a base value as a named, versioned, contract-hashed pipeline step:

```go
applyConfigPatch := forge.Patch("applyConfigPatch", "1.0.0",
    appConfigCodec, appConfigPatchCodec,
    forge.FunctionMeta{Description: "Applies a partial override onto the current config."},
)

updated, err := applyConfigPatch.Apply(forge.PatchInput[AppConfig, AppConfigPatch]{
    Base:  currentConfig,
    Patch: patch,
})
```

`FunctionSpec.Kind == FunctionKindPatch` (`"patch"`), and `Spec.Inputs`
carries TWO named ports (`"base"`, `"patch"`) — this falls out of
`NewFunction`'s existing `inputSpecs` machinery automatically, since
`Patch` builds its input codec as a `codex.Struct[PatchInput[T,P]]` with
one field per port. `Patch` delegates entirely to `NewFunction`, so
`InputError`/`OutputError`/`RefinementError` and `stats.PipelineObserver.
RecordApply` all work exactly as they do for any other forge Function —
no special-casing needed to register it in a `Registry` or see it in the
rendered pipeline YAML.

Reach for `forge.Patch` when the "apply a patch" step needs to
participate in a `Registry`'s pipeline spec/observer metrics alongside
other governed steps (e.g. an ingest pipeline that reads a raw patch off
a broker, applies it, then feeds the result into further governed
validation/alerting steps). A bare `codex.ApplyPatch` call is sufficient
when no such governance is needed.

**Applying a SEQUENCE of patches** already works today with zero new API
— compose `forge.Reduce` with `codex.ApplyPatch`:

```go
applyAll := forge.Reduce("applyAllPatches", "1.0.0",
    patchCodec, baseCodec, initialBase,
    func(acc AppConfig, p AppConfigPatch) AppConfig {
        updated, _ := codex.ApplyPatch(acc, baseCodec, p, patchCodec)
        return updated
    },
)
```

Caveat: `Reduce`'s `step` function has no error return today (a
pre-existing `Reduce` limitation, not introduced by this feature) — a
real caller should either validate each patch BEFORE folding, or use a
richer `Acc` type (e.g. wrapping a running error alongside the value) if
`ApplyPatch` might fail mid-sequence.

There is no dedicated `forge` builtin for the DOTTED-path case
(`codex.ApplyDottedPatchTo`) — wrap it in `forge.NewFunction` by hand if
a governed dotted-patch step is ever needed.

## See also

- [examples/forge-oee](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-oee) — OEE KPI computation, governance, Compose, MeasuredCodec
- [examples/forge-collection](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-collection) — Map, Filter, Reduce, MapValuesK on sensor batches
- [examples/forge-patch](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-patch) — forge.Patch applying a partial config override, registered in a Registry
- [examples/oee-chain](https://github.com/DaniDeer/go-codex/tree/main/examples/oee-chain) — full three-layer chain: codex + api/events + forge + AsyncAPI + pipeline spec

## Binary data in forge functions

`forge.NewFunction` accepts any codec type — including `codex.Bytes` for raw binary data (images, documents, sensor captures). Binary functions work exactly like numeric or struct functions:

```go
pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(5 * 1024 * 1024)).
    Refine(validate.PNG).
    WithTitle("rawImage")

// Validates input + output; PNG magic-byte check runs on both
resizeImage := forge.NewFunction("resizeImage", "1.0.0",
    pngCodec,
    pngCodec.WithTitle("resizedImage"),
    func(raw []byte) ([]byte, error) {
        return resizePNG(raw, 128, 128)
    },
    forge.FunctionMeta{Description: "Downscale PNG to 128×128 thumbnail."},
)

result, err := resizeImage.Apply(pngBytes)
// validate.PNG ran on pngBytes (input) and result (output)
```

Port names come from `.WithTitle(...)`. The pipeline YAML emits `schema: {type: string, format: binary}` for binary ports — readable and machine-processable.

### MeasuredCodec with binary values

`MeasuredCodec` wraps any codec, including binary:

```go
measuredPNG := forge.MeasuredCodec(codex.Bytes().Refine(validate.PNG))
```

Choose the value codec based on how `Measured[[]byte]` is serialised downstream:

| Downstream serialisation | Value codec | Why |
|--------------------------|-------------|-----|
| forge computation only (no serialisation) | `codex.Bytes()` | Raw bytes, no encoding overhead |
| Published via `format.Binary` (MQTT, HTTP binary) | `codex.Bytes()` | Identity marshal — bytes stay raw |
| Published via `format.JSON` (REST, MQTT JSON) | `codex.Base64()` | Go's JSON encoder base64-encodes `[]byte`; `Base64()` makes this explicit and round-trip correct |
