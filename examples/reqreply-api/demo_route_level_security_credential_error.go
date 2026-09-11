package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/route"
)

// demoRouteLevelSecurityCredentialError uses routes.SecuredComputeRoute,
// which declares its OWN Security (not relying on GlobalSecurity), called
// with a deliberately malformed/missing credential — demonstrating
// reqreply.SecurityCredentialError surfacing via errors.As, mirroring
// examples/adapters-mqtt5's own runSecurityDemo. Each credential value is
// attached to its OWN reqreply.Client (via mqtt5adapter.AttachClient) —
// CredentialFunc is fixed per-Attach, not per-call, so a valid-credential
// call and a malformed-credential call need two distinct Client
// instances. Migrated OFF the old mqtt5adapter.Call escape hatch — see
// demo_global_security_dual_mode_call.go's doc comment for the same
// rationale (Phase 0/0b of docs/roadmap/reqreply-middleware.md).
func demoRouteLevelSecurityCredentialError(ctx context.Context, built *mqtt5server.Built, securedHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]) {
	fmt.Println("\n── Demo 3: route-level security — SecurityCredentialError ──")

	fmt.Println("\n  → call with a valid bearer token:")
	validClient := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(validClient, built.Broker, built.Router, mqtt5adapter.CallOptions{
		CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
			return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "******"}}, nil
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching valid-credential client: %v\n", err)
		os.Exit(1)
	}
	respAny, err := validClient.Call(ctx, securedHandle, routes.ComputeReq{X: 7, Y: 8})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(7 + 8) = %d (credential accepted)\n", resp.Sum)

	fmt.Println("\n  → call with a malformed (empty) bearer token:")
	malformedClient := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(malformedClient, built.Broker, built.Router, mqtt5adapter.CallOptions{
		CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
			return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer "}}, nil
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching malformed-credential client: %v\n", err)
		os.Exit(1)
	}
	_, err = malformedClient.Call(ctx, securedHandle, routes.ComputeReq{X: 1, Y: 2})
	var credErr reqreply.SecurityCredentialError
	if errors.As(err, &credErr) {
		fmt.Printf("  ✓ rejected client-side: scheme=%q (request never published)\n", credErr.Scheme)
	} else {
		fmt.Fprintf(os.Stderr, "expected SecurityCredentialError, got: %v\n", err)
		os.Exit(1)
	}
}
