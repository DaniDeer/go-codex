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

// validBearerCredFn/malformedBearerCredFn are PAIRED client-side
// credential-supplying Fns for routes.BearerAuthMw — REPLACE the OLD
// mqtt5adapter.CallOptions.CredentialFunc entirely (Phase 1 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum, BREAKING removal). Two distinct
// Fns (rather than one parameterized function) mirror
// examples/rest-api/client/client.go's AliceCredFn/AdminCredFn pattern —
// each demonstrates a DIFFERENT credential outcome when attached via
// .ClientMW(&routes.BearerAuthMw, ...).
func validBearerCredFn(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
	return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "******"}}, nil
}

func malformedBearerCredFn(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
	return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer "}}, nil
}

// demoRouteLevelSecurityCredentialError uses routes.SecuredComputeRoute,
// which declares its OWN Security via .Use(routes.BearerAuthMw) (not
// relying on GlobalSecurity), called with a deliberately malformed/
// missing credential — demonstrating reqreply.SecurityCredentialError
// surfacing via errors.As, mirroring examples/events-api's own
// security demos. Each credential Fn is attached to its OWN Route
// variant (via .ClientMW) — [Route] is immutable, so
// routes.SecuredComputeRoute.ClientMW(...) called twice with different
// Fns produces two independent Go values sharing the same topic, exactly
// mirroring examples/rest-api's AliceCredFn/AdminCredFn dual-variant
// pattern.
//
// Migrated OFF the OLD mqtt5adapter.Call/CredentialFunc escape hatch
// onto the declarative .Use()/.ClientMW() mechanism.
func demoRouteLevelSecurityCredentialError(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo 3: route-level security — SecurityCredentialError ──")

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, built.Broker, built.Router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n  → call with a valid bearer token:")
	validRoute := routes.SecuredComputeRoute.Use(routes.BearerAuthMw).ClientMW(&routes.BearerAuthMw, validBearerCredFn)
	respAny, err := client.Call(ctx, validRoute, routes.ComputeReq{X: 7, Y: 8})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(7 + 8) = %d (credential accepted)\n", resp.Sum)

	fmt.Println("\n  → call with a malformed (empty) bearer token:")
	malformedRoute := routes.SecuredComputeRoute.Use(routes.BearerAuthMw).ClientMW(&routes.BearerAuthMw, malformedBearerCredFn)
	_, err = client.Call(ctx, malformedRoute, routes.ComputeReq{X: 1, Y: 2})
	var credErr reqreply.SecurityCredentialError
	if errors.As(err, &credErr) {
		fmt.Printf("  ✓ rejected client-side: scheme=%q (request never published)\n", credErr.Scheme)
	} else {
		fmt.Fprintf(os.Stderr, "expected SecurityCredentialError, got: %v\n", err)
		os.Exit(1)
	}
}
