package mqtt5

// Capability is the sealed interface for adapters/mqtt5-specific
// protocol-native declarations. Supplied via [SubscribeOptions.Capabilities]/
// [PublishOptions.Capabilities] at DECLARE time (attached via
// [events.Subscriber.WithOptions]/[events.Publisher.WithOptions]) — NOT a
// new Attach-time parameter. Sealed so an [adapters/mqtt.Capability] or
// [adapters/zeromq.Capability] value (even though [QoS]/[Retained]'s
// underlying numeric values are identical, 0/1/2) cannot be supplied here
// — the Go compiler rejects the mismatch at build time. A SEPARATE sealed
// type from [adapters/mqtt.Capability], deliberately not shared — a
// future divergence between MQTT v3 and MQTT 5 QoS semantics would not
// require touching a shared type. See
// docs/design/d-0006-protocol-native-capabilities.md's §2/§5.1/§7 (Review-13).
type Capability interface{ isMQTT5Capability() }

// QoS is a sealed [Capability] declaring the MQTT 5 quality-of-service
// level for one channel. Equivalent to the pre-existing
// [SubscribeOptions.QoS] field (kept, not deprecated — an escape hatch
// for the common single-value case); QoS via Capabilities is the
// RECOMMENDED, sealed/compile-time-checked path going forward.
type QoS byte

const (
	QoSAtMostOnce  QoS = 0
	QoSAtLeastOnce QoS = 1
	QoSExactlyOnce QoS = 2
)

func (QoS) isMQTT5Capability() {}

// CapabilityName implements [events.CapabilityName].
func (QoS) CapabilityName() string { return "QoS" }

// Retained is a sealed [Capability] declaring the MQTT 5 retained-message
// flag for one outgoing publish. Equivalent to the pre-existing
// [events.PublishAttributes.Retained]/call-time retained parameter (kept,
// not deprecated).
type Retained bool

func (Retained) isMQTT5Capability() {}

// CapabilityName implements [events.CapabilityName].
func (Retained) CapabilityName() string { return "Retained" }

// resolveCapabilities extracts the effective QoS/Retained values declared
// via caps. qosSet/retainedSet report whether the corresponding
// capability was present in caps at all.
//
// User Properties remain declared via [UserPropertyParam] (a distinct,
// pre-existing mechanism) rather than as a sealed Capability — folding
// [UserPropertyParam] into this mechanism (embedding
// middleware.Declaration, per §5.2's worked example) is left for a future
// round; the two mechanisms coexist without conflict today.
func resolveCapabilities(caps []Capability) (qos QoS, qosSet bool, retained Retained, retainedSet bool) {
	for _, c := range caps {
		switch v := c.(type) {
		case QoS:
			qos, qosSet = v, true
		case Retained:
			retained, retainedSet = v, true
		}
	}
	return
}
