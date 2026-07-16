# Stream routing operators — `GroupBy` and `Switch` — `stream`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [Stream — Phase 4](stream-phase4.md) (GroupBy's original open
> questions, resolved here) · [Feature: Reactive Streams](../features/stream.md)
> · [Wiring Guide](../guides/ports.md)

## Motivation

Two routing shapes keep appearing in pipeline sketches and have no operator:

1. **Dynamic key routing** — "one sub-pipeline per sensor ID": the key set is
   unbounded and discovered at runtime. This is `GroupBy`, deferred since
   stream Phase 3 with two open design questions (resolved below).
2. **Static case routing** — "alerts to MQTT, warnings to the log sink,
   everything else to the archive": a *finite, declared* set of cases, each
   selected by a predicate or by the item's dynamic type. This is `Switch` —
   effectively GroupBy with a static key set, which makes a much simpler,
   leak-free API possible (all case streams exist up front; no callbacks).

Both are pure `stream`-package operators: no new adapters, no port changes.
They compose with ports naturally — a `Switch` case stream feeds a
`SinkPort`, a `GroupBy` sub-stream feeds a per-key pipeline.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `GroupBy[T, K comparable]` with callback exposure and parent-close lifetime | Per-key idle timeout / eviction (needs per-key timers; add only when a real use case leaks memory without it) |
| `Switch[T]` — static, named predicate cases, first-match-wins, plus a default stream | Variadic type parameters (impossible in Go) — heterogeneous type-case outputs use fixed-arity `SwitchType2/3` |
| `OfType[U, T]` — type-filtered stream (the single-case building block) | A full pattern-matching DSL — Go's pattern language IS predicates on struct fields; `Case.When` closures cover it |
| `SwitchType2/3[T, A, B(, C)]` — type-safe sum-type routing with typed outputs + rest stream | Reflection-based `CaseType(reflect.Type)` — boxing, no compile-time safety |
| Constraint adapter: build a `Case` from a `codex.Constraint[T]` | Routing by codec validation *result* (that is `MapErr`/error-channel territory) |
| `SwitchKey[T, K]` — keyed static routing sharing `TaggedUnion`'s `selectVariant` discriminator | Extracting the discriminator FROM a built `Codec[T]` (codecs are closures — nothing to introspect; share the named function instead) |
| `SplitEither[A, B]` — total typed split of `Stream[codex.Either[A, B]]` (no rest stream) | `SplitEither3+` — `Either` is binary; nest `Either[Either[A,B], C]` splits if ever needed |

## API surface

### `GroupBy` — dynamic key routing (Phase 4 questions resolved)

```go
// GroupBy splits src into per-key sub-streams. onKey is called once for each
// new key (from the GroupBy goroutine — it must return promptly; start the
// per-key pipeline in it, don't run it), with a Stream[T] receiving only that
// key's items. Per-key streams close when src terminates or ctx is cancelled.
//
// Errors from src are forwarded to every ACTIVE per-key stream (matching
// SinkPort fan-out semantics: an error is context, every consumer sees it).
//
//	stream.GroupBy(ctx, readings,
//	    func(r Reading) string { return r.SensorID },
//	    func(id string, s stream.Stream[Reading]) {
//	        go runSensorPipeline(ctx, id, s) // caller owns the goroutine
//	    },
//	    stream.GroupByOptions{Buffer: 8})
func GroupBy[T any, K comparable](
    ctx context.Context,
    src Stream[T],
    key func(T) K,
    onKey func(K, Stream[T]),
    opts GroupByOptions,
) // blocks until src terminates — run in a goroutine like SinkPort.Feed

// GroupByOptions configures GroupBy.
type GroupByOptions struct {
    // Buffer is each per-key channel's buffer. Default 0. A slow per-key
    // consumer backpressures the WHOLE GroupBy (single dispatch goroutine) —
    // buffer accordingly.
    Buffer int
    // Observer receives per-item RecordStreamItem events (location = "groupby").
    // Nil → resolved from ctx.
    Observer stats.Observer
}
```

**Resolved (from the Phase 4 open questions):**
- *Exposure:* **Option B (callback)** — Option A's meta-channel forces every
  caller into a two-level concurrent drain; the callback is one line and
  cannot be forgotten. `onKey` runs on the dispatch goroutine → documented
  "start, don't run" contract.
- *Lifetime:* **close on parent close** — no per-key timers, no leak: when
  src terminates, every per-key channel closes and the (caller-owned)
  per-key pipelines drain out.

### `Switch` — static predicate routing

```go
// Case is one Switch branch: a name (observability + topology) and a
// predicate. Build from a codex constraint with CaseConstraint.
type Case[T any] struct {
    Name string
    When func(T) bool
}

// CaseConstraint adapts a codex.Constraint[T] into a Case — the same
// declarative vocabulary used for validation doubles as routing predicates.
func CaseConstraint[T any](name string, c codex.Constraint[T]) Case[T] {
    return Case[T]{Name: name, When: c.Check}
}

// Switch routes each item to the FIRST case whose predicate matches; items
// matching no case go to the returned default stream. All case streams exist
// up front (static key set — no callback needed, unlike GroupBy): cases[i]
// corresponds to out[i]. Errors from src are forwarded to the default
// stream only (single ownership — no duplicate error handling).
//
//	caseStreams, rest := stream.Switch(ctx, readings, []stream.Case[Reading]{
//	    {Name: "alert",   When: func(r Reading) bool { return r.Value > 90 }},
//	    {Name: "warning", When: func(r Reading) bool { return r.Value > 70 }},
//	}, stream.SwitchOptions{})
//	go alertsPort.Feed(ctx, caseStreams[0])
//	go warningsPort.Feed(ctx, caseStreams[1])
//	go archivePort.Feed(ctx, rest)
func Switch[T any](
    ctx context.Context,
    src Stream[T],
    cases []Case[T],
    opts SwitchOptions,
) (out []Stream[T], rest Stream[T])

// SwitchOptions configures Switch.
type SwitchOptions struct {
    Buffer   int            // per-case channel buffer; default 0
    Observer stats.Observer // RecordStreamItem per item, location = case name; nil → ctx
}
```

Construction validates the cases eagerly: empty `Name`, duplicate `Name`, or
nil `When` **panic** (programming errors in a literal slice, caught first
run — same class as a nil forge function, not a runtime condition; no error
return keeps the two-value signature usable inline).

### Type-based routing — `OfType` and `SwitchType2/3`

Go note: in a typed `Stream[T]` every item *is* a `T` — switching "on the
input type" is only meaningful when `T` is an interface (sum type). Two
type-safe shapes, no reflection:

```go
// OfType filters src to items whose dynamic type is U, emitting them as a
// typed Stream[U]. Items of other types are dropped silently; errors are
// forwarded. The single-case building block — compose with BroadcastHub for
// ad-hoc multi-type fanouts.
//
//	events   := ...                                  // Stream[DomainEvent] (interface)
//	created  := stream.OfType[OrderCreated](ctx, events)
func OfType[U any, T any](ctx context.Context, src Stream[T]) Stream[U]

// SwitchType2 routes a sum-typed stream into two TYPED case streams plus the
// untyped rest — the pattern-matching shape Go can express without variadic
// type parameters. First match wins (A before B); errors go to rest.
//
//	created, cancelled, other := stream.SwitchType2[OrderCreated, OrderCancelled](ctx, events, opts)
func SwitchType2[A any, B any, T any](
    ctx context.Context, src Stream[T], opts SwitchOptions,
) (Stream[A], Stream[B], Stream[T])

// SwitchType3 — three typed cases; same contract.
func SwitchType3[A, B, C any, T any](
    ctx context.Context, src Stream[T], opts SwitchOptions,
) (Stream[A], Stream[B], Stream[C], Stream[T])
```

Fixed arity follows the `CombineLatest2/3/4` precedent; for more cases,
compose (`SwitchType2` on the rest stream) — same guidance as the
CombineLatest>4 nesting note in the stream guide.

## Codec sum-type integration — the discriminator IS the router

Review finding (2026-07-16): the codec layer already has three sum-type
primitives, and each one carries exactly the piece of information Switch/
GroupBy would otherwise force the user to re-declare. Routing should reuse
those declarations, not duplicate them.

### `TaggedUnion` → keyed routing (single source of truth)

`codex.TaggedUnion[T](tag, variants map[string]Codec[T], selectVariant
func(T) (string, error))` already contains:

- **the static case set** — the variant map's keys, and
- **the key function** — `selectVariant` is precisely GroupBy's
  `key func(T) K` and the case selector Switch needs.

The same declaration also feeds the **spec**: the union's `oneOf` +
`discriminator` schema documents the variant set in OpenAPI/AsyncAPI. Reusing
`selectVariant` for routing means wire format, spec, topology step names, and
runtime routing can never drift apart.

Because a built `Codec[T]` is a struct of closures, `selectVariant` cannot be
*extracted* after construction — so the design adds a keyed Switch variant
that the user plugs the same named function into:

```go
// SwitchKey routes each item by key to the case stream whose key matches —
// the keyed sibling of Switch (predicates) and the static sibling of GroupBy
// (dynamic keys). keys is the declared case set; out[i] ↔ keys[i]. Items
// whose key is not in keys — and src errors — go to rest.
//
//	// ONE declaration drives wire format, schema, spec, AND routing:
//	func orderEventKind(e OrderEvent) (string, error) { return e.Kind, nil }
//	var orderEventCodec = codex.TaggedUnion("kind", variants, orderEventKind)
//
//	streams, rest := stream.SwitchKey(ctx, events,
//	    []string{"created", "cancelled"},
//	    func(e OrderEvent) string { k, _ := orderEventKind(e); return k },
//	    stream.SwitchOptions{})
func SwitchKey[T any, K comparable](
    ctx context.Context,
    src Stream[T],
    keys []K,
    keyOf func(T) K,
    opts SwitchOptions,
) (out []Stream[T], rest Stream[T])
```

For **GroupBy**, no new API is needed — the guidance is documentation:
declare `selectVariant` as a named function and pass it to both
`TaggedUnion` and `GroupBy`. One discriminator, three consumers (codec,
spec, router).

### `UntaggedUnion` → positional selection precedent

`UntaggedUnion`'s `which func(T) int` is *positional* discrimination — the
same shape as Switch's `out[i] ↔ cases[i]` contract. This validates the
positional-outputs decision (see Open design decisions): the codec layer
already asks users to think in variant indices.

### `Either2` → `SplitEither` (total, typed, no interface needed)

`codex.Either2[A, B]` materializes a **closed** sum as `Either[A, B]` — and
adapters decoding with `format.JSON(codex.Either2(ca, cb))` produce
`Stream[Either[A, B]]` at the boundary today. Splitting that stream is
total: every item is Left or Right, so there is **no rest stream** — a
strictly stronger contract than `SwitchType2` (which needs an interface `T`
and can always have unmatched items):

```go
// SplitEither splits a stream of codex.Either values into its two typed
// branches. Total: every item goes to exactly one branch; no rest stream.
// src errors are forwarded to BOTH branches (independent consumers, GroupBy
// semantics).
//
//	// boundary decodes into the sum; the pipeline splits it — typed end to end
//	events := ports.SourcePort → Stream[codex.Either[OrderCreated, OrderCancelled]]
//	created, cancelled := stream.SplitEither(ctx, events, stream.SwitchOptions{})
func SplitEither[A, B any](
    ctx context.Context,
    src Stream[codex.Either[A, B]],
    opts SwitchOptions,
) (Stream[A], Stream[B])
```

`SplitEither` is the codec-native alternative to `SwitchType2`: prefer it
when the boundary decodes into `Either` (data was a wire-level union);
use `OfType`/`SwitchType2` when `T` is a domain interface that was never a
codec union. Note: `stream` already imports `codex` — no new dependency.

### Constraint reuse — simplify by NOT adding operators

The same review confirmed a broader principle, now documented in the
[Constraints & Refinements guide](../guides/validation.md): the codec layer's
refinement machinery (`codex.Constraint[T]` — Name/Check/Message/Schema —
plus the ~40 `validate.*` builtins) already covers most pipeline predicates.

- **`stream.Filter` needs no constraint wrapper** — `Constraint.Check` has
  exactly the predicate shape; the guide shows `Filter(ctx, s, hot.Check)`.
  A `FilterConstraint` operator was considered and rejected: it would add
  API surface for what one field access does.
- **`CaseConstraint` (this plan) is the only adapter needed** — `Switch`
  cases want the *name* too (observability, topology, positional identity),
  so the pairing earns a helper.
- **Mid-pipeline validation needs no operator at all** — `stream.Apply` +
  `forge.Function` already validates input/output codecs per item; rules
  belong on the function's codecs or `forge.WithRefinement`.

Net effect on this plan: no scope change, one more reason `Case`/`
CaseConstraint` is the right shape.

### Summary of what the sum types contribute

| Codec primitive | Contributes to routing | Design impact |
|---|---|---|
| `TaggedUnion` (`selectVariant`, variant names, discriminator schema) | Key function + static case set + spec-visible case names | New `SwitchKey`; GroupBy documentation pattern; topology/spec name alignment |
| `UntaggedUnion` (`which func(T) int`) | Positional selection precedent | Confirms positional `out[i]` decision |
| `Either2` / `Either[A, B]` | Closed, already-typed sum at the boundary | New `SplitEither` — total split, no rest, no interface requirement |

## Structured errors (all implement `slog.LogValuer`)

**None.** All four operators route existing items; they introduce no new
failure modes. Predicates and key functions are pure (`func(T) bool` /
`func(T) K` — no error return, matching `Filter`'s contract); malformed
`Case` slices panic at construction (programming error). Upstream errors
follow the documented forwarding rules (GroupBy: all active keys; Switch/
SwitchType: rest stream; OfType: forwarded).

## Observer integration

- Per-item `stats.StreamObserver.RecordStreamItem(location, true, duration)`
  with location `"groupby"` (GroupBy) or the case `Name` (Switch) / `"oftype"`
  / type-case index (`"switchtype.0"`, `"switchtype.1"`, `"switchtype.rest"`)
  — type-asserted guard, plain Observers unaffected.
- Nil `Observer` → `stats.ObserverFromContext(ctx)` (both option structs have
  ctx in scope — standard rule).
- No TraceObserver spans: routing is not a computation boundary (same
  rationale as `Filter`/`Tee`, which emit nothing).

## Topology integration

`Topology.WithSwitch(description string)` and `WithGroupBy(description
string)` step kinds (`StepKindSwitch = "switch"`, `StepKindGroupBy =
"groupBy"`) — mirroring `WithFilter`/`WithTee`; `render/stream` picks them up
generically. Small, included in scope so the flagship-style topology print
stays honest.

## Unit test plan

| ID | Test | Verifies |
|----|------|----------|
| G1 | `TestGroupBy_OneStreamPerKey` | 2 keys interleaved → onKey twice, each sub-stream gets only its items, order preserved per key |
| G2 | `TestGroupBy_SubStreamsCloseOnParentClose` | src closes → all per-key Values/Errors channels close |
| G3 | `TestGroupBy_ErrorsFanOutToActiveKeys` | src error → every active sub-stream's Errors receives it |
| G4 | `TestGroupBy_CtxCancelUnblocks` | blocked dispatch (unbuffered, unread sub-stream) unblocks on cancel |
| G5 | `TestGroupBy_ObserverPerItem` | RecordStreamItem("groupby", …) per routed item |
| S1 | `TestSwitch_FirstMatchWins` | item matching cases 0 and 1 lands ONLY in stream 0 |
| S2 | `TestSwitch_DefaultStream` | non-matching items land in rest; case streams unaffected |
| S3 | `TestSwitch_ErrorsToDefaultOnly` | src error appears once, on rest.Errors |
| S4 | `TestSwitch_InvalidCasesPanic` | empty/duplicate name, nil When → panic with descriptive message |
| S5 | `TestSwitch_CaseConstraint` | `CaseConstraint("hot", validate.MinFloat(90)…)` routes like the hand predicate |
| S6 | `TestSwitch_AllStreamsCloseOnSrcEnd` | every case stream + rest closes |
| T1 | `TestOfType_FiltersDynamicType` | interface stream → only U items, typed; others dropped; errors forwarded |
| T2 | `TestSwitchType2_RoutesTyped` | A/B/other items land in the right typed streams; rest gets others + errors |
| T3 | `TestSwitchType2_FirstMatchWins` | value satisfying both A and B (embedding) lands only in A |
| K1 | `TestSwitchKey_RoutesByKey` | declared keys → matching streams; unknown key → rest; positional `out[i] ↔ keys[i]` |
| K2 | `TestSwitchKey_SharedTaggedUnionDiscriminator` | the SAME named selectVariant fn drives a `TaggedUnion` codec round-trip AND SwitchKey routing — no drift |
| E1 | `TestSplitEither_TotalSplit` | Left/Right land in their typed branches; both branches close on src end; no items lost |
| E2 | `TestSplitEither_ErrorsFanOutToBoth` | src error appears on both branches' Errors |
| TP1 | `TestTopology_WithSwitch_WithGroupBy` | step kinds + constants-table row |
| EX1 | `ExampleSwitch` / `ExampleGroupBy` | runnable pkg.go.dev examples (channel sources, deterministic) |

## Files to create / modify

| File | Change |
|---|---|
| `stream/route.go` (new) | `GroupBy`, `GroupByOptions`, `Switch`, `Case`, `CaseConstraint`, `SwitchOptions`, `SwitchKey`, `OfType`, `SwitchType2`, `SwitchType3`, `SplitEither` |
| `stream/route_test.go` (new) | G1–G5, S1–S6, K1–K2, E1–E2, T1–T3, EX1 |
| `stream/topology.go` (+ test) | `StepKindSwitch`, `StepKindGroupBy`, `WithSwitch`, `WithGroupBy` |
| `stream/doc.go` | Transform/routing operator list |
| `docs/features/stream.md`, `docs/guides/stream.md` | Operator tables + a routing section (Switch → SinkPorts wiring example) |
| `docs/roadmap/stream-phase4.md` | GroupBy section → resolved-pointer to this doc (FlatMap remains the doc's only open item) |
| `.github/instructions/go-codex.instructions.md` | `stream` row additions |
| Example | Extend `examples/stream-pipeline` with a Switch scene (alert/warning/archive) — decide GroupBy demo at implementation |

## Open design decisions (to resolve before/during implementation)

| Question | Leaning |
|---|---|
| `Switch` error routing: rest-only vs fan-out to all cases | **Rest-only** (single ownership; GroupBy differs because its consumers are independent pipelines) — revisit if the first real wiring wants per-case error visibility |
| Should `Switch` return named access (`map[string]Stream[T]`) instead of positional? | **Positional** (`out[i]` ↔ `cases[i]`) — compile-time-checkable at call sites, no map allocation; names serve observability/topology only |
| `GroupBy` max-keys guard (cap + error vs unbounded) | **Unbounded, documented** — a cap needs an eviction policy, which is the deferred idle-timeout problem in disguise |
