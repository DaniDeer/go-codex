package forge_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/forge"
)

// --- Compose ----------------------------------------------------------------

func TestCompose_ConfigError_EmptyName(t *testing.T) {
	c := float64Codec(0, 1)
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c, identity)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)
	_, err := forge.Compose("", "1.0.0", f1, f2)
	assertConfigError(t, err, "forge.Compose", "name")
}

func TestCompose_ConfigError_EmptyVersion(t *testing.T) {
	c := float64Codec(0, 1)
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c, identity)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)
	_, err := forge.Compose("comp", "", f1, f2)
	assertConfigError(t, err, "forge.Compose", "version")
}

func TestCompose_Success(t *testing.T) {
	c := float64Codec(0, 1)
	double := func(v float64) (float64, error) {
		result := v * 2
		if result > 1 {
			result = 1
		}
		return result, nil
	}
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c, double)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)

	comp, err := forge.Compose("comp", "1.0.0", f1, f2)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	got, err := comp.Apply(0.3)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if abs(got-0.6) > 1e-9 {
		t.Errorf("got %v, want 0.6", got)
	}
}

func TestCompose_WithGovernanceOptions(t *testing.T) {
	c := float64Codec(0, 1)
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c, identity)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)

	comp, err := forge.Compose("comp", "1.0.0", f1, f2,
		forge.WithDescription("chained"),
		forge.WithAuthor("a"),
	)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if comp.Spec.Author != "a" {
		t.Errorf("Author: got %q", comp.Spec.Author)
	}
	if comp.Spec.Description != "chained" {
		t.Errorf("Description: got %q", comp.Spec.Description)
	}
}

// TestCompose_WithRefinement verifies that WithRefinement passed to Compose is wired
// into the resulting composed Function and returns a RefinementError when the
// constraint fails — the bug where it was silently dropped.
func TestCompose_WithRefinement(t *testing.T) {
	c := float64Codec(0, 1)
	// f1 doubles its input (capped at 1); f2 is identity
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c,
		func(v float64) (float64, error) {
			r := v * 2
			if r > 1 {
				r = 1
			}
			return r, nil
		},
	)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)

	comp, err := forge.Compose("comp", "1.0.0", f1, f2,
		forge.WithRefinement(func(v float64) error {
			if v >= 0.9 {
				return fmt.Errorf("input too close to max: %v", v)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	// Value 0.5 passes the refinement (< 0.9).
	if _, err := comp.Apply(0.5); err != nil {
		t.Fatalf("Apply(0.5): unexpected error: %v", err)
	}

	// Value 0.95 fails the refinement — should be RefinementError, not silently ignored.
	_, err = comp.Apply(0.95)
	var re forge.RefinementError
	if !errors.As(err, &re) {
		t.Fatalf("expected RefinementError from Compose WithRefinement, got %T: %v", err, err)
	}
	if re.Function != "comp" {
		t.Errorf("RefinementError.Function: got %q, want %q", re.Function, "comp")
	}
}

func TestCompose_PropagatesInputError(t *testing.T) {
	c := float64Codec(0, 1)
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c, identity)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)

	comp, _ := forge.Compose("comp", "1.0.0", f1, f2)
	_, err := comp.Apply(2.0) // input validation failure
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCompose_PropagatesApplyError(t *testing.T) {
	c := float64Codec(0, 1)
	f1, _ := forge.New("f1", "1.0.0", "x", c, "y", c,
		func(v float64) (float64, error) { return 0, fmt.Errorf("f1 failed") },
	)
	f2, _ := forge.New("f2", "1.0.0", "y", c, "z", c, identity)

	comp, _ := forge.Compose("comp", "1.0.0", f1, f2)
	_, err := comp.Apply(0.5)

	var ae forge.ApplyError
	if !errors.As(err, &ae) {
		t.Fatalf("expected ApplyError, got %T: %v", err, err)
	}
	if ae.Function != "f1" {
		t.Errorf("ApplyError.Function: got %q, want %q", ae.Function, "f1")
	}
}
