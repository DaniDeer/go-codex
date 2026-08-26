package codex_test

import (
	"errors"
	"fmt"
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

// ── Left/Right constructors, IsLeft/IsRight, Swap ───────────────────────────

func TestLeft_ConstructsLeftEither(t *testing.T) {
	e := codex.Left[string, int]("hello")
	if !e.IsLeft() || e.IsRight() {
		t.Errorf("Left(%q): IsLeft=%v IsRight=%v, want true/false", "hello", e.IsLeft(), e.IsRight())
	}
	if e.Left == nil || *e.Left != "hello" {
		t.Errorf("Left field = %v, want hello", e.Left)
	}
}

func TestRight_ConstructsRightEither(t *testing.T) {
	e := codex.Right[string, int](42)
	if !e.IsRight() || e.IsLeft() {
		t.Errorf("Right(42): IsLeft=%v IsRight=%v, want false/true", e.IsLeft(), e.IsRight())
	}
	if e.Right == nil || *e.Right != 42 {
		t.Errorf("Right field = %v, want 42", e.Right)
	}
}

func TestEither_IsLeft_IsRight(t *testing.T) {
	left := codex.Left[string, int]("x")
	if !left.IsLeft() || left.IsRight() {
		t.Error("left branch: IsLeft/IsRight mismatch")
	}
	right := codex.Right[string, int](1)
	if right.IsLeft() || !right.IsRight() {
		t.Error("right branch: IsLeft/IsRight mismatch")
	}
	var zero codex.Either[string, int]
	if zero.IsLeft() || zero.IsRight() {
		t.Error("zero-value Either should report false for both IsLeft and IsRight")
	}
}

func TestEither_Swap(t *testing.T) {
	e := codex.Left[string, int]("hi")
	swapped := e.Swap()
	if !swapped.IsRight() || swapped.Right == nil || *swapped.Right != "hi" {
		t.Errorf("Swap() = %+v, want Right(hi)", swapped)
	}
	roundTripped := swapped.Swap()
	if !roundTripped.IsLeft() || roundTripped.Left == nil || *roundTripped.Left != "hi" {
		t.Errorf("Swap().Swap() = %+v, want back to Left(hi)", roundTripped)
	}
}

// ── EitherFold / EitherMapLeft / EitherMapRight ─────────────────────────────

func TestEitherFold_CallsOnLeftForLeft(t *testing.T) {
	e := codex.Left[string, int]("x")
	got := codex.EitherFold(e,
		func(s string) string { return "left:" + s },
		func(n int) string { return "right" })
	if got != "left:x" {
		t.Errorf("EitherFold = %q, want left:x", got)
	}
}

func TestEitherFold_CallsOnRightForRight(t *testing.T) {
	e := codex.Right[string, int](7)
	got := codex.EitherFold(e,
		func(s string) string { return "left" },
		func(n int) string { return fmt.Sprintf("right:%d", n) })
	if got != "right:7" {
		t.Errorf("EitherFold = %q, want right:7", got)
	}
}

func TestEitherMapLeft_TransformsLeftPassesThroughRight(t *testing.T) {
	left := codex.Left[string, int]("x")
	mapped := codex.EitherMapLeft(left, func(s string) int { return len(s) })
	if !mapped.IsLeft() || mapped.Left == nil || *mapped.Left != 1 {
		t.Errorf("EitherMapLeft(Left) = %+v, want Left(1)", mapped)
	}

	right := codex.Right[string, int](5)
	untouched := codex.EitherMapLeft(right, func(s string) int {
		t.Error("fn should not be called for a Right value")
		return -1
	})
	if !untouched.IsRight() || untouched.Right == nil || *untouched.Right != 5 {
		t.Errorf("EitherMapLeft(Right) = %+v, want Right(5) unchanged", untouched)
	}
}

func TestEitherMapRight_TransformsRightPassesThroughLeft(t *testing.T) {
	right := codex.Right[string, int](5)
	mapped := codex.EitherMapRight(right, func(n int) string { return fmt.Sprintf("n=%d", n) })
	if !mapped.IsRight() || mapped.Right == nil || *mapped.Right != "n=5" {
		t.Errorf("EitherMapRight(Right) = %+v, want Right(n=5)", mapped)
	}

	left := codex.Left[string, int]("x")
	untouched := codex.EitherMapRight(left, func(n int) string {
		t.Error("fn should not be called for a Left value")
		return "unreachable"
	})
	if !untouched.IsLeft() || untouched.Left == nil || *untouched.Left != "x" {
		t.Errorf("EitherMapRight(Left) = %+v, want Left(x) unchanged", untouched)
	}
}

// ── EitherField ──────────────────────────────────────────────────────────

type eitherFieldDoc struct {
	Value codex.Either[string, int]
}

func eitherFieldDocCodec() codex.Codec[eitherFieldDoc] {
	return codex.Struct[eitherFieldDoc](
		codex.EitherField("value", codex.String(), codex.Int(),
			func(d eitherFieldDoc) codex.Either[string, int] { return d.Value },
			func(d *eitherFieldDoc, v codex.Either[string, int]) { d.Value = v }),
	)
}

func TestEitherField_DecodesAndEncodes(t *testing.T) {
	d, err := eitherFieldDocCodec().Decode(map[string]any{"value": "hello"})
	if err != nil {
		t.Fatalf("Decode(Left): unexpected error: %v", err)
	}
	if !d.Value.IsLeft() || *d.Value.Left != "hello" {
		t.Errorf("Value = %+v, want Left(hello)", d.Value)
	}

	d2, err := eitherFieldDocCodec().Decode(map[string]any{"value": int64(42)})
	if err != nil {
		t.Fatalf("Decode(Right): unexpected error: %v", err)
	}
	if !d2.Value.IsRight() || *d2.Value.Right != 42 {
		t.Errorf("Value = %+v, want Right(42)", d2.Value)
	}

	raw, err := eitherFieldDocCodec().Encode(eitherFieldDoc{Value: codex.Left[string, int]("x")})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if raw.(map[string]any)["value"] != "x" {
		t.Errorf(`obj["value"] = %v, want x`, raw.(map[string]any)["value"])
	}
}

func TestEitherField_RequiredField_MissingKeyErrors(t *testing.T) {
	if _, err := eitherFieldDocCodec().Decode(map[string]any{}); err == nil {
		t.Error("Decode with absent key should fail -- EitherField is always Required")
	}
}

func TestEitherField_EquivalentToRequiredFieldPlusEither2(t *testing.T) {
	get := func(d eitherFieldDoc) codex.Either[string, int] { return d.Value }
	set := func(d *eitherFieldDoc, v codex.Either[string, int]) { d.Value = v }

	viaEitherField := codex.Struct[eitherFieldDoc](
		codex.EitherField("value", codex.String(), codex.Int(), get, set),
	)
	viaComposition := codex.Struct[eitherFieldDoc](
		codex.RequiredField("value", codex.Either2(codex.String(), codex.Int()), get, set),
	)

	for _, raw := range []map[string]any{
		{"value": "hello"},
		{"value": int64(42)},
	} {
		d1, err1 := viaEitherField.Decode(raw)
		d2, err2 := viaComposition.Decode(raw)
		if (err1 == nil) != (err2 == nil) {
			t.Errorf("%+v: error mismatch: EitherField=%v, composition=%v", raw, err1, err2)
		}
		if d1.Value.IsLeft() != d2.Value.IsLeft() || d1.Value.IsRight() != d2.Value.IsRight() {
			t.Errorf("%+v: decode shape mismatch: EitherField=%+v, composition=%+v", raw, d1.Value, d2.Value)
		}
	}
}
