package mqtt

// Capability is the sealed interface for adapters/mqtt-specific
// protocol-native declarations. Supplied via [SubscribeOptions.Capabilities]/
// [PublishOptions.Capabilities] at DECLARE time (attached via
// [events.Subscriber.WithOptions]/[events.Publisher.WithOptions]) — NOT a
// new Attach-time parameter. Sealed so a [adapters/zeromq.Capability]
// (or any other adapter's) value cannot be supplied here — the Go compiler
// rejects the mismatch at build time, with no custom error type needed.
// See docs/design/d-0006-protocol-native-capabilities.md's §2/§7 (Review-13).
type Capability interface{ isMQTTCapability() }

// QoS is a sealed [Capability] declaring the MQTT quality-of-service level
// for one channel. Equivalent to the pre-existing
// [SubscribeOptions.QoS]/call-time qos parameter (kept, not deprecated —
// an escape hatch for the common single-value case); QoS via Capabilities
// is the RECOMMENDED, sealed/compile-time-checked path going forward.
type QoS byte

const (
	QoSAtMostOnce  QoS = 0
	QoSAtLeastOnce QoS = 1
	QoSExactlyOnce QoS = 2
)

func (QoS) isMQTTCapability() {}

// CapabilityName implements [events.CapabilityName].
func (QoS) CapabilityName() string { return "QoS" }

// Retained is a sealed [Capability] declaring the MQTT retained-message
// flag for one outgoing publish. Equivalent to the pre-existing
// call-time retained parameter (kept, not deprecated).
type Retained bool

func (Retained) isMQTTCapability() {}

// CapabilityName implements [events.CapabilityName].
func (Retained) CapabilityName() string { return "Retained" }

// resolveCapabilities extracts the effective QoS/Retained values declared
// via caps, applied and reported to obs.RecordCapabilityApplied for every
// exercised capability. qosSet/retainedSet report whether the
// corresponding capability was present in caps at all — callers use this
// to decide whether a Capabilities-derived value should override an
// existing default.
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
