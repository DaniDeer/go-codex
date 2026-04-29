package codex_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// ── Int ──────────────────────────────────────────────────────────────────────

func TestInt_Decode(t *testing.T) {
	c := codex.Int()
	cases := []struct {
		name    string
		input   any
		want    int
		wantErr bool
	}{
		{"from int", 42, 42, false},
		{"from float64 integral", float64(7), 7, false},
		{"from float64 non-integral", 3.14, 0, true},
		{"from string", "42", 0, true},
		{"zero", 0, 0, false},
		{"negative", -5, -5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Decode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Decode(%v) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("Decode(%v) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestInt_Encode(t *testing.T) {
	c := codex.Int()
	got, err := c.Encode(99)
	if err != nil {
		t.Fatal(err)
	}
	if got != 99 {
		t.Errorf("Encode(99) = %v, want 99", got)
	}
}

func TestInt_Schema(t *testing.T) {
	if codex.Int().Schema.Type != "integer" {
		t.Errorf("Int schema type = %q, want %q", codex.Int().Schema.Type, "integer")
	}
}

// ── Int64 ─────────────────────────────────────────────────────────────────────

func TestInt64_Decode(t *testing.T) {
	c := codex.Int64()
	cases := []struct {
		name    string
		input   any
		want    int64
		wantErr bool
	}{
		{"from int", 42, 42, false},
		{"from int64", int64(9999999999), 9999999999, false},
		{"from float64 integral", float64(7), 7, false},
		{"from float64 non-integral", 3.14, 0, true},
		{"from string", "42", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Decode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Decode(%v) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("Decode(%v) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ── Float64 ───────────────────────────────────────────────────────────────────

func TestFloat64_Decode(t *testing.T) {
	c := codex.Float64()
	cases := []struct {
		name    string
		input   any
		want    float64
		wantErr bool
	}{
		{"from float64", 3.14, 3.14, false},
		{"from int", 5, 5.0, false},
		{"from int64", int64(10), 10.0, false},
		{"from string", "3.14", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Decode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Decode(%v) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("Decode(%v) = %f, want %f", tc.input, got, tc.want)
			}
		})
	}
}

func TestFloat64_Schema(t *testing.T) {
	if codex.Float64().Schema.Type != "number" {
		t.Errorf("Float64 schema type = %q, want %q", codex.Float64().Schema.Type, "number")
	}
}

// ── String ────────────────────────────────────────────────────────────────────

func TestString_Decode(t *testing.T) {
	c := codex.String()
	cases := []struct {
		name    string
		input   any
		want    string
		wantErr bool
	}{
		{"from string", "hello", "hello", false},
		{"empty string", "", "", false},
		{"from int", 42, "", true},
		{"from bool", true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Decode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Decode(%v) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("Decode(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestString_RoundTrip(t *testing.T) {
	c := codex.String()
	original := "round-trip"
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %q, want %q", got, original)
	}
}

// ── Bool ──────────────────────────────────────────────────────────────────────

func TestBool_Decode(t *testing.T) {
	c := codex.Bool()
	cases := []struct {
		name    string
		input   any
		want    bool
		wantErr bool
	}{
		{"true", true, true, false},
		{"false", false, false, false},
		{"from string", "true", false, true},
		{"from int", 1, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Decode(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Decode(%v) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Errorf("Decode(%v) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestBool_Schema(t *testing.T) {
	if codex.Bool().Schema.Type != "boolean" {
		t.Errorf("Bool schema type = %q, want %q", codex.Bool().Schema.Type, "boolean")
	}
}
