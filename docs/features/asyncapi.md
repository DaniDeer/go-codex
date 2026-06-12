# AsyncAPI Spec Generation

> See also: [`render/asyncapi/v3` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/asyncapi/v3) · [`api/events` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/events)
>
> Runnable demos: [`examples/event-driven`](https://github.com/DaniDeer/go-codex/tree/main/examples/event-driven) · [`examples/api-events`](https://github.com/DaniDeer/go-codex/tree/main/examples/api-events)

go-codex generates AsyncAPI 3.0 (and 2.6) documents from the same codec definitions that drive runtime decode/encode — no separate YAML authoring, no drift.

## Via `api/events` builder (recommended)

The `api/events` builder generates a complete AsyncAPI 3.0 document from all registered channels:

```go
import (
    "github.com/DaniDeer/go-codex/api/events"
    "github.com/DaniDeer/go-codex/validate"
)

b := events.NewBuilder(
    events.Info{Title: "User Events", Version: "1.0.0"},
    events.WithTopicConstraints(validate.MQTTPublishTopic),
)
b.AddServer("production", events.Server{
    URL:      "broker.example.com",
    Protocol: "mqtt",
})

// Register a static topic channel
userCreated, _ := events.NewChannel[UserCreatedEvent]("user/created", userCreatedCodec,
    events.Subscribe{
        OperationID: "receiveUserCreated",
        Summary:     "A user was created",
        SchemaName:  "UserCreatedEvent",  // → $ref in spec
    },
).Register(b)

// Register a template topic channel with parameter
sensorUUIDCodec := codex.String().Refine(validate.UUID)
sensorMeasurement, _ := events.NewChannel[Measurement]("sensors/{sensorID}/measurements",
    measurementCodec,
    events.Subscribe{
        OperationID: "receiveSensorMeasurement",
        Summary:     "Sensor measurement received",
        SchemaName:  "Measurement",
    },
    events.TopicParam{
        Name:        "sensorID",
        Description: "UUID of the sensor publishing the measurement.",
    }.WithCodec(sensorUUIDCodec),  // codec validates + flows schema into spec parameters:
).Register(b)

// Register both directions on same channel
events.NewChannel[UserEvent]("user/events", codec,
    events.Subscribe{OperationID: "receiveUserEvent", Summary: "Receive user events"},
    events.Publish{OperationID: "sendUserEvent",    Summary: "Send user events"},
).Register(b)

// Generate the full AsyncAPI 3.0 spec:
doc, err := b.AsyncAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

The output follows AsyncAPI 3.0: separate `channels` and `operations` top-level keys, with `action: receive` / `action: send`:

```yaml
asyncapi: 3.0.0
channels:
  sensors/{sensorID}/measurements:
    address: sensors/{sensorID}/measurements
    parameters:
      sensorID:
        schema: {type: string, format: uuid}
    messages:
      Measurement:
        payload: {$ref: '#/components/schemas/Measurement'}
operations:
  receiveSensorMeasurement:
    action: receive
    channel: {$ref: '#/channels/sensors/{sensorID}/measurements'}
```

## Using the low-level v3 DocumentBuilder directly

For cases where you build channel descriptors manually (without `api/events`):

```go
import "github.com/DaniDeer/go-codex/render/asyncapi/v3"

doc, err := v3.NewDocumentBuilder(v3.Info{
    Title:   "User Events",
    Version: "1.0.0",
}).
    AddServer("production", v3.Server{
        URL:      "broker.example.com",
        Protocol: "amqp",
    }).
    AddChannel("userCreated", v3.ChannelItem{
        Address: "user/created",
        Subscribe: &v3.Operation{
            Summary: "User created",
            Message: v3.Message{
                Schema:     UserCreatedEventCodec.Schema,
                SchemaName: "UserCreatedEvent", // → $ref + registered in components
            },
        },
    }).
    Build()

yamlBytes, _ := doc.MarshalYAML()
```

## AsyncAPI 2.6 (frozen)

`render/asyncapi/v2` is preserved for existing users who want 2.6 output. Use it directly via `DocumentBuilder`:

```go
import "github.com/DaniDeer/go-codex/render/asyncapi/v2"

doc, err := v2.NewDocumentBuilder(v2.Info{Title: "User Events", Version: "1.0.0"}).
    AddServer("production", v2.Server{URL: "broker.example.com", Protocol: "amqp"}).
    AddChannel("user/created", v2.ChannelItem{
        Subscribe: &v2.Operation{
            Summary: "User created",
            Message: v2.Message{
                Schema:     UserCreatedEventCodec.Schema,
                SchemaName: "UserCreatedEvent",
            },
        },
    }).
    Build()

yamlBytes, _ := doc.MarshalYAML()
```

## Builder options

| Option | Effect |
|---|---|
| `WithTopicCodec(c)` | Validates topics against codec `c` at `Register` time |
| `WithTopicConstraints(cs...)` | Validates topics against one or more constraints at `Register` time |

`{varName}` placeholders are replaced with `x` before validation, so topic constraints work correctly on template topics.

## Security schemes (AsyncAPI)

```go
b.AddSecurityScheme("bearerAuth", events.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken)))

userCreated, _ := events.NewChannel[UserCreatedEvent]("user/created", codec,
    events.Subscribe{
        Summary:  "Receive user created events",
        Security: []route.SecurityRequirement{route.Require("bearerAuth")},
    },
).Register(b)
```

## Error types

| Error | When returned |
|---|---|
| `events.InvalidTopicError{Topic, Err}` | Topic fails builder-level validation |
| `events.TopicParamError{Name, Value, Err}` | Topic variable value fails its codec |
| `events.MissingTopicVarError{Name}` | Template variable absent from vars map |
| `events.InvalidTopicParamError{Name, Topic}` | `TopicParams` entry names a variable not in the template |

## See also

- [Guide: MQTT Events](../guides/mqtt.md) — Paho MQTT adapter + full pub/sub example
- [examples/api-events](https://github.com/DaniDeer/go-codex/tree/main/examples/api-events) — event channel builder + AsyncAPI spec
- [examples/event-driven](https://github.com/DaniDeer/go-codex/tree/main/examples/event-driven) — full AsyncAPI 2.6 document
