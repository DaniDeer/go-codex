# Ports — Post-Phase-6 Gaps — `ports`, `stream`, `forge`, adapters

> **Status:** Reviewed and phased — G1/G2 design complete (blocking decisions
> resolved against the existing implementations), G3/G5/G6 sketched, G4 scoped.
> Not yet implemented. See [Implementation phases](#implementation-phases).
> [← Back to Roadmap](index.md)
>
> Successor to the inside-out-pipeline-wiring plan, whose Phases 1–6 all
> **shipped** (the roadmap doc was removed once [Ports feature](../features/ports.md),
> [Wiring guide](../guides/ports.md), the API instructions, and the skills were
> fully in sync with the implementation). Phase 6 (`FilePattern`/`SQLPattern`)
> was the final phase of the `Pattern` approach — this document collects what
> remains *around* it: items the original plan explicitly deferred, plus gaps
> discovered while restructuring `examples/sensor-service` into the flagship
> use case (MQTT → SQL → alert → REST time series → REST-triggered file export).

## Motivation

The ports story is feature-complete for pattern declaration: all four port
types, six `Pattern` kinds, always-`Register` handle construction, and
spec-from-declaration (AsyncAPI + OpenAPI + topology) all work and are
demonstrated end-to-end. But wiring the flagship example surfaced friction
that declaration alone does not solve: cache-style endpoints bypass ports
entirely, request-scoped sink submission needs hand-rolled channel plumbing,
lifecycle management is manual and error-prone, and the topology spec cannot
name a port hop honestly. This document is the single place those gaps are
tracked and designed.

## Gap inventory

| # | Gap | Origin | Priority | Evidence in `examples/sensor-service` |
|---|-----|--------|----------|----------------------------------------|
| G1 | Cache port (`LatestPort[T]`) | Deferred since Phase 1 (`phase3-cacheport-design`), plus `phase3-toolport-optional-pipeline` | **High** | `GET /readings/latest` is the ONLY HTTP endpoint not wired through a port — `nethttp.RegisterLatest(mux, ioports.LatestHandle, res.LatestReadings, …)` takes a raw handle + stream |
| G2 | Request-scoped sink submission (`SinkPort.Push`) | Discovered during export flow | **High** | main.go hand-rolls `exportCh` + a `Feed` goroutine + `close(exportCh)`/`<-exportsDone` shutdown, and hit a real footgun: binding the sink with the pipeline ctx killed the file adapter before the demo reached it |
| G3 | REST ingest (`SourcePort`) / SSE (`SinkPort`) `RESTPattern` support | Documented open item since Phase 4/5 | Medium | Not exercised (MQTT is the ingest transport), but `nethttp.IngestAdapter`/`SSEAdapter` remain the only stream adapters that cannot be declared via a `Pattern` |
| G4 | `forge.App` lifecycle manager | Deferred since Phase 1 (`phase3-forge-app-lifecycle`) | Medium | main.go manages two contexts (pipeline ctx cancelled mid-run, independent `exportCtx`), two done-channels, and a precise close ordering by hand |
| G5 | Topology port-step kind | Discovered during pipeline extraction | Medium | `pipeline.Topology` mis-labels the persistence port hop as `[tap]` (`WithTap("persist via sql/readings/save IOPort …")`) — no honest `StepKind` exists for an IO-port step |
| G6 | `stream.Map[In,Out]` value-transform operator | Discovered during export flow | Low | `ExportSnapshot → ExportResult` needed a full `forge.Function` because no typed 1→1 map with an error path exists (`MapErr` maps *errors*, `FlatMapSlice` has no error path) |
| G7 | Dynamic rebinding (hot-swap adapters) | Deferred since Phase 1 (`phase3-dynamic-rebinding`) | Deferred | No use case has demanded it in six phases — keep deferred |

Priorities reflect how often the flagship example (the reference for "how a
go-codex service should look") is forced to break its own rules today.

---

## G1 — `LatestPort[T]`: the cache port

### Problem

`nethttp.HandlerLatest`/`RegisterLatest`, `zeromq.ServeLatest`, and
`mcpgo.ToolLatestAdapter` all implement the same reactive-cache pattern —
a background goroutine drains a stream into an atomic pointer; requests are
served from the pointer — but each has its own wiring shape, none is a port,
and `mcpgo.ToolLatestAdapter` is a `ports.ToolAdapter` that **ignores the
pipeline function** it is contractually given (the long-standing
`phase3-toolport-optional-pipeline` wart).

### Design sketch

A fifth port type, deliberately shaped like `SinkPort` (stream in) plus a
served read side:

```go
// ports/latest_port.go

// LatestAdapter serves the most recent value of a LatestPort to clients.
// Implemented by transport binding constructors:
//   nethttp.LatestAdapter, zeromq.LatestAdapter, mcpgo.LatestAdapter
type LatestAdapter[T any] interface {
    // Serve registers/starts the transport endpoint. latest returns the most
    // recent value and false while no value has arrived yet.
    Serve(ctx context.Context, latest func() (T, bool)) error
    AdapterName() string
}

func NewLatestPort[T any](name string, codec codex.Codec[T], opts PortOptions) (*LatestPort[T], error)

func (p *LatestPort[T]) Bind(ctx context.Context, a LatestAdapter[T]) error // fan-out: many transports, one cache
func (p *LatestPort[T]) Feed(ctx context.Context, src gstream.Stream[T])    // drains src into the atomic cell
func (p *LatestPort[T]) Latest() (T, bool)                                  // programmatic read side
```

- `Patterns` support — one per serving transport family, mirroring `ToolPort`:
  `RESTPattern` (GET route, `Resp = T`, `Req = struct{}`), **`ReqReplyPattern`**
  (zeromq `ServeLatest` serves through a `reqreply.RouteHandle[Req, Resp]` —
  review finding: the original sketch omitted this), and `MCPPattern`. All
  three reuse the dual-codec build function with
  `reqCodec = codex.Struct[struct{}]()` and `respCodec` = the port's codec.
- Existing `HandlerLatest`/`ServeLatest`/`ToolLatestAdapter` become the
  internals of the new `LatestAdapter` constructors; the old exported
  functions stay (non-stream surface stays supported, as with `Subscribe`/
  `Publish`).
- **Verified against all three implementations** (`nethttp.HandlerLatest`,
  `zeromq.ServeLatest`, `mcpgo.ToolLatestAdapter`/`RegisterToolLatest`): each
  embeds its own `atomic.Pointer[Resp]` plus an identical drain goroutine
  (values stored, src errors dropped). Centralizing the cell in `LatestPort`
  and handing adapters `latest func() (T, bool)` removes that triplication —
  the request-side closures adapt mechanically (`ptr == nil` ⇔ `!ok`).
- **`Serve` lifetime semantics differ per transport** — nethttp/mcpgo register
  on a mux/server and return immediately; zeromq's REP loop blocks until ctx
  cancel. `Bind` therefore runs `Serve` in a supervised goroutine wrapped in
  `bindWithObserver` (exactly how `SourcePort.Bind` runs `Activate`) — both
  shapes are correct under the same contract: "Serve runs the endpoint; it
  MAY return immediately after registration or block until ctx is done."
- **Resolves `phase3-toolport-optional-pipeline`**: `mcpgo.ToolLatestAdapter`
  is deprecated in favor of `mcpgo.LatestAdapter` for the new port — no
  ignored pipeline argument.

### Sensor-service after

```go
// ioports — declared like every other boundary
var Latest = codex.Must(ports.NewLatestPort[db.Reading]("rest/latest", domain.ReadingCodec,
    ports.PortOptions{Patterns: []ports.Pattern{
        ports.RESTPattern{Method: "GET", Path: "/readings/latest", Opts: …},
    }, RESTBuilder: RESTBuilder}))

// main — wiring only
must(ioports.Latest.Bind(ctx, nethttp.LatestAdapter(mux, latestHandle, nethttp.Options{})))
go ioports.Latest.Feed(ctx, res.LatestReadings)
```

Every HTTP endpoint is then port-declared — the README's "every hop is a
port" claim becomes unconditionally true.

### Decisions (resolved during review)

| Question | Resolution |
|---|---|
| `Serve(ctx, latest func() (T, bool))` vs handing the adapter the atomic cell | **Function** — verified compatible with all three existing implementations; keeps the cell unexported and the adapter contract minimal |
| Does `Feed` return when src terminates (SinkPort semantics) or keep serving? | **Keep serving** — the cache outlives the stream by design (sensor-service serves 87.3 °C after pipeline cancel today); `Feed` returns when src terminates, adapters keep answering from the cell |
| Empty-cache behavior per transport | **Keep today's per-adapter semantics** (HTTP 503 + `NoLatestValueError`, zeromq error reply + `NoLatestValueError`, MCP error result) — documented, not unified |
| Which `Pattern` kinds? | **REST + ReqReply + MCP** (all three serving transports have a latest implementation today); `EventPattern` is not a request/response shape — excluded |

---

## G2 — `SinkPort.Push`: request-scoped submission

### Problem

`SinkPort` is stream-fed (`Feed(ctx, src)` drains a whole stream, then closes
all adapter channels). A request/response pipeline that wants to *also* drop
an item into a sink (the export flow) must build the plumbing by hand:

```go
// today, in sensor-service main.go — all of this is boilerplate:
exportCtx := stats.WithObserver(context.Background(), obs) // MUST NOT be the pipeline ctx (†)
exportsPort.Bind(exportCtx, fileadapter.DrainWriteFileAdapter(…))
exportCh := make(chan domain.ExportSnapshot, 4)
exportsDone := make(chan struct{})
go func() { defer close(exportsDone); exportsPort.Feed(exportCtx, gstream.From(exportCtx, exportCh)) }()
// … and at shutdown:
close(exportCh); <-exportsDone
```

(†) is a real footgun hit during implementation: the adapter's `Activate` ctx
is the **Bind** ctx, so binding with the (later-cancelled) pipeline ctx
silently killed the file adapter before the first export.

### Design sketch

Port-owned feed channel with an explicit lifecycle:

```go
// Push submits one item to all bound adapters. Returns PortNotStartedError
// before Start / after Close, or ctx.Err() when blocked and ctx is cancelled.
func (p *SinkPort[T]) Push(ctx context.Context, v T) error

// Start begins draining the port-owned channel into the bound adapters —
// the owned-channel equivalent of go Feed(ctx, src). Close stops it and
// waits for adapters to drain.
func (p *SinkPort[T]) Start(ctx context.Context)
func (p *SinkPort[T]) Close() error
```

- `Feed` (stream-fed, one-shot) and `Start`/`Push`/`Close` (request-fed,
  long-lived) are mutually exclusive per port — mixing returns a structured
  error.
- Structured error: `PortNotStartedError{Port string}` implementing
  `slog.LogValuer`.
- The pipeline side then shrinks to `submit := func(s ExportSnapshot) { _ = exports.Push(ctx, s) }`
  — no channel, no goroutine, no done-channel in user code.

### Decisions (resolved during review)

| Question | Resolution |
|---|---|
| `Push` blocking vs best-effort with buffer overflow error | **Blocking with ctx** — honors backpressure, consistent with `Feed`'s Drain semantics; `PortOptions.Buffer` gives headroom |
| Should G4 (`forge.App`) own `Start`/`Close` ordering instead? | **Independent** — `Push` is useful without App; App integration is opt-in later via `OnShutdown(exports.Close)` |

---

## G3 — REST ingest / SSE `Pattern` support

### Problem

`nethttp.IngestAdapter` (POST body → `SourcePort[T]`) and
`nethttp.SSEAdapter` (`SinkPort[Event]` → SSE) are the only stream adapters
whose routes cannot be declared on the port: `RESTPattern{Method, Path, Opts}`
builds a `rest.RouteHandle[Req, Resp]` from the port's **pair** of codecs, but
a single-codec port needs the asymmetric shapes `RouteHandle[T, struct{}]`
(ingest) and SSE's event-typed handle. Documented as an open item since
Phase 4; Phase 6 did not change the situation.

### Design sketch

Reuse `RESTPattern` (no new pattern kind) and let the **single-codec** build
function handle it, mirroring how Phase 6 made `buildEventPatternHandles`
multi-kind:

- On `SourcePort[T]`: `RESTPattern` builds `rest.NewRoute[T, struct{}](method,
  path, codec, codex.Struct[struct{}](), opts…).Register(b)` → handle
  retrievable via `ports.RESTHandle[T, struct{}]`. `IngestAdapter` gains a
  constructor accepting that shape.
- On `SinkPort[T]`: builds the SSE route shape (`rest.SSERoute`/
  `SSERouteHandle` — exact type per current `adapters/nethttp` SSE API) from
  the port's codec.
- OpenAPI: both register against `PortOptions.RESTBuilder` like every other
  `RESTPattern` — closing the "classic routes bypass the spec" class of
  drift for ingest/SSE too.

### Open decisions

| Question | Trade-off |
|---|---|
| Same `RESTPattern` struct or a dedicated `SSEPattern`? | Same struct is consistent with Phase 6's "one pattern kind per protocol family"; the port type disambiguates the shape (as `FilePattern` already does: payload codec on SinkPort, resp codec on IOPort) |
| Response body for ingest (`struct{}` vs 202 + receipt type) | `struct{}` (204/202, empty body) for Phase 1 — receipt types would need a second codec on SourcePort, which is exactly the asymmetry being avoided |

---

## G4 — `forge.App` lifecycle manager

### Problem

Deferred since Phase 1 and still unresolved: `main()` owns context trees,
shutdown ordering, and done-channel choreography by hand. The flagship
example needs two independent contexts and four synchronization points for a
five-boundary service; every additional long-lived port multiplies that.

### Design sketch (minimal viable scope)

Not a framework — a shutdown-ordering helper:

```go
// forge/app.go
type App struct { … }

func NewApp(opts AppOptions) *App                      // AppOptions{Observer, Logger}
func (a *App) Context() context.Context               // root ctx, observer pre-injected
func (a *App) Go(name string, run func(ctx context.Context) error) // supervised goroutine
func (a *App) OnShutdown(name string, fn func(ctx context.Context) error) // LIFO hooks
func (a *App) Run(ctx context.Context) error           // blocks; SIGINT/SIGTERM → ordered shutdown
```

- Ports/adapters do **not** know about App (no coupling) — App just owns the
  ctx and runs the `close(exportCh)`-style hooks in LIFO order.
- Explicitly out of scope for the first cut: dependency graphs between ports,
  health checks, restart policies.

### Open decision

Whether G2's `SinkPort.Close` registration should be automatic when the Bind
ctx is `App.Context()` — leaning **no** (explicit `OnShutdown` beats magic).

---

## G5 — Topology port-step kind

### Problem

`stream.Topology` steps are operator-shaped (`source`, `apply`, `filter`,
`tap`, `sink`, …). A port hop — persistence or enrichment through an
`IOPort` — has no honest kind, so the flagship example currently documents
its persist step as `[tap]`, which it is not.

### Design sketch

```go
// stream/topology.go
const StepKindPort StepKind = "port" // an IO hop through a ports.IOPort/SinkPort/SourcePort

// WithPort records an IO-port step. Name is the port name (e.g.
// "sql/readings/save"); description explains the hop.
func (t *Topology) WithPort(name, description string) *Topology
```

- Render layers (`render/stream`) pick the new kind up automatically if they
  switch on `Kind` generically; verify and add the YAML fixture.
- Follow-up idea (not this phase): derive the step from the port value
  itself (`stream.WithIOPort(topo, port)` capturing `port.Name()`), keeping
  topology and wiring from drifting.

---

## G6 — `stream.Map` (typed 1→1 transform with error path)

`func Map[In, Out any](ctx, src Stream[In], fn func(In) (Out, error)) Stream[Out]` —
errors go to `Stream.Errors` (as `StreamApplyError` or a lighter
`StreamMapError`). Today the choices are `FlatMapSlice` (no error path) or a
full `forge.Function` + `Apply` (governance ceremony for a trivial mapping —
right for `buildExportResult`, wrong as the only option). Small, orthogonal,
`stream` package only.

---

## Structured errors (all implement `slog.LogValuer`)

**G1 (`LatestPort`) introduces no new error types.** Construction reuses
`PatternRegisterError{Port, Kind, Err}`; `Bind` reuses `PortBindError{Port,
Adapter, Err}`; empty-cache conditions keep each adapter's existing
`NoLatestValueError` (nethttp and zeromq each already define one, with
`LogValue`).

**G2 adds one:**

```go
// ports/port_errors.go
// PortNotStartedError is returned by SinkPort.Push before Start or after
// Close, and by Start/Push when the port is already Feed-driven (the two
// feed modes are mutually exclusive).
type PortNotStartedError struct {
    Port string // port name
    Op   string // "push", "start" — which call was rejected
}

func (e PortNotStartedError) Error() string {
    return fmt.Sprintf("ports: %s: %s rejected: port not started (call Start before Push, and not after Close or Feed)", e.Port, e.Op)
}

// LogValue implements [slog.LogValuer].
func (e PortNotStartedError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("port", e.Port),
        slog.String("op", e.Op),
    )
}
```

No `Unwrap` — there is no inner error. Tests must assert `slog.KindGroup` and
both keys (see `TestValidate_LogValue` reference pattern).

## Observer integration

- **`LatestPort.Bind`** wraps the supervised `Serve` goroutine in
  `bindWithObserver` — the same `"port.bind"` `RecordRequest` (+
  `TraceObserver` span) every other port emits. Observer resolution:
  `PortOptions.Observer`, else `stats.ObserverFromContext(ctx)` at Bind time.
- **Request-side observers stay per-adapter** — `nethttp.Options.Observer`,
  `zeromq.ServeLatestOptions.Observer`, `mcpgo.Options` already report
  per-request events; `LatestPort` adds nothing on that path (ports never
  intercept per-item adapter traffic — consistent with all existing ports).
- **`LatestPort.Feed`** drains values into the cell without per-item observer
  calls (matches today's `HandlerLatest` drain goroutine, which reports
  nothing per item); src errors are dropped, as today — documented.
- **`SinkPort.Push`/`Start`** reuse the existing `Feed`/`gstream.Drain`
  observer path unchanged — `Start` internally feeds the port-owned channel
  through the same code that `Feed` uses, so per-item `RecordStreamItem`
  behavior is identical in both modes.

## Implementation phases

| Phase | Gaps | Rationale | Depends on |
|-------|------|-----------|------------|
| **A** | G2 (`SinkPort.Push`/`Start`/`Close`), then G1 (`LatestPort`) | The two High gaps. G2 first: small, `ports`-only, no new port type, and immediately deletes the sensor-service export boilerplate. G1 second: new port type + 3 adapter constructors + example rewiring that touches the same main.go region G2 just simplified | — |
| **B** | G5 (topology `StepKindPort` + `WithPort`), G6 (`stream.Map`) | Small, independent `stream`-package items with no port coupling; each fixes a concrete sensor-service dishonesty (`[tap]` mislabel; forge-fn ceremony for a trivial map) | — (parallel to A) |
| **C** | G3 (REST ingest / SSE `RESTPattern` support) | Extends the single-codec build function (the Phase-6 mechanism) to the last two pattern-less adapters; SSE needs the `SSERouteHandle[struct{}, Event]` shape on `SinkPort` | A (shares `ports/handle.go` surface; avoid parallel edits) |
| **D** | G4 (`forge.App`) | Needs its own roadmap-level design pass before code (signal handling, supervised-goroutine error policy); G2's `Close` must exist so App has something to order | A (G2) |
| — | G7 (dynamic rebinding) | Stays deferred — no demand after six phases | n/a |

Each phase follows the standard ship checklist: tests per the plan below,
`docs/features/ports.md` + `docs/guides/ports.md` + instructions `ports` row
sync, example update, full verification (`gofmt`, `go build/test ./...`,
`just check`, all examples).

## Unit test plan (for the two High-priority gaps)

| ID | Test | Verifies |
|----|------|----------|
| L1 | `TestLatestPort_ServesLatestValue` | Feed 2 values → `Latest()` and a bound adapter both see the 2nd |
| L2 | `TestLatestPort_EmptyBeforeFirstValue` | `Latest()` returns `(zero, false)`; HTTP adapter → 503 + `NoLatestValueError` |
| L3 | `TestLatestPort_SurvivesStreamTermination` | src closes → adapter still serves last value |
| L4 | `TestLatestPort_RESTPattern_InSpec` | `RESTPattern` + shared builder → route in OpenAPI spec |
| L5 | `TestLatestPort_FanOut` | two bound adapters, one cache |
| L6 | `TestZeromqLatestAdapter_ServesLatest` | zeromq `LatestAdapter` (ReqReply-shaped) answers with the cell value; empty cell → error reply + `NoLatestValueError` |
| L7 | `TestMCPLatestAdapter_ServesLatest` | mcpgo `LatestAdapter` serves the cell; deprecated `ToolLatestAdapter` still passes its existing tests unchanged |
| P1 | `TestSinkPortPush_DeliversToAdapters` | `Start` → `Push` × n → adapter receives all, order preserved |
| P2 | `TestSinkPortPush_BeforeStart_Error` | `PortNotStartedError` (+ `LogValue` group/keys) |
| P3 | `TestSinkPortPush_AfterClose_Error` | same error after `Close`; `Close` waits for drain |
| P4 | `TestSinkPort_FeedAndPush_MutuallyExclusive` | structured error on mixing |
| P5 | `TestSinkPortPush_CtxCancelUnblocks` | blocked `Push` returns `ctx.Err()` |
| P6 | `TestSinkPortPush_ConcurrentSafe` | parallel `Push` from N goroutines under `-race` — no lost items, no panic |
| P7 | `TestSinkPortPush_FanOut` | `Push` reaches ALL bound adapters (broadcast parity with `Feed`) |

## Files to create / modify

| Phase | File | Change |
|-------|------|--------|
| A (G2) | `ports/sink_port.go` + `ports/port_errors.go` (+ tests) | `Push`/`Start`/`Close`, `PortNotStartedError` (Error/LogValue, no Unwrap) |
| A (G2) | `examples/sensor-service/main.go` | Export flow via `Start`/`Push`/`Close` — deletes `exportCh` + goroutine + done-channel plumbing |
| A (G1) | `ports/latest_port.go` (+`_test.go`) | `LatestPort`, `LatestAdapter`, dual-codec pattern build reuse (REST/ReqReply/MCP with `struct{}` req codec), supervised `Serve` via `bindWithObserver` |
| A (G1) | `adapters/nethttp/binding.go` | `LatestAdapter` constructor wrapping `HandlerLatest` internals (cell moves to port) |
| A (G1) | `adapters/zeromq/binding.go` (+ test), `adapters/mcpgo/binding.go` (+ test) | `LatestAdapter` constructors; deprecate `mcpgo.ToolLatestAdapter` |
| A (G1) | `examples/sensor-service` | `ioports.Latest` port declaration; README "every hop is a port" becomes unconditional |
| B (G5) | `stream/topology.go` (+ test, + `render/stream` fixture) | `StepKindPort`, `WithPort(name, description)` |
| B (G6) | `stream/transform.go` (+ tests) | `Map[In, Out]` with error path |
| B | `examples/sensor-service/pipeline` | `Topology` uses `WithPort`; export result mapping optionally via `stream.Map` (keep the forge fn — it demonstrates governance — but reference `Map` in docs) |
| C (G3) | `ports/handle.go`, `adapters/nethttp/binding.go` | Single-codec build fn gains `RESTPattern`; ingest/SSE adapter constructors accept pattern-derived handles |
| D (G4) | `forge/app.go` (+ own design pass first) | `App`, `AppOptions`, `Go`, `OnShutdown`, `Run` |
| all | Docs + instructions | `features/ports.md`, `guides/ports.md`, instructions `ports` row, review-skill known-facts — per maintenance rules, every phase |

## Out of scope

- **G7 dynamic rebinding** — stays deferred; no demand after six phases.
- **`FilePattern`/`SQLPattern` extensions** — Phase 6 is final; custom file
  formats stay handle-first, SQL stays metadata-only by design.
- **Cross-protocol unified spec format** — OpenAPI/AsyncAPI/topology remain
  separate outputs by design.

## Open design decisions (summary)

Both former blockers are **resolved** (review pass, 2026-07-16):

1. ✅ G1 adapter contract: `Serve(ctx, latest func() (T, bool))` — confirmed
   against all three existing latest-implementations (each embeds the same
   atomic-pointer cell + drain goroutine; the request-side closures adapt
   mechanically). Additional finding folded in: zeromq requires
   `ReqReplyPattern` support, and `Bind` must run `Serve` in a supervised
   goroutine because lifetimes differ per transport.
2. ✅ G2/G4 boundary: `Push` lifecycle stays port-local; `forge.App`
   integration is opt-in later via `OnShutdown(exports.Close)` — no magic
   ctx-based auto-registration.

Remaining open (non-blocking, resolve during the owning phase):

- G3: exact SSE handle shape on `SinkPort` (`SSERouteHandle[struct{}, Event]`)
  and whether ingest responds 202 or 204 — Phase C.
- G4: supervised-goroutine error policy (fail-fast vs collect) — Phase D
  design pass.
