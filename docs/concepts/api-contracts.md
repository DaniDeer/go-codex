# API Contracts

> See also: [`api/rest`](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`api/events`](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/events) · [`api/mcp`](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/mcp)

Layer 2 builds on codecs to declare typed API contracts as values. The same declaration drives runtime behaviour (decode/encode/validate) **and** spec generation (OpenAPI, AsyncAPI, MCP schema) — no duplication.

## Workflow: declare → register → handle

```
NewRoute / NewChannel / NewTool
    │
    └─ .Register(builder) ──→ Handle ──→ adapter (nethttp / mqtt / mcpgo)
                         └──→ builder.Spec() ──→ OpenAPI / AsyncAPI / MCP JSON
```

## Design principle: one struct, one call

For any API-contract boundary with a request/response shape (or a duplex
role pair — publisher/subscriber, requestor/replier, client/server), a
caller on **either side** should be able to do the entire encode-or-decode
direction with **one struct value in (or out), one call** — no manual
map-building, no manual header/cookie/query/topic stitching, in the common
case. The struct itself can be built however the caller likes: a plain
literal, or their own `New...` constructor function — ordinary Go, no
framework sugar required for that part.

REST (`api/rest` + `adapters/nethttp`/`chi`) delivers this today, both
directions, both roles:

```go
// Client: ONE struct in, ONE struct out.
handle := getUserActivity.ClientHandle()
req := GetUserActivityReq{ID: userID, Filter: "logins"} // literal, or a New... factory
resp, err := nethttp.CallHandle(ctx, client, baseURL, handle, req, nethttp.CallOptions{})
// resp is fully decoded AND merged — body + response header/cookie fields
// (e.g. resp.RequestID) are all populated. Nothing else to do.

// Server: ONE struct in, ONE struct out.
nethttp.Register(mux, handle, func(ctx context.Context, req GetUserActivityReq) (User, error) {
    u := lookup(req.ID)     // req arrives fully merged: path+query+header+cookie+body
    u.RequestID = traceID() // just set the field — no w.Header().Set() call
    return u, nil           // adapter auto-encodes body AND response merge fields
}, nethttp.Options{})
```

This is made possible by declare-once constructors
(`rest.NewPathParam[T]`/`NewRequiredQueryParam[T]`/etc. for the request,
`NewRequiredResponseHeaderParam[Resp]`/etc. for the response) that register
BOTH the spec Param (still driving OpenAPI generation) AND a merge field in
one call — see [Concept: Codec — Reusing Field declarations](codec.md#reusing-field-declarations-for-pathtopicheaderquery-vars)
for the underlying mechanism, and the [REST API feature](../features/rest-api.md)
for the full reference. Plain, validate-only Param structs remain available
as the escape hatch for params a handler never reads/writes directly — a
route can freely mix both styles.

The promise also holds for NESTED structs (`Req`/`Resp` composed from
sub-structs like `Meta`/`Payload` instead of flat fields) and for non-JSON
body formats (Gob, binary, or any custom `format.Format[T]`) — merge-field
`get`/`set` are plain closures, not reflection, so nested access needs no
framework change; and body decode/encode is orthogonal to var-merge, so any
format composes. See
[REST API — Nested structs & binary body formats](../features/rest-api.md#nested-structs-binary-body-formats)
and `examples/rest-nested-binary` for the full runnable version.

**Shipped for `api/events` (pub/sub) and `api/reqreply` (req/reply) too**:
`events.NewTopicParam[T]`/`ChannelHandle.DecodeMerged`/`mqtt5.PublishHandle`
and `reqreply.NewTopicParam[T]`/`RouteHandle.DecodeMerged`/`mqtt5.CallHandle`
close the same loop for MQTT pub/sub and request/reply. Req/reply routes can
also declare dedicated, RUNTIME-WIRED error-reply channels via
`reqreply.ErrorPattern` on `NewRoute(...)` — one declaration drives both the
AsyncAPI reply-error channel/operation AND the actual `mqtt5`/`zeromq` Serve
reply behavior (matched errors get a typed codec-backed payload instead of a
plain-text string). `reqreply.ErrorReplyMeta` remains available for
spec-only declarations with no runtime dispatch — see
[Feature: Event Channels & MQTT](../features/events.md#topic-vars-with-automatic-merge-newtopicparam)
and the [AsyncAPI guide](../guides/asyncapi.md#declaring-dedicated-reqreply-error-channels).

**Shipped for the `ports.Pattern` BINDING LAYER too**:
`DrainCallAdapter`/`PublishAdapter`/`CallAdapter` across
`nethttp`/`mqtt5`/`zeromq`/`mqtt` delegate to `CallHandle`/`PublishHandle`
and derive vars PER-ITEM whenever their `Vars` option is left `nil` —
streaming/port-based callers get the same one-struct convenience as
calling the transport function directly. Set `Vars` to a non-nil map
(even an empty one) to keep a single static vars map for every item in the
stream instead — the escape hatch, unchanged from before this was added.
`adapters/zeromq`'s own pub/sub `Subscribe`/`Publish` and `adapters/mqtt`
(v3) events also received the same merge-field wiring `adapters/mqtt5`
had first. A shared, module-internal `internal/templatematch` package now
backs the topic/path-matching core for `mqtt`/`mqtt5`/`zeromq`/`ports.File`.
SSE and WebSocket now ship the same convenience for long-lived connections
via connection-level merge constructors (`rest.NewRequiredSSEEventParam`,
`ports.NewRequiredSocketInParam`, `ports.NewRequiredSocketOutParam`) with
adapter-side auto-merge on each event/frame.

### Where this convenience is shipped

Every boundary below has (1) a declare-once constructor that registers
BOTH a spec Param/key/path-var AND a merge field in one call, and (2) a
single-call convenience wrapper on the encode side, with automatic merge
wiring on the decode side. This is the reference matrix — every NEW
adapter/port should match this shape (see the `add-a-new-adapter` skill's
Step 5b).

| Boundary | Declare-once constructor | Single-call convenience | Reference |
|---|---|---|---|
| REST (`api/rest` + `adapters/nethttp`/`chi`) | `rest.NewPathParam[T]`/`NewRequiredQueryParam[T]`/etc. + `NewRequiredResponseHeaderParam[Resp]`/etc. | `nethttp.CallHandle` (client) + `Handler` auto-merge (server) | [Feature: REST API](../features/rest-api.md#one-line-client-calls-callhandle) |
| REST SSE (`api/rest` + `adapters/nethttp`/`chi`) | `rest.NewRequiredSSEEventParam[T]`/`NewOptionalSSEEventParam[T]` | `send(event)` on `SSEHandler`/`RegisterSSE` auto-merges path/query/header/cookie vars into each event | [Feature: SSE & Streaming](../features/sse-streaming.md#one-struct-one-call-for-sse-events) |
| Events pub/sub (`api/events` + `adapters/mqtt`/`mqtt5`/`zeromq`) | `events.NewTopicParam[T]` | `mqtt5.PublishHandle`/`zeromq.PublishHandle`/`mqtt.PublishHandle` (publish) + `Subscribe`/`SubscribeHandler` auto-merge (subscribe) | [Feature: Event Channels & MQTT](../features/events.md#topic-vars-with-automatic-merge-newtopicparam) |
| Req/reply (`api/reqreply` + `adapters/mqtt5`/`zeromq`) | `reqreply.NewTopicParam[T]` (Req-side only) | `mqtt5.CallHandle`/`zeromq.CallHandle` (client) + `mqtt5.Serve` auto-merge (server) | [MQTT 5 Guide — Request/Reply](../guides/mqtt5.md) |
| WebSocket (`ports.DuplexPort` + `adapters/websocket`) | `ports.NewRequiredSocketInParam[T]`/`NewOptionalSocketInParam[T]` + `ports.NewRequiredSocketOutParam[T]`/`NewOptionalSocketOutParam[T]` on `SocketPattern` | `DuplexSocketAdapter`/`IngestSocketAdapter`/`BroadcastSocketAdapter` auto-merge connection vars into inbound/outbound payload structs | [Feature: WebSocket](../features/websocket.md#one-struct-one-call-with-connection-vars) |
| `ports.Pattern` binding layer (`nethttp`/`mqtt5`/`zeromq`/`mqtt`) | n/a — delegates to the underlying transport's constructors above | `DrainCallAdapter`/`PublishAdapter`/`CallAdapter` derive vars per-item when `Vars` is left `nil` | [Feature: Ports](../features/ports.md#available-adapters-by-transport) |
| MCP Resources (`api/mcp` + `adapters/mcpgo`) | URI `{varName}` template (validate-only `ResourceParam`, not merge-capable — see below) | `ResourceHandle.ExtractURIVars` + `mcpgo.RegisterResourceWithVars`/`ResourceHandlerWithVars` (additive; `RegisterResource`/`ResourceHandlerFunc` unchanged) | [Feature: MCP Server](../features/mcp.md#automatic-uri-var-extraction-extracturivars-registerresourcewithvars) |
| File I/O (`ports.File` + `adapters/file`) | `ports.NewFilePathParam[T]` | `ports.WriteHandle` (write) + `File.ReadMerged` auto-merge (read, wired into `ReadEachAdapter`/`ReadAdapter`; `DrainWriteFileAdapter`'s `varsFor` may be `nil`) | [Feature: Ports](../features/ports.md) |
| Cache (`ports.Cache` + `adapters/redis`) | `ports.NewCacheKeyParam[T]` | `redis.SetHandle` (write) + `redis.GetMerged` auto-merge (read, wired into `GetAdapter`; `SetAdapter`/`DrainSetAdapter`'s `keyFn` may be `nil`) | [Feature: Redis Cache Adapter](../features/redis.md) |

**Not merge-capable, by explicit design decision — not a gap**:
MCP Resources' URI vars use validate-only `ResourceParam` (no getter/setter
merge into the resource's output type — `T` is application-produced, not
wire-decoded, so "merge after the handler runs" is a narrower win than
elsewhere); MCP Prompts' args are validated (`ValidateArgs`) but handed to
the app as a raw `map[string]string`, not a merged struct.

## REST routes (`api/rest`)

```go
var createUser = rest.NewRoute[CreateUserReq, User](
    "POST", "/users",
    createUserReqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser", Summary: "Create a user"},
    rest.PathParam{Name: "id"}.WithCodec(uuidCodec),
)

handle, _ := createUser.Register(builder)
// handle.Decode(body)        → typed CreateUserReq, validated
// handle.Encode(user)        → JSON bytes
// handle.BuildPath(vars)     → concrete path, validates params
// builder.OpenAPISpec()      → full OpenAPI 3.1 document
```

For the HTTP client side, use `ClientHandle()` — no builder needed:

```go
handle := createUser.ClientHandle()
user, err := nethttp.Call(ctx, http.DefaultClient, serverURL, handle, req, nil, opts)
```

## Event channels (`api/events`)

```go
var readingsChannel = events.NewChannel[SensorReading](
    "sensors/{sensorID}/readings",
    sensorReadingCodec,
    events.Subscribe{OperationID: "receiveSensorReading"},
    events.Publish{OperationID: "publishSensorReading"},
    events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
)

handle, _ := readingsChannel.Register(builder)
// handle.Decode(payload)      → typed SensorReading
// handle.BuildTopic(vars)     → concrete topic, validates params
// builder.AsyncAPISpec()      → full AsyncAPI 3.0 document
```

## MCP tools (`api/mcp`)

```go
searchTool := mcp.NewTool[SearchReq, SearchResp](
    "search", searchReqCodec, searchRespCodec,
    mcp.ToolMeta{Description: "Search the knowledge base"},
)

handle := searchTool.Register(builder)
// handle.Decode(args)         → typed SearchReq
// handle.Encode(result)       → JSON bytes for MCP protocol
// builder.MCPSpec()           → MCP tool manifest JSON
```

## See also

- [Feature: REST API & HTTP Adapters](../features/rest-api.md) — full REST builder reference
- [Feature: HTTP Client](../features/http-client.md) — `Call`, `ClientHandle`, typed client
- [Feature: Event Channels & MQTT](../features/events.md) — full events builder reference
- [Feature: MCP Server](../features/mcp.md) — Tools, Resources, Prompts
- [Feature: API Builders (overview)](../features/api-builder.md) — builder pattern, template-transparent validation, spec generation
