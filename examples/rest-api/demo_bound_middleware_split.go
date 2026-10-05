package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoBoundMiddlewareSplit exercises routes/bound_middleware_split_demo.go
// (chi server only, mirroring demoGrantedScopesContextField's own
// chi-only precedent) — a direct, side-by-side contrast of the two
// classes docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 introduced:
//
//  1. GET /demo-reusable-alone — the reusable class (Class 1) attached
//     ALONE, via plain .Use(). No security requirement at all.
//  2. BOUND ALONE (Class 2) — already fully exercised by
//     demoCreateUser/demoGetUser/demoUpdateUser/demoListUsers/
//     demoProfile/demoAdminAction above, each reusing
//     [routes.BoundScopeServerMW]/[routes.BoundScopeClientMW] against a
//     DIFFERENT Req type — not repeated here.
//  3. POST /stacked-demo — reusable AND bound attached TOGETHER on ONE
//     route (.Use(reusable).HandleBoundMW(bound) server-side,
//     .Use(reusable).ClientBoundMW(bound) client-side).
func demoBoundMiddlewareSplit(chiClient *rest.Client) {
	ctx := context.Background()

	fmt.Println("=== GET /demo-reusable-alone — reusable class ALONE (no security) ===")
	respAny, err := chiClient.Call(ctx, restapiclient.ReusableAloneRoute, routes.StackedDemoReq{Value: 21})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  result: %+v\n", respAny.(routes.StackedDemoResp))
	}
	fmt.Println()

	fmt.Println("=== POST /stacked-demo — reusable + bound STACKED on one route (Alice, profile scope) ===")
	respAny, err = chiClient.Call(ctx, restapiclient.StackedDemoRouteAsAlice, routes.StackedDemoReq{Value: 10})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  result: %+v\n", respAny.(routes.StackedDemoResp))
	}
	fmt.Println()
}
