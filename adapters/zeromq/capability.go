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
//
// Apply is REQUIRED (docs/design/d-0006-protocol-native-capabilities.md's
// Phase 4) — every Capability value must know how to apply itself to a
// [FramedSocket], returning applied=false as a documented no-op (not an
// error) when sock doesn't implement the socket-specific setter
// interface it needs. [events.ApplyCapabilities] — an API-LAYER-OWNED,
// fully generic dispatch loop — is the ONLY caller of Apply; this
// package no longer owns its own resolve+dispatch loop.
type Capability interface {
	isZeroMQCapability()
	Apply(sock FramedSocket) (applied bool, err error)
}

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

// Apply implements [Capability]. Configures sock's high-water-mark via
// [HWMSetter] when sock implements it; a documented no-op (applied=false,
// err=nil) otherwise.
func (h HWM) Apply(sock FramedSocket) (bool, error) {
	setter, ok := sock.(HWMSetter)
	if !ok {
		return false, nil
	}
	return true, setter.SetHWM(int(h))
}

// Conflate is a sealed [Capability] declaring whether the socket should
// keep only the LATEST message per topic (ZMQ_CONFLATE), discarding
// older, still-unread ones. Applied via [ConflateSetter] when the
// configured [FramedSocket] implements it; a no-op otherwise.
type Conflate bool

func (Conflate) isZeroMQCapability() {}

// CapabilityName implements [events.CapabilityName].
func (Conflate) CapabilityName() string { return "Conflate" }

// Apply implements [Capability]. Configures ZMQ_CONFLATE on sock via
// [ConflateSetter] when sock implements it; a documented no-op
// (applied=false, err=nil) otherwise.
func (c Conflate) Apply(sock FramedSocket) (bool, error) {
	setter, ok := sock.(ConflateSetter)
	if !ok {
		return false, nil
	}
	return true, setter.SetConflate(bool(c))
}

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

// applyCapabilities applies every capability in caps to sock via
// [events.ApplyCapabilities] — the API-LAYER-OWNED, fully generic
// dispatch loop (docs/design/d-0006-protocol-native-capabilities.md's
// Phase 4). This package contributes only [Capability.Apply]; kept as a
// thin same-signature wrapper so every existing call site (events- AND
// reqreply-side) needs no change this round.
func applyCapabilities(sock FramedSocket, caps []Capability, obs stats.Observer, location string) {
	events.ApplyCapabilities(caps, sock, obs, location)
}
