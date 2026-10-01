package events

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/stats"
)

// CapabilityRequirement declares one adapter-defined protocol-native
// capability requirement (Tier 3 — Explicit, per
// docs/design/d-0006-protocol-native-capabilities.md's three-tier
// vocabulary: Baseline/Implicit/Explicit) at the channel level — the
// decoupled, adapter-agnostic spec-rendering hook resolved in
// docs/design/d-0006-protocol-native-capabilities.md's §7 ("Spec rendering
// plan"). Renamed from CapabilitySpec (a breaking rename, deliberate —
// see the roadmap doc's Phase 1 subsection) to match this package's own
// Capability* naming family (CapabilityName/CapabilityNameOf/
// CapabilityObserver) and the "declare a requirement" vocabulary; "Spec"
// was ambiguous with AsyncAPI spec-rendering.
//
// CapabilityRequirement is declared alongside a channel's other
// [ChannelOpt] values, independent of which adapter (if any) eventually
// supplies a matching sealed Capability value (e.g. mqtt5.QoS) at declare
// time via that adapter's own SubscribeOptions/PublishOptions.Capabilities
// field. This keeps the channel's own declared type fully adapter-agnostic
// — exactly like [TopicParam] — while still letting the spec renderer
// describe protocol-native behavior.
//
// CapabilityRequirement implements [ChannelOpt]: pass it directly to
// [NewChannel]. It renders as a generic "x-capabilities" AsyncAPI
// vendor-extension array (one entry per declared CapabilityRequirement)
// — chosen over a per-capability vendor field (e.g. "x-mqtt5-qos") so the
// renderer never needs a change when a new capability type is introduced.
type CapabilityRequirement struct {
	// Name identifies the capability (e.g. "QoS", "Retained", "HWM"). Must
	// match the name a supplying adapter's Capability value reports via
	// [CapabilityNameOf] for [CheckCapabilityCoverage] to recognize it.
	Name string
	// Description is shown in the AsyncAPI spec for this capability.
	Description string
	// MinLevel, when non-nil, requires the supplied capability matching
	// Name to ALSO implement [LeveledCapability] and report a Level() >=
	// *MinLevel — a genuine VALUE check, not just presence. When nil,
	// presence-by-Name is sufficient — the correct check for
	// boolean/no-natural-ordering capabilities (e.g. Retained, Conflate).
	MinLevel *int
}

// applyChannel — unchanged body from the pre-rename CapabilitySpec.applyChannel,
// just on the renamed receiver type; no behavior change.
func (r CapabilityRequirement) applyChannel(cb *channelBuilder) {
	cb.requirements = append(cb.requirements, r)
}

// CapabilityName is implemented by an adapter's own sealed Capability
// concrete types to report a human-readable name for
// [CheckCapabilityCoverage]/[stats.CapabilityObserver] purposes. Optional —
// [CapabilityNameOf] falls back to a %T-derived name when a capability
// value does not implement this interface.
//
// Unchanged, not renamed, by Phase 1 — stated explicitly since everything
// else in this file IS renamed.
type CapabilityName interface{ CapabilityName() string }

// CapabilityNameOf returns c's declared name via [CapabilityName], falling
// back to a %T-derived name (e.g. "mqtt5.QoS") when c does not implement
// [CapabilityName].
//
// Unchanged, not renamed, by Phase 1.
func CapabilityNameOf(c any) string {
	if n, ok := c.(CapabilityName); ok {
		return n.CapabilityName()
	}
	return fmt.Sprintf("%T", c)
}

// LeveledCapability is an OPTIONAL extension to an adapter's own sealed
// Capability value (mirrors [CapabilityName]'s existing optional-interface
// pattern — api/events never imports an adapter package to use this).
// Implement Level() to let a [CapabilityRequirement] with a non-nil
// MinLevel (e.g. from RequireQoS/RequireHWM) be checked for VALUE, not
// just presence, by [CheckCapabilityCoverage].
type LeveledCapability interface {
	CapabilityName
	// Level returns this capability's own ordinal value (e.g. MQTT QoS
	// 0/1/2, ZeroMQ HWM as a plain int) — semantics are entirely up to the
	// adapter; CheckCapabilityCoverage only ever compares a supplied
	// Level() against the SAME-NAMED requirement's own MinLevel, never
	// across different capability names.
	Level() int
}

// CheckCapabilityCoverage returns [*CapabilityCoverageError] when:
//   - declared contains a [CapabilityRequirement] with NO matching
//     supplied capability at all (by [CapabilityNameOf]) — reported in
//     Missing; OR
//   - a MATCHING supplied capability exists, the requirement's MinLevel is
//     non-nil, and EITHER the supplied value doesn't implement
//     [LeveledCapability] OR its Level() is below MinLevel — reported in
//     Insufficient.
//
// If supplied contains two capabilities with the same [CapabilityNameOf]
// name, the LAST one in the slice wins (a caller supplying duplicate
// same-name capabilities is a caller mistake, not something this function
// rejects). Declaring the same Name twice in declared is allowed
// (redundant, harmless) — each occurrence is evaluated independently.
//
// Opt-in, exactly like [CheckCoverage]: not compiler-enforced, called
// automatically by each adapter's own bulk ServeSubscribers/
// Attach-equivalent dispatch at startup, so callers never have to
// remember to invoke it by hand.
func CheckCapabilityCoverage(topic string, declared []CapabilityRequirement, supplied []any) error {
	have := make(map[string]any, len(supplied))
	for _, c := range supplied {
		have[CapabilityNameOf(c)] = c // last-wins on duplicate Name
	}
	var missing []string
	var insufficient []LevelMismatch
	for _, d := range declared {
		sc, ok := have[d.Name]
		if !ok {
			missing = append(missing, d.Name)
			continue
		}
		if d.MinLevel == nil {
			continue
		}
		lc, ok := sc.(LeveledCapability)
		if !ok {
			insufficient = append(insufficient, LevelMismatch{Name: d.Name, Required: *d.MinLevel, Supplied: -1})
			continue
		}
		if lc.Level() < *d.MinLevel {
			insufficient = append(insufficient, LevelMismatch{Name: d.Name, Required: *d.MinLevel, Supplied: lc.Level()})
		}
	}
	if len(missing) == 0 && len(insufficient) == 0 {
		return nil
	}
	return &CapabilityCoverageError{Topic: topic, Missing: missing, Insufficient: insufficient}
}

// CapabilityCoverageError is returned by [CheckCapabilityCoverage],
// aggregating every declared [CapabilityRequirement] that was found
// unmet — either entirely Missing (no matching supplied capability at
// all), or present but Insufficient (a value below the declared
// MinLevel, or not implementing [LeveledCapability] at all despite
// MinLevel being set).
//
// Replaces MissingCapabilityError (a breaking rename, deliberate — see
// docs/design/d-0006-protocol-native-capabilities.md's Phase 1
// subsection) — the old name no longer described its own shape once
// Insufficient was added.
type CapabilityCoverageError struct {
	Topic        string
	Missing      []string
	Insufficient []LevelMismatch
}

// LevelMismatch reports one [CapabilityRequirement] whose declared
// MinLevel was not met by the matching supplied capability.
type LevelMismatch struct {
	Name     string
	Required int
	Supplied int // -1 when the matching capability didn't implement [LeveledCapability] at all
}

func (e *CapabilityCoverageError) Error() string {
	return fmt.Sprintf("api/events: topic %q declares capabilities %v missing, %v insufficient", e.Topic, e.Missing, e.Insufficient)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e *CapabilityCoverageError) LogValue() slog.Value {
	mismatches := make([]any, len(e.Insufficient)) // []any holding slog.Value elements — valid for slog.Any
	for i, m := range e.Insufficient {
		mismatches[i] = slog.GroupValue(
			slog.String("name", m.Name),
			slog.Int("required", m.Required),
			slog.Int("supplied", m.Supplied),
		)
	}
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("missing", e.Missing),
		slog.Any("insufficient", mismatches),
	)
}

// VerifyCapabilityCoverage is a thin-adapter convenience wrapper over
// [CheckCapabilityCoverage]: it skips the call entirely when declared is
// empty (the common case — no requirements declared), and converts
// supplied (an adapter's own concrete `[]<pkg>.Capability` slice) to
// `[]any` via a generic loop — pure type erasure, zero protocol-specific
// logic. Every adapter's own subscribe-dispatch coverage check should use
// this instead of hand-rolling the guard+conversion+call sequence.
func VerifyCapabilityCoverage[C any](topic string, declared []CapabilityRequirement, supplied []C) error {
	if len(declared) == 0 {
		return nil
	}
	anySupplied := make([]any, len(supplied))
	for i, c := range supplied {
		anySupplied[i] = c
	}
	return CheckCapabilityCoverage(topic, declared, anySupplied)
}

// ResolveCapabilityValue extracts the LAST value of concrete type V found
// in caps (an adapter's own concrete `[]<pkg>.Capability` slice) — pure
// mechanical extraction, identical in shape across every adapter's own
// (now-removed) hand-rolled `resolveCapabilities` function. ok reports
// whether a matching value was found at all.
//
// Example — adapters/mqtt5:
//
//	qos, qosSet := events.ResolveCapabilityValue[Capability, QoS](caps)
//	retained, retainedSet := events.ResolveCapabilityValue[Capability, Retained](caps)
func ResolveCapabilityValue[Iface any, V any](caps []Iface) (v V, ok bool) {
	for _, c := range caps {
		if val, match := any(c).(V); match {
			v, ok = val, true
		}
	}
	return
}

// RecordCapabilityApplied reports cap as successfully applied to obs, IF
// obs implements [stats.CapabilityObserver] — a no-op otherwise. Pure
// boilerplate (type-assertion + method call), identical across every
// adapter's own capability-application call sites; callers gate this on
// their own "was this capability actually supplied" check BEFORE calling
// (this function itself does not gate on presence).
func RecordCapabilityApplied(obs stats.Observer, location string, cap CapabilityName) {
	if capObs, ok := obs.(stats.CapabilityObserver); ok {
		capObs.RecordCapabilityApplied(location, cap.CapabilityName())
	}
}

// ApplyCapabilities is the API-LAYER-OWNED capability dispatch loop —
// docs/design/d-0006-protocol-native-capabilities.md's Phase 4: rather
// than an adapter defining its OWN resolve+assert+call+record loop (the
// pre-Phase-4 pattern, e.g. adapters/zeromq's now-removed
// `applyCapabilities` function), every adapter's own capability VALUE
// type implements `Apply(T) (applied bool, err error)` against its OWN
// protocol-specific target type T (e.g. `*pahomqtt5.Publish`,
// `zeromq.FramedSocket`) — and THIS function, living in api/events, is
// the ONE place that calls through that interface, for every adapter,
// via Go generics (T is inferred per call site, zero per-adapter
// special-casing needed here).
//
// caps is applied IN ORDER — later entries overwrite earlier ones for
// the same effective field on target, mirroring ordinary struct-field
// assignment semantics (no special "last unique type wins" dedup logic
// needed, unlike the old [ResolveCapabilityValue]-based pattern).
//
// applied=false (from Apply) means "target structurally cannot carry
// this capability" — a documented, silent no-op, NOT an error (mirrors
// every existing Capability's own established convention). A genuine
// Apply error is likewise swallowed here (no RecordCapabilityApplied),
// matching the pre-Phase-4 `if err == nil { RecordCapabilityApplied }`
// pattern byte-for-byte — no behavior change, only WHERE the loop lives.
//
// Example — adapters/mqtt5 (T = *WireAttributes, an adapter-owned
// intermediate letting ONE Apply method serve BOTH the publish and
// subscribe call sites, which target different paho struct types):
//
//	var wire mqtt5.WireAttributes
//	events.ApplyCapabilities(opts.Capabilities, &wire, obs, path)
//	pub := &pahomqtt5.Publish{QoS: wire.QoS, Retain: wire.Retained, ...}
func ApplyCapabilities[C interface{ Apply(T) (bool, error) }, T any](caps []C, target T, obs stats.Observer, location string) {
	for _, c := range caps {
		applied, err := c.Apply(target)
		if applied && err == nil {
			if nc, ok := any(c).(CapabilityName); ok {
				RecordCapabilityApplied(obs, location, nc)
			}
		}
	}
}
