# Guide: MQTT Events

This guide walks through the MQTT examples. For the full API reference, see the feature page.

**Feature:** [Event Channels — MQTT & AsyncAPI](../features/events.md)

## examples/adapters-mqtt

The most comprehensive MQTT demo. Shows the **three-layer codec pipeline** for event-driven systems:

- Layer 1: three boundary codecs (`MeasurementEvent`, `TimeSeriesRecord`, `AlertEvent`) share field-level codecs — constraints propagate automatically
- Layer 2: pure domain functions (`buildTimeSeriesRecord`, `shouldAlert`, `buildAlertEvent`) with zero IO
- Layer 3: `SubscribeHandler` + `Publish` orchestrate all MQTT and database IO

Key patterns:
- `TopicParam.WithCodec(uuidCodec)` — validates `{sensorID}` at `BuildTopic` time and flows UUID schema into AsyncAPI spec
- `TopicVarsFromMessage` + wildcard subscription (`sensors/#`) — extracts and validates topic vars from incoming messages
- `SubscribeOptions.OnError` — switch on `KindDecode` vs `KindHandler` to distinguish codec failures from application errors
- `WithFormats(format.YAML(...))` — multi-format MQTT payloads (agreed out-of-band, not in message)
- Publish failure demos: invalid `{sensorID}` → `TopicParamError`; invalid payload → `ValidationErrors`
- AsyncAPI 2.6 spec generation from the same builder

→ [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt)

## examples/adapters-mqtt-contract

Demonstrates the **codec-as-contract** pattern for MQTT. A shared `contract/` package defines channel specs, codecs, and types. Producer and consumer both import it — the compiler enforces the contract.

Key insight: `{sensorID}` topic variable (routing key, UUID) is separate from `SensorReading.SensorID` (application field, non-empty string). The consumer uses `TopicVarsFromMessage` to extract the validated routing UUID rather than assuming the payload field is a UUID.

→ [examples/adapters-mqtt-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-contract)

## examples/adapters-mqtt-security

Demonstrates MQTT credential validation via `SubscribeOptions.SecurityFunc`.

→ [examples/adapters-mqtt-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-security)
