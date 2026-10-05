package handlers

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
// (routes.NewAPIKeyAuthMW — docs/design/d-0003-codec-declared-middlewares.md's Addendum 7),
// returned as a closure since MQTT 3.1.1 carries no per-message
// credential metadata at all (no property axis to decode
// routes.APIKeyAuthIn.Key from), so credential is captured in a closure
// at CONNECT time (Pattern 1 — see examples/adapters-mqtt-security's own
// migrated docs) and routes.APIKeyAuthIn is left zero/unused. Attach via
// routes.SensorDataSub.SubscribeBoundMW(routes.NewAPIKeyAuthMW(MQTTSecurityImpl(credential))).
func MQTTSecurityImpl(credential string) func(context.Context, *routes.SensorReading, routes.APIKeyAuthIn) (routes.APIKeyAuthOut, error) {
	return func(_ context.Context, _ *routes.SensorReading, _ routes.APIKeyAuthIn) (routes.APIKeyAuthOut, error) {
		if !ValidAPIKeys[credential] {
			return routes.APIKeyAuthOut{}, fmt.Errorf("unknown API key %q", credential)
		}
		return routes.APIKeyAuthOut{GrantedScopes: map[string][]string{"apiKeyAuth": {}}}, nil
	}
}

// MQTT5SecurityImpl is the mqtt5-shaped, channel-BOUND security Fn —
// MQTT 5 exposes User Properties, so the credential is decoded into
// routes.APIKeyAuthIn.Key via a property merge field
// (routes.NewAPIKeyAuthMW(...).WithSubscribeProperty, mirroring
// routes/grantedscopes_demo.go's own GrantedScopesSensorMw pattern)
// instead of reading a raw *pahomqtt5.Publish directly.
func MQTT5SecurityImpl(_ context.Context, _ *routes.SensorReading, in routes.APIKeyAuthIn) (routes.APIKeyAuthOut, error) {
	if !ValidAPIKeys[in.Key] {
		return routes.APIKeyAuthOut{}, fmt.Errorf("missing or unknown X-API-Key user property %q", in.Key)
	}
	return routes.APIKeyAuthOut{GrantedScopes: map[string][]string{"apiKeyAuth": {}}}, nil
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
func ZeromqSecurityImpl(_ context.Context, _ *routes.SensorReading, _ routes.APIKeyAuthIn) (routes.APIKeyAuthOut, error) {
	return routes.APIKeyAuthOut{GrantedScopes: map[string][]string{"apiKeyAuth": {}}}, nil
}
