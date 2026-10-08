package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoAdminAction exercises POST /admin/action (requires the "admin"
// scope, no other params) — against BOTH chiClient AND nethttpClient —
// pure scope gating: Alice (profile-only) is rejected with 401 (go-codex
// has no separate 403 for a wrong-scope credential — see
// middleware.CheckScopes/rest.SecurityError), admin succeeds.
func demoAdminAction(chiClient, nethttpClient *rest.Client) {
	ctx := context.Background()
	clients := []struct {
		label string
		c     *rest.Client
	}{
		{"chi", chiClient},
		{"net/http", nethttpClient},
	}

	fmt.Println("=== POST /admin/action — Alice (profile-only, expect 401) ===")
	for _, cl := range clients {
		_, err := cl.c.Call(ctx, restapiclient.AdminActionRouteAsAlice, routes.AdminActionReq{Action: "reindex"})
		fmt.Printf("  [%s]", cl.label)
		printStatusErr(err)
	}
	fmt.Println()

	fmt.Println("=== POST /admin/action — admin (expect 200) ===")
	for _, cl := range clients {
		respAny, err := cl.c.Call(ctx, restapiclient.AdminActionRouteAsAdmin, routes.AdminActionReq{Action: "reindex"})
		if err != nil {
			fmt.Printf("  [%s] error: %v\n", cl.label, err)
		} else {
			fmt.Printf("  [%s] result: %+v\n", cl.label, respAny.(routes.AdminActionResp))
		}
	}
	fmt.Println()
}
