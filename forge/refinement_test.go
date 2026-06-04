package forge_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
)

// errBadRefinement is a sentinel used to verify the correct error is propagated.
var errBadRefinement = errors.New("refinement failed")

// --- Struct input helpers ----------------------------------------------------

type availIn struct {
	PlannedTime float64
	Downtime    float64
}

func availInCodec() codex.Codec[availIn] {
	pos := float64Codec(0, 100)
	return codex.Struct[availIn](
		codex.RequiredField[availIn, float64]("plannedTime", pos,
			func(v availIn) float64 { return v.PlannedTime },
			func(v *availIn, f float64) { v.PlannedTime = f },
		),
		codex.RequiredField[availIn, float64]("downtime", pos,
			func(v availIn) float64 { return v.Downtime },
			func(v *availIn, f float64) { v.Downtime = f },
		),
	)
}

type oeeIn struct{ A, P, Q float64 }

func oeeInCodec() codex.Codec[oeeIn] {
	c := float64Codec(0, 1)
	return codex.Struct[oeeIn](
		codex.RequiredField[oeeIn, float64]("a", c,
			func(v oeeIn) float64 { return v.A },
			func(v *oeeIn, f float64) { v.A = f },
		),
		codex.RequiredField[oeeIn, float64]("p", c,
			func(v oeeIn) float64 { return v.P },
			func(v *oeeIn, f float64) { v.P = f },
		),
		codex.RequiredField[oeeIn, float64]("q", c,
			func(v oeeIn) float64 { return v.Q },
			func(v *oeeIn, f float64) { v.Q = f },
		),
	)
}

// TestWithRefinement_Passes verifies that valid inputs pass cross-input refinement
// and the compute function runs normally.
func TestWithRefinement_Passes(t *testing.T) {
	out := float64Codec(0, 1)
	called := false
	f := forge.NewFunction("fn", "1.0.0",
		"inputs", availInCodec(), "out", out,
		func(in availIn) (float64, error) {
			called = true
			return (in.PlannedTime - in.Downtime) / in.PlannedTime, nil
		},
		forge.WithRefinement(func(in availIn) error {
			if in.Downtime > in.PlannedTime {
				return fmt.Errorf("downtime exceeds plannedTime")
			}
			return nil
		}),
	)
	_, err := f.Apply(availIn{PlannedTime: 8, Downtime: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("compute function was not called")
	}
}

// TestWithRefinement_FailsBeforeCompute verifies that a failing refinement returns a
// RefinementError and the compute function is never called.
func TestWithRefinement_FailsBeforeCompute(t *testing.T) {
	out := float64Codec(0, 1)
	computeCalled := false
	f := forge.NewFunction("fn", "1.0.0",
		"inputs", availInCodec(), "out", out,
		func(in availIn) (float64, error) {
			computeCalled = true
			return 0, nil
		},
		forge.WithRefinement(func(in availIn) error {
			return errBadRefinement
		}),
	)
	_, err := f.Apply(availIn{PlannedTime: 5, Downtime: 8})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if computeCalled {
		t.Error("compute function must not be called after refinement failure")
	}
	var re forge.RefinementError
	if !errors.As(err, &re) {
		t.Fatalf("expected RefinementError, got %T: %v", err, err)
	}
	if re.Function != "fn" {
		t.Errorf("RefinementError.Function = %q, want %q", re.Function, "fn")
	}
	if !errors.Is(err, errBadRefinement) {
		t.Errorf("expected errors.Is to match errBadRefinement; chain: %v", err)
	}
}

// TestRefinementError_ErrorsAs verifies the RefinementError fields and Unwrap chain.
func TestRefinementError_ErrorsAs(t *testing.T) {
	re := forge.RefinementError{Function: "myFn", Err: errBadRefinement}
	if re.Function != "myFn" {
		t.Errorf("Function = %q", re.Function)
	}
	if !errors.Is(re, errBadRefinement) {
		t.Error("errors.Is should unwrap to errBadRefinement")
	}
	var target forge.RefinementError
	if !errors.As(re, &target) {
		t.Error("errors.As should match RefinementError")
	}
	if re.Error() == "" {
		t.Error("Error() must not be empty")
	}
}

// TestObserver_CapturesRefinementFailure verifies that the observer receives a failed
// RecordApply call when refinement rejects the inputs.
func TestObserver_CapturesRefinementFailure(t *testing.T) {
	out := float64Codec(0, 1)
	obs := &recordingObserver{}
	reg := forge.NewRegistry("test", "1.0.0").WithObserver(obs)

	f := forge.NewFunction("fn", "1.0.0",
		"inputs", availInCodec(), "out", out,
		func(in availIn) (float64, error) { return 0, nil },
		forge.WithRefinement(func(in availIn) error {
			return errBadRefinement
		}),
	)
	f.Register(reg)

	_, err := f.Apply(availIn{PlannedTime: 5, Downtime: 8})
	if err == nil {
		t.Fatal("expected RefinementError")
	}
	if len(obs.calls) != 1 {
		t.Fatalf("expected 1 observer call, got %d", len(obs.calls))
	}
	if obs.calls[0].success {
		t.Error("observer should record success=false on refinement failure")
	}
}

// TestWithRefinement_StructInput_ThreeFields verifies three-field struct input refinement.
func TestWithRefinement_StructInput_ThreeFields(t *testing.T) {
	out := float64Codec(0, 1)
	f := forge.NewFunction("oee", "1.0.0",
		"oeeIn", oeeInCodec(), "oee", out,
		func(in oeeIn) (float64, error) { return in.A * in.P * in.Q, nil },
		forge.WithRefinement(func(in oeeIn) error {
			if in.A+in.P+in.Q == 0 {
				return fmt.Errorf("all inputs are zero")
			}
			return nil
		}),
	)

	// passes
	result, err := f.Apply(oeeIn{A: 0.9, P: 0.8, Q: 0.95})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result <= 0 {
		t.Errorf("expected positive OEE, got %v", result)
	}

	// fails refinement
	_, err = f.Apply(oeeIn{A: 0, P: 0, Q: 0})
	var re forge.RefinementError
	if !errors.As(err, &re) {
		t.Fatalf("expected RefinementError, got %T: %v", err, err)
	}
	if re.Function != "oee" {
		t.Errorf("Function = %q, want %q", re.Function, "oee")
	}
}

// TestCompose_RefinementRunsInComposedFunction verifies that refinement on f1 is
// exercised when the composed function runs (since compose uses f1.Apply).
func TestCompose_RefinementRunsInComposedFunction(t *testing.T) {
	c := float64Codec(0, 10)
	refinementCalled := false

	f1 := forge.NewFunction("double", "1.0.0",
		"in", c, "out", c,
		func(v float64) (float64, error) { return v * 2, nil },
		forge.WithRefinement(func(v float64) error {
			refinementCalled = true
			if v > 8 {
				return fmt.Errorf("input too large: %v", v)
			}
			return nil
		}),
	)
	f2 := forge.NewFunction("addOne", "1.0.0",
		"in", c, "out", c,
		func(v float64) (float64, error) { return v + 1, nil },
	)

	composed := forge.Compose("doubleAddOne", "1.0.0", f1, f2)

	// passes: f1 refinement OK (input 4 ≤ 8), output = 4*2+1 = 9
	result, err := composed.Apply(4.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 9.0 {
		t.Errorf("want 9.0, got %v", result)
	}
	if !refinementCalled {
		t.Error("f1 refinement was not called in composed function")
	}

	// fails: f1 refinement rejects input 9 > 8
	_, err = composed.Apply(9.0)
	var re forge.RefinementError
	if !errors.As(err, &re) {
		t.Fatalf("expected RefinementError from composed fn, got %T: %v", err, err)
	}
	if re.Function != "double" {
		t.Errorf("RefinementError.Function = %q, want %q", re.Function, "double")
	}
}

// TestCodecRefine_CrossFieldConstraint verifies that cross-field validation placed on
// the input codec (via codex.Struct + Refine) surfaces as an InputError — the preferred
// approach when the constraint is a property of the domain type.
func TestCodecRefine_CrossFieldConstraint(t *testing.T) {
	pos := float64Codec(0, 100)
	constrainedInCodec := codex.Struct[availIn](
		codex.RequiredField[availIn, float64]("plannedTime", pos,
			func(v availIn) float64 { return v.PlannedTime },
			func(v *availIn, f float64) { v.PlannedTime = f },
		),
		codex.RequiredField[availIn, float64]("downtime", pos,
			func(v availIn) float64 { return v.Downtime },
			func(v *availIn, f float64) { v.Downtime = f },
		),
	).RefineFunc(func(in availIn) error {
		if in.Downtime > in.PlannedTime {
			return fmt.Errorf("downtime (%v) exceeds plannedTime (%v)", in.Downtime, in.PlannedTime)
		}
		return nil
	})

	out := float64Codec(0, 1)
	f := forge.NewFunction("availCalc", "1.0.0",
		"inputs", constrainedInCodec, "availability", out,
		func(in availIn) (float64, error) {
			return (in.PlannedTime - in.Downtime) / in.PlannedTime, nil
		},
	)

	// valid
	_, err := f.Apply(availIn{PlannedTime: 8, Downtime: 2})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// cross-field constraint fails — surfaces as InputError (codec validation)
	_, err = f.Apply(availIn{PlannedTime: 5, Downtime: 9})
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError from codec Refine constraint, got %T: %v", err, err)
	}
}
