package main

import (
	"context"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/route"
)

// demoGlobalSecurityDualModeCall demonstrates Client.Call's CONFIRMED
// dual-mode acceptance side by side: the SAME route (routes.
// GlobalOnlyComputeRoute), relying ONLY on Server.AddGlobalSecurity (no
// per-route Security), called once via a raw Route (GlobalSecurity
// invisible client-side — no credential offered, so the server rejects it,
// the SAME accepted limitation rest.Route.ClientHandle has) and once via
// an already-registered *RouteHandle dispatched through a SECOND
// reqreply.Client attached (via mqtt5adapter.AttachClient) with a
// CredentialFunc — the fully Attach-based workflow now handles this case
// too. Migrated OFF the old mqtt5adapter.Call escape hatch: Phase 0/0b of
// docs/roadmap/reqreply-middleware.md closed the capability gap that once
// required it here, and Phase 0b's own de-duplication work confirmed
// AttachClient's CallOptions.CredentialFunc already supports exactly this
// case, needing no new mechanism.
func demoGlobalSecurityDualModeCall(ctx context.Context, built *mqtt5server.Built, mqtt5Client *reqreply.Client, globalHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]) {
	fmt.Println("\n── Demo 2: dual-mode Client.Call — GlobalSecurity visibility ──")

	req := routes.ComputeReq{X: 5, Y: 6}

	fmt.Println("\n  → raw Route via reqreply.Client.Call (GlobalSecurity invisible, no credential offered):")
	_, err := mqtt5Client.Call(ctx, routes.GlobalOnlyComputeRoute, req)
	if err != nil {
		fmt.Printf("  ✓ rejected server-side (GlobalSecurity IS still enforced there, even though the raw\n    Route call never saw it client-side): %v\n", err)
	} else {
		fmt.Println("  (unexpectedly succeeded — GlobalSecurity was not enforced)")
	}

	fmt.Println("\n  → already-registered *RouteHandle, credential supplied via a CredentialFunc-attached Client:")
	credentialedClient := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(credentialedClient, built.Broker, built.Router, mqtt5adapter.CallOptions{
		CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
			return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "******"}}, nil
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching credentialed client: %v\n", err)
		os.Exit(1)
	}
	respAny, err := credentialedClient.Call(ctx, globalHandle, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(%d + %d) = %d (GlobalSecurity satisfied via credential)\n", req.X, req.Y, resp.Sum)
}
