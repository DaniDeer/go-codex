# Dynamic Port Rebinding (Hot-Swap Adapters) — `ports`

> **Status:** Design draft.
> [← Back to Roadmap](index.md)

---

## Motivation

`ports` keeps pipeline code transport-agnostic, but adapter changes still require process restart. Some deployments need live transport cutover (broker failover, endpoint rotation, credential rollover, phased migration) without dropping long-lived streams or app-level availability. Dynamic rebinding adds explicit, typed adapter hot-swap on already-running ports while preserving existing static `Bind` behavior.

> **Cross-reference:** "credential rollover" above is listed as one
> motivating scenario for hot-swap, but it is ALREADY solved for
> `ports`, today, by [Declarative Middleware](declarative-middleware.md)'s
> per-call middleware attachment — a `ports` security/credential
> middleware value is never baked into an immutable handle, so it can be
> swapped between calls with zero mechanism from this doc. This doc's
> `Rebind` remains necessary for swapping the underlying TRANSPORT
> ADAPTER itself (broker failover, endpoint rotation, phased
> migration) — a different concern from credential rotation. See
> Declarative Middleware's "L11" for the full reconciliation; note also
> that REST/events/reqreply's immutable `RouteHandle`/`ChannelHandle`
> middleware attachment has NO hot-swap story today — an acknowledged
> gap in BOTH docs, not yet designed.

**Operational baseline today:** fail-fast + process/container restart remains a
valid default strategy and is intentionally simple. Dynamic rebinding is for
cases where restart-based recovery is insufficient (strict availability
targets, coordinated cutover windows, zero-restart operations).

---

## Scope decisions (Phase 1 vs deferred)

| In scope (Phase 1) | Out of scope (deferred) |
|---|---|
| Hot-swap for single-binding ports first: `IOPort`, `DuplexPort`, `LatestPort` (exactly one active binding) | Automatic policy engine (health-score-based auto-rebind) |
| Explicit `Rebind` API (caller-triggered), old binding cancelled via context | Transparent in-flight request replay across adapter swap |
| Adapter lifecycle tracked by binding handle/token | Cross-process/distributed rebinding orchestration |
| Structured typed errors + observer events for bind/rebind/unbind outcomes | Zero-loss guarantees for every transport (transport-specific) |
| Compatibility-preserving path: existing `Bind` still valid and unchanged | Source/Sink multi-adapter weighted traffic shaping |

Phase 2 extends to `SourcePort` and `SinkPort` with named binding sets (add/remove/replace by binding key).

**Complexity note:** implementation is medium-high complexity (lifecycle state
machine, race-free generation control, failure-phase semantics). Operation is
low-medium for manual rebinding, higher only when/if automatic policies are
introduced.

---

## API shape options

### Option A — Minimal additive methods on ports (recommended)

```go
// Token representing one live adapter binding.
type BindingRef struct {
    Port       string
    Adapter    string
    Generation uint64
}

// RebindOptions controls swap behavior.
type RebindOptions struct {
    // Timeout bounds graceful stop of old binding before forced detach.
    Timeout time.Duration
    // KeepOldOnActivateFailure keeps old binding if new Activate/Bind fails.
    KeepOldOnActivateFailure bool
    Observer stats.Observer
}

func (p *IOPort[Req, Resp]) BindRef(ctx context.Context, a IOAdapter[Req, Resp]) (BindingRef, error)
func (p *IOPort[Req, Resp]) Rebind(ctx context.Context, a IOAdapter[Req, Resp], opts RebindOptions) (BindingRef, error)
func (p *IOPort[Req, Resp]) Unbind(ctx context.Context, ref BindingRef, opts RebindOptions) error

func (p *DuplexPort[In, Out]) BindRef(ctx context.Context, a DuplexAdapter[In, Out]) (BindingRef, error)
func (p *DuplexPort[In, Out]) Rebind(ctx context.Context, a DuplexAdapter[In, Out], opts RebindOptions) (BindingRef, error)
func (p *DuplexPort[In, Out]) Unbind(ctx context.Context, ref BindingRef, opts RebindOptions) error

func (p *LatestPort[T]) BindRef(ctx context.Context, a LatestAdapter[T]) (BindingRef, error)
func (p *LatestPort[T]) Rebind(ctx context.Context, a LatestAdapter[T], opts RebindOptions) (BindingRef, error)
func (p *LatestPort[T]) Unbind(ctx context.Context, ref BindingRef, opts RebindOptions) error
```

Why recommended: smallest surface, preserves existing model, no new manager type required, easy phased rollout.

### Option B — Shared `BindingManager`

Generic helper owns lifecycle for all port kinds; ports delegate. Cleaner internals, but larger up-front refactor and broader risk. Keep as implementation detail candidate after Phase 1 proves semantics.

---

## Lifecycle semantics

1. `BindRef` creates generation `N`, starts adapter, returns ref.
2. `Rebind` starts candidate generation `N+1` first.
3. If candidate starts successfully, old generation gets cancellation signal; swap pointer atomically.
4. If candidate start fails:
   - default: keep old live (`KeepOldOnActivateFailure=true`), return typed error;
   - optional strict mode: old already stopped only if caller opts in.
5. `Unbind` validates ref generation and stops only matching live binding.

Concurrency rule: one rebind/unbind transition per port at a time (mutex/serialized control path).

---

## Structured errors (all implement `slog.LogValuer`)

```go
type PortRebindError struct {
    Port       string
    Adapter    string
    Generation uint64
    Phase      string // activate_new | stop_old | commit_swap
    Err        error
}

type PortUnbindError struct {
    Port       string
    Adapter    string
    Generation uint64
    Phase      string // validate_ref | stop_binding
    Err        error
}

type PortBindingStateError struct {
    Port string
    Op   string // bind_ref | rebind | unbind
    Msg  string // invalid state description
}
```

`PortRebindError` and `PortUnbindError` implement `Unwrap() error`. All three expose grouped fields in `LogValue()` for stable structured logs.

---

## Observer behavior

Phase 1 reuses `stats.Observer` (`RecordRequest`) with operation names:
- `"port.bind_ref"`
- `"port.rebind"`
- `"port.unbind"`

Payload conventions:
- `target`: port name
- `metadata`: adapter name + generation + phase
- error paths always report typed error (`PortRebindError`/`PortUnbindError`/`PortBindingStateError`)

Recommended metrics/tracing to standardize in Phase 1:
- `rebind_attempt_total{port,adapter,result,phase}`
- `rebind_duration_seconds{port,adapter}`
- `active_binding_generation{port,adapter}`
- `unbind_stale_ref_total{port}`
- trace spans: parent `port.rebind`, child spans per phase (`activate_new`,
  `stop_old`, `commit_swap`)

Optional Phase 2 extension (only if needed):
```go
type PortObserver interface {
    RecordPortLifecycle(ctx context.Context, op string, port string, adapter string, generation uint64, err error)
}
```
Type-assertion guard required if introduced.

---

## Unit test plan

| ID | Test | Verifies |
|---|---|---|
| DRB-01 | `BindRef_IOPort_HappyPath` | Initial binding returns ref with generation 1 |
| DRB-02 | `Rebind_IOPort_Success` | New adapter activated, old cancelled, generation increments |
| DRB-03 | `Rebind_IOPort_ActivateFailure_KeepOld` | Old binding remains active; returns `PortRebindError{Phase:"activate_new"}` |
| DRB-04 | `Unbind_IOPort_StaleRef` | Stale generation rejected with `PortBindingStateError` |
| DRB-05 | `Rebind_DuplexPort_ObserverSuccessAndFailure` | Observer fires on success and each failure branch |
| DRB-06 | `Rebind_LatestPort_NoPanicNilObserver` | Nil observer safe |
| DRB-07 | `RebindErrors_AsChain` | `errors.As` reaches inner cause from rebind/unbind errors |
| DRB-08 | `RebindErrors_LogValueShape` | `slog.KindGroup` + required keys for each error type |
| DRB-09 | `ConcurrentRebind_Serialized` | Parallel calls serialize; no generation corruption/data race |
| DRB-10 | `LegacyBind_StillWorks` | Existing `Bind` behavior unchanged (backward compatibility) |

---

## Files to create / update

| File | Responsibility |
|---|---|
| `docs/roadmap/dynamic-port-rebinding.md` | Living design doc (this file) |
| `ports/rebind.go` | Shared binding lifecycle primitives (`BindingRef`, swap state machine, errors) |
| `ports/io.go` | `BindRef`/`Rebind`/`Unbind` for `IOPort` |
| `ports/duplex.go` | `BindRef`/`Rebind`/`Unbind` for `DuplexPort` |
| `ports/latest.go` | `BindRef`/`Rebind`/`Unbind` for `LatestPort` |
| `ports/*_test.go` | Port-kind-specific rebind lifecycle tests |
| `stats/observer_test.go` | Only if new `PortObserver` extension is added |

---

## Migration / compatibility notes

- No breaking change in Phase 1.
- Existing `Bind(ctx, adapter)` APIs stay supported.
- `Bind` maps to current behavior (single static binding) and can internally call `BindRef` with ignored ref.
- `Rebind` is opt-in; applications not needing live swap change nothing.
- Spec generation unaffected (`Pattern` metadata unchanged); this feature is runtime lifecycle only.
- Existing fail-fast/restart operational model remains first-class and
  supported; dynamic rebinding is additive, not a replacement mandate.

---

## Practical implementation phases

1. **Phase 1A — Core lifecycle scaffold**
   - add `BindingRef`, transition lock, generation tracking, typed errors.
2. **Phase 1B — IOPort rollout**
   - implement `BindRef`/`Rebind`/`Unbind` for `IOPort`; full test matrix.
3. **Phase 1C — DuplexPort + LatestPort rollout**
   - same semantics, transport-specific cancellation checks.
4. **Phase 1D — Docs + examples + migration notes**
   - update `docs/features/ports.md` and wiring guide when shipped.
5. **Phase 2 — Source/Sink multi-binding sets**
   - keyed adapters, add/remove/replace by binding key, explicit fan-in/fan-out transition rules.

---

## Open design decisions

1. Should `Rebind` use activate-then-commit (safer uptime) as hard default for all ports, or allow per-port strategy?
2. For `DuplexPort`, what grace period semantics apply to active sessions during swap (drain vs immediate cut)?
3. Should stale-ref `Unbind` be soft-success (idempotent) or hard error (`PortBindingStateError`)?
4. Need new `stats.PortObserver`, or is `RecordRequest` metadata enough long-term?
