package codex_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── DeepMerge ────────────────────────────────────────────────────────────────

func TestDeepMerge_NestedMapsRecursivelyMerge(t *testing.T) {
	dst := map[string]any{
		"a": map[string]any{"x": 1, "y": 2},
		"b": "unchanged",
	}
	src := map[string]any{
		"a": map[string]any{"y": 20, "z": 3},
	}
	codex.DeepMerge(dst, src)

	a := dst["a"].(map[string]any)
	if a["x"] != 1 {
		t.Errorf("a.x = %v, want unchanged 1", a["x"])
	}
	if a["y"] != 20 {
		t.Errorf("a.y = %v, want overridden 20", a["y"])
	}
	if a["z"] != 3 {
		t.Errorf("a.z = %v, want added 3", a["z"])
	}
	if dst["b"] != "unchanged" {
		t.Errorf("b = %v, want unchanged", dst["b"])
	}
}

func TestDeepMerge_ScalarOverwritesRatherThanMerging(t *testing.T) {
	dst := map[string]any{"a": map[string]any{"x": 1}}
	src := map[string]any{"a": "now a scalar"}
	codex.DeepMerge(dst, src)
	if dst["a"] != "now a scalar" {
		t.Errorf("a = %v, want scalar replacement", dst["a"])
	}
}

// ── ApplyPatch ───────────────────────────────────────────────────────────────

type patchBase struct {
	Name  string
	Value int
	Tag   string
}

var patchBaseCodec = codex.Struct[patchBase](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(b patchBase) string { return b.Name },
		func(b *patchBase, v string) { b.Name = v }),
	codex.RequiredField("value", codex.Int(),
		func(b patchBase) int { return b.Value },
		func(b *patchBase, v int) { b.Value = v }),
	codex.OptionalField("tag", codex.String(),
		func(b patchBase) string { return b.Tag },
		func(b *patchBase, v string) { b.Tag = v }),
)

type patchDelta struct {
	Value *int
}

var patchDeltaCodec = codex.PartialStruct[patchDelta](
	codex.PartialField("value", codex.Int(),
		func(p patchDelta) *int { return p.Value },
		func(p *patchDelta, v *int) { p.Value = v }),
)

func TestApplyPatch_RoundTrip(t *testing.T) {
	base := patchBase{Name: "widget", Value: 1, Tag: "v1"}
	v := 42
	got, err := codex.ApplyPatch(base, patchBaseCodec, patchDelta{Value: &v}, patchDeltaCodec)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if got.Value != 42 {
		t.Errorf("Value = %d, want 42", got.Value)
	}
	if got.Name != "widget" || got.Tag != "v1" {
		t.Errorf("siblings changed: %+v", got)
	}
}

func TestApplyPatch_PatchFieldOverwritesBaseField_SiblingsSurvive(t *testing.T) {
	base := patchBase{Name: "widget", Value: 1, Tag: "v1"}
	v := 99
	got, err := codex.ApplyPatch(base, patchBaseCodec, patchDelta{Value: &v}, patchDeltaCodec)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	if got.Value != 99 {
		t.Errorf("Value = %d, want 99", got.Value)
	}
	if got.Name != "widget" {
		t.Errorf("Name = %q, want unchanged widget", got.Name)
	}
}

func TestApplyPatch_BaseEncodeError_Propagated(t *testing.T) {
	failCodec := codex.Codec[patchBase]{
		Encode: func(patchBase) (any, error) { return nil, errors.New("boom") },
		Decode: patchBaseCodec.Decode,
	}
	_, err := codex.ApplyPatch(patchBase{}, failCodec, patchDelta{}, patchDeltaCodec)
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestApplyPatch_PatchEncodeError_Propagated(t *testing.T) {
	failCodec := codex.Codec[patchDelta]{
		Encode: func(patchDelta) (any, error) { return nil, errors.New("patch boom") },
		Decode: patchDeltaCodec.Decode,
	}
	_, err := codex.ApplyPatch(patchBase{Name: "x", Value: 1}, patchBaseCodec, patchDelta{}, failCodec)
	if err == nil || err.Error() != "patch boom" {
		t.Errorf("err = %v, want patch boom", err)
	}
}

func TestApplyPatch_NonObjectBaseIntermediate_ReturnsTypeMismatchError(t *testing.T) {
	scalarCodec := codex.MapCodecSafe(codex.Int(),
		func(i int) int { return i },
		func(i int) (int, error) { return i, nil },
	)
	_, err := codex.ApplyPatch(5, scalarCodec, patchDelta{}, patchDeltaCodec)
	var tme codex.TypeMismatchError
	if !errors.As(err, &tme) {
		t.Fatalf("expected TypeMismatchError, got %v", err)
	}
}

func TestApplyPatch_MergedResultFailsDecodeValidation(t *testing.T) {
	base := patchBase{Name: "widget", Value: 1}
	// Patch sets name to a codec value that violates NonEmptyString.
	type namePatch struct{ Name *string }
	namePatchCodec := codex.PartialStruct[namePatch](
		codex.PartialField("name", codex.String(),
			func(p namePatch) *string { return p.Name },
			func(p *namePatch, v *string) { p.Name = v }),
	)
	empty := ""
	_, err := codex.ApplyPatch(base, patchBaseCodec, namePatch{Name: &empty}, namePatchCodec)
	if err == nil {
		t.Error("expected validation error for empty name, got nil")
	}
}

// ── IsEmptyPatch / NonEmptyPatch / EmptyPatchError ───────────────────────────

func TestIsEmptyPatch_TrueWhenNoFieldsSet(t *testing.T) {
	if !codex.IsEmptyPatch(patchDeltaCodec, patchDelta{}) {
		t.Error("IsEmptyPatch = false, want true for zero-value patch")
	}
}

func TestIsEmptyPatch_FalseWhenOneFieldSet(t *testing.T) {
	v := 1
	if codex.IsEmptyPatch(patchDeltaCodec, patchDelta{Value: &v}) {
		t.Error("IsEmptyPatch = true, want false when a field is set")
	}
}

func TestNonEmptyPatch_ConstraintRejectsEmptyPatch(t *testing.T) {
	wrapped := patchDeltaCodec.Refine(codex.NonEmptyPatch(patchDeltaCodec))
	if err := wrapped.Validate(patchDelta{}); err == nil {
		t.Error("expected error for empty patch, got nil")
	}
	v := 1
	if err := wrapped.Validate(patchDelta{Value: &v}); err != nil {
		t.Errorf("Validate non-empty patch: %v", err)
	}
}

func TestEmptyPatchError_LogValue(t *testing.T) {
	v := codex.EmptyPatchError{}.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Errorf("Kind() = %v, want KindGroup", v.Kind())
	}
	if len(v.Group()) != 0 {
		t.Errorf("Group() = %v, want empty", v.Group())
	}
}

// ── BuildDottedPatch ─────────────────────────────────────────────────────────

func TestBuildDottedPatch_WrapsNestedSegments(t *testing.T) {
	got := codex.BuildDottedPatch("env.API_URL", "http://x")
	want := map[string]any{"env": map[string]any{"API_URL": "http://x"}}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", got)
	}
	env, ok := m["env"].(map[string]any)
	if !ok || env["API_URL"] != "http://x" {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestBuildDottedPatch_BareKeyUnwrapped(t *testing.T) {
	got := codex.BuildDottedPatch("", "whole-value")
	if got != "whole-value" {
		t.Errorf("got %v, want whole-value unwrapped", got)
	}
}

// ── ApplyDottedPatch ─────────────────────────────────────────────────────────

func TestApplyDottedPatch_DeepMergesAtEveryLevel(t *testing.T) {
	base := map[string]any{
		"factory-gw": map[string]any{
			"env":    map[string]any{"API_URL": "old", "TZ": "UTC"},
			"status": "running",
		},
	}
	patch := map[string]any{
		"factory-gw.env.API_URL": "new",
	}
	got := codex.ApplyDottedPatch(base, patch)
	gw := got["factory-gw"].(map[string]any)
	env := gw["env"].(map[string]any)
	if env["API_URL"] != "new" {
		t.Errorf("API_URL = %v, want new", env["API_URL"])
	}
	if env["TZ"] != "UTC" {
		t.Errorf("TZ = %v, want unchanged UTC", env["TZ"])
	}
	if gw["status"] != "running" {
		t.Errorf("status = %v, want unchanged running", gw["status"])
	}
	// base must not be mutated.
	baseGW := base["factory-gw"].(map[string]any)
	baseEnv := baseGW["env"].(map[string]any)
	if baseEnv["API_URL"] != "old" {
		t.Error("ApplyDottedPatch must not mutate base")
	}
}

func TestApplyDottedPatch_IntroducesNewTopLevelKey(t *testing.T) {
	base := map[string]any{}
	patch := map[string]any{
		"factory-extra": map[string]any{"status": "running"},
	}
	got := codex.ApplyDottedPatch(base, patch)
	extra, ok := got["factory-extra"].(map[string]any)
	if !ok || extra["status"] != "running" {
		t.Errorf("got %+v, want factory-extra introduced wholesale", got)
	}
}

// ── ApplyDottedPatchTo ───────────────────────────────────────────────────────

type dottedBase struct {
	Modules map[string]moduleValue
}

type moduleValue struct {
	Status string
	Env    map[string]string
}

var dottedBaseCodec = codex.Struct[dottedBase](
	codex.RequiredField("modules",
		codex.StringMap(codex.Struct[moduleValue](
			codex.RequiredField("status", codex.String(),
				func(m moduleValue) string { return m.Status },
				func(m *moduleValue, v string) { m.Status = v }),
			codex.OptionalField("env", codex.StringMap(codex.String()),
				func(m moduleValue) map[string]string { return m.Env },
				func(m *moduleValue, v map[string]string) { m.Env = v }),
		)),
		func(b dottedBase) map[string]moduleValue { return b.Modules },
		func(b *dottedBase, v map[string]moduleValue) { b.Modules = v },
	),
)

func TestApplyDottedPatchTo_RoundTrip(t *testing.T) {
	base := dottedBase{Modules: map[string]moduleValue{
		"gw": {Status: "running", Env: map[string]string{"API_URL": "old"}},
	}}
	patch := map[string]any{
		"modules.gw.env.API_URL": "new",
	}
	got, err := codex.ApplyDottedPatchTo(base, dottedBaseCodec, patch)
	if err != nil {
		t.Fatalf("ApplyDottedPatchTo: %v", err)
	}
	if got.Modules["gw"].Env["API_URL"] != "new" {
		t.Errorf("API_URL = %v, want new", got.Modules["gw"].Env["API_URL"])
	}
	if got.Modules["gw"].Status != "running" {
		t.Errorf("Status = %v, want unchanged running", got.Modules["gw"].Status)
	}
}

func TestApplyDottedPatchTo_MergedResultFailsDecodeValidation(t *testing.T) {
	base := dottedBase{Modules: map[string]moduleValue{
		"gw": {Status: "running"},
	}}
	patch := map[string]any{
		"modules.gw.status": 12345, // wrong type — should fail String() decode
	}
	_, err := codex.ApplyDottedPatchTo(base, dottedBaseCodec, patch)
	if err == nil {
		t.Error("expected decode validation error, got nil")
	}
}

// ── Examples ─────────────────────────────────────────────────────────────────
//
// DottedPatchMapCodec/DottedKeyCodec/KeyVarConstraint/DottedKeyError tests
// and examples moved to codex/dottedkey_test.go alongside their new
// template-based implementations (see codex/dottedkey.go).

func ExampleApplyPatch() {
	base := patchBase{Name: "widget", Value: 1}
	v := 42
	got, err := codex.ApplyPatch(base, patchBaseCodec, patchDelta{Value: &v}, patchDeltaCodec)
	if err != nil {
		panic(err)
	}
	fmt.Println(got.Name, got.Value)
}

func ExampleApplyDottedPatch() {
	base := map[string]any{
		"factory-gw": map[string]any{"status": "running"},
	}
	patch := map[string]any{
		"factory-gw.status": "stopped",
	}
	got := codex.ApplyDottedPatch(base, patch)
	_ = got
}
