package handlers

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"

	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// VerifyOAuth2Scopes is the SHARED, transport-agnostic OAuth2 verification
// logic — simulates a token-introspection call (mirrors
// examples/rest-api/handlers/security.go's ExtractScopes simulation
// style, which does the same for REST's own bearer/scope model). Returns
// the granted scopes for a valid token, or an error for an invalid one.
//
// This function is deliberately the ONLY place OAuth2 verification logic
// lives — go-codex's declarative security mechanism lets the SAME
// middleware.Middleware value (built from ONE middleware.SecurityScheme
// declaration) be attached via .Use() to a REST route, an events channel,
// or a reqreply route interchangeably (see docs/features/security.md's
// "Sharing a security scheme declaration across REST/events/reqreply"
// section) — but the PAIRED implementation Fn attached via HandleMW/
// ClientMW/SubscribeMW cannot be shared literally, since each transport's
// Fn shape differs (REST gets *http.Request, mqtt5 gets *pahomqtt5.
// Publish, zeromq reads/writes the decoded *Req directly). The
// RECOMMENDED pattern demonstrated here: factor the actual verification
// logic into ONE shared helper like this one, then write a THIN,
// transport-specific wrapper Fn that only differs in HOW it extracts the
// raw token from its own transport (see demo_cross_api_oauth2_sharing.go's
// zeromqOAuthSecurityFn for the zeromq-side wrapper).
func VerifyOAuth2Scopes(token string) (map[string][]string, error) {
	switch token {
	case "valid-compute-write-token":
		return map[string][]string{"oauth2Compute": {routes.OAuthComputeWriteScope}}, nil
	case "":
		return nil, fmt.Errorf("oauth2: missing token")
	default:
		return nil, fmt.Errorf("oauth2: token %q not recognized", token)
	}
}

// VerifyOAuthComputeZeroMQ is the THIN, zeromq-shaped paired security Fn —
// attached via reqreply.Route.HandleMW(&routes.OAuthMw, VerifyOAuthComputeZeroMQ).
// It ONLY extracts the raw token from zeromq's own transport shape (the
// decoded *OAuthComputeReq's Token field — zeromq has no raw-message side
// channel, unlike mqtt5's User Properties) and delegates the REAL
// verification work to the SHARED VerifyOAuth2Scopes helper above —
// mirrors handlers/security.go's VerifyBearer (mqtt5-shaped) in spirit,
// but demonstrates the shared-helper factoring VerifyBearer's own
// (deliberately simple, unconditional-grant) demo didn't need.
func VerifyOAuthComputeZeroMQ(_ context.Context, req *routes.OAuthComputeReq, _ []route.SecurityRequirement) error {
	scopes, err := VerifyOAuth2Scopes(req.Token)
	if err != nil {
		return err
	}
	return middleware.CheckScopes([]route.SecurityRequirement{{"oauth2Compute": {routes.OAuthComputeWriteScope}}}, scopes)
}
