package codex_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// omitDoc is a small test-local type exercising OmitEmptyField/
// OmitEmptyFieldFunc/OmitDefaultField side-by-side with RequiredField --
// mirrors dockercompose.Service's real-world "Context==\"\" means unset"
// convention for the slice field's custom predicate.
type omitDoc struct {
	Name    string   // RequiredField -- always encoded
	Nick    string   // OmitEmptyField -- omitted when ""
	Tags    []string // OmitEmptyFieldFunc -- omitted when nil (not just len==0)
	Level   string   // OmitDefaultField -- omitted when == "info"
	Context string   // used only by TestOmitEmptyField_EncodeVarsIgnoresSparseRule
}

func omitDocCodec() codex.Codec[omitDoc] {
	return codex.Struct[omitDoc](
		codex.RequiredField("name", codex.String(),
			func(d omitDoc) string { return d.Name },
			func(d *omitDoc, v string) { d.Name = v }),
		codex.OmitEmptyField("nick", codex.String(),
			func(d omitDoc) string { return d.Nick },
			func(d *omitDoc, v string) { d.Nick = v }),
		codex.OmitEmptyFieldFunc("tags", codex.SliceOf(codex.String()),
			func(d omitDoc) []string { return d.Tags },
			func(d *omitDoc, v []string) { d.Tags = v },
			func(v []string) bool { return v == nil }),
		codex.OmitDefaultField("level", codex.String(), "info",
			func(d omitDoc) string { return d.Level },
			func(d *omitDoc, v string) { d.Level = v }),
	)
}

func TestOmitEmptyField_EncodeOmitsZeroValue(t *testing.T) {
	raw, err := omitDocCodec().Encode(omitDoc{Name: "x"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if _, ok := obj["nick"]; ok {
		t.Error("nick should be absent from the encoded map (zero value)")
	}
}

func TestOmitEmptyField_EncodeWritesNonZeroValue(t *testing.T) {
	raw, err := omitDocCodec().Encode(omitDoc{Name: "x", Nick: "bob"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if obj["nick"] != "bob" {
		t.Errorf("nick = %v, want bob", obj["nick"])
	}
}

func TestOmitEmptyField_DecodeAbsentKeyLeavesZeroValue(t *testing.T) {
	d, err := omitDocCodec().Decode(map[string]any{"name": "x"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if d.Nick != "" {
		t.Errorf("Nick = %q, want zero value", d.Nick)
	}
}

func TestOmitEmptyField_DecodePresentKeyAssigns(t *testing.T) {
	d, err := omitDocCodec().Decode(map[string]any{"name": "x", "nick": "bob"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if d.Nick != "bob" {
		t.Errorf("Nick = %q, want bob", d.Nick)
	}
}

func TestOmitEmptyField_SchemaNeverRequired(t *testing.T) {
	s := omitDocCodec().Schema
	for _, r := range s.Required {
		if r == "nick" || r == "tags" || r == "level" {
			t.Errorf("Schema.Required unexpectedly includes %q", r)
		}
	}
	found := false
	for _, r := range s.Required {
		if r == "name" {
			found = true
		}
	}
	if !found {
		t.Error("Schema.Required should still include \"name\" (RequiredField unaffected)")
	}
}

func TestOmitEmptyFieldFunc_SliceNilPredicate(t *testing.T) {
	// Never-populated (nil) slice -- omitted.
	raw, err := omitDocCodec().Encode(omitDoc{Name: "x"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if _, ok := obj["tags"]; ok {
		t.Error("tags should be absent from the encoded map (nil slice)")
	}

	// Explicitly-set, non-nil EMPTY slice -- kept, not conflated with nil.
	raw, err = omitDocCodec().Encode(omitDoc{Name: "x", Tags: []string{}})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj = raw.(map[string]any)
	if _, ok := obj["tags"]; !ok {
		t.Error("tags should be PRESENT for a non-nil empty slice (distinct from nil/unset)")
	}
}

func TestOmitEmptyFieldFunc_CustomIsSetPredicate(t *testing.T) {
	type withBuild struct {
		Context string
		Extra   string
	}
	buildCodec := codex.Struct[withBuild](
		codex.RequiredField("context", codex.String(),
			func(b withBuild) string { return b.Context },
			func(b *withBuild, v string) { b.Context = v }),
		codex.OptionalField("extra", codex.String(),
			func(b withBuild) string { return b.Extra },
			func(b *withBuild, v string) { b.Extra = v }),
	)
	isSet := func(b withBuild) bool { return b.Context != "" }

	type withField struct {
		Build withBuild
	}
	fieldCodec := codex.Struct[withField](
		codex.OmitEmptyFieldFunc("build", buildCodec,
			func(w withField) withBuild { return w.Build },
			func(w *withField, v withBuild) { w.Build = v },
			func(v withBuild) bool { return !isSet(v) }),
	)

	raw, err := fieldCodec.Encode(withField{})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if _, ok := raw.(map[string]any)["build"]; ok {
		t.Error("build should be absent when not IsSet (Context==\"\")")
	}

	raw, err = fieldCodec.Encode(withField{Build: withBuild{Context: "./app"}})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if _, ok := raw.(map[string]any)["build"]; !ok {
		t.Error("build should be present when IsSet (Context != \"\")")
	}
}

func TestStruct_MixesOmitEmptyAndRequiredFields(t *testing.T) {
	raw, err := omitDocCodec().Encode(omitDoc{Name: "x"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if obj["name"] != "x" {
		t.Errorf("name = %v, want x (RequiredField always encoded)", obj["name"])
	}
	if _, ok := obj["nick"]; ok {
		t.Error("nick should be absent (OmitEmptyField, zero value)")
	}
}

func TestStruct_ExistingFieldsUnaffected(t *testing.T) {
	// Regression guard: a struct using ONLY Required/Optional/Default
	// fields (no sparse fields at all) keeps encoding every field,
	// unchanged by the new type-assertion check in Struct's Encode loop.
	type plain struct {
		A string
		B string
		C string
	}
	plainCodec := codex.Struct[plain](
		codex.RequiredField("a", codex.String(),
			func(p plain) string { return p.A },
			func(p *plain, v string) { p.A = v }),
		codex.OptionalField("b", codex.String(),
			func(p plain) string { return p.B },
			func(p *plain, v string) { p.B = v }),
		codex.DefaultField("c", codex.String(), "default-c",
			func(p plain) string { return p.C },
			func(p *plain, v string) { p.C = v }),
	)
	raw, err := plainCodec.Encode(plain{A: "a"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if _, ok := obj["b"]; !ok {
		t.Error("b should still be present (OptionalField always encodes, unaffected by this change)")
	}
	if obj["c"] != "" {
		t.Errorf("c = %v, want zero value (DefaultField only applies on decode)", obj["c"])
	}
	if _, ok := obj["c"]; !ok {
		t.Error("c should still be present (DefaultField always encodes, unaffected by this change)")
	}
}

func TestOmitDefaultField_EncodeOmitsWhenEqualToDefault(t *testing.T) {
	raw, err := omitDocCodec().Encode(omitDoc{Name: "x", Level: "info"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if _, ok := raw.(map[string]any)["level"]; ok {
		t.Error("level should be absent when equal to the declared default")
	}
}

func TestOmitDefaultField_EncodeWritesWhenDifferentFromDefault(t *testing.T) {
	raw, err := omitDocCodec().Encode(omitDoc{Name: "x", Level: "debug"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if obj["level"] != "debug" {
		t.Errorf("level = %v, want debug", obj["level"])
	}
}

func TestOmitDefaultField_DecodeAppliesDefaultWhenAbsent(t *testing.T) {
	d, err := omitDocCodec().Decode(map[string]any{"name": "x"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if d.Level != "info" {
		t.Errorf("Level = %q, want info (declared default applied)", d.Level)
	}
}

func TestIsZeroValue_ScalarsAndNilSlicesAreZero(t *testing.T) {
	if !codex.IsZeroValue(0) {
		t.Error("IsZeroValue(0) = false, want true")
	}
	if !codex.IsZeroValue("") {
		t.Error(`IsZeroValue("") = false, want true`)
	}
	var nilSlice []string
	if !codex.IsZeroValue(nilSlice) {
		t.Error("IsZeroValue(nil slice) = false, want true")
	}
	var nilMap map[string]int
	if !codex.IsZeroValue(nilMap) {
		t.Error("IsZeroValue(nil map) = false, want true")
	}
}

func TestIsZeroValue_NonNilEmptySliceIsNotZero(t *testing.T) {
	if codex.IsZeroValue([]string{}) {
		t.Error("IsZeroValue([]string{}) = true, want false (non-nil, distinct from unset)")
	}
	if codex.IsZeroValue(map[string]int{}) {
		t.Error("IsZeroValue(map[string]int{}) = true, want false (non-nil, distinct from unset)")
	}
}

func TestIsZeroValue_NilInterfaceGuard(t *testing.T) {
	var v any
	if !codex.IsZeroValue(v) {
		t.Error("IsZeroValue(nil interface) = false, want true (no panic, treated as zero)")
	}
}

func TestOmitEmptyField_EncodeVarsIgnoresSparseRule(t *testing.T) {
	// EncodeVars calls a field's plain encode() directly, NOT Struct's
	// sparse-aware Encode loop -- confirms the omit rule is exclusive to
	// Struct, per docs/concepts/codec.md's "Interaction with
	// Template/DottedKeyCodec/DecodeVars/EncodeVars" paragraph.
	field := codex.OmitEmptyField("context", codex.String(),
		func(d omitDoc) string { return d.Context },
		func(d *omitDoc, v string) { d.Context = v })

	vars, err := codex.EncodeVars(omitDoc{}, field)
	if err != nil {
		t.Fatalf("EncodeVars: unexpected error: %v", err)
	}
	if _, ok := vars["context"]; !ok {
		t.Error(`vars["context"] absent -- EncodeVars must still encode a zero value (sparse rule is Struct-loop-exclusive)`)
	}
}
