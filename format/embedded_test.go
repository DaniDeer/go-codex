package format_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared test types ─────────────────────────────────────────────────────────

type embeddedItem struct {
	Name  string
	Value int
}

var embeddedItemCodec = codex.Struct[embeddedItem](
	codex.RequiredField("name",
		codex.String().Refine(validate.NonEmptyString),
		func(i embeddedItem) string { return i.Name },
		func(i *embeddedItem, v string) { i.Name = v },
	),
	codex.RequiredField("value",
		codex.Int().Refine(validate.RangeInt(0, 1000)),
		func(i embeddedItem) int { return i.Value },
		func(i *embeddedItem, v int) { i.Value = v },
	),
)

// outerDoc wraps an embeddedItem as a string-encoded field.
type outerDoc struct {
	ID      string
	Payload embeddedItem
}

// ── EmbeddedJSON ──────────────────────────────────────────────────────────────

func TestEmbeddedJSON_DecodeHappyPath(t *testing.T) {
	c := format.EmbeddedJSON(embeddedItemCodec)
	got, err := c.Decode(`{"name":"widget","value":42}`)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Name != "widget" || got.Value != 42 {
		t.Errorf("want {widget,42}, got %+v", got)
	}
}

func TestEmbeddedJSON_EncodeHappyPath(t *testing.T) {
	c := format.EmbeddedJSON(embeddedItemCodec)
	raw, err := c.Encode(embeddedItem{Name: "widget", Value: 42})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	s, ok := raw.(string)
	if !ok {
		t.Fatalf("expected string intermediate, got %T", raw)
	}
	if s == "" {
		t.Error("expected non-empty JSON string")
	}
}

func TestEmbeddedJSON_RoundTrip(t *testing.T) {
	c := format.EmbeddedJSON(embeddedItemCodec)
	original := embeddedItem{Name: "sensor-01", Value: 99}
	encoded, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode after Encode: %v", err)
	}
	if decoded != original {
		t.Errorf("round-trip mismatch: want %+v, got %+v", original, decoded)
	}
}

func TestEmbeddedJSON_DecodeInvalidJSON_ReturnsEmbeddedDecodeError(t *testing.T) {
	c := format.EmbeddedJSON(embeddedItemCodec)
	_, err := c.Decode("not-valid-json{{{")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	var decErr format.EmbeddedDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected EmbeddedDecodeError, got %T: %v", err, err)
	}
	if decErr.Format != "json" {
		t.Errorf("expected Format=%q, got %q", "json", decErr.Format)
	}
}

func TestEmbeddedJSON_DecodeCodecValidationError_Propagates(t *testing.T) {
	// Inner codec constraint fails (value out of range) — not an EmbeddedDecodeError.
	c := format.EmbeddedJSON(embeddedItemCodec)
	_, err := c.Decode(`{"name":"x","value":9999}`) // exceeds RangeInt(0,1000)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var decErr format.EmbeddedDecodeError
	if errors.As(err, &decErr) {
		t.Fatal("codec validation error should not be wrapped in EmbeddedDecodeError")
	}
}

func TestEmbeddedJSON_EncodeCodecValidationError_Propagates(t *testing.T) {
	c := format.EmbeddedJSON(embeddedItemCodec)
	_, err := c.Encode(embeddedItem{Name: "", Value: 42}) // empty name fails NonEmptyString
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var encErr format.EmbeddedEncodeError
	if errors.As(err, &encErr) {
		t.Fatal("codec validation error should not be wrapped in EmbeddedEncodeError")
	}
}

func TestEmbeddedJSON_InStructCodec(t *testing.T) {
	// EmbeddedJSON inside a Struct codec — common usage pattern.
	outerCodec := codex.Struct[outerDoc](
		codex.RequiredField("id",
			codex.String(),
			func(o outerDoc) string { return o.ID },
			func(o *outerDoc, v string) { o.ID = v },
		),
		codex.RequiredField("payload",
			format.EmbeddedJSON(embeddedItemCodec),
			func(o outerDoc) embeddedItem { return o.Payload },
			func(o *outerDoc, v embeddedItem) { o.Payload = v },
		),
	)

	raw := map[string]any{
		"id":      "doc-1",
		"payload": `{"name":"widget","value":42}`,
	}
	doc, err := outerCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode outer: %v", err)
	}
	if doc.ID != "doc-1" || doc.Payload.Name != "widget" || doc.Payload.Value != 42 {
		t.Errorf("unexpected doc: %+v", doc)
	}
}

// ── EmbeddedDecodeError / EmbeddedEncodeError slog.LogValuer ─────────────────

func TestEmbeddedDecodeError_LogValue(t *testing.T) {
	e := format.EmbeddedDecodeError{Format: "json", Err: errors.New("invalid char")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
}

func TestEmbeddedEncodeError_LogValue(t *testing.T) {
	e := format.EmbeddedEncodeError{Format: "yaml", Err: errors.New("marshal failed")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group, got %v", v.Kind())
	}
}

func TestEmbeddedDecodeError_ErrorsAs(t *testing.T) {
	inner := errors.New("parse error")
	e := format.EmbeddedDecodeError{Format: "json", Err: inner}
	if !errors.Is(e, inner) {
		t.Fatal("errors.Is must find inner via Unwrap")
	}
}

func TestEmbeddedEncodeError_ErrorsAs(t *testing.T) {
	inner := errors.New("marshal error")
	e := format.EmbeddedEncodeError{Format: "toml", Err: inner}
	if !errors.Is(e, inner) {
		t.Fatal("errors.Is must find inner via Unwrap")
	}
}

// ── EmbeddedYAML ──────────────────────────────────────────────────────────────

func TestEmbeddedYAML_RoundTrip(t *testing.T) {
	c := format.EmbeddedYAML(embeddedItemCodec)
	original := embeddedItem{Name: "sensor-01", Value: 55}
	encoded, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != original {
		t.Errorf("want %+v, got %+v", original, decoded)
	}
}

func TestEmbeddedYAML_DecodeInvalidYAML_ReturnsEmbeddedDecodeError(t *testing.T) {
	c := format.EmbeddedYAML(embeddedItemCodec)
	_, err := c.Decode(":\t:\t invalid yaml {{{{")
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	var decErr format.EmbeddedDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected EmbeddedDecodeError, got %T: %v", err, err)
	}
	if decErr.Format != "yaml" {
		t.Errorf("expected Format=%q, got %q", "yaml", decErr.Format)
	}
}

func TestEmbeddedYAML_IntegersDecodedCorrectly(t *testing.T) {
	// YAML unmarshals integers as int (not float64). Verify the codec handles this.
	c := format.EmbeddedYAML(embeddedItemCodec)
	got, err := c.Decode("name: gadget\nvalue: 7\n")
	if err != nil {
		t.Fatalf("Decode YAML int: %v", err)
	}
	if got.Value != 7 {
		t.Errorf("expected value=7, got %d", got.Value)
	}
}

// ── EmbeddedTOML ─────────────────────────────────────────────────────────────

func TestEmbeddedTOML_RoundTrip(t *testing.T) {
	c := format.EmbeddedTOML(embeddedItemCodec)
	original := embeddedItem{Name: "device-01", Value: 128}
	encoded, err := c.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := c.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded != original {
		t.Errorf("want %+v, got %+v", original, decoded)
	}
}

func TestEmbeddedTOML_DecodeInvalidTOML_ReturnsEmbeddedDecodeError(t *testing.T) {
	c := format.EmbeddedTOML(embeddedItemCodec)
	_, err := c.Decode("not = valid [[[toml")
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
	var decErr format.EmbeddedDecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected EmbeddedDecodeError, got %T: %v", err, err)
	}
	if decErr.Format != "toml" {
		t.Errorf("expected Format=%q, got %q", "toml", decErr.Format)
	}
}

func TestEmbeddedTOML_IntegersDecodedCorrectly(t *testing.T) {
	// TOML unmarshals integers as int64 (not float64). Verify the codec handles this.
	c := format.EmbeddedTOML(embeddedItemCodec)
	got, err := c.Decode("name = \"sensor\"\nvalue = 256\n")
	if err != nil {
		t.Fatalf("Decode TOML int64: %v", err)
	}
	if got.Value != 256 {
		t.Errorf("expected value=256, got %d", got.Value)
	}
}
