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
