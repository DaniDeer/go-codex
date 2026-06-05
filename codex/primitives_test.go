package codex_test

import (
	"math"
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

// ── Bytes ─────────────────────────────────────────────────────────────────────

func TestBytes_RoundTrip(t *testing.T) {
	c := codex.Bytes()
	original := []byte{0x00, 0xFF, 0xAB, 0x12}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("round-trip mismatch: want %v, got %v", original, got)
	}
}

func TestBytes_EncodeIsBase64String(t *testing.T) {
	c := codex.Bytes()
	enc, _ := c.Encode([]byte("hello"))
	s, ok := enc.(string)
	if !ok {
		t.Fatalf("expected string, got %T", enc)
	}
	if s != "aGVsbG8=" {
		t.Errorf("want %q, got %q", "aGVsbG8=", s)
	}
}

func TestBytes_DecodeInvalidBase64(t *testing.T) {
	c := codex.Bytes()
	if _, err := c.Decode("not-valid-base64!!!"); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestBytes_DecodeWrongType(t *testing.T) {
	c := codex.Bytes()
	if _, err := c.Decode(42); err == nil {
		t.Fatal("expected error for non-string input")
	}
}

func TestBytes_Schema(t *testing.T) {
	c := codex.Bytes()
	if c.Schema.Type != "string" {
		t.Errorf("want type=string, got %q", c.Schema.Type)
	}
	if c.Schema.Format != "byte" {
		t.Errorf("want format=byte, got %q", c.Schema.Format)
	}
}

// ── Int32 ─────────────────────────────────────────────────────────────────────

func TestInt32_Decode(t *testing.T) {
	c := codex.Int32()
	cases := []struct {
		name    string
		input   any
		want    int32
		wantErr bool
	}{
		{"from int32", int32(42), 42, false},
		{"from int", 100, 100, false},
		{"from float64 integral", float64(7), 7, false},
		{"from float64 non-integral", 3.14, 0, true},
		{"from int64 in range", int64(32767), 32767, false},
		{"from int overflow", int(math.MaxInt32 + 1), 0, true},
		{"from float64 overflow", float64(math.MaxInt32 + 1), 0, true},
		{"from string", "42", 0, true},
		{"negative", -10, -10, false},
		{"zero", 0, 0, false},
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

func TestInt32_Encode(t *testing.T) {
	c := codex.Int32()
	got, err := c.Encode(int32(99))
	if err != nil {
		t.Fatal(err)
	}
	if got != int32(99) {
		t.Errorf("Encode(99) = %v, want 99", got)
	}
}

func TestInt32_Schema(t *testing.T) {
	c := codex.Int32()
	if c.Schema.Type != "integer" {
		t.Errorf("Int32 schema type = %q, want %q", c.Schema.Type, "integer")
	}
	if c.Schema.Format != "int32" {
		t.Errorf("Int32 schema format = %q, want %q", c.Schema.Format, "int32")
	}
}

func TestInt32_RoundTrip(t *testing.T) {
	c := codex.Int32()
	encoded, err := c.Encode(int32(12345))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != int32(12345) {
		t.Errorf("round-trip: got %d, want 12345", decoded)
	}
}

// ── Float32 ───────────────────────────────────────────────────────────────────

func TestFloat32_Decode(t *testing.T) {
	c := codex.Float32()
	cases := []struct {
		name    string
		input   any
		want    float32
		wantErr bool
	}{
		{"from float64", float64(3.14), float32(3.14), false},
		{"from float32", float32(2.5), float32(2.5), false},
		{"from int", 10, float32(10), false},
		{"from int64", int64(20), float32(20), false},
		{"from string", "3.14", 0, true},
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

func TestFloat32_Encode(t *testing.T) {
	c := codex.Float32()
	got, err := c.Encode(float32(1.5))
	if err != nil {
		t.Fatal(err)
	}
	if got != float64(float32(1.5)) {
		t.Errorf("Encode(1.5) = %v, want float64(float32(1.5))", got)
	}
}

func TestFloat32_Schema(t *testing.T) {
	c := codex.Float32()
	if c.Schema.Type != "number" {
		t.Errorf("Float32 schema type = %q, want %q", c.Schema.Type, "number")
	}
	if c.Schema.Format != "float" {
		t.Errorf("Float32 schema format = %q, want %q", c.Schema.Format, "float")
	}
}

// ── Uint ──────────────────────────────────────────────────────────────────────

func TestUint_Decode(t *testing.T) {
	c := codex.Uint()
	cases := []struct {
		name    string
		input   any
		want    uint
		wantErr bool
	}{
		{"from uint", uint(42), 42, false},
		{"from uint64", uint64(100), 100, false},
		{"from int positive", 10, 10, false},
		{"from int64 positive", int64(20), 20, false},
		{"from float64 integral", float64(7), 7, false},
		{"from float64 non-integral", 3.14, 0, true},
		{"from int negative", -1, 0, true},
		{"from float64 negative", float64(-5), 0, true},
		{"from int64 negative", int64(-3), 0, true},
		{"from string", "42", 0, true},
		{"zero", 0, 0, false},
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

func TestUint_Encode(t *testing.T) {
	c := codex.Uint()
	got, err := c.Encode(uint(99))
	if err != nil {
		t.Fatal(err)
	}
	if got != uint(99) {
		t.Errorf("Encode(99) = %v, want 99", got)
	}
}

func TestUint_Schema(t *testing.T) {
	c := codex.Uint()
	if c.Schema.Type != "integer" {
		t.Errorf("Uint schema type = %q, want %q", c.Schema.Type, "integer")
	}
	if c.Schema.Minimum == nil || *c.Schema.Minimum != 0 {
		t.Errorf("Uint schema minimum: expected 0, got %v", c.Schema.Minimum)
	}
}

func TestUint_RoundTrip(t *testing.T) {
	c := codex.Uint()
	encoded, err := c.Encode(uint(12345))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != uint(12345) {
		t.Errorf("round-trip: got %d, want 12345", decoded)
	}
}

// ── Uint64 ────────────────────────────────────────────────────────────────────

func TestUint64_Decode(t *testing.T) {
	c := codex.Uint64()
	cases := []struct {
		name    string
		input   any
		want    uint64
		wantErr bool
	}{
		{"from uint64", uint64(42), 42, false},
		{"from uint", uint(100), 100, false},
		{"from int positive", 10, 10, false},
		{"from int64 positive", int64(20), 20, false},
		{"from float64 integral", float64(7), 7, false},
		{"from float64 non-integral", 3.14, 0, true},
		{"from int negative", -1, 0, true},
		{"from float64 negative", float64(-5), 0, true},
		{"from int64 negative", int64(-3), 0, true},
		{"from string", "42", 0, true},
		{"zero", 0, 0, false},
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

func TestUint64_Encode(t *testing.T) {
	c := codex.Uint64()
	got, err := c.Encode(uint64(99))
	if err != nil {
		t.Fatal(err)
	}
	if got != uint64(99) {
		t.Errorf("Encode(99) = %v, want 99", got)
	}
}

func TestUint64_Schema(t *testing.T) {
	c := codex.Uint64()
	if c.Schema.Type != "integer" {
		t.Errorf("Uint64 schema type = %q, want %q", c.Schema.Type, "integer")
	}
	if c.Schema.Minimum == nil || *c.Schema.Minimum != 0 {
		t.Errorf("Uint64 schema minimum: expected 0, got %v", c.Schema.Minimum)
	}
}

func TestUint64_RoundTrip(t *testing.T) {
	c := codex.Uint64()
	encoded, err := c.Encode(uint64(9999999))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != uint64(9999999) {
		t.Errorf("round-trip: got %d, want 9999999", decoded)
	}
}

// ── Any ───────────────────────────────────────────────────────────────────────

func TestAny_DecodePrimitivesPassThrough(t *testing.T) {
	c := codex.Any()
	cases := []any{"hello", 42, 3.14, true, nil}
	for _, tc := range cases {
		got, err := c.Decode(tc)
		if err != nil {
			t.Errorf("Decode(%v) unexpected error: %v", tc, err)
		}
		if got != tc {
			t.Errorf("Decode(%v) = %v, want pass-through", tc, got)
		}
	}
}

func TestAny_DecodeMapPassesThrough(t *testing.T) {
	c := codex.Any()
	val := map[string]any{"key": "value"}
	got, err := c.Decode(val)
	if err != nil {
		t.Fatal(err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok || gotMap["key"] != "value" {
		t.Errorf("Decode map: got %v, want pass-through", got)
	}
}

func TestAny_EncodePassesThrough(t *testing.T) {
	c := codex.Any()
	val := map[string]any{"x": 1}
	got, err := c.Encode(val)
	if err != nil {
		t.Fatal(err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok || gotMap["x"] != 1 {
		t.Errorf("Encode should pass through unchanged, got %v", got)
	}
}

func TestAny_SchemaIsEmpty(t *testing.T) {
	c := codex.Any()
	if c.Schema.Type != "" {
		t.Errorf("Any schema Type should be empty, got %q", c.Schema.Type)
	}
}

func TestPure_DecodeIgnoresInput(t *testing.T) {
	c := codex.Pure("fixed")
	for _, raw := range []any{nil, "other", 42, map[string]any{"x": 1}} {
		got, err := c.Decode(raw)
		if err != nil {
			t.Fatalf("Decode(%v) unexpected error: %v", raw, err)
		}
		if got != "fixed" {
			t.Errorf("Decode(%v) = %q, want %q", raw, got, "fixed")
		}
	}
}

func TestPure_EncodeIgnoresInput(t *testing.T) {
	c := codex.Pure("fixed")
	got, err := c.Encode("anything")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fixed" {
		t.Errorf("Encode = %v, want %q", got, "fixed")
	}
}

func TestPure_SchemaEnum(t *testing.T) {
	c := codex.Pure(42)
	if len(c.Schema.Enum) != 1 || c.Schema.Enum[0] != 42 {
		t.Errorf("Pure schema Enum = %v, want [42]", c.Schema.Enum)
	}
}

func TestEmpty_isZeroStruct(t *testing.T) {
	v, err := codex.Empty.Decode(map[string]any{})
	if err != nil {
		t.Fatalf("Empty.Decode: %v", err)
	}
	if v != (struct{}{}) {
		t.Errorf("Empty.Decode = %v, want struct{}{}", v)
	}
	enc, err := codex.Empty.Encode(struct{}{})
	if err != nil {
		t.Fatalf("Empty.Encode: %v", err)
	}
	if enc == nil {
		t.Error("Empty.Encode returned nil")
	}
}
