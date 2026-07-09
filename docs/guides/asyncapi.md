# Guide: AsyncAPI Spec

For the full API reference and all code examples, see the feature page.

**Feature:** [Event Channels — MQTT & AsyncAPI](../features/events.md) — AsyncAPI spec generation section

## Examples

- [examples/api-events](https://github.com/DaniDeer/go-codex/tree/main/examples/api-events) — event channel builder + `AsyncAPISpec()` output
- [examples/event-driven](https://github.com/DaniDeer/go-codex/tree/main/examples/event-driven) — full AsyncAPI 2.6 document via the low-level `DocumentBuilder`

---

## Combining pub/sub and request-reply in one AsyncAPI spec

By default, `api/events.Builder` (PUB/SUB channels) and `api/reqreply.Builder`
(request-reply channels) each produce their own `AsyncAPISpec()`. To publish a
**single combined AsyncAPI 3.0 document** covering both patterns, use
`AppendTo(*asyncapi.DocumentBuilder)` on each builder:

```go
import asyncapi "github.com/DaniDeer/go-codex/render/asyncapi/v3"

// 1. Create a shared underlying document builder.
doc := asyncapi.NewDocumentBuilder(asyncapi.Info{
    Title:   "Sensor Service API",
    Version: "1.0.0",
})
doc.AddServer("mqtt5", asyncapi.Server{
    URL:      "mqtts://broker.example.com:8883",
    Protocol: "mqtt5",
})

// 2. Register pub/sub channels and append them.
eventsB := events.NewBuilder(events.Info{Title: "Sensor Service API", Version: "1.0.0"})
sensorHandle, _ := sensorChannel.Register(eventsB)
if err := eventsB.AppendTo(doc); err != nil {
    log.Fatal(err)
}

// 3. Register request-reply routes and append them.
reqreplyB := reqreply.NewBuilder(reqreply.Info{Title: "Sensor Service API", Version: "1.0.0"})
computeHandle, _ := computeRoute.Register(reqreplyB)
if err := reqreplyB.AppendTo(doc); err != nil {
    log.Fatal(err)
}

// 4. Build once — one document covers pub/sub + request-reply.
spec, err := doc.Build()
if err != nil {
    log.Fatal(err)
}
yaml, _ := spec.MarshalYAML()
fmt.Println(string(yaml))
```

The combined YAML will contain both channel types:

```yaml
asyncapi: 3.0.0
info:
  title: Sensor Service API
  version: 1.0.0
channels:
  sensor/reading:            # ← pub/sub channel from events.Builder
    address: sensor/reading
    ...
  computeAdd:                # ← request channel from reqreply.Builder
    address: compute/add
    ...
  computeAddReply:           # ← auto-generated reply channel
    address: compute/add/reply
    ...
```

### What `AppendTo` does and does NOT copy

| Copied by `AppendTo` | Not copied (caller owns) |
|---|---|
| All registered channels | Servers |
| Reply channels (request-reply pattern) | Schemas registered via `AddSchema` |
| | Security schemes |

Servers, schemas, and security schemes must be added directly to the shared
`*asyncapi.DocumentBuilder` before calling `Build()`. This gives you full
control over the combined document without any hidden merging surprises.
