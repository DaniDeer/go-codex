// Package v3 renders schema.Schema values and route.SecurityScheme definitions
// as an AsyncAPI 3.0 document.
//
// It is the 3.0 counterpart to render/asyncapi/v2 (which remains frozen at 2.6).
// The structural changes from 2.6 to 3.0 include:
//
//   - Version string changes to "3.0.0".
//   - Channels and operations are separated: channels describe topics and their
//     messages; a top-level operations map links operations to channels via $ref.
//   - Channel keys are logical identifiers; the actual topic address is in
//     ChannelItem.Address.
//   - Subscribe/publish are replaced by action: "receive" / "send".
//   - Per-operation security is supported in addition to server-level security.
//
// Typical usage:
//
//	doc, err := v3.NewDocumentBuilder(v3.Info{
//	    Title:   "User Events",
//	    Version: "1.0.0",
//	}).
//	    AddServer("production", v3.Server{
//	        URL:      "broker.example.com",
//	        Protocol: "mqtt",
//	    }).
//	    AddSecurityScheme("bearerAuth", route.BearerScheme("JWT")).
//	    AddChannel("userCreated", v3.ChannelItem{
//	        Address: "user/created",
//	        Subscribe: &v3.Operation{
//	            Summary:  "User created event",
//	            Security: []route.SecurityRequirement{route.Require("bearerAuth")},
//	            Message:  v3.Message{Schema: UserCodec.Schema, SchemaName: "User"},
//	        },
//	    }).
//	    Build()
//
//	yamlBytes, err := doc.MarshalYAML()
package v3

import (
	"github.com/DaniDeer/go-codex/render/internal/schemarender"
	"github.com/DaniDeer/go-codex/route"
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

// buildSecurityRequirements converts a slice of SecurityRequirement to the
// AsyncAPI 3.0 security array format: [{schemeName: [scope, ...]}, ...].
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

// buildSecuritySchemes converts the named security schemes into the AsyncAPI
// components/securitySchemes object.
func buildSecuritySchemes(schemes map[string]route.SecurityScheme) map[string]any {
	out := make(map[string]any, len(schemes))
	for name, s := range schemes {
		out[name] = buildSecurityScheme(s)
	}
	return out
}

// buildSecurityScheme converts a route.SecurityScheme to its AsyncAPI 3.0 representation.
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

// buildOAuthFlows converts OAuthFlows to the AsyncAPI 3.0 flows object.
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

// buildOAuthFlow converts an OAuthFlow to its AsyncAPI representation.
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
