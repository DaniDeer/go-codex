package main

import (
	"context"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// globalSecurityCredFn is routes.BearerAuthMw's reusable-class WithSend
// Fn (client side) — REPLACES the OLD mqtt5adapter.CallOptions.
// CredentialFunc entirely (Phase 1 of docs/design/d-0004-reqreply-workflow-simplification.md's Addendum,
// BREAKING removal) and the OLD legacy-shaped paired ClientMW Fn
// permanently closed by docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Phase
// C. Attached via routes.BearerAuthMw.WithSend(...), itself attached via
// .Use(...).
func globalSecurityCredFn(context.Context) (routes.BearerAuthIn, error) {
	return routes.BearerAuthIn{Token: "Bearer ******"}, nil
}

// demoGlobalSecurityDualModeCall demonstrates Client.Call's CONFIRMED
// dual-mode acceptance side by side: the SAME PRISTINE route value
// (routes.GlobalOnlyComputeRoute, never .Use()'d), relying ONLY on
// Server.AddGlobalSecurity (no per-route Security declared on THIS
// value), called once directly (GlobalSecurity invisible client-side —
// no credential offered, so the server rejects it, the SAME accepted
// limitation rest.Route.ClientHandle has) and once via a SEPARATE Route
// VARIANT built by chaining .Use(routes.BearerAuthMw.WithSend(...)) onto
// the SAME base value — [Route] and [reqreply.Middleware] are both
// immutable, so this produces a distinct Go value sharing the same
// topic, without mutating the original (confirmed via [Route.Use]'s own
// doc comment).
//
// Migrated OFF the OLD mqtt5adapter.Call/CredentialFunc escape hatch
// onto the declarative .Use()/.WithSend() mechanism, mirroring
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
	if err := credentialedClient.Attach(mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{Client: built.Broker, Router: built.Router})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching credentialed client: %v\n", err)
		os.Exit(1)
	}
	credentialedRoute := routes.GlobalOnlyComputeRoute.
		Use(routes.BearerAuthMw.WithSend(globalSecurityCredFn))
	respAny, err := credentialedClient.Call(ctx, credentialedRoute, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(%d + %d) = %d (GlobalSecurity satisfied via credential)\n", req.X, req.Y, resp.Sum)
}
