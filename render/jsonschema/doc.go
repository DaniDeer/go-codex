// Package jsonschema renders [schema.Schema] values to plain JSON Schema
// compatible [json.RawMessage].
//
// Unlike [render/openapi] and [render/asyncapi], this package produces
// stand-alone JSON Schema objects without any OpenAPI or AsyncAPI envelope.
// The output is suitable for use as MCP tool input/output schemas or any
// context that requires raw JSON Schema rather than a full API document.
//
// # Usage
//
// Call [Schema] with any [schema.Schema] value to obtain the JSON Schema bytes.
// Returns nil for a zero-value schema (no type, no properties):
//
//	raw, err := jsonschema.Schema(myCodec.Schema)
//	// raw is json.RawMessage ready to pass to an MCP tool's inputSchema field.
//
// This package is used internally by [api/mcp] to convert codec schemas to
// the MCP tool manifest format. It wraps [render/internal/schemarender.SchemaObject]
// and marshals the result to JSON.
package jsonschema
