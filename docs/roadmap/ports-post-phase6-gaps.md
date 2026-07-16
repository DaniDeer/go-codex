# Ports — Post-Phase-6 Gaps — `ports`, `stream`, `forge`, adapters

> **Status:** Phases A + B + C ✅ **implemented** (G1 `LatestPort`, G2
> `SinkPort.Push`, G3 role-aware `RESTPattern` for ingest/SSE, G5 topology
> port step, G6 `stream.Map`). Phase D (G4 `app.App`) — **design complete,
> not yet implemented** (all open decisions resolved; see the G4 section).
> G7 stays deferred. See [Implementation phases](#implementation-phases).
>
> Implementation deviations from the design: `mcpgo.ToolLatestAdapter` was
> **removed** outright (breaking change approved) rather than deprecated;
> `PortNotStartedError` also covers `Push` on a Feed-driven port; a
> pre-existing data race in the zeromq test mock (`mockSocket`) was fixed
> along the way (mutex + `sentSnapshot()` polling).
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
| G4 | `app.App` lifecycle manager (originally sketched as `forge.App`) | Deferred since Phase 1 (`phase3-forge-app-lifecycle`) | Medium | main.go manages two contexts (pipeline ctx cancelled mid-run, independent `exportCtx`), two done-channels, and a precise close ordering by hand |
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
  was removed in favor of `mcpgo.LatestAdapter` for the new port — no
  ignored pipeline argument. (`ToolLatestHandler`/`RegisterToolLatest`, the
  non-port functions, remain.)

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
| Should G4 (`app.App`) own `Start`/`Close` ordering instead? | **Independent** — `Push` is useful without App; App integration is opt-in later via `OnShutdown(exports.Close)` |

---

## G3 — REST ingest / SSE `Pattern` support

> **Phase C — ✅ implemented (2026-07-16)** exactly as designed below.
> Implementation notes: the C2/C8 end-to-end tests live in
> `adapters/nethttp/binding_test.go` (adapter e2e tests belong to adapter
> packages); the SSE e2e test must pump events in the background before the
> client connects — `SSEHandler` commits headers on the first event; the
> example decision fell to "doc snippets suffice" (sensor-service's ingest
> transport is MQTT; no example currently wires ingest/SSE adapters).

### Problem

`nethttp.IngestAdapter` (POST body → `SourcePort[T]`) and
`nethttp.SSEAdapter` (`SinkPort[Event]` → SSE) are the only stream adapters
whose routes cannot be declared on the port: `RESTPattern{Method, Path, Opts}`
builds a `rest.RouteHandle[Req, Resp]` from the port's **pair** of codecs, but
a single-codec port needs the asymmetric shapes `RouteHandle[T, struct{}]`
(ingest) and `SSERouteHandle[struct{}, Event]` (SSE). Documented as an open
item since Phase 4; Phase 6 did not change the situation.

### Design

Reuse `RESTPattern` (no new pattern kind); the **port type** disambiguates the
shape — exactly the precedent `FilePattern` set (payload codec on `SinkPort`,
response codec on `IOPort`):

- **`SourcePort[T]` + `RESTPattern`** → HTTP **ingest**:
  `rest.NewRoute[T, struct{}](pat.Method, pat.Path, codec,
  codex.Struct[struct{}](), pat.Opts…).Register(b)`. Handle retrievable via
  the **existing** accessor `ports.RESTHandle[T, struct{}]` — no new accessor
  needed (the type parameters express the ingest shape). `Method` is required
  (typically `"POST"`).
- **`SinkPort[T]` + `RESTPattern`** → **SSE**:
  `rest.NewSSERoute[struct{}, T](pat.Path, codex.Struct[struct{}](), codec,
  pat.Opts…).Register(b)`. SSE routes are always GET (`NewSSERoute` hardcodes
  it, Content-Type `text/event-stream` in the spec) — a non-empty
  `pat.Method` other than `"GET"` fails construction with
  `PatternRegisterError` wrapping a descriptive error. New accessor:

  ```go
  // ports/handle.go
  // SSEHandle returns the *rest.SSERouteHandle built from a SinkPort's
  // declared RESTPattern, or (nil, false) if none was declared.
  func SSEHandle[Event any](port any) (*rest.SSERouteHandle[struct{}, Event], bool)
  ```

  A new accessor (rather than reusing `RESTHandle`) because
  `SSERouteHandle` is a distinct type — `RESTHandle`'s type assertion can
  never match it.
- **Why SSE is the only REST shape on `SinkPort`**: the other REST sink,
  `nethttp.DrainCallAdapter[Req, Resp]` (outbound client POST per item),
  needs a full `RouteHandle[Req, Resp]` with an **independent response codec**
  the single-codec port cannot supply — same asymmetric-shape category as
  `file.ReadEachAdapter`'s enrichment type. DrainCall stays handle-first;
  documented, not papered over.
- **Build-function wiring**: `buildEventPatternHandles` (the single-codec
  build fn) gains a `role` parameter distinguishing source/sink (an
  unexported enum, passed by `NewSourcePort`/`NewSinkPort`), because the same
  `RESTPattern` struct builds different handle types per role. Handles/specs
  stored under `patternKindREST`.
- **Spec replay**: `RegisterREST` works for ingest (spec value is
  `rest.Route[T, struct{}]`). SSE needs a matching new replay function:

  ```go
  // ports/spec.go
  // RegisterSSE replays a SinkPort's RESTPattern-declared SSE route against b.
  func RegisterSSE[Event any](b *rest.Builder, port any) error
  ```
- **Adapters unchanged**: `nethttp.IngestAdapter` already accepts
  `*rest.RouteHandle[T, struct{}]` and `nethttp.SSEAdapter` already accepts
  `*rest.SSERouteHandle[struct{}, Event]` — the pattern-derived handles slot
  straight in. Same for `chi.IngestAdapter`/`chi.SSEAdapter` (identical handle
  types). Zero adapter-side changes.
- **OpenAPI**: both register against `PortOptions.RESTBuilder` like every
  other `RESTPattern` — ingest and SSE endpoints appear in the shared spec
  (SSE with `text/event-stream`), closing the last "stream adapters bypass
  the spec" drift.

### Decisions (resolved during Phase C design)

| Question | Resolution |
|---|---|
| Same `RESTPattern` struct or a dedicated `SSEPattern`? | **Same struct** — one pattern kind per protocol family (Phase 6 rule); the port type disambiguates, as `FilePattern` already does. `Method` validation ("" or "GET" on SinkPort) catches the one honest mistake. |
| Response body for ingest | **`struct{}` exactly as today** — `nethttp.IngestAdapter` already wraps `Handler` with a `struct{}` response (200 + empty JSON body; 503 + `PipelineFullError` when the buffer is full). The pattern changes route *declaration*, not response semantics. Receipt types would need a second codec on SourcePort — the asymmetry being avoided. |
| New accessor vs reuse | **`SSEHandle[Event]`** new (distinct handle type); **`RESTHandle[T, struct{}]` reused** for ingest (type params already express it). |
| Replay | **`RegisterSSE[Event](b, port)`** new, mirroring `RegisterREST`; `RegisterREST[T, struct{}]` covers ingest. |
| `RESTPattern` on `LatestPort`? | Already shipped in Phase A (GET + `struct{}` req) — G3 does not touch it. |

### Unit test plan (Phase C)

| ID | Test | Verifies |
|----|------|----------|
| C1 | `TestRESTPattern_SourcePort_BuildsIngestHandle` | `RESTHandle[T, struct{}]` present; Descriptor method/path correct |
| C2 | `TestRESTPattern_SourcePort_IngestEndToEnd` | httptest POST through `IngestAdapter(pattern handle)` → item arrives on `Stream`; bad body → 400 |
| C3 | `TestRESTPattern_SinkPort_BuildsSSEHandle` | `SSEHandle[T]` present; GET + `text/event-stream` in Descriptor |
| C4 | `TestRESTPattern_SinkPort_MethodValidation` | `Method: "POST"` on SinkPort → `PatternRegisterError` |
| C5 | `TestRESTPattern_InSharedSpec_IngestAndSSE` | both routes appear in `RESTBuilder.OpenAPISpec()` |
| C6 | `TestSSEHandle_MissingPattern_ReturnsFalse` | `(nil, false)` cases incl. non-port values |
| C7 | `TestRegisterSSE_ReplaysSpec` | replay against a fresh builder; `MissingPatternError` when absent |
| C8 | `TestRESTPattern_SinkPort_SSEEndToEnd` | httptest GET streams events fed through the port (existing `SSEAdapter` + pattern handle) |

### Files to create / modify (Phase C)

| File | Change |
|---|---|
| `ports/handle.go` | `buildEventPatternHandles` gains role param + `RESTPattern` case (ingest/SSE per role); `SSEHandle[Event]` accessor |
| `ports/source_port.go`, `ports/sink_port.go` | pass role to the build fn |
| `ports/pattern.go` | `RESTPattern` doc: SourcePort/SinkPort semantics |
| `ports/spec.go` | `RegisterSSE[Event]` |
| `ports/port_test.go` | C1–C8 |
| `docs/features/ports.md`, `docs/guides/ports.md` | pattern table rows + ingest/SSE sections; remove the open-item notes |
| `.github/instructions/go-codex.instructions.md` | `ports` row: RESTPattern on SourcePort/SinkPort, SSEHandle, RegisterSSE; drop the open-item sentence |
| `.github/skills/review-go-codex/SKILL.md` | retire the "no RESTPattern support yet" known-fact |
| Example | `examples/sensor-service` gains no new scene (MQTT is its ingest transport); `examples/stream-pipeline` or a nethttp example demonstrates ingest/SSE patterns if one already wires those adapters — otherwise doc snippets suffice (decide at implementation) |

---

## G4 — `app.App` lifecycle manager

> **Phase D design — complete (2026-07-16; revised same day: moved from
> `forge.App` to a NEW top-level package `app`).** All former open decisions
> resolved below. The package imports only `stats` + stdlib — no cycle risk
> anywhere.

### Problem

Deferred since Phase 1 and still unresolved: `main()` owns context trees,
shutdown ordering, and done-channel choreography by hand. The flagship
example needs two independent contexts and four synchronization points for a
five-boundary service; every additional long-lived port multiplies that.

### Design — a shutdown-ordering helper, not a framework

```go
// app/app.go

// Options configures [New].
type Options struct {
    // Observer is injected into App.Context() via stats.WithObserver, so
    // every port/adapter bound with that ctx resolves it automatically.
    // Nil means no injection (ports fall back to NoopObserver as usual).
    Observer stats.Observer
    // Logger receives lifecycle events (goroutine exits, hook results).
    // Nil means slog.Default().
    Logger *slog.Logger
    // ShutdownTimeout bounds the ctx passed to shutdown hooks.
    // Zero means 10 seconds.
    ShutdownTimeout time.Duration
}

func New(opts Options) *App

// Context returns the app's root context: cancelable, observer pre-injected.
// Use it for every Bind/Feed/Start call. It is cancelled when Run begins
// shutdown (signal, supervised-goroutine failure, or parent ctx done).
func (a *App) Context() context.Context

// Go runs fn in a supervised goroutine. A non-nil return CANCELS the app
// (fail-fast, errgroup-style) and becomes part of Run's returned error.
// A nil return just logs completion. name feeds logs, observer events, and
// GoroutineError.
func (a *App) Go(name string, fn func(ctx context.Context) error)

// OnShutdown registers a hook run during shutdown in LIFO order (last
// registered, first run — matching defer semantics: close what you opened
// last, first). Hook errors are collected, logged, and joined into Run's
// returned error — a failing hook never stops later hooks.
func (a *App) OnShutdown(name string, fn func(ctx context.Context) error)

// Run blocks until SIGINT/SIGTERM, parent ctx cancellation, or the first
// supervised-goroutine failure — then cancels App.Context(), waits for all
// Go goroutines, runs the OnShutdown hooks (LIFO, bounded by
// ShutdownTimeout), and returns errors.Join of goroutine + hook errors
// (nil on a clean shutdown).
func (a *App) Run(parent context.Context) error

// Shutdown triggers the same ordered teardown without signal-waiting: cancel,
// wait for goroutines, run hooks. For demos/tests and callers that own their
// own run loop. Idempotent; concurrent calls share one execution.
func (a *App) Shutdown() error
```

- **Ports/adapters do NOT know about App** (zero coupling, no new imports in
  `ports`) — App owns the ctx and runs `exports.Close()`-style hooks; wiring
  stays explicit:

  ```go
  a := app.New(app.Options{Observer: obs, Logger: logger})
  ctx := a.Context()

  exportsPort.Bind(ctx, file.DrainWriteFileAdapter(…))
  exportsPort.Start(ctx)
  a.OnShutdown("exports", func(context.Context) error { return exportsPort.Close() })

  a.Go("mqtt-feed", func(ctx context.Context) error {
      ioports.Alerts.Feed(ctx, res.AlertPayloads) // returns on ctx cancel
      return nil
  })

  return a.Run(context.Background()) // SIGINT/SIGTERM → ordered teardown
  ```

- **`Shutdown()` exists because the flagship demo is not signal-driven** — it
  runs scenes and exits; `Run` would block forever. Demos/tests call
  `app.Shutdown()` where `main()` of a real service calls `app.Run(ctx)`.
- **Signal handling**: `signal.NotifyContext(parent, os.Interrupt,
  syscall.SIGTERM)` inside `Run` only — constructing an App never installs
  signal handlers (test-friendly).

### Structured errors (all implement `slog.LogValuer`)

```go
// app/errors.go

// GoroutineError wraps a supervised goroutine's non-nil return.
type GoroutineError struct {
    Name string // the name passed to App.Go
    Err  error
}
func (e GoroutineError) Error() string  // "app: goroutine %q failed: %v"
func (e GoroutineError) Unwrap() error
func (e GoroutineError) LogValue() slog.Value // group{name, err}

// HookError wraps a shutdown hook's non-nil return (incl. ctx.DeadlineExceeded
// when the hook exceeded ShutdownTimeout).
type HookError struct {
    Name string // the name passed to App.OnShutdown
    Err  error
}
func (e HookError) Error() string  // "app: shutdown hook %q failed: %v"
func (e HookError) Unwrap() error
func (e HookError) LogValue() slog.Value // group{name, err}
```

`Run`/`Shutdown` return `errors.Join(goroutineErrs..., hookErrs...)` —
callers reach individual failures via `errors.As`.

### Observer integration

- `Options.Observer` is stored in `App.Context()` via `stats.WithObserver`
  — the single place the whole service's observer is injected (replaces the
  hand-written `ctx = stats.WithObserver(ctx, obs)` line in main()).
- App itself emits two event families via `Observer.RecordRequest` (plain
  Observer — no type-assertion needed): `("app.go", name, 200|500, duration)`
  when a supervised goroutine exits, and `("app.shutdown", name, 200|500,
  duration)` per hook — mirroring the `"port.bind"` convention. Nil observer
  → `stats.NoopObserver{}`.
- No `TraceObserver` spans in the first cut — app lifecycle is not a
  request-scoped operation (same rationale as pattern construction in
  Phase 4).

### Decisions (resolved during Phase D design)

| Question | Resolution |
|---|---|
| Supervised-goroutine error policy: fail-fast vs collect | **Fail-fast, errgroup-style** — first non-nil `Go` return cancels the app; all errors (goroutines + hooks) are still **collected** into `errors.Join`. A long-running service losing one supervised boundary should shut down in an orderly way, not limp; adapters that should survive errors handle them internally (per-adapter `OnError`) and return nil. |
| Auto-register `SinkPort.Close` when Bind ctx is `App.Context()`? | **No** — explicit `OnShutdown` beats ctx-sniffing magic (confirmed from the G2 design pass). Zero coupling between `ports` and `forge`. |
| Hook order | **LIFO** — matches `defer` semantics; close what you opened last, first. Failing hooks never stop later hooks. |
| Package placement | **New top-level package `app`** (revised after review — the original `forge.App` name was backlog inertia). forge is Layer-2 computation governance (named/versioned functions); App is process lifecycle — a different concern that would muddy forge's identity. A top-level package per concern matches the repo architecture (`codex`/`format`/`forge`/`stream`/`ports`/`stats`); `app` imports only `stats` + stdlib. Not a separate Go module — versioning overhead, no consumer benefit. Not `ports`: App is transport-agnostic lifecycle, not an IO boundary. |
| `Run` vs demo-friendly teardown | Both: `Run(parent)` (signal-driven, blocks) and `Shutdown()` (direct, idempotent). `Run` calls the same shutdown path. |
| Restart policies / health / dependency graphs | Out of scope (unchanged from the sketch) — this is a shutdown-ordering helper. |

### Unit test plan (Phase D)

| ID | Test | Verifies |
|----|------|----------|
| D1 | `TestApp_ShutdownRunsHooksLIFO` | 3 hooks run in reverse registration order |
| D2 | `TestApp_HookErrorDoesNotStopLaterHooks` | failing hook → later hooks still run; error in `errors.Join`, `HookError` via `errors.As` |
| D3 | `TestApp_GoFailureCancelsApp` | `Go` fn returns error → `Context()` cancelled; `Run` returns `GoroutineError` (fail-fast) |
| D4 | `TestApp_RunParentCancelTriggersShutdown` | cancel parent → orderly shutdown, nil error when clean |
| D5 | `TestApp_ShutdownIdempotent` | second `Shutdown` returns the same (memoized) result; concurrent calls share one execution |
| D6 | `TestApp_ShutdownTimeoutBoundsHooks` | slow hook → `HookError` wrapping `context.DeadlineExceeded`; later hooks still run |
| D7 | `TestApp_ContextCarriesObserver` | `stats.ObserverFromContext(app.Context())` returns the injected observer |
| D8 | `TestApp_ObserverEvents` | `app.go` + `app.shutdown` `RecordRequest` events with 200/500 statuses |
| D9 | `TestGoroutineError_LogValue` / `TestHookError_LogValue` | `slog.KindGroup` + name/err keys; `Unwrap` chains |
| D10 | `TestApp_GoAfterShutdown_NoPanic` | `Go`/`OnShutdown` after shutdown are safe no-ops (logged) |

### Files to create / modify (Phase D)

| File | Change |
|---|---|
| `app/app.go` (+ `app/doc.go`) | `App`, `Options`, `New`, `Context`, `Go`, `OnShutdown`, `Run`, `Shutdown` |
| `app/errors.go` | `GoroutineError`, `HookError` |
| `app/app_test.go` | D1–D10 (+ `Example` function for pkg.go.dev) |
| `examples/sensor-service/main.go` | Adopt App: `app.Context()` replaces the hand-built observer ctx; export sink `Close` + HTTP server close as `OnShutdown` hooks; demo calls `app.Shutdown()` |
| new `docs/features/app.md` + wiring note in `docs/guides/ports.md` | Feature docs (own page — new package) + `docs/reference/project-structure.md` row |
| `.github/instructions/go-codex.instructions.md` | NEW `app` package row + import tables |
| `.github/skills/review-go-codex/*` | history entry + known-facts (fail-fast policy, LIFO hooks, no ports coupling) |
| Roadmap | G4 → implemented; this doc likely retires (all gaps closed except deferred G7) |

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
| **C** | G3 (REST ingest / SSE `RESTPattern` support) — ✅ **implemented** | Extends the single-codec build function (the Phase-6 mechanism) to the last two pattern-less adapters: ingest = `RouteHandle[T, struct{}]` on `SourcePort` (existing `RESTHandle` accessor), SSE = `SSERouteHandle[struct{}, T]` on `SinkPort` (new `SSEHandle` accessor + `RegisterSSE` replay); zero adapter-side changes | A ✅ (shipped) |
| **D** | G4 (`app.App`) — **design complete** | Shutdown-ordering helper in a NEW top-level `app` package (stats + stdlib only): `Context()` with injected observer, `Go` supervised goroutines (fail-fast, errgroup-style), `OnShutdown` LIFO hooks, `Run` (signal-driven) + `Shutdown` (direct, idempotent); `GoroutineError`/`HookError`; zero coupling to `ports`/`forge` | A ✅ (G2's `Close` shipped) |
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
| D (G4) | `app/app.go` + `app/errors.go` (design complete — see G4 section) | `App`, `Options`, `New`, `Go`, `OnShutdown`, `Run`, `Shutdown` |
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
2. ✅ G2/G4 boundary: `Push` lifecycle stays port-local; `app.App`
   integration is opt-in later via `OnShutdown(exports.Close)` — no magic
   ctx-based auto-registration.

Remaining open (non-blocking, resolve during the owning phase):

- ✅ G3: resolved in the Phase C design pass (2026-07-16) — SSE handle is
  `SSERouteHandle[struct{}, Event]` via new `SSEHandle` accessor; ingest keeps
  today's `struct{}` response semantics (200 empty body / 503 on full buffer);
  same `RESTPattern` struct, port type disambiguates; `RegisterSSE` replay.
- ✅ G4: resolved in the Phase D design pass (2026-07-16) — **fail-fast**
  (first supervised-goroutine error cancels the app, errgroup-style) with all
  errors still **collected** via `errors.Join`; LIFO hooks that never stop on
  failure; `Run` (signal-driven) + `Shutdown` (direct) share one teardown
  path; `forge` placement confirmed cycle-free.

No open decisions remain — every gap is either implemented (G1/G2/G3/G5/G6),
design-complete (G4), or deferred (G7).
