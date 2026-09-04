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
//	// Producer: register the channel (for AsyncAPI spec purposes) and publish
//	// readings via the handle-based transport (Decision 7).
//	_, err := contract.ReadingsPublisher.Handle(producerBuilder)
//	pubTransport := adaptermqtt.NewPublishTransport[contract.SensorReading](client, 1, false, adaptermqtt.PublishOptions[contract.SensorReading]{})
//	err = events.PublishHandle(ctx, contract.ReadingsPublisher, pubTransport, reading) // reading.RoutingID carries the topic's {sensorID}
//
//	// Consumer: register the channel and subscribe to readings.
//	_, err := contract.ReadingsSubscriber.Handle(consumerBuilder)
//	subTransport := adaptermqtt.NewSubscribeTransport[contract.SensorReading](client, 1, adaptermqtt.SubscribeOptions{})
//	err = events.SubscribeHandle(ctx, contract.ReadingsSubscriber, subTransport, fn) // blocks until ctx is cancelled
package contract

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// SensorReading is the event published by a sensor device when it takes a measurement.
type SensorReading struct {
	SensorID string
	Value    float64
	Unit     string
	// RoutingID is the {sensorID} MQTT topic routing key (a UUID) — the
	// merge-capable field registered via [events.NewTopicParam] on
	// [ReadingsChannel]. Deliberately DISTINCT from SensorID (the
	// application-level device identifier, e.g. "temp-01"): the routing
	// key and the payload field may differ, and RoutingID is populated/
	// read automatically by [events.SubscribeHandle]/[events.PublishHandle]
	// (via each ChannelHandle's declared merge fields) — never part of the
	// JSON wire payload itself (SensorReadingCodec does not encode it).
	RoutingID string
	Timestamp string // RFC 3339
}

// Alert is the event published by the consumer service when a sensor reading
// exceeds a configured threshold.
type Alert struct {
	SensorID string
	Value    float64
	Unit     string
	// RoutingID is the {sensorID} MQTT topic routing key — see
	// [SensorReading.RoutingID]'s doc comment for the shared rationale.
	RoutingID string
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
// Producer:  ReadingsPublisher.Handle(producerBuilder) → events.PublishHandle(...)
// Consumer:  ReadingsSubscriber.Handle(consumerBuilder) → events.SubscribeHandle(...)
//
// Both sides call Handle on their own role-scoped builder (each wrapping the
// SAME underlying Channel value) against their own events.Client, producing
// identical ChannelHandles from the same spec. The compiler enforces the
// contract: codec changes break both sides at compile time.
//
// {sensorID} is declared via [events.NewTopicParam] (not a plain
// [events.TopicParam]) so it merges automatically into/out of
// [SensorReading.RoutingID] — [events.PublishHandle] derives the topic
// variable from RoutingID, and [events.SubscribeHandle] populates
// RoutingID on the decoded value, on every call, with zero manual
// vars-map plumbing.
var ReadingsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	SensorReadingCodec,
	events.ChannelMeta{
		Title:       "Sensor Readings",
		Description: "Raw sensor measurements published by field devices.",
	},
	events.NewTopicParam("sensorID", SensorIDCodec,
		func(r SensorReading) string { return r.RoutingID },
		func(r *SensorReading, v string) { r.RoutingID = v },
	).WithDescription("UUID of the sensor publishing the reading."),
)

// ReadingsSubscriber/ReadingsPublisher fork ReadingsChannel's shared
// topic/codec/TopicParam declaration into its two roles — see
// [events.Channel.WithSubscribe]/[events.Channel.WithPublish]. The
// consumer service calls ReadingsSubscriber.Handle(...); the producer
// service calls ReadingsPublisher.Handle(...).
var ReadingsSubscriber = ReadingsChannel.WithSubscribe(events.Subscribe{
	OperationID: "consumeSensorReading",
	Summary:     "Receive a sensor reading",
	SchemaName:  "SensorReading",
	Tags:        []string{"readings"},
})

var ReadingsPublisher = ReadingsChannel.WithPublish(events.Publish{
	OperationID: "publishSensorReading",
	Summary:     "Publish a sensor reading",
	SchemaName:  "SensorReading",
	Tags:        []string{"readings"},
})

// AlertsChannel is the declarative channel spec for threshold alerts.
//
// Topic template: alerts/{sensorID}
//
// The consumer service publishes alerts here when a reading exceeds the
// threshold. {sensorID} merges into/out of [Alert.RoutingID] — see
// [ReadingsChannel]'s doc comment for the shared rationale.
var AlertsChannel = events.NewChannel[Alert](
	"alerts/{sensorID}",
	AlertCodec,
	events.ChannelMeta{
		Title:       "Threshold Alerts",
		Description: "Alerts published when a sensor reading exceeds a configured threshold.",
	},
	events.NewTopicParam("sensorID", SensorIDCodec,
		func(a Alert) string { return a.RoutingID },
		func(a *Alert, v string) { a.RoutingID = v },
	).WithDescription("UUID of the sensor that triggered the alert."),
)

// AlertsSubscriber/AlertsPublisher fork AlertsChannel's shared
// topic/codec/TopicParam declaration into its two roles — see
// ReadingsSubscriber/ReadingsPublisher's doc comment for the shared
// rationale.
var AlertsSubscriber = AlertsChannel.WithSubscribe(events.Subscribe{
	OperationID: "consumeAlert",
	Summary:     "Receive a threshold alert",
	SchemaName:  "Alert",
	Tags:        []string{"alerts"},
})

var AlertsPublisher = AlertsChannel.WithPublish(events.Publish{
	OperationID: "publishAlert",
	Summary:     "Publish a threshold alert",
	SchemaName:  "Alert",
	Tags:        []string{"alerts"},
})
