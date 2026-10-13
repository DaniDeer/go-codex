package events

import "fmt"

// QoSLevel is a DECLARATION-ONLY vocabulary for [RequireQoS] — it never
// crosses into an adapter's own sealed Capability type. Its int values
// intentionally mirror MQTT's own wire QoS byte (0/1/2), matching
// adapters/mqtt.QoS's/adapters/mqtt5.QoS's own numerically, but this
// type itself is never compared against or converted to those — only
// used to shape [CapabilityRequirement.Description] and to set
// [CapabilityRequirement.MinLevel] for [CheckCapabilityCoverage]'s
// value-aware check via the supplied capability's own [LeveledCapability]
// implementation.
type QoSLevel int

const (
	AtMostOnce QoSLevel = iota
	AtLeastOnce
	ExactlyOnce
)

// String returns a human-readable label used in [RequireQoS]'s generated
// [CapabilityRequirement.Description].
func (l QoSLevel) String() string {
	switch l {
	case AtLeastOnce:
		return "at-least-once"
	case ExactlyOnce:
		return "exactly-once"
	default:
		return "at-most-once"
	}
}

// RequireQoS declares, at the CHANNEL level — independent of any concrete
// adapter — that this channel needs AT LEAST the given QoS level from
// whichever adapter attaches. Sugar over
// CapabilityRequirement{Name: "QoS", ...}; matches adapters/mqtt.QoS's/
// adapters/mqtt5.QoS's own CapabilityName() ("QoS") for
// [CheckCapabilityCoverage], and their new Level() method (implementing
// [LeveledCapability]) for the genuine value check level enables.
//
// Example — require at-least-once delivery:
//
//	ch := events.NewChannel[SensorReading]("sensor/reading", sensorCodec,
//	    events.RequireQoS(events.AtLeastOnce),
//	)
func RequireQoS(level QoSLevel) ChannelOpt {
	min := int(level)
	return CapabilityRequirement{
		Name:        "QoS",
		Description: fmt.Sprintf("requires at least %s delivery", level),
		MinLevel:    &min,
	}
}

// RequireRetained declares that this channel needs retained-message
// support from whichever adapter attaches. Sugar over
// CapabilityRequirement{Name: "Retained", ...} — presence-only (no
// MinLevel): Retained is a pure boolean toggle, "insufficient level"
// doesn't apply.
func RequireRetained() ChannelOpt {
	return CapabilityRequirement{
		Name:        "Retained",
		Description: "requires retained-message support",
	}
}

// RequireHWM declares that this channel needs high-water-mark
// backpressure control of AT LEAST minimum (today satisfiable only by
// adapters/zeromq.HWM — no mqtt/mqtt5 equivalent exists). Sugar over
// CapabilityRequirement{Name: "HWM", ...}, GENUINELY enforced via
// zeromq.HWM's Level() method (implementing [LeveledCapability]).
func RequireHWM(minimum int) ChannelOpt {
	min := minimum
	return CapabilityRequirement{
		Name:        "HWM",
		Description: fmt.Sprintf("requires HWM >= %d", minimum),
		MinLevel:    &min,
	}
}

// RequireConflate declares that this channel needs "keep only the latest
// message" semantics (today satisfiable only by adapters/zeromq.Conflate).
// Sugar over CapabilityRequirement{Name: "Conflate", ...} — presence-only,
// same reasoning as [RequireRetained].
func RequireConflate() ChannelOpt {
	return CapabilityRequirement{
		Name:        "Conflate",
		Description: "requires keep-latest-only semantics",
	}
}

// RequireUserProperties declares, at the CHANNEL level — independent of
// any concrete adapter — that this channel needs MQTT 5 user-property
// support from whichever adapter attaches. Sugar over
// CapabilityRequirement{Name: "UserProperties", ...} — presence-only, same
// reasoning as [RequireRetained]/[RequireConflate].
//
// This is the protocol-neutral declaration half of folding
// adapters/mqtt5's pre-existing UserPropertyParam mechanism into the
// sealed Capability model (see
// docs/roadmap/adapter-integration-architecture.md's Phase 3, case 3):
// adapters/mqtt5.UserProperties is the only sealed Capability today that
// reports CapabilityName() "UserProperties"; adapters/mqtt (MQTT 3) has
// no equivalent type at all, so a channel declaring this requirement
// attached to an MQTT 3 adapter fails [CheckCapabilityCoverage] with an
// actionable [CapabilityCoverageError] — not a compile error — exactly
// the scenario this requirement exists to make possible.
//
// Example — require MQTT 5 user-property support:
//
//	ch := events.NewChannel[SensorReading]("sensor/reading", sensorCodec,
//	    events.RequireUserProperties(),
//	)
func RequireUserProperties() ChannelOpt {
	return CapabilityRequirement{
		Name:        "UserProperties",
		Description: "requires MQTT 5 user-property support",
	}
}
