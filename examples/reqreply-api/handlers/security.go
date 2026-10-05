package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// VerifyBearer is routes.BearerAuthMw's reusable-class WithReceive Fn —
// attached via routes.BearerAuthMw.WithReceive(VerifyBearer), itself
// attached to a route via plain .Use(...). The credential's FORMAT
// (non-empty) is already validated by the built-in codec-based check
// (via reqreply.SecurityScheme.Codec) BEFORE this Fn ever runs; the raw
// "Authorization" MQTT5 User Property value is now merge-field-decoded
// into in.Token DECLARATIVELY (routes.BearerAuthMw's own
// WithRequestProperty) rather than read off *pahomqtt5.Publish by hand —
// REPLACES the OLD legacy-shaped paired Fn permanently closed by
// docs/roadmap/bound-middleware-split.md's Phase C. Returns an
// unconditional grant — this example has only ONE scope-less scheme, so
// there is no scope-matching logic to demonstrate; a real implementation
// would look the extracted token up against an identity provider/
// database, mirroring examples/rest-api/handlers/security.go's
// ExtractScopes.
func VerifyBearer(_ context.Context, in routes.BearerAuthIn) (routes.BearerAuthOut, error) {
	token := strings.TrimPrefix(in.Token, "Bearer ")
	_ = token // format already validated upstream; nothing further to check for this demo
	return routes.BearerAuthOut{GrantedScopes: map[string][]string{"bearerAuth": nil}}, nil
}

// AlwaysRejectSecurityImpl is a reusable-class WithReceive Fn that ALWAYS
// rejects — used by demo_error_pattern.go's security-middleware +
// ErrorPattern combination demo to prove a declared
// reqreply.ErrorPattern[reqreply.SecurityError, ...] intercepts a
// SECURITY-MIDDLEWARE Fn failure (not just a handler failure). Attached
// via .Use(routes.BearerAuthMw.WithReceive(AlwaysRejectSecurityImpl)),
// not routes.BearerAuthMw directly — Middleware is immutable, so this
// derived value shares the "bearerAuth" scheme name without ever
// mutating the shared base.
func AlwaysRejectSecurityImpl(_ context.Context, _ routes.BearerAuthIn) (routes.BearerAuthOut, error) {
	return routes.BearerAuthOut{}, errors.New("access denied for demo")
}
