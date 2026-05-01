package codex_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestStringMap_RoundTrip(t *testing.T) {
	c := codex.StringMap(codex.Int())
	original := map[string]int{"a": 1, "b": 2}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for k, v := range original {
		if got[k] != v {
			t.Errorf("key %q: want %d, got %d", k, v, got[k])
		}
	}
}

func TestStringMap_Empty(t *testing.T) {
	c := codex.StringMap(codex.String())
	enc, err := c.Encode(map[string]string{})
	if err != nil {
		t.Fatalf("Encode empty: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestStringMap_DecodeWrongType(t *testing.T) {
	c := codex.StringMap(codex.Int())
	if _, err := c.Decode("not-an-object"); err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestStringMap_DecodeValueError(t *testing.T) {
	c := codex.StringMap(codex.Int())
	raw := map[string]any{"k": "not-a-number"}
	if _, err := c.Decode(raw); err == nil {
		t.Fatal("expected error for bad value type")
	}
}

func TestStringMap_Schema(t *testing.T) {
	c := codex.StringMap(codex.String())
	if c.Schema.Type != "object" {
		t.Errorf("want type=object, got %q", c.Schema.Type)
	}
	if c.Schema.AdditionalPropertiesSchema == nil {
		t.Fatal("want AdditionalPropertiesSchema set")
	}
	if c.Schema.AdditionalPropertiesSchema.Type != "string" {
		t.Errorf("want additionalProperties type=string, got %q", c.Schema.AdditionalPropertiesSchema.Type)
	}
}

func TestStringMap_SchemaDoesNotMutateInner(t *testing.T) {
	inner := codex.Int()
	_ = codex.StringMap(inner)
	if inner.Schema.AdditionalPropertiesSchema != nil {
		t.Error("StringMap should not mutate inner codec schema")
	}
}
