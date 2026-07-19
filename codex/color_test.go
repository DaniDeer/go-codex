package codex_test

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// ── HexColor round-trip ──────────────────────────────────────────────────────

func TestHexColor_RoundTrip6Digit(t *testing.T) {
	c := codex.HexColor()
	got, err := c.Decode("#1e90ff")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := codex.Color{R: 0x1E, G: 0x90, B: 0xFF, A: 0xFF}
	if got != want {
		t.Fatalf("Decode: want %+v, got %+v", want, got)
	}

	enc, err := c.Encode(got)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc != "#1e90ff" {
		t.Errorf("Encode: want %q, got %q", "#1e90ff", enc)
	}
}

func TestHexColor_RoundTrip8DigitWithAlpha(t *testing.T) {
	c := codex.HexColor()
	got, err := c.Decode("#1e90ff80")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := codex.Color{R: 0x1E, G: 0x90, B: 0xFF, A: 0x80}
	if got != want {
		t.Fatalf("Decode: want %+v, got %+v", want, got)
	}

	enc, err := c.Encode(got)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc != "#1e90ff80" {
		t.Errorf("Encode: want %q, got %q", "#1e90ff80", enc)
	}
}

// ── HexColor shorthand decode ────────────────────────────────────────────────

func TestHexColor_Decode3DigitShorthand(t *testing.T) {
	c := codex.HexColor()
	got, err := c.Decode("#f00")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := codex.Color{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF}
	if got != want {
		t.Fatalf("Decode: want %+v, got %+v", want, got)
	}
}

func TestHexColor_Decode4DigitShorthandWithAlpha(t *testing.T) {
	c := codex.HexColor()
	got, err := c.Decode("#f008")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := codex.Color{R: 0xFF, G: 0x00, B: 0x00, A: 0x88}
	if got != want {
		t.Fatalf("Decode: want %+v, got %+v", want, got)
	}
}

func TestHexColor_DecodeCaseInsensitive(t *testing.T) {
	c := codex.HexColor()
	upper, err := c.Decode("#1E90FF")
	if err != nil {
		t.Fatalf("Decode upper: %v", err)
	}
	lower, err := c.Decode("#1e90ff")
	if err != nil {
		t.Fatalf("Decode lower: %v", err)
	}
	if upper != lower {
		t.Errorf("case-insensitive decode mismatch: %+v != %+v", upper, lower)
	}
}

func TestHexColor_EncodeIsCanonical(t *testing.T) {
	c := codex.HexColor()
	// Decoded from 3-digit shorthand — Encode must emit the 6-digit form,
	// never re-emit the shorthand.
	v, err := c.Decode("#f00")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	enc, err := c.Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc != "#ff0000" {
		t.Errorf("Encode: want canonical %q, got %q", "#ff0000", enc)
	}
}

// ── HexColor decode errors ───────────────────────────────────────────────────

func TestHexColor_DecodeMissingHash(t *testing.T) {
	c := codex.HexColor()
	_, err := c.Decode("1e90ff")
	if err == nil {
		t.Fatal("expected error for missing '#'")
	}
	var ice codex.InvalidColorError
	if !errors.As(err, &ice) {
		t.Fatalf("expected InvalidColorError, got %T: %v", err, err)
	}
	if ice.Value != "1e90ff" {
		t.Errorf("InvalidColorError.Value: want %q, got %q", "1e90ff", ice.Value)
	}
}

func TestHexColor_DecodeWrongLength(t *testing.T) {
	c := codex.HexColor()
	_, err := c.Decode("#12345")
	if err == nil {
		t.Fatal("expected error for wrong length")
	}
	var ice codex.InvalidColorError
	if !errors.As(err, &ice) {
		t.Fatalf("expected InvalidColorError, got %T: %v", err, err)
	}
}

func TestHexColor_DecodeNonHexCharacters(t *testing.T) {
	c := codex.HexColor()
	_, err := c.Decode("#gggggg")
	if err == nil {
		t.Fatal("expected error for non-hex characters")
	}
	var ice codex.InvalidColorError
	if !errors.As(err, &ice) {
		t.Fatalf("expected InvalidColorError, got %T: %v", err, err)
	}
}

func TestHexColor_DecodeWrongType(t *testing.T) {
	c := codex.HexColor()
	_, err := c.Decode(42)
	if err == nil {
		t.Fatal("expected error for non-string input")
	}
	var tme codex.TypeMismatchError
	if !errors.As(err, &tme) {
		t.Fatalf("expected TypeMismatchError, got %T: %v", err, err)
	}
	if tme.Expected != "string" {
		t.Errorf("TypeMismatchError.Expected: want %q, got %q", "string", tme.Expected)
	}
}

// ── InvalidColorError ─────────────────────────────────────────────────────────

func TestInvalidColorError_LogValue(t *testing.T) {
	err := codex.InvalidColorError{Value: "not-a-color"}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	if !keys["value"] {
		t.Errorf("LogValue missing attribute %q", "value")
	}
}

func TestInvalidColorError_ErrorsAs(t *testing.T) {
	c := codex.HexColor()
	_, err := c.Decode("not-a-color")
	var ice codex.InvalidColorError
	if !errors.As(err, &ice) {
		t.Fatalf("errors.As: expected InvalidColorError, got %T: %v", err, err)
	}
}

// ── Schema ────────────────────────────────────────────────────────────────────

func TestHexColor_Schema(t *testing.T) {
	c := codex.HexColor()
	if c.Schema.Type != "string" {
		t.Errorf("Schema.Type: want %q, got %q", "string", c.Schema.Type)
	}
	if c.Schema.Pattern == "" {
		t.Fatal("Schema.Pattern: want non-empty")
	}
	// The pattern documents all four accepted forms — sanity check against a
	// few representative strings without requiring an exact regex match here
	// (the real acceptance behavior is exercised by Decode above).
	re, err := regexp.Compile(c.Schema.Pattern)
	if err != nil {
		t.Fatalf("Schema.Pattern is not a valid regex: %v", err)
	}
	for _, s := range []string{"#f00", "#f008", "#1e90ff", "#1e90ff80"} {
		if !re.MatchString(s) {
			t.Errorf("Schema.Pattern does not match valid color %q", s)
		}
	}
	for _, s := range []string{"1e90ff", "#12345", "#gggggg"} {
		if re.MatchString(s) {
			t.Errorf("Schema.Pattern unexpectedly matches invalid color %q", s)
		}
	}
}

// ── Example ───────────────────────────────────────────────────────────────────

func ExampleHexColor() {
	c := codex.HexColor()

	v, err := c.Decode("#1E90FF")
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("R=%d G=%d B=%d A=%d\n", v.R, v.G, v.B, v.A)

	enc, _ := c.Encode(v)
	fmt.Println(enc)

	// 3-digit shorthand expands, and Encode always emits the canonical form.
	short, _ := c.Decode("#f00")
	enc2, _ := c.Encode(short)
	fmt.Println(enc2)

	// Output:
	// R=30 G=144 B=255 A=255
	// #1e90ff
	// #ff0000
}
