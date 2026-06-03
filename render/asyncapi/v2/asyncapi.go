// Package v2 renders schema.Schema values as an AsyncAPI 2.6 document.
//
// It imports only the schema package — no codec logic is involved. The same
// schema.Schema that drives OpenAPI output can describe AsyncAPI message payloads.
//
// Use [render/asyncapi/v3] for AsyncAPI 3.0 documents (per-operation security,
// separate channels/operations map, channel address field).
//
// Typical usage:
//
//	doc, err := v2.NewDocumentBuilder(v2.Info{
//	    Title:   "User Events",
//	    Version: "1.0.0",
//	}).
//	    AddChannel("user/created", v2.ChannelItem{
//	        Subscribe: &v2.Operation{
//	            Summary: "User created event",
//	            Message: v2.Message{
//	                Schema:     UserCodec.Schema,
//	                SchemaName: "User",
//	            },
//	        },
//	    }).
//	    Build()
//
//	yamlBytes, err := doc.MarshalYAML()
package v2

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
