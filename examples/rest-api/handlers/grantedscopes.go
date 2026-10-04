package handlers

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// gsTokenScopes is a mock credential store for the GrantedScopes demo —
// deliberately separate from tokenScopes above (a different scheme,
// "bearerAuthGS", with its own scope vocabulary).
var gsTokenScopes = map[string][]string{
	"valid-compute-token":  {"compute:write"},
	"valid-readonly-token": {"profile"}, // lacks "compute:write" — proves rejection
}

// VerifyBearerGS is GrantedScopesComputeMw's paired, BOUND HandleMW Fn —
// func(ctx, *Req, In) (Out, error), detected via its own reflected
// signature (docs/design/d-0007-declarative-middleware-layering.md's
// Architecture revision). Unlike ScopesImpl[Req] above (which wraps a
// RAW *http.Request), this Fn receives routes.AuthIn ALREADY DECODED from
// the Authorization header by the shared middleware.DecodeLayer
// mechanism — no manual r.Header.Get/TrimPrefix needed.
func VerifyBearerGS(_ context.Context, _ *routes.ComputeGSReq, in routes.AuthIn) (routes.AuthOut, error) {
	scopes, ok := gsTokenScopes[in.Token]
	if !ok {
		return routes.AuthOut{}, fmt.Errorf("unknown or expired token %q", in.Token)
	}
	return routes.AuthOut{GrantedScopes: map[string][]string{"bearerAuthGS": scopes}}, nil
}

// MakeComputeGSHandler is the real business handler — it never touches
// the Authorization header itself. The authenticated token reaches it
// PURELY via routes.GrantedScopesUserIDField.Get(ctx), published by
// VerifyBearerGS above through SetContextFieldFromIn — the concrete
// "zero manual re-decoding" promise docs/design/
// d-0007-declarative-middleware-layering.md's Phase 3 makes.
func MakeComputeGSHandler() func(ctx context.Context, req routes.ComputeGSReq) (routes.ComputeGSResp, error) {
	return func(ctx context.Context, req routes.ComputeGSReq) (routes.ComputeGSResp, error) {
		token, ok := routes.GrantedScopesUserIDField.Get(ctx)
		fmt.Printf("  [handler] authenticated token (via ContextField, ok=%v): %q\n", ok, token)
		return routes.ComputeGSResp{Sum: req.X + req.Y}, nil
	}
}
