package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoAdminAction exercises POST /admin/action (requires the "admin"
// scope, no other params) against chiClient — pure scope gating: Alice
// (profile-only) is rejected with 401 (go-codex has no separate 403 for
// a wrong-scope credential — see middleware.CheckScopes/rest.SecurityError),
// admin succeeds.
func demoAdminAction(chiClient *rest.Client) {
	ctx := context.Background()

	fmt.Println("=== POST /admin/action — Alice (profile-only, expect 401) ===")
	_, err := chiClient.Call(ctx, restapiclient.AdminActionRouteAsAlice, routes.AdminActionReq{Action: "reindex"})
	printStatusErr(err)
	fmt.Println()

	fmt.Println("=== POST /admin/action — admin (expect 200) ===")
	respAny, err := chiClient.Call(ctx, restapiclient.AdminActionRouteAsAdmin, routes.AdminActionReq{Action: "reindex"})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  result: %+v\n", respAny.(routes.AdminActionResp))
	}
	fmt.Println()
}
