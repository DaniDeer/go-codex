package file

import (
	"fmt"
	"log/slog"
	"os"
)

// Capability is the sealed interface for api/file-specific
// protocol-native declarations — mirrors
// [github.com/DaniDeer/go-codex/adapters/mqtt5.Capability]'s own sealing
// pattern (isFileCapability() instead of isMQTT5Capability()), for the
// SAME reason: a Capability meant for one boundary should not compile
// against an unrelated one. Supplied via a role's WithOptions at
// declare/attach time.
type Capability interface {
	isFileCapability()
	// Apply applies this capability's value to wire, returning
	// applied=false when it doesn't apply (e.g. [NativePOSIXMode] on a
	// non-POSIX backend) and a non-nil err only on a genuine failure.
	Apply(wire *WireAttributes) (applied bool, err error)
}

// CapabilityName is an OPTIONAL interface a [Capability] may additionally
// implement for [CheckCapabilityCoverage]/[stats.CapabilityObserver]
// purposes — mirrors
// [github.com/DaniDeer/go-codex/api/events.CapabilityName] EXACTLY:
// deliberately NOT part of the sealed [Capability] interface itself,
// since an external implementer of a future adapter-specific Capability
// should not be FORCED to supply a name — [CapabilityNameOf] falls back
// to a %T-derived name when a capability doesn't implement this.
type CapabilityName interface{ CapabilityName() string }

// CapabilityNameOf returns c's declared name via [CapabilityName],
// falling back to a %T-derived name when c does not implement
// [CapabilityName].
func CapabilityNameOf(c any) string {
	if n, ok := c.(CapabilityName); ok {
		return n.CapabilityName()
	}
	return fmt.Sprintf("%T", c)
}

// WireAttributes is the ONE intermediate value every [Capability.Apply]
// targets — mirrors [adapters/mqtt5.WireAttributes]'s exact rationale.
type WireAttributes struct {
	// AccessMode is the zero value (AccessModeUnset) when unset — "use
	// backend default."
	AccessMode      AccessMode
	NativePOSIXMode *os.FileMode
}

// AccessMode is a sealed [Capability] declaring READ-ONLY vs. READ-WRITE
// access for one Resource role — the ONE permission concept confirmed
// (via direct research, not assumption) to be genuinely portable across
// filesystem/S3/SMB backends. See docs/design/d-0011-file-api-backend-transparency.md's
// "file.Capability" section for the full research rationale (Go's own
// os.Chmod Windows behavior, S3's ACL/policy model).
type AccessMode int

const (
	AccessModeUnset     AccessMode = 0
	AccessModeReadOnly  AccessMode = 1
	AccessModeReadWrite AccessMode = 2
)

func (AccessMode) isFileCapability() {}

// CapabilityName implements [CapabilityName].
func (AccessMode) CapabilityName() string { return "AccessMode" }

// Apply implements [Capability] — sets wire.AccessMode. Always applies.
func (m AccessMode) Apply(wire *WireAttributes) (bool, error) {
	wire.AccessMode = m
	return true, nil
}

// NativePOSIXMode is a sealed [Capability] escape hatch for callers who
// genuinely need literal POSIX mode bits (e.g. 0755) — ONLY meaningful
// on filesystem-family backends. Explicitly a NO-OP (applied=false, nil
// error) on a backend that doesn't honor POSIX permissions (e.g. S3) —
// "doesn't apply here," NOT a failure.
type NativePOSIXMode os.FileMode

func (NativePOSIXMode) isFileCapability() {}

// CapabilityName implements [CapabilityName].
func (NativePOSIXMode) CapabilityName() string { return "NativePOSIXMode" }

// Apply implements [Capability] — sets wire.NativePOSIXMode. Always
// reports applied=true at THIS layer (the dispatch core always records
// the declared value into WireAttributes); whether a given Backend
// actually HONORS it is a separate, backend-specific concern handled at
// the Backend.Mkdir/WriteBytes call site, not here.
func (m NativePOSIXMode) Apply(wire *WireAttributes) (bool, error) {
	v := os.FileMode(m)
	wire.NativePOSIXMode = &v
	return true, nil
}

// CapabilityRequirement declares one role-level capability requirement,
// checked at attach time by [CheckCapabilityCoverage] — mirrors
// [github.com/DaniDeer/go-codex/api/events.CapabilityRequirement]'s
// identical shape, one boundary over.
type CapabilityRequirement struct {
	// Name identifies the capability (e.g. "AccessMode"). Must match the
	// name a supplied Capability value reports via [CapabilityNameOf].
	Name string
	// Description documents the requirement.
	Description string
}

// RequireAccessMode declares that a role requires an [AccessMode]
// Capability to be supplied at attach time — fails loudly (via
// [CheckCapabilityCoverage]) rather than silently, mirroring
// [github.com/DaniDeer/go-codex/api/events.RequireQoS]'s identical
// "fail fast, not silently" contract.
func RequireAccessMode() CapabilityRequirement {
	return CapabilityRequirement{Name: "AccessMode", Description: "requires an AccessMode capability to be supplied"}
}

// CheckCapabilityCoverage returns a [*CapabilityCoverageError] when
// declared contains a [CapabilityRequirement] with NO matching supplied
// capability (by [CapabilityNameOf]). Mirrors
// [github.com/DaniDeer/go-codex/api/events.CheckCapabilityCoverage]'s
// identical first-rule-wins matching logic and error shape — a
// structurally-mirrored, SEPARATELY-OWNED copy (api/events's own
// function has an events-specific `topic string` signature baked in and
// cannot literally be reused here), exactly how [ErrorPattern] is
// already 3 separately-owned types across rest/events/reqreply sharing
// one convention.
func CheckCapabilityCoverage(path string, declared []CapabilityRequirement, supplied []any) error {
	have := make(map[string]any, len(supplied))
	for _, c := range supplied {
		have[CapabilityNameOf(c)] = c // last-wins on duplicate Name
	}
	var missing []string
	for _, d := range declared {
		if _, ok := have[d.Name]; !ok {
			missing = append(missing, d.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &CapabilityCoverageError{Path: path, Missing: missing}
}

// CapabilityCoverageError is returned by [CheckCapabilityCoverage],
// aggregating every declared [CapabilityRequirement] that was found
// unmet.
type CapabilityCoverageError struct {
	Path    string
	Missing []string
}

func (e *CapabilityCoverageError) Error() string {
	return fmt.Sprintf("api/file: resource %q declares capabilities %v missing", e.Path, e.Missing)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e *CapabilityCoverageError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("path", e.Path),
		slog.Any("missing", e.Missing),
	)
}

// ApplyCapabilities applies every capability in caps to a fresh
// [WireAttributes], in order (last-wins on a field collision, e.g. two
// AccessMode values — the SAME convention [CheckCapabilityCoverage]'s
// own last-wins-on-duplicate-Name matching uses).
func ApplyCapabilities(caps []Capability) (WireAttributes, error) {
	var wire WireAttributes
	for _, c := range caps {
		if _, err := c.Apply(&wire); err != nil {
			return wire, err
		}
	}
	return wire, nil
}
