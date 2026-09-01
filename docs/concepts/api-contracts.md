# API Contracts

> See also: [`api/rest`](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`api/events`](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/events) · [`api/mcp`](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/mcp) · [Declaring APIs and Ports](declaring-apis-and-ports.md) (constructor/escape-hatch comparison across all six boundaries)

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
resp, err := nethttp.CallWithHandle(ctx, client, baseURL, handle, req, nethttp.CallOptions{})
// resp is fully decoded AND merged — body + response header/cookie fields
// (e.g. resp.RequestID) are all populated. Nothing else to do.

// Server: ONE struct in, ONE struct out.
route := getUserActivity.WithHandler(func(ctx context.Context, req GetUserActivityReq) (User, error) {
    u := lookup(req.ID)     // req arrives fully merged: path+query+header+cookie+body
    u.RequestID = traceID() // just set the field — no w.Header().Set() call
    return u, nil           // adapter auto-encodes body AND response merge fields
})
route.Register(builder)
nethttp.Serve(mux, builder)
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
| REST (`api/rest` + `adapters/nethttp`/`chi`) | `rest.NewPathParam[T]`/`NewRequiredQueryParam[T]`/etc. + `NewRequiredResponseHeaderParam[Resp]`/etc. | `nethttp.Call`/`CallWithHandle` (client) + `Serve`/`Handler` auto-merge (server) | [Feature: REST API](../features/rest-api.md#one-line-client-calls-nethttpcall) |
| REST SSE (`api/rest` + `adapters/nethttp`/`chi`) | `rest.NewRequiredSSEEventParam[T]`/`NewOptionalSSEEventParam[T]` | `send(event)` on `SSEHandler`/`RegisterSSE` auto-merges path/query/header/cookie vars into each event | [Feature: SSE & Streaming](../features/sse-streaming.md#one-struct-one-call-for-sse-events) |
| Events pub/sub (`api/events` + `adapters/mqtt`/`mqtt5`/`zeromq`) | `events.NewTopicParam[T]` | `mqtt5.PublishHandle`/`zeromq.PublishHandle`/`mqtt.PublishHandle` (publish) + `Subscribe`/`SubscribeHandler` auto-merge (subscribe) | [Feature: Event Channels & MQTT](../features/events.md#topic-vars-with-automatic-merge-newtopicparam) |
| Req/reply (`api/reqreply` + `adapters/mqtt5`/`zeromq`) | `reqreply.NewTopicParam[T]` (Req-side only) | `mqtt5.CallHandle`/`zeromq.CallHandle` (client) + `mqtt5.Serve` auto-merge (server) | [MQTT 5 Guide — Request/Reply](../guides/mqtt5.md) |
| WebSocket (`ports.DuplexPort` + `adapters/websocket`) | `ports.NewRequiredSocketInParam[T]`/`NewOptionalSocketInParam[T]` + `ports.NewRequiredSocketOutParam[T]`/`NewOptionalSocketOutParam[T]` on `SocketPattern` | `DuplexSocketAdapter`/`IngestSocketAdapter`/`BroadcastSocketAdapter` auto-merge connection vars into inbound/outbound payload structs | [Feature: WebSocket](../features/websocket.md#one-struct-one-call-with-connection-vars) |
| `ports.Pattern` binding layer (`nethttp`/`mqtt5`/`zeromq`/`mqtt`) | n/a — delegates to the underlying transport's constructors above | `DrainCallAdapter`/`PublishAdapter`/`CallAdapter` derive vars per-item when `Vars` is left `nil` | [Feature: Ports](../features/ports.md#available-adapters-by-transport) |
| MCP Resources (`api/mcp` + `adapters/mcpgo`) | URI `{varName}` template — `ResourceHandle[V,T]` built directly on `codex.Template[V]` (a real typed vars container, not merge-capable into `T` — see below) | `ResourceHandle.ExtractURIVars` + `mcpgo.RegisterResourceWithVars`/`ResourceHandlerWithVars` (additive; `RegisterResource`/`ResourceHandlerFunc` unchanged) | [Feature: MCP Server](../features/mcp.md#automatic-uri-var-extraction-extracturivars-registerresourcewithvars) |
| File I/O (`ports.File` + `adapters/file`) | `ports.NewFilePathParam[T]` | `ports.WriteHandle` (write) + `File.ReadMerged` auto-merge (read, wired into `ReadEachAdapter`/`ReadAdapter`; `DrainWriteFileAdapter`'s `varsFor` may be `nil`) | [Feature: Ports](../features/ports.md) |
| Cache (`ports.Cache` + `adapters/redis`) | `ports.NewCacheKeyParam[T]` | `redis.SetHandle` (write) + `redis.GetMerged` auto-merge (read, wired into `GetAdapter`; `SetAdapter`/`DrainSetAdapter`'s `keyFn` may be `nil`) | [Feature: Redis Cache Adapter](../features/redis.md) |

**Not merge-capable, by explicit design decision — not a gap**:
MCP Resources' URI vars are a real typed `V` (via `codex.Template[V]`) but
never merge into the resource's output type `T` — `T` is
application-produced, not wire-decoded, so "merge after the handler runs"
is a narrower win than elsewhere; MCP Prompts' args are validated
(`ValidateArgs`) but handed to the app as a raw `map[string]string`, not a
merged struct.

## REST routes (`api/rest`)

```go
var createUser = rest.NewRoute[CreateUserReq, User](
    "POST", "/users",
    createUserReqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser", Summary: "Create a user"},
    rest.PathParam{Name: "id"}.WithCodec(uuidCodec),
)

handle, _ := createUser.RegisterHandle(builder)
// handle.Decode(body)        → typed CreateUserReq, validated
// handle.Encode(user)        → JSON bytes
// handle.BuildPath(vars)     → concrete path, validates params
// builder.OpenAPISpec()      → full OpenAPI 3.1 document
```

For the HTTP client side, `nethttp.Call` takes the `rest.Route` value
directly — no builder, no separate handle needed:

```go
caller := nethttp.NewCaller(http.DefaultClient, serverURL)
user, err := nethttp.Call(ctx, caller, createUser, req, opts)
```

### Why SSE lives in `api/rest`, not `api/events`

Server-Sent Events (`rest.NewSSERoute`) isn't "RESTful" by the strict
CRUD/cacheable-resource definition — but its WIRE PROTOCOL is plain HTTP:
a normal `GET` request whose response never closes, streaming
`text/event-stream` chunks instead of one JSON body. Because the
transport is ordinary HTTP, an SSE route reuses `api/rest`'s ENTIRE
existing toolchain — path/query/header params, security schemes,
`nethttp.Call`, OpenAPI generation — with zero new machinery, instead of
needing an AsyncAPI-shaped channel/message model built from scratch. This
also matches the wider OpenAPI-ecosystem convention: most tooling
(Swagger, FastAPI, NestJS, etc.) documents an SSE endpoint as a plain
`GET` operation with a streamed content type, not as an AsyncAPI channel.

AsyncAPI's channel/message model exists for genuinely broker/pub-sub-style
protocols (MQTT, Kafka, AMQP, WebSocket) where the interaction ISN'T a
single request/response pair. A DUAL description (OpenAPI as the primary
spec, plus an optional AsyncAPI channel describing the same event payload)
is technically possible in this codebase's renderer today —
`render/asyncapi`'s `Server.Protocol` field already accepts a bare string
like `"https"` (the same precedent `docs/roadmap/webhook-adapter.md` uses
for its own HTTP-based case) — but this is NOT wired up for SSE: the
HTTP-native OpenAPI description is already complete and sufficient, so no
second spec surface is needed.

See [`rest.NewSSERoute`'s godoc](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest#NewSSERoute)
and [Feature: SSE & Streaming](../features/sse-streaming.md) for the full
API.

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

### Reusing a Topic/Path/FilePathTemplate

The plain-string form above (`"sensors/{sensorID}/readings"` passed
directly to `NewChannel`) is the default and stays that way — nothing
below changes it. If the SAME topic template and `TopicParam` end up
declared for two or more channels of DIFFERENT payload types (e.g. one
topic family carrying several event types), extract the shape once as a
`Topic` value and reuse it:

```go
var sensorReadingsTopic = events.NewTopic("sensors/{sensorID}/readings",
    events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
)

var temperatureChannel = events.NewChannelFromTopic(sensorReadingsTopic, temperatureCodec,
    events.Subscribe{OperationID: "receiveTemperature"},
)
var humidityChannel = events.NewChannelFromTopic(sensorReadingsTopic, humidityCodec,
    events.Subscribe{OperationID: "receiveHumidity"},
)

// Standalone use — no payload codec involved at all:
topic, err := sensorReadingsTopic.BuildTopic(map[string]string{
    "sensorID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
})
```

`temperatureChannel`/`humidityChannel` are byte-for-byte identical to
channels declared via `NewChannel` with the same template and
`TopicParam` passed inline — the `{sensorID}` variable's name and codec
now have exactly one source of truth instead of being copy-pasted per
channel. `rest.Path`/`reqreply.Topic`/`mcp` Template+`NewResourceFromTemplate`/
`ports.FilePathTemplate`/`ports.DirPathTemplate` provide the identical
convenience for `api/rest` routes, `api/reqreply` routes, `api/mcp`
resources, and `ports.File`/`ports.Dir` declarations respectively. See
[Guide: Declarative wire-format vocabulary](../guides/wire-vocabulary.md#reusing-a-topicpathfilepathtemplate)
for the full recipe, and [Declaring APIs and Ports](declaring-apis-and-ports.md)
for the full comparison across all six boundaries.

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
