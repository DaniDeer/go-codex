package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/DaniDeer/go-codex/schema"
)

func TestSchema_JSONEmptyOmitsAllFields(t *testing.T) {
	s := schema.Schema{}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Errorf("empty Schema marshalled to %s, want {}", string(b))
	}
}

func TestSchema_JSONTypeOnly(t *testing.T) {
	s := schema.Schema{Type: "string"}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"Type":"string"}` {
		t.Errorf("Schema{Type:string} marshalled to %s", string(b))
	}
}

func TestSchema_JSONWithItems(t *testing.T) {
	itemSchema := schema.Schema{Type: "integer"}
	s := schema.Schema{Type: "array", Items: &itemSchema}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["Type"] != "array" {
		t.Errorf("Type = %v, want array", out["Type"])
	}
	items, ok := out["Items"].(map[string]any)
	if !ok {
		t.Fatalf("Items is not an object: %T", out["Items"])
	}
	if items["Type"] != "integer" {
		t.Errorf("Items.Type = %v, want integer", items["Type"])
	}
}

func TestSchema_JSONNilItemsOmitted(t *testing.T) {
	s := schema.Schema{Type: "string"}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if _, ok := out["Items"]; ok {
		t.Error("Items should be omitted when nil")
	}
}

func TestSchema_IsZero_emptySchema(t *testing.T) {
	s := schema.Schema{}
	if !s.IsZero() {
		t.Error("zero Schema should report IsZero() = true")
	}
}

func TestSchema_IsZero_typeSet(t *testing.T) {
	s := schema.Schema{Type: "string"}
	if s.IsZero() {
		t.Error("Schema{Type:string} should report IsZero() = false")
	}
}

func TestSchema_IsZero_descriptionOnly(t *testing.T) {
	s := schema.Schema{Description: "a description"}
	if s.IsZero() {
		t.Error("Schema with Description set should report IsZero() = false")
	}
}

func TestSchema_IsZero_propertiesSet(t *testing.T) {
	s := schema.Schema{Properties: []schema.Property{{Name: "id", Schema: schema.Schema{Type: "string"}}}}
	if s.IsZero() {
		t.Error("Schema with Properties set should report IsZero() = false")
	}
}

func TestSchema_IsZero_minItemsSet(t *testing.T) {
	n := 1
	s := schema.Schema{MinItems: &n}
	if s.IsZero() {
		t.Error("Schema with MinItems set should report IsZero() = false")
	}
}

func TestSchema_IsZero_maxItemsSet(t *testing.T) {
	n := 10
	s := schema.Schema{MaxItems: &n}
	if s.IsZero() {
		t.Error("Schema with MaxItems set should report IsZero() = false")
	}
}

func TestSchema_IsZero_uniqueItemsSet(t *testing.T) {
	s := schema.Schema{UniqueItems: true}
	if s.IsZero() {
		t.Error("Schema with UniqueItems set should report IsZero() = false")
	}
}

func TestSchema_IsZero_minPropertiesSet(t *testing.T) {
	n := 1
	s := schema.Schema{MinProperties: &n}
	if s.IsZero() {
		t.Error("Schema with MinProperties set should report IsZero() = false")
	}
}

func TestSchema_IsZero_maxPropertiesSet(t *testing.T) {
	n := 5
	s := schema.Schema{MaxProperties: &n}
	if s.IsZero() {
		t.Error("Schema with MaxProperties set should report IsZero() = false")
	}
}
