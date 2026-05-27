package codex_test

import (
	"errors"
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

// ── RefineFunc ────────────────────────────────────────────────────────────────

func TestRefineFunc_PassesWhenFnReturnsNil(t *testing.T) {
	type Range struct{ Start, End int }
	c := codex.Struct[Range](
		codex.RequiredField[Range, int]("start", codex.Int(),
			func(r Range) int { return r.Start },
			func(r *Range, v int) { r.Start = v },
		),
		codex.RequiredField[Range, int]("end", codex.Int(),
			func(r Range) int { return r.End },
			func(r *Range, v int) { r.End = v },
		),
	).RefineFunc(func(r Range) error {
		if r.End <= r.Start {
			return errors.New("end must be greater than start")
		}
		return nil
	})

	got, err := c.Decode(map[string]any{"start": 1, "end": 5})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got.Start != 1 || got.End != 5 {
		t.Errorf("unexpected value: %+v", got)
	}
}

func TestRefineFunc_FailsWhenFnReturnsError(t *testing.T) {
	type Range struct{ Start, End int }
	c := codex.Struct[Range](
		codex.RequiredField[Range, int]("start", codex.Int(),
			func(r Range) int { return r.Start },
			func(r *Range, v int) { r.Start = v },
		),
		codex.RequiredField[Range, int]("end", codex.Int(),
			func(r Range) int { return r.End },
			func(r *Range, v int) { r.End = v },
		),
	).RefineFunc(func(r Range) error {
		if r.End <= r.Start {
			return errors.New("end must be greater than start")
		}
		return nil
	})

	_, err := c.Decode(map[string]any{"start": 10, "end": 3})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "end must be greater than start") {
		t.Errorf("error should contain fn message, got: %v", err)
	}
}

func TestRefineFunc_EncodeUnaffected(t *testing.T) {
	c := codex.Int().RefineFunc(func(v int) error {
		if v < 0 {
			return errors.New("must be positive")
		}
		return nil
	})
	enc, err := c.Encode(-1)
	if err != nil {
		t.Fatalf("encode should not apply RefineFunc, got: %v", err)
	}
	if enc != -1 {
		t.Errorf("Encode(-1) = %v, want -1", enc)
	}
}

func TestRefineFunc_SchemaUnchanged(t *testing.T) {
	c := codex.Int().RefineFunc(func(v int) error { return nil })
	if c.Schema.Type != "integer" {
		t.Errorf("RefineFunc should not change schema type, got %q", c.Schema.Type)
	}
}

func TestEq_MatchingValueSucceeds(t *testing.T) {
	c := codex.Eq(codex.String(), "hello")
	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if got != "hello" {
		t.Errorf("Decode = %q, want %q", got, "hello")
	}
}

func TestEq_NonMatchingValueFails(t *testing.T) {
	c := codex.Eq(codex.String(), "hello")
	_, err := c.Decode("world")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "eq(hello)") {
		t.Errorf("error should name the constraint, got: %v", err)
	}
}

func TestEq_IntCoercionFromFloat64(t *testing.T) {
	c := codex.Eq(codex.Int(), 42)
	got, err := c.Decode(float64(42)) // JSON numbers arrive as float64
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("Decode(float64(42)) = %v, want 42", got)
	}
	_, err = c.Decode(float64(99))
	if err == nil {
		t.Fatal("expected error for non-matching int, got nil")
	}
}

func TestEq_SchemaEnum(t *testing.T) {
	c := codex.Eq(codex.String(), "v2")
	if len(c.Schema.Enum) != 1 || c.Schema.Enum[0] != "v2" {
		t.Errorf("Eq schema Enum = %v, want [v2]", c.Schema.Enum)
	}
	if c.Schema.Type != "string" {
		t.Errorf("Eq schema Type = %q, want string", c.Schema.Type)
	}
}
