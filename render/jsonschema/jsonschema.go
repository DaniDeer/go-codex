package jsonschema

import (
	"encoding/json"

	"github.com/DaniDeer/go-codex/render/internal/schemarender"
	"github.com/DaniDeer/go-codex/schema"
)

// Schema renders s to a JSON Schema compatible [json.RawMessage].
// Returns nil when s is the zero value — callers may omit the schema field.
func Schema(s schema.Schema) (json.RawMessage, error) {
	if s.IsZero() {
		return nil, nil
	}
	obj := schemarender.SchemaObject(s)
	return json.Marshal(obj)
}
