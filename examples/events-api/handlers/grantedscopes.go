package handlers

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// gsAPIKeyScopes is a mock credential store for the GrantedScopes demo —
// deliberately separate from ValidAPIKeys above (a different scheme,
// "apiKeyGS", with its own scope vocabulary).
var gsAPIKeyScopes = map[string][]string{
	"sensor-key-readonly":  {"read:sensors"},
	"sensor-key-writeonly": {"write:sensors"}, // lacks "read:sensors" — proves rejection
}

// VerifyAPIKeyGS is GrantedScopesSensorMw's paired, BOUND SubscribeMW
// Fn — func(ctx, *T, In) (Out, error), the NEW additive 2-return shape
// (docs/design/d-0007-declarative-middleware-layering.md's "Prerequisite
// for Phase 2 (api/events)"), detected via its own reflected signature.
// Unlike MQTT5SecurityImpl above (which reads a raw *pahomqtt5.Publish),
// this Fn receives routes.AuthIn ALREADY DECODED from the X-API-Key User
// Property by the shared middleware.DecodeLayer mechanism.
func VerifyAPIKeyGS(_ context.Context, _ *routes.SensorReading, in routes.AuthIn) (routes.AuthOut, error) {
	scopes, ok := gsAPIKeyScopes[in.Key]
	if !ok {
		return routes.AuthOut{}, fmt.Errorf("unknown or expired API key %q", in.Key)
	}
	return routes.AuthOut{GrantedScopes: map[string][]string{"apiKeyGS": scopes}}, nil
}

// PrintReadingGS is the real business handler — it never touches the
// X-API-Key User Property itself. The authenticated key reaches it
// PURELY via routes.GrantedScopesUserIDField.Get(ctx), published by
// VerifyAPIKeyGS above through SetContextFieldFromIn.
func PrintReadingGS(label string) func(context.Context, routes.SensorReading) error {
	return func(ctx context.Context, r routes.SensorReading) error {
		key, ok := routes.GrantedScopesUserIDField.Get(ctx)
		fmt.Printf("  [%s handler] authenticated API key (via ContextField, ok=%v): %q — reading: %+v\n", label, ok, key, r)
		return nil
	}
}
