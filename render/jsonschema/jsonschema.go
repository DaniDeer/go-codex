// Package jsonschema renders [schema.Schema] values to plain JSON Schema
// compatible [json.RawMessage].
//
// Unlike [render/openapi] and [render/asyncapi], this package produces
// stand-alone JSON Schema objects without any OpenAPI or AsyncAPI envelope.
// The output is suitable for use as MCP tool input/output schemas or any
// context that requires raw JSON Schema rather than a full API document.
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
