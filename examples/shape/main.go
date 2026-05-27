package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Types ─────────────────────────────────────────────────────────────────────

type Rectangle struct {
	Width  int
	Height int
}

type Square struct {
	Side int
}

// Shape is a tagged union of Rectangle and Square.
type Shape struct {
	Rectangle *Rectangle
	Square    *Square
}

// Geometry is a common interface for shapes that can compute their area.
// Downcast is useful when variant types share such an interface.
type Geometry interface {
	Area() float64
}

func (r Rectangle) Area() float64 { return float64(r.Width * r.Height) }
func (s Square) Area() float64    { return float64(s.Side * s.Side) }

// ── Codecs ────────────────────────────────────────────────────────────────────

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

// Lift each concrete codec to Codec[Shape] via MapCodecSafe.
var rectShapeCodec = codex.MapCodecSafe(
	rectangleCodec,
	func(r Rectangle) Shape { return Shape{Rectangle: &r} },
	func(s Shape) (Rectangle, error) {
		if s.Rectangle == nil {
			return Rectangle{}, errors.New("shape is not a rectangle")
		}
		return *s.Rectangle, nil
	},
)

var squareShapeCodec = codex.MapCodecSafe(
	squareCodec,
	func(s Square) Shape { return Shape{Square: &s} },
	func(sh Shape) (Square, error) {
		if sh.Square == nil {
			return Square{}, errors.New("shape is not a square")
		}
		return *sh.Square, nil
	},
)

var shapeCodec = codex.TaggedUnion[Shape](
	"type",
	map[string]codex.Codec[Shape]{
		"rectangle": rectShapeCodec,
		"square":    squareShapeCodec,
	},
	func(s Shape) (string, error) {
		switch {
		case s.Rectangle != nil:
			return "rectangle", nil
		case s.Square != nil:
			return "square", nil
		default:
			return "", errors.New("empty shape: set Rectangle or Square")
		}
	},
)

// ── Geometry codecs (demonstrate Downcast) ────────────────────────────────────

// Lift each concrete codec to Codec[Geometry].
// Downcast handles the encode direction: assert the interface back to a concrete type.
var rectGeoCodec = codex.MapCodecSafe(
	rectangleCodec,
	func(r Rectangle) Geometry { return r },
	func(g Geometry) (Rectangle, error) { return codex.Downcast[Rectangle](g) },
)

var squareGeoCodec = codex.MapCodecSafe(
	squareCodec,
	func(s Square) Geometry { return s },
	func(g Geometry) (Square, error) { return codex.Downcast[Square](g) },
)

var geometryCodec = codex.TaggedUnion[Geometry](
	"type",
	map[string]codex.Codec[Geometry]{
		"rectangle": rectGeoCodec,
		"square":    squareGeoCodec,
	},
	func(g Geometry) (string, error) {
		switch g.(type) {
		case Rectangle:
			return "rectangle", nil
		case Square:
			return "square", nil
		default:
			return "", fmt.Errorf("unknown geometry: %T", g)
		}
	},
)

// ── Untagged union codec ───────────────────────────────────────────────────────
//
// UntaggedUnion[Geometry] decodes by shape alone — no "type" discriminator field.
// First variant whose codec succeeds wins. Encode requires an explicit which
// selector (0-based index) because there is no discriminator to inspect.

var untaggedGeoCodec = codex.UntaggedUnion[Geometry](
	func(g Geometry) int {
		switch g.(type) {
		case Rectangle:
			return 0
		case Square:
			return 1
		}
		return -1
	},
	codex.UntaggedVariant[Geometry]{Name: "rectangle", Codec: rectGeoCodec},
	codex.UntaggedVariant[Geometry]{Name: "square", Codec: squareGeoCodec},
)

// ── Either2 codec ─────────────────────────────────────────────────────────────
//
// Either2 produces Codec[Either[string, Rectangle]]: a value that is either a
// shape name reference (string) or a full Rectangle definition. Left branch
// (string) is tried first; if it fails, the right branch (Rectangle) is tried.
// If both fail, Decode returns EitherError listing both branch errors.

var shapeRefCodec = codex.Either2(codex.String(), rectangleCodec)

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// Decode a rectangle from raw map data.
	rectData := map[string]any{"type": "rectangle", "width": 10, "height": 5}
	shape, err := shapeCodec.Decode(rectData)
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("decoded rectangle: %+v\n", *shape.Rectangle)
	}

	// Decode a square from raw map data.
	squareData := map[string]any{"type": "square", "side": 7}
	shape, err = shapeCodec.Decode(squareData)
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("decoded square:    %+v\n", *shape.Square)
	}

	// Encode a Shape back to a map.
	encoded, err := shapeCodec.Encode(Shape{Square: &Square{Side: 5}})
	if err != nil {
		fmt.Println("encode error:", err)
	} else {
		fmt.Printf("encoded square:    %v\n", encoded)
	}

	// Validation error: negative width violates PositiveInt constraint.
	_, err = shapeCodec.Decode(map[string]any{"type": "rectangle", "width": -3, "height": 4})
	if err != nil {
		fmt.Println("validation error:", err)
	}

	// Print the schema as JSON.
	schemaJSON, _ := json.MarshalIndent(shapeCodec.Schema, "", "  ")
	fmt.Printf("\nschema:\n%s\n", schemaJSON)

	// ── Geometry codec (Downcast demo) ────────────────────────────────────────

	// Decode into Geometry interface — Downcast used internally in encode direction.
	geo, err := geometryCodec.Decode(map[string]any{"type": "rectangle", "width": 6, "height": 4})
	if err != nil {
		fmt.Println("geometry decode error:", err)
	} else {
		fmt.Printf("\ngeometry decoded: %T, area=%.0f\n", geo, geo.Area())
	}

	// Encode a Geometry value back — Downcast asserts Square from the interface.
	encodedGeo, err := geometryCodec.Encode(Square{Side: 3})
	if err != nil {
		fmt.Println("geometry encode error:", err)
	} else {
		fmt.Printf("geometry encoded:  %v\n", encodedGeo)
	}

	// Downcast failure: pass a Rectangle where Square is expected.
	_, err = codex.Downcast[Square](Rectangle{Width: 1, Height: 2})
	fmt.Println("downcast failure:  ", err)

	// ── Untagged union ────────────────────────────────────────────────────────
	fmt.Println("\n=== Untagged union (no type discriminator) ===")

	// {width, height} → Rectangle (first variant tried, succeeds).
	geo, err = untaggedGeoCodec.Decode(map[string]any{"width": float64(6), "height": float64(4)})
	if err != nil {
		fmt.Println("untagged decode error:", err)
	} else {
		fmt.Printf("untagged decoded: %T, area=%.0f\n", geo, geo.Area())
	}

	// {side} → Square (first variant fails, second succeeds).
	geo, err = untaggedGeoCodec.Decode(map[string]any{"side": float64(5)})
	if err != nil {
		fmt.Println("untagged decode error:", err)
	} else {
		fmt.Printf("untagged decoded: %T, area=%.0f\n", geo, geo.Area())
	}

	// Encode: which selector picks the variant by index.
	encodedGeo, err = untaggedGeoCodec.Encode(Rectangle{Width: 3, Height: 4})
	if err != nil {
		fmt.Println("untagged encode error:", err)
	} else {
		fmt.Printf("untagged encoded: %v\n", encodedGeo)
	}

	// All variants fail: EitherError lists branch errors.
	_, err = untaggedGeoCodec.Decode(map[string]any{"x": float64(1)})
	fmt.Println("untagged all-fail:", err)

	// ── Either2 ───────────────────────────────────────────────────────────────
	fmt.Println("\n=== Either2 (string name or Rectangle definition) ===")

	// Left branch (string) succeeds.
	ref, err := shapeRefCodec.Decode("unit-square")
	if err != nil {
		fmt.Println("either decode error:", err)
	} else {
		fmt.Printf("either decoded: Left=%q\n", *ref.Left)
	}

	// Right branch (Rectangle) succeeds when input is a map.
	ref, err = shapeRefCodec.Decode(map[string]any{"width": float64(4), "height": float64(3)})
	if err != nil {
		fmt.Println("either decode error:", err)
	} else {
		fmt.Printf("either decoded: Right=%+v\n", *ref.Right)
	}

	// Both branches fail: EitherError with both branch errors.
	_, err = shapeRefCodec.Decode(42)
	fmt.Println("either all-fail:", err)
}
