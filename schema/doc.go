// Package schema defines the pure data model for describing value shapes.
//
// A [Schema] value carries the JSON/YAML/OpenAPI/AsyncAPI description of a
// Go type: its JSON Schema type, properties, constraints (minimum, maximum,
// minLength, pattern, enum, …), description, title, example, and deprecation
// flag.
//
// The schema package has zero dependencies inside the module — it is imported
// by codex (to annotate codecs), by render/openapi and render/asyncapi (to
// emit specs), and by validate (to annotate constraints). Nothing in schema
// knows about codecs, renderers, or adapters.
//
// Typical usage:
//
//	// Schema values are produced by codecs — you rarely construct them directly.
//	var s schema.Schema = codex.String().
//	    Refine(validate.Email).
//	    WithDescription("Primary email address.").
//	    Schema
//	// s.Type == "string", s.Format == "email", s.Description == "Primary email address."
//
// Renderers such as [render/openapi] and [render/asyncapi/v3] accept
// map[string]Schema and emit the corresponding spec document.
package schema
