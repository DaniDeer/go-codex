package codex_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestSliceOf_Encode(t *testing.T) {
	c := codex.SliceOf(codex.Int())
	enc, err := c.Encode([]int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := enc.([]any)
	if !ok {
		t.Fatalf("encoded type = %T, want []any", enc)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("encoded = %v, want [1 2 3]", got)
	}
}

func TestSliceOf_Decode(t *testing.T) {
	c := codex.SliceOf(codex.String())
	got, err := c.Decode([]any{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("decoded = %v, want [a b c]", got)
	}
}

func TestSliceOf_DecodeNonArray(t *testing.T) {
	c := codex.SliceOf(codex.Int())
	_, err := c.Decode("not-an-array")
	if err == nil {
		t.Fatal("expected error for non-array input")
	}
}

func TestSliceOf_DecodeElementError(t *testing.T) {
	c := codex.SliceOf(codex.Int())
	_, err := c.Decode([]any{1, 2, "three"})
	if err == nil {
		t.Fatal("expected error for wrong element type")
	}
	if !strings.Contains(err.Error(), "element 2") {
		t.Errorf("error %q does not include element index", err.Error())
	}
}

func TestSliceOf_EmptySlice(t *testing.T) {
	c := codex.SliceOf(codex.Int())
	enc, err := c.Encode([]int{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestSliceOf_RoundTrip(t *testing.T) {
	c := codex.SliceOf(codex.Int())
	original := []int{10, 20, 30}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(original))
	}
	for i := range original {
		if got[i] != original[i] {
			t.Errorf("element %d: got %d, want %d", i, got[i], original[i])
		}
	}
}

func TestSliceOf_Schema(t *testing.T) {
	c := codex.SliceOf(codex.Int())
	s := c.Schema
	if s.Type != "array" {
		t.Errorf("schema type = %q, want %q", s.Type, "array")
	}
	if s.Items == nil {
		t.Fatal("schema Items is nil")
	}
	if s.Items.Type != "integer" {
		t.Errorf("schema Items.Type = %q, want %q", s.Items.Type, "integer")
	}
}
