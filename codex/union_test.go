package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// ── Test types ────────────────────────────────────────────────────────────────

type circle struct{ Radius int }
type rect struct{ Width, Height int }

// vehicle is a tagged union of circle and rect variants (for testing purposes).
type vehicle struct {
	Circle *circle
	Rect   *rect
}

func vehicleCodec() codex.Codec[vehicle] {
	circleC := codex.Struct[circle](
		codex.Field[circle, int]{
			Name: "radius", Codec: codex.Int(),
			Get: func(c circle) int { return c.Radius },
			Set: func(c *circle, v int) { c.Radius = v }, Required: true,
		},
	)
	rectC := codex.Struct[rect](
		codex.Field[rect, int]{
			Name: "width", Codec: codex.Int(),
			Get: func(r rect) int { return r.Width },
			Set: func(r *rect, v int) { r.Width = v }, Required: true,
		},
		codex.Field[rect, int]{
			Name: "height", Codec: codex.Int(),
			Get: func(r rect) int { return r.Height },
			Set: func(r *rect, v int) { r.Height = v }, Required: true,
		},
	)

	circleV := codex.MapCodecSafe(circleC,
		func(c circle) vehicle { return vehicle{Circle: &c} },
		func(v vehicle) (circle, error) {
			if v.Circle == nil {
				return circle{}, errors.New("not a circle")
			}
			return *v.Circle, nil
		},
	)
	rectV := codex.MapCodecSafe(rectC,
		func(r rect) vehicle { return vehicle{Rect: &r} },
		func(v vehicle) (rect, error) {
			if v.Rect == nil {
				return rect{}, errors.New("not a rect")
			}
			return *v.Rect, nil
		},
	)

	return codex.TaggedUnion[vehicle]("kind",
		map[string]codex.Codec[vehicle]{"circle": circleV, "rect": rectV},
		func(v vehicle) (string, error) {
			switch {
			case v.Circle != nil:
				return "circle", nil
			case v.Rect != nil:
				return "rect", nil
			default:
				return "", errors.New("empty vehicle")
			}
		},
	)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestTaggedUnion_DecodeKnownVariant(t *testing.T) {
	c := vehicleCodec()
	got, err := c.Decode(map[string]any{"kind": "circle", "radius": 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Circle == nil || got.Circle.Radius != 5 {
		t.Errorf("got %+v, want circle with radius 5", got)
	}
}

func TestTaggedUnion_DecodeUnknownTag(t *testing.T) {
	c := vehicleCodec()
	_, err := c.Decode(map[string]any{"kind": "triangle"})
	if err == nil {
		t.Fatal("expected error for unknown tag")
	}
	var uve codex.UnknownVariantError
	if !errors.As(err, &uve) {
		t.Fatalf("expected UnknownVariantError, got %T: %v", err, err)
	}
	if uve.Variant != "triangle" {
		t.Errorf("Variant: got %q, want %q", uve.Variant, "triangle")
	}
}

func TestTaggedUnion_DecodeMissingTag(t *testing.T) {
	c := vehicleCodec()
	_, err := c.Decode(map[string]any{"radius": 5})
	if err == nil {
		t.Fatal("expected error for missing tag field")
	}
}

func TestTaggedUnion_DecodeNonObject(t *testing.T) {
	c := vehicleCodec()
	_, err := c.Decode("not-an-object")
	if err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestTaggedUnion_Encode_InjectsTag(t *testing.T) {
	c := vehicleCodec()
	r := 7
	enc, err := c.Encode(vehicle{Circle: &circle{Radius: r}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("encoded value is not a map: %T", enc)
	}
	if m["kind"] != "circle" {
		t.Errorf("tag field = %v, want %q", m["kind"], "circle")
	}
}

func TestTaggedUnion_SelectVariantError(t *testing.T) {
	c := vehicleCodec()
	_, err := c.Encode(vehicle{}) // no variant set → selectVariant returns error
	if err == nil {
		t.Fatal("expected error for empty vehicle")
	}
}

func TestTaggedUnion_RoundTrip(t *testing.T) {
	c := vehicleCodec()
	original := vehicle{Rect: &rect{Width: 4, Height: 6}}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rect == nil || got.Rect.Width != 4 || got.Rect.Height != 6 {
		t.Errorf("round-trip = %+v, want rect {4,6}", got)
	}
}

// TestTaggedUnion_SchemaMutation_Regression ensures that reusing the same base
// codec in two different TaggedUnions does not corrupt either union's schema.
func TestTaggedUnion_SchemaMutation_Regression(t *testing.T) {
	base := codex.Struct[circle](
		codex.Field[circle, int]{
			Name: "radius", Codec: codex.Int(),
			Get: func(c circle) int { return c.Radius },
			Set: func(c *circle, v int) { c.Radius = v }, Required: true,
		},
	)

	liftFn := func(c circle) vehicle { return vehicle{Circle: &c} }
	fromFn := func(v vehicle) (circle, error) {
		if v.Circle == nil {
			return circle{}, errors.New("not a circle")
		}
		return *v.Circle, nil
	}

	v1 := codex.TaggedUnion[vehicle]("type",
		map[string]codex.Codec[vehicle]{
			"circle": codex.MapCodecSafe(base, liftFn, fromFn),
		},
		func(v vehicle) (string, error) { return "circle", nil },
	)
	v2 := codex.TaggedUnion[vehicle]("kind",
		map[string]codex.Codec[vehicle]{
			"circle": codex.MapCodecSafe(base, liftFn, fromFn),
		},
		func(v vehicle) (string, error) { return "circle", nil },
	)

	// v1 uses "type" tag; v2 uses "kind" tag.
	// If buildUnionSchema mutates shared schema, one union's tag leaks into the other.
	for _, s := range v1.Schema.OneOf {
		if _, ok := s.Prop("kind"); ok {
			t.Error("v1 schema was polluted with v2's 'kind' tag property")
		}
	}
	for _, s := range v2.Schema.OneOf {
		if _, ok := s.Prop("type"); ok {
			t.Error("v2 schema was polluted with v1's 'type' tag property")
		}
	}
}

// ── UntaggedUnion ─────────────────────────────────────────────────────────────

func TestUntaggedUnion_DecodesFirstMatchingVariant(t *testing.T) {
	type Circle struct{ Radius float64 }
	type Square struct{ Side float64 }
	type Shape struct {
		Circle *Circle
		Square *Square
	}

	circleCodec := codex.Struct[Shape](
		codex.RequiredField("radius", codex.Float64(),
			func(s Shape) float64 {
				if s.Circle != nil {
					return s.Circle.Radius
				}
				return 0
			},
			func(s *Shape, v float64) { s.Circle = &Circle{Radius: v} },
		),
	)
	squareCodec := codex.Struct[Shape](
		codex.RequiredField("side", codex.Float64(),
			func(s Shape) float64 {
				if s.Square != nil {
					return s.Square.Side
				}
				return 0
			},
			func(s *Shape, v float64) { s.Square = &Square{Side: v} },
		),
	)

	c := codex.UntaggedUnion(
		func(s Shape) int {
			if s.Circle != nil {
				return 0
			}
			return 1
		},
		codex.UntaggedVariant[Shape]{Name: "circle", Codec: circleCodec},
		codex.UntaggedVariant[Shape]{Name: "square", Codec: squareCodec},
	)

	got, err := c.Decode(map[string]any{"radius": 5.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Circle == nil || got.Circle.Radius != 5.0 {
		t.Errorf("expected Circle{Radius:5}, got %+v", got)
	}

	got, err = c.Decode(map[string]any{"side": 3.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Square == nil || got.Square.Side != 3.0 {
		t.Errorf("expected Square{Side:3}, got %+v", got)
	}
}

func TestUntaggedUnion_AllBranchesFailReturnsEitherError(t *testing.T) {
	c := codex.UntaggedUnion(
		func(s string) int { return 0 },
		codex.UntaggedVariant[string]{Name: "int-string", Codec: codex.String().Refine(codex.Constraint[string]{
			Name:    "numeric",
			Check:   func(v string) bool { return v == "1" },
			Message: func(v string) string { return "must be '1'" },
		})},
		codex.UntaggedVariant[string]{Name: "bool-string", Codec: codex.String().Refine(codex.Constraint[string]{
			Name:    "bool",
			Check:   func(v string) bool { return v == "true" },
			Message: func(v string) string { return "must be 'true'" },
		})},
	)

	_, err := c.Decode("neither")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("expected EitherError, got %T", err)
	}
	if len(eitherErr.Errors) != 2 {
		t.Errorf("expected 2 branch errors, got %d", len(eitherErr.Errors))
	}
}

func TestUntaggedUnion_EncodeUsesWhichSelector(t *testing.T) {
	c := codex.UntaggedUnion(
		func(n int) int {
			if n >= 0 {
				return 0
			}
			return 1
		},
		codex.UntaggedVariant[int]{Name: "non-negative", Codec: codex.Int()},
		codex.UntaggedVariant[int]{Name: "negative", Codec: codex.Int()},
	)

	enc, err := c.Encode(5)
	if err != nil {
		t.Fatal(err)
	}
	if enc != 5 {
		t.Errorf("Encode(5) = %v, want 5", enc)
	}
}

func TestUntaggedUnion_Schema(t *testing.T) {
	c := codex.UntaggedUnion(
		func(n int) int { return 0 },
		codex.UntaggedVariant[int]{Name: "a", Codec: codex.Int()},
		codex.UntaggedVariant[int]{Name: "b", Codec: codex.Int()},
	)
	if len(c.Schema.OneOf) != 2 {
		t.Errorf("expected 2 oneOf branches, got %d", len(c.Schema.OneOf))
	}
	if c.Schema.Discriminator != nil {
		t.Errorf("UntaggedUnion schema should have no discriminator")
	}
}
