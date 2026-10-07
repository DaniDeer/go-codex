package routes

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Security scheme ───────────────────────────────────────────────────────────
//
// auth.APIKeyAuth/auth.APIKeyAuthIn/auth.APIKeyAuthOut/auth.NewAPIKeyAuthMW
// now live in examples/events-api/auth — this project's SELF-CONTAINED
// auth module (codecs + middleware declarations + verifier
// implementations + auth-flow demo channels all together). SensorDataSub
// below ATTACHES auth.NewAPIKeyAuthMW at the attachment site
// (mqtt5broker/mqttbroker/zeromqbroker), importing auth.X — a one-way
// dependency, routes/ never imports auth/.

// ── SensorData channel — secured, shared across all 3 adapters ───────────────
//
// Demonstrates: Client.Attach preferred workflow (demo_client_attach_
// workflow.go), SubscribeMW/PublishMW-based security (demo_security_
// subscribemw.go), events.Observability[T] (demo_observability_
// middleware.go), AsyncAPI spec printing proving zero-drift across all 3
// transports (demo_spec_printing_asyncapi.go).
//
// Declares a RELATIVE topic ("data") — Mounted under a REAL
// docs/design/d-0008-declarative-router-groups.md Mount
// (events.NewRouter("sensor")) in mqtt5broker/mqttbroker/zeromqbroker's
// own Build() functions, composing back to the SAME, byte-identical
// absolute topic ("sensor/data") this example has always used. Standalone
// callers OUTSIDE those brokers' own Build() (e.g.
// demo_security_subscribemw.go's bare Client.Publish(ctx,
// routes.SensorDataPub, ...) calls) compose the SAME "sensor" Router via
// .Handle(nil, events.WithRouter(...)) — see that file's own doc
// comment for the full rationale (events.WithRouter closed this exact
// gap this session, mirroring api/rest's/api/reqreply's own identical
// mechanism).
var SensorDataChannel = events.NewChannel[SensorReading](
	"data",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Sensor readings received from the sensor network."},
)

// SensorDataSub is declared PRISTINE (no Security baked into WithSubscribe
// itself) — attachment happens per-demo/per-broker via
// .SubscribeBoundMW(NewAPIKeyAuthMW(implFn)), mirroring examples/reqreply-
// api's own "pristine base, secured at the attachment site" separation
// (see docs/design/d-0002-pubsub-workflow-simplification.md's Addendum
// for the migration guidance).
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

// ObservedTopic is the FINAL, absolute wire topic — ObservedChannel
// below declares a RELATIVE topic ("observed"), Mounted under a REAL
// docs/design/d-0008-declarative-router-groups.md Mount
// (events.NewRouter("sensor")) in demo_observability_middleware.go,
// composing back to this SAME, byte-identical absolute value.
const ObservedTopic = "sensor/observed"

var ObservedChannel = events.NewChannel[SensorReading](
	"observed",
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

// ── Capability channel — dedicated to the protocol-native Capability
// mechanism demo (adapters/mqtt5.QoS/adapters/mqtt5.Retained, supplied via
// SubscribeOptions.Capabilities/PublishOptions.Capabilities). Declares ONE
// events.RequireQoS requirement ("QoS") so the AsyncAPI spec renders
// "x-capabilities" and events.CheckCapabilityCoverage has something to
// check against — only "QoS" (not "Retained") since coverage is
// auto-checked at SUBSCRIBE dispatch time (adapters/mqtt/mqtt5/zeromq's
// ServeSubscribers) against the SUBSCRIBE side's own supplied
// Capabilities; a publish-only capability like "Retained" is
// demonstrated below WITHOUT a declared requirement — CapabilityRequirement
// is opt-in, not a gate on which Capabilities may be supplied. ─────────────

// CapabilityTopic is the FINAL, absolute wire topic — CapabilityChannel
// below declares a RELATIVE topic ("capability"), Mounted under a REAL
// Mount (events.NewRouter("sensor")) in demo_capability_mechanism.go,
// composing back to this SAME, byte-identical absolute value.
const CapabilityTopic = "sensor/capability"

var CapabilityChannel = events.NewChannel[SensorReading](
	"capability",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Sensor readings — protocol-native Capability mechanism demo."},
	// RequireQoS is sugar over events.CapabilityRequirement{Name: "QoS", ...}
	// — declares the SAME requirement, GENUINELY value-checked now (a
	// supplied QoS below AtLeastOnce is rejected, not just name-matched).
	events.RequireQoS(events.AtLeastOnce),
)

var CapabilitySub = CapabilityChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveCapabilityReading",
	Summary:     "Receive a sensor reading (Capability mechanism demo).",
})

var CapabilityPub = CapabilityChannel.WithPublish(events.Publish{
	OperationID: "publishCapabilityReading",
	Summary:     "Publish a sensor reading (Capability mechanism demo).",
})

// ── PropertyMerge channel — dedicated to the events.NewPropertyParam
// direct (Middleware-free) attachment demo (mqtt5 User Properties merged
// straight into TenantSensorReading, no Middleware wrapper needed) ─────────

// PropertyMergeTopic is the FINAL, absolute wire topic —
// PropertyMergeChannel below declares a RELATIVE topic
// ("tenant-property"), Mounted under a REAL Mount
// (events.NewRouter("sensor")) in demo_property_merge_direct_attachment.go,
// composing back to this SAME, byte-identical absolute value.
const PropertyMergeTopic = "sensor/tenant-property"

var PropertyMergeChannel = events.NewChannel[TenantSensorReading](
	"tenant-property",
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

// SecuredReadingsChannel pairs a SubscribeBoundMW-attached security Fn
// (see demo_error_pattern.go, which ALWAYS rejects by granting zero
// scopes) with a declared
// events.ErrorChannel[events.SecurityError, SecurityRejectedPayload] —
// proving ErrorChannel intercepts a Security-carrying Fn's rejection
// (auto-wrapped in events.SecurityError — a FAILING MiddlewareHandler
// whose own Satisfies is non-empty keeps this distinct error type an
// ordinary Middleware Fn failure would NOT get, mirroring REST's/
// reqreply's own isSecuritySatisfyingHandler precedent), not just a
// subscribe-handler business error. This now holds for BOTH the Fn
// returning an error DIRECTLY and the unified middleware.CheckScopes
// failure path (zero granted scopes) — see alwaysRejectFn's own doc
// comment below for why this demo exercises the latter specifically.
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
// .SubscribeBoundMW(NewAPIKeyAuthMW(alwaysRejectFn)) per-demo.
var SecuredReadingsSub = SecuredReadingsChannel.WithSubscribe(events.Subscribe{
	OperationID: "receiveSecuredReadingsDemo",
	Summary:     "Receive a sensor reading (security-middleware ErrorChannel demo).",
	// Security MUST be declared here (unlike SensorDataSub's identical-
	// looking "pristine" comment above) — demo_error_pattern.go's
	// alwaysRejectFn rejects by granting ZERO scopes (a nil error, empty
	// GrantedScopes), which only the adapter's unified
	// middleware.CheckScopes call can turn into a rejection — and
	// CheckScopes only runs when Security is non-empty. A BOUND Fn's OWN
	// DIRECT error return ALSO wraps as events.SecurityError now (a
	// Satisfies-gated fix applied to all 3 events adapters — confirmed
	// via the SAME isSecuritySatisfyingHandler mechanism REST/reqreply
	// already use), so the zero-scopes trick is no longer the ONLY way
	// to exercise this ErrorChannel — it remains a valid, demonstrated
	// path in its own right (not a required workaround anymore).
	Security: []route.SecurityRequirement{route.Require("apiKeyAuth")},
})

// ── zeromq PUB/SUB roundtrip channel ──────────────────────────────────────────

// ZeromqReadingsChannel declares a RELATIVE topic ("zeromq/readings") —
// Mounted under a REAL Mount (events.NewRouter("sensor")) in
// zeromqbroker/broker.go (the SAME Router routes.SensorDataSub already
// Mounts under in that package) and composed consistently at its
// standalone bare-Publish call site in
// demo_zeromq_pubsub_roundtrip.go via events.WithRouter — composing
// back to the SAME, byte-identical absolute topic
// ("sensor/zeromq/readings") this example has always used.
var ZeromqReadingsChannel = events.NewChannel[SensorReading](
	"zeromq/readings",
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
