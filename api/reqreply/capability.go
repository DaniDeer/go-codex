package reqreply

import (
	"fmt"
	"log/slog"
)

// CapabilityRequirement declares one adapter-defined protocol-native
// capability requirement (Tier 3 — Explicit, per docs/design/
// d-0006-protocol-native-capabilities.md's three-tier vocabulary:
// Baseline/Implicit/Explicit) at the route level — the reqreply-side
// counterpart of [events.CapabilityRequirement]. Own type, in its own
// package, deliberately NOT shared with api/events (mirrors
// [stats.Disposition]'s own D-0004 placement precedent — cheap to
// duplicate, avoids an api/reqreply → api/events import that would
// otherwise exist for no other reason than this one small struct).
//
// CapabilityRequirement is declared alongside a route's other [RouteOpt]
// values, independent of which adapter (if any) eventually supplies a
// matching sealed Capability value (e.g. mqtt5.QoS) at declare time via
// that adapter's own ServeOptions/CallOptions.Capabilities field.
//
// CapabilityRequirement implements [RouteOpt]: pass it directly to
// [NewRoute]. It renders as a generic "x-capabilities" AsyncAPI
// vendor-extension array on the route's REQUEST channel (the reply
// channel does not get one — coverage is checked once at dispatch time,
// not per direction), byte-identical rendering shape to events'.
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

func (r CapabilityRequirement) applyRoute(rb *routeBuilder) {
	rb.requirements = append(rb.requirements, r)
}

// CapabilityName is implemented by an adapter's own sealed Capability
// concrete types to report a human-readable name for
// [CheckCapabilityCoverage]/[stats.CapabilityObserver] purposes. Optional —
// [CapabilityNameOf] falls back to a %T-derived name when a capability
// value does not implement this interface. Byte-identical shape to
// [events.CapabilityName].
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

// LeveledCapability is an OPTIONAL extension to an adapter's own sealed
// Capability value (mirrors [CapabilityName]'s existing optional-interface
// pattern — api/reqreply never imports an adapter package to use this).
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
// automatically by each adapter's own Serve/Call dispatch at startup, so
// callers never have to remember to invoke it by hand.
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
	return fmt.Sprintf("api/reqreply: topic %q declares capabilities %v missing, %v insufficient", e.Topic, e.Missing, e.Insufficient)
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
// logic. Every adapter's own Serve/Call dispatch coverage check should use
// this instead of hand-rolling the guard+conversion+call sequence. Built
// in from the START (per Phase 1's post-ship Learnings), not added later
// as a follow-up refactor.
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
