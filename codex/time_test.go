package codex_test

import (
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/codex"
)

// ── Time ─────────────────────────────────────────────────────────────────────

func TestTime_RoundTrip(t *testing.T) {
	c := codex.Time()
	original := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !got.Equal(original) {
		t.Errorf("want %v, got %v", original, got)
	}
}

func TestTime_NormalizesToUTC(t *testing.T) {
	c := codex.Time()
	loc := time.FixedZone("EST", -5*3600)
	v := time.Date(2024, 1, 1, 10, 0, 0, 0, loc) // 10:00 EST = 15:00 UTC
	enc, err := c.Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s, ok := enc.(string)
	if !ok {
		t.Fatalf("expected string, got %T", enc)
	}
	if want := "2024-01-01T15:00:00Z"; s != want {
		t.Errorf("want %q, got %q", want, s)
	}
}

func TestTime_DecodeInvalidString(t *testing.T) {
	c := codex.Time()
	if _, err := c.Decode("not-a-date"); err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

func TestTime_DecodeWrongType(t *testing.T) {
	c := codex.Time()
	if _, err := c.Decode(42); err == nil {
		t.Fatal("expected error for non-string input")
	}
}

func TestTime_Schema(t *testing.T) {
	c := codex.Time()
	if c.Schema.Type != "string" {
		t.Errorf("want type=string, got %q", c.Schema.Type)
	}
	if c.Schema.Format != "date-time" {
		t.Errorf("want format=date-time, got %q", c.Schema.Format)
	}
}

// ── Date ─────────────────────────────────────────────────────────────────────

func TestDate_RoundTrip(t *testing.T) {
	c := codex.Date()
	original := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := "2024-12-31"
	if enc.(string) != want {
		t.Errorf("encoded: want %q, got %q", want, enc)
	}
	if got.Year() != 2024 || got.Month() != 12 || got.Day() != 31 {
		t.Errorf("decoded date wrong: %v", got)
	}
}

func TestDate_StripsTime(t *testing.T) {
	c := codex.Date()
	v := time.Date(2024, 3, 1, 14, 59, 0, 0, time.UTC)
	enc, _ := c.Encode(v)
	if enc.(string) != "2024-03-01" {
		t.Errorf("want 2024-03-01, got %q", enc)
	}
}

func TestDate_DecodeInvalid(t *testing.T) {
	c := codex.Date()
	if _, err := c.Decode("not-a-date"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDate_Schema(t *testing.T) {
	c := codex.Date()
	if c.Schema.Format != "date" {
		t.Errorf("want format=date, got %q", c.Schema.Format)
	}
}
