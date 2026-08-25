package codex_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── DottedKeyCodec ───────────────────────────────────────────────────────────

type moduleKeyDK struct {
	Tenant string
	Name   string
}

var moduleKeyDKFields = []codex.FieldCodec[moduleKeyDK]{
	codex.RequiredField("tenant", codex.String().Refine(validate.Slug),
		func(k moduleKeyDK) string { return k.Tenant },
		func(k *moduleKeyDK, v string) { k.Tenant = v }),
	codex.RequiredField("name", codex.String().Refine(validate.Slug),
		func(k moduleKeyDK) string { return k.Name },
		func(k *moduleKeyDK, v string) { k.Name = v }),
}

func TestDottedKeyCodec_RoundTrip(t *testing.T) {
	c := codex.DottedKeyCodec("properties.desired.modules.{tenant}.{name}", moduleKeyDKFields...)
	enc, err := c.Encode(moduleKeyDK{Tenant: "tenant-acme", Name: "cv-writer"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc != "properties.desired.modules.tenant-acme.cv-writer" {
		t.Errorf("Encode = %v, want full dotted key", enc)
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec != (moduleKeyDK{Tenant: "tenant-acme", Name: "cv-writer"}) {
		t.Errorf("round-trip = %+v, want original", dec)
	}
}

func TestDottedKeyCodec_Schema_IsString(t *testing.T) {
	c := codex.DottedKeyCodec("properties.desired.modules.{tenant}.{name}", moduleKeyDKFields...)
	if c.Schema.Type != "string" {
		t.Errorf("Schema.Type = %q, want %q", c.Schema.Type, "string")
	}
}

func TestDottedKeyCodec_PanicsOnWildcardInTemplate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for wildcard in template, got none")
		}
	}()
	codex.DottedKeyCodec[moduleKeyDK]("properties.desired.modules.{tenant}.#", moduleKeyDKFields...)
}

func TestDottedKeyCodec_DecodeError_StructuralMismatch(t *testing.T) {
	c := codex.DottedKeyCodec("properties.desired.modules.{tenant}.{name}", moduleKeyDKFields...)
	_, err := c.Decode("wrong.prefix.tenant-acme.cv-writer")
	if err == nil {
		t.Error("expected structural mismatch error, got nil")
	}
}

func TestDottedKeyCodec_DecodeError_StructuralMismatch_ReturnsDottedKeyError(t *testing.T) {
	template := "properties.desired.modules.{tenant}.{name}"
	c := codex.DottedKeyCodec(template, moduleKeyDKFields...)
	_, err := c.Decode("wrong.prefix.tenant-acme.cv-writer")
	var dke codex.DottedKeyError
	if !errors.As(err, &dke) {
		t.Fatalf("expected DottedKeyError, got %T: %v", err, err)
	}
	if dke.Key != "wrong.prefix.tenant-acme.cv-writer" {
		t.Errorf("DottedKeyError.Key = %q, want %q", dke.Key, "wrong.prefix.tenant-acme.cv-writer")
	}
	if dke.Template != template {
		t.Errorf("DottedKeyError.Template = %q, want %q", dke.Template, template)
	}
}

func TestDottedKeyCodec_ComposesWithMapForTypedValues(t *testing.T) {
	type ModuleValue struct{ Image string }
	valueCodec := codex.Struct[ModuleValue](
		codex.RequiredField("image", codex.String(),
			func(v ModuleValue) string { return v.Image },
			func(v *ModuleValue, s string) { v.Image = s }),
	)
	keyCodec := codex.DottedKeyCodec("properties.desired.modules.{tenant}.{name}", moduleKeyDKFields...)
	mapCodec := codex.Map[moduleKeyDK, ModuleValue](keyCodec, valueCodec)

	m := map[moduleKeyDK]ModuleValue{
		{Tenant: "tenant-acme", Name: "cv-writer"}: {Image: "myregistry/cv-writer:1.0"},
	}
	enc, err := mapCodec.Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	if _, ok := encMap["properties.desired.modules.tenant-acme.cv-writer"]; !ok {
		t.Errorf("Encode result = %+v, want full dotted key present", encMap)
	}

	dec, err := mapCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got, ok := dec[moduleKeyDK{Tenant: "tenant-acme", Name: "cv-writer"}]
	if !ok || got.Image != "myregistry/cv-writer:1.0" {
		t.Errorf("Decode = %+v, want typed round trip", dec)
	}

	if mapCodec.Schema.PropertyNames == nil || mapCodec.Schema.PropertyNames.Type != "string" {
		t.Errorf("Schema.PropertyNames = %+v, want {Type: \"string\"} inherited from keyCodec", mapCodec.Schema.PropertyNames)
	}
}

func ExampleDottedKeyCodec() {
	type ModuleKey struct{ Tenant, Name string }
	fields := []codex.FieldCodec[ModuleKey]{
		codex.RequiredField("tenant", codex.String(),
			func(k ModuleKey) string { return k.Tenant },
			func(k *ModuleKey, v string) { k.Tenant = v }),
		codex.RequiredField("name", codex.String(),
			func(k ModuleKey) string { return k.Name },
			func(k *ModuleKey, v string) { k.Name = v }),
	}
	keyCodec := codex.DottedKeyCodec("properties.desired.modules.{tenant}.{name}", fields...)
	key, err := keyCodec.Decode("properties.desired.modules.tenant-acme.cv-writer")
	if err != nil {
		panic(err)
	}
	_ = key
}

// ── DottedPatchMapCodec ──────────────────────────────────────────────────────

func TestDottedPatchMapCodec_Schema_IsObject(t *testing.T) {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.#",
		codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
	)
	if c.Schema.Type != "object" {
		t.Errorf("Schema.Type = %q, want %q", c.Schema.Type, "object")
	}
}

func TestDottedPatchMapCodec_RoundTrip_NamedVarAndTrailingHash(t *testing.T) {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.#",
		codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
	)
	enc, err := c.Encode(map[string]any{"factory-gw.env.API_URL": "http://x"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	if _, ok := encMap["properties.desired.modules.factory-gw.env.API_URL"]; !ok {
		t.Errorf("Encode result = %+v, want prefixed key present", encMap)
	}

	dec, err := c.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec["factory-gw.env.API_URL"] != "http://x" {
		t.Errorf("Decode = %+v, want prefix-free dotted key", dec)
	}
}

func TestDottedPatchMapCodec_BareModuleKey_HashMatchesZeroSegments(t *testing.T) {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.#",
		codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
	)
	dec, err := c.Decode(map[string]any{
		"properties.desired.modules.factory-gw": map[string]any{"status": "stopped"},
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := dec["factory-gw"]; !ok {
		t.Errorf("Decode = %+v, want bare module key present", dec)
	}
}

func TestDottedPatchMapCodec_AnonymousWildcardSegment(t *testing.T) {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.env.+")
	dec, err := c.Decode(map[string]any{
		"properties.desired.modules.factory-gw.env.API_URL": "http://x",
	})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec["factory-gw.env.API_URL"] != "http://x" {
		t.Errorf("Decode = %+v, want env var key present", dec)
	}
	// A key with an extra segment beyond the anonymous wildcard must be rejected.
	_, err = c.Decode(map[string]any{
		"properties.desired.modules.factory-gw.env.API_URL.extra": "http://x",
	})
	if err == nil {
		t.Error("expected structural mismatch for extra segment beyond +, got nil")
	}
}

func TestDottedPatchMapCodec_DecodeError_StructuralMismatch(t *testing.T) {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.#",
		codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
	)
	_, err := c.Decode(map[string]any{"wrong.prefix.factory-gw": "x"})
	var dke codex.DottedKeyError
	if !errors.As(err, &dke) {
		t.Fatalf("expected DottedKeyError, got %v", err)
	}
}

func TestDottedPatchMapCodec_DecodeError_NamedVarConstraintFails(t *testing.T) {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.#",
		codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
	)
	_, err := c.Decode(map[string]any{"properties.desired.modules.Invalid Name!.env": "x"})
	var dke codex.DottedKeyError
	if !errors.As(err, &dke) {
		t.Fatalf("expected DottedKeyError, got %v", err)
	}
}

func TestDottedKeyError_LogValue_HasTemplateKey(t *testing.T) {
	e := codex.DottedKeyError{Key: "bad.key", Template: "prefix.{name}.#", Err: errors.New("boom")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("Kind() = %v, want KindGroup", v.Kind())
	}
	attrs := v.Group()
	found := map[string]bool{}
	for _, a := range attrs {
		found[a.Key] = true
	}
	for _, want := range []string{"key", "template", "err"} {
		if !found[want] {
			t.Errorf("missing attribute %q in %+v", want, attrs)
		}
	}
}

func ExampleDottedPatchMapCodec() {
	c := codex.DottedPatchMapCodec("properties.desired.modules.{moduleName}.#",
		codex.KeyVarConstraint{Name: "moduleName", Constraint: validate.Slug},
	)
	dec, err := c.Decode(map[string]any{
		"properties.desired.modules.factory-gw.env.API_URL": "http://x",
	})
	if err != nil {
		panic(err)
	}
	_ = dec
}
