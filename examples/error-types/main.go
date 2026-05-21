// Package error-types demonstrates every structured error type in go-codex.
//
// Each section triggers one specific error type, shows how to extract it via
// errors.As or errors.Is, accesses its typed fields, and logs it with slog so
// the structured LogValue() output is visible.
//
// Error types covered:
//   - codex.TypeMismatchError  — wrong Go type passed to a codec
//   - codex.ConstraintError    — Refine constraint check failed
//   - codex.ErrMissingField    — required struct field absent from input
//   - codex.ValidationError    — single field error from struct decode
//   - codex.ValidationErrors   — all field errors from struct decode
//   - codex.ElementError       — error at a specific slice index
//   - codex.KeyError           — error at a specific map key
//   - codex.UnknownVariantError — tagged-union discriminator has no matching codec
//   - codex.VariantError        — known variant's codec failed
//
// Run with: go run ./examples/error-types
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// logger writes structured text output to stdout for all sections.
var logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
	// Omit timestamp so output is deterministic.
	ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	},
}))

// ── Shape types for TaggedUnion sections ─────────────────────────────────────

type Rectangle struct {
	Width  int
	Height int
}

type Square struct {
	Side int
}

type Shape struct {
	Rectangle *Rectangle
	Square    *Square
}

var rectangleCodec = codex.Struct[Rectangle](
	codex.Field[Rectangle, int]{
		Name:     "width",
		Codec:    codex.Int().Refine(validate.PositiveInt),
		Get:      func(r Rectangle) int { return r.Width },
		Set:      func(r *Rectangle, v int) { r.Width = v },
		Required: true,
	},
	codex.Field[Rectangle, int]{
		Name:     "height",
		Codec:    codex.Int().Refine(validate.PositiveInt),
		Get:      func(r Rectangle) int { return r.Height },
		Set:      func(r *Rectangle, v int) { r.Height = v },
		Required: true,
	},
)

var squareCodec = codex.Struct[Square](
	codex.Field[Square, int]{
		Name:     "side",
		Codec:    codex.Int().Refine(validate.PositiveInt),
		Get:      func(s Square) int { return s.Side },
		Set:      func(s *Square, v int) { s.Side = v },
		Required: true,
	},
)

var shapeCodec = codex.TaggedUnion[Shape](
	"type",
	map[string]codex.Codec[Shape]{
		"rectangle": codex.MapCodecSafe(
			rectangleCodec,
			func(r Rectangle) Shape { return Shape{Rectangle: &r} },
			func(s Shape) (Rectangle, error) {
				if s.Rectangle == nil {
					return Rectangle{}, errors.New("shape is not a rectangle")
				}
				return *s.Rectangle, nil
			},
		),
		"square": codex.MapCodecSafe(
			squareCodec,
			func(s Square) Shape { return Shape{Square: &s} },
			func(sh Shape) (Square, error) {
				if sh.Square == nil {
					return Square{}, errors.New("shape is not a square")
				}
				return *sh.Square, nil
			},
		),
	},
	func(s Shape) (string, error) {
		switch {
		case s.Rectangle != nil:
			return "rectangle", nil
		case s.Square != nil:
			return "square", nil
		default:
			return "", errors.New("empty shape")
		}
	},
)

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// ── Section 1: TypeMismatchError ─────────────────────────────────────────
	//
	// Returned when a codec receives a value of an unexpected Go type.
	// All primitive codecs (Int, String, Bool, Bytes) and container codecs
	// (Struct, SliceOf, StringMap, TaggedUnion) return this when the input
	// is not the required type.

	fmt.Println("=== 1. TypeMismatchError ===")
	fmt.Println()

	_, err := codex.Int().Decode("thirty")
	var tme codex.TypeMismatchError
	if errors.As(err, &tme) {
		fmt.Printf("  expected=%q  got=%q\n", tme.Expected, tme.Got)
		logger.Error("type mismatch", slog.Any("error", tme))
	}
	fmt.Println()

	// ── Section 2: ConstraintError ───────────────────────────────────────────
	//
	// Returned directly from a Refine constraint when the check function
	// returns false. Name is the constraint identifier; Message describes the
	// specific failure for this value.

	fmt.Println("=== 2. ConstraintError ===")
	fmt.Println()

	_, err = codex.Int().Refine(validate.PositiveInt).Decode(-5)
	var ce codex.ConstraintError
	if errors.As(err, &ce) {
		fmt.Printf("  constraint=%q  message=%q\n", ce.Name, ce.Message)
		logger.Error("constraint failed", slog.Any("error", ce))
	}
	fmt.Println()

	// ── Section 3: ErrMissingField ───────────────────────────────────────────
	//
	// Sentinel error returned when a required struct field is absent from the
	// decoded map. The ValidationError wrapping it carries the field name;
	// errors.Is(fieldErr.Err, codex.ErrMissingField) identifies the cause.

	fmt.Println("=== 3. ErrMissingField ===")
	fmt.Println()

	type Req struct{ Name string }
	reqCodec := codex.Struct[Req](
		codex.RequiredField[Req, string]("name", codex.String(),
			func(r Req) string { return r.Name },
			func(r *Req, v string) { r.Name = v },
		),
	)

	_, err = reqCodec.Decode(map[string]any{})
	var ve codex.ValidationErrors
	if errors.As(err, &ve) {
		for _, fieldErr := range ve {
			if errors.Is(fieldErr.Err, codex.ErrMissingField) {
				fmt.Printf("  field=%q  missing=true\n", fieldErr.Field)
				logger.Error("missing required field", slog.Any("error", fieldErr))
			}
		}
	}
	fmt.Println()

	// ── Section 4: ValidationErrors + ValidationError ────────────────────────
	//
	// Struct decode collects all field failures into ValidationErrors before
	// returning. Each ValidationError carries the field name and its underlying
	// error (ConstraintError, ErrMissingField, TypeMismatchError, …).
	// Unwrap() returns []error so errors.Is / errors.As traverse the whole list.

	fmt.Println("=== 4. ValidationErrors + ValidationError ===")
	fmt.Println()

	type CreateUser struct {
		Name  string
		Email string
		Age   int
	}
	userCodec := codex.Struct[CreateUser](
		codex.RequiredField[CreateUser, string]("name",
			codex.String().Refine(validate.NonEmptyString),
			func(r CreateUser) string { return r.Name },
			func(r *CreateUser, v string) { r.Name = v },
		),
		codex.RequiredField[CreateUser, string]("email",
			codex.String().Refine(validate.Email),
			func(r CreateUser) string { return r.Email },
			func(r *CreateUser, v string) { r.Email = v },
		),
		codex.RequiredField[CreateUser, int]("age",
			codex.Int().Refine(validate.PositiveInt),
			func(r CreateUser) int { return r.Age },
			func(r *CreateUser, v int) { r.Age = v },
		),
	)

	_, err = userCodec.Decode(map[string]any{"name": "", "email": "bad", "age": -1})
	if errors.As(err, &ve) {
		fmt.Printf("  %d field(s) failed:\n", len(ve))
		for _, fieldErr := range ve {
			var constraint codex.ConstraintError
			if errors.As(fieldErr.Err, &constraint) {
				fmt.Printf("    field=%q  constraint=%q  message=%q\n",
					fieldErr.Field, constraint.Name, constraint.Message)
			}
		}
		logger.Error("validation failed", slog.Any("errors", ve))
	}
	fmt.Println()

	// ── Section 5: ElementError ───────────────────────────────────────────────
	//
	// Returned by SliceOf when an element fails to decode. Index is the
	// zero-based position; Err is the underlying cause (here TypeMismatchError).

	fmt.Println("=== 5. ElementError ===")
	fmt.Println()

	sliceCodec := codex.SliceOf(codex.Int())
	_, err = sliceCodec.Decode([]any{10, "bad", 20})
	var ee codex.ElementError
	if errors.As(err, &ee) {
		fmt.Printf("  index=%d  cause=%v\n", ee.Index, ee.Err)
		logger.Error("slice element failed", slog.Any("error", ee))
	}
	fmt.Println()

	// ── Section 6: KeyError ───────────────────────────────────────────────────
	//
	// Returned by StringMap when a value fails to decode. Key is the map key;
	// Err is the underlying cause (here TypeMismatchError).

	fmt.Println("=== 6. KeyError ===")
	fmt.Println()

	mapCodec := codex.StringMap(codex.Int())
	_, err = mapCodec.Decode(map[string]any{"a": 5, "b": "not-an-int"})
	var ke codex.KeyError
	if errors.As(err, &ke) {
		fmt.Printf("  key=%q  cause=%v\n", ke.Key, ke.Err)
		logger.Error("map key failed", slog.Any("error", ke))
	}
	fmt.Println()

	// ── Section 7: UnknownVariantError ───────────────────────────────────────
	//
	// Returned by TaggedUnion when the discriminator field value does not match
	// any registered variant key. Tag is the discriminator field name; Variant
	// is the unrecognised value from the input. No Unwrap — this is terminal.

	fmt.Println("=== 7. UnknownVariantError ===")
	fmt.Println()

	_, err = shapeCodec.Decode(map[string]any{"type": "triangle", "sides": 3})
	var uve codex.UnknownVariantError
	if errors.As(err, &uve) {
		fmt.Printf("  tag=%q  variant=%q\n", uve.Tag, uve.Variant)
		logger.Error("unknown variant", slog.Any("error", uve))
	}
	fmt.Println()

	// ── Section 8: VariantError ───────────────────────────────────────────────
	//
	// Returned by TaggedUnion when a known variant is matched but its codec
	// fails. Tag and Variant identify which discriminator matched; Err is the
	// underlying failure (here ValidationErrors from the rectangle codec).
	// Unwrap() → Err, so errors.As can traverse into the inner error.

	fmt.Println("=== 8. VariantError ===")
	fmt.Println()

	_, err = shapeCodec.Decode(map[string]any{"type": "rectangle", "width": "oops", "height": 4})
	var varErr codex.VariantError
	if errors.As(err, &varErr) {
		fmt.Printf("  tag=%q  variant=%q  cause=%v\n", varErr.Tag, varErr.Variant, varErr.Err)
		logger.Error("variant decode failed", slog.Any("error", varErr))

		// The cause is ValidationErrors — errors.As traverses through VariantError.Unwrap.
		var innerVE codex.ValidationErrors
		if errors.As(varErr.Err, &innerVE) {
			fmt.Printf("  inner field errors: %d\n", len(innerVE))
			for _, fieldErr := range innerVE {
				fmt.Printf("    field=%q  err=%v\n", fieldErr.Field, fieldErr.Err)
			}
		}
	}
	fmt.Println()
}
