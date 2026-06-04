package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// ── Either2 ───────────────────────────────────────────────────────────────────

func TestEither2_DecodesLeftBranch(t *testing.T) {
	c := codex.Either2(codex.String(), codex.Int())

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" {
		t.Errorf("expected Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}
	if got.Right != nil {
		t.Errorf("Right should be nil when Left is set")
	}
}

func TestEither2_DecodesRightBranchWhenLeftFails(t *testing.T) {
	// Use a struct+int so the struct codec fails on a plain int.
	c2 := codex.Either2(
		codex.Struct[struct{ X int }](
			codex.RequiredField("x", codex.Int(),
				func(s struct{ X int }) int { return s.X },
				func(s *struct{ X int }, v int) { s.X = v },
			),
		),
		codex.Int(),
	)

	got, err := c2.Decode(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 {
		t.Errorf("expected Right=42, got Left=%v Right=%v", got.Left, got.Right)
	}
	if got.Left != nil {
		t.Errorf("Left should be nil when Right is set")
	}
}

func TestEither2_ReturnsEitherErrorWhenBothFail(t *testing.T) {
	c := codex.Either2(
		codex.Struct[struct{ X int }](
			codex.RequiredField("x", codex.Int(),
				func(s struct{ X int }) int { return s.X },
				func(s *struct{ X int }, v int) { s.X = v },
			),
		),
		codex.Struct[struct{ Y string }](
			codex.RequiredField("y", codex.String(),
				func(s struct{ Y string }) string { return s.Y },
				func(s *struct{ Y string }, v string) { s.Y = v },
			),
		),
	)

	_, err := c.Decode("not an object")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("expected EitherError, got %T: %v", err, err)
	}
	if len(eitherErr.Errors) != 2 {
		t.Errorf("expected 2 branch errors, got %d", len(eitherErr.Errors))
	}
}

func TestEither2_EncodeLeft(t *testing.T) {
	c := codex.Either2(codex.String(), codex.Int())
	s := "hello"
	got, err := c.Encode(codex.Either[string, int]{Left: &s})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("Encode Left: got %v, want %q", got, "hello")
	}
}

func TestEither2_EncodeRight(t *testing.T) {
	c := codex.Either2(codex.String(), codex.Int())
	n := 42
	got, err := c.Encode(codex.Either[string, int]{Right: &n})
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("Encode Right: got %v, want 42", got)
	}
}

func TestEither2_Schema(t *testing.T) {
	c := codex.Either2(codex.String(), codex.Int())
	if len(c.Schema.OneOf) != 2 {
		t.Errorf("schema OneOf: expected 2 branches, got %d", len(c.Schema.OneOf))
	}
	if c.Schema.OneOf[0].Type != "string" {
		t.Errorf("first branch schema type = %q, want string", c.Schema.OneOf[0].Type)
	}
	if c.Schema.OneOf[1].Type != "integer" {
		t.Errorf("second branch schema type = %q, want integer", c.Schema.OneOf[1].Type)
	}
}

// ── EitherError ───────────────────────────────────────────────────────────────

func TestEitherError_Message(t *testing.T) {
	e := codex.EitherError{Errors: []error{
		errors.New("expected string"),
		errors.New("expected number"),
	}}
	msg := e.Error()
	if msg == "" {
		t.Fatal("EitherError.Error() should not be empty")
	}
	t.Log(msg)
}

func TestEitherError_Unwrap(t *testing.T) {
	inner1 := errors.New("branch 1 error")
	inner2 := errors.New("branch 2 error")
	e := codex.EitherError{Errors: []error{inner1, inner2}}
	if !errors.Is(e, inner1) {
		t.Error("errors.Is should find inner1")
	}
	if !errors.Is(e, inner2) {
		t.Error("errors.Is should find inner2")
	}
}
