package routes

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Security scheme ───────────────────────────────────────────────────────────
//
// Declared once — referenced via events.FromSecurityScheme + Subscriber/
// Publisher.Use on any channel that needs it. The Codec field is omitted
// (nil): none of the 3 pub/sub adapters can extract a credential purely
// from message metadata in a protocol-agnostic way (mqtt v3 has none at
// all; mqtt5's User Properties and zeromq's in-payload field are both
// adapter-specific extraction mechanisms), so codec-level FORMAT
// validation of the extracted credential happens per-adapter instead (see
// handlers/security.go).
var APIKeyAuth = events.SecurityScheme{
	SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
}

// APIKeyAuthMW pairs with a SubscribeMW/PublishMW attachment on any
// channel declaring this security requirement — CheckCoverage (run
// unconditionally at Subscriber/Publisher.Handle time) rejects a declared
// scheme with no attached implementation satisfying it.
var APIKeyAuthMW = events.FromSecurityScheme("apiKeyAuth", APIKeyAuth, nil)

// ── SensorData channel — secured, shared across all 3 adapters ───────────────
//
// Demonstrates: Client.Attach preferred workflow (demo_client_attach_
// workflow.go), SubscribeMW/PublishMW-based security (demo_security_
// subscribemw.go), events.Observability[T] (demo_observability_
// middleware.go), AsyncAPI spec printing proving zero-drift across all 3
// transports (demo_spec_printing_asyncapi.go).
var SensorDataChannel = events.NewChannel[SensorReading](
	"sensor/data",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Sensor readings received from the sensor network."},
)

// SensorDataSub is declared PRISTINE (no Security baked into WithSubscribe
// itself) — SubscribeMW attachment happens per-demo/per-broker via
// .Use(APIKeyAuthMW).SubscribeMW(&APIKeyAuthMW, implFn), mirroring
// examples/reqreply-api's own "pristine base, secured at the attachment
// site" separation (see docs/design/d-0002-pubsub-workflow-simplification.md's
// Addendum for the migration guidance).
var SensorDataSub = SensorDataChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSensorReading",
	Summary:     "Receive sensor reading",
	Tags:        []string{"sensor", "iot"},
	SchemaName:  "SensorReading",
	Security:    []route.SecurityRequirement{route.Require("apiKeyAuth")},
})

// SensorDataPub is the unsecured publish role of the SAME channel — used
// by demos that publish sensor readings without any credential.
var SensorDataPub = SensorDataChannel.WithPublish(events.Publish{
	OperationID: "publishSensorReading",
	Summary:     "Publish sensor reading",
	Tags:        []string{"sensor", "iot"},
	SchemaName:  "SensorReading",
})

// ── Plain channel — unsecured, used by the basic Client.Attach demo ─────────

// PlainReadingsTopic is exported (unlike Channel[T]'s own unexported topic
// field) so demo files can wait for broker-side subscription registration
// without needing a *ChannelHandle[T] first.
const PlainReadingsTopic = "sensor/plain"

var PlainReadingsChannel = events.NewChannel[SensorReading](
	PlainReadingsTopic,
	SensorReadingCodec,
	events.ChannelMeta{Description: "Unsecured sensor readings — basic Client.Attach workflow demo."},
)

var PlainReadingsSub = PlainReadingsChannel.WithSubscribe(events.Subscribe{
	OperationID: "receivePlainReading",
	Summary:     "Receive a plain (unsecured) sensor reading.",
})

var PlainReadingsPub = PlainReadingsChannel.WithPublish(events.Publish{
	OperationID: "publishPlainReading",
	Summary:     "Publish a plain (unsecured) sensor reading.",
})

// ── Observed channel — dedicated to the events.Observability[T] demo,
// avoiding double-registration on the SAME topic mqtt5broker.Build
// already attaches PlainReadingsSub to (first-registered-wins would
// otherwise dispatch twice for one message) ─────────────────────────────────

const ObservedTopic = "sensor/observed"

var ObservedChannel = events.NewChannel[SensorReading](
	ObservedTopic,
	SensorReadingCodec,
	events.ChannelMeta{Description: "Sensor readings — events.Observability[T] middleware demo."},
)

var ObservedSub = ObservedChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveObservedReading",
	Summary:     "Receive a sensor reading (observability demo).",
})

var ObservedPub = ObservedChannel.WithPublish(events.Publish{
	OperationID: "publishObservedReading",
	Summary:     "Publish a sensor reading (observability demo).",
})

// ── SensorAlerts channel — publish only, no security ─────────────────────────

var SensorAlertsChannel = events.NewChannel[AlertEvent](
	"sensor/alerts",
	AlertEventCodec,
	events.ChannelMeta{Description: "Alert events produced by this service on threshold breach."},
)

var SensorAlertsPub = SensorAlertsChannel.WithPublish(events.Publish{
	OperationID: "publishSensorAlert",
	Summary:     "Send sensor alert",
	Tags:        []string{"sensor", "alerts"},
	SchemaName:  "SensorAlert",
})

// ── Domain-boundary pipeline channels (mqtt v3-specific demo) ────────────────
//
// Preserves adapters-mqtt's MeasurementEvent→TimeSeriesRecord→AlertEvent
// three-layer scenario — see demo_domain_boundary_pipeline.go and
// docs/concepts/codec-as-domain-boundary.md.

var measurementSensorCodec = codex.String().Refine(validate.UUID)

// MeasurementChannel's {sensorID} TopicParam is MERGE-CAPABLE
// (events.NewTopicParam, not the plain validate-only events.TopicParam)
// — the getter lets Client.Publish derive the topic var automatically
// from MeasurementEvent.SensorID, and the setter lets the subscribe side
// merge the extracted var back into the decoded value.
var MeasurementChannel = events.NewChannel[MeasurementEvent](
	"sensors/{sensorID}/measurements",
	MeasurementEventCodec,
	events.NewTopicParam("sensorID", measurementSensorCodec,
		func(m MeasurementEvent) string { return m.SensorID },
		func(m *MeasurementEvent, v string) { m.SensorID = v },
	).WithDescription("UUID of the originating sensor."),
)

var MeasurementSub = MeasurementChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveMeasurement",
	Summary:     "Receive a sensor measurement.",
})

var MeasurementAlertChannel = events.NewChannel[AlertEvent](
	"sensors/{sensorID}/alerts",
	AlertEventCodec,
	events.NewTopicParam("sensorID", measurementSensorCodec,
		func(a AlertEvent) string { return a.SensorID },
		func(a *AlertEvent, v string) { a.SensorID = v },
	).WithDescription("UUID of the originating sensor."),
)

var MeasurementAlertPub = MeasurementAlertChannel.WithPublish(events.Publish{
	OperationID: "publishMeasurementAlert",
	Summary:     "Publish an alert for a measurement threshold breach.",
})

// ── Wildcard subscription channel (mqtt v3 escape-hatch demo) ────────────────

var WildcardChannel = events.NewChannel[SensorReading](
	"sensors/#",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Wildcard subscription across every sensor topic."},
)

var WildcardSub = WildcardChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveAnySensorReading",
	Summary:     "Receive a reading from any sensor topic (wildcard).",
})

// ── Error-path ergonomics channel (mqtt5-specific demo) ──────────────────────
//
// ReadingsWithErrorsChannel mirrors SensorDataChannel but additionally
// declares an events.ErrorChannel: when a SensorOutOfRangeError reaches
// the publish adapter, the typed payload is published to
// "sensors/{sensorID}/readings/errors" instead of just being forwarded to
// OnError.
var ReadingsWithErrorsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/readings",
	SensorReadingCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[SensorOutOfRangeError, SensorErrorPayload](
		"sensors/{sensorID}/readings/errors", SensorErrorPayloadCodec,
		func(e SensorOutOfRangeError) (SensorErrorPayload, error) {
			return SensorErrorPayload{Code: "out_of_range", Message: e.Error()}, nil
		},
	),
)

var ReadingsWithErrorsPub = ReadingsWithErrorsChannel.WithPublish(events.Publish{
	OperationID: "publishSensorReadingWithErrors",
	Summary:     "Publish a sensor reading (error-path ergonomics demo).",
})

// ── zeromq PUB/SUB roundtrip channel ──────────────────────────────────────────

var ZeromqReadingsChannel = events.NewChannel[SensorReading](
	"sensor/zeromq/readings",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Sensor readings over ZeroMQ PUB/SUB."},
)

var ZeromqReadingsSub = ZeromqReadingsChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveZeromqReading",
	Summary:     "Receive a sensor reading over ZeroMQ.",
})

var ZeromqReadingsPub = ZeromqReadingsChannel.WithPublish(events.Publish{
	OperationID: "publishZeromqReading",
	Summary:     "Publish a sensor reading over ZeroMQ.",
})
