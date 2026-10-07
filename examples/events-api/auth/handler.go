package auth

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// ValidAPIKeys is a mock set of trusted API keys — in production this
// would be a database lookup or a signed token check.
var ValidAPIKeys = map[string]bool{
	"sensor-key-abc123": true,
	"sensor-key-xyz789": true,
}

// MQTTSecurityImpl is the mqtt v3-shaped, channel-BOUND security Fn
// (NewAPIKeyAuthMW — docs/design/d-0003-codec-declared-middlewares.md's
// Addendum 7), returned as a closure since MQTT 3.1.1 carries no
// per-message credential metadata at all (no property axis to decode
// APIKeyAuthIn.Key from), so credential is captured in a closure at
// CONNECT time (Pattern 1 — see examples/adapters-mqtt-security's own
// migrated docs) and APIKeyAuthIn is left zero/unused. Attach via
// routes.SensorDataSub.SubscribeBoundMW(auth.NewAPIKeyAuthMW(auth.MQTTSecurityImpl(credential))).
func MQTTSecurityImpl(credential string) func(context.Context, *routes.SensorReading, APIKeyAuthIn) (APIKeyAuthOut, error) {
	return func(_ context.Context, _ *routes.SensorReading, _ APIKeyAuthIn) (APIKeyAuthOut, error) {
		if !ValidAPIKeys[credential] {
			return APIKeyAuthOut{}, fmt.Errorf("unknown API key %q", credential)
		}
		return APIKeyAuthOut{GrantedScopes: map[string][]string{"apiKeyAuth": {}}}, nil
	}
}

// MQTT5SecurityImpl is the mqtt5-shaped, channel-BOUND security Fn —
// MQTT 5 exposes User Properties, so the credential is decoded into
// APIKeyAuthIn.Key via a property merge field
// (NewAPIKeyAuthMW(...).WithSubscribeProperty, mirroring
// middleware.go's own GrantedScopesSensorMw pattern) instead of reading
// a raw *pahomqtt5.Publish directly.
func MQTT5SecurityImpl(_ context.Context, _ *routes.SensorReading, in APIKeyAuthIn) (APIKeyAuthOut, error) {
	if !ValidAPIKeys[in.Key] {
		return APIKeyAuthOut{}, fmt.Errorf("missing or unknown X-API-Key user property %q", in.Key)
	}
	return APIKeyAuthOut{GrantedScopes: map[string][]string{"apiKeyAuth": {}}}, nil
}

// ZeromqSecurityImpl is the zeromq-shaped, channel-BOUND security Fn —
// ZeroMQ's [topic, payload] frames carry nothing beyond the decoded
// value, so there is no property/merge-field axis to decode a credential
// from here either. This demo's routes.SensorReading has no credential
// field, so this implementation demonstrates the SHAPE (always grants,
// no real credential check) — a production zeromq channel would add a
// Token-equivalent field to its message type (see
// examples/reqreply-api/routes/routes.go's OAuthComputeReq for the
// pattern) and validate it here, reading it off msg directly (the bound
// class's Fn DOES have *T access, unlike the reusable class).
func ZeromqSecurityImpl(_ context.Context, _ *routes.SensorReading, _ APIKeyAuthIn) (APIKeyAuthOut, error) {
	return APIKeyAuthOut{GrantedScopes: map[string][]string{"apiKeyAuth": {}}}, nil
}

// gsAPIKeyScopes is a mock credential store for the GrantedScopes demo —
// deliberately separate from ValidAPIKeys above (a different scheme,
// "apiKeyGS", with its own scope vocabulary).
var gsAPIKeyScopes = map[string][]string{
	"sensor-key-readonly":  {"read:sensors"},
	"sensor-key-writeonly": {"write:sensors"}, // lacks "read:sensors" — proves rejection
}

// VerifyAPIKeyGS is GrantedScopesSensorMw's embedded Fn — func(ctx, *T,
// In) (Out, error), the shape [events.NewBoundSubscribeMiddleware]/
// [events.BoundSecuritySubscribeMiddleware] require at construction
// (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's
// events/BoundSubscribeMiddleware class), passed directly to the
// constructor rather than paired separately at attachment time. Unlike
// MQTT5SecurityImpl above (which has no *T access at all, the reusable
// class), this Fn receives AuthIn ALREADY DECODED from the X-API-Key
// User Property by the shared middleware.DecodeLayer mechanism.
func VerifyAPIKeyGS(_ context.Context, _ *routes.SensorReading, in AuthIn) (AuthOut, error) {
	scopes, ok := gsAPIKeyScopes[in.Key]
	if !ok {
		return AuthOut{}, fmt.Errorf("unknown or expired API key %q", in.Key)
	}
	return AuthOut{GrantedScopes: map[string][]string{"apiKeyGS": scopes}}, nil
}

// PrintReadingGS is the real business handler — it never touches the
// X-API-Key User Property itself. The authenticated key reaches it
// PURELY via GrantedScopesUserIDField.Get(ctx), published by
// VerifyAPIKeyGS above through SetContextFieldFromIn.
func PrintReadingGS(label string) func(context.Context, routes.SensorReading) error {
	return func(ctx context.Context, r routes.SensorReading) error {
		key, ok := GrantedScopesUserIDField.Get(ctx)
		fmt.Printf("  [%s handler] authenticated API key (via ContextField, ok=%v): %q — reading: %+v\n", label, ok, key, r)
		return nil
	}
}
