package handlers

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// gsTokenScopes is a mock credential store for the GrantedScopes demo —
// deliberately separate from any other scheme's own credential store (a
// different scheme, "bearerAuthGS", with its own scope vocabulary).
var gsTokenScopes = map[string][]string{
	"valid-compute-token":  {"compute:write"},
	"valid-readonly-token": {"profile"}, // lacks "compute:write" — proves rejection
}

// VerifyBearerGS is routes.NewGrantedScopesComputeMw's embedded Fn —
// func(ctx, *Req, In) (Out, error), attached via
// routes.ComputeGSRoute.HandleBoundMW(routes.NewGrantedScopesComputeMw(VerifyBearerGS)).
// Unlike the generic merge-field case, zeromq has no property/header
// side channel for In to decode FROM (routes.AuthIn is deliberately
// empty — see its own doc comment), so this Fn reads the credential
// directly off its OWN *Req parameter instead — the SAME established
// pattern VerifyOAuthComputeZeroMQ uses. Returns a GrantedScopes-carrying
// routes.AuthOut, PLUS Subject (the authenticated identity, propagated to
// the real handler via SetContextFieldFromOut).
func VerifyBearerGS(_ context.Context, req *routes.ComputeGSReq, _ routes.AuthIn) (routes.AuthOut, error) {
	scopes, ok := gsTokenScopes[req.Token]
	if !ok {
		return routes.AuthOut{}, fmt.Errorf("unknown or expired token %q", req.Token)
	}
	return routes.AuthOut{
		GrantedScopes: map[string][]string{"bearerAuthGS": scopes},
		Subject:       req.Token,
	}, nil
}

// MakeComputeGSHandler is the real business handler — it never touches
// req.Token itself. The authenticated token reaches it PURELY via
// routes.GrantedScopesUserIDField.Get(ctx), published by VerifyBearerGS
// above through SetContextFieldFromIn — the concrete "zero manual
// re-decoding" promise docs/design/
// d-0007-declarative-middleware-layering.md's Phase 3 makes.
func MakeComputeGSHandler() func(ctx context.Context, req routes.ComputeGSReq) (routes.ComputeResp, error) {
	return func(ctx context.Context, req routes.ComputeGSReq) (routes.ComputeResp, error) {
		token, ok := routes.GrantedScopesUserIDField.Get(ctx)
		fmt.Printf("  [handler] authenticated token (via ContextField, ok=%v): %q\n", ok, token)
		return routes.ComputeResp{Sum: req.X + req.Y}, nil
	}
}
