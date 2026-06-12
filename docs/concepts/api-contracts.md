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
