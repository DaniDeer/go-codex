package codex_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestRefine_ConstraintPasses(t *testing.T) {
	positive := codex.Constraint[int]{
		Name:    "positive",
		Check:   func(v int) bool { return v > 0 },
		Message: func(v int) string { return "not positive" },
	}
	c := codex.Int().Refine(positive)
	got, err := c.Decode(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5 {
		t.Errorf("got %d, want 5", got)
	}
}

func TestRefine_ConstraintFails(t *testing.T) {
	positive := codex.Constraint[int]{
		Name:    "positive",
		Check:   func(v int) bool { return v > 0 },
		Message: func(v int) string { return "not positive" },
	}
	c := codex.Int().Refine(positive)
	_, err := c.Decode(-3)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("error %q does not mention constraint name", err.Error())
	}
	if !strings.Contains(err.Error(), "not positive") {
		t.Errorf("error %q does not mention constraint message", err.Error())
	}
}

func TestRefine_MultipleConstraints_FirstFails(t *testing.T) {
	checked := 0
	first := codex.Constraint[int]{
		Name:    "first",
		Check:   func(v int) bool { checked++; return false },
		Message: func(v int) string { return "first failed" },
	}
	second := codex.Constraint[int]{
		Name:    "second",
		Check:   func(v int) bool { checked++; return true },
		Message: func(v int) string { return "second failed" },
	}
	c := codex.Refine(codex.Int(), first, second)
	_, err := c.Decode(1)
	if err == nil {
		t.Fatal("expected error")
	}
	if checked != 1 {
		t.Errorf("expected second constraint not checked, but checked count = %d", checked)
	}
}

func TestRefine_MultipleConstraints_AllPass(t *testing.T) {
	positive := codex.Constraint[int]{
		Name:    "positive",
		Check:   func(v int) bool { return v > 0 },
		Message: func(v int) string { return "not positive" },
	}
	small := codex.Constraint[int]{
		Name:    "small",
		Check:   func(v int) bool { return v < 100 },
		Message: func(v int) string { return "too large" },
	}
	c := codex.Refine(codex.Int(), positive, small)
	got, err := c.Decode(50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 50 {
		t.Errorf("got %d, want 50", got)
	}
}

func TestRefine_SchemaPreserved(t *testing.T) {
	original := codex.Int()
	refined := original.Refine(codex.Constraint[int]{
		Name:    "x",
		Check:   func(v int) bool { return true },
		Message: func(v int) string { return "" },
	})
	if refined.Schema.Type != original.Schema.Type {
		t.Errorf("schema type changed after Refine: got %q, want %q", refined.Schema.Type, original.Schema.Type)
	}
}

func TestRefine_EncodeUnaffected(t *testing.T) {
	c := codex.Int().Refine(codex.Constraint[int]{
		Name:    "negative-only",
		Check:   func(v int) bool { return v < 0 },
		Message: func(v int) string { return "must be negative" },
	})
	// Encode should succeed even for a value that would fail the constraint.
	enc, err := c.Encode(42)
	if err != nil {
		t.Fatalf("Encode should not apply constraints, got error: %v", err)
	}
	if enc != 42 {
		t.Errorf("Encode(42) = %v, want 42", enc)
	}
}
