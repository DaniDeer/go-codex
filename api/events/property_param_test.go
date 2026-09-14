package events_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── PropertyParam/MergedPropertyParam[T] construction ────────────────────

func TestNewPropertyParam_RequiredByDefault(t *testing.T) {
	p := events.NewPropertyParam("tenantID", codex.String(),
		func(s string) string { return s },
		func(s *string, v string) { *s = v })
	if !p.Required {
		t.Error("want Required true")
	}
}

func TestNewOptionalPropertyParam_NotRequired(t *testing.T) {
	p := events.NewOptionalPropertyParam("tenantID", codex.String(),
		func(s string) string { return s },
		func(s *string, v string) { *s = v })
	if p.Required {
		t.Error("want Required false")
	}
}

func TestPropertyParam_WithCodec(t *testing.T) {
	c := codex.String()
	p := events.PropertyParam{Param: codex.Param{Name: "x"}}.WithCodec(c)
	if p.Codec == nil {
		t.Fatal("want Codec set")
	}
}

func TestMergedPropertyParam_WithDescription(t *testing.T) {
	p := events.NewPropertyParam("tenantID", codex.String(),
		func(s string) string { return s },
		func(s *string, v string) { *s = v }).WithDescription("tenant id")
	if p.Description != "tenant id" {
		t.Errorf("want Description %q, got %q", "tenant id", p.Description)
	}
}

// ── Round 14: nested sub-struct field access ──────────────────────────────

func TestNewPropertyParam_NestedSubStructField_GetSetReachesIntoSubstruct(t *testing.T) {
	type meta struct{ TenantID string }
	type in struct{ Meta meta }

	p := events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
		func(i in) string { return i.Meta.TenantID },
		func(i *in, v string) { i.Meta.TenantID = v })

	var target in
	if err := codex.DecodeVars(&target, map[string]string{"tenantID": "acme"}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if target.Meta.TenantID != "acme" {
		t.Errorf("want nested field populated, got %+v", target)
	}

	vars, err := codex.EncodeVars(target, p.Field)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["tenantID"] != "acme" {
		t.Errorf("want encoded var %q, got %v", "acme", vars)
	}
}

func TestNewOptionalPropertyParam_AbsentLeavesZeroValue(t *testing.T) {
	type in struct{ TenantID string }
	p := events.NewOptionalPropertyParam("tenantID", codex.String(),
		func(i in) string { return i.TenantID },
		func(i *in, v string) { i.TenantID = v })

	var target in
	if err := codex.DecodeVars(&target, map[string]string{}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if target.TenantID != "" {
		t.Errorf("want zero value, got %q", target.TenantID)
	}
}
