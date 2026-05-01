package openapi_test

import (
	"encoding/json"
	"testing"

	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
)

func float64ptr(v float64) *float64 { return &v }
func intptr(v int) *int             { return &v }

// TestSchemaObject_delegates verifies that openapi.SchemaObject correctly
// delegates to the shared schemarender package. Detailed schema rendering
// behaviour is tested in render/internal/schemarender.
func TestSchemaObject_delegates(t *testing.T) {
	got := openapi.SchemaObject(schema.Schema{Type: "string", Description: "test"})
	if got["type"] != "string" {
		t.Errorf("type: want 'string', got %v", got["type"])
	}
	if got["description"] != "test" {
		t.Errorf("description: want 'test', got %v", got["description"])
	}
}

func TestComponentsSchemas(t *testing.T) {
	named := map[string]schema.Schema{
		"Tag":  {Type: "string", Description: "A tag label"},
		"Page": {Type: "integer", Minimum: float64ptr(1)},
	}
	got := openapi.ComponentsSchemas(named)
	if len(got) != 2 {
		t.Fatalf("want 2 schemas, got %d", len(got))
	}
	tag, ok := got["Tag"].(map[string]any)
	if !ok {
		t.Fatalf("Tag: want map[string]any, got %T", got["Tag"])
	}
	if tag["description"] != "A tag label" {
		t.Errorf("Tag description: want 'A tag label', got %v", tag["description"])
	}
}

func TestMarshalJSON_roundtrip(t *testing.T) {
	named := map[string]schema.Schema{
		"Score": {Type: "integer", Minimum: float64ptr(0), Maximum: float64ptr(100)},
	}
	data, err := openapi.MarshalJSON(named)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	score, ok := parsed["Score"].(map[string]any)
	if !ok {
		t.Fatalf("Score: want map, got %T", parsed["Score"])
	}
	if score["maximum"] != float64(100) {
		t.Errorf("Score.maximum: want 100, got %v", score["maximum"])
	}
}

func TestMarshalYAML_valid(t *testing.T) {
	named := map[string]schema.Schema{
		"Name": {Type: "string", MinLength: intptr(1)},
	}
	out, err := openapi.MarshalYAML(named)
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	if len(out) == 0 {
		t.Error("MarshalYAML returned empty output")
	}
}
