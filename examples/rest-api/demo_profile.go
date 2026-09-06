package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoProfile exercises GET /profile (requires the "profile" scope,
// LAYERED with request-side cookie + header validation on the SAME
// route) against chiClient — cookie/header vars auto-derived from
// ProfileReq via RouteHandle.EncodeCookieVars/EncodeHeaderVars. The
// invalid-cookie demo is ANOTHER client-side pre-flight rejection (the
// SAME codec that validates server-side also validates the merge-field
// encode client-side) — the unauthenticated demo shows the SERVER-side
// 401 instead, since that's a security gate, not a codec constraint.
func demoProfile(chiClient *rest.Client) {
	ctx := context.Background()

	fmt.Println("=== GET /profile — profile scope, valid cookie+header (auto-derived) ===")
	respAny, err := chiClient.Call(ctx, restapiclient.ProfileRouteAsAlice, routes.ProfileReq{
		SessionToken: "my-valid-session-token",
		RequestID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
	})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  user: %+v\n", respAny.(routes.User))
	}
	fmt.Println()

	fmt.Println("=== GET /profile — invalid cookie (client-side PRE-FLIGHT rejection) ===")
	_, err = chiClient.Call(ctx, restapiclient.ProfileRouteAsAlice, routes.ProfileReq{
		SessionToken: "", // fails NonEmptyString — never reaches the network
		RequestID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
	})
	fmt.Printf("  client-side rejection (never reached the network): %v\n", err)
	fmt.Println()

	fmt.Println("=== GET /profile — unauthenticated (server-side 401) ===")
	_, err = chiClient.Call(ctx, restapiclient.ProfileRouteUnauthenticated, routes.ProfileReq{
		SessionToken: "my-valid-session-token",
		RequestID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
	})
	printStatusErr(err)
	fmt.Println()
}
