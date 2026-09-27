package zeromq

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

// Capability is the sealed interface for adapters/zeromq-specific
// protocol-native declarations. Supplied via [SubscribeOptions.Capabilities]/
// [PublishOptions.Capabilities] at DECLARE time (attached via
// [events.Subscriber.WithOptions]/[events.Publisher.WithOptions]) — NOT a
// new Attach-time parameter. Sealed so an [adapters/mqtt.Capability] or
// [adapters/mqtt5.Capability] value cannot be supplied here — the Go
// compiler rejects the mismatch at build time. See
// docs/design/d-0006-protocol-native-capabilities.md's §2/§6/§7 (Review-13) — zeromq
// is a REAL, non-MQTT capability slice, proving the mechanism generalizes.
type Capability interface{ isZeroMQCapability() }

// HWM is a sealed [Capability] declaring the socket's high-water-mark (the
// outstanding-message queue limit before ZMQ starts dropping/blocking,
// depending on socket type). Applied via [HWMSetter] when the configured
// [FramedSocket] implements it; a no-op otherwise.
type HWM int

func (HWM) isZeroMQCapability() {}

// CapabilityName implements [events.CapabilityName].
func (HWM) CapabilityName() string { return "HWM" }

// Level implements [events.LeveledCapability] — lets a declare-time
// [events.RequireHWM] requirement be checked for VALUE (not just
// presence) by [events.CheckCapabilityCoverage].
func (h HWM) Level() int { return int(h) }

// Conflate is a sealed [Capability] declaring whether the socket should
// keep only the LATEST message per topic (ZMQ_CONFLATE), discarding
// older, still-unread ones. Applied via [ConflateSetter] when the
// configured [FramedSocket] implements it; a no-op otherwise.
type Conflate bool

func (Conflate) isZeroMQCapability() {}

// CapabilityName implements [events.CapabilityName].
func (Conflate) CapabilityName() string { return "Conflate" }

// HWMSetter is an optional extension to [FramedSocket] — implement it to
// let an [HWM] capability configure the socket's high-water-mark. Purely
// additive: a [FramedSocket] that doesn't implement it simply never
// applies [HWM] (a documented no-op, not an error).
type HWMSetter interface {
	SetHWM(n int) error
}

// ConflateSetter is an optional extension to [FramedSocket] — implement
// it to let a [Conflate] capability configure ZMQ_CONFLATE on the socket.
// Purely additive: a [FramedSocket] that doesn't implement it simply
// never applies [Conflate] (a documented no-op, not an error).
type ConflateSetter interface {
	SetConflate(on bool) error
}

// applyCapabilities applies every capability in caps to sock (via
// [HWMSetter]/[ConflateSetter] when sock implements them — a documented
// no-op otherwise), reporting each SUCCESSFULLY applied capability once
// via obs.RecordCapabilityApplied when obs implements
// [stats.CapabilityObserver].
func applyCapabilities(sock FramedSocket, caps []Capability, obs stats.Observer, location string) {
	if len(caps) == 0 {
		return
	}
	hwm, hwmSet := events.ResolveCapabilityValue[Capability, HWM](caps)
	conflate, conflateSet := events.ResolveCapabilityValue[Capability, Conflate](caps)
	if hwmSet {
		if setter, ok := sock.(HWMSetter); ok {
			if err := setter.SetHWM(int(hwm)); err == nil {
				events.RecordCapabilityApplied(obs, location, hwm)
			}
		}
	}
	if conflateSet {
		if setter, ok := sock.(ConflateSetter); ok {
			if err := setter.SetConflate(bool(conflate)); err == nil {
				events.RecordCapabilityApplied(obs, location, conflate)
			}
		}
	}
}
