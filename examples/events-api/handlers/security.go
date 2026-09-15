package handlers

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/route"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// ValidAPIKeys is a mock set of trusted API keys — in production this
// would be a database lookup or a signed token check.
var ValidAPIKeys = map[string]bool{
	"sensor-key-abc123": true,
	"sensor-key-xyz789": true,
}

// MQTTSecurityImpl is the mqtt v3-shaped, SubscribeMW-paired security
// implementation Fn. MQTT 3.1.1 carries no per-message credential
// metadata, so credential is captured in a closure at CONNECT time
// (Pattern 1 — see examples/adapters-mqtt-security's own migrated
// docs). Returns granted scopes on success — attach via
// routes.SensorDataSub.SubscribeMW(&routes.APIKeyAuthMW, MQTTSecurityImpl(credential)).
func MQTTSecurityImpl(credential string) func(context.Context, pahomqtt.Message, *routes.SensorReading) (map[string][]string, error) {
	return func(_ context.Context, _ pahomqtt.Message, _ *routes.SensorReading) (map[string][]string, error) {
		if !ValidAPIKeys[credential] {
			return nil, fmt.Errorf("unknown API key %q", credential)
		}
		return map[string][]string{"apiKeyAuth": {}}, nil
	}
}

// MQTT5SecurityImpl is the mqtt5-shaped, SubscribeMW-paired security
// implementation Fn — MQTT 5 exposes User Properties, so the credential
// is extracted directly from the raw *pahomqtt5.Publish (Pattern 2).
func MQTT5SecurityImpl(_ context.Context, msg *pahomqtt5.Publish, _ *routes.SensorReading) (map[string][]string, error) {
	var apiKey string
	if msg.Properties != nil {
		apiKey = msg.Properties.User.Get("X-API-Key")
	}
	if !ValidAPIKeys[apiKey] {
		return nil, fmt.Errorf("missing or unknown X-API-Key user property %q", apiKey)
	}
	return map[string][]string{"apiKeyAuth": {}}, nil
}

// ZeromqSecurityImpl is the zeromq-shaped, SubscribeMW-paired security
// implementation Fn — ZeroMQ's [topic, payload] frames carry nothing
// beyond the decoded value, so the credential must be an in-payload
// field. This demo's routes.SensorReading has no credential field, so
// this implementation demonstrates the SHAPE (read/write access to *T,
// plain error return — no scope-grant map, unlike mqtt/mqtt5's shape)
// without a real credential check — a production zeromq channel would
// add a Token-equivalent field to its message type (see
// examples/reqreply-api/routes/routes.go's OAuthComputeReq for the
// pattern) and validate it here.
func ZeromqSecurityImpl(_ context.Context, _ *routes.SensorReading, _ []route.SecurityRequirement) error {
	return nil
}
