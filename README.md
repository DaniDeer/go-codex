# GO Codex

[![CI](https://github.com/DaniDeer/go-codex/actions/workflows/ci.yml/badge.svg)](https://github.com/DaniDeer/go-codex/actions/workflows/ci.yml)

## What is go-codex?

In standard Go, encoding, decoding, validation, and documentation are separate concerns that drift apart.
Rename a field and you must update struct tags, the validator, and the schema docs independently — one missed update causes a silent bug or a stale spec.

go-codex is inspired by Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec).
A single `Codec[T]` value is the source of truth for **encode**, **decode**, **validation**, and **schema** — written once, never duplicated.

### The Problem

```go
// Three separate sources of truth — they drift.
type User struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

func decodeUser(data []byte) (User, error) {
    var u User
    return u, json.Unmarshal(data, &u) // no validation
}

func validateUser(u User) error {
    if u.Name == "" {
        return errors.New("name: must not be empty")
    }
    if u.Age <= 0 {
        return errors.New("age: must be positive")
    }
    return nil
}

// Schema lives in a separate openapi.yaml — updated by hand.
```

### The Solution

```go
// One Codec[User] is encode + decode + validate + schema.
type User struct {
    Name string
    Age  int
}

var UserCodec = codex.Struct[User](
    codex.Field[User, string]{
        Name:     "name",
        Codec:    codex.String().Refine(validate.NonEmptyString),
        Get:      func(u User) string { return u.Name },
        Set:      func(u *User, v string) { u.Name = v },
        Required: true,
    },
    codex.Field[User, int]{
        Name:     "age",
        Codec:    codex.Int().Refine(validate.PositiveInt),
        Get:      func(u User) int { return u.Age },
        Set:      func(u *User, v int) { u.Age = v },
        Required: true,
    },
)

// Decode and validate in one step — error includes field path.
user, err := UserCodec.Decode(map[string]any{"name": "Alice", "age": 30})

// Encode back to the intermediate representation.
data, err := UserCodec.Encode(user)

// Schema derived automatically — no separate YAML needed.
schemaJSON, _ := json.MarshalIndent(UserCodec.Schema, "", "  ")
```

## Installation & Usage

```bash
go get github.com/DaniDeer/go-codex@latest
```

Requires Go 1.25 or later.

### Import paths

| Package                           | Import path                                    |
| --------------------------------- | ---------------------------------------------- |
| Core codecs                       | `github.com/DaniDeer/go-codex/codex`           |
| Format bridges (JSON, YAML, TOML) | `github.com/DaniDeer/go-codex/format`          |
| Built-in constraints              | `github.com/DaniDeer/go-codex/validate`        |
| HTTP route descriptors            | `github.com/DaniDeer/go-codex/route`           |
| REST API builder                  | `github.com/DaniDeer/go-codex/api/rest`        |
| Event channel builder             | `github.com/DaniDeer/go-codex/api/events`      |
| net/http adapter                  | `github.com/DaniDeer/go-codex/adapters/nethttp` |
| Paho MQTT adapter                 | `github.com/DaniDeer/go-codex/adapters/mqtt`   |
| OpenAPI 3.1 renderer              | `github.com/DaniDeer/go-codex/render/openapi`  |
| AsyncAPI 2.6 renderer             | `github.com/DaniDeer/go-codex/render/asyncapi` |
| Schema model                      | `github.com/DaniDeer/go-codex/schema`          |

## Features

- **Multi-Format Support** — one `Codec[T]` reads and writes JSON, YAML, and TOML unchanged
- **Encode, Decode, and Validation** — constraints run on decode; encode is trusted; validate is explicit
- **Builtin Format Constraints** — `email`, `uuid`, `url`, `date`, `date-time` validated and reflected into schema automatically
- **OpenAPI Schema Generation** — `components/schemas` map from codec-derived schemas, no manual YAML
- **Full OpenAPI 3.1 Document** — complete REST API spec (paths, operations, params) from `route.Route` descriptors
- **AsyncAPI 2.6 Document** — complete event-driven spec from channel descriptors; same schemas, no duplication
- **REST API Builder** — typed `Decode`/`Encode` helpers per route + OpenAPI spec generation, no HTTP library import
- **Event Channel Builder** — typed `Decode`/`Encode` helpers per channel + AsyncAPI spec generation, no messaging library import
- **net/http Adapter** — wire `RouteHandle` to `net/http.ServeMux` with one call; 400/500 error handling included
- **Paho MQTT Adapter** — wire `ChannelHandle` to Paho MQTT subscribe callbacks; context-aware publish

### Multi-Format Support

`Codec[T]` operates on an intermediate representation (`map[string]any`) that is format-agnostic.
The `format` package bridges that intermediate to concrete wire formats — the same codec reads and writes JSON, YAML, and TOML unchanged.

```go
jsonFmt := format.JSON(UserCodec)
yamlFmt := format.YAML(UserCodec)
tomlFmt := format.TOML(UserCodec)

// All three produce identical Go values; validation runs on all three.
user, err := jsonFmt.Unmarshal([]byte(`{"name":"Alice","age":30}`))
user, err  = yamlFmt.Unmarshal([]byte("name: Alice\nage: 30\n"))
user, err  = tomlFmt.Unmarshal([]byte("name = \"Alice\"\nage = 30\n"))

// Encode to any format.
jsonBytes, _ := jsonFmt.Marshal(user)
tomlBytes, _ := tomlFmt.Marshal(user)
```

Validation errors and field paths are identical regardless of which format is used.

### Encode, Decode, and Validation

#### The trust boundary

go-codex draws a deliberate line between trusted and untrusted data:

| Direction  | What runs                              | Rationale                                                                                                                                   |
| ---------- | -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **Decode** | type checks + all `Refine` constraints | Input comes from outside — JSON on the wire, YAML from a file, a CLI flag. You cannot trust it. Every constraint runs.                      |
| **Encode** | type conversion only                   | The Go value was constructed by your own code. You already trust it. Running constraints on every encode would be redundant and surprising. |

This mirrors the design of [autodocodec](https://hackage.haskell.org/package/autodocodec): constraints are a guard on ingress, not a restriction on your own domain logic.

#### Decode — validates automatically

```go
// Constraints run during Decode. Invalid input is rejected with field-path errors.
user, err := jsonFmt.Unmarshal([]byte(`{"name":"","age":-5}`))
// err: field name: constraint failed (non-empty): expected non-empty string
```

#### Encode — trusted, no constraints

```go
// Encoding the value you constructed always succeeds (no constraints run).
// You are responsible for the correctness of values you build.
data, err := jsonFmt.Marshal(User{Name: "", Age: -5}) // succeeds
```

#### Validate — explicit bidirectional check

When you need to validate a Go value you constructed — before storing it, after building it programmatically, or to surface errors early — call `Validate` explicitly. It reuses the exact same `Refine` constraints, with no duplication:

```go
// Codec.Validate — no format required.
if err := UserCodec.Validate(u); err != nil {
    return fmt.Errorf("constructed invalid user: %w", err)
}

// Format.Validate — same check, accessed through a Format binding.
if err := jsonFmt.Validate(u); err != nil {
    return err
}
```

`Validate` is always explicit. `Marshal` and `Encode` never silently validate.

### Builtin Format Constraints

`validate/` ships format constraints for common string types. Each constraint validates the value **and** annotates `schema.Schema` so the format appears in OpenAPI output automatically.

| Constraint          | Validates                        | OpenAPI format |
| ------------------- | -------------------------------- | -------------- |
| `validate.Email`    | `user@domain.tld`                | `email`        |
| `validate.UUID`     | RFC 4122 UUID (case-insensitive) | `uuid`         |
| `validate.URL`      | absolute http/https URL          | `uri`          |
| `validate.IPv4`     | dotted-decimal IPv4              | `ipv4`         |
| `validate.IPv6`     | IPv6 address                     | `ipv6`         |
| `validate.Date`     | `YYYY-MM-DD` (ISO 8601)          | `date`         |
| `validate.DateTime` | RFC 3339 date-time               | `date-time`    |
| `validate.Slug`     | `lowercase-hyphen-slug`          | `pattern`      |

```go
var ContactCodec = codex.Struct[Contact](
    codex.Field[Contact, string]{
        Name:     "email",
        Codec:    codex.String().Refine(validate.Email).WithDescription("Primary email."),
        Get:      func(c Contact) string { return c.Email },
        Set:      func(c *Contact, v string) { c.Email = v },
        Required: true,
    },
    codex.Field[Contact, string]{
        Name:     "id",
        Codec:    codex.String().Refine(validate.UUID),
        Get:      func(c Contact) string { return c.ID },
        Set:      func(c *Contact, v string) { c.ID = v },
        Required: true,
    },
)

// Decode validates format automatically — no extra step.
contact, err := ContactCodec.Decode(map[string]any{
    "email": "not-an-email",   // → constraint failed (email): invalid email address: "not-an-email"
    "id":    "bad-uuid",       // → constraint failed (uuid): invalid UUID: "bad-uuid"
})

// OpenAPI schema includes format: email, format: uuid automatically.
yamlBytes, _ := openapi.MarshalYAML(map[string]schema.Schema{"Contact": ContactCodec.Schema})
```

See `examples/formats/` for a runnable demo covering all constraints.

### OpenAPI Schema Generation

[Spec: openapis.org - OpenAPI 3.2.0](https://spec.openapis.org/oas/v3.2.0.html)

`Codec[T]` carries a `schema.Schema` that describes the type: field names, types, constraints, descriptions, and examples. The `render/openapi` package converts that schema into an OpenAPI 3.x `components/schemas` map — no manual YAML authoring, no drift.

```go
import (
    "github.com/DaniDeer/go-codex/render/openapi"
    "github.com/DaniDeer/go-codex/validate"
)

var UserCodec = codex.Struct[User](
    codex.Field[User, string]{
        Name: "name",
        Codec: codex.String().
            Refine(validate.NonEmptyString).
            Refine(validate.MaxLen(100)).
            WithTitle("Full Name").
            WithDescription("The user's full display name."),
        Get:      func(u User) string { return u.Name },
        Set:      func(u *User, v string) { u.Name = v },
        Required: true,
    },
    codex.Field[User, int]{
        Name: "age",
        Codec: codex.Int().
            Refine(validate.RangeInt(0, 150)).
            WithDescription("Age in years."),
        Get:      func(u User) int { return u.Age },
        Set:      func(u *User, v int) { u.Age = v },
        Required: true,
    },
)

// Render components/schemas as YAML — ready to paste into openapi.yaml.
yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
    "User": UserCodec.Schema,
})
```

Output (trimmed):

```yaml
User:
  type: object
  properties:
    name:
      type: string
      title: Full Name
      description: The user's full display name.
      minLength: 1
      maxLength: 100
    age:
      type: integer
      description: Age in years.
      minimum: 0
      maximum: 150
  required: [name, age]
```

The same `UserCodec` encodes, decodes, validates, and documents — written once.

Constraint schema reflection is opt-in: `validate.*` constraints (e.g. `MinLen`, `RangeInt`, `OneOf`, `Pattern`) automatically annotate the schema. Custom constraints can do the same by setting `Constraint.Schema`.

See `examples/openapi/` for a runnable demonstration.

### Full OpenAPI 3.1 Document

`render/openapi` can emit a complete OpenAPI 3.1 document — not just `components/schemas` — using the `DocumentBuilder`. Define HTTP routes with `route.Route` descriptors that reference codec schemas; the builder assembles paths, operations, parameters, request bodies, responses, and `components/schemas` in one step.

Schemas named via `Body.SchemaName` or `Response.SchemaName` are automatically registered in `components/schemas` and referenced with `$ref`. Unnamed schemas are inlined.

```go
import (
    "github.com/DaniDeer/go-codex/render/openapi"
    "github.com/DaniDeer/go-codex/route"
)

doc, err := openapi.NewDocumentBuilder(openapi.Info{
    Title:   "User API",
    Version: "1.0.0",
}).
    AddServer(openapi.Server{URL: "https://api.example.com/v1"}).
    AddRoute(route.Route{
        Method:      "POST",
        Path:        "/users",
        OperationID: "createUser",
        Summary:     "Create a user",
        RequestBody: &route.Body{
            Required:   true,
            Schema:     CreateUserRequestCodec.Schema,
            SchemaName: "CreateUserRequest", // → $ref + registered in components
        },
        Responses: []route.Response{
            {Status: "201", Description: "Created", Schema: &UserCodec.Schema, SchemaName: "User"},
            {Status: "400", Description: "Validation error."},
        },
    }).
    AddRoute(route.Route{
        Method: "GET",
        Path:   "/users/{id}",
        PathParams: []route.Param{
            {Name: "id", Required: true, Schema: schema.Schema{Type: "string", Format: "uuid"}},
        },
        Responses: []route.Response{
            {Status: "200", Description: "OK", Schema: &UserCodec.Schema, SchemaName: "User"},
            {Status: "204", Description: "No Content"}, // no body — content omitted
        },
    }).
    Build()

yamlBytes, err := doc.MarshalYAML()
```

`Build()` validates:

- No duplicate `(method, path)` pairs.
- `PathParams` names exactly match `{placeholder}` segments in the path.
- Path parameters are always `required: true` in the output.

See `examples/rest-api/` for a runnable demonstration.

### AsyncAPI 2.6 Document

[Spec: asyncapi.com - specification 3.1.0](https://www.asyncapi.com/docs/reference/specification/v3.1.0)

`render/asyncapi` produces a full AsyncAPI 2.6 document from channel descriptors. The same `schema.Schema` that drives OpenAPI output also describes AsyncAPI message payloads — no duplication.

```go
import "github.com/DaniDeer/go-codex/render/asyncapi"

doc, err := asyncapi.NewDocumentBuilder(asyncapi.Info{
    Title:   "User Events",
    Version: "1.0.0",
}).
    AddServer("production", asyncapi.Server{
        URL:      "amqp://broker.example.com",
        Protocol: "amqp",
    }).
    AddChannel("user/created", asyncapi.ChannelItem{
        Subscribe: &asyncapi.Operation{
            Summary: "User created",
            Message: asyncapi.Message{
                Schema:     UserCreatedEventCodec.Schema,
                SchemaName: "UserCreatedEvent", // → $ref + registered in components
            },
        },
    }).
    Build()

yamlBytes, err := doc.MarshalYAML()
```

Output (trimmed):

```yaml
asyncapi: 2.6.0
info:
  title: User Events
  version: 1.0.0
channels:
  user/created:
    subscribe:
      summary: User created
      message:
        payload:
          $ref: "#/components/schemas/UserCreatedEvent"
components:
  schemas:
    UserCreatedEvent:
      type: object
      properties:
        id: { type: string, format: uuid }
        name: { type: string, minLength: 1 }
```

See `examples/event-driven/` for a runnable demonstration.

### REST API Builder

`api/rest` is a transport-agnostic REST API builder. Register routes with codec-backed request and response types; the builder returns a `RouteHandle` with typed `Decode` and `Encode` helpers. Pass those helpers to any HTTP framework — this package imports **no HTTP library**.

The same builder generates a complete OpenAPI 3.1 spec from all registered routes.

```go
import "github.com/DaniDeer/go-codex/api/rest"

b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
b.AddServer(rest.Server{URL: "https://api.example.com/v1"})

// AddRoute returns a RouteHandle — typed Decode/Encode helpers, no net/http import.
createUser := rest.AddRoute[CreateUserRequest, User](b, "POST", "/users",
    createUserCodec, userCodec,
    rest.RouteConfig{
        OperationID:    "createUser",
        Summary:        "Create a user",
        ReqSchemaName:  "CreateUserRequest",
        RespSchemaName: "User",
        Responses: []rest.ResponseMeta{
            {Status: "400", Description: "Validation error."},
        },
    })

// In your HTTP handler — works with net/http, Gin, Chi, Echo, anything:
req, err := createUser.Decode(body)   // JSON → CreateUserRequest, validates
user, err := myService.Create(req)
out, err  := createUser.Encode(user)  // User → JSON

// Route descriptor for your framework's router:
fmt.Println(createUser.Descriptor.Method, createUser.Descriptor.Path) // POST /users

// OpenAPI 3.1 spec from all registered routes:
doc, err := b.OpenAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

**Future:** framework-specific adapters (`adapters/gin`, `adapters/chi`, etc.) will wrap `RouteHandle` for zero-boilerplate integration. The `api/rest` core stays dependency-free.

See `examples/api-rest/` for a runnable demonstration, and `examples/adapters-nethttp/` for the net/http adapter.

### net/http Adapter

`adapters/nethttp` wires a `RouteHandle` to `net/http` in one line. No boilerplate for body reading, JSON encoding, or error response formatting.

```go
import nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"

mux := http.NewServeMux()

// Register uses the Go 1.22+ "METHOD /path" ServeMux pattern automatically.
nethttp.Register(mux, createUser, func(ctx context.Context, req CreateUserReq) (User, error) {
    return svc.CreateUser(ctx, req)
})

http.ListenAndServe(":8080", mux)
```

- POST/PUT/PATCH: body read → `handle.Decode` (validates) → handler → `handle.Encode` → write
- GET/HEAD/DELETE: handler called with zero value of `Req`; path/query extraction via middleware or context
- Errors: `{"error":"..."}` JSON — 400 for decode/validation, 500 for handler/encode failures
- Response status: taken from the route descriptor's primary response (e.g. 201 for POST)

### Event Channel Builder

`api/events` is a transport-agnostic event channel builder. Register channels with codec-backed payload types; the builder returns a `ChannelHandle` with typed `Decode` and `Encode` helpers. Pass those helpers to any message broker — this package imports **no messaging library**.

The same builder generates a complete AsyncAPI 2.6 spec from all registered channels.

```go
import "github.com/DaniDeer/go-codex/api/events"

b := events.NewBuilder(events.Info{Title: "User Events", Version: "1.0.0"})
b.AddServer("production", events.Server{URL: "amqp://broker.example.com", Protocol: "amqp"})

// AddChannel returns a ChannelHandle — typed Decode/Encode helpers, no broker import.
userCreated := events.AddChannel[UserCreatedEvent](b, "user/created", userCreatedCodec,
    events.ChannelConfig{
        Subscribe: &events.OperationConfig{
            Summary:    "A user was created",
            SchemaName: "UserCreatedEvent",
        },
    })

// In your broker callback — works with Paho MQTT, AMQP, Kafka, NATS, anything:
event, err := userCreated.Decode(msg.Payload()) // JSON → UserCreatedEvent, validates
handleUserCreated(event)

// Publish:
payload, _ := userCreated.Encode(UserCreatedEvent{...})
client.Publish(userCreated.Topic, payload)

// AsyncAPI 2.6 spec from all registered channels:
doc, err := b.AsyncAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

Both subscribe and publish directions can be registered on the same channel:

```go
events.AddChannel[UserEvent](b, "user/events", codec, events.ChannelConfig{
    Subscribe: &events.OperationConfig{Summary: "Receive user events"},
    Publish:   &events.OperationConfig{Summary: "Send user events"},
})
```

**Future:** broker-specific adapters (`adapters/amqp`, `adapters/kafka`, etc.) will wrap `ChannelHandle` for zero-boilerplate integration.

See `examples/api-events/` for a runnable demonstration, and `examples/adapters-mqtt/` for the Paho MQTT adapter.

### Paho MQTT Adapter

`adapters/mqtt` wires a `ChannelHandle` to Paho MQTT. `SubscribeHandler` returns a `mqtt.MessageHandler` ready to pass to `client.Subscribe`. `Publish` encodes the value and publishes it, waiting for broker acknowledgement with context-aware cancellation.

```go
import (
    mqtt    "github.com/eclipse/paho.mqtt.golang"
    amqtt   "github.com/DaniDeer/go-codex/adapters/mqtt"
)

// Subscribe: decode + validate incoming messages automatically.
client.Subscribe(userCreated.Topic, 1,
    amqtt.SubscribeHandler(ctx, userCreated,
        func(ctx context.Context, e UserCreatedEvent) error {
            return svc.HandleUserCreated(ctx, e)
        },
        func(err error) { log.Println("event error:", err) },
    ),
)

// Publish: encode outgoing message and wait for broker ack.
err := amqtt.Publish(ctx, client, notifChannel, 1, false, NotificationCommand{...})
```

## Special Topics

### Protobuf Integration

go-codex and Protobuf solve different problems. In a proto-first workflow the two complement each other cleanly.

**Ownership model:**

| Concern                                                        | Owner                      |
| -------------------------------------------------------------- | -------------------------- |
| Wire format, field numbers, binary encoding                    | `.proto` + `protoc-gen-go` |
| Validation rules, richer documentation, format-agnostic decode | `Codec[T]`                 |

**Workflow:**

1. Define your `.proto` file — this is the source of truth for the wire format.
2. Run `protoc-gen-go` to generate Go structs.
3. Write a `Codec[T]` on top of the generated struct to add what proto cannot express: validation constraints, field descriptions, examples, and format-agnostic (JSON/YAML/TOML) decode.

```go
// Generated by protoc-gen-go — do not edit.
type CreateUserRequest struct {
    Name  string
    Email string
    Age   int32
}

// Defined by you — the codec adds validation + documentation.
var CreateUserRequestCodec = codex.Struct[CreateUserRequest](
    codex.Field[CreateUserRequest, string]{
        Name:     "name",
        Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Display name."),
        Get:      func(r CreateUserRequest) string { return r.Name },
        Set:      func(r *CreateUserRequest, v string) { r.Name = v },
        Required: true,
    },
    // ...
)
```

**What this gives you:**

- gRPC handles binary transport; the codec handles REST/JSON/YAML config validation.
- `render/openapi` renders the codec's schema as OpenAPI documentation — no separate YAML file.
- Validation rules (`Refine`) live in Go, next to the type, not scattered across proto options.

**What this is not:** go-codex does not generate `.proto` files from codecs, and does not read `.proto` files. The proto file is the wire-format source of truth; the codec is the validation-and-documentation source of truth. These concerns are intentionally separate.

## Project Structure

```TEXT
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC API: codecs, primitives, struct, union, slice
│   ├── codec.go            # Codec[T], WithDescription, WithTitle
│   ├── map.go              # MapCodecSafe, Downcast
│   ├── refine.go           # Constraint + Refine (Constraint.Schema for schema reflection)
│   ├── primitives.go       # Int, Int64, Float64, String, Bool
│   ├── object.go           # Field[T,F], Struct[T]
│   ├── union.go            # TaggedUnion[T]
│   ├── slice.go            # SliceOf[T]
│
├── format/                 # format bridges: JSON, YAML, TOML
│   ├── format.go           # Format[T], JSON(), YAML(), TOML()
│
├── route/                  # HTTP route descriptors (no renderer logic)
│   └── route.go            # Route, Param, Body, Response
│
├── api/                    # API builders (no HTTP or messaging library imports)
│   ├── rest/               # REST API builder: typed Decode/Encode + OpenAPI spec
│   │   └── builder.go      # Builder, AddRoute[Req,Resp], RouteHandle, RouteConfig
│   └── events/             # Event channel builder: typed Decode/Encode + AsyncAPI spec
│       └── builder.go      # Builder, AddChannel[T], ChannelHandle, ChannelConfig
│
├── adapters/               # transport-specific adapters (wrap api/rest or api/events)
│   ├── nethttp/            # net/http adapter for api/rest RouteHandles
│   │   └── adapter.go      # Handler[Req,Resp], Register[Req,Resp], HandlerFunc
│   └── mqtt/               # Paho MQTT adapter for api/events ChannelHandles
│       └── adapter.go      # SubscribeHandler[T], Publish[T]
│
├── render/                 # spec renderers (import schema only, or schema + route)
│   ├── openapi/            # OpenAPI 3.1 renderer
│   │   ├── openapi.go      # SchemaObject, ComponentsSchemas, MarshalJSON, MarshalYAML
│   │   └── document.go     # DocumentBuilder, Document, Info, Server — full 3.1 spec
│   └── asyncapi/           # AsyncAPI 2.6 renderer
│       ├── asyncapi.go     # unexported schema helpers (schemaObject, schemaRef)
│       └── document.go     # DocumentBuilder, Document, ChannelItem, Operation, Message
│
├── schema/                 # schema model (pure data, zero dependencies)
│   ├── schema.go
│
├── validate/               # reusable constraints (reflect into schema automatically)
│   ├── int.go
│   ├── float.go
│   ├── string.go
│   ├── format.go           # Email, UUID, URL, IPv4, IPv6, Date, DateTime, Slug
│
└── examples/
    ├── formats/            # builtin format constraints demo (Email, UUID, URL, ...)
    ├── openapi/            # OpenAPI components/schemas generation from a Codec
    ├── rest-api/           # full OpenAPI 3.1 document from route descriptors (low-level)
    ├── event-driven/       # full AsyncAPI 2.6 document from channel descriptors (low-level)
    ├── api-rest/           # REST API builder: typed helpers + OpenAPI spec
    ├── api-events/         # Event channel builder: typed helpers + AsyncAPI spec
    ├── adapters-nethttp/   # net/http adapter: wiring api/rest to ServeMux
    ├── adapters-mqtt/      # Paho MQTT adapter: wiring api/events to Paho client
    ├── shape/              # tagged union + Downcast demo
    ├── order/              # nested structs + SliceOf demo
    ├── multiformat/        # JSON / YAML / TOML with one codec
    └── validate/           # explicit Validate before marshal
```
