package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
)

// Round 42: reqreply.MergedPropertyParam[T] direct (Middleware-free)
// route attachment, exercised end-to-end through mqtt5's real Serve/Call
// dispatch — the symmetry-bug fix's reqreply adapter-level proof. Mirrors
// TestAttachClient_WithRequestProperty_WritesOutgoingUserProperty/
// TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty's
// shape, but with NO reqreply.Middleware/Transform involved — only a
// plain reqreply.NewPropertyParam attached directly to NewRoute.

type tenantComputeReqProp struct {
	TenantID string
	X, Y     int
}

var tenantComputeReqPropCodec = codex.Struct[tenantComputeReqProp](
	codex.RequiredField("x", codex.Int(),
		func(r tenantComputeReqProp) int { return r.X },
		func(r *tenantComputeReqProp, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(),
		func(r tenantComputeReqProp) int { return r.Y },
		func(r *tenantComputeReqProp, v int) { r.Y = v }),
)

// TestAttachServer_DirectPropertyAttachment_MergesRealUserProperty proves
// a reqreply.MergedPropertyParam attached DIRECTLY to NewRoute (no
// Middleware) now merges a real incoming MQTT5 User Property into the
// decoded request — previously silently dropped.
func TestAttachServer_DirectPropertyAttachment_MergesRealUserProperty(t *testing.T) {
	route := reqreply.NewRoute[tenantComputeReqProp, computeResp]("compute/direct-prop-test",
		tenantComputeReqPropCodec, computeRespCodec,
		reqreply.NewPropertyParam("X-Tenant", codex.String(),
			func(r tenantComputeReqProp) string { return r.TenantID },
			func(r *tenantComputeReqProp, v string) { r.TenantID = v }),
	)

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	var gotTenant string
	handler := func(ctx context.Context, req tenantComputeReqProp) (computeResp, error) {
		gotTenant = req.TenantID
		return computeResp{Sum: req.X + req.Y}, nil
	}
	if _, err := route.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/direct-prop-test")

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	callRoute := reqreply.NewRoute[tenantComputeReqProp, computeResp]("compute/direct-prop-test",
		tenantComputeReqPropCodec, computeRespCodec,
		reqreply.NewPropertyParam("X-Tenant", codex.String(),
			func(r tenantComputeReqProp) string { return r.TenantID },
			func(r *tenantComputeReqProp, v string) { r.TenantID = v }),
	)
	if _, err := client.Call(context.Background(), callRoute,
		tenantComputeReqProp{TenantID: "acme", X: 1, Y: 2}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	cancel()
	<-errCh

	if gotTenant != "acme" {
		t.Errorf("want server-side TenantID merged to %q, got %q", "acme", gotTenant)
	}
}
