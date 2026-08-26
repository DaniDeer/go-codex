package codex_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Param / MergedParam / NewParam ────────────────────────────────────────

func TestParam_WithCodec_ReturnsCopy(t *testing.T) {
	p := codex.Param{Name: "id"}
	c := codex.String()
	p2 := p.WithCodec(c)
	if p.Codec != nil {
		t.Fatal("WithCodec must not mutate original")
	}
	if p2.Codec == nil {
		t.Fatal("WithCodec must set Codec on copy")
	}
}

type tenantReq struct {
	TenantID string
	X        int
}

func TestNewParam_RegistersFieldAndCodec(t *testing.T) {
	mp := codex.NewParam("tenantID", codex.String().Refine(validate.NonEmptyString),
		func(r tenantReq) string { return r.TenantID },
		func(r *tenantReq, v string) { r.TenantID = v })
	if mp.Name != "tenantID" {
		t.Errorf("Name: got %q", mp.Name)
	}
	if mp.Codec == nil {
		t.Fatal("expected Codec to be set")
	}
	if mp.Field == nil {
		t.Fatal("expected Field to be set")
	}
}

func TestMergedParam_WithDescription(t *testing.T) {
	mp := codex.NewParam("tenantID", codex.String(),
		func(r tenantReq) string { return r.TenantID },
		func(r *tenantReq, v string) { r.TenantID = v })
	mp2 := mp.WithDescription("Tenant namespace.")
	if mp2.Description != "Tenant namespace." {
		t.Errorf("Description: got %q", mp2.Description)
	}
}

func TestNewParam_FieldMergesIntoT(t *testing.T) {
	mp := codex.NewParam("tenantID", codex.String().Refine(validate.NonEmptyString),
		func(r tenantReq) string { return r.TenantID },
		func(r *tenantReq, v string) { r.TenantID = v })
	var req tenantReq
	if err := codex.DecodeVars(&req, map[string]string{"tenantID": "acme"}, mp.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if req.TenantID != "acme" {
		t.Errorf("TenantID: got %q", req.TenantID)
	}
}

type intIDReq struct {
	ID int
}

func TestNewParam_TypedValue_MergesIntoNonStringField(t *testing.T) {
	mp := codex.NewParam("id", codex.IntString(),
		func(r intIDReq) int { return r.ID },
		func(r *intIDReq, v int) { r.ID = v })
	if mp.Codec == nil {
		t.Fatal("expected derived string Codec to be set")
	}
	var req intIDReq
	if err := codex.DecodeVars(&req, map[string]string{"id": "42"}, mp.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if req.ID != 42 {
		t.Errorf("ID: got %d, want 42", req.ID)
	}
	got, err := codex.EncodeVars(intIDReq{ID: 42}, mp.Field)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if got["id"] != "42" {
		t.Errorf("EncodeVars: got %q, want \"42\"", got["id"])
	}
}

func TestNewParam_TypedValue_SpecCodecRejectsInvalidString(t *testing.T) {
	mp := codex.NewParam("id", codex.IntString(),
		func(r intIDReq) int { return r.ID },
		func(r *intIDReq, v int) { r.ID = v })
	err := codex.ValidateParams([]codex.Param{mp.Param}, map[string]string{"id": "not-a-number"})
	var pe codex.ParamError
	if !errors.As(err, &pe) {
		t.Fatalf("ValidateParams: got %T: %v, want ParamError", err, err)
	}
}

// ── StringValidatorFrom (direct coverage — previously only exercised
// indirectly via NewParam/NewPathParam/etc.) ──────────────────────────────

func TestStringValidatorFrom_ValidStringPassesThrough(t *testing.T) {
	c := codex.StringValidatorFrom(codex.IntString())
	got, err := c.Decode("42")
	if err != nil {
		t.Fatalf("Decode(\"42\") error = %v", err)
	}
	if got != "42" {
		t.Fatalf("Decode(\"42\") = %q, want unchanged \"42\"", got)
	}
}

func TestStringValidatorFrom_ConstraintFailurePropagates(t *testing.T) {
	c := codex.StringValidatorFrom(codex.IntString())
	_, err := c.Decode("not-a-number")
	if err == nil {
		t.Fatal("Decode(\"not-a-number\") expected error, got nil")
	}
}

func TestStringValidatorFrom_WrongTypeReturnsTypeMismatchError(t *testing.T) {
	c := codex.StringValidatorFrom(codex.IntString())
	_, err := c.Decode(42) // not a string
	var tme codex.TypeMismatchError
	if !errors.As(err, &tme) {
		t.Fatalf("Decode(42) error = %v, want TypeMismatchError", err)
	}
}

func TestStringValidatorFrom_EncodeIsIdentity(t *testing.T) {
	c := codex.StringValidatorFrom(codex.IntString())
	enc, err := c.Encode("42")
	if err != nil {
		t.Fatalf("Encode(\"42\") error = %v", err)
	}
	if enc != "42" {
		t.Fatalf("Encode(\"42\") = %v, want unchanged \"42\"", enc)
	}
}

func TestStringValidatorFrom_SchemaMatchesInnerCodec(t *testing.T) {
	c := codex.StringValidatorFrom(codex.IntString())
	if c.Schema.Type != "integer" {
		t.Fatalf("Schema.Type = %q, want \"integer\"", c.Schema.Type)
	}
}

// ── ValidateParams ─────────────────────────────────────────────────────────

func TestValidateParams_Happy(t *testing.T) {
	params := []codex.Param{codex.Param{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString))}
	if err := codex.ValidateParams(params, map[string]string{"id": "42"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateParams_Missing(t *testing.T) {
	params := []codex.Param{codex.Param{Name: "id"}.WithCodec(codex.String())}
	err := codex.ValidateParams(params, map[string]string{})
	var me codex.MissingParamError
	if !errors.As(err, &me) || me.Name != "id" {
		t.Fatalf("expected MissingParamError{id}, got %T: %v", err, err)
	}
}

func TestValidateParams_CodecFailure(t *testing.T) {
	params := []codex.Param{codex.Param{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString))}
	err := codex.ValidateParams(params, map[string]string{"id": ""})
	var pe codex.ParamError
	if !errors.As(err, &pe) || pe.Name != "id" {
		t.Fatalf("expected ParamError{id}, got %T: %v", err, err)
	}
}

func TestValidateParams_NoCodecSkipsEntirely(t *testing.T) {
	params := []codex.Param{{Name: "id"}} // no codec
	if err := codex.ValidateParams(params, map[string]string{}); err != nil {
		t.Fatalf("unexpected error for uncodec'd param: %v", err)
	}
}

// ── ValidateDeclaredParams ─────────────────────────────────────────────────

func TestValidateDeclaredParams_Happy(t *testing.T) {
	params := []codex.Param{{Name: "id"}}
	if err := codex.ValidateDeclaredParams("items/{id}", params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDeclaredParams_UnknownName(t *testing.T) {
	params := []codex.Param{{Name: "unknown"}}
	err := codex.ValidateDeclaredParams("items/{id}", params)
	var ipe codex.InvalidParamError
	if !errors.As(err, &ipe) || ipe.Name != "unknown" {
		t.Fatalf("expected InvalidParamError{unknown}, got %T: %v", err, err)
	}
	if ipe.Template != "items/{id}" {
		t.Errorf("Template: got %q", ipe.Template)
	}
}

// ── BuildFromParams ────────────────────────────────────────────────────────

func TestBuildFromParams_Happy(t *testing.T) {
	params := []codex.Param{codex.Param{Name: "tenantID"}.WithCodec(codex.String().Refine(validate.NonEmptyString))}
	got, err := codex.BuildFromParams("compute/{tenantID}/add", params, map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("BuildFromParams: %v", err)
	}
	if got != "compute/acme/add" {
		t.Errorf("got %q, want %q", got, "compute/acme/add")
	}
}

func TestBuildFromParams_MissingVar(t *testing.T) {
	params := []codex.Param{{Name: "tenantID"}}
	_, err := codex.BuildFromParams("compute/{tenantID}/add", params, map[string]string{})
	var me codex.MissingParamError
	if !errors.As(err, &me) || me.Name != "tenantID" {
		t.Fatalf("expected MissingParamError{tenantID}, got %T: %v", err, err)
	}
}

func TestBuildFromParams_CodecFailure(t *testing.T) {
	params := []codex.Param{codex.Param{Name: "tenantID"}.WithCodec(codex.String().Refine(validate.NonEmptyString))}
	_, err := codex.BuildFromParams("compute/{tenantID}/add", params, map[string]string{"tenantID": ""})
	var pe codex.ParamError
	if !errors.As(err, &pe) || pe.Name != "tenantID" {
		t.Fatalf("expected ParamError{tenantID}, got %T: %v", err, err)
	}
}

// Regression guard: with TWO vars, a missing FIRST-in-template var must win
// over a codec failure on the SECOND-in-template var.
func TestBuildFromParams_MultiVar_FirstErrorInTemplateOrderWins(t *testing.T) {
	params := []codex.Param{codex.Param{Name: "region"}.WithCodec(codex.String().Refine(validate.NonEmptyString))}
	_, err := codex.BuildFromParams("{tenantID}/{region}", params, map[string]string{"region": ""})
	var me codex.MissingParamError
	if !errors.As(err, &me) || me.Name != "tenantID" {
		t.Fatalf("expected MissingParamError{tenantID} (first in template) to win, got %T: %v", err, err)
	}
}

func TestBuildFromParams_UnregisteredVarSkipsValidation(t *testing.T) {
	got, err := codex.BuildFromParams("compute/{tenantID}/add", nil, map[string]string{"tenantID": ""})
	if err != nil {
		t.Fatalf("BuildFromParams: %v", err)
	}
	if got != "compute//add" {
		t.Errorf("got %q, want %q", got, "compute//add")
	}
}

func TestBuildFromParams_ExtraVarsIgnored(t *testing.T) {
	got, err := codex.BuildFromParams("compute/add", nil, map[string]string{"unused": "x"})
	if err != nil {
		t.Fatalf("BuildFromParams: %v", err)
	}
	if got != "compute/add" {
		t.Errorf("got %q, want %q", got, "compute/add")
	}
}

// ── Error types ─────────────────────────────────────────────────────────────

func TestParamError_ErrorsAs(t *testing.T) {
	inner := errors.New("constraint failed")
	outer := codex.ParamError{Name: "id", Value: "x", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

func TestParamError_LogValue(t *testing.T) {
	e := codex.ParamError{Name: "id", Value: "", Err: errors.New("must not be empty")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"name", "value", "err"} {
		if !keys[want] {
			t.Errorf("missing key %q in LogValue attrs: %+v", want, keys)
		}
	}
}

func TestMissingParamError_LogValue(t *testing.T) {
	e := codex.MissingParamError{Name: "id"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
}

func TestInvalidParamError_LogValue(t *testing.T) {
	e := codex.InvalidParamError{Name: "id", Template: "items/{id}"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	if !keys["name"] || !keys["template"] {
		t.Errorf("missing expected keys: %+v", keys)
	}
}
