package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestValidationError_Error(t *testing.T) {
	inner := errors.New("must be positive")
	e := codex.ValidationError{Field: "age", Err: inner}
	want := "field age: must be positive"
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}

func TestValidationError_Unwrap(t *testing.T) {
	inner := errors.New("too short")
	e := codex.ValidationError{Field: "name", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestValidationErrors_Error_single(t *testing.T) {
	ve := codex.ValidationErrors{
		{Field: "email", Err: errors.New("invalid email")},
	}
	want := "field email: invalid email"
	if ve.Error() != want {
		t.Errorf("got %q, want %q", ve.Error(), want)
	}
}

func TestValidationErrors_Error_multi(t *testing.T) {
	ve := codex.ValidationErrors{
		{Field: "name", Err: errors.New("required")},
		{Field: "email", Err: errors.New("invalid email")},
		{Field: "age", Err: errors.New("must be positive")},
	}
	got := ve.Error()
	// All three fields must appear, joined by "; "
	for _, want := range []string{"field name: required", "field email: invalid email", "field age: must be positive"} {
		if !contains(got, want) {
			t.Errorf("error %q missing segment %q", got, want)
		}
	}
}

func TestValidationErrors_ErrorsAs(t *testing.T) {
	inner := codex.ValidationErrors{
		{Field: "x", Err: errors.New("bad")},
	}
	// ValidationErrors is itself the error — errors.As must succeed.
	var ve codex.ValidationErrors
	if !errors.As(inner, &ve) {
		t.Fatal("errors.As should succeed for ValidationErrors")
	}
	if len(ve) != 1 || ve[0].Field != "x" {
		t.Errorf("extracted ValidationErrors unexpected: %v", ve)
	}
}

func TestValidationErrors_FromStructDecode(t *testing.T) {
	// Verify that Struct.Decode returns ValidationErrors when multiple fields fail.
	type req struct{ Name, Email string }
	c := codex.Struct[req](
		codex.RequiredField("name", codex.String(),
			func(r req) string { return r.Name },
			func(r *req, v string) { r.Name = v },
		),
		codex.RequiredField("email", codex.String(),
			func(r req) string { return r.Email },
			func(r *req, v string) { r.Email = v },
		),
	)

	// Missing both required fields.
	_, err := c.Decode(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}

	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 2 {
		t.Errorf("expected 2 field errors, got %d: %v", len(ve), ve)
	}
}

func TestValidationErrors_UnwrapSlice(t *testing.T) {
	inner1 := errors.New("too short")
	inner2 := errors.New("invalid email")
	ve := codex.ValidationErrors{
		{Field: "name", Err: inner1},
		{Field: "email", Err: inner2},
	}

	// Unwrap returns all individual errors.
	if !errors.Is(ve, inner1) {
		t.Error("errors.Is should find inner1 via Unwrap")
	}
	if !errors.Is(ve, inner2) {
		t.Error("errors.Is should find inner2 via Unwrap")
	}
}

func TestConstraintError_Error(t *testing.T) {
	e := codex.ConstraintError{Name: "minLen(3)", Message: "expected at least 3 characters"}
	want := "constraint failed (minLen(3)): expected at least 3 characters"
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}

func TestTypeMismatchError_Error(t *testing.T) {
	e := codex.TypeMismatchError{Expected: "object", Got: "int"}
	want := "expected object, got int"
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}

func TestElementError_Error(t *testing.T) {
	inner := errors.New("bad value")
	e := codex.ElementError{Index: 2, Err: inner}
	want := "element 2: bad value"
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner via Unwrap")
	}
}

func TestKeyError_Error(t *testing.T) {
	inner := errors.New("bad value")
	e := codex.KeyError{Key: "myKey", Err: inner}
	want := `key "myKey": bad value`
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner via Unwrap")
	}
}

func TestVariantError_Error_withCause(t *testing.T) {
	inner := errors.New("decode failed")
	e := codex.VariantError{Tag: "kind", Variant: "circle", Err: inner}
	want := `variant [kind="circle"]: decode failed`
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
	if !contains(e.Error(), "kind") {
		t.Errorf("error %q should mention tag name", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find inner via Unwrap")
	}
}

func TestUnknownVariantError_Error(t *testing.T) {
	e := codex.UnknownVariantError{Tag: "kind", Variant: "triangle"}
	want := `field kind: unknown variant "triangle"`
	if e.Error() != want {
		t.Errorf("got %q, want %q", e.Error(), want)
	}
}

func TestErrMissingField_Is(t *testing.T) {
	if !errors.Is(codex.ErrMissingField, codex.ErrMissingField) {
		t.Error("ErrMissingField should match itself via errors.Is")
	}
}

func TestConstraintError_FromStructDecode(t *testing.T) {
	// Verify that ConstraintError is returned inside ValidationError.Err after Refine fails.
	type req struct{ Age int }
	c := codex.Struct[req](
		codex.RequiredField("age",
			codex.Int().Refine(codex.Constraint[int]{
				Name:    "positive",
				Check:   func(v int) bool { return v > 0 },
				Message: func(v int) string { return "must be positive" },
			}),
			func(r req) int { return r.Age },
			func(r *req, v int) { r.Age = v },
		),
	)

	_, err := c.Decode(map[string]any{"age": -1})
	if err == nil {
		t.Fatal("expected error")
	}

	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}

	var ce codex.ConstraintError
	if !errors.As(ve[0].Err, &ce) {
		t.Fatalf("expected ConstraintError inside ValidationError.Err, got %T", ve[0].Err)
	}
	if ce.Name != "positive" {
		t.Errorf("constraint name: got %q, want %q", ce.Name, "positive")
	}
}

func TestPrimitive_TypeMismatchError(t *testing.T) {
	cases := []struct {
		name     string
		decode   func(any) error
		input    any
		expected string
	}{
		{"Int/string", func(v any) error { _, err := codex.Int().Decode(v); return err }, "x", "number"},
		{"Int64/string", func(v any) error { _, err := codex.Int64().Decode(v); return err }, "x", "number"},
		{"Float64/string", func(v any) error { _, err := codex.Float64().Decode(v); return err }, "x", "number"},
		{"String/int", func(v any) error { _, err := codex.String().Decode(v); return err }, 42, "string"},
		{"Bool/int", func(v any) error { _, err := codex.Bool().Decode(v); return err }, 1, "boolean"},
		{"Bytes/int", func(v any) error { _, err := codex.Bytes().Decode(v); return err }, 99, "string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decode(tc.input)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var tme codex.TypeMismatchError
			if !errors.As(err, &tme) {
				t.Fatalf("expected TypeMismatchError, got %T: %v", err, err)
			}
			if tme.Expected != tc.expected {
				t.Errorf("Expected=%q, want %q", tme.Expected, tc.expected)
			}
		})
	}
}

func TestPrimitive_ConstraintError_NonIntegralFloat(t *testing.T) {
	cases := []struct {
		name   string
		decode func(any) error
	}{
		{"Int", func(v any) error { _, err := codex.Int().Decode(v); return err }},
		{"Int64", func(v any) error { _, err := codex.Int64().Decode(v); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decode(3.14)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var ce codex.ConstraintError
			if !errors.As(err, &ce) {
				t.Fatalf("expected ConstraintError, got %T: %v", err, ce)
			}
			if ce.Name != "integer" {
				t.Errorf("constraint name: got %q, want %q", ce.Name, "integer")
			}
		})
	}
}

// contains is a helper for substring checks in tests.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
