# Inside-Out Pipeline Wiring — Protocol-Agnostic IO Ports

> **Status:** ✅ Phase 1 + Phase 2 complete. `ports` package (SourcePort, SinkPort, IOPort, ToolPort) + all transport bindings (mqtt, mqtt5, nethttp, chi, zeromq, file, sql, mcpgo). Stream bridge helpers fully removed.
> [← Back to Roadmap](index.md)

---

## Motivation

go-codex is designed for inside-out development: start from domain types, define
business logic, then connect to the outside world. In practice there is structural
friction: **every stream bridge helper requires a protocol-specific handle to exist
before a single line of pipeline code can be written.**

```go
// Today — transport decision contaminates pipeline code:
b := events.NewBuilder(events.Info{...})
sensorHandle, _ := events.NewChannel[SensorReading](
    "sensors/{sensorID}/data", readingCodec,
    events.Subscribe{Summary: "Sensor reading"},
    events.TopicParam{Name: "sensorID", Description: "Sensor ID"}.WithCodec(sensorIDCodec),
).Register(b)  // ← must exist before any pipeline code

sensorStream := mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, ...) // via SourcePort.Bind
oeeStream    := gstream.Apply(ctx, sensorStream, oeeCalcFn, opts)
// ↑ pipeline code — but it already carries a hard MQTT5 dependency
```

This forces a top-down workflow: API contract → pipeline → adapters. It makes
swapping MQTT for HTTP require changes in pipeline code, and prevents writing
pipelines that are truly transport-agnostic.

**go-codex's inside-out vision requires the inverse order:**

```
1. Domain core       codex.Codec[T], forge.Function[In,Out]
2. Pipeline logic    forge.SourcePort, forge.SinkPort, forge.IOPort
3. Wiring (main.go)  bind ports to concrete adapters
```

Ports carry the full schema (payload codec, IO parameters with codecs, formats).
Pipeline code carries **zero adapter imports**. The protocol decision lives entirely
in `main.go`.

---

## Design vision

```go
// ── domain/codecs.go — Layer 1, zero transport imports ──────────────────────

var ReadingCodec  = codex.Struct[SensorReading](...)
var OEECodec      = codex.Float64(zeroToOne()).WithTitle("oee")
var SensorIDCodec = codex.String(validate.UUID())

// ── domain/functions.go ──────────────────────────────────────────────────────

var OEECalc = forge.NewFunction("oeeCalc", "1.0.0", oeeInCodec, OEECodec, ...)

// ── domain/pipeline.go — no adapter/transport imports ───────────────────────

// IO enforcement points — declared with full schema; protocol decided at wiring time.
var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Params: []ports.IOParam{{Name: "sensorID", Description: "Sensor identifier", Required: true}.WithCodec(SensorIDCodec)},
    })

var Calibration = ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec,
    ports.PortOptions{
        Params: []ports.IOParam{{Name: "sensorID"}.WithCodec(SensorIDCodec)},
    })

var OEEResults = ports.NewSinkPort[OEE]("oee-results", OEECodec,
    ports.PortOptions{
        Params: []ports.IOParam{{Name: "machineID"}.WithCodec(machineIDCodec)},
    })

func StartOEEPipeline(ctx context.Context) {
    raw         := SensorReadings.Stream(ctx)
    calibrated  := Calibration.Connect(ctx, raw)
    oeeStream   := gstream.Apply(ctx, calibrated, OEECalc, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// ── main.go — wiring layer, all protocol decisions here ─────────────────────

func main() {
    ctx := context.Background()

    // SensorReadings: accept from MQTT5 AND HTTP ingest (fan-in)
    domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(
        client, router, sensorHandle, 0,
        format.JSON(domain.ReadingCodec),
        mqtt5.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"},
    ))
    domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(
        mux, ingestHandle, nethttp.IngestAdapterOptions{},
    ))

    // Calibration: HTTP enrichment call (swap for SQL with one line change)
    domain.Calibration.Bind(ctx, nethttp.CallAdapter(
        httpClient, "http://calibration-svc:8080",
        calibrationRouteHandle,
        nethttp.CallStreamOptions{},
    ))
    // OR: domain.Calibration.Bind(ctx, sql.QueryEachAdapter(...))
    // OR: domain.Calibration.Bind(ctx, file.ReadEachAdapter(...))

    // OEEResults: publish to MQTT5 AND SSE clients (fan-out)
    domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(
        client, alertHandle,
        format.JSON(domain.OEECodec),
        mqtt5.MQTT5DrainPublishOptions{},
    ))
    domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(
        mux, sseHandle, nethttp.SSEAdapterOptions{},
    ))

    domain.StartOEEPipeline(ctx)
    // ...
}
```

**What this achieves:**
- `domain/pipeline.go` has zero imports from `adapters/*`
- Swapping MQTT for ZeroMQ only touches `main.go`
- Swapping SQL for HTTP enrichment only touches `main.go`
- Adding a second source (HTTP ingest) is one `Bind` call
- Adding a second sink (SSE + MQTT publish) is one `Bind` call
- Testing the pipeline uses `ports.ChanSourceAdapter` / `ports.ChanSinkAdapter` — no broker

---

## Scope decisions

| In scope — Phase 1 | Out of scope (deferred) |
|--------------------|------------------------|
| `ports.IOParam` — protocol-agnostic parameter with codec | `forge.App` lifecycle manager |
| `ports.SourcePort[T]` — inbound boundary | Cache ports (`HandlerLatest`, `ServeLatest`) — different pattern |
| `ports.SinkPort[T]` — outbound boundary | `AsPipelineFunc` adapter bindings — non-stream server pattern |
| `ports.IOPort[Req, Resp]` — intermediate transform | `adapters/chi` bindings — same API as nethttp, add after |
| `ports.SourceAdapter[T]`, `SinkAdapter[T]`, `IOAdapter[Req,Resp]` interfaces | `adapters/mcpgo` bindings — request-response, different pattern |
| Fan-in (multiple SourceAdapters → merged stream) | Dynamic rebinding at runtime |
| Fan-out (SinkPort broadcasts to all SinkAdapters) | Auto OpenAPI/AsyncAPI spec from ports alone |
| `ports.ChanSourceAdapter[T]`, `ports.ChanSinkAdapter[T]`, `ports.FuncIOAdapter[Req,Resp]` — test helpers | IOParam ↔ adapter template cross-validation (advisory only in Phase 1) |
| `adapters/mqtt5/binding.go` — SubscribeAdapter, PublishAdapter, CallAdapter | — |
| `adapters/mqtt/binding.go` — SubscribeAdapter, PublishAdapter | — |
| `adapters/nethttp/binding.go` — IngestAdapter, SSEAdapter, PollAdapter, CallAdapter, DrainCallAdapter | — |
| `adapters/zeromq/binding.go` — SubscribeAdapter, PublishAdapter, CallAdapter | — |
| `adapters/file/binding.go` — ScanAdapter, WatchAdapter, ReadEachAdapter, DrainWriteAdapter, DrainWriteFileAdapter | — |
| `adapters/sql/binding.go` — QueryAdapter, QueryEachAdapter, DrainInsertAdapter | — |
| **Deprecate** all stream bridge helpers (`SubscribeStream`, `DrainPublish`, `HandlerIngest`, `ScanStream`, etc.) | — |
| **Keep** all non-stream adapter functions (`Subscribe`, `Publish`, `Call`, `Serve`, `Handler`, etc.) | — |

---

## `forge.IOParam` — the protocol-agnostic parameter

Every port carries a set of `IOParam` values. They serve two purposes:

1. **Schema documentation** — appear in AsyncAPI/OpenAPI spec as path params, topic
   vars, query params, user properties, etc. — depending on the bound adapter.
2. **Runtime enforcement** — the adapter uses the IOParam's codec to validate the
   incoming value before it enters the pipeline (or before routing outbound items).

`IOParam` maps to protocol-specific params at adapter binding time:

| IOParam use | REST (HTTP) | Events (MQTT/ZeroMQ) | MQTT5 extra | File | SQL |
|-------------|-------------|---------------------|-------------|------|-----|
| Routing var | PathParam `{name}` | TopicParam `{name}` | — | FilePathParam `{name}` | query bind var |
| Metadata | HeaderParam, QueryParam | — | UserPropertyParam | — | — |
| Cookie | CookieParam | — | — | — | — |

```go
// forge/io_param.go

// IOParam is a protocol-agnostic parameter declaration for a port.
// At adapter binding time, each adapter maps it to its protocol-specific
// parameter type (PathParam, TopicParam, FilePathParam, UserPropertyParam, etc.)
// using the IOParam.Name as the key.
//
// IOParam mirrors the .WithCodec(c) pattern of rest.PathParam, events.TopicParam, etc.
type IOParam struct {
    // Name is the parameter key. Must match the {name} placeholder in the
    // adapter's topic/path template (e.g. {sensorID} requires Name: "sensorID").
    Name        string
    // Description documents the parameter for spec generation.
    Description string
    // Codec, when non-nil, validates the parameter value at runtime.
    // Use codex.String(validate.UUID()) for UUID params, etc.
    Codec       *codex.Codec[string]
    // Required, when true, causes the adapter to reject messages/requests
    // where this parameter is absent.
    Required    bool
}

// WithCodec returns a copy of IOParam with Codec set. Avoids a temporary variable:
//
//    forge.IOParam{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)
func (p IOParam) WithCodec(c codex.Codec[string]) IOParam { p.Codec = &c; return p }

// Params is a PortOpt that adds IO parameters to a port.
func Params(params ...IOParam) PortOpt { return portParams(params) }
```

---

## API surface — `ports` package (as implemented)

### Port options

> **Implementation note:** The final implementation uses a plain struct `PortOptions` instead of the planned sealed `PortOpt` interface — simpler and zero boilerplate.

```go
// PortOptions configures a port constructor (NewSourcePort, NewSinkPort, NewIOPort, NewToolPort).
type PortOptions struct {
    // Params declares the protocol-agnostic IO parameters for this port.
    // Propagated to bound adapters via ParamsFromContext(ctx). Adapters with no
    // protocol-level param builder of their own (file.ReadEachAdapter,
    // file.DrainWriteFileAdapter) call ValidateParams for real runtime
    // enforcement. Adapters backed by rest.Route / events.ChannelHandle /
    // mqtt5.UserPropertyParam already validate their own declarations and do
    // not consult Params.
    Params []IOParam
    // Buffer sets the internal channel buffer size. Default 0.
    // Only SourcePort and SinkPort honor Buffer — IOPort and ToolPort have no
    // internal channel (Connect/Bind delegate directly to the adapter's
    // Transform/Bind call) and ignore this field.
    Buffer int
    // Observer receives port lifecycle events: every Bind call wraps its
    // adapter's Activate/Transform/Bind with a "port.bind" RecordRequest call
    // (and TraceObserver span, when supported). Resolved from ctx when nil.
    Observer stats.Observer
}
```

### SourcePort[T]

```go
// SourcePort[T] is a typed, protocol-agnostic inbound IO enforcement point.
// It represents an external → pipeline boundary.
//
// Declare in domain/pipeline code. Bind one or more SourceAdapters in main.go.
// Multiple adapters produce a merged (fan-in) stream.
//
// A SourcePort carries:
//   - Payload codec: validates every item before it enters the pipeline
//   - IOParams: routing/metadata params (sensorID, machineID, …)
//
// Bind all adapters before calling Stream.
type SourcePort[T any] struct { /* unexported */ }

// NewSourcePort creates a SourcePort with the given name and payload codec.
// name is used for observability, error context, and spec generation.
func NewSourcePort[T any](name string, codec codex.Codec[T], opts PortOptions) *SourcePort[T]

// Bind activates a SourceAdapter, merging its output into this port's stream.
// Multiple Bind calls produce fan-in: items from all adapters are merged.
// Bind must be called before Stream.
func (p *SourcePort[T]) Bind(ctx context.Context, a SourceAdapter[T])

// Stream returns the merged gstream.Stream[T] from all bound adapters.
// Items are validated by the port's payload codec before entering the stream.
// Validation failures and adapter errors are routed to Stream.Errors.
// The stream terminates when ctx is cancelled.
func (p *SourcePort[T]) Stream(ctx context.Context) gstream.Stream[T]
```

### SinkPort[T]

```go
// SinkPort[T] is a typed, protocol-agnostic outbound IO enforcement point.
// It represents a pipeline → external boundary.
//
// Multiple adapters produce fan-out: each item is broadcast to all bound adapters.
type SinkPort[T any] struct { /* unexported */ }

// NewSinkPort creates a SinkPort with the given name and payload codec.
func NewSinkPort[T any](name string, codec codex.Codec[T], opts PortOptions) *SinkPort[T]

// Bind registers a SinkAdapter to receive items from this port.
// Multiple Bind calls produce fan-out.
func (p *SinkPort[T]) Bind(ctx context.Context, a SinkAdapter[T])

// Feed connects an upstream gstream.Stream[T] to this port, broadcasting each
// item to all bound adapters. Blocks until src terminates or ctx is cancelled.
// Call in a goroutine when the pipeline continues concurrently.
func (p *SinkPort[T]) Feed(ctx context.Context, src gstream.Stream[T])
```

### IOPort[Req, Resp]

```go
// IOPort[Req, Resp] is a typed, protocol-agnostic intermediate IO enforcement point.
// It transforms each upstream Req into a downstream Resp through a bound IOAdapter.
//
// Examples of what binds to an IOPort:
//   - nethttp.CallAdapter    — HTTP enrichment call per item
//   - mqtt5.CallAdapter      — MQTT5 request-reply per item
//   - sql.QueryEachAdapter   — per-item parameterized SQL lookup
//   - file.ReadEachAdapter   — per-item file read with path vars
//   - zeromq.CallAdapter     — ZeroMQ request-reply per item
//
// Exactly one IOAdapter must be bound before Connect is called.
// Swapping the adapter (MQTT → SQL → file) only changes the Bind call in main.go;
// the pipeline code is identical in all cases.
//
// IOPort carries:
//   - Req codec: validates each upstream item before it is sent outbound
//   - Resp codec: validates each inbound response before it enters the pipeline
//   - IOParams: parameters derived from Req for routing (e.g. {sensorID} in path)
type IOPort[Req, Resp any] struct { /* unexported */ }

// NewIOPort creates an IOPort with the given name, request codec, and response codec.
func NewIOPort[Req, Resp any](
    name      string,
    reqCodec  codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts      PortOptions,
) *IOPort[Req, Resp]

// Bind sets the IOAdapter for this port. Returns PortBindError if called
// more than once or if activation fails.
func (p *IOPort[Req, Resp]) Bind(ctx context.Context, a IOAdapter[Req, Resp]) error

// Connect transforms each item from src through the bound adapter and returns
// the result stream. Returns an error stream if no adapter is bound.
// All codec validation runs per item. Errors go to Stream.Errors.
func (p *IOPort[Req, Resp]) Connect(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp]
```

### Adapter interfaces

```go
// SourceAdapter[T] produces items for a SourcePort. Implemented by each transport
// package's binding constructor. Do not implement directly — use the constructors.
type SourceAdapter[T any] interface {
    // Activate runs the adapter until ctx is cancelled, writing items and errors
    // into the provided channels. Must not close either channel.
    Activate(ctx context.Context, dst chan<- T, errs chan<- error)
    // adapterName returns the adapter descriptor for PortBindError context.
    adapterName() string
}

// SinkAdapter[T] consumes items from a SinkPort.
type SinkAdapter[T any] interface {
    // Activate connects src to the adapter's transport backend.
    // Runs until src terminates or ctx is cancelled.
    Activate(ctx context.Context, src gstream.Stream[T])
    adapterName() string
}

// IOAdapter[Req, Resp] transforms items for an IOPort.
type IOAdapter[Req, Resp any] interface {
    // Transform applies the adapter's IO operation to each item in src.
    Transform(ctx context.Context, src gstream.Stream[Req]) gstream.Stream[Resp]
    adapterName() string
}
```

### Test helpers

```go
// ChanSourceAdapter wraps a plain Go channel as a SourceAdapter.
// Use in tests to feed items into a SourcePort without a real transport.
//
//    ch := make(chan SensorReading, 2)
//    ch <- reading1; ch <- reading2; close(ch)
//    domain.SensorReadings.Bind(ctx, forge.ChanSourceAdapter(ch))
func ChanSourceAdapter[T any](ch <-chan T) SourceAdapter[T]

// ChanSinkAdapter wraps a plain Go channel as a SinkAdapter.
// Use in tests to capture sink output without a real transport.
//
//    out := make(chan OEE, 8)
//    domain.OEEResults.Bind(ctx, forge.ChanSinkAdapter(out))
func ChanSinkAdapter[T any](ch chan<- T) SinkAdapter[T]

// FuncIOAdapter wraps a plain function as an IOAdapter.
// Use in tests to stub an IOPort without a real service.
//
//    domain.Calibration.Bind(ctx, forge.FuncIOAdapter(func(ctx context.Context, r SensorReading) (CalibratedReading, error) {
//        return CalibratedReading{Reading: r, Offset: 0.0}, nil
//    }))
func FuncIOAdapter[Req, Resp any](fn func(context.Context, Req) (Resp, error)) IOAdapter[Req, Resp]
```

---

## Adapter binding constructors — per transport package

Each adapter package adds a `binding.go` file. Stream bridge helpers in each package's
`stream.go` are **deprecated** — the binding constructors replace them as the public API.
Non-stream functions (`Subscribe`, `Publish`, `Call`, `Serve`, `Handler`) are unchanged.

### `adapters/mqtt5/binding.go`

```go
// Deprecated stream bridges replaced: SubscribeStream, DrainPublish, CallStream

func SubscribeAdapter[T any](
    client  MQTTClient,
    router  MQTTRouter,
    filter  string,       // topic filter, e.g. "sensors/{sensorID}/data"
    qos     byte,
    fmt     format.Format[T],
    opts    SubscribeAdapterOptions,
) forge.SourceAdapter[T]

type SubscribeAdapterOptions struct {
    // UserPropertyParams maps IOParam names to MQTT5 User Property validation.
    // Keys must match IOParam.Name declared on the bound SourcePort.
    UserPropertyParams []UserPropertyParam
    SecurityFunc       func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) error
    Observer           stats.Observer
    Buffer             int
}

func PublishAdapter[T any](
    client MQTTClient,
    topic  string,          // topic template, e.g. "machines/{machineID}/oee"
    fmt    format.Format[T],
    opts   PublishAdapterOptions,
) forge.SinkAdapter[T]

type PublishAdapterOptions struct {
    // VarsFor derives {name} template vars from each item.
    // IOParam names must match {name} placeholders in the topic template.
    VarsFor  func(T) map[string]string
    QoS      byte
    Retain   bool
    Observer stats.Observer
}

func CallAdapter[Req, Resp any](
    client  MQTTClient,
    router  MQTTRouter,
    handle  *reqreply.RouteHandle[Req, Resp],
    opts    mqtt5.CallOptions,
) forge.IOAdapter[Req, Resp]
```

### `adapters/mqtt/binding.go`

```go
// Deprecated stream bridges replaced: SubscribeStream, DrainPublish

func SubscribeAdapter[T any](
    client  pahomqtt.Client,
    filter  string,
    qos     byte,
    fmt     format.Format[T],
    opts    SubscribeAdapterOptions,
) forge.SourceAdapter[T]

func PublishAdapter[T any](
    client pahomqtt.Client,
    topic  string,
    fmt    format.Format[T],
    opts   PublishAdapterOptions,
) forge.SinkAdapter[T]
```

### `adapters/nethttp/binding.go`

```go
// Deprecated stream bridges replaced: HandlerIngest, SSEFromStream, PollStream,
//   DrainCall, CallStream. HandlerLatest/RegisterLatest are deferred (cache ports).

// IngestAdapter accepts HTTP requests as SourcePort items.
// Registers a handler with mux at Activate time.
// IOParam names must match {name} placeholders in the path template.
func IngestAdapter[T any](
    mux    *http.ServeMux,
    method string,         // e.g. "POST"
    path   string,         // e.g. "/sensors/{sensorID}/readings"
    codec  codex.Codec[T],
    opts   IngestAdapterOptions,
) forge.SourceAdapter[T]

type IngestAdapterOptions struct {
    QueryParams    []rest.QueryParam    // IOParam.Name keys for query validation
    HeaderParams   []rest.HeaderParam   // IOParam.Name keys for header validation
    SecurityFunc   SecurityFunc
    Observer       stats.Observer
    Buffer         int
}

// PollAdapter polls an HTTP endpoint at interval. Each response is a new item.
func PollAdapter[Req, Resp any](
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    req     Req,           // constant request to poll with
    interval time.Duration,
    opts    PollAdapterOptions,
) forge.SourceAdapter[Resp]

// SSEAdapter writes each SinkPort item as an SSE event to all connected clients.
func SSEAdapter[Event any](
    mux   *http.ServeMux,
    path  string,
    codec codex.Codec[Event],
    opts  SSEAdapterOptions,
) forge.SinkAdapter[Event]

// DrainCallAdapter calls an HTTP endpoint for each SinkPort item (fire-and-forget).
func DrainCallAdapter[Req, Resp any](
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    opts    DrainCallAdapterOptions,
) forge.SinkAdapter[Req]

// CallAdapter sends each item as an HTTP request, emitting responses downstream.
func CallAdapter[Req, Resp any](
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    opts    CallAdapterOptions,
) forge.IOAdapter[Req, Resp]
```

### `adapters/zeromq/binding.go`

```go
// Deprecated stream bridges replaced: SubscribeStream, DrainPublish, CallStream

func SubscribeAdapter[T any](sock FramedSocket, filter string, fmt format.Format[T], opts SubscribeAdapterOptions) forge.SourceAdapter[T]
func PublishAdapter[T any](sock FramedSocket, topic string, fmt format.Format[T], opts PublishAdapterOptions) forge.SinkAdapter[T]
func CallAdapter[Req, Resp any](sock FramedSocket, handle *reqreply.RouteHandle[Req, Resp], opts CallOptions) forge.IOAdapter[Req, Resp]
```

### `adapters/file/binding.go`

```go
// Deprecated stream bridges replaced: ScanStream, WatchStream, ReadEachStream,
//   DrainWrite, DrainWriteFile, TapWriteFile

// ScanAdapter reads a file line-by-line.
func ScanAdapter[T any](path string, fmt format.Format[T], opts ScanAdapterOptions) forge.SourceAdapter[T]

// WatchAdapter emits file paths for new files created in a directory.
func WatchAdapter(dir string, interval time.Duration, opts WatchAdapterOptions) forge.SourceAdapter[string]

// ReadEachAdapter reads a complete typed file for each upstream Req item.
// VarsFor derives path template vars from the Req item.
// Combine pairs the original Req with the file content to produce Resp.
func ReadEachAdapter[Req, T, Resp any](
    f       format.File[T],
    varsFor func(Req) map[string]string,
    combine func(Req, T) Resp,
    opts    ReadEachAdapterOptions,
) forge.IOAdapter[Req, Resp]

// DrainWriteAdapter encodes each item and writes it as a line to a file.
func DrainWriteAdapter[T any](path string, fmt format.Format[T], opts DrainWriteAdapterOptions) forge.SinkAdapter[T]

// DrainWriteFileAdapter writes each item as a complete typed file (whole-file overwrite).
func DrainWriteFileAdapter[T any](f format.File[T], varsFor func(T) map[string]string, opts DrainWriteFileAdapterOptions) forge.SinkAdapter[T]
```

### `adapters/sql/binding.go`

```go
// Deprecated stream bridges replaced: QueryStream, DrainInsert, QueryEachStream

// QueryAdapter polls a SQL query at interval, emitting each row.
func QueryAdapter[T any](
    codec    codex.Codec[T],
    queryFn  func(context.Context) ([]T, error),
    interval time.Duration,
    opts     QueryAdapterOptions,
) forge.SourceAdapter[T]

// DrainInsertAdapter inserts each item via insertFn.
func DrainInsertAdapter[T any](
    codec    codex.Codec[T],
    insertFn func(context.Context, T) error,
    opts     DrainInsertAdapterOptions,
) forge.SinkAdapter[T]

// QueryEachAdapter performs a parameterized SQL query for each Req item.
func QueryEachAdapter[Req, T any](
    codec   codex.Codec[T],
    queryFn func(context.Context, Req) ([]T, error),
    opts    QueryEachAdapterOptions,
) forge.IOAdapter[Req, T]
```

---

## Structured errors

```go
// PortBindError is returned by SourcePort.Bind or IOPort.Bind when the adapter's
// Activate/Transform call fails (e.g. broker reject, mux route conflict, file not found).
type PortBindError struct {
    Port    string  // port name
    Adapter string  // adapter descriptor, e.g. "mqtt5.SubscribeAdapter"
    Err     error
}

func (e PortBindError) Error() string {
    return fmt.Sprintf("port %q bind (%s): %v", e.Port, e.Adapter, e.Err)
}
func (e PortBindError) Unwrap() error { return e.Err }
func (e PortBindError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("port", e.Port),
        slog.String("adapter", e.Adapter),
        slog.Any("err", e.Err),
    )
}

// PortNoAdapterError is returned by IOPort.Connect when no adapter has been bound.
type PortNoAdapterError struct {
    Port string
}
func (e PortNoAdapterError) Error() string {
    return fmt.Sprintf("port %q: no adapter bound — call Bind before Connect", e.Port)
}
func (e PortNoAdapterError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("port", e.Port))
}
```

---

## Observer integration

Ports use `stats.Observer` (same as adapters). Resolved from ctx at `Bind` time
(standard nil-guard: port's own `Observer` field first, then `stats.ObserverFromContext(ctx)`).

Every port's `Bind` call (`SourcePort.Bind`, `SinkPort.Bind`, `IOPort.Bind`,
`ToolPort.Bind`) wraps its adapter's Activate/Transform/Bind call with
`ports.bindWithObserver`, which:
- Calls `obs.RecordRequest("port.bind", "<portName>/<adapterName>", 200|500, duration)`
  once the wrapped call returns. For `SourcePort`/`SinkPort` the wrapped call is the
  adapter's entire `Activate` lifetime (the background goroutine that runs until
  `ctx` is cancelled); for `IOPort`/`ToolPort` it wraps the synchronous `Bind` call.
- When `obs` implements `stats.TraceObserver`, brackets the call in a `"port.bind"`
  span (`StartSpan`/`EndSpan`), passed via context to the adapter for span
  propagation.

Per-item events remain the adapter's responsibility — adapters retain their
existing `RecordSubscribe`/`RecordPublish`/`RecordRequest` calls for individual
messages; ports do not double-record per item.

---

## Known gaps (found in Phase 3 review) — resolved

A code-vs-docs audit found several items documented as implemented but not
enforced/wired in code. All were fixed in the Phase 3 follow-up:

| Gap | What the docs/API promised | Fix applied |
|-----|------------------------------|-------------|
| **IOParam runtime enforcement** | Adapter validates routing/topic/path param values against the `IOParam.Codec` | Added `ports.ValidateParams`, `ports.WithParams`/`ports.ParamsFromContext` (context-propagated by every port's `Bind`). Wired into `file.ReadEachAdapter` and `file.DrainWriteFileAdapter` — the two adapters with no protocol-level param builder of their own (`ReadError`/`WriteError` now wrap `codex.ValidationErrors` on failure). Adapters backed by `rest.Route`/`events.ChannelHandle`/`mqtt5.UserPropertyParam` already validate via their own builder-time mechanism and intentionally do not consult `Params` (documented on `IOParam` and `PortOptions.Params`). |
| **Port-level Observer events** | `Bind`/`Stream`/`Connect` emit `RecordRequest("port.bind", …)` and `TraceObserver` spans | Added `ports.bindWithObserver` helper; wired into `SourcePort.Bind`, `SinkPort.Bind`, `IOPort.Bind`, `ToolPort.Bind`. Each now emits `RecordRequest("port.bind", "<port>/<adapter>", 200\|500, duration)` and, when the observer implements `stats.TraceObserver`, a `"port.bind"` span bracketing the adapter's Activate/Transform/Bind call. |
| **`T05 TestSourcePort_CodecValidation`** | Test asserting invalid items surface as errors via codec validation | Superseded by `ports.ValidateParams` tests (`TestValidateParams_*`) plus `file` adapter param-validation tests (`TestReadEachAdapter_ParamValidationError`, `TestDrainWriteFileAdapter_ParamValidationError`) — SourcePort/SinkPort's own payload `Codec()` remains descriptive-only (payload decode/validate happens in the format/adapter layer, not redundantly in the port). |
| **`PortOptions.Buffer` on all port types** | Buffer configures the internal channel size for any port constructor | Documented as intentional: `IOPort`/`ToolPort` have no internal channel (`Connect`/`Bind` delegate directly to the adapter's `Transform`/`Bind` call), so there is nothing to buffer. `PortOptions.Buffer` and `NewIOPort`/`NewToolPort` godoc now state this explicitly instead of silently ignoring the field. |

Test-coverage gaps also closed: `chi.PipelineAdapter`, `zeromq.ServeAdapter`,
`mqtt5.ServeAdapter` each gained tests exercising the full `ports.ToolPort.Bind`
round-trip; `mcpgo.ToolLatestAdapter` gained `TestToolLatestAdapter_ReturnsCachedValue`
(polls for the background cache-store goroutine, then asserts the tool response
contains the cached value) and `TestToolLatestAdapter_NoValueYet_ReturnsIsError`.

Remaining backlog from the Phase 3 review (design work, not doc/code mismatches):
`forge.App` lifecycle manager, `CachePort[T]`, auto spec generation from ports,
IOParam↔adapter template cross-validation at Bind time, dynamic adapter
rebinding, and reconsidering whether `ToolPort.Bind` should require
`SetPipeline` for cache-only adapters like `mcpgo.ToolLatestAdapter`. These stay
tracked as SQL todos pending design decisions.

---

## Deprecation path for stream bridge helpers

| Bridge helper (deprecated) | Replaced by | Package |
|----------------------------|-------------|---------|
| `mqtt5.SubscribeStream` | `mqtt5.SubscribeAdapter` | `adapters/mqtt5` |
| `mqtt5.DrainPublish` | `mqtt5.PublishAdapter` | `adapters/mqtt5` |
| `mqtt5.CallStream` | `mqtt5.CallAdapter` | `adapters/mqtt5` |
| `mqtt.SubscribeStream` | `mqtt.SubscribeAdapter` | `adapters/mqtt` |
| `mqtt.DrainPublish` | `mqtt.PublishAdapter` | `adapters/mqtt` |
| `nethttp.HandlerIngest` / `RegisterIngest` | `nethttp.IngestAdapter` | `adapters/nethttp` |
| `nethttp.SSEFromStream` / `SSEFromHub` | `nethttp.SSEAdapter` | `adapters/nethttp` |
| `nethttp.PollStream` | `nethttp.PollAdapter` | `adapters/nethttp` |
| `nethttp.DrainCall` | `nethttp.DrainCallAdapter` | `adapters/nethttp` |
| `nethttp.CallStream` | `nethttp.CallAdapter` | `adapters/nethttp` |
| `zeromq.SubscribeStream` | `zeromq.SubscribeAdapter` | `adapters/zeromq` |
| `zeromq.DrainPublish` | `zeromq.PublishAdapter` | `adapters/zeromq` |
| `zeromq.CallStream` | `zeromq.CallAdapter` | `adapters/zeromq` |
| `file.ScanStream` | `file.ScanAdapter` | `adapters/file` |
| `file.WatchStream` | `file.WatchAdapter` | `adapters/file` |
| `file.ReadEachStream` | `file.ReadEachAdapter` | `adapters/file` |
| `file.DrainWrite` | `file.DrainWriteAdapter` | `adapters/file` |
| `file.TapWriteFile` / `DrainWriteFile` | `file.DrainWriteFileAdapter` | `adapters/file` |
| `sql.QueryStream` | `sql.QueryAdapter` | `adapters/sql` |
| `sql.DrainInsert` | `sql.DrainInsertAdapter` | `adapters/sql` |
| `sql.QueryEachStream` | `sql.QueryEachAdapter` | `adapters/sql` |

**Not deprecated (standalone imperative use — kept):**

`mqtt5.Subscribe`, `mqtt5.Publish`, `mqtt5.Call`, `mqtt5.Serve`,
`mqtt.SubscribeHandler`, `mqtt.Publish`,
`nethttp.Handler`, `nethttp.Call`, `nethttp.HandlerLatest`/`RegisterLatest` (cache — different pattern),
`nethttp.PipelineHandler`/`RegisterPipeline`,
`zeromq.Subscribe`, `zeromq.Publish`, `zeromq.Serve`, `zeromq.Call`,
`zeromq.ServeLatest` (cache — different pattern),
`zeromq.AsPipelineFunc`, `mqtt5.AsPipelineFunc`,
`sql.Validate`, `adapters/chi` (same API as nethttp — add bindings separately)

---

## Unit test plan

| ID | Name | What it verifies |
|----|------|-----------------|
| T01 | `TestSourcePort_SingleAdapter` | One adapter bound → items appear in Stream |
| T02 | `TestSourcePort_FanIn` | Two adapters bound → items from both merged into Stream |
| T03 | `TestSourcePort_NoBind` | Stream terminates on ctx cancel when no adapter bound |
| T04 | `TestSourcePort_BindError` | Activate failure → PortBindError returned |
| T05 | `TestSourcePort_CodecValidation` | Invalid item → error in Stream.Errors, valid item passes |
| T06 | `TestSinkPort_SingleAdapter` | Items from Feed appear in bound adapter |
| T07 | `TestSinkPort_FanOut` | Items from Feed appear in all bound adapters |
| T08 | `TestIOPort_HappyPath` | Connect transforms items via bound adapter |
| T09 | `TestIOPort_NoAdapterError` | Connect without Bind → PortNoAdapterError in Stream.Errors |
| T10 | `TestIOPort_DoubleBindError` | Second Bind → PortBindError |
| T11 | `TestChanSourceAdapter` | ChanSourceAdapter pushes all items to port |
| T12 | `TestChanSinkAdapter` | ChanSinkAdapter captures all items from port |
| T13 | `TestFuncIOAdapter` | FuncIOAdapter transforms items correctly |
| T14 | `TestPortBindError_LogValue` | Returns slog.KindGroup with `port`, `adapter`, `err` |
| T15 | `TestPortNoAdapterError_LogValue` | Returns slog.KindGroup with `port` |
| T16 | `TestIOParam_WithCodec` | Returns copy with Codec set, original unchanged |
| T17 | `TestMQTT5SubscribeAdapter` | Delivers items from mock broker to SourcePort |
| T18 | `TestMQTT5PublishAdapter` | SinkPort items appear in mock broker publish |
| T19 | `TestFileReadEachAdapter` | IOPort enrichment from file per item |
| T20 | `TestSQLQueryEachAdapter` | IOPort enrichment from SQL query per item |

---

## Files created / modified (Phase 1 — complete)

> All items below have been implemented. Package location changed from `forge` → `ports` during implementation (better dependency graph).

| File | Status | Responsibility |
|------|--------|----------------|
| `ports/io_param.go` | ✅ Created | `IOParam`, `PortOptions` |
| `ports/port_errors.go` | ✅ Created | `PortBindError`, `PortNoAdapterError` |
| `ports/source_port.go` | ✅ Created | `SourcePort[T]`, `SourceAdapter[T]` interface |
| `ports/sink_port.go` | ✅ Created | `SinkPort[T]`, `SinkAdapter[T]` interface |
| `ports/io_port.go` | ✅ Created | `IOPort[Req,Resp]`, `IOAdapter[Req,Resp]` interface |
| `ports/test_adapters.go` | ✅ Created | `ChanSourceAdapter[T]`, `ChanSinkAdapter[T]`, `FuncIOAdapter[Req,Resp]` |
| `ports/port_test.go` | ✅ Created | Tests T01–T17 |
| `ports/doc.go` | ✅ Created | Package overview |
| `adapters/mqtt5/binding.go` | ✅ Created | `SubscribeAdapter`, `PublishAdapter`, `CallAdapter` |
| `adapters/mqtt5/binding_test.go` | ✅ Created | SubscribeAdapter, PublishAdapter, AsPipelineFunc, CallAdapter tests |
| `adapters/mqtt/binding.go` | ✅ Created | `SubscribeAdapter`, `PublishAdapter` |
| `adapters/nethttp/binding.go` | ✅ Created | `IngestAdapter`, `SSEAdapter`, `PollAdapter`, `CallAdapter`, `DrainCallAdapter` |
| `adapters/nethttp/binding_test.go` | ✅ Created | Binding adapter tests |
| `adapters/zeromq/binding.go` | ✅ Created | `SubscribeAdapter`, `PublishAdapter`, `CallAdapter` |
| `adapters/zeromq/binding_test.go` | ✅ Created | `CallAdapter` tests |
| `adapters/file/binding.go` | ✅ Created | `ScanAdapter`, `WatchAdapter`, `ReadEachAdapter`, `DrainWriteAdapter`, `DrainWriteFileAdapter` |
| `adapters/file/binding_test.go` | ✅ Created | All binding adapter tests |
| `adapters/sql/binding.go` | ✅ Created | `QueryAdapter`, `QueryEachAdapter`, `DrainInsertAdapter` |
| `adapters/sql/binding_test.go` | ✅ Created | All binding adapter tests |
| `adapters/mqtt5/stream.go` | ✅ Trimmed | `SubscribeStream`/`DrainPublish`/`CallStream` **removed** (not deprecated — fully deleted) |
| `adapters/mqtt/stream.go` | ✅ Deleted | Was empty after bridge removal |
| `adapters/nethttp/stream.go` | ✅ Trimmed | Bridge functions removed; `HandlerLatest`, `PipelineHandler`, `SSEFromHub` kept |
| `adapters/zeromq/stream.go` | ✅ Trimmed | Bridge functions removed; `AsPipelineFunc`, `ServeLatest` kept |
| `adapters/file/stream.go` | ✅ Deleted | Was doc-only; doc moved to `binding.go` |
| `adapters/sql/stream.go` | ✅ Deleted | Was doc-only; doc moved to `binding.go` |

---

## Phase 2 — Design

### Phase 2 scope

| Priority | Item | Complexity | Status |
|----------|------|-----------|--------|
| **P1** ✅ | `adapters/chi/binding.go` | Low | Implemented: `IngestAdapter`, `SSEAdapter`, `PipelineAdapter` ← added beyond original plan |
| **P1** ✅ | `adapters/mcpgo/binding.go` + `ports.ToolPort[In,Out]` | Medium | Implemented: `ToolPort`, `ToolPipelineAdapter`, `ToolLatestAdapter` |
| **P2** ✅ | `adapters/nethttp/binding.go` server-side | Low | Implemented: `PipelineAdapter` |
| **P2** ✅ | `adapters/zeromq/binding.go` server-side | Low | Implemented: `ServeAdapter` |
| **P2** ✅ | `adapters/mqtt5/binding.go` server-side | Low | Implemented: `ServeAdapter` |
| **Deferred** | `forge.App` lifecycle manager | High | Not designed |
| **Deferred** | Cache ports (`HandlerLatest`/`ServeLatest` → `CachePort[T]`) | Medium | Not designed |
| **Deferred** | Auto spec generation from ports | High | Not designed |
| **Deferred** | IOParam ↔ adapter cross-validation at Bind time | Medium | Not designed |

---

### P1 — `adapters/chi/binding.go`

Chi is a server-only router — it only needs server-side source and sink adapters. No client-side adapters (no `PollAdapter`, `CallAdapter`, `DrainCallAdapter`).

```go
// adapters/chi/binding.go

// IngestAdapterOptions configures [IngestAdapter].
type IngestAdapterOptions struct {
    Options Options
    Buffer  int
}

// IngestAdapter returns a [ports.SourceAdapter] that accepts HTTP requests as
// pipeline items via chi router. When Activate is called it registers a handler.
func IngestAdapter[T any](
    r      chi.Router,
    handle *rest.RouteHandle[T, struct{}],
    opts   IngestAdapterOptions,
) ports.SourceAdapter[T]

// SSEAdapterOptions configures [SSEAdapter].
type SSEAdapterOptions struct {
    Options          Options
    SSEStreamOptions SSEStreamOptions
}

// SSEAdapter returns a [ports.SinkAdapter] that serves each SinkPort item as
// an SSE event to all connected clients via chi router.
func SSEAdapter[Event any](
    r      chi.Router,
    handle *rest.SSERouteHandle[struct{}, Event],
    opts   SSEAdapterOptions,
) ports.SinkAdapter[Event]

// PipelineAdapterOptions configures [chi.PipelineAdapter].
type PipelineAdapterOptions struct {
    Options Options
}

// PipelineAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an HTTP endpoint via chi router. Use with [ports.ToolPort.Bind].
func PipelineAdapter[Req, Resp any](
    r      chi.Router,
    handle *rest.RouteHandle[Req, Resp],
    opts   PipelineAdapterOptions,
) ports.ToolAdapter[Req, Resp]
```

---

### P1 — `ports.ToolPort[In,Out]` + `adapters/mcpgo/binding.go`

#### The ToolPort concept

MCP tools are server-side request/response: the LLM triggers a call, the pipeline
processes it, a response returns. This is the inverse of `IOPort` (which is client-side).

A `ToolPort[In,Out]` declares the request/response shape; the transport that
receives calls (MCP server, HTTP POST endpoint, ZeroMQ REP, MQTT5 Serve) is
bound in `main.go`. The pipeline function (domain logic) is provided once via
`SetPipeline` before binding.

```go
// ports/tool_port.go

// ToolAdapter[In, Out] is an adapter that receives requests and routes them
// through the pipeline function, delivering responses to the caller.
//
// Implemented by: mcpgo.ToolPipelineAdapter, nethttp.PipelineAdapter,
// zeromq.ServeAdapter, mqtt5.ServeAdapter.
type ToolAdapter[In, Out any] interface {
    // Bind registers fn as the handler for this transport backend.
    // fn is the pipeline function set on ToolPort.
    Bind(ctx context.Context, fn func(context.Context, In) gstream.Stream[Out]) error
    AdapterName() string
}

// ToolPort[In, Out] is a typed, protocol-agnostic server-side request/response
// IO enforcement point. It represents the "handle this request" boundary.
//
// Declare in domain/pipeline code. Set the pipeline function with SetPipeline.
// Bind to one or more transports in main.go.
//
//   // domain/pipeline.go — no adapter imports
//   var OEEToolPort = ports.NewToolPort[OEEIn, OEEResult]("oee-calc", inCodec, outCodec, ports.PortOptions{})
//
//   func init() {
//       OEEToolPort.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
//           return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
//       })
//   }
//
//   // main.go — bind to MCP AND HTTP (serve same logic on both transports)
//   domain.OEEToolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, toolHandle, mcpgo.Options{}))
//   domain.OEEToolPort.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.Options{}))
type ToolPort[In, Out any] struct { /* unexported */ }

// NewToolPort creates a ToolPort with the given name, request codec, and response codec.
func NewToolPort[In, Out any](
    name      string,
    inCodec   codex.Codec[In],
    outCodec  codex.Codec[Out],
    opts      PortOptions,
) *ToolPort[In, Out]

// SetPipeline sets the domain pipeline function that handles each request.
// Must be called before Bind. Call once at startup (init or main.go).
func (p *ToolPort[In, Out]) SetPipeline(fn func(context.Context, In) gstream.Stream[Out])

// Bind registers the pipeline with a transport adapter. Can be called multiple
// times to expose the same pipeline on multiple transports (MCP + HTTP).
// Returns PortBindError if SetPipeline was not called, or if the adapter's
// Bind call fails.
func (p *ToolPort[In, Out]) Bind(ctx context.Context, a ToolAdapter[In, Out]) error
```

#### `adapters/mcpgo/binding.go`

```go
// adapters/mcpgo/binding.go

// ToolPipelineAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an MCP tool. When Bind is called it calls [RegisterToolPipeline].
//
//   domain.OEEToolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(server, toolHandle,
//       mcpgo.Options{Observer: obs}))
func ToolPipelineAdapter[In, Out any](
    server *server.MCPServer,
    handle *apimcp.ToolHandle[In, Out],
    opts   Options,
) ports.ToolAdapter[In, Out]

// ToolLatestAdapter returns a [ports.ToolAdapter] backed by a reactive cache stream.
// When Bind is called it calls [RegisterToolLatest] with src.
// Each MCP tool call returns the most recently emitted value from src.
//
//   domain.OEEToolPort.Bind(ctx, mcpgo.ToolLatestAdapter(server, toolHandle, oeeStream, mcpgo.Options{}))
//
// Note: ToolLatestAdapter ignores the pipeline function set on ToolPort (the
// response comes from src, not the pipeline fn). This overrides SetPipeline.
func ToolLatestAdapter[In, Out any](
    server *server.MCPServer,
    handle *apimcp.ToolHandle[In, Out],
    src    gstream.Stream[Out],
    opts   Options,
) ports.ToolAdapter[In, Out]
```

#### Server-side adapters for nethttp, zeromq, mqtt5

Each server adapter wraps the existing `PipelineHandler`/`Serve` pattern:

```go
// adapters/nethttp/binding.go (addition)

// PipelineAdapterOptions configures [PipelineAdapter].
type PipelineAdapterOptions struct {
    Options Options
}

// PipelineAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an HTTP endpoint via [PipelineHandler]. When Bind is called it
// registers the handler with mux.
func PipelineAdapter[Req, Resp any](
    mux    *http.ServeMux,
    handle *rest.RouteHandle[Req, Resp],
    opts   PipelineAdapterOptions,
) ports.ToolAdapter[Req, Resp]

// adapters/zeromq/binding.go (addition)

// ServeAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as a ZeroMQ REP server via [Serve]. Runs in a background goroutine.
func ServeAdapter[Req, Resp any](
    sock FramedSocket,
    handle *reqreply.RouteHandle[Req, Resp],
    opts   ServeOptions,
) ports.ToolAdapter[Req, Resp]

// adapters/mqtt5/binding.go (addition)

// ServeAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an MQTT5 request/reply server via [Serve].
func ServeAdapter[Req, Resp any](
    client MQTTClient,
    router MQTTRouter,
    handle *reqreply.RouteHandle[Req, Resp],
    opts   ServeOptions,
) ports.ToolAdapter[Req, Resp]
```

#### What this enables

```go
// domain/pipeline.go — zero transport imports
var OEECalcTool = ports.NewToolPort[OEEIn, OEEResult](
    "oee-calc", oeeInCodec, oeeResultCodec, ports.PortOptions{})

func init() {
    OEECalcTool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
        s := gstream.Single(ctx, req)
        return gstream.Apply(ctx, s, oeeCalcFn, gstream.ApplyOptions{})
    })
}

// main.go — serve on all three transports with one line each
domain.OEECalcTool.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, mcpToolHandle, mcpgo.Options{}))
domain.OEECalcTool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))
domain.OEECalcTool.Bind(ctx, zeromq.ServeAdapter(repSock, zmqHandle, zeromq.ServeOptions{}))
// Same pipeline logic served on MCP, HTTP, and ZeroMQ — zero domain code changes.
//
// Note: ToolLatestAdapter ignores the pipeline fn (response comes from src).
// SetPipeline is still required before Bind — ToolPort enforces this.
// domain.OEEToolPort.Bind(ctx, mcpgo.ToolLatestAdapter(server, handle, oeeStream, opts))
// ↑ Returns cached value from oeeStream, not from the pipeline fn.
```

---

### Structured errors (Phase 2 additions)

```go
// PortNoPipelineError is returned by ToolPort.Bind when SetPipeline was not called.
type PortNoPipelineError struct {
    Port string
}
func (e PortNoPipelineError) Error() string {
    return fmt.Sprintf("port %q: no pipeline set — call SetPipeline before Bind", e.Port)
}
func (e PortNoPipelineError) LogValue() slog.Value {
    return slog.GroupValue(slog.String("port", e.Port))
}
```

---

### Deferred (not in Phase 2)

- **`forge.App`** — lifecycle manager with graceful shutdown and port dependency graph
- **Cache ports** (`CachePort[T]` backed by `HandlerLatest`/`ServeLatest`) — different pattern
- **Auto spec generation from ports** — ports register to AsyncAPI/OpenAPI builders
- **IOParam ↔ adapter cross-validation** — advisory check at Bind time
- **Dynamic rebinding** — hot-swap adapter on a running port

---

## Design decisions — resolved

| Question | Resolution |
|----------|-----------|
| **Adapter interface sealed or open?** | **Open** — `SourceAdapter[T]`, `SinkAdapter[T]`, `IOAdapter[Req,Resp]` have exported `AdapterName() string` + `Activate`/`Transform` methods. Users can implement custom adapters. |
| **Where do adapter interfaces live?** | **`ports` package** (not `forge`) — avoids import cycle (`stream` imports `forge`; `forge` imports `stream`). `ports` has no such cycle. |
| **Fan-out error policy** | **Continue per-adapter** — a broken MQTT sink does not stop SSE delivery. `SinkPort` has no single `OnError` field of its own; each bound `SinkAdapter` receives errors on its own error channel/callback (e.g. via `gstream.Drain`'s per-adapter error path), so one adapter erroring does not halt or terminate delivery to the others. |
| **TapWriteFile deprecation** | **Removed** — `file.DrainWriteFileAdapter` + `ports.SinkPort` replaces both `TapWriteFile` and `DrainWriteFile`. |
| **`adapterName()` method** | **`AdapterName() string`** — exported, used in `PortBindError.Adapter` field for observability. |
| **IOParam validation timing** | **Per-item only** — adapters validate at message/request time using the IOParam codec. No Bind-time cross-validation in Phase 1. |
| **Port options shape** | **`PortOptions` struct** — simpler than a sealed `PortOpt` interface; zero boilerplate for callers. |
