package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// ── StringOrInt ───────────────────────────────────────────────────────────────

func TestStringOrInt(t *testing.T) {
	c := codex.StringOrInt()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(42))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 || got.Left != nil {
		t.Errorf("decode number: want Right=42, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, int]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	n := 7
	encR, err := c.Encode(codex.Either[string, int]{Right: &n})
	if err != nil || encR != 7 {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "integer" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── StringOrInt32 ─────────────────────────────────────────────────────────────

func TestStringOrInt32(t *testing.T) {
	c := codex.StringOrInt32()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(42))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 || got.Left != nil {
		t.Errorf("decode number: want Right=42, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, int32]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	var n int32 = 7
	encR, err := c.Encode(codex.Either[string, int32]{Right: &n})
	if err != nil || encR != int32(7) {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "integer" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── StringOrInt64 ─────────────────────────────────────────────────────────────

func TestStringOrInt64(t *testing.T) {
	c := codex.StringOrInt64()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(42))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 || got.Left != nil {
		t.Errorf("decode number: want Right=42, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, int64]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	var n int64 = 7
	encR, err := c.Encode(codex.Either[string, int64]{Right: &n})
	if err != nil || encR != int64(7) {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "integer" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── StringOrUint ──────────────────────────────────────────────────────────────

func TestStringOrUint(t *testing.T) {
	c := codex.StringOrUint()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(42))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 || got.Left != nil {
		t.Errorf("decode number: want Right=42, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, uint]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	var n uint = 7
	encR, err := c.Encode(codex.Either[string, uint]{Right: &n})
	if err != nil || encR != uint(7) {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "integer" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── StringOrUint64 ────────────────────────────────────────────────────────────

func TestStringOrUint64(t *testing.T) {
	c := codex.StringOrUint64()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(42))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 || got.Left != nil {
		t.Errorf("decode number: want Right=42, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, uint64]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	var n uint64 = 7
	encR, err := c.Encode(codex.Either[string, uint64]{Right: &n})
	if err != nil || encR != uint64(7) {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "integer" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── StringOrFloat32 ───────────────────────────────────────────────────────────

func TestStringOrFloat32(t *testing.T) {
	c := codex.StringOrFloat32()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(4.5))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 4.5 || got.Left != nil {
		t.Errorf("decode number: want Right=4.5, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, float32]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	var n float32 = 4.5
	encR, err := c.Encode(codex.Either[string, float32]{Right: &n})
	// Float32().Encode widens to float64 (matching encoding/json's own
	// float64-everywhere convention) — compare against float64, not float32.
	if err != nil || encR != float64(4.5) {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "number" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── StringOrFloat64 ───────────────────────────────────────────────────────────

func TestStringOrFloat64(t *testing.T) {
	c := codex.StringOrFloat64()

	got, err := c.Decode("hello")
	if err != nil {
		t.Fatalf("decode string: unexpected error: %v", err)
	}
	if got.Left == nil || *got.Left != "hello" || got.Right != nil {
		t.Errorf("decode string: want Left=%q, got Left=%v Right=%v", "hello", got.Left, got.Right)
	}

	got, err = c.Decode(float64(4.5))
	if err != nil {
		t.Fatalf("decode number: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 4.5 || got.Left != nil {
		t.Errorf("decode number: want Right=4.5, got Left=%v Right=%v", got.Left, got.Right)
	}

	_, err = c.Decode(true)
	var eitherErr codex.EitherError
	if !errors.As(err, &eitherErr) {
		t.Fatalf("decode invalid: expected EitherError, got %T: %v", err, err)
	}

	s := "left"
	encL, err := c.Encode(codex.Either[string, float64]{Left: &s})
	if err != nil || encL != "left" {
		t.Errorf("encode Left: got %v, %v", encL, err)
	}
	var n float64 = 4.5
	encR, err := c.Encode(codex.Either[string, float64]{Right: &n})
	if err != nil || encR != 4.5 {
		t.Errorf("encode Right: got %v, %v", encR, err)
	}

	if len(c.Schema.OneOf) != 2 || c.Schema.OneOf[0].Type != "string" || c.Schema.OneOf[1].Type != "number" {
		t.Errorf("schema: unexpected shape %+v", c.Schema)
	}
}

// ── Format-agnostic native input shapes (YAML/TOML produce int/int64, not float64) ──

func TestStringOrInt64_DecodesNativeIntAndInt64(t *testing.T) {
	c := codex.StringOrInt64()

	// yaml.v3 decodes a YAML integer into `any` as a native Go int.
	got, err := c.Decode(int(42))
	if err != nil {
		t.Fatalf("decode native int: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 {
		t.Errorf("decode native int: want Right=42, got %+v", got)
	}

	// BurntSushi/toml decodes a TOML integer into `any` as a native int64.
	got, err = c.Decode(int64(42))
	if err != nil {
		t.Fatalf("decode native int64: unexpected error: %v", err)
	}
	if got.Right == nil || *got.Right != 42 {
		t.Errorf("decode native int64: want Right=42, got %+v", got)
	}
}
