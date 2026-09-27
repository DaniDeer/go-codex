package events

// Address is the sealed-adjacent (not sealed — intentionally open, unlike
// [Capability]) interface for a channel's addressing scheme. TopicAddress
// (string-topic pub/sub, MQTT/ZeroMQ's own model) is the ONLY
// implementation today; a future AMQP-shaped address (exchange + routing
// key + queue) would be a second implementation, added when
// docs/roadmap/amqp-adapter.md's own adapter is actually built — see
// docs/design/d-0006-protocol-native-capabilities.md's §7 Address-parameterization
// discussion for why this stays ADDITIVE (not retrofitted onto
// [Channel]/[NewChannel]) for now.
type Address interface {
	// Template returns this address's topic-template-equivalent string
	// (e.g. "sensors/{sensorID}/data" for [TopicAddress]) — used the same
	// way [Channel]'s own topic string is used today: template-var
	// parsing, [ChannelHandle.BuildTopic]-equivalent derivation, spec
	// rendering.
	Template() string
}

// TopicAddress is the [Address] implementation for today's string-topic
// pub/sub model (MQTT, ZeroMQ) — the ONLY addressing scheme every current
// adapter understands.
type TopicAddress struct {
	Topic string
}

// Template implements [Address].
func (a TopicAddress) Template() string { return a.Topic }
