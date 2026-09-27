package events

import (
	"fmt"
	"log/slog"
)

// CapabilitySpec declares one adapter-defined protocol-native capability
// (e.g. MQTT QoS, ZeroMQ high-water-mark) at the channel level — the
// decoupled, adapter-agnostic spec-rendering hook resolved in
// docs/design/d-0006-protocol-native-capabilities.md's §7 ("Spec rendering plan").
//
// CapabilitySpec is declared alongside a channel's other [ChannelOpt]
// values, independent of which adapter (if any) eventually supplies a
// matching sealed Capability value (e.g. mqtt5.QoS) at declare time via
// that adapter's own SubscribeOptions/PublishOptions.Capabilities field.
// This keeps the channel's own declared type fully adapter-agnostic —
// exactly like [TopicParam] — while still letting the spec renderer
// describe protocol-native behavior.
//
// CapabilitySpec implements [ChannelOpt]: pass it directly to [NewChannel].
// It renders as a generic "x-capabilities" AsyncAPI vendor-extension array
// (one entry per declared CapabilitySpec) — chosen over a per-capability
// vendor field (e.g. "x-mqtt5-qos") so the renderer never needs a change
// when a new capability type is introduced.
type CapabilitySpec struct {
	// Name identifies the capability (e.g. "QoS", "Retained", "HWM"). Must
	// match the name a supplying adapter's Capability value reports via
	// [CapabilityNameOf] for [CheckCapabilityCoverage] to recognize it.
	Name string
	// Description is shown in the AsyncAPI spec for this capability.
	Description string
}

func (s CapabilitySpec) applyChannel(cb *channelBuilder) {
	cb.capabilitySpecs = append(cb.capabilitySpecs, s)
}

// CapabilityName is implemented by an adapter's own sealed Capability
// concrete types to report a human-readable name for
// [CheckCapabilityCoverage]/[stats.CapabilityObserver] purposes. Optional —
// [CapabilityNameOf] falls back to a %T-derived name when a capability
// value does not implement this interface.
type CapabilityName interface{ CapabilityName() string }

// CapabilityNameOf returns c's declared name via [CapabilityName], falling
// back to a %T-derived name (e.g. "mqtt5.QoS") when c does not implement
// [CapabilityName].
func CapabilityNameOf(c any) string {
	if n, ok := c.(CapabilityName); ok {
		return n.CapabilityName()
	}
	return fmt.Sprintf("%T", c)
}

// CheckCapabilityCoverage returns [MissingCapabilityError] when declared
// contains a [CapabilitySpec] with no matching entry (by
// [CapabilityNameOf]) in supplied — the capability-mechanism analogue of
// [CheckCoverage]'s own security-scheme drift check. Opt-in, exactly like
// [CheckCoverage]: not compiler-enforced, called automatically by each
// adapter's own bulk ServeSubscribers/Attach-equivalent dispatch at
// startup, so callers never have to remember to invoke it by hand.
func CheckCapabilityCoverage(topic string, declared []CapabilitySpec, supplied []any) error {
	have := make(map[string]bool, len(supplied))
	for _, c := range supplied {
		have[CapabilityNameOf(c)] = true
	}
	var missing []string
	for _, d := range declared {
		if !have[d.Name] {
			missing = append(missing, d.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingCapabilityError{Topic: topic, Names: missing}
}

// MissingCapabilityError is returned by [CheckCapabilityCoverage] when a
// channel declares a [CapabilitySpec] with no matching supplied
// Capability value.
type MissingCapabilityError struct {
	Topic string
	Names []string
}

func (e *MissingCapabilityError) Error() string {
	return fmt.Sprintf("api/events: topic %q declares capabilities %v with no matching supplied Capability", e.Topic, e.Names)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e *MissingCapabilityError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("names", e.Names),
	)
}
