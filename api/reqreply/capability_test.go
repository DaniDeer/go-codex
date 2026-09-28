package reqreply

import (
	"strings"
	"testing"
)

type fakeCapabilityWithName struct{}

func (fakeCapabilityWithName) CapabilityName() string { return "Fake" }

type fakeCapabilityWithoutName struct{}

func TestCapabilityNameOf(t *testing.T) {
	if got := CapabilityNameOf(fakeCapabilityWithName{}); got != "Fake" {
		t.Errorf("want %q, got %q", "Fake", got)
	}
	if got := CapabilityNameOf(fakeCapabilityWithoutName{}); got != "reqreply.fakeCapabilityWithoutName" {
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
	err := CheckCapabilityCoverage("compute/x", declared, nil)
	if err == nil {
		t.Fatal("want CapabilityCoverageError, got nil")
	}
	var cce *CapabilityCoverageError
	if !asCapabilityCoverageError(err, &cce) {
		t.Fatalf("want *CapabilityCoverageError, got %T", err)
	}
	if cce.Topic != "compute/x" {
		t.Errorf("want Topic %q, got %q", "compute/x", cce.Topic)
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

func TestVerifyCapabilityCoverage_emptyDeclared_skipsEntirely(t *testing.T) {
	// No declared requirements — must return nil without even attempting
	// to build the []any conversion (and must not panic on a nil supplied
	// slice either).
	type fakeAdapterCapability interface{ isFakeAdapterCapability() }
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

func TestCapabilityRequirement_appliesRouteAndRendersSpec(t *testing.T) {
	route := NewRoute[capTestReq, capTestResp]("cap/spec", capTestReqCodec, capTestRespCodec,
		CapabilityRequirement{Name: "QoS", Description: "MQTT quality-of-service level"})
	handle := route.ClientHandle()
	if len(handle.Requirements) != 1 || handle.Requirements[0].Name != "QoS" {
		t.Fatalf("want Requirements=[{QoS ...}], got %+v", handle.Requirements)
	}

	// Also verify the SERVER-side registration renders it into the
	// AsyncAPI spec's request channel as "x-capabilities" — the
	// render-layer half of this mechanism, not just the Go-facing side.
	b := NewServer(Info{Title: "Cap API", Version: "1.0.0"})
	b.AddServer("zmq", ServerEntry{URL: "tcp://localhost:5556", Protocol: "zmq"})
	if _, err := route.Register(b); err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec() error: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error: %v", err)
	}
	out := string(yamlBytes)
	if !strings.Contains(out, "x-capabilities") {
		t.Errorf("want x-capabilities vendor extension in spec:\n%s", out)
	}
	if !strings.Contains(out, "MQTT quality-of-service level") {
		t.Errorf("want capability description in spec:\n%s", out)
	}
}

func TestBuildCapabilityRequirements_empty(t *testing.T) {
	if got := buildCapabilityRequirements(nil); got != nil {
		t.Errorf("want nil for empty input, got %+v", got)
	}
}

func TestBuildCapabilityRequirements_convertsShape(t *testing.T) {
	in := []CapabilityRequirement{{Name: "QoS", Description: "d"}}
	out := buildCapabilityRequirements(in)
	if len(out) != 1 || out[0].Name != "QoS" || out[0].Description != "d" {
		t.Fatalf("want [{QoS d}], got %+v", out)
	}
}
