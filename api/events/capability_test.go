package events

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
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
	declared := []CapabilitySpec{{Name: "QoS"}, {Name: "Retained"}}
	supplied := []any{fakeCapabilityWithName{}}
	// "Fake" doesn't match QoS/Retained, so this must still report missing.
	if err := CheckCapabilityCoverage("topic", declared, supplied); err == nil {
		t.Fatal("want MissingCapabilityError, got nil")
	}

	declared2 := []CapabilitySpec{{Name: "Fake"}}
	if err := CheckCapabilityCoverage("topic", declared2, supplied); err != nil {
		t.Fatalf("want nil error when names match, got %v", err)
	}
}

func TestCheckCapabilityCoverage_fails_reportsMissingNames(t *testing.T) {
	declared := []CapabilitySpec{{Name: "QoS"}, {Name: "Retained"}}
	err := CheckCapabilityCoverage("sensors/x", declared, nil)
	if err == nil {
		t.Fatal("want MissingCapabilityError, got nil")
	}
	var mce *MissingCapabilityError
	if !asMissingCapabilityError(err, &mce) {
		t.Fatalf("want *MissingCapabilityError, got %T", err)
	}
	if mce.Topic != "sensors/x" {
		t.Errorf("want Topic %q, got %q", "sensors/x", mce.Topic)
	}
	if len(mce.Names) != 2 {
		t.Errorf("want 2 missing names, got %v", mce.Names)
	}
}

func asMissingCapabilityError(err error, target **MissingCapabilityError) bool {
	mce, ok := err.(*MissingCapabilityError)
	if !ok {
		return false
	}
	*target = mce
	return true
}

func TestCapabilitySpec_appliesChannelAndRendersSpec(t *testing.T) {
	codec := codex.Codec[string]{}
	ch := NewChannel[string]("sensors/spec", codec,
		CapabilitySpec{Name: "QoS", Description: "MQTT quality-of-service level"})
	sub := ch.WithSubscribe(Subscribe{})
	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.CapabilitySpecs) != 1 || handle.CapabilitySpecs[0].Name != "QoS" {
		t.Fatalf("want CapabilitySpecs=[{QoS ...}], got %+v", handle.CapabilitySpecs)
	}
	if handle.Descriptor.Capabilities == nil || handle.Descriptor.Capabilities[0].Name != "QoS" {
		t.Fatalf("want Descriptor.Capabilities to carry the spec, got %+v", handle.Descriptor.Capabilities)
	}
}
