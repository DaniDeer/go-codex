// Package openapi renders schema.Schema values as OpenAPI 3.x schema objects.
//
// It imports only the schema package — no codec logic is involved. Renderers
// read pure schema data; codecs write it. This separation means the same
// schema.Schema can be used by multiple renderers without any coupling.
//
// Typical usage:
//
//	named := map[string]schema.Schema{
//	    "User": UserCodec.Schema,
//	}
//	out, err := openapi.MarshalYAML(named)
package openapi

import (
	"encoding/json"

	"github.com/DaniDeer/go-codex/render/internal/schemarender"
	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// SchemaObject converts s into an OpenAPI 3.x schema object (map[string]any).
// Only fields that are set in s are included in the output.
func SchemaObject(s schema.Schema) map[string]any {
	return schemarender.SchemaObject(s)
}

// ComponentsSchemas produces the map suitable for embedding as the value of
// components.schemas in an OpenAPI 3.x document.
func ComponentsSchemas(named map[string]schema.Schema) map[string]any {
	out := make(map[string]any, len(named))
	for name, s := range named {
		out[name] = SchemaObject(s)
	}
	return out
}

// MarshalJSON renders the named schemas as the JSON bytes of a
// components/schemas map, suitable for embedding in a larger OpenAPI document.
func MarshalJSON(named map[string]schema.Schema) ([]byte, error) {
	return json.Marshal(ComponentsSchemas(named))
}

// MarshalYAML renders the named schemas as the YAML bytes of a
// components/schemas map, suitable for embedding in a larger OpenAPI document.
func MarshalYAML(named map[string]schema.Schema) ([]byte, error) {
	return yaml.Marshal(ComponentsSchemas(named))
}
