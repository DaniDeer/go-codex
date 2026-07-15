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

---

## Phase 4 — Ports as the primary pattern declaration surface

> **Status:** Implemented (core scope) — see "Implementation notes" at the end of
> this section for what shipped, what narrowed in scope during implementation, and
> what remains open. This was a breaking change by explicit user decision
> (single-user library; correctness of design takes priority over compatibility).

### Motivation

Phase 1–3 solved *wiring* (bind an adapter to a port without importing adapters in
domain code) but not *declaration*. Today, declaring a REST/event/reqreply-backed
port still requires two separate, redundant steps:

1. Build a `rest.RouteHandle` / `events.ChannelHandle` / `reqreply.RouteHandle` via
   `rest.NewRoute(...).Register(builder)` — declaring method/path/topic, params
   (`PathParam`, `QueryParam`, `TopicParam`, …), and codecs.
2. Separately declare a `ports.SourcePort`/`SinkPort`/`IOPort`/`ToolPort` with its
   own `PortOptions.Params []IOParam` — a second, weaker, disconnected copy of the
   same param names, often with no codec at all because the "real" validation
   already happens through the handle in step 1.

Verified during the Phase 3 review: for any adapter backed by a handle (REST,
events, MQTT 5, reqreply), the adapter's `Handler`/`Subscribe`/`Serve` already
validates body, path, query, cookie, header, and topic params **fully** through
the handle — `ports.IOParam` contributes nothing for these adapters; it is pure,
disconnected documentation. Only handle-less adapters (`file`, `sql`) benefit from
`IOParam` as real enforcement (Phase 3 already wired this — see "Known gaps" above).

The user's target workflow: **declare the communication pattern once, on the
port** (source = REST request / MQTT subscribe; sink = REST response / MQTT
publish / file write; io = REST call / MQTT5 request-reply call; tool = REST
pipeline / MQTT5 serve / MCP tool), **bind it to a concrete adapter with no
re-declaration**, and **optionally build the OpenAPI/AsyncAPI spec from that same
binding afterwards** — reversing today's "spec first, pipeline second" order.

### Scope decisions

| In scope | Out of scope (this phase) |
|---|---|
| `ports.Pattern` sealed interface + `RESTPattern`, `EventPattern`, `ReqReplyPattern`, `MCPPattern` structs, each a thin wrapper reusing the existing `rest.RouteOpt` / `events.ChannelOpt` / `reqreply.RouteOpt` / `apimcp.ToolOpt` vocabulary (no new param types — `PathParam`, `QueryParam`, `TopicParam`, etc. are reused as-is) | Reinventing param/codec types — `rest.PathParam` etc. remain the one definition |
| Ports construct their own `*rest.RouteHandle` / `*events.ChannelHandle` / `*reqreply.RouteHandle` / `*apimcp.ToolHandle` internally at construction time, **builder-free** (via `ClientHandle()` — see below) | Adapter interface signatures (`SourceAdapter[T].Activate`, etc.) — unchanged |
| Typed accessor functions (`ports.RESTHandle[Req,Resp](port)`, `ports.EventHandle[T](port)`, …) so adapter constructors keep their existing `handle` parameter, now filled from the port instead of hand-built | Removing the `handle` parameter from adapter constructors — kept for now to minimize blast radius across 8 adapter packages |
| `events.Channel[T].ClientHandle()` — new, mirrors the existing `rest.Route.ClientHandle()` / `reqreply.Route.ClientHandle()` (builder-free handle, no spec side effects) | Changing `rest.Route.ClientHandle()` / `reqreply.Route.ClientHandle()` — already correct, reused as-is |
| `ports.RegisterSpec(builder, port)` (or per-kind `RegisterREST`/`RegisterEvent`/`RegisterReqReply`/`RegisterMCP`) — replays the port's stored `Pattern` against a real `*rest.Builder`/`*events.Builder`/`*reqreply.Builder`/`*apimcp.Builder` to produce spec entries, **after** the port has already been declared and bound | A generic cross-protocol spec format — OpenAPI and AsyncAPI documents remain separate, protocol-specific outputs |
| Migrate `PortOptions.Params` to be derived automatically from a `RESTPattern`/`EventPattern`/`ReqReplyPattern`'s own param options when a Pattern is set; `Params` remains the primary (and only) declaration for handle-less adapters (`file`, `sql`) | Removing `PortOptions.Params` — still needed for `file`/`sql` |
| Update all `examples/*` that construct ports + adapters to the new pattern-first style | Bridges / old stream helpers — already fully removed, nothing to migrate there |

### API surface (sketch)

```go
// ports/pattern.go

// Pattern is the sealed interface for a port's declared communication pattern.
// Exactly one Pattern implementation applies per protocol family a port is
// bound to; a ToolPort exposed over both HTTP and MQTT5 declares both a
// RESTPattern and a ReqReplyPattern.
type Pattern interface{ isPortPattern() }

// RESTPattern declares an HTTP-shaped pattern, reusing rest.RouteOpt vocabulary
// (rest.PathParam, rest.QueryParam, rest.HeaderParam, rest.CookieParam,
// rest.RouteMeta, rest.ResponseMeta) unchanged.
type RESTPattern struct {
    Method string        // "GET", "POST", … — required for SourcePort ingest / IOPort call / ToolPort pipeline
    Path   string        // "/sensors/{sensorID}/data"
    Opts   []rest.RouteOpt
}
func (RESTPattern) isPortPattern() {}

// EventPattern declares a topic-shaped pattern, reusing events.ChannelOpt
// vocabulary (events.TopicParam, events.ChannelMeta, events.Subscribe, events.Publish).
type EventPattern struct {
    Topic string
    Opts  []events.ChannelOpt
}
func (EventPattern) isPortPattern() {}

// ReqReplyPattern declares a request/reply-shaped pattern (MQTT5 reqreply,
// ZeroMQ REQ/REP), reusing reqreply.RouteOpt vocabulary.
type ReqReplyPattern struct {
    Topic string
    Opts  []reqreply.RouteOpt
}
func (ReqReplyPattern) isPortPattern() {}

// MCPPattern declares an MCP tool pattern, reusing apimcp.ToolOpt vocabulary.
type MCPPattern struct {
    Name string
    Opts []apimcp.ToolOpt
}
func (MCPPattern) isPortPattern() {}
```

```go
// ports/io_param.go — PortOptions gains Patterns; Params stays for handle-less adapters.
type PortOptions struct {
    Patterns []Pattern   // NEW — one entry per protocol family this port will bind to
    Params   []IOParam   // unchanged — real enforcement only for handle-less adapters (file, sql)
    Buffer   int
    Observer stats.Observer
}
```

```go
// ports/handle.go — typed accessors; each constructs (once, cached) the
// corresponding *Handle via <Route|Channel>.ClientHandle() — no Builder needed.

func RESTHandle[Req, Resp any](p interface{ patterns() []Pattern }) (*rest.RouteHandle[Req, Resp], bool)
func EventHandle[T any](p interface{ patterns() []Pattern }) (*events.ChannelHandle[T], bool)
func ReqReplyHandle[Req, Resp any](p interface{ patterns() []Pattern }) (*reqreply.RouteHandle[Req, Resp], bool)
func MCPHandle[In, Out any](p interface{ patterns() []Pattern }) (*apimcp.ToolHandle[In, Out], bool)
```

```go
// Usage — declare once, bind directly, no separate Route/Channel/Register call:

// domain/pipeline.go — zero adapter imports, one declaration
var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.EventPattern{
                Topic: "sensors/{sensorID}/data",
                Opts: []events.ChannelOpt{
                    events.Subscribe{Summary: "Sensor reading received"},
                    events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
                },
            },
        },
    })

// main.go — bind directly; handle is derived from the port, not hand-built
handle, _ := ports.EventHandle[SensorReading](SensorReadings)
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, handle, 0, format.JSON(ReadingCodec), mqtt5.SubscribeAdapterOptions{}))

// later, optionally, build the AsyncAPI spec from the same binding:
b := events.NewBuilder(events.Info{Title: "Sensors", Version: "1.0.0"})
ports.RegisterEvent(b, SensorReadings) //nolint:errcheck
doc := b.Build()
```

```go
// events/builder.go — new, mirrors rest.Route.ClientHandle() / reqreply.Route.ClientHandle()
func (c Channel[T]) ClientHandle() *ChannelHandle[T]
```

### Structured errors

| Error | Returned by | Fields |
|---|---|---|
| `MissingPatternError` | `RESTHandle`/`EventHandle`/`ReqReplyHandle`/`MCPHandle` accessors, when the port declares no matching `Pattern` kind | `Port string`, `Kind string` (`"rest"`, `"event"`, `"reqreply"`, `"mcp"`) |
| `PatternRegisterError` | Port constructor, when `ClientHandle()`-equivalent construction fails (invalid path/topic template, unknown param name) — wraps the underlying `rest.InvalidPathParamError` / `events` equivalent | `Port string`, `Err error` |

Both implement `Error() string`, `Unwrap() error` (where applicable), and
`LogValue() slog.Value` per the mandatory error contract.

### Observer integration

No new observer hooks — pattern construction happens at `NewSourcePort`/etc.
construction time (not a request-scoped operation), so it is out of scope for
`stats.Observer`. `RegisterSpec`/`RegisterEvent`/etc. are one-shot document-build
calls, also not per-request — no observer hook needed there either.

### Unit test plan

| Test | Verifies |
|---|---|
| `TestRESTPattern_BuildsClientHandle` | `RESTHandle[Req,Resp](port)` returns a working handle when `RESTPattern` declared |
| `TestEventPattern_BuildsClientHandle` | `EventHandle[T](port)` returns a working handle when `EventPattern` declared |
| `TestReqReplyPattern_BuildsClientHandle` | `ReqReplyHandle[Req,Resp](port)` returns a working handle when `ReqReplyPattern` declared |
| `TestMCPPattern_BuildsClientHandle` | `MCPHandle[In,Out](port)` returns a working handle when `MCPPattern` declared |
| `TestHandleAccessor_MissingPattern_ReturnsFalse` | Accessor returns `(nil, false)` — no panic — when the port has no matching Pattern kind |
| `TestPort_MultiplePatterns_BothHandlesAvailable` | A `ToolPort` with both `RESTPattern` and `ReqReplyPattern` exposes both `RESTHandle` and `ReqReplyHandle` |
| `TestChannel_ClientHandle_NoBuilderRequired` | `events.Channel[T].ClientHandle()` works without a `Builder`, mirrors `rest.Route.ClientHandle()` test shape |
| `TestRegisterEvent_AddsChannelToBuilder` | `ports.RegisterEvent(b, port)` adds the port's declared channel to the builder's document |
| `TestRegisterREST_AddsRouteToBuilder` | `ports.RegisterREST(b, port)` adds the port's declared route to the builder's document |
| `TestPatternRegisterError_LogValue` / `TestMissingPatternError_LogValue` | Structured error contract |
| End-to-end: existing `SubscribeAdapter`/`IngestAdapter`/`CallAdapter`/`ServeAdapter` tests re-run with pattern-derived handles instead of hand-built ones — same assertions, new construction path | No regression in per-message validation behavior |

### Files to create / modify

| File | Responsibility |
|---|---|
| `ports/pattern.go` | `Pattern` interface, `RESTPattern`, `EventPattern`, `ReqReplyPattern`, `MCPPattern` |
| `ports/handle.go` | `RESTHandle`, `EventHandle`, `ReqReplyHandle`, `MCPHandle` accessors + internal handle cache |
| `ports/pattern_errors.go` | `MissingPatternError`, `PatternRegisterError` |
| `ports/spec.go` | `RegisterREST`, `RegisterEvent`, `RegisterReqReply`, `RegisterMCP` |
| `ports/io_param.go` | `PortOptions.Patterns` field added |
| `ports/source_port.go`, `sink_port.go`, `io_port.go`, `tool_port.go` | Construct handle(s) from `Patterns` at `New*Port` time; store for accessor use |
| `api/events/builder.go` | New `Channel[T].ClientHandle()` |
| `examples/*` (event-driven, adapters-mqtt5, sensor-service, forge-oee, …) | Migrate to pattern-first port declarations |
| `docs/features/ports.md`, `docs/guides/ports.md` | Document the new pattern-first workflow as primary; keep handle-first as an documented alternative for advanced/shared-handle cases |

### Open design decisions

| Question | Options | Recommendation |
|---|---|---|
| Does `ports.Pattern` replace `PortOptions.Params` entirely, or coexist? | (a) Coexist: `Params` stays for handle-less adapters only, as scoped above. (b) Deprecate `Params` entirely, require a `FilePattern`/`SQLPattern` too. | (a) — `file`/`sql` have no natural "route/channel" shape; forcing a `Pattern` there adds ceremony without benefit. |
| Should handle construction be eager (at `New*Port` time) or lazy (first accessor call)? | Eager: fail fast on bad pattern (unknown param name, invalid template) at declaration time — matches `rest.Route.Register`'s fail-fast philosophy. Lazy: defer cost until actually bound. | Eager — consistent with the "fail fast, at declaration" principle already used elsewhere (e.g. `rest.NewRoute(...).Register` validates path immediately). |
| Should `RegisterREST`/`RegisterEvent`/etc. be free functions or `AnyPort` methods? | Free functions (as sketched) avoid adding spec-building methods to every port type; `AnyPort` interface centralizes but adds surface. | Free functions, generic over the concrete port type — mirrors `ports.RESTHandle[Req,Resp](port)`'s shape. |
| Multi-pattern ports (e.g. `ToolPort` bound to both HTTP and MQTT5) — one `Patterns` slice mixing kinds, or separate typed fields? | Single `[]Pattern` slice (sketched) keeps `PortOptions` uniform across all 4 port types. Separate fields (`RESTPattern *RESTPattern`, `EventPattern *EventPattern`, …) are more discoverable via autocomplete but bloat `PortOptions` for ports that only ever use one. | `[]Pattern` slice — matches `rest.NewRoute`'s existing variadic-opts idiom (`RouteOpt`, `ChannelOpt`) already used throughout go-codex. |
| Do the now-largely-redundant Phase 3 `IOParam`-on-REST/Event/ReqReply-backed ports get removed, or kept as a fallback? | Remove `Params` usage recommendation for handle-backed adapters in docs (still technically settable, just inert) vs. hard-error if both `Patterns` and `Params` set for the same protocol family. | Soft: keep `Params` settable (backward compatible with Phase 3 `file`/`sql` usage) but update docs to state it is ignored once a matching `Pattern` handle exists for that protocol family. |

### Relationship to previously deferred items

This phase **absorbs and resolves**:
- `phase3-ioparam-cross-validation` — cross-validation becomes unnecessary once the
  port's own `Pattern` *is* the single source of the param declarations used to
  build the handle; there is nothing left to cross-validate against.
- `phase3-auto-spec-from-ports` (`Design auto AsyncAPI/OpenAPI spec generation
  from ports`) — resolved by `RegisterREST`/`RegisterEvent`/`RegisterReqReply`/`RegisterMCP`.

This phase is **orthogonal to and does not resolve**:
- `phase3-forge-app-lifecycle` — a lifecycle/dependency-graph manager is still a
  separate concern from pattern declaration.
- `phase3-cacheport-design` — the cache-pattern unification is unrelated to how a
  port declares its wire pattern.
- `phase3-dynamic-rebinding` — hot-swap is unrelated to declaration.
- `phase3-toolport-optional-pipeline` — still an open ergonomics question for
  `mcpgo.ToolLatestAdapter`-style cache tools, independent of pattern declaration.

### Implementation notes

Shipped, with all "Files to create/modify" items complete except examples (only
`examples/sensor-service` used `ports` at implementation time — migrated):

- `events.Channel[T].ClientHandle()` added (`api/events/builder.go`), mirroring
  `rest.Route.ClientHandle()`/`reqreply.Route.ClientHandle()`. `apimcp.Tool[In,Out].ClientHandle()`
  was also added (`api/mcp/builder.go`) — needed for `MCPPattern` and not originally
  listed as a separate file, but the same builder-free construction idiom.
- `ports.Pattern`, `RESTPattern`, `EventPattern`, `ReqReplyPattern`, `MCPPattern`
  (`ports/pattern.go`); `MissingPatternError`, `PatternRegisterError`
  (`ports/pattern_errors.go`); `RESTHandle`, `EventHandle`, `ReqReplyHandle`, `MCPHandle`
  accessors + eager handle/spec construction (`ports/handle.go`); `RegisterREST`,
  `RegisterEvent`, `RegisterReqReply`, `RegisterMCP` (`ports/spec.go`).
- `PortOptions.Patterns []Pattern` added alongside the existing `Params`.
- **Narrowed from the original sketch:** `NewSourcePort`/`NewSinkPort` only build
  handles for `EventPattern` (pub/sub); `NewIOPort`/`NewToolPort` only build handles
  for `RESTPattern`/`ReqReplyPattern`/`MCPPattern` (dual-codec patterns). `RESTPattern`
  for `SourcePort` (HTTP ingest) and `SinkPort` (SSE) is **not yet implemented** —
  both need an asymmetric `Req`/`Resp` shape (`rest.RouteHandle[T, struct{}]` for
  ingest, `rest.SSERouteHandle[struct{}, Event]` for SSE) that a single-codec port
  can't express with today's `RESTPattern{Method, Path, Opts}` shape. Tracking this
  as a follow-up (not yet a numbered SQL todo — revisit if REST ingest/SSE pattern
  declaration becomes a real pain point).
- **`NewIOPort`/`NewToolPort` signature changed to `(*Port, error)`** (breaking) to
  support fail-fast `PatternRegisterError` on malformed `MCPPattern` (empty tool name,
  schema render failure). `NewSourcePort`/`NewSinkPort` stayed infallible —
  `EventPattern` construction via `events.Channel.ClientHandle()` never errors.
- Handle accessors return `(nil, false)` (not an error) when no matching `Pattern`
  was declared — matches Go's map-lookup idiom (`v, ok := m[k]`) rather than forcing
  every call site to handle an error for an expected "not present" case.
- `RegisterREST`/etc. return `MissingPatternError` (not a bool) since calling them
  with the wrong/no `Pattern` is closer to a programming error at spec-build time.
- Full verification after implementation: `go build ./...`, `go vet ./...`
  (pre-existing unrelated `adapters/chi/adapter_test.go:1206` vet note, untouched),
  `go test ./...` (all packages pass, including new `ports` tests for pattern
  construction, handle accessors, and `Register*` functions), `just check`
  (staticcheck+gosec, 0 issues), all `examples/*/` exit 0.

---

## Phase 5 — Full `api` module parity in `Pattern` + adapter option review

> **Status:** Implemented — see "Implementation notes" at the end of this section.
>
> **Trigger:** asked to show topic-format constraints (`examples/adapters-mqtt`'s
> `sensorTopicConstraint` via `events.WithTopicConstraints`) on sensor-service's
> Pattern-based `sensorsPort`. Investigation revealed `ClientHandle()` (what every
> `Pattern` uses today) structurally cannot express *any* Builder-level capability —
> not just topic/path constraints. Broadened per explicit direction: *"review the
> whole api module... adapt it to the ports... let ports be the wrapper for the
> pipelines used together with the adapters to implement a concrete protocol. This
> way the bindings in the adapters can be questioned what their additional value
> is."* Refined once more per follow-up: *"is there an elegant way that we can
> reuse the api builder or its builder functions in the ports module to have
> really one point in the lib where we implement this and for the adapter it is
> transparent if the declaration comes from pure API builder or pipeline ports
> with API contract via the builder"* — landed on always calling `Register`
> (never branching to `ClientHandle`) so there is exactly one code path, shared
> verbatim between the pure-builder workflow and the ports workflow.

### Investigation findings

**1. `Pattern`'s `Opts` field already carries the full per-route/per-channel/per-tool
vocabulary** — confirmed by enumerating every `RouteOpt`/`ChannelOpt`/`ToolOpt`
implementor:

| Package | `Opt` implementors (all flow through `Pattern.Opts` today) |
|---|---|
| `rest` | `PathParam`, `QueryParam`, `CookieParam`, `HeaderParam`, `ResponseHeaderParam`, `ResponseCookieParam`, `ResponseMeta`, `RouteMeta` (incl. per-route `Security` override) |
| `events` | `TopicParam`, `ChannelMeta`, `Subscribe` (incl. per-op `Security`), `Publish` (incl. per-op `Security`) |
| `reqreply` | `TopicParam`, `RouteMeta` |
| `apimcp` | `ToolMeta` |

No gap here — per-route/channel/tool declarations were never the problem.

**2. The real gap is Builder-*level* capability, which `Opts` cannot carry because it
only applies to a single route/channel/tool, not the builder that registers many:**

| Builder-level capability | Where it lives | Populated by `ClientHandle()`? |
|---|---|---|
| Whole-path/topic format constraints | `rest.WithPathCodec`/`WithPathConstraints`, `events.WithTopicCodec`/`WithTopicConstraints` | **No** |
| Security scheme registry (`map[string]SecurityScheme` with runtime `Codec`) | `Builder.AddSecurityScheme` | **No — always empty map** |
| Global security fallback (`[]route.SecurityRequirement`) | `Builder.AddGlobalSecurity` | **No — always nil** |
| Servers, schemas (spec-only, no runtime effect) | `Builder.AddServer`/`AddSchema` | N/A (spec generation only) |

**3. Correctness gap found (not just a missing feature): silent security bypass.**
`adapters/nethttp/adapter.go`'s `validateSecurityCredentials` does
`s, ok := schemes[name]; if !ok || s.Codec == nil { continue }` — an unknown scheme
name is silently **skipped**, not rejected. Since every `Pattern`-built handle today
has an empty `SecuritySchemes` map (`ClientHandle()` never populates it), **any**
`RouteMeta.Security`/`Subscribe.Security`/`Publish.Security` requirement declared on
a `Pattern`-based port is silently unenforced. This is a real bug introduced by
Phase 4, not a hypothetical — fixed in this phase.

**4. Multi-format support (`RouteHandle.WithRequestFormats`/`WithFormats`,
`ChannelHandle.WithFormats`/`WithSubscribeFormats`/`WithPublishFormats`) already
works with `Pattern`** — these mutate the handle in place and return it for
chaining; since `ports.RESTHandle[Req,Resp](port)`/`ports.EventHandle[T](port)`
return the same cached `*RouteHandle`/`*ChannelHandle` pointer stored inside the
port, calling `.WithFormats(...)` on the retrieved handle before binding an adapter
already takes effect. No gap, just needs documenting.

**5. Adapter binding option review** — audited every `adapters/*/binding.go`
constructor's `XxxAdapterOptions` struct against what the `Pattern`-derived handle
already carries. Verdict: **the large majority of adapter option fields are genuine
protocol-specific glue with no ports-level equivalent** — e.g. `SecurityFunc`
(protocol-specific credential *extraction*, even though the *requirements* now come
from the handle), `Observer`, `OnError`, poll intervals, buffer sizes, format
negotiation. **One confirmed, fixable duplication:** `mqtt5.SubscribeAdapterOptions.TopicFilter`
/ `mqtt.SubscribeAdapterOptions.TopicFilter` require manually restating the topic in
MQTT wildcard syntax (`"sensors/+/data"`) even though the `Pattern`-declared topic
template (`"sensors/{sensorID}/data"`) already encodes the identical shape — fixed by
auto-deriving the wildcard filter when `TopicFilter` is empty.

### Design — one construction path, not two

The first draft of this phase had `Pattern` construction branch between
`Register(builder)` (when a builder is supplied) and `ClientHandle()` (when not) —
two code paths doing almost the same thing. Refined per feedback: **there should be
exactly one point in the library where a `Pattern` becomes a handle, full stop.**

`Route`/`Channel`/`Tool.Register(b)` is a strict superset of what `ClientHandle()`
does — `Register` performs every check `ClientHandle` performs (decode/encode
closures, per-variable param wiring) *plus* the builder-tracked ones `ClientHandle`
skips (`InvalidPathParamError`/`InvalidTopicParamError` for a param name with no
matching `{var}` placeholder, path/topic codec validation, security scheme/global
security population, and — for `reqreply`/`mcp` only — duplicate-topic/tool-name
detection; `rest`/`events` do not detect duplicate routes/topics at all, in either
`Register` or `ClientHandle`). There is no runtime behavior `ClientHandle` provides
that `Register` doesn't already provide as a superset — so `Pattern` construction
should **always call `Register`**, never `ClientHandle`. When the caller doesn't
supply a `*Builder`, `ports` creates a private, single-use one with zero `Info` and
uses it for that one `Register` call — functionally identical to the old
builder-free default, but through the *exact same*
code path used when a real, shared, security/constraint-configured `Builder` is
supplied:

```go
type PortOptions struct {
    Patterns []Pattern
    Params   []IOParam
    Buffer   int
    Observer stats.Observer

    // RESTBuilder registers each RESTPattern's Route against b via Route.Register(b).
    // Supply the SAME *rest.Builder your application already uses (with
    // rest.WithPathConstraints, b.AddSecurityScheme, b.AddGlobalSecurity, …
    // already configured) to get full parity with hand-declared routes — the
    // resulting handle is indistinguishable from one built by calling
    // rest.NewRoute(...).Register(b) directly; RESTHandle[Req,Resp](port) and a
    // plain *rest.RouteHandle passed straight into an adapter are the same thing.
    //
    // When nil, ports registers against a private, single-use *rest.Builder
    // with zero Info — the same zero-ceremony default as before, through the
    // identical Register code path (no separate builder-free construction).
    RESTBuilder *rest.Builder

    // EventBuilder — same idea for EventPattern / events.Builder.
    EventBuilder *events.Builder

    // ReqReplyBuilder — same idea for ReqReplyPattern / reqreply.Builder.
    ReqReplyBuilder *reqreply.Builder

    // MCPBuilder — same idea for MCPPattern / apimcp.Builder.
    MCPBuilder *apimcp.Builder
}
```

`buildEventPatternHandles`/`buildDualCodecPatternHandles` (`ports/handle.go`) always
call `.Register(builder)` — `builder` is `opts.XBuilder` when non-nil, otherwise a
freshly constructed, discarded-after-use `rest.NewBuilder(rest.Info{})` /
`events.NewBuilder(events.Info{})` / `reqreply.NewBuilder(reqreply.Info{})` /
`apimcp.NewBuilder(apimcp.Info{})`. **This is the single point in the library where
a `Pattern` becomes a handle** — identical whether the caller wired it through
`ports` or would have called `Register` by hand. Any error is wrapped as
`PatternRegisterError`.

This is what makes the result **transparent to the adapter**: an
`adapters/mqtt5.SubscribeAdapter(client, router, handle, …)` call cannot tell
whether `handle` came from `events.NewChannel(...).Register(myBuilder)` written
directly in `main.go` (pure API-builder workflow, no `ports` involved) or from
`ports.EventHandle[T](sensorsPort)` where `sensorsPort` declared an `EventPattern`
with `PortOptions.EventBuilder: myBuilder` — both paths run through the exact same
`Channel.Register` call, on the exact same `*events.Builder`, producing the exact
same `*events.ChannelHandle[T]`. The only degree of freedom `ports` adds is *when*
and *where* that `Register` call happens (inside the port constructor instead of
by hand in `main.go`) — never *what* it does.

Because `Register` is fallible in ways the old `ClientHandle()` path wasn't
(`InvalidPathParamError`/`InvalidTopicParamError`, `InvalidPathError`/
`InvalidTopicError` from a configured path/topic codec, and — for `reqreply`/`mcp`
only — `DuplicateRouteError`/an "already registered" error) — and is now used
unconditionally, not just when a `Builder` is supplied — **`NewSourcePort`/
`NewSinkPort` must also become `(*Port, error)`**, completing the same consistency
change already made to `NewIOPort`/`NewToolPort` in Phase 4.

**Consequence for `RegisterREST`/`RegisterEvent`/`RegisterReqReply`/`RegisterMCP`
(Phase 4's spec-replay functions):** if the caller already supplied a `Builder` via
`PortOptions`, the port's route/channel/tool is *already* registered with it — the
`Register*` family exists only for the case where no `Builder` was supplied up
front (the port used its private, throwaway one) and the caller wants to add the
already-bound port to a *different*, real spec `Builder` after the fact. Calling
`RegisterREST(b, port)`/`RegisterEvent(b, port)` with the *same* `b` already passed
via `PortOptions.RESTBuilder`/`EventBuilder` does **not** error — `rest`/`events`
don't detect duplicate routes/topics at all, so it just adds a second, identical
entry to the spec. Calling `RegisterReqReply`/`RegisterMCP` the same way **does**
error (`DuplicateRouteError` / an "already registered" error), since `reqreply`/`mcp`
do detect duplicates. Document this asymmetry explicitly.

### Unit test plan

| Test | Verifies |
|---|---|
| `TestEventPattern_WithBuilder_PopulatesSecuritySchemes` | A `Pattern`-derived handle built via `PortOptions.EventBuilder` (with `AddSecurityScheme`/`AddGlobalSecurity` configured) carries real `SecuritySchemes`/`GlobalSecurity` |
| `TestEventPattern_NilBuilder_NoSecuritySchemes` | Regression — no `EventBuilder` supplied still constructs successfully but carries no security schemes |
| `TestRESTPattern_NilBuilder_StillGoesThroughRegister` | No `RESTBuilder` supplied → still goes through `Register` (against a private builder), not `ClientHandle` — verified via an unknown `PathParam` name → `InvalidPathParamError`, which the old `ClientHandle` path silently ignored |
| `TestRESTPattern_WithBuilder_PathConstraintFailure_ReturnsPatternRegisterError` | `rest.WithPathConstraints` failure surfaces as `PatternRegisterError` wrapping `InvalidPathError` |
| `TestRegisterReqReply_SameBuilderAlreadyUsed_ReturnsDuplicateRouteError` | `reqreply`'s real duplicate-topic detection fires when replaying against the same builder already used at construction |
| `TestRegisterMCP_SameBuilderAlreadyUsed_ReturnsError` | Same for `mcp`'s duplicate-name detection |
| `TestRESTPattern_WithBuilder_UsesSharedBuilderForSpec` | A `RESTBuilder`-backed port's route is already present in that builder's `OpenAPISpec()` output — no separate `RegisterREST` replay needed |
| TopicFilter auto-derivation (mqtt5/mqtt adapter tests) | Empty `TopicFilter` + `{var}` topic → correct MQTT wildcard subscription, no manual restatement needed |

### Files to create/modify

| File | Change |
|---|---|
| `ports/io_param.go` | Add `RESTBuilder`/`EventBuilder`/`ReqReplyBuilder`/`MCPBuilder` to `PortOptions` |
| `ports/handle.go` | Replace `ClientHandle()` calls with unconditional `Register(builder)` (private builder when `opts.XBuilder` is nil) — single construction path |
| `ports/source_port.go`, `ports/sink_port.go` | `NewSourcePort`/`NewSinkPort` return `(*Port, error)` |
| `adapters/mqtt5/adapter.go`, `adapters/mqtt/adapter.go` | Auto-derive wildcard `TopicFilter` from `{var}` topic shape when empty |
| ~27 call sites (ports/port_test.go, examples/sensor-service/main.go, 5 adapter `binding_test.go` files) | Mechanical `(*Port, error)` handling update |
| `examples/sensor-service/main.go` | `sensorsPort`/`alertsPort` demonstrate `EventBuilder` + `WithTopicConstraints`, mirroring `examples/adapters-mqtt` |
| `docs/features/ports.md`, `docs/guides/ports.md` | Document `RESTBuilder`/etc. fields, the single-construction-path guarantee, the security-scheme fix, `TopicFilter` auto-derivation, and the "adapter binding value" review conclusion |

### Open design decisions

| Question | Resolution |
|---|---|
| Does `ClientHandle()` get removed from `rest`/`events`/`reqreply`/`mcp` now that `ports` never calls it? | **No** — it remains genuinely useful standalone, for pure client-side usage with no `Builder`/spec at all (e.g. `nethttp.Call` against a third-party API declared with `rest.NewRoute(...).ClientHandle()`). Only `ports`' *internal* construction stops using it. |
| Should `ReqReplyBuilder`/`MCPBuilder` exist even though `reqreply`/`mcp` have no `BuilderOption` mechanism (no constraints/security to gain today)? | **Yes, for consistency and future-proofing** — `Register` still gives duplicate-name/topic detection across ports sharing one builder, for free, today. If `reqreply`/`mcp` later grow `BuilderOption`s, `Pattern` gains them automatically. |
| Should `PortOptions` gain a single `Builders` bundle struct instead of 4 separate fields? | **4 separate typed fields** — matches the existing `RESTHandle`/`EventHandle`/`ReqReplyHandle`/`MCPHandle` one-function-per-kind idiom; a bundle struct would just move the same 4 fields one level deeper with no real benefit. |
| Is creating a throwaway `Builder` for every `Pattern`-based port with no supplied `Builder` wasteful? | No — a `Builder` is a small struct with a few empty maps/slices; the cost is negligible and this is exactly what makes the "one construction path" guarantee possible. |

### Implementation notes

Shipped as designed, with one correction found during implementation:
**`rest.Route.Register` and `events.Channel.Register` do not detect duplicate
routes/topics** (only `reqreply.Route.Register` and `apimcp.Tool.Register` do,
via `DuplicateRouteError` and a plain "already registered" error respectively) —
calling `RegisterREST`/`RegisterEvent` with the same builder a `Pattern` already
registered against does **not** error for REST/events, it just adds a second,
duplicate entry to the spec. Only `RegisterReqReply`/`RegisterMCP` reject the
redundant call. Tests and docs were adjusted to reflect this actual behavior
rather than the originally assumed uniform duplicate-detection story.

- `PortOptions.RESTBuilder`/`EventBuilder`/`ReqReplyBuilder`/`MCPBuilder` added
  (`ports/io_param.go`).
- `ports/handle.go`'s `buildEventPatternHandles`/`buildDualCodecPatternHandles`
  rewritten to always call `Register` (private single-use builder when the
  matching field is nil) — `ClientHandle()` is no longer called anywhere inside
  `ports`. `rest`/`events`/`reqreply`/`apimcp`'s `ClientHandle()` methods
  themselves are unchanged and remain available for standalone (non-`ports`)
  client-only usage.
- `NewSourcePort`/`NewSinkPort` changed to `(*Port, error)` (breaking, joining
  `NewIOPort`/`NewToolPort` from Phase 4) — ~27 call sites updated across
  `ports/port_test.go`, `examples/sensor-service/main.go`, and 5 adapter
  `binding_test.go` files, using the same mechanical transform as Phase 4.
- Fixed the confirmed security-scheme silent-bypass gap: a `Pattern`-derived
  handle now carries real `SecuritySchemes`/`GlobalSecurity` when a `Builder` is
  supplied, verified end-to-end with a dedicated test
  (`TestEventPattern_WithBuilder_PopulatesSecuritySchemes`) plus a regression
  test proving the nil-builder default still has none
  (`TestEventPattern_NilBuilder_NoSecuritySchemes`).
- `adapters/mqtt5` and `adapters/mqtt` `SubscribeAdapterOptions.TopicFilter` now
  auto-derives an MQTT wildcard filter (`{var}` → `+`) from the handle's topic
  when empty, instead of subscribing with the raw, brace-containing topic
  string — the one confirmed adapter-option redundancy found during the review.
  `adapters/mqtt` gained its first `binding_test.go` in the process (previously
  zero test coverage for `mqtt.SubscribeAdapter`).
- `examples/sensor-service` updated: `sensorsPort`/`alertsPort` now share one
  `events.Builder` (via `PortOptions.EventBuilder`) configured with
  `events.WithTopicConstraints(validate.MQTTPublishTopic, sensorTopicConstraint)`
  — directly mirroring `examples/adapters-mqtt`'s builder-level constraint style,
  but enforced through the port's `Pattern` instead of a hand-built
  `events.Channel`. The example also now prints the AsyncAPI spec built directly
  from the two ports' bindings (`eventsBuilder.AsyncAPISpec()`), demonstrating
  "build the spec from the binding" end-to-end.
- Full verification: `go build ./...`, `go vet ./...` (pre-existing unrelated
  `adapters/chi/adapter_test.go:1206` note, untouched), `go test ./...` (all
  packages pass), `just check` (staticcheck+gosec, 0 issues), all `examples/*/`
  exit 0.
