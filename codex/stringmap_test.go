package codex_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
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

// sensorIDPattern matches keys of the form <sensor>-<id>, e.g. "temp-01".
var sensorIDPattern = regexp.MustCompile(`^[a-z]+-\d+$`)

func TestMap_RoundTrip(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	original := map[string]int{"temp-01": 42, "press-02": 7}
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

func TestMap_TypedKey_RoundTrip(t *testing.T) {
	type SensorID = string
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern)).WithTitle("SensorID")
	c := codex.Map[SensorID, float64](keyCodec, codex.Float64())
	original := map[SensorID]float64{"vibr-03": 1.5}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["vibr-03"] != 1.5 {
		t.Errorf("want 1.5, got %v", got["vibr-03"])
	}
}

func TestMap_KeyValidationError_Encode(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	_, err := c.Encode(map[string]int{"INVALID_KEY": 1})
	if err == nil {
		t.Fatal("expected error for invalid key on Encode")
	}
	var keyErr codex.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want KeyError, got %T: %v", err, err)
	}
	if keyErr.Key != "INVALID_KEY" {
		t.Errorf("want key=INVALID_KEY, got %q", keyErr.Key)
	}
}

func TestMap_KeyValidationError_Decode(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	raw := map[string]any{"INVALID_KEY": 1}
	_, err := c.Decode(raw)
	if err == nil {
		t.Fatal("expected error for invalid key on Decode")
	}
}

func TestMap_ValueError(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern))
	c := codex.Map[string, int](keyCodec, codex.Int())
	raw := map[string]any{"temp-01": "not-a-number"}
	_, err := c.Decode(raw)
	if err == nil {
		t.Fatal("expected error for bad value type")
	}
	var keyErr codex.KeyError
	if !errors.As(err, &keyErr) {
		t.Fatalf("want KeyError wrapping value error, got %T: %v", err, err)
	}
}

func TestMap_DecodeWrongType(t *testing.T) {
	c := codex.Map[string, int](codex.String(), codex.Int())
	if _, err := c.Decode("not-an-object"); err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestMap_Schema(t *testing.T) {
	keyCodec := codex.String().Refine(validate.Pattern(sensorIDPattern)).WithTitle("SensorID")
	c := codex.Map[string, float64](keyCodec, codex.Float64())
	s := c.Schema
	if s.Type != "object" {
		t.Errorf("want type=object, got %q", s.Type)
	}
	if s.PropertyNames == nil {
		t.Fatal("want PropertyNames set")
	}
	if s.PropertyNames.Title != "SensorID" {
		t.Errorf("want PropertyNames.Title=SensorID, got %q", s.PropertyNames.Title)
	}
	if s.AdditionalPropertiesSchema == nil {
		t.Fatal("want AdditionalPropertiesSchema set")
	}
	if s.AdditionalPropertiesSchema.Type != "number" {
		t.Errorf("want additionalProperties type=number, got %q", s.AdditionalPropertiesSchema.Type)
	}
}
