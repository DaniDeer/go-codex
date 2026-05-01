package codex_test

import (
	"errors"
	"strings"
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
	if !strings.Contains(err.Error(), "triangle") {
		t.Errorf("error %q does not mention unknown variant", err.Error())
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
