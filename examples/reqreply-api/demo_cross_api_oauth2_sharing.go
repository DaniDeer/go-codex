package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
	reqreplyapiclient "github.com/DaniDeer/go-codex/examples/reqreply-api/client"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/zeromqserver"
	"github.com/DaniDeer/go-codex/route"
)

// validOAuthCredFn/invalidOAuthCredFn are PAIRED client-side credential
// Fns for routes.OAuthMw, zeromq-shaped (writes the token INTO the
// decoded *OAuthComputeReq — zeromq has no raw-message side channel,
// unlike mqtt5's User Properties) — mirrors
// demo_route_level_security_credential_error.go's validBearerCredFn/
// malformedBearerCredFn pattern, adapted to zeromq's in-payload model.
func validOAuthCredFn(_ context.Context, req *routes.OAuthComputeReq, _ []route.SecurityRequirement) error {
	req.Token = "valid-compute-write-token"
	return nil
}

func invalidOAuthCredFn(_ context.Context, req *routes.OAuthComputeReq, _ []route.SecurityRequirement) error {
	req.Token = "expired-token"
	return nil
}

// demoCrossAPIOAuth2Sharing is the concrete answer to "can a middleware
// declare a REST OAuth2 scheme for a zeromq reqreply route?": YES, the
// DECLARATION is fully shared — routes.OAuthMw (a single
// middleware.Middleware value, built from route.OAuth2Scheme) is
// attached via .Use() to BOTH a zeromq reqreply route (already
// registered in zeromqserver.Build, real/served/called below) AND a
// throwaway, LOCALLY-declared REST route (spec-only — no HTTP server
// needed to prove the point; examples/rest-api already fully covers
// real REST serving). What CANNOT be shared is the paired
// implementation Fn itself: REST's shape needs *http.Request access,
// zeromq's needs *OAuthComputeReq access — each transport gets its OWN
// THIN wrapper Fn, both delegating to the SAME shared
// handlers.VerifyOAuth2Scopes helper. See docs/features/security.md's
// "Sharing a security scheme declaration across REST/events/reqreply"
// section for the full write-up this demo backs.
func demoCrossAPIOAuth2Sharing(ctx context.Context, zeromqBuilt *zeromqserver.Built) {
	fmt.Println("\n── Demo 9: cross-API OAuth2 scheme sharing (REST + zeromq reqreply) ──")

	// A dedicated client (independent of the other demos' shared zeromq
	// client) attached to the SAME zeromqserver.Built.ClientSockets map —
	// its ClientMW-attached Route variants below don't interfere with
	// any other demo's calls.
	zClient, err := reqreplyapiclient.BuildZeroMQ(zeromqBuilt.ClientSockets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error building zeromq client: %v\n", err)
		os.Exit(1)
	}

	// ── zeromq side: real, served, called ───────────────────────────────
	fmt.Println("\n  → zeromq reqreply call WITHOUT a valid OAuth2 token:")
	invalidRoute := routes.OAuthComputeRoute.Use(routes.OAuthMw).ClientMW(&routes.OAuthMw, invalidOAuthCredFn)
	_, err = zClient.Call(ctx, invalidRoute, routes.OAuthComputeReq{X: 3, Y: 4})
	if err == nil || !strings.Contains(err.Error(), "not recognized") {
		fmt.Fprintf(os.Stderr, "expected a rejection mentioning an unrecognized token, got: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ rejected: %v\n", err)

	fmt.Println("\n  → zeromq reqreply call WITH a valid OAuth2 token:")
	validRoute := routes.OAuthComputeRoute.Use(routes.OAuthMw).ClientMW(&routes.OAuthMw, validOAuthCredFn)
	respAny, err := zClient.Call(ctx, validRoute, routes.OAuthComputeReq{X: 3, Y: 4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.OAuthComputeResp)
	fmt.Printf("  ✓ compute(3 + 4) = %d (oauth2 compute:write scope granted)\n", resp.Sum)

	// ── REST side: SAME routes.OAuthMw value, declare + register only ──
	fmt.Println("\n  → the SAME routes.OAuthMw value, attached to a locally-declared REST route:")
	restServer := rest.NewServer(rest.Info{Title: "Compute API (REST, spec-only demo)", Version: "1.0.0"})
	if err := rest.NewRoute[routes.OAuthComputeReq, routes.OAuthComputeResp](
		"POST", "/compute/oauth-add",
		routes.OAuthComputeReqCodec, routes.OAuthComputeRespCodec,
		rest.RouteMeta{OperationID: "oauthComputeAddREST"},
	).Use(routes.OAuthMw).Register(restServer); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering REST route: %v\n", err)
		os.Exit(1)
	}

	restDoc, err := restServer.OpenAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenAPISpec error: %v\n", err)
		os.Exit(1)
	}
	restYAML, _ := restDoc.MarshalYAML()
	asyncDoc, err := zeromqBuilt.Server.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec error: %v\n", err)
		os.Exit(1)
	}
	asyncYAML, _ := asyncDoc.MarshalYAML()

	fmt.Println("  OpenAPI (REST) securitySchemes.oauth2Compute:")
	printSchemeSnippet(string(restYAML), "oauth2Compute")
	fmt.Println("  AsyncAPI (zeromq reqreply) components.securitySchemes.oauth2Compute:")
	printSchemeSnippet(string(asyncYAML), "oauth2Compute")
	fmt.Println("  ✓ same scheme name/type/flows in BOTH specs — one middleware.SecurityScheme declaration, two protocols")
}

// printSchemeSnippet prints every line of doc from the FIRST line
// containing name through the next blank/dedented block — a minimal,
// dependency-free "just show me the relevant YAML" helper for this demo
// (avoids pulling in a YAML parser just to slice one nested key).
func printSchemeSnippet(doc, name string) {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if strings.Contains(line, name+":") {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			fmt.Println("   ", strings.TrimRight(line, " "))
			for _, l := range lines[i+1:] {
				if strings.TrimSpace(l) == "" {
					continue
				}
				lIndent := len(l) - len(strings.TrimLeft(l, " "))
				if lIndent <= indent {
					return
				}
				fmt.Println("   ", strings.TrimRight(l, " "))
			}
			return
		}
	}
}
