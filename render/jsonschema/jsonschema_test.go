package jsonschema_test

import (
	"encoding/json"
	"testing"

	"github.com/DaniDeer/go-codex/render/jsonschema"
	"github.com/DaniDeer/go-codex/schema"
)

func TestSchema_zeroValueReturnsNil(t *testing.T) {
	raw, err := jsonschema.Schema(schema.Schema{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw != nil {
		t.Fatalf("expected nil for zero schema, got %s", raw)
	}
}

func TestSchema_stringType(t *testing.T) {
	minLen := 1
	s := schema.Schema{
		Type:      "string",
		Title:     "Name",
		MinLength: &minLen,
	}
	raw, err := jsonschema.Schema(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if obj["type"] != "string" {
		t.Errorf("type: got %v, want string", obj["type"])
	}
	if obj["title"] != "Name" {
		t.Errorf("title: got %v, want Name", obj["title"])
	}
	if v, ok := obj["minLength"].(float64); !ok || v != 1 {
		t.Errorf("minLength: got %v, want 1", obj["minLength"])
	}
}

func TestSchema_objectWithProperties(t *testing.T) {
	s := schema.Schema{
		Type: "object",
		Properties: []schema.Property{
			{Name: "id", Schema: schema.Schema{Type: "string"}},
			{Name: "count", Schema: schema.Schema{Type: "integer"}},
		},
		Required: []string{"id"},
	}
	raw, err := jsonschema.Schema(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	props, ok := obj["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not a map, got %T", obj["properties"])
	}
	if _, ok := props["id"]; !ok {
		t.Error("properties missing 'id'")
	}
	if _, ok := props["count"]; !ok {
		t.Error("properties missing 'count'")
	}
	req, ok := obj["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "id" {
		t.Errorf("required: got %v, want [id]", obj["required"])
	}
}

func TestSchema_enumField(t *testing.T) {
	s := schema.Schema{
		Type: "string",
		Enum: []any{"debug", "info", "warn", "error"},
	}
	raw, err := jsonschema.Schema(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	enum, ok := obj["enum"].([]any)
	if !ok || len(enum) != 4 {
		t.Errorf("enum: got %v, want 4 items", obj["enum"])
	}
}

func TestSchema_numericConstraints(t *testing.T) {
	min := float64(1)
	max := float64(65535)
	s := schema.Schema{
		Type:    "integer",
		Minimum: &min,
		Maximum: &max,
	}
	raw, err := jsonschema.Schema(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if obj["minimum"] != float64(1) {
		t.Errorf("minimum: got %v, want 1", obj["minimum"])
	}
	if obj["maximum"] != float64(65535) {
		t.Errorf("maximum: got %v, want 65535", obj["maximum"])
	}
}
