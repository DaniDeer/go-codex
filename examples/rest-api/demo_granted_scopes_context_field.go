package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/rest-api/auth"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
)

// demoGrantedScopesContextField exercises POST /compute-gs — the
// GENERALIZED rest.SecurityMiddleware[In,Out] (routes.
// GrantedScopesComputeMw) dispatched through the bound HandleMW/
// ClientMW path, with a REAL GrantedScopes-carrying Out enforced by
// middleware.CheckScopes, AND the authenticated token propagated to the
// real handler via middleware.ContextField (routes.
// GrantedScopesUserIDField) — zero manual re-decoding inside the
// handler. See docs/design/d-0007-declarative-middleware-layering.md
// for the full design this demo exercises end-to-end.
func demoGrantedScopesContextField(chiClient *rest.Client) {
	ctx := context.Background()

	fmt.Println("=== POST /compute-gs — correct scope granted (compute:write) ===")
	resp, err := chiClient.Call(ctx, restapiclient.ComputeGSRouteWithWriteScope, auth.ComputeGSReq{X: 3, Y: 4})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  result: %+v\n", resp.(auth.ComputeGSResp))
	}
	fmt.Println()

	fmt.Println("=== POST /compute-gs — known credential, WRONG scope granted (profile, not compute:write) ===")
	_, err = chiClient.Call(ctx, restapiclient.ComputeGSRouteWithWrongScope, auth.ComputeGSReq{X: 3, Y: 4})
	printStatusErr(err)
	fmt.Println()
}
