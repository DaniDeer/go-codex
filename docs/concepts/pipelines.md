# Forge Pipelines

> See also: [`forge` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/forge) · [`render/pipeline` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/pipeline)
>
> Runnable demos: [`examples/forge-oee`](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-oee) · [`examples/forge-collection`](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-collection) · [`examples/oee-chain`](https://github.com/DaniDeer/go-codex/tree/main/examples/oee-chain)

`forge` is the third layer of go-codex. It adds **named, versioned, and governance-tracked computation** on top of the validated domain types from Layer 1 and the event/REST channels from Layer 2.

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

## See also

- [examples/forge-oee](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-oee) — OEE KPI computation, governance, Compose, MeasuredCodec
- [examples/forge-collection](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-collection) — Map, Filter, Reduce, MapValuesK on sensor batches
- [examples/oee-chain](https://github.com/DaniDeer/go-codex/tree/main/examples/oee-chain) — full three-layer chain: codex + api/events + forge + AsyncAPI + pipeline spec
