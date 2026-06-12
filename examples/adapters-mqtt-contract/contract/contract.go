// Package contract is the shared API contract between producer and consumer services.
//
// Both services import this package. The Go compiler enforces the contract:
// any field rename, type change, or constraint modification breaks compilation on
// both sides immediately — no stale AsyncAPI YAML, no schema drift, no code-generation.
//
// The contract defines:
//   - Domain types (SensorReading, Alert)
//   - Codecs — shape, constraints, and schema in one value
//   - Channel specs — topic templates, parameter codecs, and AsyncAPI operations
//
// Unlike [gob-contract], which uses binary gob encoding for internal Go-to-Go
// communication, this package uses JSON — suitable for cross-language brokers
// and for generating a machine-readable AsyncAPI 3.0 specification.
//
// Usage:
//
//	// Producer: register the channel and publish readings.
//	handle, err := contract.ReadingsChannel.Register(producerBuilder)
//	err = adaptermqtt.Publish(ctx, client, handle, 1, false, reading,
//	    map[string]string{"sensorID": id}, adaptermqtt.PublishOptions{})
//
//	// Consumer: register the channel and subscribe to readings.
//	handle, err := contract.ReadingsChannel.Register(consumerBuilder)
//	client.Subscribe(topic, 1, adaptermqtt.SubscribeHandler(ctx, handle, fn, opts))
package contract

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// SensorReading is the event published by a sensor device when it takes a measurement.
type SensorReading struct {
	SensorID  string
	Value     float64
	Unit      string
	Timestamp string // RFC 3339
}

// Alert is the event published by the consumer service when a sensor reading
// exceeds a configured threshold.
type Alert struct {
	SensorID  string
	Value     float64
	Unit      string
	Threshold float64
	Timestamp string // RFC 3339
}

// ── Shared field codecs ───────────────────────────────────────────────────────
//
// Defining field codecs once and composing them into struct codecs ensures
// the same constraint is enforced at every boundary — producer encode,
// consumer decode — without any duplication.

var sensorIDFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Unique sensor identifier.")

var valueFieldCodec = codex.Float64().
	Refine(validate.NonZeroFloat).
	WithDescription("Measured value (non-zero).")

var unitFieldCodec = codex.String().
	Refine(validate.NonEmptyString).
	WithDescription("Physical unit (e.g. celsius, bar, rpm).")

var timestampFieldCodec = codex.String().
	Refine(validate.DateTime).
	WithDescription("Event time (RFC 3339).")

// ── Struct codecs ─────────────────────────────────────────────────────────────

// SensorReadingCodec is the canonical codec for SensorReading.
var SensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField("sensor_id", sensorIDFieldCodec,
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value", valueFieldCodec,
		func(r SensorReading) float64 { return r.Value },
		func(r *SensorReading, v float64) { r.Value = v },
	),
	codex.RequiredField("unit", unitFieldCodec,
		func(r SensorReading) string { return r.Unit },
		func(r *SensorReading, v string) { r.Unit = v },
	),
	codex.RequiredField("timestamp", timestampFieldCodec,
		func(r SensorReading) string { return r.Timestamp },
		func(r *SensorReading, v string) { r.Timestamp = v },
	),
)

// AlertCodec is the canonical codec for Alert.
var AlertCodec = codex.Struct[Alert](
	codex.RequiredField("sensor_id", sensorIDFieldCodec,
		func(a Alert) string { return a.SensorID },
		func(a *Alert, v string) { a.SensorID = v },
	),
	codex.RequiredField("value", valueFieldCodec,
		func(a Alert) float64 { return a.Value },
		func(a *Alert, v float64) { a.Value = v },
	),
	codex.RequiredField("unit", unitFieldCodec,
		func(a Alert) string { return a.Unit },
		func(a *Alert, v string) { a.Unit = v },
	),
	codex.RequiredField("threshold",
		codex.Float64().Refine(validate.NonZeroFloat).WithDescription("Configured alert threshold."),
		func(a Alert) float64 { return a.Threshold },
		func(a *Alert, v float64) { a.Threshold = v },
	),
	codex.RequiredField("timestamp", timestampFieldCodec,
		func(a Alert) string { return a.Timestamp },
		func(a *Alert, v string) { a.Timestamp = v },
	),
)

// ── Topic param codecs ────────────────────────────────────────────────────────
//
// Exported so both producer and consumer reference the exact same constraint.
// BuildTopic and TopicVarsFromMessage both validate against this codec.

// SensorIDCodec validates the {sensorID} topic variable as a UUID v4.
// Exported so the codec is shared between ReadingsChannel and AlertsChannel —
// one definition, two channels, zero duplication.
var SensorIDCodec = codex.String().
	Refine(validate.UUID).
	WithDescription("Sensor UUID (RFC 4122 v4).")

// ── Channel specs ─────────────────────────────────────────────────────────────

// ReadingsChannel is the declarative channel spec for sensor readings.
//
// Topic template: sensors/{sensorID}/readings
//
// Producer:  Register(producerBuilder) → handle → adaptermqtt.Publish(...)
// Consumer:  Register(consumerBuilder) → handle → adaptermqtt.SubscribeHandler(...)
//
// Both sides call Register on their own events.Builder, producing identical
// ChannelHandles from the same spec. The compiler enforces the contract:
// codec changes break both sides at compile time.
var ReadingsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	SensorReadingCodec,
	events.ChannelMeta{
		Title:       "Sensor Readings",
		Description: "Raw sensor measurements published by field devices.",
	},
	events.Subscribe{
		OperationID: "consumeSensorReading",
		Summary:     "Receive a sensor reading",
		SchemaName:  "SensorReading",
		Tags:        []string{"readings"},
	},
	events.Publish{
		OperationID: "publishSensorReading",
		Summary:     "Publish a sensor reading",
		SchemaName:  "SensorReading",
		Tags:        []string{"readings"},
	},
	events.TopicParam{
		Name:        "sensorID",
		Description: "UUID of the sensor publishing the reading.",
	}.WithCodec(SensorIDCodec),
)

// AlertsChannel is the declarative channel spec for threshold alerts.
//
// Topic template: alerts/{sensorID}
//
// The consumer service publishes alerts here when a reading exceeds the threshold.
var AlertsChannel = events.NewChannel[Alert](
	"alerts/{sensorID}",
	AlertCodec,
	events.ChannelMeta{
		Title:       "Threshold Alerts",
		Description: "Alerts published when a sensor reading exceeds a configured threshold.",
	},
	events.Subscribe{
		OperationID: "consumeAlert",
		Summary:     "Receive a threshold alert",
		SchemaName:  "Alert",
		Tags:        []string{"alerts"},
	},
	events.Publish{
		OperationID: "publishAlert",
		Summary:     "Publish a threshold alert",
		SchemaName:  "Alert",
		Tags:        []string{"alerts"},
	},
	events.TopicParam{
		Name:        "sensorID",
		Description: "UUID of the sensor that triggered the alert.",
	}.WithCodec(SensorIDCodec),
)
