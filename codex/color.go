package codex

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/schema"
)

// Color is a straight (non-premultiplied) RGB(A) color value. Component
// values are 0-255; A defaults to 255 (fully opaque) when a hex string omits
// the alpha channel.
//
// Color is intentionally NOT image/color.RGBA — that stdlib type's R/G/B/A
// are alpha-premultiplied, a different semantic than hex notation's straight
// channels, and reusing it would silently produce wrong values for any
// partially-transparent color.
type Color struct {
	R, G, B, A uint8
}

// InvalidColorError is returned when [HexColor]'s Decode receives a string
// that is not valid CSS Color Level 4 hex notation.
type InvalidColorError struct {
	Value string // the raw string that failed to parse
}

func (e InvalidColorError) Error() string {
	return fmt.Sprintf("codex: invalid hex color %q (expected #RGB, #RRGGBB, #RGBA, or #RRGGBBAA)", e.Value)
}

// LogValue implements slog.LogValuer for structured logging.
func (e InvalidColorError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("value", e.Value),
	)
}

// hexColorPattern documents the accepted shape in generated OpenAPI/AsyncAPI
// specs. There is no standard JSON Schema "format" keyword for color, so
// Schema.Pattern is used instead of Schema.Format.
const hexColorPattern = `^#([0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`

// HexColor returns a Codec for [Color] using CSS Color Level 4 hex notation:
// "#RGB", "#RRGGBB", "#RGBA", or "#RRGGBBAA" (case-insensitive on decode; the
// leading "#" is required). Encode always emits a canonical lowercase form —
// "#rrggbb" when A is 255 (fully opaque), "#rrggbbaa" otherwise — never the
// 3/4-digit shorthand, regardless of the shorthand used on decode.
func HexColor() Codec[Color] {
	return Codec[Color]{
		Schema: schema.Schema{Type: "string", Pattern: hexColorPattern},
		Encode: func(c Color) (any, error) {
			if c.A == 0xFF {
				return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B), nil
			}
			return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A), nil
		},
		Decode: func(v any) (Color, error) {
			s, ok := v.(string)
			if !ok {
				return Color{}, TypeMismatchError{Expected: "string", Got: fmt.Sprintf("%T", v)}
			}
			return parseHexColor(s)
		},
	}
}

// parseHexColor accepts the four CSS Color Level 4 hex forms and expands
// shorthand (3/4-digit) notation by duplicating each nibble, matching the
// CSS spec's expansion rule (e.g. "f" -> "ff").
func parseHexColor(s string) (Color, error) {
	if len(s) < 2 || s[0] != '#' {
		return Color{}, InvalidColorError{Value: s}
	}
	hex := s[1:]
	if !isHexDigits(hex) {
		return Color{}, InvalidColorError{Value: s}
	}
	switch len(hex) {
	case 3:
		return Color{R: dupNibble(hex[0]), G: dupNibble(hex[1]), B: dupNibble(hex[2]), A: 0xFF}, nil
	case 4:
		return Color{R: dupNibble(hex[0]), G: dupNibble(hex[1]), B: dupNibble(hex[2]), A: dupNibble(hex[3])}, nil
	case 6:
		return Color{R: hexPair(hex[0:2]), G: hexPair(hex[2:4]), B: hexPair(hex[4:6]), A: 0xFF}, nil
	case 8:
		return Color{R: hexPair(hex[0:2]), G: hexPair(hex[2:4]), B: hexPair(hex[4:6]), A: hexPair(hex[6:8])}, nil
	default:
		return Color{}, InvalidColorError{Value: s}
	}
}

func isHexDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// dupNibble expands a single hex digit "f" into a full byte 0xFF, matching
// CSS shorthand notation's expansion rule.
func dupNibble(c byte) uint8 {
	v := hexNibble(c)
	return v<<4 | v
}

// hexPair parses two hex digits into a single byte.
func hexPair(s string) uint8 {
	return hexNibble(s[0])<<4 | hexNibble(s[1])
}

func hexNibble(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}
