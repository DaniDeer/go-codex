package events

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

type fakeCapabilityWithName struct{}

func (fakeCapabilityWithName) CapabilityName() string { return "Fake" }

type fakeCapabilityWithoutName struct{}

func TestCapabilityNameOf(t *testing.T) {
	if got := CapabilityNameOf(fakeCapabilityWithName{}); got != "Fake" {
		t.Errorf("want %q, got %q", "Fake", got)
	}
	if got := CapabilityNameOf(fakeCapabilityWithoutName{}); got != "events.fakeCapabilityWithoutName" {
		t.Errorf("want %%T-derived name, got %q", got)
	}
}

func TestCheckCapabilityCoverage_passes(t *testing.T) {
	declared := []CapabilityRequirement{{Name: "QoS"}, {Name: "Retained"}}
	supplied := []any{fakeCapabilityWithName{}}
	// "Fake" doesn't match QoS/Retained, so this must still report missing.
	if err := CheckCapabilityCoverage("topic", declared, supplied); err == nil {
		t.Fatal("want CapabilityCoverageError, got nil")
	}

	declared2 := []CapabilityRequirement{{Name: "Fake"}}
	if err := CheckCapabilityCoverage("topic", declared2, supplied); err != nil {
		t.Fatalf("want nil error when names match, got %v", err)
	}
}

func TestCheckCapabilityCoverage_fails_reportsMissingNames(t *testing.T) {
	declared := []CapabilityRequirement{{Name: "QoS"}, {Name: "Retained"}}
	err := CheckCapabilityCoverage("sensors/x", declared, nil)
	if err == nil {
		t.Fatal("want CapabilityCoverageError, got nil")
	}
	var cce *CapabilityCoverageError
	if !asCapabilityCoverageError(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T", err)
	}
	if cce.Topic != "sensors/x" {
		t.Errorf("want Topic %q, got %q", "sensors/x", cce.Topic)
	}
	if len(cce.Missing) != 2 {
		t.Errorf("want 2 missing names, got %v", cce.Missing)
	}
	if len(cce.Insufficient) != 0 {
		t.Errorf("want 0 insufficient entries, got %v", cce.Insufficient)
	}
}

func asCapabilityCoverageError(err error, target **CapabilityCoverageError) bool {
	cce, ok := err.(*CapabilityCoverageError)
	if !ok {
		return false
	}
	*target = cce
	return true
}

// fakeAdapterCapability simulates an adapter's own sealed Capability
// interface + concrete types, for testing [ResolveCapabilityValue]
// without depending on any real adapter package.
type fakeAdapterCapability interface{ isFakeAdapterCapability() }

type fakeQoS int

func (fakeQoS) isFakeAdapterCapability() {}

type fakeRetained bool

func (fakeRetained) isFakeAdapterCapability() {}

func TestResolveCapabilityValue_found(t *testing.T) {
	caps := []fakeAdapterCapability{fakeQoS(1), fakeRetained(true)}
	qos, ok := ResolveCapabilityValue[fakeAdapterCapability, fakeQoS](caps)
	if !ok || qos != 1 {
		t.Errorf("want fakeQoS(1) found, got %v ok=%v", qos, ok)
	}
	retained, ok := ResolveCapabilityValue[fakeAdapterCapability, fakeRetained](caps)
	if !ok || !bool(retained) {
		t.Errorf("want fakeRetained(true) found, got %v ok=%v", retained, ok)
	}
}

func TestResolveCapabilityValue_lastWins(t *testing.T) {
	caps := []fakeAdapterCapability{fakeQoS(0), fakeQoS(2)}
	qos, ok := ResolveCapabilityValue[fakeAdapterCapability, fakeQoS](caps)
	if !ok || qos != 2 {
		t.Errorf("want LAST value fakeQoS(2), got %v ok=%v", qos, ok)
	}
}

func TestResolveCapabilityValue_notFound(t *testing.T) {
	if _, ok := ResolveCapabilityValue[fakeAdapterCapability, fakeQoS](nil); ok {
		t.Error("want ok=false for empty/nil slice")
	}
	caps := []fakeAdapterCapability{fakeRetained(true)}
	if _, ok := ResolveCapabilityValue[fakeAdapterCapability, fakeQoS](caps); ok {
		t.Error("want ok=false when no matching concrete type is present")
	}
}

// mockCapabilityObserver spies on RecordCapabilityApplied calls.
type mockCapabilityObserver struct {
	stats.NoopObserver
	applied []string
}

func (o *mockCapabilityObserver) RecordCapabilityApplied(location, capability string) {
	o.applied = append(o.applied, location+":"+capability)
}

func TestRecordCapabilityApplied_reportsWhenObserverImplementsCapabilityObserver(t *testing.T) {
	obs := &mockCapabilityObserver{}
	RecordCapabilityApplied(obs, "sensors/x", fakeCapabilityWithName{})
	if len(obs.applied) != 1 || obs.applied[0] != "sensors/x:Fake" {
		t.Errorf("want [\"sensors/x:Fake\"], got %v", obs.applied)
	}
}

func TestRecordCapabilityApplied_noopWhenObserverDoesNotImplementCapabilityObserver(t *testing.T) {
	// stats.NoopObserver does NOT implement CapabilityObserver — must not panic.
	RecordCapabilityApplied(stats.NoopObserver{}, "sensors/x", fakeCapabilityWithName{})
}

// fakeWireAttributes mirrors adapters/mqtt5.WireAttributes's role: an
// adapter-owned intermediate target ApplyCapabilities writes into.
type fakeWireAttributes struct {
	QoS byte
}

// fakeQoSCapability mirrors adapters/mqtt5.QoS: a sealed capability value
// implementing both Apply (required by ApplyCapabilities) and
// CapabilityName (optional, consulted for RecordCapabilityApplied).
type fakeQoSCapability byte

func (fakeQoSCapability) CapabilityName() string { return "QoS" }

func (q fakeQoSCapability) Apply(wire *fakeWireAttributes) (bool, error) {
	wire.QoS = byte(q)
	return true, nil
}

// fakeNeverAppliesCapability always reports applied=false — mirrors a
// capability that's structurally incompatible with the target type.
type fakeNeverAppliesCapability struct{}

func (fakeNeverAppliesCapability) CapabilityName() string { return "Never" }

func (fakeNeverAppliesCapability) Apply(*fakeWireAttributes) (bool, error) {
	return false, nil
}

func TestApplyCapabilities_appliesAndRecords(t *testing.T) {
	var wire fakeWireAttributes
	obs := &mockCapabilityObserver{}
	ApplyCapabilities([]fakeQoSCapability{fakeQoSCapability(1)}, &wire, obs, "sensors/x")
	if wire.QoS != 1 {
		t.Errorf("want wire.QoS=1, got %d", wire.QoS)
	}
	if len(obs.applied) != 1 || obs.applied[0] != "sensors/x:QoS" {
		t.Errorf("want [\"sensors/x:QoS\"], got %v", obs.applied)
	}
}

func TestApplyCapabilities_laterEntryOverwritesEarlier(t *testing.T) {
	var wire fakeWireAttributes
	obs := &mockCapabilityObserver{}
	ApplyCapabilities([]fakeQoSCapability{fakeQoSCapability(1), fakeQoSCapability(2)}, &wire, obs, "sensors/x")
	if wire.QoS != 2 {
		t.Errorf("want wire.QoS=2 (last wins), got %d", wire.QoS)
	}
	if len(obs.applied) != 2 {
		t.Errorf("want 2 RecordCapabilityApplied calls, got %d", len(obs.applied))
	}
}

func TestApplyCapabilities_appliedFalse_noRecord(t *testing.T) {
	var wire fakeWireAttributes
	obs := &mockCapabilityObserver{}
	ApplyCapabilities([]fakeNeverAppliesCapability{{}}, &wire, obs, "sensors/x")
	if len(obs.applied) != 0 {
		t.Errorf("want no RecordCapabilityApplied calls for applied=false, got %v", obs.applied)
	}
}

func TestApplyCapabilities_nilObserver_noPanic(t *testing.T) {
	var wire fakeWireAttributes
	// stats.NoopObserver does NOT implement CapabilityObserver — must not panic.
	ApplyCapabilities([]fakeQoSCapability{fakeQoSCapability(1)}, &wire, stats.NoopObserver{}, "sensors/x")
	if wire.QoS != 1 {
		t.Errorf("want wire.QoS=1, got %d", wire.QoS)
	}
}

func TestVerifyCapabilityCoverage_emptyDeclared_skipsEntirely(t *testing.T) {
	// No declared requirements — must return nil without even attempting
	// to build the []any conversion (and must not panic on a nil supplied
	// slice either).
	if err := VerifyCapabilityCoverage[fakeAdapterCapability]("t", nil, nil); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestVerifyCapabilityCoverage_delegatesToCheckCapabilityCoverage(t *testing.T) {
	declared := []CapabilityRequirement{{Name: "Fake"}}
	supplied := []fakeCapabilityWithName{{}}
	if err := VerifyCapabilityCoverage("t", declared, supplied); err != nil {
		t.Fatalf("want nil (name matches), got %v", err)
	}

	missing := []CapabilityRequirement{{Name: "QoS"}}
	err := VerifyCapabilityCoverage("t", missing, supplied)
	var cce *CapabilityCoverageError
	if !asCapabilityCoverageError(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T (%v)", err, err)
	}
	if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
		t.Errorf("want Missing=[QoS], got %v", cce.Missing)
	}
}

func TestCapabilityRequirement_appliesChannelAndRendersSpec(t *testing.T) {
	codec := codex.Codec[string]{}
	ch := NewChannel[string]("sensors/spec", codec,
		CapabilityRequirement{Name: "QoS", Description: "MQTT quality-of-service level"})
	sub := ch.WithSubscribe(Subscribe{})
	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.Requirements) != 1 || handle.Requirements[0].Name != "QoS" {
		t.Fatalf("want Requirements=[{QoS ...}], got %+v", handle.Requirements)
	}
	if handle.Descriptor.Capabilities == nil || handle.Descriptor.Capabilities[0].Name != "QoS" {
		t.Fatalf("want Descriptor.Capabilities to carry the spec, got %+v", handle.Descriptor.Capabilities)
	}
}
