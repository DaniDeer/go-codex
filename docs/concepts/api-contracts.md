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
close the same loop for MQTT pub/sub and request/reply — see
[Feature: Event Channels & MQTT](../features/events.md#topic-vars-with-automatic-merge-newtopicparam).

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
had first. See
[Roadmap: Merge-Field Remaining Gaps](../roadmap/merge-field-remaining-gaps.md)
for the remaining low-priority backlog (SSE merge support, a shared
non-wildcard topic-template core).

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
