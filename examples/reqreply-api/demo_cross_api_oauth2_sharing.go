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
)

// demoCrossAPIOAuth2Sharing is the concrete answer to "can a REST OAuth2
// scheme and a zeromq reqreply OAuth2 scheme come from the SAME source
// of truth?": YES — routes.NewOAuthMwReqreply(...) and routes.OAuthMwREST
// are two DISTINCT Go values (reqreply.BoundMiddleware[OAuthComputeReq,
// struct{},OAuthOut] and rest.Middleware[struct{},struct{}] respectively
// — not the same type, cannot be the same value), but both are built
// from the SAME shared route.SecurityScheme (routes.oauthComputeScheme,
// via route.OAuth2Scheme) and the SAME shared credential codec
// (routes.OAuthCodec) — see routes/middleware.go's doc comments for why
// true single-value sharing across api/rest and api/reqreply isn't
// achievable with the codec-backed Middleware[In,Out] family (each
// pattern's internal dispatch only recognizes its OWN concrete type) and
// why "one shared config, one declaration per pattern" is the
// replacement guarantee. routes.OAuthComputeRoute (already registered,
// security attached, in zeromqserver.Build) is called below with the
// credential supplied directly as an ordinary OAuthComputeReq.Token field
// — zeromq has no property/header side channel, so there is no
// declarative client-side credential-supply step to demonstrate here
// (unlike routes.BearerAuthMw's mqtt5 property-merge-field case); the
// caller simply sets the field like any other request value.
// routes.OAuthMwREST attaches to a throwaway, LOCALLY-declared REST
// route (spec-only — no HTTP server needed to prove the point;
// examples/rest-api already fully covers real REST serving). What CANNOT
// be shared is the paired SERVER implementation Fn itself: REST's shape
// needs *http.Request access, zeromq's needs *OAuthComputeReq access —
// each transport gets its OWN THIN wrapper Fn, both delegating to the
// SAME shared handlers.VerifyOAuth2Scopes helper. See
// docs/features/security.md's "Sharing a security SCHEME across
// REST/events/reqreply" section for the full write-up this demo backs.
func demoCrossAPIOAuth2Sharing(ctx context.Context, zeromqBuilt *zeromqserver.Built) {
	fmt.Println("\n── Demo 9: cross-API OAuth2 scheme sharing (REST + zeromq reqreply) ──")

	// A dedicated client (independent of the other demos' shared zeromq
	// client) attached to the SAME zeromqserver.Built.ClientSockets map.
	zClient, err := reqreplyapiclient.BuildZeroMQ(zeromqBuilt.ClientSockets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error building zeromq client: %v\n", err)
		os.Exit(1)
	}

	// ── zeromq side: real, served, called ───────────────────────────────
	fmt.Println("\n  → zeromq reqreply call WITHOUT a valid OAuth2 token:")
	_, err = zClient.Call(ctx, routes.OAuthComputeRoute, routes.OAuthComputeReq{X: 3, Y: 4, Token: "expired-token"})
	if err == nil || !strings.Contains(err.Error(), "not recognized") {
		fmt.Fprintf(os.Stderr, "expected a rejection mentioning an unrecognized token, got: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ rejected: %v\n", err)

	fmt.Println("\n  → zeromq reqreply call WITH a valid OAuth2 token:")
	respAny, err := zClient.Call(ctx, routes.OAuthComputeRoute, routes.OAuthComputeReq{X: 3, Y: 4, Token: "valid-compute-write-token"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.OAuthComputeResp)
	fmt.Printf("  ✓ compute(3 + 4) = %d (oauth2 compute:write scope granted)\n", resp.Sum)

	// ── REST side: routes.OAuthMwREST, same SCHEME config, declare + register only ──
	fmt.Println("\n  → routes.OAuthMwREST (same oauth2Compute scheme config as above), attached to a locally-declared REST route:")
	restServer := rest.NewServer(rest.Info{Title: "Compute API (REST, spec-only demo)", Version: "1.0.0"})
	if err := rest.NewRoute[routes.OAuthComputeReq, routes.OAuthComputeResp](
		"POST", "/compute/oauth-add",
		routes.OAuthComputeReqCodec, routes.OAuthComputeRespCodec,
		rest.RouteMeta{OperationID: "oauthComputeAddREST"},
	).Use(routes.OAuthMwREST).Register(restServer); err != nil {
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
	fmt.Println("  ✓ same scheme name/type/flows in BOTH specs — one shared route.SecurityScheme config, two pattern-specific declarations")
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
