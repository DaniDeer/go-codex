package routes

import "github.com/DaniDeer/go-codex/api/reqreply"

// ── Bound-middleware-split demo (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) ──
//
// A direct, side-by-side contrast of the two classes the split
// introduced, mirroring examples/rest-api's/examples/events-api's own
// demo_bound_middleware_split.go files:
//
//  1. REUSABLE ALONE (Class 1) — already fully exercised by
//     [BearerAuthMw] (.Use(BearerAuthMw.WithReceive(fn)), mqtt5's
//     property-decoded credential) — not re-demonstrated here.
//  2. BOUND ALONE (Class 2) — already fully exercised by
//     [NewOAuthMwReqreply] attached to [OAuthComputeRoute]
//     (.HandleBoundMW(...), zeromq's in-payload credential — the ONE
//     genuine motivating case this class exists for, SERVER-side only:
//     there is deliberately NO BoundClientMiddleware here — it cannot
//     write into the request BODY on any transport, so the client just
//     sets Token directly as an ordinary field, see
//     demo_cross_api_oauth2_sharing.go) — not re-demonstrated here.
//  3. STACKED (both together, ONE route) — [StackedDemoRoute] below:
//     .Use(BearerAuthMw.WithReceive(fn)) (the "bearerAuth" scheme,
//     property-decoded, generic across ANY Req type) run FIRST, then
//     .HandleBoundMW(NewOAuthMwReqreply(fn)) (the DIFFERENT
//     "oauth2Compute" scheme, reusing the SAME bound helper
//     OAuthComputeRoute already uses, generic ACROSS this bound value
//     being shared across 2 routes) run SECOND — different scheme
//     names, so no [DuplicateMiddlewareNameError] conflict — proving a
//     reusable and a bound attachment, for TWO DIFFERENT schemes, compose
//     on one route. Wired on mqtt5 (mqtt5server/server.go) since
//     BearerAuthMw's property merge field needs mqtt5's User Properties;
//     NewOAuthMwReqreply's Fn reads req.Token directly, so it works
//     identically regardless of transport.
var StackedDemoRoute = reqreply.NewRoute[OAuthComputeReq, OAuthComputeResp](
	"compute/stacked-demo",
	OAuthComputeReqCodec, OAuthComputeRespCodec,
	reqreply.RouteMeta{
		OperationID: "stackedDemoAdd",
		Summary:     "Reusable + bound middleware STACKED on one route (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7).",
	},
)
