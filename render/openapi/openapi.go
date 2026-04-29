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

	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// SchemaObject converts s into an OpenAPI 3.x schema object (map[string]any).
// Only fields that are set in s are included in the output.
func SchemaObject(s schema.Schema) map[string]any {
	obj := map[string]any{}

	if s.Type != "" {
		obj["type"] = s.Type
	}
	if s.Title != "" {
		obj["title"] = s.Title
	}
	if s.Description != "" {
		obj["description"] = s.Description
	}
	if s.Format != "" {
		obj["format"] = s.Format
	}
	if s.Example != nil {
		obj["example"] = s.Example
	}

	// Numeric bounds.
	if s.Minimum != nil {
		obj["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		obj["maximum"] = *s.Maximum
	}
	if s.ExclusiveMinimum {
		obj["exclusiveMinimum"] = true
	}
	if s.ExclusiveMaximum {
		obj["exclusiveMaximum"] = true
	}

	// String constraints.
	if s.MinLength != nil {
		obj["minLength"] = *s.MinLength
	}
	if s.MaxLength != nil {
		obj["maxLength"] = *s.MaxLength
	}
	if s.Pattern != "" {
		obj["pattern"] = s.Pattern
	}

	// Enum.
	if len(s.Enum) > 0 {
		obj["enum"] = s.Enum
	}

	// Object properties.
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for name, prop := range s.Properties {
			props[name] = SchemaObject(prop)
		}
		obj["properties"] = props
	}
	if len(s.Required) > 0 {
		obj["required"] = s.Required
	}

	// Array items.
	if s.Items != nil {
		obj["items"] = SchemaObject(*s.Items)
	}

	// Polymorphism.
	if len(s.OneOf) > 0 {
		oneOf := make([]any, len(s.OneOf))
		for i, variant := range s.OneOf {
			oneOf[i] = SchemaObject(variant)
		}
		obj["oneOf"] = oneOf
	}

	return obj
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
