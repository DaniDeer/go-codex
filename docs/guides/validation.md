# Constraints & Refinements — define once, use everywhere

> See also: [Reactive Streams guide](stream.md) · [Ports guide](ports.md) · [Error Handling](error-handling.md) · [`validate` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/validate)
>
> Runnable demo: [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — the same `SensorTopicConstraint` and `APIKeyConstraint` values drive builder-level topic enforcement, header validation, and the OpenAPI/AsyncAPI specs.

A `codex.Constraint[T]` is a small, named, reusable value:

```go
type Constraint[T any] struct {
    Name    string                            // observability + error identity
    Check   func(T) bool                      // the predicate
    Message func(T) string                    // human-readable failure text
    Schema  func(schema.Schema) schema.Schema // optional: reflected into OpenAPI/AsyncAPI
}
```

That one value can serve **four consumers**: wire validation (`Refine`),
schema/spec documentation, pipeline routing (`stream.Filter`, `Switch`
cases), and structured error reporting (`ConstraintError{Name, Message}` +
observer `constraint` labels). Define the business rule once — everything
downstream agrees by construction.

## Step 1 — Define named constraints in the domain layer

Keep constraints next to your codecs, as package-level values:

```go
// domain/rules.go
package domain

// HotReading is the alerting threshold rule. ONE definition drives wire
// validation, the spec, the alert filter, and observer labels.
func HotReading(threshold float64) codex.Constraint[db.Reading] {
    return codex.Constraint[db.Reading]{
        Name:  "hot-reading",
        Check: func(r db.Reading) bool { return r.Value > threshold },
        Message: func(r db.Reading) string {
            return fmt.Sprintf("value %.1f exceeds threshold %.1f", r.Value, threshold)
        },
    }
}
```

For single-field rules, prefer the ~40 builtin constraints in `validate` —
they come with names, messages, AND schema effects:

```go
codex.String().Refine(validate.UUID)                        // + format: uuid in the spec
codex.Float64().Refine(validate.RangeFloat(-9999, 9999))    // + minimum/maximum in the spec
codex.String().Refine(validate.OneOf("C", "F", "pct"))      // + enum in the spec
codex.Bytes().Refine(validate.PNG)                          // binary signature check
```

## Step 2 — Enforce at the boundary with `Refine`

`Refine` wraps a codec so every `Encode` **and** `Decode` checks the
constraint — adapters reject bad data before it enters the pipeline, and the
constraint's `Schema` effect lands in the generated spec:

```go
var ReadingCodec = codex.Struct(
    codex.RequiredField("value",
        codex.Float64().Refine(validate.RangeFloat(-9999, 9999)), get, set),
    …,
)
```

Failures are `codex.ConstraintError{Name, Message}` — `errors.As`-navigable,
`slog.LogValuer`, and reported by every adapter's observer with the
constraint name (you have already seen this in logs:
`location=header constraint=api-key-format field=X-Api-Key`).

## Cross-field rules — name them too

`Constraint[T]` is generic over **any** `T`, including structs — a
cross-field rule is just a constraint whose `Check` reads several fields.
Written that way, it gets the same four consumers as a field rule:

```go
// domain/rules.go — cross-field, named, reusable
var ValidRange = codex.Constraint[DateRange]{
    Name:  "end-after-start",
    Check: func(r DateRange) bool { return r.End.After(r.Start) },
    Message: func(r DateRange) string {
        return fmt.Sprintf("end %s must be after start %s", r.End, r.Start)
    },
}

var rangeCodec = codex.Struct[DateRange](…).Refine(ValidRange) // boundary
valid := stream.Filter(ctx, ranges, ValidRange.Check)          // pipeline routing
```

Two honest caveats:

- **Cross-field rules don't reach the spec.** JSON Schema cannot express
  "end > start"; a struct-level `Constraint.Schema` effect has nowhere
  standard to land (a `description` note at most). Their value is
  validation + routing + observer identity — three of the four consumers.
- **`RefineFunc` is the anonymous shortcut, and boundary-only.** It takes
  `func(T) error` (not `bool`), and its failures carry the fixed name
  `"refine"` — no identity for logs, topology, or `Switch` cases. Use it
  when the rule exists only to reject at the boundary and the error text is
  dynamic (see the [SQL guide](sql.md)'s `db.User` example). The moment a
  pipeline wants to *route* on the same rule, promote it to a named
  `Constraint[T]`.
- Already have a `func(T) error` you can't change? Adapt it inline —
  `Check: func(v T) bool { return fn(v) == nil }` — no helper needed.

For **cross-input rules on forge functions**, `forge.WithRefinement` runs
after input-codec validation and surfaces as `RefinementError` (distinct
from `InputError` — you can tell field failures from cross-input failures
in the error chain):

```go
oeeFn := forge.NewFunction("oee", "1.0.0", inCodec, outCodec, compute,
    forge.WithRefinement(func(in OEEIn) error {
        if in.PlannedTime <= 0 { return errors.New("planned time must be positive") }
        return nil
    }))
```

Rule of thumb: constraints on the **codec** travel with the data everywhere
that codec is used (every adapter, every function reusing it); a
`WithRefinement` belongs to **one function's contract**. Prefer the codec
unless the rule is specific to that computation.

## Step 3 — Reuse the SAME constraints inside pipelines

This is the simplification: pipeline predicates are usually re-statements of
rules the boundary already knows. Don't re-state them — pass `Check`:

```go
hot := domain.HotReading(cfg.Threshold)

// Filter: the constraint IS the predicate.
alerts := stream.Filter(ctx, readings, hot.Check)

// Topology: the constraint name keeps the documentation honest.
topo.WithFilter(hot.Name + " — " + hot.Message(db.Reading{}))
```

No wrapper operator is needed — `Constraint.Check` has exactly `Filter`'s
predicate shape. What you gain over an anonymous closure:

| Anonymous `func(T) bool` | Named `Constraint[T]` |
|---|---|
| Logic duplicated per call site | One definition, N consumers |
| No identity in logs/topology | `Name` labels observer events, topology steps, errors |
| Invisible in the spec | `Schema` effect documents the rule in OpenAPI/AsyncAPI |
| Drifts from boundary validation | Boundary and pipeline share the same `Check` |

The planned routing operators take this further
([Stream — GroupBy & Switch roadmap](../roadmap/stream-groupby-switch.md)):
`stream.CaseConstraint(name, c)` turns a constraint directly into a `Switch`
case, so an alert/warning/archive router is three constraint declarations —
each of which can *also* refine a codec and appear in the spec.

## Step 4 — Combine with validated config

Constraints often carry tunable parameters. Combine with the
[validated-config factory pattern](config.md#passing-env-config-into-pipeline-functions):
load typed config once via `format.FromEnv`, build the constraint from it,
then hand the same value to the boundary and the pipeline:

```go
cfg, _ := format.FromEnv(domain.AlertConfigCodec, "APP_ALERT_") // threshold validated here
hot := domain.HotReading(cfg.Threshold)

alerts := stream.Filter(ctx, readings, hot.Check)               // pipeline
topo.WithFilter(hot.Name)                                       // documentation
```

`examples/sensor-service` demonstrates the full loop with
`APP_ALERT_THRESHOLD=90 go run ./examples/sensor-service` — the filter, the
printed topology, and the demo output all change together.

## What NOT to do

- **Don't validate mid-pipeline with bare codecs.** `stream.Apply` +
  `forge.Function` already validates input and output codecs per item — a
  separate "validate step" duplicates work. Put rules on the function's
  codecs or `WithRefinement`.
- **Don't encode routing in error channels.** Constraint failures at the
  boundary are *rejections* (item never enters); routing healthy items by a
  business rule is `Filter`/`Switch` on `Check` — two different jobs, same
  constraint value.
- **Don't build a predicate DSL.** Go closures over typed fields *are* the
  pattern language; `Constraint` adds the name, message, and schema — that's
  the whole abstraction.
