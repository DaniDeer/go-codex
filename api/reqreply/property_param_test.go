package reqreply_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
)

type propTestIn struct {
	TenantID string
	Meta     struct{ Region string }
}

func TestNewPropertyParam_RequiredAndMerges(t *testing.T) {
	p := reqreply.NewPropertyParam("tenantID", codex.String(),
		func(v propTestIn) string { return v.TenantID },
		func(v *propTestIn, s string) { v.TenantID = s })
	if !p.Required {
		t.Fatalf("want Required true from NewPropertyParam")
	}
	var out propTestIn
	if err := codex.DecodeVars(&out, map[string]string{"tenantID": "acme"}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if out.TenantID != "acme" {
		t.Fatalf("want TenantID=acme, got %q", out.TenantID)
	}
}

func TestNewPropertyParam_MissingRequired_Errors(t *testing.T) {
	p := reqreply.NewPropertyParam("tenantID", codex.String(),
		func(v propTestIn) string { return v.TenantID },
		func(v *propTestIn, s string) { v.TenantID = s })
	var out propTestIn
	if err := codex.DecodeVars(&out, map[string]string{}, p.Field); err == nil {
		t.Fatalf("want error for missing required property")
	}
}

func TestNewOptionalPropertyParam_PresentMergesCorrectly(t *testing.T) {
	p := reqreply.NewOptionalPropertyParam("region", codex.String(),
		func(v propTestIn) string { return v.Meta.Region },
		func(v *propTestIn, s string) { v.Meta.Region = s })
	if p.Required {
		t.Fatalf("want Required false from NewOptionalPropertyParam")
	}
	var out propTestIn
	if err := codex.DecodeVars(&out, map[string]string{"region": "eu"}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if out.Meta.Region != "eu" {
		t.Fatalf("want Meta.Region=eu, got %q", out.Meta.Region)
	}
}

func TestNewOptionalPropertyParam_AbsentLeavesZeroValueNoError(t *testing.T) {
	p := reqreply.NewOptionalPropertyParam("region", codex.String(),
		func(v propTestIn) string { return v.Meta.Region },
		func(v *propTestIn, s string) { v.Meta.Region = s })
	var out propTestIn
	if err := codex.DecodeVars(&out, map[string]string{}, p.Field); err != nil {
		t.Fatalf("want no error when optional property absent, got %v", err)
	}
	if out.Meta.Region != "" {
		t.Fatalf("want zero value when absent, got %q", out.Meta.Region)
	}
}

func TestPropertyParam_WithCodec(t *testing.T) {
	pp := reqreply.PropertyParam{Param: codex.Param{Name: "X"}}.WithCodec(codex.String())
	if pp.Codec == nil {
		t.Fatalf("want Codec set by WithCodec")
	}
}

func TestMergedPropertyParam_WithDescription(t *testing.T) {
	mp := reqreply.NewPropertyParam("tenantID", codex.String(),
		func(v propTestIn) string { return v.TenantID },
		func(v *propTestIn, s string) { v.TenantID = s }).WithDescription("tenant id")
	if mp.Description != "tenant id" {
		t.Fatalf("want Description set, got %q", mp.Description)
	}
}

// TestNewPropertyParam_NestedSubStructField_GetSetReachesIntoSubstruct
// mirrors rest's own TestNestedStructMergeFields_GetSetReachIntoSubstruct
// reference pattern (Round 14) — a merge field whose get/set reach into a
// NESTED sub-struct field, confirming the merge mechanism works
// identically to a flat top-level field.
func TestNewPropertyParam_NestedSubStructField_GetSetReachesIntoSubstruct(t *testing.T) {
	p := reqreply.NewPropertyParam("region", codex.String(),
		func(v propTestIn) string { return v.Meta.Region },
		func(v *propTestIn, s string) { v.Meta.Region = s })
	var out propTestIn
	if err := codex.DecodeVars(&out, map[string]string{"region": "eu-west"}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if out.Meta.Region != "eu-west" {
		t.Fatalf("want nested Meta.Region=eu-west, got %q", out.Meta.Region)
	}
}
