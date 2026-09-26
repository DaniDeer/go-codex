package rest_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

func staticExtractor(values map[string]map[string]string) rest.CredentialExtractor {
	return func(location, name string) string {
		return values[location][name]
	}
}

func TestValidateSecurityCredentials_bearerHappyPath(t *testing.T) {
	// Captures the credential value ValidateSecurityCredentials actually
	// hands to the codec, so this test verifies the "Bearer " prefix is
	// stripped by extractCredential -- not merely that a non-empty string
	// passes validation.
	var gotCred string
	captureCodec := codex.String().Refine(codex.Constraint[string]{
		Name:  "capture",
		Check: func(s string) bool { gotCred = s; return true },
		Message: func(s string) string {
			return "unreachable"
		},
	})
	schemes := map[string]rest.SecurityScheme{
		"bearer": rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(captureCodec),
	}
	reqs := []route.SecurityRequirement{{"bearer": nil}}
	extract := staticExtractor(map[string]map[string]string{
		"header": {"Authorization": "Bearer abc123"},
	})
	if err := rest.ValidateSecurityCredentials(extract, reqs, schemes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCred != "abc123" {
		t.Fatalf("expected \"Bearer \" prefix to be stripped, got credential %q", gotCred)
	}
}

func TestValidateSecurityCredentials_apiKeyQuery(t *testing.T) {
	schemes := map[string]rest.SecurityScheme{
		"apiKey": rest.SecurityScheme{SecurityScheme: route.APIKeyScheme("key", "query")}.WithCodec(codex.String()),
	}
	reqs := []route.SecurityRequirement{{"apiKey": nil}}
	extract := staticExtractor(map[string]map[string]string{
		"query": {"key": "s3cr3t"},
	})
	if err := rest.ValidateSecurityCredentials(extract, reqs, schemes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSecurityCredentials_apiKeyCookie(t *testing.T) {
	schemes := map[string]rest.SecurityScheme{
		"apiKey": rest.SecurityScheme{SecurityScheme: route.APIKeyScheme("session", "cookie")}.WithCodec(codex.String()),
	}
	reqs := []route.SecurityRequirement{{"apiKey": nil}}
	extract := staticExtractor(map[string]map[string]string{
		"cookie": {"session": "cookieval"},
	})
	if err := rest.ValidateSecurityCredentials(extract, reqs, schemes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSecurityCredentials_codecFailure(t *testing.T) {
	strictCodec := codex.String().Refine(codex.Constraint[string]{
		Name:  "non-empty",
		Check: func(s string) bool { return s != "" },
		Message: func(s string) string {
			return "must not be empty"
		},
	})
	schemes := map[string]rest.SecurityScheme{
		"bearer": rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(strictCodec),
	}
	reqs := []route.SecurityRequirement{{"bearer": nil}}
	extract := staticExtractor(nil) // no Authorization header present -> empty credential
	err := rest.ValidateSecurityCredentials(extract, reqs, schemes)
	var credErr rest.SecurityCredentialError
	if !errors.As(err, &credErr) || credErr.Scheme != "bearer" {
		t.Fatalf("expected SecurityCredentialError, got %v", err)
	}
}

func TestValidateSecurityCredentials_noCodecSkipsValidation(t *testing.T) {
	schemes := map[string]rest.SecurityScheme{
		"bearer": {SecurityScheme: route.BearerScheme("JWT")}, // no Codec set
	}
	reqs := []route.SecurityRequirement{{"bearer": nil}}
	if err := rest.ValidateSecurityCredentials(staticExtractor(nil), reqs, schemes); err != nil {
		t.Fatalf("expected nil error when scheme has no Codec, got %v", err)
	}
}

func TestValidateSecurityCredentials_unknownSchemeSkipped(t *testing.T) {
	reqs := []route.SecurityRequirement{{"missing": nil}}
	if err := rest.ValidateSecurityCredentials(staticExtractor(nil), reqs, map[string]rest.SecurityScheme{}); err != nil {
		t.Fatalf("expected nil error for unknown scheme, got %v", err)
	}
}
