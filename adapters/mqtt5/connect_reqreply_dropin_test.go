package mqtt5

import (
	"context"
	"testing"
)

// TestConnect_DropInForReqreplyTransports is a PURE compile-time
// assertion (Connect is referenced as a value, NEVER invoked — no ctx, no
// dial, mirrors this package's established "no real network dependency
// in tests" precedent, see connect_test.go) proving docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase C claim: [Connect]'s
// plain (MQTTClient, MQTTRouter) return pair works UNMODIFIED as BOTH
// [ServerTransportOptions]'s AND [ClientTransportOptions]'s Client/Router
// fields — ZERO adapter change needed for reqreply's Serve/Call sides to
// reuse events' Connect helper. A future divergence between Connect's
// return types and either Options struct's field types would FAIL TO
// COMPILE here, not just silently pass.
func TestConnect_DropInForReqreplyTransports(t *testing.T) {
	var connectFn func(context.Context, string, ConnectOptions) (MQTTClient, MQTTRouter, error) = Connect
	_ = connectFn

	var client MQTTClient
	var router MQTTRouter
	_ = ServerTransportOptions{Client: client, Router: router}
	_ = ClientTransportOptions{Client: client, Router: router}
}

// TestSecuredClient_DropInForReqreplyTransports is
// [TestConnect_DropInForReqreplyTransports]'s sibling for
// [*SecuredClient] (the OPTIONAL secondary pre-check wrapper
// [NewSecuredClient] returns) — confirms it ALSO promotes transparently
// into [ServerTransportOptions]'s/[ClientTransportOptions]'s Client
// field, via struct embedding (already compile-time-asserted in
// connect_security.go — this test pins the SAME claim from reqreply's
// own perspective).
func TestSecuredClient_DropInForReqreplyTransports(t *testing.T) {
	var secured *SecuredClient
	var router MQTTRouter
	_ = ServerTransportOptions{Client: secured, Router: router}
	_ = ClientTransportOptions{Client: secured, Router: router}
}
