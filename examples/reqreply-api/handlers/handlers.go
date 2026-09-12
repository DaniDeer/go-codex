// Package handlers holds SERVER-side business logic for this example —
// adapter-agnostic (works identically whether mqtt5 or zeromq supplies the
// decoded request), mirroring examples/rest-api/handlers' identical
// separation of concerns.
package handlers

import (
	"context"

	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// Add is the baseline handler: sums X and Y. Used by ComputeRoute,
// SecuredComputeRoute, GlobalOnlyComputeRoute, and RouterComputeRoute — the
// same domain logic, wired to different transports/security postures.
func Add(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
	return routes.ComputeResp{Sum: req.X + req.Y}, nil
}

// Double sums X and Y, then doubles the result. Used by DoubleRoute in
// Demo 4's concurrent multi-route dispatch.
func Double(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
	return routes.ComputeResp{Sum: (req.X + req.Y) * 2}, nil
}

// Triple sums X and Y, then triples the result. Used by TripleRoute in
// Demo 4's concurrent multi-route dispatch.
func Triple(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
	return routes.ComputeResp{Sum: (req.X + req.Y) * 3}, nil
}

// AddOAuth is OAuthComputeRoute's handler — same domain logic as Add,
// against the OAuthComputeReq/Resp pair (which carries an in-payload
// Token field the zeromq security Fn already validated before this runs).
func AddOAuth(_ context.Context, req routes.OAuthComputeReq) (routes.OAuthComputeResp, error) {
	return routes.OAuthComputeResp{Sum: req.X + req.Y}, nil
}
