package rest

import (
	"strings"

	"github.com/DaniDeer/go-codex/route"
)

// CredentialExtractor supplies a raw credential string from wherever an
// adapter's concrete request type stores it — location is one of
// "header", "query", or "cookie"; name is the header/query-param/cookie
// name to read. Returns "" when the value is absent, mirroring
// [ValidateSecurityCredentials]'s pre-refactor `*http.Request`-based
// behavior exactly. An adapter builds this closure from its own request
// object (e.g. `r.Header.Get`/`r.URL.Query().Get`/`r.Cookie`) — this
// abstraction is what lets [ValidateSecurityCredentials] live in `api/rest`
// with ZERO `net/http` import.
type CredentialExtractor func(location, name string) string

// ValidateSecurityCredentials extracts credentials via extract and
// validates them against the registered SecurityScheme codecs for the
// declared requirements. Returns a [SecurityCredentialError] if any codec
// check fails.
func ValidateSecurityCredentials(extract CredentialExtractor, reqs []route.SecurityRequirement, schemes map[string]SecurityScheme) error {
	for _, req := range reqs {
		for name := range req {
			s, ok := schemes[name]
			if !ok || s.Codec == nil {
				continue
			}
			cred := extractCredential(extract, s)
			if err := s.Codec.Validate(cred); err != nil {
				return SecurityCredentialError{Scheme: name, Err: err}
			}
		}
	}
	return nil
}

// extractCredential returns the raw credential string via extract, based
// on the scheme's type and location.
func extractCredential(extract CredentialExtractor, s SecurityScheme) string {
	switch s.Type {
	case route.SecuritySchemeHTTP:
		auth := extract("header", "Authorization")
		switch strings.ToLower(s.Scheme) {
		case "bearer":
			// RFC 7235 §2.1: scheme names are case-insensitive.
			if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
				return auth[7:]
			}
			return auth
		case "basic":
			if len(auth) >= 6 && strings.EqualFold(auth[:6], "Basic ") {
				return auth[6:]
			}
			return auth
		}
		return auth
	case route.SecuritySchemeOAuth2, route.SecuritySchemeOpenIDConnect:
		auth := extract("header", "Authorization")
		if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
			return auth[7:]
		}
		return auth
	case route.SecuritySchemeAPIKey:
		switch strings.ToLower(s.In) {
		case "header":
			return extract("header", s.Name)
		case "query":
			return extract("query", s.Name)
		case "cookie":
			return extract("cookie", s.Name)
		}
	}
	return ""
}
