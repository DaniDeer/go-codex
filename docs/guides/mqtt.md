# Guide: MQTT Events

This guide walks through the MQTT examples. For the full API reference, see the feature page.

**Feature:** [Event Channels — MQTT & AsyncAPI](../features/events.md)

## examples/adapters-mqtt

The most comprehensive MQTT demo. Leads with Demo 1, the PREFERRED `Client.Attach` +
`Client.Publish`/`.Subscribe` workflow (spec printed for free from the same client — no
separate builder), then shows the handle-based escape hatch (Demo 2+) needed for
capabilities `Client.Attach`'s v1 reflection shim doesn't support — the **three-layer
codec pipeline** for event-driven systems:

- Layer 1: three boundary codecs (`MeasurementEvent`, `TimeSeriesRecord`, `AlertEvent`) share field-level codecs — constraints propagate automatically
- Layer 2: pure domain functions (`buildTimeSeriesRecord`, `shouldAlert`, `buildAlertEvent`) with zero IO
- Layer 3: `NewSubscribeTransport`/`NewPublishTransport` (consumed via `events.SubscribeHandle`/`events.PublishHandle`) orchestrate all MQTT and database IO — the escape hatch used for everything below

Key patterns (escape-hatch section):
- `TopicParam.WithCodec(uuidCodec)` — validates `{sensorID}` at `BuildTopic` time and flows UUID schema into AsyncAPI spec
- `TopicVarsFromMessage` + wildcard subscription (`sensors/#`) — extracts and validates topic vars from incoming messages
- `SubscribeOptions.OnError` — switch on `KindDecode` vs `KindHandler` to distinguish codec failures from application errors
- `WithFormats(format.YAML(...))` — multi-format MQTT payloads (agreed out-of-band, not in message)
- Publish failure demos: invalid `{sensorID}` → `ValidationErrors` (via the `NewTopicParam` merge field's codec); invalid payload → `ValidationErrors`
- AsyncAPI 2.6 spec generation from a dedicated, throwaway `events.Client` (contrast with Demo 1's spec-for-free)

→ [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt)

## examples/adapters-mqtt-contract

Demonstrates the **codec-as-contract** pattern for MQTT. A shared `contract/` package defines channel specs, codecs, and types. Producer and consumer both import it — the compiler enforces the contract.

Key insight: `{sensorID}` topic variable (routing key, UUID) is separate from `SensorReading.SensorID` (application field, non-empty string). The consumer uses `TopicVarsFromMessage` to extract the validated routing UUID rather than assuming the payload field is a UUID.

→ [examples/adapters-mqtt-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-contract)

## Binary payloads (PNG, JPEG, PDF…)

Binary data — camera snapshots, image uploads, sensor captures — works with the
MQTT adapter without any changes. MQTT natively carries raw `[]byte` payloads,
and `format.Binary` writes and reads bytes as-is (no encoding overhead).

### How it works

`format.Binary` implements `Format[[]byte]` with identity marshal/unmarshal:

- **Publish path**: `format.Binary.Marshal(pngBytes)` validates via Refine
  constraints (magic bytes, size), then returns the raw bytes — which become the
  MQTT message payload.
- **Subscribe path**: `format.Binary.Unmarshal(msg.Payload())` reads the raw
  MQTT bytes, validates them, and delivers the `[]byte` to your handler.

Validation fires on **both directions** — a malformed or oversized image is
rejected before it reaches your application code.

### Wiring

```go
// 1. Codec — size cap first (cheap), then format check (reads 8 bytes)
pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(512 * 1024)). // 512 KiB — tune to your broker limit
    Refine(validate.PNG)

// 2. Channel — T = []byte, registered with an events.Client
imageCh, _ := events.NewChannel[[]byte](
    "cameras/{id}/snapshot",
    pngCodec,
    events.Publish{OperationID:   "publishSnapshot"},
    events.Subscribe{OperationID: "subscribeSnapshot"},
    events.TopicParam{Name: "id"},
).Register(b)

// 3. Set binary as the payload format for both directions
imageCh.WithFormats(format.Binary(pngCodec).WithContentType("image/png"))

// Publish a PNG — validate.PNG runs before the message is sent, via the
// spec-free, handle-based Decision 7 call surface
// (docs/design/d-0002-pubsub-workflow-simplification.md): adaptermqtt.NewPublishTransport[T]
// satisfies events.PublishTransport[T], consumed through events.PublishHandle.
pubTransport := adaptermqtt.NewPublishTransport[[]byte](client, 1, false,
    adaptermqtt.PublishOptions[[]byte]{Observer: obs})
pub := imageCh.WithPublish(events.Publish{})
err := events.PublishHandle(ctx, pub, pubTransport, pngBytes)

// Subscribe — handler receives validated PNG bytes. mqtt (v3) has no broker
// router, so subscribeTransport.Subscribe registers the subscription then
// blocks on ctx.Done(); run it in a goroutine.
subTransport := adaptermqtt.NewSubscribeTransport[[]byte](client, 1,
    adaptermqtt.SubscribeOptions{Observer: obs})
sub := imageCh.WithSubscribe(events.Subscribe{})
go func() {
    _ = events.SubscribeHandle(ctx, sub, subTransport,
        func(ctx context.Context, png []byte) error {
            // png passed validate.PNG and MaxBytes — safe to process
            return processSnapshot(png)
        },
    )
}()
```

### Same thing, declared through `ports.EventPattern`

`events.Formats(...)`/`SubscribeFormats(...)`/`PublishFormats(...)` are
`ChannelOpt`s — they slot directly into `EventPattern.Opts`, so a
`ports`-wired channel gets the same one-step declaration:

```go
ports.EventPattern{
    Topic: "cameras/{id}/snapshot",
    Opts: []events.ChannelOpt{
        events.TopicParam{Name: "id"},
        events.Formats(format.Binary(pngCodec).WithContentType("image/png")),
    },
}
```

Use `SubscribeFormats`/`PublishFormats` instead of `Formats` for asymmetric
channels (different formats per direction — e.g. YAML in, JSON out). A type
mismatch returns `events.FormatOptError` from the port constructor.

For JPEG or other formats, swap in the matching constraint:

```go
jpegCodec := codex.Bytes().
    Refine(validate.MaxBytes(512 * 1024)).
    Refine(validate.JPEG)
```

### Considerations

| Topic | Detail |
|-------|--------|
| **Payload size** | MQTT max payload is broker-dependent (common defaults: 128 KB – 256 MB). Always add `validate.MaxBytes` and tune it to your broker's limit. |
| **No content-type in MQTT 3.1.1** | MQTT 3.1.1 carries no Content-Type metadata. The format must be agreed out-of-band — publisher and subscriber must both use `format.Binary` for the same channel. |
| **AsyncAPI spec** | `codex.Bytes()` emits `type: string, format: binary` in the generated AsyncAPI document, correctly describing a binary payload. The binary format constraints (`validate.PNG` etc.) are runtime-only — there is no standard AsyncAPI schema keyword for file type. |
| **Error handling** | A constraint failure on subscribe arrives as a `SubscribeError{Kind: KindDecode}` in `SubscribeOptions.OnError`. Unwrap to `codex.ConstraintError` for the specific failing constraint name (`"png"`, `"maxBytes(524288)"`, …). |

## examples/adapters-mqtt-security

Demonstrates MQTT credential validation via `SubscribeOptions.SecurityFunc`.

→ [examples/adapters-mqtt-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-security)

## `Client.Attach` — the inverted-control workflow

`mqtt.Attach(client, mqttClient)` binds mqttClient to `client` as its `events.Transport` —
the "attach the adapter to the client" step. From there, call `client.Publish`/
`client.Subscribe` directly on the `*events.Client` value itself:

```go
client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
if err := mqtt.Attach(client, mqttClient); err != nil {
    log.Fatal(err)
}

sub := contract.ReadingsChannel.WithSubscribe(events.Subscribe{})
pub := contract.ReadingsChannel.WithPublish(events.Publish{})

go func() {
    _ = client.Subscribe(ctx, sub, func(ctx context.Context, r SensorReading) error {
        log.Printf("received: %+v", r)
        return nil
    })
}()

err := client.Publish(ctx, pub, reading) // "one struct, one call"
```

Since `Client.Publish`/`Client.Subscribe` are ordinary Go methods (not generic — Go forbids
a method from introducing its own type parameters), arguments are passed as `any` and their
concrete types are recovered internally via reflection; a mismatch surfaces as
`events.TransportTypeMismatchError` at CALL time. See
`docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 5 for the full design and its
documented v1 scope limits (no per-call format override, QoS 0 only, no general-purpose
SubscribeMW/PublishMW wrapping — use `events.SubscribeHandle`/`events.PublishHandle` with
`mqtt.NewSubscribeTransport`/`mqtt.NewPublishTransport` directly for those).
