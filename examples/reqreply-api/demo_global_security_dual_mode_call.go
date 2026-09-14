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

// globalSecurityCredFn is the PAIRED client-side credential-supplying Fn
// for routes.BearerAuthMw — REPLACES the OLD mqtt5adapter.CallOptions.
// CredentialFunc entirely (Phase 1 of docs/design/d-0004-reqreply-workflow-simplification.md's Addendum,
// BREAKING removal). Attached via .ClientMW(&routes.BearerAuthMw, ...).
func globalSecurityCredFn(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
	return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "******"}}, nil
}

// demoGlobalSecurityDualModeCall demonstrates Client.Call's CONFIRMED
// dual-mode acceptance side by side: the SAME PRISTINE route value
// (routes.GlobalOnlyComputeRoute, never .Use()'d), relying ONLY on
// Server.AddGlobalSecurity (no per-route Security declared on THIS
// value), called once directly (GlobalSecurity invisible client-side —
// no credential offered, so the server rejects it, the SAME accepted
// limitation rest.Route.ClientHandle has) and once via a SEPARATE Route
// VARIANT built by chaining .Use(routes.BearerAuthMw).ClientMW(...) onto
// the SAME base value — [Route] is immutable, so this produces a
// distinct Go value sharing the same topic, without mutating the
// original (confirmed via [Route.Use]'s own doc comment).
//
// Migrated OFF the OLD mqtt5adapter.Call/CredentialFunc escape hatch
// onto the declarative .Use()/.ClientMW() mechanism, mirroring
// examples/rest-api's identical workflow.
func demoGlobalSecurityDualModeCall(ctx context.Context, built *mqtt5server.Built, mqtt5Client *reqreply.Client) {
	fmt.Println("\n── Demo 2: dual-mode Client.Call — GlobalSecurity visibility ──")

	req := routes.ComputeReq{X: 5, Y: 6}

	fmt.Println("\n  → pristine Route via reqreply.Client.Call (GlobalSecurity invisible, no credential offered):")
	_, err := mqtt5Client.Call(ctx, routes.GlobalOnlyComputeRoute, req)
	if err != nil {
		fmt.Printf("  ✓ rejected server-side (GlobalSecurity IS still enforced there, even though the raw\n    Route call never saw it client-side): %v\n", err)
	} else {
		fmt.Println("  (unexpectedly succeeded — GlobalSecurity was not enforced)")
	}

	fmt.Println("\n  → a SEPARATE Route variant with .Use()+.ClientMW() attached, credential supplied declaratively:")
	credentialedClient := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(credentialedClient, built.Broker, built.Router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching credentialed client: %v\n", err)
		os.Exit(1)
	}
	credentialedRoute := routes.GlobalOnlyComputeRoute.
		Use(routes.BearerAuthMw).
		ClientMW(&routes.BearerAuthMw, globalSecurityCredFn)
	respAny, err := credentialedClient.Call(ctx, credentialedRoute, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(%d + %d) = %d (GlobalSecurity satisfied via credential)\n", req.X, req.Y, resp.Sum)
}
