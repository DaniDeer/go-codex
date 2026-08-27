package route_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/route"
)

func TestBearerScheme(t *testing.T) {
	s := route.BearerScheme("JWT")
	if s.Type != route.SecuritySchemeHTTP {
		t.Errorf("want Type=SecuritySchemeHTTP, got %v", s.Type)
	}
	if s.Scheme != "bearer" {
		t.Errorf("want Scheme=bearer, got %q", s.Scheme)
	}
	if s.BearerFormat != "JWT" {
		t.Errorf("want BearerFormat=JWT, got %q", s.BearerFormat)
	}
}

func TestBearerScheme_emptyFormat(t *testing.T) {
	s := route.BearerScheme("")
	if s.BearerFormat != "" {
		t.Errorf("want BearerFormat empty, got %q", s.BearerFormat)
	}
	if s.Scheme != "bearer" {
		t.Errorf("want Scheme=bearer, got %q", s.Scheme)
	}
}

func TestBasicScheme(t *testing.T) {
	s := route.BasicScheme()
	if s.Type != route.SecuritySchemeHTTP {
		t.Errorf("want Type=SecuritySchemeHTTP, got %v", s.Type)
	}
	if s.Scheme != "basic" {
		t.Errorf("want Scheme=basic, got %q", s.Scheme)
	}
}

func TestAPIKeyScheme(t *testing.T) {
	s := route.APIKeyScheme("X-API-Key", "header")
	if s.Type != route.SecuritySchemeAPIKey {
		t.Errorf("want Type=SecuritySchemeAPIKey, got %v", s.Type)
	}
	if s.Name != "X-API-Key" {
		t.Errorf("want Name=X-API-Key, got %q", s.Name)
	}
	if s.In != "header" {
		t.Errorf("want In=header, got %q", s.In)
	}
}

func TestOAuth2Scheme(t *testing.T) {
	flows := route.OAuthFlows{
		ClientCredentials: &route.OAuthFlow{
			TokenURL: "https://auth.example.com/token",
			Scopes:   map[string]string{"read": "Read access"},
		},
	}
	s := route.OAuth2Scheme(flows)
	if s.Type != route.SecuritySchemeOAuth2 {
		t.Errorf("want Type=SecuritySchemeOAuth2, got %v", s.Type)
	}
	if s.Flows == nil {
		t.Fatal("want Flows non-nil")
	}
	if s.Flows.ClientCredentials == nil {
		t.Fatal("want ClientCredentials non-nil")
	}
	if s.Flows.ClientCredentials.TokenURL != "https://auth.example.com/token" {
		t.Errorf("want TokenURL, got %q", s.Flows.ClientCredentials.TokenURL)
	}
}

func TestOpenIDConnectScheme(t *testing.T) {
	url := "https://accounts.google.com/.well-known/openid-configuration"
	s := route.OpenIDConnectScheme(url)
	if s.Type != route.SecuritySchemeOpenIDConnect {
		t.Errorf("want Type=SecuritySchemeOpenIDConnect, got %v", s.Type)
	}
	if s.OpenIDConnectURL != url {
		t.Errorf("want OpenIDConnectURL=%q, got %q", url, s.OpenIDConnectURL)
	}
}

func TestRequire_noScopes(t *testing.T) {
	req := route.Require("bearerAuth")
	scopes, ok := req["bearerAuth"]
	if !ok {
		t.Fatal("want key 'bearerAuth' in requirement")
	}
	if len(scopes) != 0 {
		t.Errorf("want empty scopes, got %v", scopes)
	}
}

func TestRequire_withScopes(t *testing.T) {
	req := route.Require("oauth2", "read:users", "admin")
	scopes, ok := req["oauth2"]
	if !ok {
		t.Fatal("want key 'oauth2' in requirement")
	}
	if len(scopes) != 2 {
		t.Fatalf("want 2 scopes, got %v", scopes)
	}
	if scopes[0] != "read:users" || scopes[1] != "admin" {
		t.Errorf("want scopes [read:users admin], got %v", scopes)
	}
}

func TestSecuritySchemeTypes(t *testing.T) {
	if route.SecuritySchemeAPIKey != "apiKey" {
		t.Errorf("SecuritySchemeAPIKey = %q, want 'apiKey'", route.SecuritySchemeAPIKey)
	}
	if route.SecuritySchemeHTTP != "http" {
		t.Errorf("SecuritySchemeHTTP = %q, want 'http'", route.SecuritySchemeHTTP)
	}
	if route.SecuritySchemeOAuth2 != "oauth2" {
		t.Errorf("SecuritySchemeOAuth2 = %q, want 'oauth2'", route.SecuritySchemeOAuth2)
	}
	if route.SecuritySchemeOpenIDConnect != "openIdConnect" {
		t.Errorf("SecuritySchemeOpenIDConnect = %q, want 'openIdConnect'", route.SecuritySchemeOpenIDConnect)
	}
}

func TestSatisfied_emptyReqsAlwaysTrue(t *testing.T) {
	if !route.Satisfied(nil, nil) {
		t.Error("want empty reqs to always be satisfied")
	}
	if !route.Satisfied([]route.SecurityRequirement{}, map[string][]string{}) {
		t.Error("want empty reqs to always be satisfied, even with empty granted")
	}
}

func TestSatisfied_ANDWithinOneRequirement(t *testing.T) {
	reqs := []route.SecurityRequirement{
		{"bearerAuth": nil, "apiKey": nil},
	}
	// Only bearerAuth granted — apiKey missing — should fail (AND).
	if route.Satisfied(reqs, map[string][]string{"bearerAuth": nil}) {
		t.Error("want AND-combined requirement to fail when one scheme is missing")
	}
	// Both granted — should pass.
	if !route.Satisfied(reqs, map[string][]string{"bearerAuth": nil, "apiKey": nil}) {
		t.Error("want AND-combined requirement to pass when both schemes are granted")
	}
}

func TestSatisfied_ORAcrossRequirements(t *testing.T) {
	reqs := []route.SecurityRequirement{
		{"bearerAuth": nil},
		{"apiKey": nil},
	}
	if !route.Satisfied(reqs, map[string][]string{"apiKey": nil}) {
		t.Error("want OR-across-requirements to pass when only the second alternative is granted")
	}
	if route.Satisfied(reqs, map[string][]string{"unrelated": nil}) {
		t.Error("want OR-across-requirements to fail when neither alternative is granted")
	}
}

func TestSatisfied_emptyScopesMeansAuthenticatedNoRestriction(t *testing.T) {
	reqs := []route.SecurityRequirement{
		{"bearerAuth": nil}, // no scopes required
	}
	if !route.Satisfied(reqs, map[string][]string{"bearerAuth": nil}) {
		t.Error("want a granted scheme with nil scopes to satisfy a no-scope requirement")
	}
	if !route.Satisfied(reqs, map[string][]string{"bearerAuth": {}}) {
		t.Error("want a granted scheme with empty scopes to satisfy a no-scope requirement")
	}
}

func TestSatisfied_scopeSubsetRequired(t *testing.T) {
	reqs := []route.SecurityRequirement{
		{"oauth2": {"read:users", "admin"}},
	}
	if route.Satisfied(reqs, map[string][]string{"oauth2": {"read:users"}}) {
		t.Error("want a partial scope grant to fail")
	}
	if !route.Satisfied(reqs, map[string][]string{"oauth2": {"read:users", "admin", "extra"}}) {
		t.Error("want a superset scope grant to pass")
	}
}

func TestSatisfied_missingSchemeFails(t *testing.T) {
	reqs := []route.SecurityRequirement{
		{"oauth2": {"read:users"}},
	}
	if route.Satisfied(reqs, map[string][]string{}) {
		t.Error("want an ungranted scheme to fail")
	}
}
