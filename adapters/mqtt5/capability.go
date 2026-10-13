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
//
// Capability now REQUIRES Apply (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4) — this is genuinely
// "an API the adapter implements against," not a marker:
// [events.ApplyCapabilities] (living
// in api/events, NOT here) is the ONE place that calls Apply, for every
// capability, driven entirely by what the declaring user supplied via
// Capabilities — this package no longer owns any resolve+assign loop of
// its own.
type Capability interface {
	isMQTT5Capability()
	// Apply applies this capability's value to wire, returning
	// applied=false when it doesn't apply (never the case for QoS/
	// Retained today, both always apply — reserved for a future
	// capability that might legitimately not) and a non-nil err only on
	// a genuine failure (also not possible for these two, which are pure
	// field assignments).
	Apply(wire *WireAttributes) (applied bool, err error)
}

// WireAttributes is the adapter-owned intermediate [events.ApplyCapabilities]
// targets — NOT a native `paho.golang` struct, deliberately: [QoS] applies
// to BOTH the publish path (`*pahomqtt5.Publish`) and the subscribe path
// (`pahomqtt5.SubscribeOptions`), two genuinely different paho struct
// types, but Go does not allow ONE method (`Apply`) to be overloaded by
// parameter type — WireAttributes lets ONE `Apply(*WireAttributes)`
// method serve BOTH call sites; each call site then copies the
// (possibly capability-overridden) fields into its own native struct.
type WireAttributes struct {
	QoS      byte
	Retained bool
}

// QoS is a sealed [Capability] declaring the MQTT 5 quality-of-service
// level for one channel. Capabilities is now the ONLY mechanism — the
// former plain [SubscribeOptions.QoS] field/call-time qos parameter
// escape hatch has been REMOVED (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 4, a deliberate
// breaking change: every interaction between the API layer and the
// adapter layer now goes
// through the Capability/Apply interface, no competing raw-value path).
type QoS byte

const (
	QoSAtMostOnce  QoS = 0
	QoSAtLeastOnce QoS = 1
	QoSExactlyOnce QoS = 2
)

func (QoS) isMQTT5Capability() {}

// CapabilityName implements [events.CapabilityName].
func (QoS) CapabilityName() string { return "QoS" }

// Level implements [events.LeveledCapability] — lets a declare-time
// [events.RequireQoS] requirement be checked for VALUE (not just
// presence) by [events.CheckCapabilityCoverage].
func (q QoS) Level() int { return int(q) }

// Apply implements [Capability] — sets wire.QoS. Always applies (QoS is
// meaningful in every context this package uses WireAttributes for).
func (q QoS) Apply(wire *WireAttributes) (bool, error) {
	wire.QoS = byte(q)
	return true, nil
}

// Retained is a sealed [Capability] declaring the MQTT 5 retained-message
// flag for one outgoing publish. Capabilities is now the ONLY mechanism
// — the former call-time retained parameter escape hatch has been
// REMOVED (same Phase 4 breaking change as [QoS]).
type Retained bool

func (Retained) isMQTT5Capability() {}

// CapabilityName implements [events.CapabilityName].
func (Retained) CapabilityName() string { return "Retained" }

// Apply implements [Capability] — sets wire.Retained. Always applies to
// the publish path; harmless (ignored) on the subscribe path, which
// never reads wire.Retained.
func (r Retained) Apply(wire *WireAttributes) (bool, error) {
	wire.Retained = bool(r)
	return true, nil
}

// UserProperties is a sealed [Capability] declaring MQTT 5 user-property
// SUPPORT for a channel — presence-only (no natural ordering), same
// reasoning as [Retained]. Pairs with [events.RequireUserProperties] at
// the channel-declaration level (see
// docs/roadmap/adapter-integration-architecture.md's Phase 3, case 3):
// supplying UserProperties(true) via [SubscribeOptions.Capabilities]/
// [PublishOptions.Capabilities] satisfies a declared
// events.RequireUserProperties() requirement for
// [events.CheckCapabilityCoverage]/[events.VerifyCapabilityCoverage].
//
// This is deliberately NARROW: it only makes "does this adapter support
// user properties at all" checkable at declare/attach time, the same
// declare-time REQUIREMENT-COVERAGE concern [CheckCapabilityCoverage]
// already handles for QoS/Retained. It does NOT replace
// [UserPropertyParam] — per-message user-property NAME/VALUE validation
// (a distinct, pre-existing, per-call CAPABILITY-APPLICATION concern, per
// this roadmap's Design goal 12 split) remains exactly as it is today,
// via [SubscribeOptions.UserPropertyParams]/[ServeOptions.UserPropertyParams]
// and [validateUserProperties]. Folding UserPropertyParam's own per-property
// validation fully into the Capability/Apply model (embedding
// middleware.Declaration, per d-0006 §5.2's worked example) remains left
// for a future round; the two mechanisms coexist without conflict.
//
// adapters/mqtt (MQTT 3) has no equivalent type — MQTT 3 has no user
// properties concept at all — so a channel declaring
// events.RequireUserProperties() attached to an MQTT 3 adapter naturally
// has nothing to supply for "UserProperties" and fails
// CheckCapabilityCoverage with an actionable, structured
// CapabilityCoverageError, not a compile error.
type UserProperties bool

func (UserProperties) isMQTT5Capability() {}

// CapabilityName implements [events.CapabilityName].
func (UserProperties) CapabilityName() string { return "UserProperties" }

// Apply implements [Capability]. UserProperties has no WireAttributes
// field of its own to set — its sole purpose is declare-time
// requirement-coverage matching via CapabilityName(), not per-message
// wire application (that remains [UserPropertyParam]'s job). Always
// reports applied=true: presence is all [CheckCapabilityCoverage] checks
// for a requirement with no MinLevel.
func (UserProperties) Apply(*WireAttributes) (bool, error) {
	return true, nil
}
