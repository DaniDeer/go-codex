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

// ── PropertyMerge channel — dedicated to the events.NewPropertyParam
// direct (Middleware-free) attachment demo (mqtt5 User Properties merged
// straight into TenantSensorReading, no Middleware wrapper needed) ─────────

const PropertyMergeTopic = "sensor/tenant-property"

var PropertyMergeChannel = events.NewChannel[TenantSensorReading](
	PropertyMergeTopic,
	TenantSensorReadingCodec,
	events.NewPropertyParam("tenantID", codex.String(),
		func(r TenantSensorReading) string { return r.TenantID },
		func(r *TenantSensorReading, v string) { r.TenantID = v }),
	events.ChannelMeta{Description: "Sensor readings with a directly-attached MergedPropertyParam (mqtt5 User Property → TenantID, no Middleware needed)."},
)

var PropertyMergeSub = PropertyMergeChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveTenantSensorReading",
	Summary:     "Receive a sensor reading with its tenant ID merged from a User Property.",
})

var PropertyMergePub = PropertyMergeChannel.WithPublish(events.Publish{
	OperationID: "publishTenantSensorReading",
	Summary:     "Publish a sensor reading, deriving its tenant ID into a User Property.",
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
// declares an events.ErrorChannel (Mapped mode): when a
// SensorOutOfRangeError reaches the publish adapter, the typed payload is
// published to the declared error-output topic instead of just being
// forwarded to OnError. The events.DeadLetter declaration is the OTHER
// tier of the SAME two-tier fallback: an error type the ErrorChannel does
// NOT declare (SensorOfflineError — a genuine miss) falls through to the
// dead-letter topic instead — see demo_error_pattern.go's DLQ demo, which
// dispatches BOTH error types side-by-side to prove the ordering.
//
// NOTE: BOTH the ErrorChannel's error-output topic AND DeadLetter's topic
// are used LITERALLY — unlike the channel's OWN topic template above (
// "{sensorID}" IS substituted there), neither error mechanism performs
// {var} substitution on ITS OWN topic string (see G2's fix, which
// corrected DeadLetter's docs to stop implying otherwise). A literal
// "{sensorID}" segment here still functions correctly as a topic (MQTT
// permits any string), it just never becomes a real sensor ID on the
// wire — kept here, unsubstituted, deliberately, so demo_error_pattern.go
// prints the ACTUAL published topic rather than a misleading assumption.
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
	events.DeadLetter("sensors/readings/dlq"),
)

var ReadingsWithErrorsPub = ReadingsWithErrorsChannel.WithPublish(events.Publish{
	OperationID: "publishSensorReadingWithErrors",
	Summary:     "Publish a sensor reading (error-path ergonomics demo).",
})

// ReadingsWithErrorsSub is the SAME ReadingsWithErrorsChannel bound as a
// subscribe operation — used by demo_subscribe_error_channel.go to show
// the OTHER Category-A error-channel dispatch point: a HANDLER (fn)
// business error, as opposed to ReadingsWithErrorsPub's upstream pipeline
// error. Both share the SAME declared events.ErrorChannel, proving one
// declaration covers both directions.
var ReadingsWithErrorsSub = ReadingsWithErrorsChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSensorReadingWithErrors",
	Summary:     "Receive a sensor reading (error-path ergonomics demo).",
})

// SensorErrorTopicChannel declares the error-output topic itself
// ("sensors/{sensorID}/readings/errors") as an ORDINARY typed channel —
// the pub/sub analogue of REST/reqreply's client-side ErrorPattern
// recovery: a downstream consumer decodes the typed SensorErrorPayload
// exactly like any other declared message, no errors.As/special decode
// step needed, since the error notification is already just a normal
// message on the wire. See demo_error_channel_consumer.go.
var SensorErrorTopicChannel = events.NewChannel[SensorErrorPayload](
	"sensors/{sensorID}/readings/errors",
	SensorErrorPayloadCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
)

var SensorErrorTopicSub = SensorErrorTopicChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSensorReadingError",
	Summary:     "Receive a typed sensor error notification (error-path ergonomics consumer demo).",
})

// MaintenanceChannel demonstrates events.ErrorChannel's DIRECT mode (no
// mapFn — SensorMaintenanceError itself IS the payload) — the 2nd of
// events' 2 declaration mechanisms (Mapped mode is
// ReadingsWithErrorsChannel above).
var MaintenanceChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/maintenance-demo",
	SensorReadingCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[SensorMaintenanceError, SensorMaintenanceError](
		"sensors/{sensorID}/maintenance-demo/errors", SensorMaintenanceErrorCodec,
	),
)

var MaintenanceSub = MaintenanceChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSensorMaintenanceDemo",
	Summary:     "Receive a sensor reading (ErrorChannel Direct-mode demo, subscribe side).",
})

// MaintenancePub is the SAME MaintenanceChannel bound as a PUBLISH
// operation — used by demo_error_pattern.go to show Direct mode on the
// UPSTREAM PIPELINE error path too (mirroring ReadingsWithErrorsPub's
// role for Mapped mode), so Direct mode is demoed on BOTH sides, not just
// subscribe.
var MaintenancePub = MaintenanceChannel.WithPublish(events.Publish{
	OperationID: "publishSensorMaintenanceDemo",
	Summary:     "Publish a sensor reading (ErrorChannel Direct-mode demo, publish side).",
})

// ── 3 ErrorActions (Respond/Handle/Log) — same SensorOutOfRangeError/
// SensorErrorPayload type, 3 sibling channels so each action's behavior is
// visible independently — see demo_error_pattern.go.
var ActionRespondChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/action-respond-demo",
	SensorReadingCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[SensorOutOfRangeError, SensorErrorPayload](
		"sensors/{sensorID}/action-respond-demo/errors", SensorErrorPayloadCodec,
		func(e SensorOutOfRangeError) (SensorErrorPayload, error) {
			return SensorErrorPayload{Code: "out_of_range", Message: e.Error()}, nil
		},
	), // Action defaults to ErrorRespond when WithAction is not called.
)

var ActionRespondPub = ActionRespondChannel.WithPublish(events.Publish{
	OperationID: "publishActionRespondDemo",
	Summary:     "Publish a sensor reading (ErrorAction: Respond, publish side).",
})

// ActionRespondSub is the SAME ActionRespondChannel bound as a SUBSCRIBE
// operation — used by demo_error_pattern.go to show the 3 ErrorActions on
// the SUBSCRIBE side too (a handler business error), not just the
// publish-side upstream-pipeline-error path ActionRespondPub demos.
var ActionRespondSub = ActionRespondChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveActionRespondDemo",
	Summary:     "Receive a sensor reading (ErrorAction: Respond, subscribe side).",
})

var ActionHandleChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/action-handle-demo",
	SensorReadingCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[SensorOutOfRangeError, SensorErrorPayload](
		"sensors/{sensorID}/action-handle-demo/errors", SensorErrorPayloadCodec,
		func(e SensorOutOfRangeError) (SensorErrorPayload, error) {
			return SensorErrorPayload{Code: "out_of_range", Message: e.Error()}, nil
		},
	).WithAction(events.ErrorHandle),
)

var ActionHandlePub = ActionHandleChannel.WithPublish(events.Publish{
	OperationID: "publishActionHandleDemo",
	Summary:     "Publish a sensor reading (ErrorAction: Handle, publish side).",
})

// ActionHandleSub is the SAME ActionHandleChannel bound as a SUBSCRIBE
// operation — see ActionRespondSub's doc comment.
var ActionHandleSub = ActionHandleChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveActionHandleDemo",
	Summary:     "Receive a sensor reading (ErrorAction: Handle, subscribe side).",
})

var ActionLogChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/action-log-demo",
	SensorReadingCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[SensorOutOfRangeError, SensorErrorPayload](
		"sensors/{sensorID}/action-log-demo/errors", SensorErrorPayloadCodec,
		func(e SensorOutOfRangeError) (SensorErrorPayload, error) {
			return SensorErrorPayload{Code: "out_of_range", Message: e.Error()}, nil
		},
	).WithAction(events.ErrorLog),
)

var ActionLogPub = ActionLogChannel.WithPublish(events.Publish{
	OperationID: "publishActionLogDemo",
	Summary:     "Publish a sensor reading (ErrorAction: Log, publish side).",
})

// ActionLogSub is the SAME ActionLogChannel bound as a SUBSCRIBE
// operation — see ActionRespondSub's doc comment.
var ActionLogSub = ActionLogChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveActionLogDemo",
	Summary:     "Receive a sensor reading (ErrorAction: Log, subscribe side).",
})

// ── Security middleware + ErrorChannel combination ───────────────────────────

// SecuredReadingsChannel pairs a SubscribeMW-attached security Fn (see
// demo_error_pattern.go, which ALWAYS rejects) with a declared
// events.ErrorChannel[events.SecurityError, SecurityRejectedPayload] —
// proving ErrorChannel intercepts a SECURITY-MIDDLEWARE Fn failure
// (auto-wrapped in events.SecurityError by the adapter), not just a
// subscribe-handler business error.
var SecuredReadingsChannel = events.NewChannel[SensorReading](
	"sensors/{sensorID}/secured-errorchannel-demo",
	SensorReadingCodec,
	events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
	events.ErrorChannel[events.SecurityError, SecurityRejectedPayload](
		"sensors/{sensorID}/secured-errorchannel-demo/errors", SecurityRejectedPayloadCodec,
		func(e events.SecurityError) (SecurityRejectedPayload, error) {
			return SecurityRejectedPayload{Code: "security_rejected"}, nil
		},
	),
)

// SecuredReadingsSub is declared PRISTINE (no Security baked in) — the
// SAME pattern SensorDataSub uses — demo_error_pattern.go attaches
// .Use(APIKeyAuthMW).SubscribeMW(&APIKeyAuthMW, alwaysRejectFn) per-demo.
var SecuredReadingsSub = SecuredReadingsChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSecuredReadingsDemo",
	Summary:     "Receive a sensor reading (security-middleware ErrorChannel demo).",
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
