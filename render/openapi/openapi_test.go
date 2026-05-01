package openapi_test

import (
	"encoding/json"
	"testing"

	"github.com/DaniDeer/go-codex/render/openapi"
	"github.com/DaniDeer/go-codex/schema"
)

func float64ptr(v float64) *float64 { return &v }
func intptr(v int) *int             { return &v }

func TestSchemaObject_primitive(t *testing.T) {
	cases := []struct {
		name   string
		input  schema.Schema
		wantKV map[string]any
	}{
		{
			name:   "string type",
			input:  schema.Schema{Type: "string"},
			wantKV: map[string]any{"type": "string"},
		},
		{
			name:   "integer with title and description",
			input:  schema.Schema{Type: "integer", Title: "Age", Description: "User age in years"},
			wantKV: map[string]any{"type": "integer", "title": "Age", "description": "User age in years"},
		},
		{
			name:   "number with format",
			input:  schema.Schema{Type: "number", Format: "double"},
			wantKV: map[string]any{"type": "number", "format": "double"},
		},
		{
			name:   "string with example",
			input:  schema.Schema{Type: "string", Example: "alice@example.com"},
			wantKV: map[string]any{"type": "string", "example": "alice@example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := openapi.SchemaObject(tc.input)
			for k, want := range tc.wantKV {
				if got[k] != want {
					t.Errorf("key %q: want %v, got %v", k, want, got[k])
				}
			}
		})
	}
}

func TestSchemaObject_numericBounds(t *testing.T) {
	cases := []struct {
		name  string
		input schema.Schema
		check func(t *testing.T, got map[string]any)
	}{
		{
			name:  "minimum inclusive",
			input: schema.Schema{Type: "integer", Minimum: float64ptr(0)},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got["minimum"] != float64(0) {
					t.Errorf("minimum: want 0, got %v", got["minimum"])
				}
				if _, ok := got["exclusiveMinimum"]; ok {
					t.Error("exclusiveMinimum should be absent")
				}
			},
		},
		{
			name:  "exclusive minimum",
			input: schema.Schema{Type: "integer", Minimum: float64ptr(0), ExclusiveMinimum: true},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got["exclusiveMinimum"] != true {
					t.Errorf("exclusiveMinimum: want true, got %v", got["exclusiveMinimum"])
				}
			},
		},
		{
			name:  "maximum exclusive",
			input: schema.Schema{Type: "integer", Maximum: float64ptr(0), ExclusiveMaximum: true},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got["exclusiveMaximum"] != true {
					t.Errorf("exclusiveMaximum: want true, got %v", got["exclusiveMaximum"])
				}
			},
		},
		{
			name:  "range",
			input: schema.Schema{Type: "number", Minimum: float64ptr(1.5), Maximum: float64ptr(9.9)},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got["minimum"] != 1.5 {
					t.Errorf("minimum: want 1.5, got %v", got["minimum"])
				}
				if got["maximum"] != 9.9 {
					t.Errorf("maximum: want 9.9, got %v", got["maximum"])
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, openapi.SchemaObject(tc.input))
		})
	}
}

func TestSchemaObject_stringConstraints(t *testing.T) {
	s := schema.Schema{
		Type:      "string",
		MinLength: intptr(3),
		MaxLength: intptr(64),
		Pattern:   `^[a-z]+$`,
	}
	got := openapi.SchemaObject(s)
	if got["minLength"] != 3 {
		t.Errorf("minLength: want 3, got %v", got["minLength"])
	}
	if got["maxLength"] != 64 {
		t.Errorf("maxLength: want 64, got %v", got["maxLength"])
	}
	if got["pattern"] != `^[a-z]+$` {
		t.Errorf("pattern: want ^[a-z]+$, got %v", got["pattern"])
	}
}

func TestSchemaObject_enum(t *testing.T) {
	s := schema.Schema{Type: "string", Enum: []any{"red", "green", "blue"}}
	got := openapi.SchemaObject(s)
	enum, ok := got["enum"].([]any)
	if !ok {
		t.Fatalf("enum: want []any, got %T", got["enum"])
	}
	if len(enum) != 3 || enum[0] != "red" {
		t.Errorf("enum: unexpected value %v", enum)
	}
}

func TestSchemaObject_object(t *testing.T) {
	s := schema.Schema{
		Type: "object",
		Properties: []schema.Property{
			{Name: "name", Schema: schema.Schema{Type: "string", Description: "Full name"}},
			{Name: "age", Schema: schema.Schema{Type: "integer"}},
		},
		Required: []string{"name"},
	}
	got := openapi.SchemaObject(s)

	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties: want map[string]any, got %T", got["properties"])
	}
	nameProp, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatalf("properties.name: want map[string]any, got %T", props["name"])
	}
	if nameProp["description"] != "Full name" {
		t.Errorf("properties.name.description: want 'Full name', got %v", nameProp["description"])
	}

	req, ok := got["required"].([]string)
	if !ok {
		t.Fatalf("required: want []string, got %T", got["required"])
	}
	if len(req) != 1 || req[0] != "name" {
		t.Errorf("required: unexpected value %v", req)
	}
}

func TestSchemaObject_array(t *testing.T) {
	s := schema.Schema{
		Type:  "array",
		Items: &schema.Schema{Type: "string"},
	}
	got := openapi.SchemaObject(s)
	items, ok := got["items"].(map[string]any)
	if !ok {
		t.Fatalf("items: want map[string]any, got %T", got["items"])
	}
	if items["type"] != "string" {
		t.Errorf("items.type: want 'string', got %v", items["type"])
	}
}

func TestSchemaObject_oneOf(t *testing.T) {
	s := schema.Schema{
		OneOf: []schema.Schema{
			{Type: "string"},
			{Type: "integer"},
		},
	}
	got := openapi.SchemaObject(s)
	oneOf, ok := got["oneOf"].([]any)
	if !ok {
		t.Fatalf("oneOf: want []any, got %T", got["oneOf"])
	}
	if len(oneOf) != 2 {
		t.Errorf("oneOf: want 2 entries, got %d", len(oneOf))
	}
}

func TestSchemaObject_emptyFieldsOmitted(t *testing.T) {
	s := schema.Schema{Type: "string"}
	got := openapi.SchemaObject(s)
	for _, key := range []string{"title", "description", "format", "example", "minimum", "maximum",
		"exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "pattern",
		"enum", "properties", "required", "items", "oneOf"} {
		if _, ok := got[key]; ok {
			t.Errorf("key %q should be absent for a bare string schema", key)
		}
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
