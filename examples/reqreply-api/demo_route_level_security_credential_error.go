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
// examples/adapters-mqtt5's own runSecurityDemo.
func demoRouteLevelSecurityCredentialError(ctx context.Context, built *mqtt5server.Built, securedHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]) {
	fmt.Println("\n── Demo 3: route-level security — SecurityCredentialError ──")

	fmt.Println("\n  → call with a valid bearer token:")
	resp, err := mqtt5adapter.Call(ctx, built.Broker, built.Router, securedHandle, routes.ComputeReq{X: 7, Y: 8},
		mqtt5adapter.CallOptions{
			CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
				return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer valid-token"}}, nil
			},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(7 + 8) = %d (credential accepted)\n", resp.Sum)

	fmt.Println("\n  → call with a malformed (empty) bearer token:")
	_, err = mqtt5adapter.Call(ctx, built.Broker, built.Router, securedHandle, routes.ComputeReq{X: 1, Y: 2},
		mqtt5adapter.CallOptions{
			CredentialFunc: func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
				return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer "}}, nil
			},
		})
	var credErr reqreply.SecurityCredentialError
	if errors.As(err, &credErr) {
		fmt.Printf("  ✓ rejected client-side: scheme=%q (request never published)\n", credErr.Scheme)
	} else {
		fmt.Fprintf(os.Stderr, "expected SecurityCredentialError, got: %v\n", err)
		os.Exit(1)
	}
}
