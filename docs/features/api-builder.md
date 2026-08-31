# API Builders — declare, register, handle

go-codex provides three transport-agnostic API builders — one for each layer 2 protocol:

| Builder | Package | Declares | Generates |
|---------|---------|----------|-----------|
| `rest.Builder` | `api/rest` | HTTP routes + params | OpenAPI 3.1 spec |
| `events.Builder` | `api/events` | Event channels + topic params | AsyncAPI 3.0 spec |
| `mcp.Builder` | `api/mcp` | Tools, Resources, Prompts | MCP JSON manifest |

All three follow the same pattern: **declare → register → handle**.

## Why a builder?

The builder is the single registration point that serves two purposes simultaneously:

1. **Spec generation** — `b.OpenAPISpec()` / `b.AsyncAPISpec()` / `b.MCPSpec()` derive the complete document from everything registered with `b`; nothing is defined twice
2. **Definition-time validation** — `Register(b)` validates path/topic templates, param names, and builder-level codecs immediately — before any request is ever received

The declarations themselves (`NewRoute`, `NewChannel`, `NewTool`) are **plain Go values**. No builder is needed to declare them. This means you can:

```go
// Declare once — pass around, store in packages, register with multiple builders
var createUser = rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec,
    rest.RouteMeta{OperationID: "createUser"},
)

// Register with a server builder for runtime + OpenAPI
serverHandle, _ := createUser.RegisterHandle(serverBuilder)
// Or get a client handle directly — no builder, no spec
clientHandle := createUser.ClientHandle()
```

## The common workflow

```
NewRoute / NewChannel / NewTool  ←── plain Go value (no builder)
    │
    └─ .Register(builder) ──→ Handle ──→ adapter (nethttp / mqtt / mcpgo)
                         └──→ builder.Spec() ──→ OpenAPI / AsyncAPI / MCP JSON
```

The returned `Handle` carries:
- `Decode(...)` — typed decode with codec validation
- `Encode(...)` — typed encode
- `BuildPath(vars)` / `BuildTopic(vars)` / `BuildURI(vars)` — template substitution with per-variable codec validation
- `Descriptor` — the spec descriptor (method, path/topic, params, responses)

## Builder options

Both `rest.Builder` and `events.Builder` accept a builder-level codec that validates every path/topic registered with that builder:

```go
// REST — validate every path at Register time
b := rest.NewBuilder(info,
    rest.WithPathConstraints(validate.HTTPPath),     // built-in path rules
    rest.WithPathConstraints(myDomainPathConstraint), // compose custom rules
)

// Events — validate every topic at Register time
b := events.NewBuilder(info,
    events.WithTopicConstraints(validate.MQTTPublishTopic),
    events.WithTopicConstraints(mySensorTopicConstraint),
)
```

| REST option | Effect |
|---|---|
| `WithPathCodec(c)` | Validates every path against codec `c` at `Register` time |
| `WithPathConstraints(cs...)` | Builds a String codec from constraints; validates at `Register` time |

| Events option | Effect |
|---|---|
| `WithTopicCodec(c)` | Validates every topic against codec `c` at `Register` time |
| `WithTopicConstraints(cs...)` | Builds a String codec from constraints; validates at `Register` time |

If validation fails, `Register` returns an error immediately — no route/channel is added to the builder, and the problem surfaces at startup, not at request time.

## Template-transparent validation

Path templates like `/users/{id}` and topic templates like `sensors/{sensorID}/readings` contain `{varName}` placeholders. Builder-level codecs validate the **structural shape** of the template, not the placeholder content.

Before validation runs, placeholders are replaced with `x`:
- `/users/{id}` → `/users/x` (before `validate.HTTPPath` runs)
- `sensors/{sensorID}/readings` → `sensors/x/readings` (before `validate.MQTTPublishTopic` runs)

This means any constraint — including constraints that reject `{` or `}` characters — works correctly on parameterised templates. The stored `Descriptor.Path` / `ChannelHandle.Topic` is always the original template.

## Per-variable codec validation

Individual `{varName}` placeholders get their own codecs via `PathParam.WithCodec` / `TopicParam.WithCodec`. These run at **call time** (when `BuildPath`/`BuildTopic` is called), not at `Register` time:

```go
uuidCodec := codex.String().Refine(validate.UUID)

getUser, _ := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
    codex.Empty, userCodec,
    rest.PathParam{Name: "id"}.WithCodec(uuidCodec), // validates {id} at BuildPath time
).RegisterHandle(b)

// At call time — uuid codec rejects non-UUID values
path, err := getUser.BuildPath(map[string]string{"id": "not-a-uuid"})
// err: rest.PathParamError{Name: "id", Value: "not-a-uuid", Err: ...}
```

The codec schema flows into the spec automatically — no manual annotation needed.

## Final path/topic re-validation

After `BuildPath`/`BuildTopic` substitutes all variables, the **assembled string** is re-validated against the builder-level codec. This catches variable values that individually pass their per-param codec but together violate the global constraint:

```go
// Suppose the builder has WithPathConstraints(validate.HTTPPath).
// A path variable value with a space would pass the non-empty UUID codec
// but the assembled path "/users/hello world" would fail validate.HTTPPath.
// BuildPath catches this BEFORE the URL is ever sent.
```

This gives you a two-stage guarantee:
1. Per-variable codec validates the semantics of each placeholder value
2. Global codec validates the shape of the final assembled path/topic

## Spec generation

The builder accumulates all registered routes/channels/tools. Call `Spec()` at any time:

```go
// REST
doc, err := b.OpenAPISpec()
yamlBytes, _ := doc.MarshalYAML()

// Events
doc, err := b.AsyncAPISpec()
yamlBytes, _ := doc.MarshalYAML()

// MCP
spec, err := b.MCPSpec()
jsonBytes, _ := json.MarshalIndent(spec, "", "  ")
```

All param codecs, security schemes, response headers, and content types flow into the spec automatically from their declarations.

## See also

- [Feature: REST API & HTTP Adapters](rest-api.md) — full REST builder reference
- [Feature: Event Channels & MQTT](events.md) — full events builder reference
- [Feature: MCP Server](mcp.md) — full MCP builder reference
- [Concept: API Contracts](../concepts/api-contracts.md) — the declare → register → handle mental model
