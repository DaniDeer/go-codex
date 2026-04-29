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
