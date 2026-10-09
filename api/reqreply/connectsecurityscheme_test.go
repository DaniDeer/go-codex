package reqreply_test

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/internal/route"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C —
// Phase 4, connection-level auth: Server.AddConnectSecurityScheme tests,
// mirroring api/events's identical TestAddConnectSecurityScheme_* tests.

func TestServer_AddConnectSecurityScheme_AppearsInAsyncAPISpec(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	b.AddConnectSecurityScheme("brokerAuth", route.SecurityScheme{Type: route.SecuritySchemeHTTP, Scheme: "basic"})
	b.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtts://broker:8883", Protocol: "mqtt5",
		Security: []route.SecurityRequirement{route.Require("brokerAuth")}})

	r := newMWTestRoute()
	if _, err := r.HandleMW(nil, func() (map[string][]string, error) { return nil, nil }).Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	spec := mustSpec(t, b)
	if !strings.Contains(spec, "brokerAuth:") {
		t.Errorf("want 'brokerAuth' connection-level scheme in components/securitySchemes, got:\n%s", spec)
	}
	if !strings.Contains(spec, "scheme: basic") {
		t.Errorf("want scheme: basic rendered, got:\n%s", spec)
	}
}

// TestServer_AddConnectSecurityScheme_RouteCollision_LastRegisteredWins
// proves a route re-registering the IDENTICAL scheme name still wins on
// collision (unchanged last-registered-wins policy, now a 2nd
// contributor alongside the per-route WithSecurityScheme declarations).
func TestServer_AddConnectSecurityScheme_RouteCollision_LastRegisteredWins(t *testing.T) {
	b := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	b.AddConnectSecurityScheme("shared", route.SecurityScheme{Type: route.SecuritySchemeHTTP, Scheme: "basic"})

	r := reqreply.NewRoute[computeReq, computeResp](
		"compute/connect-scheme-collision",
		mwTestReqCodec, mwTestRespCodec,
		reqreply.RouteMeta{OperationID: "connectSchemeCollision"},
		reqreply.WithSecurityScheme("shared", reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}),
	)
	if _, err := r.HandleMW(nil, func() (map[string][]string, error) { return nil, nil }).Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	spec := mustSpec(t, b)
	if !strings.Contains(spec, "scheme: bearer") {
		t.Errorf("want route-registered (bearer) scheme to win collision over the connection-level one, got:\n%s", spec)
	}
}
