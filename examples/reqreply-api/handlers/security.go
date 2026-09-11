package handlers

import (
	"context"
	"strings"

	"github.com/DaniDeer/go-codex/route"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// VerifyBearer is the mqtt5-shaped PAIRED security implementation Fn —
// attached via reqreply.Route.HandleMW(&routes.BearerAuthMw, VerifyBearer).
// The credential's FORMAT (non-empty) is already validated by the
// built-in codec-based check (via reqreply.SecurityScheme.Codec) BEFORE
// this Fn ever runs — mirrors adapters/mqtt5's OLD ServeOptions.
// SecurityFunc ordering exactly, now reached via the declarative
// mechanism instead of a per-Attach option. Returns an unconditional
// grant — this example has only ONE scope-less scheme, so there is no
// scope-matching logic to demonstrate; a real implementation would look
// the extracted token up against an identity provider/database, mirroring
// examples/rest-api/handlers/security.go's ExtractScopes.
func VerifyBearer(_ context.Context, msg *pahomqtt5.Publish, _ []route.SecurityRequirement) (map[string][]string, error) {
	var token string
	if msg.Properties != nil {
		for _, p := range msg.Properties.User {
			if p.Key == "Authorization" {
				token = strings.TrimPrefix(p.Value, "Bearer ")
			}
		}
	}
	_ = token // format already validated upstream; nothing further to check for this demo
	return map[string][]string{"bearerAuth": nil}, nil
}
