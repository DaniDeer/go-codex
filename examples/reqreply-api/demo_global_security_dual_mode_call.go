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
// an already-registered *RouteHandle dispatched through the fully
// featured mqtt5adapter.Call escape hatch (a credential IS supplied and
// GlobalSecurity is satisfied).
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

	fmt.Println("\n  → already-registered *RouteHandle, credential supplied via mqtt5adapter.Call directly:")
	resp, err := mqtt5adapter.Call(ctx, built.Broker, built.Router, globalHandle, req, mqtt5adapter.CallOptions{
		CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
			return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer demo-token"}}, nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(%d + %d) = %d (GlobalSecurity satisfied via credential)\n", req.X, req.Y, resp.Sum)
}
