package codex_test

import (
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Fixtures ─────────────────────────────────────────────────────────────

type getUserVars struct {
	ID string
}

var getUserVarsFields = []codex.FieldCodec[getUserVars]{
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
		func(v getUserVars) string { return v.ID },
		func(v *getUserVars, s string) { v.ID = s }),
}

// ── Interface satisfaction ───────────────────────────────────────────────

func TestTemplate_ImplementsGetterString(t *testing.T) {
	var _ codex.Getter[string] = codex.Template[getUserVars]{}
}

func TestTemplate_ImplementsHasCodec(t *testing.T) {
	var _ codex.HasCodec[getUserVars] = codex.Template[getUserVars]{}
}

// ── PathStyle: build + match ─────────────────────────────────────────────

func TestTemplate_PathStyle_EncodeBuildsConcretePath(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	got, err := tmpl.Codec().Encode(getUserVars{ID: "42"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if got != "/users/42" {
		t.Errorf("Encode = %v, want %q", got, "/users/42")
	}
}

func TestTemplate_PathStyle_DecodeExtractsVars(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	got, err := tmpl.Codec().Decode("/users/42")
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if got != (getUserVars{ID: "42"}) {
		t.Errorf("Decode = %+v, want {ID: 42}", got)
	}
}

func TestTemplate_PathStyle_RoundTrip(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	c := tmpl.Codec()
	enc, err := c.Encode(getUserVars{ID: "abc-123"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	dec, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec != (getUserVars{ID: "abc-123"}) {
		t.Errorf("round trip = %+v, want {ID: abc-123}", dec)
	}
}

func TestTemplate_PathStyle_DecodeMismatchReturnsTypedError(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	_, err := tmpl.Codec().Decode("/other/path")
	if err == nil {
		t.Fatal("Decode: want error for structural mismatch, got nil")
	}
	if _, ok := err.(codex.TemplateMismatchError); !ok {
		t.Errorf("error type = %T, want codex.TemplateMismatchError", err)
	}
}

func TestTemplate_PathStyle_EncodeInvalidFieldPropagatesCodecError(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	if _, err := tmpl.Codec().Encode(getUserVars{ID: ""}); err == nil {
		t.Error("Encode with empty ID: want error, got nil")
	}
}

func TestTemplate_DecodeNonStringReturnsTypeMismatchError(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	_, err := tmpl.Codec().Decode(42)
	if _, ok := err.(codex.TypeMismatchError); !ok {
		t.Errorf("error type = %T, want codex.TypeMismatchError", err)
	}
}

// ── Static (zero-variable) patterns ──────────────────────────────────────

func TestTemplate_StaticPattern_EncodeReturnsPatternUnchanged(t *testing.T) {
	tmpl := codex.NewTemplate[struct{}]("/health", codex.PathStyle)
	got, err := tmpl.Codec().Encode(struct{}{})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if got != "/health" {
		t.Errorf("Encode = %v, want %q", got, "/health")
	}
}

func TestTemplate_StaticPattern_DecodeRejectsMismatch(t *testing.T) {
	tmpl := codex.NewTemplate[struct{}]("/health", codex.PathStyle)
	if _, err := tmpl.Codec().Decode("/other"); err == nil {
		t.Error("Decode(\"/other\"): want mismatch error, got nil")
	}
	if _, err := tmpl.Codec().Decode("/health"); err != nil {
		t.Errorf("Decode(\"/health\"): unexpected error: %v", err)
	}
}

// ── Getter[string] / String() ─────────────────────────────────────────────

func TestTemplate_GetReturnsRawPattern(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	if tmpl.Get() != "/users/{id}" {
		t.Errorf("Get() = %q, want %q", tmpl.Get(), "/users/{id}")
	}
	if tmpl.String() != "/users/{id}" {
		t.Errorf("String() = %q, want %q", tmpl.String(), "/users/{id}")
	}
}

// ── Vars() ─────────────────────────────────────────────────────────────

func TestTemplate_Vars_ReturnsPlaceholderNames(t *testing.T) {
	type twoVars struct{ A, B string }
	fields := []codex.FieldCodec[twoVars]{
		codex.RequiredField("a", codex.String(), func(v twoVars) string { return v.A }, func(v *twoVars, s string) { v.A = s }),
		codex.RequiredField("b", codex.String(), func(v twoVars) string { return v.B }, func(v *twoVars, s string) { v.B = s }),
	}
	tmpl := codex.NewTemplate("readings/{a}/{b}.json", codex.PathStyle, fields...)
	vars := tmpl.Vars()
	if !vars["a"] || !vars["b"] || len(vars) != 2 {
		t.Errorf("Vars() = %+v, want {a: true, b: true}", vars)
	}
}

func TestTemplate_Fields_ReturnsDeclaredFieldsInOrder(t *testing.T) {
	fields := []codex.FieldCodec[getUserVars]{
		codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
			func(v getUserVars) string { return v.ID },
			func(v *getUserVars, s string) { v.ID = s }),
	}
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, fields...)
	got := tmpl.Fields()
	if len(got) != 1 {
		t.Fatalf("Fields() len = %d, want 1", len(got))
	}
	// Confirm the returned field is genuinely usable (round-trips via
	// DecodeVars/EncodeVars), not just non-nil.
	vars, err := codex.EncodeVars(getUserVars{ID: "42"}, got...)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["id"] != "42" {
		t.Errorf("EncodeVars via Fields() = %+v, want id=42", vars)
	}
}

func TestTemplate_Fields_EmptyForStaticPattern(t *testing.T) {
	tmpl := codex.NewTemplate[struct{}]("static/path", codex.PathStyle)
	if len(tmpl.Fields()) != 0 {
		t.Errorf("Fields() = %+v, want empty", tmpl.Fields())
	}
}

// ── Construction-time panics ──────────────────────────────────────────────

func TestNewTemplate_PanicsOnUndeclaredVar(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for undeclared {var}, got none")
		}
	}()
	codex.NewTemplate[struct{}]("/users/{id}", codex.PathStyle) // no fields declared for {id}
}

func TestNewTemplate_ConstructsGlobPatternWithoutPanicking(t *testing.T) {
	// A glob-enabled pattern (containing "*") must be CONSTRUCTIBLE (for
	// matching/listing) even though it can never be built — construction
	// never panics for wildcard/glob content; only Encode fails.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("NewTemplate with glob pattern panicked: %v", r)
		}
	}()
	codex.NewTemplate[struct{}]("readings/*.json", codex.GlobStyle)
}

// ── Wildcard/glob build rejection (encode-time, not construction-time) ────

func TestTemplate_GlobStyle_EncodeReturnsWildcardBuildError(t *testing.T) {
	tmpl := codex.NewTemplate[struct{}]("readings/*.json", codex.GlobStyle)
	_, err := tmpl.Codec().Encode(struct{}{})
	if err == nil {
		t.Fatal("Encode on glob-enabled template: want error, got nil")
	}
	if _, ok := err.(codex.TemplateWildcardBuildError); !ok {
		t.Errorf("error type = %T, want codex.TemplateWildcardBuildError", err)
	}
}

func TestTemplate_MQTTStyle_EncodeReturnsWildcardBuildErrorForWildcardPattern(t *testing.T) {
	tmpl := codex.NewTemplate[struct{}]("sensors/#", codex.MQTTStyle)
	_, err := tmpl.Codec().Encode(struct{}{})
	if err == nil {
		t.Fatal("Encode on wildcard MQTT template: want error, got nil")
	}
	if _, ok := err.(codex.TemplateWildcardBuildError); !ok {
		t.Errorf("error type = %T, want codex.TemplateWildcardBuildError", err)
	}
}

func TestTemplate_MQTTStyle_EncodeSucceedsForNonWildcardPattern(t *testing.T) {
	type deviceVars struct{ DeviceID string }
	fields := []codex.FieldCodec[deviceVars]{
		codex.RequiredField("device_id", codex.String().Refine(validate.NonEmptyString),
			func(v deviceVars) string { return v.DeviceID },
			func(v *deviceVars, s string) { v.DeviceID = s }),
	}
	tmpl := codex.NewTemplate("sensors/{device_id}/readings", codex.MQTTStyle, fields...)
	got, err := tmpl.Codec().Encode(deviceVars{DeviceID: "sensor-1"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if got != "sensors/sensor-1/readings" {
		t.Errorf("Encode = %v, want %q", got, "sensors/sensor-1/readings")
	}
}

// ── Build ────────────────────────────────────────────────────────────────

func TestTemplate_Build_Success(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	got, err := tmpl.Build(getUserVars{ID: "42"})
	if err != nil {
		t.Fatalf("Build: unexpected error: %v", err)
	}
	if got != "/users/42" {
		t.Errorf("Build = %q, want %q", got, "/users/42")
	}
}

func TestTemplate_Build_PropagatesEncodeError(t *testing.T) {
	tmpl := codex.NewTemplate[struct{}]("sensors/#", codex.MQTTStyle)
	_, err := tmpl.Build(struct{}{})
	if err == nil {
		t.Fatal("Build on wildcard MQTT template: want error, got nil")
	}
	if _, ok := err.(codex.TemplateWildcardBuildError); !ok {
		t.Errorf("error type = %T, want codex.TemplateWildcardBuildError", err)
	}
}

func TestTemplate_Build_PropagatesFieldValidationError(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	if _, err := tmpl.Build(getUserVars{ID: ""}); err == nil {
		t.Error("Build with invalid field: want error, got nil")
	}
}

// ── Schema ─────────────────────────────────────────────────────────────

func TestTemplate_Codec_SchemaIsString(t *testing.T) {
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, getUserVarsFields...)
	if tmpl.Codec().Schema.Type != "string" {
		t.Errorf("Schema.Type = %q, want %q", tmpl.Codec().Schema.Type, "string")
	}
}

// ── Example ─────────────────────────────────────────────────────────────

func ExampleTemplate() {
	type GetUserVars struct{ ID string }
	fields := []codex.FieldCodec[GetUserVars]{
		codex.RequiredField("id", codex.String(),
			func(v GetUserVars) string { return v.ID },
			func(v *GetUserVars, s string) { v.ID = s }),
	}
	tmpl := codex.NewTemplate("/users/{id}", codex.PathStyle, fields...)

	path, _ := tmpl.Codec().Encode(GetUserVars{ID: "42"})
	fmt.Println(path)
	// Output: /users/42
}
