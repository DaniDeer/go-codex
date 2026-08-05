package mqtt5

import (
	"strings"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file holds the built-in codec-based credential extraction/validation
// helpers shared by BOTH the events pub/sub path (adapter.go's
// Subscribe/Publish) and the reqreply request/reply path (reqreply.go's
// Serve/Call) — mirroring [adapters/nethttp]'s validateSecurityCredentials/
// extractCredential/firstScheme, swapped from HTTP headers/query/cookie to
// MQTT 5 User Properties.
//
// [events.SecurityScheme] and [reqreply.SecurityScheme] are structurally
// identical (embed [route.SecurityScheme] + an optional *codex.Codec[string])
// but are distinct Go types, so validateSecurityCredentials takes the scheme
// metadata and codecs as two separate flattened maps rather than either
// package's typed SecurityScheme map directly — each call site (adapter.go,
// reqreply.go) builds these two maps from its own typed
// map[string]events.SecurityScheme / map[string]reqreply.SecurityScheme once,
// then calls the shared helper below.

// extractUserPropertyCredential returns the raw credential string from props
// based on s's scheme type — the MQTT 5 User Property analogue of
// [adapters/nethttp]'s extractCredential:
//
//   - http (bearer/basic), oauth2, openIdConnect: reads the "Authorization"
//     User Property; strips a leading "Bearer "/"Basic " prefix
//     (case-insensitive) matching the scheme, same as the HTTP Authorization
//     header convention.
//   - apiKey: reads the User Property named s.Name. s.In has no runtime
//     effect here (unlike REST's header/query/cookie split) — MQTT has
//     exactly one credential "location", the User Property list.
func extractUserPropertyCredential(props pahomqtt5.UserProperties, s route.SecurityScheme) string {
	switch s.Type {
	case route.SecuritySchemeHTTP:
		auth := props.Get("Authorization")
		switch strings.ToLower(s.Scheme) {
		case "bearer":
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
		auth := props.Get("Authorization")
		if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
			return auth[7:]
		}
		return auth
	case route.SecuritySchemeAPIKey:
		return props.Get(s.Name)
	}
	return ""
}

// validateSecurityCredentials extracts credentials from props and validates
// them against schemeCodecs for the declared reqs — the built-in check that
// runs BEFORE the optional custom SecurityFunc (server) / before the actual
// publish/call (client), mirroring [adapters/nethttp]'s
// validateSecurityCredentials exactly. Returns the failing scheme's name and
// its constraint error, or ("", nil) on success — including when a scheme in
// reqs has no entry in schemeCodecs, or a nil Codec (matching REST's
// "nil Codec means no format validation" contract).
func validateSecurityCredentials(
	props pahomqtt5.UserProperties,
	reqs []route.SecurityRequirement,
	schemeTypes map[string]route.SecurityScheme,
	schemeCodecs map[string]*codex.Codec[string],
) (string, error) {
	for _, req := range reqs {
		for name := range req {
			s, ok := schemeTypes[name]
			if !ok {
				continue
			}
			c, ok := schemeCodecs[name]
			if !ok || c == nil {
				continue
			}
			cred := extractUserPropertyCredential(props, s)
			if err := c.Validate(cred); err != nil {
				return name, err
			}
		}
	}
	return "", nil
}
