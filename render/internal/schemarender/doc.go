// Package schemarender converts [schema.Schema] values to [map[string]any] objects
// suitable for marshalling into OpenAPI or AsyncAPI documents.
//
// Both [render/openapi] and [render/asyncapi] (v2 and v3) delegate to this package
// so that adding a new [schema.Schema] field requires only one change here —
// both spec renderers pick it up automatically.
//
// This package is internal to the render/ subtree and is not part of the public API.
// Do not import it from outside the render/ packages.
//
// # SchemaObject
//
// [SchemaObject] is the single conversion function. It maps every field of
// [schema.Schema] — type, properties, constraints, $ref, oneOf, discriminator,
// additionalProperties, title, description, example, deprecated, default — to
// the corresponding JSON Schema / OpenAPI representation.
//
// [AdditionalPropertiesSchema] takes precedence over [AdditionalProperties *bool]
// when both are set; the renderer checks the pointer field first.
package schemarender
