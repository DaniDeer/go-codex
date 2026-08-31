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

## Topic vars with automatic merge — `NewTopicParam`

`TopicParam` above is validate-only — it checks a topic variable against a
codec but leaves extracting it into your payload struct to hand-written
code. `events.NewTopicParam` declares the SAME spec `TopicParam` AND a
merge field in one call — the handler receives an already-merged,
already-validated payload, no manual `TopicVarsFromMessage` extraction:

```go
type SensorReading struct {
    SensorID string
    Value    float64
}

var sensorChannel = events.NewChannel[SensorReading]("sensors/{sensorID}/readings", sensorCodec,
    events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
        func(r SensorReading) string { return r.SensorID },
        func(r *SensorReading, v string) { r.SensorID = v },
    ),
)
handle, _ := sensorChannel.Register(builder)

// adapters/mqtt5.Subscribe calls ChannelHandle.DecodeMerged automatically
// whenever handle.MergeFields() is non-empty — the handler function just
// receives a fully populated, validated SensorReading:
func(ctx context.Context, r SensorReading) error {
    store.Save(r) // r.SensorID already validated as a UUID
    ...
}
```

This is the PRIMARY, recommended way to declare a topic variable — but not
the SOLE way: the plain `TopicParam` struct literal remains available for
validate-only variables a handler never reads directly. A channel can
freely mix both styles.

`ChannelHandle.MergeFields()` returns the registered merge fields directly
(no role-aware split needed — unlike REST's path/query/header/cookie,
events has exactly ONE var destination, the topic, so a single flat slice
is always safe for both directions). `ChannelHandle.DecodeMerged(payload,
topicVars) (T, error)` closes the loop: decodes the payload (via
`ChannelHandle.Decode`, JSON) AND merges every topic variable into the SAME
value, in one call.

`adapters/mqtt5.PublishHandle` is the single-call publisher convenience —
mirrors `nethttp.CallWithHandle`: derives the topic vars from the payload
struct automatically via `codex.EncodeVars(msg, handle.MergeFields()...)`,
then delegates to `Publish`. `Publish` remains the lower-level escape
hatch for callers building the vars map themselves. The same convenience
exists for every transport with a pub/sub event surface:
`adapters/mqtt.PublishHandle` (MQTT 3.1.1) and `adapters/zeromq.PublishHandle`
(ZeroMQ PUB/SUB) — identical signature shape and semantics, one per
transport package.

```go
err := mqtt5.PublishHandle(ctx, client, sensorChannel, 1, false, reading, mqtt5.PublishOptions{})
// topic + payload both derived from the SAME reading value — no manual vars map.
```

`adapters/mqtt5.TopicVarsFromMessage` is the mqtt5-specific equivalent of
the (Paho MQTT 3.1.1) `mqtt.TopicVarsFromMessage` shown above — used
internally by `Subscribe`'s auto-merge wiring, and directly usable by
hand-rolled mqtt5 consumers. `adapters/zeromq.TopicVarsFromMessage` is the
ZeroMQ equivalent, adapted for zeromq's plain-string topic (the first frame
of a `[topic, payload]` PUB/SUB message) rather than a message struct;
`zeromq.Subscribe` calls it internally whenever the channel declares merge
fields, exactly like `mqtt5.Subscribe`/`mqtt.SubscribeHandler` do.

Every port-binding `SinkAdapter`/`IOAdapter` constructed for one of these
transports (`nethttp.DrainCallAdapter`/`CallAdapter`,
`mqtt5.PublishAdapter`/`CallAdapter`, `zeromq.PublishAdapter`/`CallAdapter`,
`mqtt.PublishAdapter`) derives vars PER-ITEM automatically via the
corresponding `*Handle` function whenever its `Vars` option is left `nil` —
the "one struct, one call" convenience extends all the way through
`ports.SinkPort`/`ports.IOPort`, not just the standalone functions. Set
`Vars` explicitly (even to an empty, non-nil map) to keep today's static,
same-vars-for-every-item behavior instead.

### Nested structs & non-JSON payloads

The merge-field convenience is neither JSON-specific nor
flat-struct-specific — the same guarantees documented for REST
([Nested structs & binary body formats](rest-api.md#nested-structs-binary-body-formats))
apply here:

- Merge-field `get`/`set` are plain closures, so a topic variable can
  target a NESTED sub-struct field (`func(r Reading) string { return
  r.Meta.SensorID }`) with zero framework changes.
- Payload decode/encode is orthogonal to topic-var merge, so `format.Gob`/
  `format.Binary`/any custom format composes unchanged — but the SAME
  `format.Gob(codec)` caveat applies: it serialises the WHOLE typed value
  directly via `encoding/gob`'s reflection, bypassing the codec entirely
  for the wire bytes. Use `format.NewTyped` with a custom marshal/
  unmarshal to project the wire bytes onto ONLY a nested payload sub-field.

See `examples/events-nested-binary` for the full runnable version (nested
`Meta`/`Value` payload, Gob body projected via `format.NewTyped`, topic
merge into the nested field).

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

## Error-path ergonomics — `ErrorChannel`

Pub/sub channels have no synchronous caller to respond to — `events.ErrorChannel`
is the pub/sub analogue of [`rest.ErrorPattern`](rest-api.md#error-path-ergonomics-errorstatus--errorpattern):
declare a codec-backed typed error payload for a matched error type, published
to a dedicated error-output topic instead of an HTTP status/body.

```go
type ValidationError struct{ Reason string }
func (e ValidationError) Error() string { return "validation: " + e.Reason }

type ErrorPayload struct {
    Code    string
    Message string
}

handle, err := events.NewChannel[SensorReading]("sensors/{id}/data", sensorCodec,
    events.ErrorChannel[ValidationError, ErrorPayload](
        "sensors/{id}/errors", errorPayloadCodec,
        func(e ValidationError) (ErrorPayload, error) {
            return ErrorPayload{Code: "validation", Message: e.Reason}, nil
        },
    ),
).Register(b)
```

- **Direct mode** (no map function): `E` must itself be assignable to the declared
  payload type `B`.
- **Mapped mode** (map function provided): the map function converts `E` into `B`.
- **Matching**: type-only via `errors.As`; the first declared `ErrorChannel` (in
  `NewChannel` option order) whose type matches wins — the same deterministic
  precedence used by REST.
- **`ChannelHandle.ErrorResponseFor(err) (ErrorChannelResponse, bool, error)`**
  looks up the first matching pattern — adapters call this before falling back
  to their own default error handling.

### Action model — `respond` / `handle` / `log`

A matched pattern executes exactly **one** action, never an implicit chain:

| Action | Behavior | Default |
|---|---|---|
| `events.ErrorRespond` | publish the typed payload to the declared error topic | ✅ default |
| `events.ErrorHandle` | run a custom callback instead of publishing | opt-in via `.WithAction(events.ErrorHandle)` |
| `events.ErrorLog` | forward the error through the adapter's normal error path only (same as no match) | opt-in via `.WithAction(events.ErrorLog)` |

```go
events.ErrorChannel[ValidationError, ErrorPayload]("sensors/{id}/errors", errorPayloadCodec, mapFn).
    WithAction(events.ErrorLog)
```

### Adapter wiring (`adapters/mqtt5`, `adapters/mqtt`, `adapters/zeromq`)

`mqtt5.PublishAdapter`, `mqtt.PublishAdapter`, and `zeromq.PublishAdapter`
all consult `handle.ErrorResponseFor(err)` for every upstream stream error
before falling back to their own `OnError` option:

- matched + `respond` → publishes the encoded payload to the declared topic
  (does not call `OnError`);
- matched + `handle` → falls through to `OnError` (unchanged existing behavior —
  `OnError` already IS the "handle" realization for these adapters);
- matched + `log`, or unmatched → falls through to `OnError` unchanged.

## WebSocket error frames

See [WebSocket — error frames](websocket.md#error-path-ergonomics-errorframe)
for the duplex-socket analogue (`websocket.ErrorFrame`), which broadcasts a
typed error frame to every connected session instead of publishing to a
declared topic (sockets have no dedicated error-output channel — broadcast
IS the notification path).

## See also

- [Feature: Security & Auth](security.md) — MQTT security, SecurityFunc
- [Concept: Go Library as Contract](../concepts/codec-as-contract.md) — shared contract pattern
- [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — three-layer pipeline with MQTT
- [examples/adapters-mqtt-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-contract) — producer + consumer sharing a contract
- [examples/api-events](https://github.com/DaniDeer/go-codex/tree/main/examples/api-events) — event builder + AsyncAPI spec
