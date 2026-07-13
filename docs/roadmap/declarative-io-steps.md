# Declarative I/O Steps in Stream Pipelines

> **Status:** ✅ `nethttp.CallStream` implemented. MQTT `SubscribeStream` ergonomic fix and `file.ReadEachStream` remain deferred.
> [← Back to Roadmap](index.md)

---

## Design vision

Stream pipelines should have two explicit, composable building blocks:

```
stream.Apply(ctx, src, forgeFunction, opts)          → pure computation step
transport.CallStream(ctx, transport, handle, src, opts) → declarative I/O step
```

A **declarative I/O step** works like this:
- You pass a **route handle** or **channel handle** — the handle carries the schema
  (input codec, output codec, path/topic params, security requirements)
- The step sends each stream item as a request using that handle's full codec machinery:
  codec validation, param encoding, security credentials, structured errors, observer calls
- The response is emitted as the next stream item

This is the same "declare the contract once, wire anywhere" principle that applies to
HTTP routes (`rest.NewRoute`), MQTT channels (`events.NewChannel`), and ZeroMQ routes
(`reqreply.NewRoute`) — extended to the stream level.

---

## What exists today

The pattern is already implemented for ZeroMQ and MQTT5:

### `zeromq.CallStream` ✅

```go
// Declared once (same as zeromq.Serve):
computeHandle, _ := ComputeRoute.Register(builder)

// Used as a declarative I/O step in any pipeline:
responses := zeromq.CallStream(ctx, reqSock, computeHandle, requestStream,
    zeromq.CallStreamOptions{Vars: vars, Observer: obs, Buffer: 4})
// responses: Stream[ComputeResp] — each item is the ZeroMQ reply
```

`computeHandle` carries `Req` and `Resp` codecs and validates both on every call.
Errors go to `Stream.Errors` as typed `CallError` — fully `errors.As`-navigable.

### `mqtt5.CallStream` ✅

```go
// MQTT5 request-reply per stream item:
responses := mqtt5.CallStream(ctx, client, router, serviceHandle, requestStream, callOpts)
// serviceHandle: *reqreply.RouteHandle[Req, Resp]
```

---

## Gaps

### `nethttp.CallStream` ✅ — Implemented

`nethttp.CallStream` is now implemented in `adapters/nethttp/stream.go`. HTTP enrichment pipelines can use the declarative I/O step pattern:

The correct pattern — identical to ZeroMQ:

```go
// Declared once — same handle used by the server AND the client stream step:
enrichHandle, _ := rest.NewRoute[IntermediaryData, EnrichedData](
    "POST", "/enrich",
    intermediaryCodec, enrichedCodec, rest.RouteMeta{},
).Register(builder)

// Declarative I/O step in the pipeline:
enriched := nethttp.CallStream(ctx, httpClient, "http://enrichment-svc:8080",
    enrichHandle, intermediaryStream,
    nethttp.CallStreamOptions{
        Vars:     nil,        // no path vars on this route
        CallOpts: callOpts,   // credentials, query/header params, observer
        Buffer:   4,
    })
// enriched: Stream[EnrichedData]
// Full codec validation on both IntermediaryData and EnrichedData
// Errors → UnexpectedStatusError, RequestError, ResponseBodyError in Stream.Errors
```

---

## API design — `nethttp.CallStream`

### Function signature

```go
// adapters/nethttp/stream.go (addition)

// CallStream sends each request item from src to handle using [Call],
// emitting each decoded response to the returned [gstream.Stream].
// All codec validation runs per item: path vars, query/cookie/header params,
// security credentials, request body encode, response body decode.
// Call errors (UnexpectedStatusError, RequestError, etc.) are sent to
// [gstream.Stream.Errors]. The stream terminates when src closes or ctx
// is cancelled.
//
// Requests are issued sequentially. Use multiple parallel pipelines feeding
// the same handle for concurrent throughput.
func CallStream[Req, Resp any](
    ctx     context.Context,
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    src     gstream.Stream[Req],
    opts    CallStreamOptions,
) gstream.Stream[Resp]
```

### Options struct

```go
// CallStreamOptions configures [CallStream].
type CallStreamOptions struct {
    // Vars, when non-nil, substitutes {varName} placeholders in the route's
    // path template via [rest.RouteHandle.BuildPath]. The same map is used for
    // every request (static path vars only). For routes with no path vars, omit Vars.
    //
    // For per-item path var substitution (e.g. {sensorID} derived from each Req),
    // encode the path variable into the Req body codec and use a route with no path
    // params — or use the Req struct fields to set QueryParams in CallOpts per item.
    Vars map[string]string

    // CallOpts are forwarded to [Call] for each item — including Observer,
    // CredentialFunc, QueryParams, CookieParams, HeaderParams, and ExtraHeaders.
    // [stats.Observer.RecordRequest] fires for every call attempt.
    CallOpts CallOptions

    // Buffer is the output Stream channel buffer size. Default 0.
    Buffer int
}
```

### Codec coverage per item

Every `Call` invocation validates all HTTP layers:

```
Encode Req → BuildPath(Vars) → ValidateQuery → ValidateCookies → ValidateHeaders
→ security (CredentialFunc) → HTTP request
← HTTP response → ValidateStatusCode → Decode Resp (body codec)
```

Failures at any layer produce typed errors in `Stream.Errors`:
`rest.PathParamError`, `rest.MissingPathVarError`, `UnexpectedStatusError`,
`RequestError`, `ResponseBodyError` — all `errors.As`-navigable + `slog.LogValuer`.

---

## The complete transport matrix

| Transport | Source | Intermediate (CallStream) | Sink |
|-----------|--------|--------------------------|------|
| HTTP | `PollStream` | ✅ `CallStream` | `DrainCall` |
| ZeroMQ | `SubscribeStream` | ✅ `CallStream` | `DrainPublish` |
| MQTT5 | `SubscribeStream` | ✅ `CallStream` | `DrainPublish` |
| MQTT | `SubscribeStream` | — (no request-reply) | `DrainPublish` |
| SQL | `QueryStream` | — (see below) | `DrainInsert` |
| File | `ScanStream`, `WatchStream` | — (see below) | `DrainWrite` |

---

## File and SQL — why `CallStream` does not apply

### SQL — `QueryStream` covers the intermediate case

SQL `QueryStream` polls a query at an interval. For per-item parameterized lookups,
the query function already receives the item's context and can use per-item parameters:

```go
// Per-item SQL lookup is already composable via CombineLatest or a custom source:
configs := sql.QueryStream(ctx, configCodec,
    func(ctx context.Context) ([]Config, error) {
        return db.ListActiveConfigs(ctx) // full result set on each poll
    }, 5*time.Minute, opts)

// Combine latest config with incoming stream:
combined := stream.CombineLatest2(ctx, sensorStream, configs,
    func(s Sensor, cs []Config) EnrichInput { return findConfig(s, cs) })
```

For per-item parameterized lookups, `sql.QueryEachStream[In,T]` is now available:

```go
thresholds := sql.QueryEachStream(ctx, thresholdCodec, sensorStream,
    func(ctx context.Context, s Sensor) ([]Threshold, error) {
        return db.GetThresholdBySensor(ctx, s.ID)
    }, sql.QueryEachStreamOptions{Table: "thresholds", Op: "get_by_sensor"})
```

### File — `WatchStream` + `CombineLatest2` covers the dynamic config case

```go
// Dynamic config: reload on file change + combine with sensor stream:
configs := /* WatchStream + FlatMapSlice + format.File.Read */
combined := stream.CombineLatest2(ctx, sensorStream, configs,
    func(s Sensor, c Config) EnrichInput { return EnrichInput{s, c} })
```

`file.TapWriteFile` and `file.DrainWriteFile` are now implemented for the sink position.
A `file.ReadEachStream` operator (per-item file read with different path vars) would
add value for large-scale per-item template lookups. This is tracked as a potential Phase 2 item:

```go
// Potential Phase 2:
type FileReadStreamOptions struct {
    Combiner func(In, T) Out  // required — combines original item + file content
    Observer stats.Observer
    Buffer   int
}
func ReadEachStream[In, T, Out any](
    ctx     context.Context,
    f       format.File[T],
    src     gstream.Stream[In],
    varsFor func(In) map[string]string,
    opts    FileReadStreamOptions[In, T, Out],
) gstream.Stream[Out]
```

Deferred: the `WatchStream + CombineLatest2` pattern covers most use cases without
the combiner complexity.

---

## Implementation order

| Priority | Item | Complexity | Depends on |
|----------|------|-----------|-----------|
| ~~**High**~~ ✅ | `nethttp.CallStream` | Implemented — `CallStreamOptions{Vars, CallOpts, Buffer}` | — |
| **Low** (deferred) | `file.ReadEachStream` (Phase 2) | Medium — combiner function required | None |
