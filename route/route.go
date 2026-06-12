package route

import "github.com/DaniDeer/go-codex/schema"

// SecuritySchemeType identifies the type of an OpenAPI / AsyncAPI security scheme.
type SecuritySchemeType string

const (
	SecuritySchemeAPIKey        SecuritySchemeType = "apiKey"
	SecuritySchemeHTTP          SecuritySchemeType = "http"
	SecuritySchemeOAuth2        SecuritySchemeType = "oauth2"
	SecuritySchemeOpenIDConnect SecuritySchemeType = "openIdConnect"
)

// OAuthFlow describes a single OAuth 2.0 flow.
type OAuthFlow struct {
	// AuthorizationURL is required for implicit and authorizationCode flows.
	AuthorizationURL string
	// TokenURL is required for password, clientCredentials, and authorizationCode flows.
	TokenURL   string
	RefreshURL string            // optional
	Scopes     map[string]string // scope name → description
}

// OAuthFlows holds the OAuth 2.0 flow definitions for an oauth2 security scheme.
// Set only the flows that apply to the scheme.
type OAuthFlows struct {
	Implicit          *OAuthFlow
	Password          *OAuthFlow
	ClientCredentials *OAuthFlow
	AuthorizationCode *OAuthFlow
}

// SecurityScheme is a spec-only descriptor for an OpenAPI / AsyncAPI security
// scheme. It carries no runtime codec; higher-level packages (api/rest,
// api/events) embed SecurityScheme and add a Codec field for credential
// extraction and format validation.
//
// Use the named constructor helpers (BearerScheme, APIKeyScheme, etc.) rather
// than constructing SecurityScheme literals directly.
type SecurityScheme struct {
	Type        SecuritySchemeType
	Description string

	// Name is the header / query / cookie key name for apiKey schemes.
	Name string
	// In is the location for apiKey schemes: "header", "query", or "cookie".
	In string

	// Scheme is the HTTP authentication scheme: "bearer", "basic", "digest".
	Scheme string
	// BearerFormat is informational (e.g. "JWT") and only relevant for bearer schemes.
	BearerFormat string

	// Flows defines the OAuth 2.0 flows; required for oauth2 schemes.
	Flows *OAuthFlows

	// OpenIDConnectURL is the well-known discovery URL for openIdConnect schemes.
	OpenIDConnectURL string
}

// SecurityRequirement maps a scheme name to the required OAuth 2.0 scopes.
// For non-OAuth schemes the scopes slice is empty.
//
// nil Security on a Route means "inherit global security".
// An empty []SecurityRequirement means "no auth required" for that operation.
type SecurityRequirement map[string][]string

// BearerScheme returns an HTTP bearer token SecurityScheme.
// bearerFormat is informational (e.g. "JWT"); pass an empty string to omit it.
func BearerScheme(bearerFormat string) SecurityScheme {
	return SecurityScheme{
		Type:         SecuritySchemeHTTP,
		Scheme:       "bearer",
		BearerFormat: bearerFormat,
	}
}

// BasicScheme returns an HTTP basic authentication SecurityScheme.
func BasicScheme() SecurityScheme {
	return SecurityScheme{
		Type:   SecuritySchemeHTTP,
		Scheme: "basic",
	}
}

// APIKeyScheme returns an API key SecurityScheme.
// name is the header / query / cookie key. in is "header", "query", or "cookie".
func APIKeyScheme(name, in string) SecurityScheme {
	return SecurityScheme{
		Type: SecuritySchemeAPIKey,
		Name: name,
		In:   in,
	}
}

// OAuth2Scheme returns an OAuth 2.0 SecurityScheme with the given flows.
func OAuth2Scheme(flows OAuthFlows) SecurityScheme {
	return SecurityScheme{
		Type:  SecuritySchemeOAuth2,
		Flows: &flows,
	}
}

// OpenIDConnectScheme returns an OpenID Connect SecurityScheme.
// url is the well-known OpenID Connect discovery URL.
func OpenIDConnectScheme(url string) SecurityScheme {
	return SecurityScheme{
		Type:             SecuritySchemeOpenIDConnect,
		OpenIDConnectURL: url,
	}
}

// Require returns a SecurityRequirement for the named scheme with optional
// OAuth 2.0 scopes. For non-OAuth schemes pass no scopes.
//
//	route.Require("bearerAuth")                      // bearer — no scope restriction
//	route.Require("oauth2", "read:users", "admin")   // oauth2 with required scopes
func Require(scheme string, scopes ...string) SecurityRequirement {
	s := make([]string, len(scopes))
	copy(s, scopes)
	return SecurityRequirement{scheme: s}
}

// Route describes a single HTTP operation.
type Route struct {
	Method       string // GET, POST, PUT, PATCH, DELETE
	Path         string // e.g. /users/{id}
	OperationID  string
	Summary      string
	Description  string
	Tags         []string
	PathParams   []Param
	QueryParams  []Param
	CookieParams []Param
	HeaderParams []Param
	RequestBody  *Body
	Responses    []Response
	// Security, when non-nil, overrides global security for this operation.
	// An empty slice explicitly declares "no auth required" for the operation.
	// nil means "inherit global security".
	Security []SecurityRequirement
}

// Param describes a path or query parameter.
type Param struct {
	Name        string
	Description string
	Required    bool
	Schema      schema.Schema
}

// Body describes an HTTP request body.
//
// When SchemaName is non-empty, the renderer emits a $ref to
// components/schemas and registers Schema under that name automatically.
// When SchemaName is empty, Schema is inlined in the operation.
type Body struct {
	Description string
	Required    bool
	// Schema is the payload schema. Required when SchemaName is non-empty.
	Schema schema.Schema
	// SchemaName, when non-empty, emits a $ref and registers Schema in components/schemas.
	SchemaName string
	// ContentType is the primary media type for the request body. Defaults to
	// "application/json". Ignored when ContentTypes is non-empty.
	ContentType string
	// ContentTypes, when non-empty, lists all media types this body can accept.
	// The renderer emits the schema under every listed content type in the spec.
	// Takes precedence over ContentType when set.
	ContentTypes []string
}

// Response describes one HTTP response for an operation.
//
// Status is the HTTP status code as a string: "200", "201", "default", "2XX", etc.
// When SchemaName is non-empty, the renderer emits a $ref to components/schemas.
// A nil Schema with empty SchemaName produces a description-only response (e.g. 204).
type Response struct {
	Status      string // "200", "201", "default", "2XX", etc.
	Description string
	// Schema is the response body schema. Nil means no response body.
	Schema *schema.Schema
	// SchemaName, when non-empty, emits a $ref and registers Schema in components/schemas.
	SchemaName  string
	ContentType string // defaults to "application/json"; ignored when ContentTypes is non-empty
	// ContentTypes, when non-empty, lists all content types this response can produce.
	// The renderer emits the schema under every listed content type in the spec.
	// Takes precedence over ContentType when set.
	ContentTypes []string
	// Headers describes response headers emitted by this operation.
	Headers []Param
}
