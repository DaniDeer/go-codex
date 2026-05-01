// Package asyncapi renders schema.Schema values as an AsyncAPI 2.6 document.
//
// It imports only the schema package — no codec logic is involved. The same
// schema.Schema that drives OpenAPI output can describe AsyncAPI message payloads.
//
// Typical usage:
//
//	doc, err := asyncapi.NewDocumentBuilder(asyncapi.Info{
//	    Title:   "User Events",
//	    Version: "1.0.0",
//	}).
//	    AddChannel("user/created", asyncapi.ChannelItem{
//	        Subscribe: &asyncapi.Operation{
//	            Summary: "User created event",
//	            Message: asyncapi.Message{
//	                Schema:     UserCodec.Schema,
//	                SchemaName: "User",
//	            },
//	        },
//	    }).
//	    Build()
//
//	yamlBytes, err := doc.MarshalYAML()
package asyncapi

import "github.com/DaniDeer/go-codex/schema"

// schemaRef returns a $ref object when name is non-empty, otherwise inlines the schema.
func schemaRef(s schema.Schema, name string) map[string]any {
	if name != "" {
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return schemaObject(s)
}

// buildComponentsSchemas renders named schemas as component schema objects.
func buildComponentsSchemas(named map[string]schema.Schema) map[string]any {
	out := make(map[string]any, len(named))
	for name, s := range named {
		out[name] = schemaObject(s)
	}
	return out
}

// schemaObject converts s into a schema object (map[string]any).
// Mirrors render/openapi.SchemaObject — kept local to avoid cross-package render dependency.
func schemaObject(s schema.Schema) map[string]any {
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
	if s.MinLength != nil {
		obj["minLength"] = *s.MinLength
	}
	if s.MaxLength != nil {
		obj["maxLength"] = *s.MaxLength
	}
	if s.Pattern != "" {
		obj["pattern"] = s.Pattern
	}
	if len(s.Enum) > 0 {
		obj["enum"] = s.Enum
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for name, prop := range s.Properties {
			props[name] = schemaObject(prop)
		}
		obj["properties"] = props
	}
	if len(s.Required) > 0 {
		obj["required"] = s.Required
	}
	if s.Items != nil {
		obj["items"] = schemaObject(*s.Items)
	}
	if len(s.OneOf) > 0 {
		oneOf := make([]any, len(s.OneOf))
		for i, variant := range s.OneOf {
			oneOf[i] = schemaObject(variant)
		}
		obj["oneOf"] = oneOf
	}

	return obj
}
