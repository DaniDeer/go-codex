# Event Channels — MQTT & AsyncAPI

> See also: [`api/events` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/events) · [`adapters/mqtt` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt)

`api/events` is a transport-agnostic event channel builder. The same builder that drives typed decode/encode also generates a complete AsyncAPI 3.0 spec.

## Declaring channels

```go
b := events.NewBuilder(
    events.Info{Title: "Sensor Platform", Version: "1.0.0"},
    events.WithTopicConstraints(validate.MQTTPublishTopic),
)
b.AddServer("production", events.Server{URL: "mqtt://broker.example.com:1883", Protocol: "mqtt"})

// Static topic
userCreated, _ := events.NewChannel[UserCreatedEvent]("user/created", userCreatedCodec,
    events.Subscribe{OperationID: "receiveUserCreated", Summary: "A user was created", SchemaName: "UserCreatedEvent"},
).Register(b)

// Template topic with parameter codec
sensorUUIDCodec := codex.String().Refine(validate.UUID)
sensorMeasurement, _ := events.NewChannel[Measurement]("sensors/{sensorID}/measurements",
    measurementCodec,
    events.Subscribe{OperationID: "receiveMeasurement", SchemaName: "Measurement"},
    events.Publish{OperationID: "publishMeasurement"},
    events.TopicParam{
        Name:        "sensorID",
        Description: "UUID of the sensor.",
    }.WithCodec(sensorUUIDCodec),
).Register(b)
```

## Parameter types

| Type | Location | Auto-validated by | Schema in spec |
|---|---|---|---|
| `TopicParam` | `{varName}` in topic | `BuildTopic` + `TopicVarsFromMessage` | `channels[name].parameters` |

`TopicParam` has no `Required` field — topic variables must always be present (same rationale as `PathParam`).

## BuildTopic — type-safe topic construction

```go
topic, err := sensorMeasurement.BuildTopic(map[string]string{"sensorID": "f47ac10b-..."})
// → "sensors/f47ac10b-.../measurements"
// err: events.TopicParamError or events.MissingTopicVarError on failure
```

## Paho MQTT adapter — subscribing

```go
import amqtt "github.com/DaniDeer/go-codex/adapters/mqtt"

client.Subscribe(topic, 1,
    amqtt.SubscribeHandler(ctx, sensorMeasurement,
        func(ctx context.Context, m Measurement) error {
            // MessageFromContext gives access to QoS, retained flag, etc.
            if msg, ok := amqtt.MessageFromContext(ctx); ok {
                if msg.Retained() { return nil } // skip stale retained messages
            }
            return svc.HandleMeasurement(ctx, m)
        },
        amqtt.SubscribeOptions{
            Observer: obs,
            OnError: func(e amqtt.SubscribeError) {
                switch e.Kind {
                case amqtt.KindDecode:
                    logger.Warn("decode error", "topic", e.Topic, "error", e.Err)
                case amqtt.KindHandler:
                    logger.Error("handler error", "topic", e.Topic, "error", e.Err)
                }
            },
        },
    ),
)
```

## Paho MQTT adapter — publishing

```go
// Static topic — pass nil for vars
err := amqtt.Publish(ctx, client, alertChannel, 1, false, alert, nil,
    amqtt.PublishOptions{Observer: obs})

// Template topic — BuildTopic called internally
err = amqtt.Publish(ctx, client, sensorMeasurement, 1, false, m,
    map[string]string{"sensorID": sensorUUID},
    amqtt.PublishOptions{Observer: obs})
```

**`SubscribeError.Topic`** — always the concrete incoming message topic, even for template channels (e.g. `sensors/abc-123/measurements`, never `sensors/{sensorID}/measurements`). Use this in `OnError` logging to identify the exact message that failed.

## TopicVarsFromMessage — wildcard subscription

Extracts and validates `{varName}` from the concrete received topic — the inverse of `BuildTopic`:

```go
// Channel: "sensors/{sensorID}/measurements"
// Subscribed to: "sensors/+/measurements"
// Message arrives on: "sensors/f47ac10b-.../measurements"

vars, err := amqtt.TopicVarsFromMessage(sensorMeasurement, msg)
// vars["sensorID"] == "f47ac10b-..."
```

Wildcard capture rules:
- `{varName}` — captures the corresponding topic segment into the variable
- `+` in the subscription pattern — matches one level (anonymous; not captured into vars)
- `#` as the last segment — matches all remaining levels; captured under key `"#"`

Validation chain (in order):
1. Structural match (segment count + literals) → `TopicMismatchError`
2. Builder-level topic codec → `InvalidTopicError`
3. `TopicParam.Codec` per variable → `TopicParamError`

## Multi-format MQTT payloads

MQTT 3.1.1 carries no content-type metadata — format is agreed out-of-band. Configure the default format once on the handle; `WithFormats` also updates the AsyncAPI spec: the first format's content type is written to `message.contentType` on each registered operation.

**Structured logging:** all codec error types (`ValidationErrors`, `ConstraintError`, `TypeMismatchError`, etc.) implement `slog.LogValuer`. Pass them directly to `slog.Any(...)` for full nested structured output — field names, constraint details, type mismatches — without any string parsing.

```go
// Configure YAML as default — adapter picks it up automatically
yamlChannel := measurementCh.WithFormats(format.YAML(measurementCodec))

client.Subscribe(topic, 1, amqtt.SubscribeHandler(ctx, yamlChannel, handler, opts))
err := amqtt.Publish(ctx, client, yamlChannel, 1, false, m, nil, amqtt.PublishOptions{})

// Call-time override still works
amqtt.SubscribeHandler(ctx, yamlChannel, handler, opts, format.JSON(measurementCodec))
```

Format priority: call-time variadic → `handle.SubscribeFormats`/`PublishFormats` → `handle.Formats` → JSON fallback.

## TopicParam schema → AsyncAPI spec

`TopicParam.Codec` schema flows automatically into the AsyncAPI `parameters:` block. Every `{varName}` placeholder gets a parameter entry:
- No `TopicParam` declared → auto-generated as `{type: string}`
- `TopicParam` with `.WithCodec(c)` → codec schema in `parameters:` + runtime validation at `BuildTopic` time
- `TopicParam` with `.Description` only → enriches the spec without runtime validation

```go
// With codec: UUID schema in spec + UUID validation at BuildTopic time
events.TopicParam{Name: "sensorID", Description: "Sensor UUID"}.WithCodec(
    codex.String().Refine(validate.UUID),
)
// Without codec: description only, no runtime validation
events.TopicParam{Name: "region", Description: "Geographic region"}
```

## Builder options

| Option | Effect |
|---|---|
| `WithTopicCodec(c)` | Validates every registered topic against codec `c` at `Register` time |
| `WithTopicConstraints(cs...)` | Validates every topic against one or more constraints at `Register` time |

**Template-transparent validation:** constraints run on the structural shape of the topic, not the literal template. `{varName}` placeholders are replaced with `x` before validation — `sensors/{sensorID}/readings` → `sensors/x/readings`. The stored `ChannelHandle.Topic` is always the original template.

**Final topic re-validation:** `BuildTopic` re-validates the fully assembled topic against the builder-level codec after substitution. Returns `events.InvalidTopicError{Topic, Err}` with the concrete topic on failure.

## AsyncAPI spec generation

```go
doc, err := b.AsyncAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

AsyncAPI 3.0: separate `channels` and `operations` top-level keys, `action: receive` / `action: send`. The `render/asyncapi/v2` package generates AsyncAPI 2.6 for existing users.

## Error types

| Error | When returned |
|---|---|
| `events.InvalidTopicError{Topic, Err}` | Topic fails builder-level validation |
| `events.TopicParamError{Name, Value, Err}` | Topic variable fails its codec |
| `events.MissingTopicVarError{Name}` | Template variable absent from vars map |
| `amqtt.TopicMismatchError{Template, Topic}` | Concrete topic doesn't match template structure |

## Codec-as-contract pattern

```go
// contract/contract.go
var ReadingsChannel = events.NewChannel[SensorReading](
    "sensors/{sensorID}/readings", sensorReadingCodec,
    events.Subscribe{...}, events.Publish{...},
    events.TopicParam{Name: "sensorID"}.WithCodec(SensorIDCodec),
)

// producer/main.go
handle, _ := contract.ReadingsChannel.Register(producerBuilder)
amqtt.Publish(ctx, client, handle, 1, false, reading, vars, opts)

// consumer/main.go
handle, _ := contract.ReadingsChannel.Register(consumerBuilder)
client.Subscribe(topic, 1, amqtt.SubscribeHandler(ctx, handle, fn, opts))
```

## See also

- [Feature: Security & Auth](security.md) — MQTT security, SecurityFunc
- [Concept: Go Library as Contract](../concepts/codec-as-contract.md) — shared contract pattern
- [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — three-layer pipeline with MQTT
- [examples/adapters-mqtt-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-contract) — producer + consumer sharing a contract
- [examples/api-events](https://github.com/DaniDeer/go-codex/tree/main/examples/api-events) — event builder + AsyncAPI spec
