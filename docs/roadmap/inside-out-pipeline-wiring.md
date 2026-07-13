# Inside-Out Pipeline Wiring — Protocol-Agnostic IO Ports

> **Status:** ✅ Phase 1 implemented. `ports` package + adapter bindings for all transports. Stream bridge helpers deprecated.
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

sensorStream := mqtt5.SubscribeStream(ctx, client, router, sensorHandle, 0, ...)
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
var SensorReadings = forge.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    forge.Params(
        forge.IOParam{Name: "sensorID", Description: "Sensor identifier"}.WithCodec(SensorIDCodec),
    ))

var Calibration = forge.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec,
    forge.Params(
        forge.IOParam{Name: "sensorID"}.WithCodec(SensorIDCodec),
    ))

var OEEResults = forge.NewSinkPort[OEE]("oee-results", OEECodec,
    forge.Params(
        forge.IOParam{Name: "machineID"}.WithCodec(machineIDCodec),
    ))

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
        client, router,
        "sensors/{sensorID}/data", 0,
        format.JSON(domain.ReadingCodec),
        mqtt5.SubscribeAdapterOptions{},
    ))
    domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(
        mux,
        "POST", "/sensors/{sensorID}/readings",
        domain.ReadingCodec,
        nethttp.IngestAdapterOptions{},
    ))

    // Calibration: HTTP enrichment call (swap for SQL with one line change)
    domain.Calibration.Bind(ctx, nethttp.CallAdapter(
        httpClient, "http://calibration-svc:8080",
        calibrationRouteHandle,
        nethttp.CallAdapterOptions{},
    ))
    // OR: domain.Calibration.Bind(ctx, sql.QueryEachAdapter(...))
    // OR: domain.Calibration.Bind(ctx, file.ReadEachAdapter(...))

    // OEEResults: publish to MQTT5 AND SSE clients (fan-out)
    domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(
        client,
        "machines/{machineID}/oee",
        format.JSON(domain.OEECodec),
        mqtt5.PublishAdapterOptions{},
    ))
    domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(
        mux, "/machines/oee/stream",
        domain.OEECodec,
        nethttp.SSEAdapterOptions{},
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
- Testing the pipeline uses `forge.ChanSourceAdapter` / `forge.ChanSinkAdapter` — no broker

---

## Scope decisions

| In scope — Phase 1 | Out of scope (deferred) |
|--------------------|------------------------|
| `forge.IOParam` — protocol-agnostic parameter with codec | `forge.App` lifecycle manager |
| `forge.SourcePort[T]` — inbound boundary | Cache ports (`HandlerLatest`, `ServeLatest`) — different pattern |
| `forge.SinkPort[T]` — outbound boundary | `AsPipelineFunc` adapter bindings — non-stream server pattern |
| `forge.IOPort[Req, Resp]` — intermediate transform | `adapters/chi` bindings — same API as nethttp, add after |
| `forge.SourceAdapter[T]`, `SinkAdapter[T]`, `IOAdapter[Req,Resp]` interfaces | `adapters/mcpgo` bindings — request-response, different pattern |
| Fan-in (multiple SourceAdapters → merged stream) | Dynamic rebinding at runtime |
| Fan-out (SinkPort broadcasts to all SinkAdapters) | Auto OpenAPI/AsyncAPI spec from ports alone |
| `forge.ChanSourceAdapter[T]`, `forge.ChanSinkAdapter[T]` — test helpers | IOParam ↔ adapter template cross-validation (advisory only in Phase 1) |
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

## API surface — `forge` package

### Port options (sealed option interface)

```go
// PortOpt is the sealed option interface for port constructors.
// Accepted by NewSourcePort, NewSinkPort, NewIOPort.
type PortOpt interface { applyPort(*portConfig) }

// Params adds IOParam declarations. Each param is validated by bound adapters.
func Params(params ...IOParam) PortOpt

// PortBuffer sets the internal channel buffer size. Default 0.
func PortBuffer(n int) PortOpt

// PortObserver sets the observer for port lifecycle events (bind/activate).
// When nil, resolved from ctx at Bind time.
func PortObserver(obs stats.Observer) PortOpt
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
func NewSourcePort[T any](name string, codec codex.Codec[T], opts ...PortOpt) *SourcePort[T]

// Bind activates a SourceAdapter, merging its output into this port's stream.
// Returns PortBindError when the adapter's Activate call fails.
// Bind must be called before Stream.
func (p *SourcePort[T]) Bind(ctx context.Context, a SourceAdapter[T]) error

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
func NewSinkPort[T any](name string, codec codex.Codec[T], opts ...PortOpt) *SinkPort[T]

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
    name     string,
    reqCodec  codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts     ...PortOpt,
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

Ports use `stats.Observer` (same as adapters). Resolved from ctx at `Bind`/`Stream`/
`Connect` time when nil (standard nil-guard pattern).

Observer fires at:
- `Bind` / `Activate`: `RecordRequest("port.bind", portName, 200/500, duration)`
- Per-item through IOPort: adapters retain their existing observer calls — no double-calling

`stats.TraceObserver` type-asserted for port lifecycle spans: `"port.bind"`,
`"port.activate"`. Adapter-internal spans unchanged.

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

## Files to create / modify

| File | Action | Responsibility |
|------|--------|----------------|
| `forge/io_param.go` | Create | `IOParam`, `Params(...)`, `PortOpt` interface, `PortBuffer`, `PortObserver` |
| `forge/port_errors.go` | Create | `PortBindError`, `PortNoAdapterError` |
| `forge/source_port.go` | Create | `SourcePort[T]`, `SourceAdapter[T]` interface |
| `forge/sink_port.go` | Create | `SinkPort[T]`, `SinkAdapter[T]` interface |
| `forge/io_port.go` | Create | `IOPort[Req,Resp]`, `IOAdapter[Req,Resp]` interface |
| `forge/test_adapters.go` | Create | `ChanSourceAdapter[T]`, `ChanSinkAdapter[T]`, `FuncIOAdapter[Req,Resp]` |
| `forge/port_test.go` | Create | Tests T01–T16 |
| `forge/doc.go` | Modify | Add ports to package overview |
| `adapters/mqtt5/binding.go` | Create | `SubscribeAdapter`, `PublishAdapter`, `CallAdapter` |
| `adapters/mqtt5/binding_test.go` | Create | Tests T17–T18 |
| `adapters/mqtt/binding.go` | Create | `SubscribeAdapter`, `PublishAdapter` |
| `adapters/nethttp/binding.go` | Create | `IngestAdapter`, `SSEAdapter`, `PollAdapter`, `CallAdapter`, `DrainCallAdapter` |
| `adapters/zeromq/binding.go` | Create | `SubscribeAdapter`, `PublishAdapter`, `CallAdapter` |
| `adapters/file/binding.go` | Create | `ScanAdapter`, `WatchAdapter`, `ReadEachAdapter`, `DrainWriteAdapter`, `DrainWriteFileAdapter` |
| `adapters/file/binding_test.go` | Create | Test T19 |
| `adapters/sql/binding.go` | Create | `QueryAdapter`, `QueryEachAdapter`, `DrainInsertAdapter` |
| `adapters/sql/binding_test.go` | Create | Test T20 |
| `adapters/mqtt5/stream.go` | Modify | Mark `SubscribeStream`, `DrainPublish`, `CallStream` as deprecated |
| `adapters/mqtt/stream.go` | Modify | Mark `SubscribeStream`, `DrainPublish` as deprecated |
| `adapters/nethttp/stream.go` | Modify | Mark ingest/SSE/poll/call bridges as deprecated |
| `adapters/zeromq/stream.go` | Modify | Mark `SubscribeStream`, `DrainPublish`, `CallStream` as deprecated |
| `adapters/file/stream.go` | Modify | Mark `ScanStream`, `WatchStream`, `ReadEachStream`, `DrainWrite`, file write bridges as deprecated |
| `adapters/sql/stream.go` | Modify | Mark `QueryStream`, `DrainInsert`, `QueryEachStream` as deprecated |

---

## Out of scope — Phase 2

- **`forge.App`** — lifecycle manager that tracks all ports, ensures all ports are
  bound before Start, provides graceful shutdown and port dependency graph.
- **Cache ports** — `HandlerLatest` / `ServeLatest` are reactive cache patterns
  (not a stream-to-stream transform); they need a separate `CachePort[T]` concept.
- **`adapters/chi/binding.go`** — same API as nethttp; add after nethttp bindings.
- **`adapters/mcpgo/binding.go`** — MCP is request/response (not pub/sub stream);
  needs separate design for `ToolPort[In,Out]`.
- **`zeromq.AsPipelineFunc`** / **`mqtt5.AsPipelineFunc`** — adapts forge functions
  for server loops; not a port pattern.
- **Auto spec generation from ports** — `port.RegisterToBuilder(b)` to produce
  AsyncAPI/OpenAPI spec from port IOParams + adapter topic/path templates without
  a separately declared Channel/Route.
- **IOParam ↔ adapter cross-validation** — verify that `{name}` placeholders in
  adapter templates match port IOParam names at Bind time.
- **Dynamic rebinding** — hot-swap adapter on a running port.
- **Port introspection / pipeline visualisation**.

---

## Open design decisions

| Question | Options | Current preference |
|----------|---------|-------------------|
| **Adapter interface sealed or open?** | Sealed (unexported methods) — prevents external implementations | Open (exported methods) — user can write custom adapters | **Open** — extensibility wins; document that constructors are preferred |
| **Where do adapter interfaces live?** | `forge` package (current proposal) | `stream` package (lower in dep graph) | New `wire` package | **`forge`** — ports and interfaces are application-composition concerns |
| **Fan-out error policy** | One sink failure → log + continue (OnError callback) | One sink failure → terminate all sinks | **Log + continue** — a broken MQTT connection should not stop SSE delivery |
| **TapWriteFile deprecation** | Deprecate — `DrainWriteFileAdapter` with `stream.Tap` pattern replaces it | Keep as convenience | **Deprecate** — `DrainWriteFileAdapter` covers the pattern cleanly |
| **`adapterName()` method** | Unexported (seals interface) | Return string constant for error context | **Exported `AdapterName() string`** if interfaces are open; name used in PortBindError |
| **IOParam validation timing** | At Bind time (advisory: warn if template mismatches params) | At Connect/Stream time (per item) | At Bind time for template mismatch; per-item for value codec validation |
