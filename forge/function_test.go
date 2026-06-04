package forge_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/forge"
)

// --- Panic for New ----------------------------------------------------------

func TestNew_PanicEmptyName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty name")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "name") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	c := float64Codec(0, 1)
	forge.NewFunction[float64, float64]("", "1.0.0", c, c, identity)
}

func TestNew_PanicEmptyVersion(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty version")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "version") {
			t.Errorf("unexpected panic message: %v", r)
		}
	}()
	c := float64Codec(0, 1)
	forge.NewFunction[float64, float64]("fn", "", c, c, identity)
}

// --- Function.Apply (scalar In) ---------------------------------------------

func TestFunction_Apply_Success(t *testing.T) {
	c := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", c, c, identity)
	got, err := f.Apply(0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0.5 {
		t.Errorf("got %v, want 0.5", got)
	}
}

func TestFunction_Apply_InputError(t *testing.T) {
	c := float64Codec(0, 1)
	cX := c.WithTitle("x")
	cY := c.WithTitle("y")
	f := forge.NewFunction("fn", "1.0.0", cX, cY, identity)
	_, err := f.Apply(1.5) // > 1, fails input validation
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %T: %v", err, err)
	}
	if ie.Function != "fn" {
		t.Errorf("Function: got %q", ie.Function)
	}
	if ie.Input != "x" {
		t.Errorf("Input: got %q", ie.Input)
	}
	if ie.Err == nil {
		t.Error("Err must not be nil")
	}
}

func TestFunction_Apply_ApplyError(t *testing.T) {
	c := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", c, c,
		func(v float64) (float64, error) { return 0, fmt.Errorf("compute failed") },
	)
	_, err := f.Apply(0.5)
	var ae forge.ApplyError
	if !errors.As(err, &ae) {
		t.Fatalf("expected ApplyError, got %T: %v", err, err)
	}
	if ae.Function != "fn" {
		t.Errorf("Function: got %q", ae.Function)
	}
}

func TestFunction_Apply_OutputError(t *testing.T) {
	in := float64Codec(-10, 10)
	out := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", in, out,
		func(v float64) (float64, error) { return v, nil },
	)
	_, err := f.Apply(-5.0) // valid input but invalid output
	var oe forge.OutputError
	if !errors.As(err, &oe) {
		t.Fatalf("expected OutputError, got %T: %v", err, err)
	}
	if oe.Function != "fn" {
		t.Errorf("Function: got %q", oe.Function)
	}
}

// --- Function.Apply (struct In for multi-input) -----------------------------

// pair is a test input struct grouping two float64 values.
type pair struct{ A, B float64 }

func pairCodec(min, max float64) codex.Codec[pair] {
	c := float64Codec(min, max)
	return codex.Struct[pair](
		codex.RequiredField[pair, float64]("a", c,
			func(p pair) float64 { return p.A },
			func(p *pair, v float64) { p.A = v },
		),
		codex.RequiredField[pair, float64]("b", c,
			func(p pair) float64 { return p.B },
			func(p *pair, v float64) { p.B = v },
		),
	)
}

func TestFunction_Apply_StructIn_Success(t *testing.T) {
	pc := pairCodec(0, 1)
	out := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", pc, out,
		func(p pair) (float64, error) { return (p.A + p.B) / 2, nil },
	)
	got, err := f.Apply(pair{A: 0.4, B: 0.6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0.5 {
		t.Errorf("got %v, want 0.5", got)
	}
}

func TestFunction_Apply_StructIn_FieldValidationError(t *testing.T) {
	pc := pairCodec(0, 1)
	out := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", pc, out,
		func(p pair) (float64, error) { return (p.A + p.B) / 2, nil },
	)
	// B = 2.0 exceeds the [0,1] range — struct codec validation should fail
	_, err := f.Apply(pair{A: 0.5, B: 2.0})
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError for struct field violation, got %T: %v", err, err)
	}
	if ie.Err == nil {
		t.Error("Err must not be nil")
	}
}

// TestFunction_Apply_StructIn_InputError_FieldName verifies that when a struct input
// codec field fails validation, InputError.Input contains the failing field name rather
// than the function name (the bug where it fell back to f.Spec.Name).
func TestFunction_Apply_StructIn_InputError_FieldName(t *testing.T) {
	pc := pairCodec(0, 1)
	out := float64Codec(0, 1)
	f := forge.NewFunction("myFunc", "1.0.0", pc, out,
		func(p pair) (float64, error) { return (p.A + p.B) / 2, nil },
	)
	// Field "b" fails — InputError.Input should be "b", not "myFunc".
	_, err := f.Apply(pair{A: 0.5, B: 2.0})
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %T: %v", err, err)
	}
	if ie.Input != "b" {
		t.Errorf("InputError.Input = %q, want %q (the failing field name)", ie.Input, "b")
	}
}

// --- triple input struct -----------------------------------------------------

type triple struct{ A, B, C float64 }

func tripleCodec(min, max float64) codex.Codec[triple] {
	c := float64Codec(min, max)
	return codex.Struct[triple](
		codex.RequiredField[triple, float64]("a", c,
			func(t triple) float64 { return t.A },
			func(t *triple, v float64) { t.A = v },
		),
		codex.RequiredField[triple, float64]("b", c,
			func(t triple) float64 { return t.B },
			func(t *triple, v float64) { t.B = v },
		),
		codex.RequiredField[triple, float64]("c", c,
			func(t triple) float64 { return t.C },
			func(t *triple, v float64) { t.C = v },
		),
	)
}

func TestFunction_Apply_TripleIn_Success(t *testing.T) {
	tc := tripleCodec(0, 1)
	out := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", tc, out,
		func(v triple) (float64, error) { return v.A * v.B * v.C, nil },
	)
	got, err := f.Apply(triple{A: 0.8, B: 0.9, C: 0.98})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 0.8 * 0.9 * 0.98
	if abs(got-expected) > 1e-9 {
		t.Errorf("got %v, want %v", got, expected)
	}
}

func TestFunction_Apply_TripleIn_FieldValidationError(t *testing.T) {
	tc := tripleCodec(0, 1)
	out := float64Codec(0, 1)
	f := forge.NewFunction("fn", "1.0.0", tc, out,
		func(v triple) (float64, error) { return v.A * v.B * v.C, nil },
	)
	_, err := f.Apply(triple{A: 0.8, B: 0.9, C: 1.5}) // C out of range
	var ie forge.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("expected InputError, got %T: %v", err, err)
	}
}

// --- helpers ----------------------------------------------------------------

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
