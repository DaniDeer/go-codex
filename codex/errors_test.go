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
		codex.RequiredField[req, string]("name", codex.String(),
			func(r req) string { return r.Name },
			func(r *req, v string) { r.Name = v },
		),
		codex.RequiredField[req, string]("email", codex.String(),
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
