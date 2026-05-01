package codex_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

type point struct {
	X int
	Y int
}

func pointCodec() codex.Codec[point] {
	return codex.Struct[point](
		codex.Field[point, int]{
			Name:     "x",
			Codec:    codex.Int(),
			Get:      func(p point) int { return p.X },
			Set:      func(p *point, v int) { p.X = v },
			Required: true,
		},
		codex.Field[point, int]{
			Name:     "y",
			Codec:    codex.Int(),
			Get:      func(p point) int { return p.Y },
			Set:      func(p *point, v int) { p.Y = v },
			Required: false,
		},
	)
}

func TestStruct_DecodeRequiredPresent(t *testing.T) {
	c := pointCodec()
	got, err := c.Decode(map[string]any{"x": 3, "y": 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 3 || got.Y != 4 {
		t.Errorf("got %+v, want {X:3 Y:4}", got)
	}
}

func TestStruct_DecodeRequiredMissing(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode(map[string]any{"y": 4})
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	if !strings.Contains(err.Error(), "x") {
		t.Errorf("error %q does not name the missing field", err.Error())
	}
}

func TestStruct_DecodeOptionalMissing(t *testing.T) {
	c := pointCodec()
	got, err := c.Decode(map[string]any{"x": 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 5 || got.Y != 0 {
		t.Errorf("got %+v, want {X:5 Y:0}", got)
	}
}

func TestStruct_DecodeFieldWrongType(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode(map[string]any{"x": "not-a-number", "y": 1})
	if err == nil {
		t.Fatal("expected error for wrong field type")
	}
	if !strings.Contains(err.Error(), "field x") {
		t.Errorf("error %q does not include field path", err.Error())
	}
}

func TestStruct_DecodeNonObject(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode("not-an-object")
	if err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestStruct_Encode(t *testing.T) {
	c := pointCodec()
	enc, err := c.Encode(point{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("encoded value is not a map: %T", enc)
	}
	if m["x"] != 1 || m["y"] != 2 {
		t.Errorf("encoded map = %v, want {x:1 y:2}", m)
	}
}

func TestStruct_Schema(t *testing.T) {
	c := pointCodec()
	s := c.Schema
	if s.Type != "object" {
		t.Errorf("schema type = %q, want %q", s.Type, "object")
	}
	if _, ok := s.Prop("x"); !ok {
		t.Error("schema missing property 'x'")
	}
	if _, ok := s.Prop("y"); !ok {
		t.Error("schema missing property 'y'")
	}
	// Only 'x' is required (Required: true); 'y' is optional.
	found := false
	for _, r := range s.Required {
		if r == "x" {
			found = true
		}
	}
	if !found {
		t.Errorf("required list %v does not include 'x'", s.Required)
	}
}

func TestStruct_RoundTrip(t *testing.T) {
	c := pointCodec()
	original := point{X: 10, Y: 20}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %+v, want %+v", got, original)
	}
}

func TestRequiredField_SetsRequired(t *testing.T) {
	f := codex.RequiredField[point, int]("x", codex.Int(),
		func(p point) int { return p.X },
		func(p *point, v int) { p.X = v },
	)
	if !f.Required {
		t.Error("RequiredField: want Required=true")
	}
	if f.Name != "x" {
		t.Errorf("RequiredField: want Name=x, got %q", f.Name)
	}
}

func TestOptionalField_NotRequired(t *testing.T) {
	f := codex.OptionalField[point, int]("y", codex.Int(),
		func(p point) int { return p.Y },
		func(p *point, v int) { p.Y = v },
	)
	if f.Required {
		t.Error("OptionalField: want Required=false")
	}
}

func TestRequiredField_RoundTrip(t *testing.T) {
	c := codex.Struct[point](
		codex.RequiredField[point, int]("x", codex.Int(),
			func(p point) int { return p.X },
			func(p *point, v int) { p.X = v },
		),
		codex.OptionalField[point, int]("y", codex.Int(),
			func(p point) int { return p.Y },
			func(p *point, v int) { p.Y = v },
		),
	)
	original := point{X: 3, Y: 7}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %+v, want %+v", got, original)
	}
}

func TestStruct_DecodeMultipleErrors(t *testing.T) {
	// codec with 2 required fields
	type pair struct{ A, B int }
	c := codex.Struct[pair](
		codex.RequiredField[pair, int]("a", codex.Int(),
			func(p pair) int { return p.A },
			func(p *pair, v int) { p.A = v },
		),
		codex.RequiredField[pair, int]("b", codex.Int(),
			func(p pair) int { return p.B },
			func(p *pair, v int) { p.B = v },
		),
	)

	// both required fields missing
	_, err := c.Decode(map[string]any{})
	if err == nil {
		t.Fatal("expected error for two missing required fields")
	}

	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	if len(ve) != 2 {
		t.Errorf("expected 2 validation errors, got %d: %v", len(ve), ve)
	}

	msg := err.Error()
	if !strings.Contains(msg, "field a") {
		t.Errorf("error %q does not mention field a", msg)
	}
	if !strings.Contains(msg, "field b") {
		t.Errorf("error %q does not mention field b", msg)
	}
}
