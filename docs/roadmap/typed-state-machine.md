# Typed State Machine Contracts & Runtime — `state` + `stream` + `forge`

> **Status:** Design draft.
> [← Back to Roadmap](index.md)

## Motivation

### Codecs already define valid shapes — state machines define valid progressions

`codex.Codec[T]` already validates that a value is well-formed: field types,
constraints, required fields. But many domain models have a second layer of
rules: not "is this value valid?" but "is this *transition* valid, given
where we are now?" An order can only be shipped after it's been paid. A ticket
can only be closed after it's been opened. A workflow step can only be approved
by someone with the right role.

Today, that second layer lives in ad-hoc `if/switch` blocks scattered across
handlers, pipeline steps, and business logic. This feature brings it into
go-codex's declarative layer — the same "define once, validate everywhere"
philosophy that REST routes, event channels, and codec constraints already follow.

### Declare once, use anywhere

The pattern a go-codex user already knows:

```
declare the contract as a value
    ↓
compose it with codecs + constraints
    ↓
register it anywhere — REST routes, event channels, pipeline steps, forge functions
```

Typed state machines follow the same pattern:

```
declare Spec[S,K,E] as a value (state codec + event codecs + transition graph)
    ↓
compose with codex.Constraint[S] per state key (reuse domain constraints already declared)
    ↓
apply it anywhere — stream.Reduce, forge.NewFunction, direct Machine.Apply calls
```

`Spec[S,K,E]` is a plain Go value. It is declared once and passed to
whatever runtime helpers need it — the same way `rest.RouteHandle` is derived
once from a `Route` and passed to both `nethttp.Register` and `nethttp.Call`.
There is no "machine builder" to configure at each call site.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope (Phase 1) | Out of scope (deferred) |
|---|---|
| Codec-first `Spec[S,K,E]` — state codec, per-key codecs/constraints, event codec, transition graph | Distributed coordination / consensus for transitions |
| `Machine[S,K,E]` runtime executor derived from the spec | Exactly-once replay/recovery guarantees across process restarts |
| Per-state `codex.Constraint[S]` — reuse existing domain constraints, validated before AND after transitions | Automatic SQL/EventStore persistence adapters |
| `state.Reduce` stream helper — emits validated successor states | Multi-writer conflict resolution semantics |
| `forge.NewTransitionFunction` convenience wrapper | Visual workflow editor / UI modeler |
| Typed errors with `slog.LogValuer`, observer integration | |

## Toolchain / dependency decisions

- **No external FSM dependency in Phase 1** — stdlib + go-codex only.
  Explicit generic types; no external runtime conventions.
- Re-evaluate external libraries only if Phase 2 demands capabilities not
  practical with current primitives.

## Design principle: codecs for everything

The "use codecs for everything" principle means:

| Concern | How it is expressed |
|---|---|
| Valid state shape | `codex.Codec[S]` — same codec used in REST/events/file/cache |
| Valid state value at a given key | `codex.Constraint[S]` — named, reusable; identical to constraints already applied to REST route params and forge function inputs |
| Valid event shape | `codex.Codec[E]` — event is validated before any transition lookup |
| Per-transition guard | `codex.Constraint[S]` on the source state |
| Schema / documentation | schema already surfaces from codecs automatically |

No ad-hoc `func(S) error` guards at the call site — declare the constraint
once (`codex.Constraint[S]{Name: "order.paid", Check: ...}`) and reuse it in
the `Spec`, in the REST route that changes the order, and in any forge
function that validates the order payload before writing it.

## API surface

### `state.Spec[S,K,E]` — the contract value

```go
package state

import "github.com/DaniDeer/go-codex/codex"

// Transition declares a single valid edge in the state graph.
type Transition[K comparable, E comparable] struct {
	From  K
	Event E
	To    K
}

// Spec is the machine's contract — a plain value, declared once, passed anywhere.
// S is the state type, K is the state-key type (comparable), E is the event type.
type Spec[S any, K comparable, E comparable] struct {
	// Codec validates the state shape on every Apply and ValidateState call —
	// the same codex.Codec[S] used elsewhere in the domain layer.
	Codec codex.Codec[S]

	// Key extracts the logical state key from a state value (e.g. order.Status).
	Key func(S) K

	// EventCodec validates the event before any transition lookup.
	// E may be a string, an integer enum, or a structured codec.Codec[E] type.
	EventCodec codex.Codec[E]

	// Transitions is the full transition graph — declare once.
	Transitions []Transition[K, E]

	// ConstraintsByKey holds per-state codex.Constraint[S] rules applied
	// before (source) and after (target) a transition.
	// Reuse any named constraint already declared for other boundaries.
	ConstraintsByKey map[K][]codex.Constraint[S]

	// Order is an optional forward-only rank; when set a transition whose
	// target rank ≤ source rank is rejected as TransitionDirectionError.
	Order map[K]int
}
```

### `state.Machine[S,K,E]` — the runtime executor

`NewMachine` validates the spec eagerly (duplicate edges, unreachable keys,
constraint-name conflicts) and returns a *Machine — a compiled, immutable
value passed to all helpers.

```go
type Machine[S any, K comparable, E comparable] struct { /* unexported */ }

// NewMachine compiles spec into an immutable, shareable Machine value.
// Returns SpecError on invalid declaration.
func NewMachine[S any, K comparable, E comparable](spec Spec[S, K, E]) (*Machine[S, K, E], error)

// ValidateState validates s against the state codec AND the per-key constraints.
func (m *Machine[S, K, E]) ValidateState(s S) error

// Can reports whether event is a valid transition from s's current key.
func (m *Machine[S, K, E]) Can(event E, s S) bool

// Apply validates the event (via EventCodec), resolves the transition,
// validates source-state constraints, calls mutate, then validates
// the returned state (codec + target-key constraints).
// One call — fully validated in, fully validated out.
func (m *Machine[S, K, E]) Apply(event E, s S, mutate func(cur S, to K) (S, error)) (S, error)
```

### `state.Reduce` — stream helper

```go
package state

type ReduceOptions struct {
	Name     string        // machine name for observer reporting
	Observer stats.Observer
}

// Reduce folds a stream of events onto an initial state using m.Apply.
// Each successfully-transitioned state is emitted downstream; transition
// or validation failures are sent to the stream's error channel.
func Reduce[S any, K comparable, E comparable](
	ctx context.Context,
	src stream.Stream[E],
	initial S,
	m *Machine[S, K, E],
	mutate func(cur S, evt E, to K) (S, error),
	opts ReduceOptions,
) stream.Stream[S]
```

### `forge.NewTransitionFunction` — forge helper

```go
// NewTransitionFunction wraps a Machine as a named forge.Function[E, S].
// The current state is carried in a closure (suitable for single-writer
// sequential pipelines). Reuse the same forge composition vocabulary:
// forge.Compose, forge.Registry.Register, forge.Topology.WithPort.
func NewTransitionFunction[S any, K comparable, E comparable](
	name string,
	initial S,
	m *Machine[S, K, E],
	mutate func(cur S, evt E, to K) (S, error),
) forge.Function[E, S]
```

### Usage sketch — declare once, apply anywhere

```go
// domain/order.go — declare the contract as a package-level value.

var orderMachine = codex.Must(state.NewMachine(state.Spec[Order, OrderStatus, OrderEvent]{
    Codec:      OrderCodec,         // already declared for REST/events/cache
    EventCodec: codex.String().Refine(validate.NonEmpty),
    Key:        func(o Order) OrderStatus { return o.Status },
    Transitions: []state.Transition[OrderStatus, OrderEvent]{
        {From: StatusPending,  Event: EventPay,    To: StatusPaid},
        {From: StatusPaid,     Event: EventShip,   To: StatusShipped},
        {From: StatusShipped,  Event: EventDeliver, To: StatusDelivered},
    },
    // Reuse the SAME constraints already on your REST route or forge function:
    ConstraintsByKey: map[OrderStatus][]codex.Constraint[Order]{
        StatusPaid:    {OrderPaidConstraint},      // same constraint as the payment forge fn
        StatusShipped: {OrderShippedConstraint},
    },
    Order: map[OrderStatus]int{
        StatusPending: 0, StatusPaid: 1, StatusShipped: 2, StatusDelivered: 3,
    },
}))

// pipeline/order_pipeline.go — apply in a stream pipeline.
func BuildOrderPipeline(ctx context.Context, events stream.Stream[OrderEvent], initial Order) stream.Stream[Order] {
    return state.Reduce(ctx, events, initial, orderMachine, applyOrderMutation,
        state.ReduceOptions{Name: "order", Observer: stats.ObserverFromContext(ctx)})
}

// handler/order_handler.go — apply directly in a REST handler.
func handleOrderEvent(ctx context.Context, req TransitionReq) (Order, error) {
    return orderMachine.Apply(req.Event, req.Current, func(cur Order, to OrderStatus) (Order, error) {
        cur.Status = to
        cur.UpdatedAt = time.Now()
        return cur, nil
    })
}

// forge integration — same machine, wrapped as a named forge Function.
var orderTransitionFn = forge.NewTransitionFunction("order.transition", Order{}, orderMachine, applyOrderMutation)
```

## Structured errors (all implement `slog.LogValuer`)

| Error type | When | `Unwrap` |
|---|---|---|
| `SpecError{Op string, Err error}` | invalid spec (duplicate edge, unreachable key, bad order) | yes |
| `StateValidationError[K]{Key K, Err error}` | codec or constraint violation on state value | yes |
| `EventValidationError{Err error}` | `EventCodec` rejects the event before lookup | yes |
| `TransitionNotAllowedError[K,E]{From K, Event E}` | edge not in transition graph | no |
| `TransitionDirectionError[K]{From K, To K}` | forward-only order violated | no |
| `ApplyError[K,E]{From K, Event E, To K, Err error}` | mutate fn failed after valid transition | yes |

All implement `Error() string`, `LogValue() slog.Value` (group with typed keys),
and `Unwrap() error` where marked.

## Observer integration

Phase 1 reuses `stats.Observer`:
- `RecordRequest("state.apply", machine name, success, duration)` per `Apply`/`Reduce` call
- `stats.ReportErrors(obs, "state", err)` propagates `ValidationErrors` per-field

Phase 2 candidate (add only if `RecordRequest` proves too coarse):

```go
type StateObserver interface {
    RecordTransition(machine string, from, event, to string, success bool, d time.Duration)
}
```

Type-asserted, same pattern as `SQLObserver`/`TraceObserver`. Reuse
`NoopObserver`/`LoggingObserver`/`fanout` implementations.

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| SM-01 | `NewMachine_ValidSpec` | valid spec compiles to Machine |
| SM-02 | `NewMachine_DuplicateEdge` | duplicate transition → `SpecError`, `errors.As`, `LogValue` |
| SM-03 | `ValidateState_CodecFailure` | state codec violation → `StateValidationError` |
| SM-04 | `ValidateState_ConstraintByKey` | per-key constraint fires on the right key only |
| SM-05 | `Apply_ValidTransition` | happy path: event validated, state transitions, target constraints run |
| SM-06 | `Apply_EventCodecRejects` | bad event → `EventValidationError` before graph lookup |
| SM-07 | `Apply_TransitionForbidden` | unlisted edge → `TransitionNotAllowedError` |
| SM-08 | `Apply_ForwardOnlyViolation` | lower-rank target → `TransitionDirectionError` |
| SM-09 | `Apply_MutateFailure` | mutate error → `ApplyError`; `errors.As` reaches cause |
| SM-10 | `Apply_ConstraintReuse` | same `codex.Constraint[S]` used in Spec AND in a REST route codec validates identically |
| SM-11 | `Errors_LogValueShape` | every error type returns `slog.KindGroup` with expected attribute keys |
| SM-12 | `Reduce_SequentialFlow` | stream emits correct state sequence |
| SM-13 | `Reduce_ObserverSuccessAndFailure` | observer called on every Apply outcome |
| SM-14 | `NewTransitionFunction_ForgeCompose` | wrapped as forge.Function; composable with forge.Compose |

## Files to create

| File | Responsibility |
|---|---|
| `state/machine.go` | `Spec`, `Transition`, `Machine`, `NewMachine`, `Apply`, `Can`, `ValidateState` |
| `state/reduce.go` | `Reduce` stream helper + `ReduceOptions` |
| `state/errors.go` | all typed errors with `Error`, `Unwrap`, `LogValue` |
| `state/doc.go` | package overview; "declare once, apply anywhere" pattern |
| `state/machine_test.go` | SM-01–SM-11 |
| `state/reduce_test.go` | SM-12–SM-13 |
| `forge/state_function.go` | `NewTransitionFunction` |
| `forge/state_function_test.go` | SM-14 |
| `examples/state-machine/main.go` | runnable order-lifecycle demo: `Spec` declared once, applied via stream pipeline AND direct handler call AND forge function — all three from the same `orderMachine` value |

## Out of scope (Phase 2)

- Durable transition log and replay storage.
- Distributed/stateful orchestrator behavior.
- Automatic retry/compensation policy engine.
- Conflict resolution for concurrent transition writers.
- GUI/statechart authoring tooling.

## Open design decisions

1. **`Apply` immutable vs. in-place convention**: `mutate` func returns a new `S`
   (immutable-style). Fits the "validated in, validated out" promise cleanly. Leaning: keep immutable return.
2. **Forward-only opt-in vs. always-on when `Order` set**: when `Order` is non-nil,
   ALL transitions are forward-only by default. Leaning: always-on when Order present — an explicit nil Order map opts out entirely.
3. **`EventCodec` required vs. optional**: if `E` is already a strongly-typed
   enum (e.g. `type OrderEvent string`), declaring a codec just for compile-time safety may feel redundant. Leaning: optional (`nil` skips event validation, like `nil` observers skip reporting).
4. **Forge integration phase**: `NewTransitionFunction` closes over mutable
   state (current S in a closure) — incompatible with forge's stateless-function
   model if used in a concurrent fan-out. Leaning: ship in Phase 1 with a clear
   doc warning: "single-writer sequential only; use `state.Reduce` for
   concurrent stream fan-out."
5. **`StateObserver` phase**: existing `RecordRequest` is sufficient for Phase 1.
   Add `StateObserver` only after real observability friction is observed in practice.
