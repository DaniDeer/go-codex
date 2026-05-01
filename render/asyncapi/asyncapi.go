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

import (
	"github.com/DaniDeer/go-codex/render/internal/schemarender"
	"github.com/DaniDeer/go-codex/schema"
)

// schemaRef returns a $ref object when name is non-empty, otherwise inlines the schema.
func schemaRef(s schema.Schema, name string) map[string]any {
	if name != "" {
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return schemarender.SchemaObject(s)
}

// buildComponentsSchemas renders named schemas as component schema objects.
func buildComponentsSchemas(named map[string]schema.Schema) map[string]any {
	out := make(map[string]any, len(named))
	for name, s := range named {
		out[name] = schemarender.SchemaObject(s)
	}
	return out
}
