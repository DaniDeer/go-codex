package codex_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

func TestCodecValidate_PassesForValidValue(t *testing.T) {
	c := codex.Int().Refine(validate.PositiveInt)
	if err := c.Validate(5); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCodecValidate_FailsForConstraintViolation(t *testing.T) {
	c := codex.Int().Refine(validate.PositiveInt)
	err := c.Validate(-1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("error should mention constraint name, got: %v", err)
	}
}

func TestCodecValidate_WorksWithCustomConstraint(t *testing.T) {
	even := codex.Constraint[int]{
		Name:    "even",
		Check:   func(v int) bool { return v%2 == 0 },
		Message: func(v int) string { return "must be even" },
	}
	c := codex.Int().Refine(even)

	if err := c.Validate(4); err != nil {
		t.Fatalf("expected no error for even value, got: %v", err)
	}
	if err := c.Validate(3); err == nil {
		t.Fatal("expected error for odd value, got nil")
	}
}

func TestCodecValidate_MultipleConstraints(t *testing.T) {
	c := codex.Int().Refine(validate.PositiveInt).Refine(validate.MaxInt(10))

	if err := c.Validate(5); err != nil {
		t.Fatalf("5 should pass both constraints, got: %v", err)
	}
	if err := c.Validate(-1); err == nil {
		t.Fatal("expected error for negative value")
	}
	if err := c.Validate(11); err == nil {
		t.Fatal("expected error for value > max")
	}
}

func TestCodecValidate_StructAllFields(t *testing.T) {
	type Point struct{ X, Y int }
	c := codex.Struct[Point](
		codex.Field[Point, int]{
			Name:     "x",
			Codec:    codex.Int().Refine(validate.MinInt(0)),
			Get:      func(p Point) int { return p.X },
			Set:      func(p *Point, v int) { p.X = v },
			Required: true,
		},
		codex.Field[Point, int]{
			Name:     "y",
			Codec:    codex.Int().Refine(validate.MinInt(0)),
			Get:      func(p Point) int { return p.Y },
			Set:      func(p *Point, v int) { p.Y = v },
			Required: true,
		},
	)

	if err := c.Validate(Point{X: 1, Y: 2}); err != nil {
		t.Fatalf("valid point should pass: %v", err)
	}
	if err := c.Validate(Point{X: -1, Y: 2}); err == nil {
		t.Fatal("expected error for negative X")
	}
}

func TestCodecValidate_NoConstraintAlwaysPasses(t *testing.T) {
	c := codex.String()
	if err := c.Validate("anything"); err != nil {
		t.Fatalf("unconstrained codec should always pass, got: %v", err)
	}
	if err := c.Validate(""); err != nil {
		t.Fatalf("empty string should pass unconstrained codec, got: %v", err)
	}
}
