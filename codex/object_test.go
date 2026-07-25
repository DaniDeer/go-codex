package codex_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

type point struct {
	X int
	Y int
}

func pointCodec() codex.Codec[point] {
	return codex.Struct[point](
		codex.RequiredField("x",
			codex.Int(),
			func(p point) int { return p.X },
			func(p *point, v int) { p.X = v },
		),
		codex.OptionalField("y",
			codex.Int(),
			func(p point) int { return p.Y },
			func(p *point, v int) { p.Y = v },
		),
	)
}

func TestStruct_DecodeRequiredPresent(t *testing.T) {
	c := pointCodec()
	got, err := c.Decode(map[string]any{"x": 3, "y": 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 3 || got.Y != 4 {
		t.Errorf("got %+v, want {X:3 Y:4}", got)
	}
}

func TestStruct_DecodeRequiredMissing(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode(map[string]any{"y": 4})
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	if !strings.Contains(err.Error(), "x") {
		t.Errorf("error %q does not name the missing field", err.Error())
	}
}

func TestStruct_DecodeOptionalMissing(t *testing.T) {
	c := pointCodec()
	got, err := c.Decode(map[string]any{"x": 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 5 || got.Y != 0 {
		t.Errorf("got %+v, want {X:5 Y:0}", got)
	}
}

func TestStruct_DecodeFieldWrongType(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode(map[string]any{"x": "not-a-number", "y": 1})
	if err == nil {
		t.Fatal("expected error for wrong field type")
	}
	if !strings.Contains(err.Error(), "field x") {
		t.Errorf("error %q does not include field path", err.Error())
	}
}

func TestStruct_DecodeNonObject(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode("not-an-object")
	if err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestStruct_Encode(t *testing.T) {
	c := pointCodec()
	enc, err := c.Encode(point{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("encoded value is not a map: %T", enc)
	}
	if m["x"] != 1 || m["y"] != 2 {
		t.Errorf("encoded map = %v, want {x:1 y:2}", m)
	}
}

func TestStruct_Schema(t *testing.T) {
	c := pointCodec()
	s := c.Schema
	if s.Type != "object" {
		t.Errorf("schema type = %q, want %q", s.Type, "object")
	}
	if _, ok := s.Prop("x"); !ok {
		t.Error("schema missing property 'x'")
	}
	if _, ok := s.Prop("y"); !ok {
		t.Error("schema missing property 'y'")
	}
	// Only 'x' is required (Required: true); 'y' is optional.
	found := false
	for _, r := range s.Required {
		if r == "x" {
			found = true
		}
	}
	if !found {
		t.Errorf("required list %v does not include 'x'", s.Required)
	}
}

func TestStruct_RoundTrip(t *testing.T) {
	c := pointCodec()
	original := point{X: 10, Y: 20}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %+v, want %+v", got, original)
	}
}

func TestRequiredField_SetsRequired(t *testing.T) {
	f := codex.RequiredField("x", codex.Int(),
		func(p point) int { return p.X },
		func(p *point, v int) { p.X = v },
	)
	if !f.Required {
		t.Error("RequiredField: want Required=true")
	}
	if f.Name != "x" {
		t.Errorf("RequiredField: want Name=x, got %q", f.Name)
	}
}

func TestOptionalField_NotRequired(t *testing.T) {
	f := codex.OptionalField("y", codex.Int(),
		func(p point) int { return p.Y },
		func(p *point, v int) { p.Y = v },
	)
	if f.Required {
		t.Error("OptionalField: want Required=false")
	}
}

func TestRequiredField_RoundTrip(t *testing.T) {
	c := codex.Struct[point](
		codex.RequiredField("x", codex.Int(),
			func(p point) int { return p.X },
			func(p *point, v int) { p.X = v },
		),
		codex.OptionalField("y", codex.Int(),
			func(p point) int { return p.Y },
			func(p *point, v int) { p.Y = v },
		),
	)
	original := point{X: 3, Y: 7}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %+v, want %+v", got, original)
	}
}

func TestStruct_DecodeMultipleErrors(t *testing.T) {
	// codec with 2 required fields
	type pair struct{ A, B int }
	c := codex.Struct[pair](
		codex.RequiredField("a", codex.Int(),
			func(p pair) int { return p.A },
			func(p *pair, v int) { p.A = v },
		),
		codex.RequiredField("b", codex.Int(),
			func(p pair) int { return p.B },
			func(p *pair, v int) { p.B = v },
		),
	)

	// both required fields missing
	_, err := c.Decode(map[string]any{})
	if err == nil {
		t.Fatal("expected error for two missing required fields")
	}

	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	if len(ve) != 2 {
		t.Errorf("expected 2 validation errors, got %d: %v", len(ve), ve)
	}

	msg := err.Error()
	if !strings.Contains(msg, "field a") {
		t.Errorf("error %q does not mention field a", msg)
	}
	if !strings.Contains(msg, "field b") {
		t.Errorf("error %q does not mention field b", msg)
	}
}

// ── DefaultField ──────────────────────────────────────────────────────────────

func TestDefaultField_UsesDefaultWhenAbsent(t *testing.T) {
	type Config struct{ LogLevel string }
	c := codex.Struct[Config](
		codex.DefaultField("log_level", codex.String(), "info",
			func(cfg Config) string { return cfg.LogLevel },
			func(cfg *Config, v string) { cfg.LogLevel = v },
		),
	)

	got, err := c.Decode(map[string]any{})
	if err != nil {
		t.Fatalf("decode empty map: %v", err)
	}
	if got.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", got.LogLevel, "info")
	}
}

func TestDefaultField_PresentValueOverridesDefault(t *testing.T) {
	type Config struct{ LogLevel string }
	c := codex.Struct[Config](
		codex.DefaultField("log_level", codex.String(), "info",
			func(cfg Config) string { return cfg.LogLevel },
			func(cfg *Config, v string) { cfg.LogLevel = v },
		),
	)

	got, err := c.Decode(map[string]any{"log_level": "debug"})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", got.LogLevel, "debug")
	}
}

func TestDefaultField_ZeroValueDefault(t *testing.T) {
	type Config struct{ Timeout int }
	c := codex.Struct[Config](
		codex.DefaultField("timeout", codex.Int(), 0,
			func(cfg Config) int { return cfg.Timeout },
			func(cfg *Config, v int) { cfg.Timeout = v },
		),
	)

	got, err := c.Decode(map[string]any{})
	if err != nil {
		t.Fatalf("decode empty map: %v", err)
	}
	if got.Timeout != 0 {
		t.Errorf("Timeout = %d, want 0", got.Timeout)
	}
}

func TestDefaultField_SchemaContainsDefault(t *testing.T) {
	type Config struct{ LogLevel string }
	c := codex.Struct[Config](
		codex.DefaultField("log_level", codex.String(), "info",
			func(cfg Config) string { return cfg.LogLevel },
			func(cfg *Config, v string) { cfg.LogLevel = v },
		),
	)

	prop, ok := c.Schema.Prop("log_level")
	if !ok {
		t.Fatal("expected log_level in schema properties")
	}
	if prop.Default != "info" {
		t.Errorf("Schema.Default = %v, want %q", prop.Default, "info")
	}
}

func TestDefaultField_RequiredIsFalse(t *testing.T) {
	type Config struct{ X int }
	f := codex.DefaultField("x", codex.Int(), 42,
		func(c Config) int { return c.X },
		func(c *Config, v int) { c.X = v },
	)
	if f.Required {
		t.Errorf("DefaultField.Required should be false")
	}
}

func TestStruct_Encode_CollectsAllFieldErrors(t *testing.T) {
	type User struct {
		Name  string
		Email string
	}
	c := codex.Struct[User](
		codex.RequiredField("name",
			codex.String().Refine(codex.Constraint[string]{
				Name:    "non-empty",
				Check:   func(v string) bool { return v != "" },
				Message: func(v string) string { return "must not be empty" },
			}),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
		codex.RequiredField("email",
			codex.String().Refine(codex.Constraint[string]{
				Name:    "has-at",
				Check:   func(v string) bool { return strings.Contains(v, "@") },
				Message: func(v string) string { return "must contain @" },
			}),
			func(u User) string { return u.Email },
			func(u *User, v string) { u.Email = v },
		),
	)

	// Both fields fail — should collect all errors, not fail-fast.
	_, err := c.Encode(User{Name: "", Email: "not-an-email"})
	if err == nil {
		t.Fatal("expected ValidationErrors, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 2 {
		t.Errorf("expected 2 field errors, got %d: %v", len(ve), ve)
	}
}

func TestStruct_Encode_ValidValueSucceeds(t *testing.T) {
	type User struct{ Name string }
	c := codex.Struct[User](
		codex.RequiredField("name",
			codex.String().Refine(codex.Constraint[string]{
				Name:    "non-empty",
				Check:   func(v string) bool { return v != "" },
				Message: func(v string) string { return "must not be empty" },
			}),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
	)
	enc, err := c.Encode(User{Name: "Alice"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	obj, ok := enc.(map[string]any)
	if !ok || obj["name"] != "Alice" {
		t.Errorf("unexpected encode result: %v", enc)
	}
}

// --- Nested struct field required/optional (F itself is a codex.Struct[F]) ---

// address/addressCodec are shared by the nested-struct tests below — a small
// nested object whose "zip" field is itself Required, used to prove that a
// nested struct's OWN required/optional rules are enforced independently of
// whether the OUTER field holding it is Required or Optional.
type address struct {
	Zip string
}

func addressCodec() codex.Codec[address] {
	return codex.Struct[address](
		codex.RequiredField("zip", codex.String(),
			func(a address) string { return a.Zip },
			func(a *address, v string) { a.Zip = v },
		),
	)
}

type person struct {
	Name    string
	Billing address
}

// personCodecOptionalBilling declares "billing" as OptionalField — the
// nested struct itself may be absent from the input.
func personCodecOptionalBilling() codex.Codec[person] {
	return codex.Struct[person](
		codex.RequiredField("name", codex.String(),
			func(p person) string { return p.Name },
			func(p *person, v string) { p.Name = v },
		),
		codex.OptionalField("billing", addressCodec(),
			func(p person) address { return p.Billing },
			func(p *person, v address) { p.Billing = v },
		),
	)
}

// personCodecRequiredBilling declares "billing" as RequiredField — the
// nested struct itself must be present in the input.
func personCodecRequiredBilling() codex.Codec[person] {
	return codex.Struct[person](
		codex.RequiredField("name", codex.String(),
			func(p person) string { return p.Name },
			func(p *person, v string) { p.Name = v },
		),
		codex.RequiredField("billing", addressCodec(),
			func(p person) address { return p.Billing },
			func(p *person, v address) { p.Billing = v },
		),
	)
}

func TestStruct_NestedOptionalStruct_AbsentDecodesToZeroValue(t *testing.T) {
	c := personCodecOptionalBilling()
	got, err := c.Decode(map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "Alice" || got.Billing != (address{}) {
		t.Errorf("got %+v, want {Name:Alice Billing:{}}", got)
	}
}

func TestStruct_NestedOptionalStruct_PresentValidDecodes(t *testing.T) {
	c := personCodecOptionalBilling()
	got, err := c.Decode(map[string]any{
		"name":    "Alice",
		"billing": map[string]any{"zip": "12345"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Billing.Zip != "12345" {
		t.Errorf("got billing %+v, want Zip=12345", got.Billing)
	}
}

func TestStruct_NestedOptionalStruct_PresentButMissingInnerRequiredField(t *testing.T) {
	c := personCodecOptionalBilling()
	// "billing" is present (so its OWN codec runs), but its inner "zip" field
	// — required on addressCodec — is missing. The outer field being
	// Optional must NOT suppress the inner struct's own Required check.
	_, err := c.Decode(map[string]any{
		"name":    "Alice",
		"billing": map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing inner required field, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	// The outer error must name "billing"; the nested error (reachable via
	// Unwrap) must name "zip" — proving both levels of field attribution
	// survive the nesting.
	if len(ve) != 1 || ve[0].Field != "billing" {
		t.Fatalf("want outer error for field 'billing', got %+v", ve)
	}
	var inner codex.ValidationErrors
	if !errors.As(ve[0].Err, &inner) {
		t.Fatalf("want nested ValidationErrors from addressCodec, got %T: %v", ve[0].Err, ve[0].Err)
	}
	if len(inner) != 1 || inner[0].Field != "zip" {
		t.Fatalf("want inner error for field 'zip', got %+v", inner)
	}
}

func TestStruct_NestedRequiredStruct_AbsentReturnsError(t *testing.T) {
	c := personCodecRequiredBilling()
	_, err := c.Decode(map[string]any{"name": "Alice"})
	if err == nil {
		t.Fatal("expected error for missing required nested struct, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 1 || ve[0].Field != "billing" {
		t.Fatalf("want error for field 'billing', got %+v", ve)
	}
	if !errors.Is(ve[0].Err, codex.ErrMissingField) {
		t.Errorf("want ErrMissingField, got %v", ve[0].Err)
	}
}

func TestStruct_NestedOptionalStruct_SchemaShapeIndependentOfOuterAndInner(t *testing.T) {
	s := personCodecOptionalBilling().Schema
	// Outer schema: "billing" must NOT be in the outer Required list.
	for _, r := range s.Required {
		if r == "billing" {
			t.Errorf("outer Required list %v should not include 'billing' (declared Optional)", s.Required)
		}
	}
	// The nested schema is embedded verbatim as the "billing" property —
	// its OWN Required list (["zip"]) must be present and correct,
	// completely independent of the outer field's optionality.
	billingProp, ok := s.Prop("billing")
	if !ok {
		t.Fatal("outer schema missing 'billing' property")
	}
	if billingProp.Type != "object" {
		t.Errorf("billing property type = %q, want %q", billingProp.Type, "object")
	}
	foundZip := false
	for _, r := range billingProp.Required {
		if r == "zip" {
			foundZip = true
		}
	}
	if !foundZip {
		t.Errorf("nested 'billing' schema Required = %v, want to include 'zip'", billingProp.Required)
	}
}

func TestStruct_NestedRequiredStruct_SchemaIncludesOuterRequired(t *testing.T) {
	s := personCodecRequiredBilling().Schema
	found := false
	for _, r := range s.Required {
		if r == "billing" {
			found = true
		}
	}
	if !found {
		t.Errorf("outer Required list %v should include 'billing' (declared Required)", s.Required)
	}
}

func TestStruct_NestedOptionalStruct_EncodeAlwaysIncludesField(t *testing.T) {
	// codex has no "omit empty" semantics — encode always emits every field
	// regardless of Required/Optional, for scalar AND nested-struct fields
	// alike. A never-populated Optional nested struct still round-trips as
	// its zero value on the wire, not an absent key.
	c := personCodecOptionalBilling()
	enc, err := c.Encode(person{Name: "Alice"}) // Billing left at zero value
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	obj, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("encoded value is not a map: %T", enc)
	}
	billing, present := obj["billing"]
	if !present {
		t.Fatal("want 'billing' key present on encode even though it was never set (no omit-empty semantics)")
	}
	billingObj, ok := billing.(map[string]any)
	if !ok || billingObj["zip"] != "" {
		t.Errorf("want billing = {zip:\"\"}, got %v", billing)
	}
}

// --- Whole-struct (cross-field) Refine ---

// dateRange has two fields whose validity depends on EACH OTHER — not
// expressible as a per-field Constraint. codex.Struct[T] returns a plain
// Codec[T], so .Refine(...) already supports this: the constraint's Check
// receives the FULLY DECODED dateRange, after all per-field decode/validation
// succeeded.
type dateRange struct {
	Start int // day-of-year, for simplicity
	End   int
}

func dateRangeCodec() codex.Codec[dateRange] {
	return codex.Struct[dateRange](
		codex.RequiredField("start", codex.Int(),
			func(d dateRange) int { return d.Start },
			func(d *dateRange, v int) { d.Start = v },
		),
		codex.RequiredField("end", codex.Int(),
			func(d dateRange) int { return d.End },
			func(d *dateRange, v int) { d.End = v },
		),
	).Refine(codex.Constraint[dateRange]{
		Name:    "start-before-end",
		Check:   func(d dateRange) bool { return d.Start < d.End },
		Message: func(d dateRange) string { return fmt.Sprintf("start (%d) must be before end (%d)", d.Start, d.End) },
	})
}

func TestStruct_WholeStructRefine_ValidCrossFieldPasses(t *testing.T) {
	c := dateRangeCodec()
	got, err := c.Decode(map[string]any{"start": 1.0, "end": 10.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Start != 1 || got.End != 10 {
		t.Errorf("got %+v, want {Start:1 End:10}", got)
	}
}

func TestStruct_WholeStructRefine_InvalidCrossFieldFails(t *testing.T) {
	c := dateRangeCodec()
	// Both fields individually valid ints, but Start >= End violates the
	// cross-field constraint — only a whole-struct Refine can catch this;
	// no per-field Constraint has visibility into both fields at once.
	_, err := c.Decode(map[string]any{"start": 10.0, "end": 1.0})
	if err == nil {
		t.Fatal("expected error for start >= end, got nil")
	}
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
	if ce.Name != "start-before-end" {
		t.Errorf("want constraint name 'start-before-end', got %q", ce.Name)
	}
}

func TestStruct_WholeStructRefine_PerFieldErrorsStillRunFirst(t *testing.T) {
	c := dateRangeCodec()
	// "end" is missing entirely — the per-field Required check must fire
	// (and be reported) BEFORE the whole-struct cross-field constraint ever
	// gets a chance to run (Refine validates AFTER Decode succeeds).
	_, err := c.Decode(map[string]any{"start": 1.0})
	if err == nil {
		t.Fatal("expected error for missing 'end', got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors (per-field), got %T: %v", err, err)
	}
}

func TestStruct_WholeStructRefine_EncodeAlsoValidatesCrossField(t *testing.T) {
	c := dateRangeCodec()
	// Refine validates symmetrically on Encode too — an in-memory value
	// that violates the cross-field invariant fails to encode.
	_, err := c.Encode(dateRange{Start: 10, End: 1})
	if err == nil {
		t.Fatal("expected error encoding an invalid dateRange, got nil")
	}
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

// --- StrictStruct (reject unknown keys) ---

func strictPointCodec() codex.Codec[point] {
	return codex.StrictStruct[point](
		codex.RequiredField("x", codex.Int(),
			func(p point) int { return p.X },
			func(p *point, v int) { p.X = v },
		),
		codex.OptionalField("y", codex.Int(),
			func(p point) int { return p.Y },
			func(p *point, v int) { p.Y = v },
		),
	)
}

func TestStrictStruct_KnownFieldsOnly_Decodes(t *testing.T) {
	c := strictPointCodec()
	got, err := c.Decode(map[string]any{"x": 1.0, "y": 2.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 1 || got.Y != 2 {
		t.Errorf("got %+v, want {X:1 Y:2}", got)
	}
}

func TestStrictStruct_UnknownKey_ReturnsError(t *testing.T) {
	c := strictPointCodec()
	_, err := c.Decode(map[string]any{"x": 1.0, "z": 3.0})
	if err == nil {
		t.Fatal("expected error for unknown key 'z', got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 1 || ve[0].Field != "z" {
		t.Fatalf("want error for field 'z', got %+v", ve)
	}
	if !errors.Is(ve[0].Err, codex.ErrUnknownField) {
		t.Errorf("want ErrUnknownField, got %v", ve[0].Err)
	}
}

func TestStrictStruct_MultipleUnknownKeys_AllReported(t *testing.T) {
	c := strictPointCodec()
	_, err := c.Decode(map[string]any{"x": 1.0, "z": 3.0, "w": 4.0})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 2 {
		t.Fatalf("want 2 unknown-key errors, got %d: %+v", len(ve), ve)
	}
	// Deterministic (sorted) order: "w" before "z".
	if ve[0].Field != "w" || ve[1].Field != "z" {
		t.Errorf("want fields [w z] in sorted order, got [%s %s]", ve[0].Field, ve[1].Field)
	}
}

func TestStrictStruct_UnknownKeyAndMissingRequired_BothReported(t *testing.T) {
	c := strictPointCodec()
	// "x" (required) is missing AND "z" (unknown) is present — both errors
	// must be collected in one pass, not just one or the other.
	_, err := c.Decode(map[string]any{"z": 3.0})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 2 {
		t.Fatalf("want 2 errors (missing 'x' + unknown 'z'), got %d: %+v", len(ve), ve)
	}
	var sawMissingX, sawUnknownZ bool
	for _, e := range ve {
		if e.Field == "x" && errors.Is(e.Err, codex.ErrMissingField) {
			sawMissingX = true
		}
		if e.Field == "z" && errors.Is(e.Err, codex.ErrUnknownField) {
			sawUnknownZ = true
		}
	}
	if !sawMissingX || !sawUnknownZ {
		t.Errorf("want both missing-x and unknown-z errors, got %+v", ve)
	}
}

func TestStrictStruct_SchemaSetsAdditionalPropertiesFalse(t *testing.T) {
	s := strictPointCodec().Schema
	if s.AdditionalProperties == nil || *s.AdditionalProperties != false {
		t.Errorf("Schema.AdditionalProperties = %v, want pointer to false", s.AdditionalProperties)
	}
}

func TestStruct_SchemaLeavesAdditionalPropertiesUnset(t *testing.T) {
	// Plain Struct must NOT set AdditionalProperties — StrictStruct is
	// opt-in, not the default.
	s := pointCodec().Schema
	if s.AdditionalProperties != nil {
		t.Errorf("Schema.AdditionalProperties = %v, want nil (Struct is not strict by default)", s.AdditionalProperties)
	}
}

func TestStrictStruct_NotViralAcrossNesting(t *testing.T) {
	// A nested field declared with plain Struct (not StrictStruct) stays
	// non-strict even when the OUTER struct is StrictStruct — strictness is
	// opt-in per nesting level, not recursive.
	type wrapper struct {
		Inner point
	}
	inner := codex.Struct[point]( // plain Struct, not Strict
		codex.RequiredField("x", codex.Int(), func(p point) int { return p.X }, func(p *point, v int) { p.X = v }),
	)
	outer := codex.StrictStruct[wrapper](
		codex.RequiredField("inner", inner, func(w wrapper) point { return w.Inner }, func(w *wrapper, v point) { w.Inner = v }),
	)
	// Unknown key inside the NESTED (non-strict) struct is NOT rejected.
	_, err := outer.Decode(map[string]any{
		"inner": map[string]any{"x": 1.0, "extra": "ignored"},
	})
	if err != nil {
		t.Errorf("nested plain Struct should ignore unknown keys, got error: %v", err)
	}
	// But an unknown key on the OUTER strict struct IS rejected.
	_, err = outer.Decode(map[string]any{
		"inner":      map[string]any{"x": 1.0},
		"outerExtra": "rejected",
	})
	if err == nil {
		t.Fatal("expected error for unknown key on outer StrictStruct, got nil")
	}
}

func TestStrictStruct_EncodeUnchangedFromStruct(t *testing.T) {
	c := strictPointCodec()
	enc, err := c.Encode(point{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := enc.(map[string]any)
	if !ok || m["x"] != 1 || m["y"] != 2 {
		t.Errorf("encoded map = %v, want {x:1 y:2}", enc)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleRequiredField() {
	type User struct {
		Name  string
		Email string
	}

	// Define the codec once — encode, decode, validate, and schema from one value.
	userCodec := codex.Struct[User](
		codex.RequiredField("name", codex.String(),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
		codex.RequiredField("email", codex.String(),
			func(u User) string { return u.Email },
			func(u *User, v string) { u.Email = v },
		),
	)

	// Decode from intermediate representation (map[string]any).
	user, err := userCodec.Decode(map[string]any{"name": "Alice", "email": "alice@example.com"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%s <%s>\n", user.Name, user.Email)

	// Missing required field returns a structured error.
	_, err = userCodec.Decode(map[string]any{"name": "Bob"})
	fmt.Println(err != nil)
	// Output:
	// Alice <alice@example.com>
	// true
}
