package events

// MQTTQoS mirrors MQTT's three quality-of-service levels without importing
// any MQTT client library (api/events stays transport-agnostic) — both
// adapters/mqtt and adapters/mqtt5 translate this to their own byte wire
// value (numerically identical to the MQTT wire protocol's own QoS byte,
// 0/1/2, by design — no translation table needed).
//
// MQTTQoS/PublishAttributes are an ADDITIVE, still-fully-supported legacy
// path — kept unchanged, not deprecated, since neither has any correctness
// problem. The RECOMMENDED path going forward is each adapter's own sealed
// Capability mechanism (e.g. adapters/mqtt5.QoS/adapters/mqtt5.Retained,
// supplied via SubscribeOptions.Capabilities/PublishOptions.Capabilities),
// which is compile-time-checked (a zeromq.Capability cannot be supplied to
// an mqtt5.SubscribeOptions.Capabilities field — a Go compile error) and
// generalizes to adapters (e.g. zeromq HWM/Conflate) that have no
// MQTT-specific concept at all. See
// docs/design/d-0006-protocol-native-capabilities.md.
type MQTTQoS byte

const (
	// QoSAtMostOnce is MQTT QoS 0 (fire and forget) — the implicit,
	// undeclared default this codebase used before this mechanism existed.
	QoSAtMostOnce  MQTTQoS = 0
	QoSAtLeastOnce MQTTQoS = 1
	QoSExactlyOnce MQTTQoS = 2
)

// PublishAttributes declares the QoS/Retained flag for one outgoing MQTT
// publish — the pub/sub-side counterpart to [rest.CookieAttributes].
// Attach via [Publisher.WithAttributes].
type PublishAttributes struct {
	QoS      MQTTQoS
	Retained bool
}
