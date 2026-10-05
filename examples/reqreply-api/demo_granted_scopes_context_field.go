package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoGrantedScopesContextField exercises routes.ComputeGSRoute — the
// BOUND reqreply.BoundMiddleware[Req,In,Out] (routes.
// NewGrantedScopesComputeMw) dispatched through HandleBoundMW,
// with a REAL GrantedScopes-carrying Out enforced by
// middleware.CheckScopes, AND the authenticated identity propagated to
// the real handler via middleware.ContextField (routes.
// GrantedScopesUserIDField, published via SetContextFieldFromOut — see
// routes/grantedscopes_demo.go's own doc comment for why FromOut, not
// FromIn, for zeromq specifically). See docs/design/
// d-0007-declarative-middleware-layering.md for the full design this
// demo exercises end-to-end.
func demoGrantedScopesContextField(ctx context.Context, zeromqClient *reqreply.Client) {
	fmt.Println("\n── Demo: GrantedScopes + ContextField (reqreply, zeromq, bound HandleMW) ──")

	fmt.Println("  → correct scope granted (compute:write):")
	resp, err := zeromqClient.Call(ctx, routes.ComputeGSRoute, routes.ComputeGSReq{X: 3, Y: 4, Token: "valid-compute-token"})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  result: %+v\n", resp.(routes.ComputeResp))
	}

	fmt.Println("  → known token, WRONG scope granted (profile, not compute:write):")
	_, err = zeromqClient.Call(ctx, routes.ComputeGSRoute, routes.ComputeGSReq{X: 3, Y: 4, Token: "valid-readonly-token"})
	if err != nil {
		fmt.Printf("  rejected as expected: %v\n", err)
	} else {
		fmt.Println("  ✗ unexpectedly succeeded — BUG")
	}
}
