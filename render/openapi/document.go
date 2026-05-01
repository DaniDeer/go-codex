// document.go provides DocumentBuilder for assembling a full OpenAPI 3.1 document
// from route descriptors and named schemas.
package openapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
	"gopkg.in/yaml.v3"
)

// Info holds the metadata for an OpenAPI document.
type Info struct {
	Title       string
	Version     string
	Description string
}

// Server describes one entry in the OpenAPI servers list.
type Server struct {
	URL         string
	Description string
}

// Document is a full OpenAPI 3.1 document produced by DocumentBuilder.
// Use MarshalJSON or MarshalYAML to serialise it.
type Document struct {
	info    Info
	servers []Server
	routes  []route.Route
	schemas map[string]schema.Schema
}

// DocumentBuilder accumulates routes and named schemas, then produces a Document.
type DocumentBuilder struct {
	info    Info
	servers []Server
	routes  []route.Route
	schemas map[string]schema.Schema
}

// NewDocumentBuilder returns a builder initialised with the given Info.
func NewDocumentBuilder(info Info) *DocumentBuilder {
	return &DocumentBuilder{
		info:    info,
		schemas: make(map[string]schema.Schema),
	}
}

// AddServer appends a server to the document.
func (b *DocumentBuilder) AddServer(s Server) *DocumentBuilder {
	b.servers = append(b.servers, s)
	return b
}

// AddRoute appends a route to the document.
func (b *DocumentBuilder) AddRoute(r route.Route) *DocumentBuilder {
	b.routes = append(b.routes, r)
	return b
}

// AddSchema registers a named schema in components/schemas.
// Explicitly registered schemas take precedence over schemas inferred from routes.
func (b *DocumentBuilder) AddSchema(name string, s schema.Schema) *DocumentBuilder {
	b.schemas[name] = s
	return b
}

// Build validates the accumulated routes and produces a Document.
//
// Validation:
//   - Duplicate (method, path) pairs are rejected.
//   - Path parameter names must exactly match the {param} placeholders in the path.
//   - Path parameters are always treated as required.
func (b *DocumentBuilder) Build() (Document, error) {
	// Collect schemas from routes; explicit schemas added via AddSchema take precedence.
	schemas := make(map[string]schema.Schema)
	for _, r := range b.routes {
		if r.RequestBody != nil && r.RequestBody.SchemaName != "" {
			schemas[r.RequestBody.SchemaName] = r.RequestBody.Schema
		}
		for _, resp := range r.Responses {
			if resp.SchemaName != "" && resp.Schema != nil {
				schemas[resp.SchemaName] = *resp.Schema
			}
		}
	}
	for k, v := range b.schemas {
		schemas[k] = v // explicit wins
	}

	// Validate routes.
	seen := make(map[string]bool)
	for _, r := range b.routes {
		key := strings.ToUpper(r.Method) + " " + r.Path
		if seen[key] {
			return Document{}, fmt.Errorf("duplicate route: %s %s", r.Method, r.Path)
		}
		seen[key] = true

		if err := validatePathParams(r); err != nil {
			return Document{}, err
		}
	}

	return Document{
		info:    b.info,
		servers: b.servers,
		routes:  b.routes,
		schemas: schemas,
	}, nil
}

// MarshalJSON encodes the document as JSON bytes.
func (d Document) MarshalJSON() ([]byte, error) {
	return json.MarshalIndent(d.toMap(), "", "  ")
}

// MarshalYAML encodes the document as YAML bytes.
func (d Document) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(d.toMap())
}

// toMap converts the document to a map[string]any suitable for JSON/YAML marshaling.
func (d Document) toMap() map[string]any {
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    buildInfo(d.info),
	}

	if len(d.servers) > 0 {
		doc["servers"] = buildServers(d.servers)
	}

	paths := buildPaths(d.routes)
	if len(paths) > 0 {
		doc["paths"] = paths
	}

	if len(d.schemas) > 0 {
		doc["components"] = map[string]any{
			"schemas": ComponentsSchemas(d.schemas),
		}
	}

	return doc
}

// buildInfo produces the OpenAPI info object.
func buildInfo(info Info) map[string]any {
	m := map[string]any{
		"title":   info.Title,
		"version": info.Version,
	}
	if info.Description != "" {
		m["description"] = info.Description
	}
	return m
}

// buildServers produces the OpenAPI servers list.
func buildServers(servers []Server) []any {
	out := make([]any, len(servers))
	for i, s := range servers {
		srv := map[string]any{"url": s.URL}
		if s.Description != "" {
			srv["description"] = s.Description
		}
		out[i] = srv
	}
	return out
}

// buildPaths groups routes by path and produces the OpenAPI paths object.
func buildPaths(routes []route.Route) map[string]any {
	paths := map[string]any{}
	for _, r := range routes {
		pathItem, ok := paths[r.Path].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
		}
		pathItem[strings.ToLower(r.Method)] = buildOperation(r)
		paths[r.Path] = pathItem
	}
	return paths
}

// buildOperation converts a Route into an OpenAPI operation object.
func buildOperation(r route.Route) map[string]any {
	op := map[string]any{}

	if r.OperationID != "" {
		op["operationId"] = r.OperationID
	}
	if r.Summary != "" {
		op["summary"] = r.Summary
	}
	if r.Description != "" {
		op["description"] = r.Description
	}
	if len(r.Tags) > 0 {
		op["tags"] = r.Tags
	}

	var params []any
	params = append(params, buildParams(r.PathParams, "path")...)
	params = append(params, buildParams(r.QueryParams, "query")...)
	if len(params) > 0 {
		op["parameters"] = params
	}

	if rb := buildRequestBody(r.RequestBody); rb != nil {
		op["requestBody"] = rb
	}

	if len(r.Responses) > 0 {
		op["responses"] = buildResponses(r.Responses)
	}

	return op
}

// buildParams converts a slice of Params into OpenAPI parameter objects.
func buildParams(params []route.Param, in string) []any {
	out := make([]any, len(params))
	for i, p := range params {
		param := map[string]any{
			"name": p.Name,
			"in":   in,
		}
		// Path parameters are always required per OpenAPI 3.1 spec.
		if in == "path" || p.Required {
			param["required"] = true
		}
		if p.Description != "" {
			param["description"] = p.Description
		}
		param["schema"] = SchemaObject(p.Schema)
		out[i] = param
	}
	return out
}

// buildRequestBody converts a Body into an OpenAPI requestBody object.
func buildRequestBody(body *route.Body) map[string]any {
	if body == nil {
		return nil
	}
	ct := body.ContentType
	if ct == "" {
		ct = "application/json"
	}
	rb := map[string]any{
		"content": map[string]any{
			ct: map[string]any{
				"schema": schemaRef(body.Schema, body.SchemaName),
			},
		},
	}
	if body.Required {
		rb["required"] = true
	}
	if body.Description != "" {
		rb["description"] = body.Description
	}
	return rb
}

// buildResponses converts a slice of Responses into an OpenAPI responses object.
func buildResponses(responses []route.Response) map[string]any {
	result := map[string]any{}
	for _, r := range responses {
		resp := map[string]any{
			"description": r.Description,
		}
		if r.Schema != nil {
			ct := r.ContentType
			if ct == "" {
				ct = "application/json"
			}
			resp["content"] = map[string]any{
				ct: map[string]any{
					"schema": schemaRef(*r.Schema, r.SchemaName),
				},
			}
		}
		result[r.Status] = resp
	}
	return result
}

// schemaRef returns a $ref object when name is non-empty, otherwise inlines the schema.
func schemaRef(s schema.Schema, name string) map[string]any {
	if name != "" {
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	return SchemaObject(s)
}

// pathParamRE matches {paramName} placeholders in a path template.
var pathParamRE = regexp.MustCompile(`\{([^}]+)\}`)

// validatePathParams checks that PathParams names exactly match the
// path template placeholders and reports any mismatch.
func validatePathParams(r route.Route) error {
	matches := pathParamRE.FindAllStringSubmatch(r.Path, -1)
	placeholders := make(map[string]bool, len(matches))
	for _, m := range matches {
		placeholders[m[1]] = true
	}

	declared := make(map[string]bool, len(r.PathParams))
	for _, p := range r.PathParams {
		declared[p.Name] = true
	}

	for name := range placeholders {
		if !declared[name] {
			return fmt.Errorf("route %s %s: path placeholder {%s} has no matching PathParam",
				r.Method, r.Path, name)
		}
	}
	for name := range declared {
		if !placeholders[name] {
			return fmt.Errorf("route %s %s: PathParam %q has no matching placeholder in path",
				r.Method, r.Path, name)
		}
	}
	return nil
}
