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
	info            Info
	servers         []Server
	routes          []route.Route
	schemas         map[string]schema.Schema
	securitySchemes map[string]route.SecurityScheme
	globalSecurity  []route.SecurityRequirement
}

// DocumentBuilder accumulates routes and named schemas, then produces a Document.
type DocumentBuilder struct {
	info            Info
	servers         []Server
	routes          []route.Route
	schemas         map[string]schema.Schema
	securitySchemes map[string]route.SecurityScheme
	globalSecurity  []route.SecurityRequirement
}

// NewDocumentBuilder returns a builder initialised with the given Info.
func NewDocumentBuilder(info Info) *DocumentBuilder {
	return &DocumentBuilder{
		info:            info,
		schemas:         make(map[string]schema.Schema),
		securitySchemes: make(map[string]route.SecurityScheme),
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

// AddSecurityScheme registers a named security scheme in components/securitySchemes.
// The name must match those used in SecurityRequirement maps on routes and
// in AddGlobalSecurity calls.
func (b *DocumentBuilder) AddSecurityScheme(name string, s route.SecurityScheme) *DocumentBuilder {
	b.securitySchemes[name] = s
	return b
}

// AddGlobalSecurity appends security requirements that apply to all operations
// by default. Per-route Security on a route.Route overrides the global list for
// that operation; an empty per-route slice marks the operation as unsecured.
func (b *DocumentBuilder) AddGlobalSecurity(reqs ...route.SecurityRequirement) *DocumentBuilder {
	b.globalSecurity = append(b.globalSecurity, reqs...)
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
		info:            b.info,
		servers:         b.servers,
		routes:          b.routes,
		schemas:         schemas,
		securitySchemes: b.securitySchemes,
		globalSecurity:  b.globalSecurity,
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

	if len(d.globalSecurity) > 0 {
		doc["security"] = buildSecurityRequirements(d.globalSecurity)
	}

	components := map[string]any{}
	if len(d.schemas) > 0 {
		components["schemas"] = ComponentsSchemas(d.schemas)
	}
	if len(d.securitySchemes) > 0 {
		components["securitySchemes"] = buildSecuritySchemes(d.securitySchemes)
	}
	if len(components) > 0 {
		doc["components"] = components
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
	params = append(params, buildParams(r.CookieParams, "cookie")...)
	params = append(params, buildParams(r.HeaderParams, "header")...)
	if len(params) > 0 {
		op["parameters"] = params
	}

	if rb := buildRequestBody(r.RequestBody); rb != nil {
		op["requestBody"] = rb
	}

	if len(r.Responses) > 0 {
		op["responses"] = buildResponses(r.Responses)
	}

	// nil Security = inherit global; non-nil (including empty) overrides global.
	if r.Security != nil {
		op["security"] = buildSecurityRequirements(r.Security)
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
	contentTypes := body.ContentTypes
	if len(contentTypes) == 0 {
		ct := body.ContentType
		if ct == "" {
			ct = "application/json"
		}
		contentTypes = []string{ct}
	}
	schemaObj := schemaRef(body.Schema, body.SchemaName)
	content := make(map[string]any, len(contentTypes))
	for _, ct := range contentTypes {
		content[ct] = map[string]any{"schema": schemaObj}
	}
	rb := map[string]any{"content": content}
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
			contentTypes := r.ContentTypes
			if len(contentTypes) == 0 {
				ct := r.ContentType
				if ct == "" {
					ct = "application/json"
				}
				contentTypes = []string{ct}
			}
			schemaObj := schemaRef(*r.Schema, r.SchemaName)
			content := make(map[string]any, len(contentTypes))
			for _, ct := range contentTypes {
				content[ct] = map[string]any{"schema": schemaObj}
			}
			resp["content"] = content
		}
		if len(r.Headers) > 0 {
			headers := map[string]any{}
			for _, h := range r.Headers {
				hdr := map[string]any{
					"schema": SchemaObject(h.Schema),
				}
				if h.Description != "" {
					hdr["description"] = h.Description
				}
				if h.Required {
					hdr["required"] = true
				}
				headers[h.Name] = hdr
			}
			resp["headers"] = headers
		}
		result[r.Status] = resp
	}
	return result
}

// buildSecurityRequirements converts a slice of SecurityRequirement into the
// OpenAPI security array format: [{schemeName: [scope, ...]}, ...].
func buildSecurityRequirements(reqs []route.SecurityRequirement) []any {
	out := make([]any, len(reqs))
	for i, req := range reqs {
		m := make(map[string]any, len(req))
		for name, scopes := range req {
			if scopes == nil {
				scopes = []string{}
			}
			m[name] = scopes
		}
		out[i] = m
	}
	return out
}

// buildSecuritySchemes converts the named security schemes into the OpenAPI
// components/securitySchemes object.
func buildSecuritySchemes(schemes map[string]route.SecurityScheme) map[string]any {
	out := make(map[string]any, len(schemes))
	for name, s := range schemes {
		out[name] = buildSecurityScheme(s)
	}
	return out
}

// buildSecurityScheme converts a route.SecurityScheme to its OpenAPI representation.
func buildSecurityScheme(s route.SecurityScheme) map[string]any {
	m := map[string]any{"type": string(s.Type)}
	if s.Description != "" {
		m["description"] = s.Description
	}
	switch s.Type {
	case route.SecuritySchemeAPIKey:
		m["name"] = s.Name
		m["in"] = s.In
	case route.SecuritySchemeHTTP:
		m["scheme"] = s.Scheme
		if s.BearerFormat != "" {
			m["bearerFormat"] = s.BearerFormat
		}
	case route.SecuritySchemeOAuth2:
		if s.Flows != nil {
			m["flows"] = buildOAuthFlows(s.Flows)
		}
	case route.SecuritySchemeOpenIDConnect:
		m["openIdConnectUrl"] = s.OpenIDConnectURL
	}
	return m
}

// buildOAuthFlows converts OAuthFlows to the OpenAPI flows object.
func buildOAuthFlows(flows *route.OAuthFlows) map[string]any {
	m := map[string]any{}
	if flows.Implicit != nil {
		m["implicit"] = buildOAuthFlow(flows.Implicit)
	}
	if flows.Password != nil {
		m["password"] = buildOAuthFlow(flows.Password)
	}
	if flows.ClientCredentials != nil {
		m["clientCredentials"] = buildOAuthFlow(flows.ClientCredentials)
	}
	if flows.AuthorizationCode != nil {
		m["authorizationCode"] = buildOAuthFlow(flows.AuthorizationCode)
	}
	return m
}

// buildOAuthFlow converts an OAuthFlow to its OpenAPI representation.
func buildOAuthFlow(f *route.OAuthFlow) map[string]any {
	m := map[string]any{}
	if f.AuthorizationURL != "" {
		m["authorizationUrl"] = f.AuthorizationURL
	}
	if f.TokenURL != "" {
		m["tokenUrl"] = f.TokenURL
	}
	if f.RefreshURL != "" {
		m["refreshUrl"] = f.RefreshURL
	}
	scopes := make(map[string]any, len(f.Scopes))
	for k, v := range f.Scopes {
		scopes[k] = v
	}
	m["scopes"] = scopes
	return m
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
