package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/auth"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/observer"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoBoundMiddlewareSplit exercises routes.StackedDemoRoute (mqtt5
// only) — a direct, side-by-side contrast of the two classes
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 introduced, mirroring
// examples/rest-api's/examples/events-api's own identically-named demo
// files:
//
//  1. REUSABLE ALONE (Class 1) — already fully exercised by
//     demoRouteLevelSecurityCredentialError/demoObserverMiddleware above
//     ([auth.BearerAuthMw], mqtt5's property-decoded credential) — not
//     repeated here.
//  2. BOUND ALONE (Class 2) — already fully exercised by
//     demoCrossAPIOAuth2Sharing above ([auth.NewOAuthMwReqreply]
//     attached to [routes.OAuthComputeRoute], zeromq's in-payload
//     credential — SERVER-side only, confirmed: there is deliberately NO
//     BoundClientMiddleware for this case) — not repeated here.
//  3. STACKED (both together, ONE route) — [routes.StackedDemoRoute],
//     attached server-side in mqtt5server/server.go with BOTH
//     .Use(BearerAuthMw.WithReceive(...)) (the "bearerAuth" scheme) AND
//     .HandleBoundMW(NewOAuthMwReqreply(...)) (the DIFFERENT
//     "oauth2Compute" scheme, reused UNCHANGED from
//     routes.OAuthComputeRoute's own zeromq attachment, proving a bound
//     value attaches across MULTIPLE routes too) — different scheme
//     names, so no [reqreply.DuplicateMiddlewareNameError]. This demo
//     ALSO composes the general-purpose observer middleware
//     client-side (.ClientMW(nil, reqreply.Observability(obs))),
//     mirroring demoObserverMiddleware's own 3-way composition, to show
//     all THREE attachment kinds — reusable, bound, and general-purpose
//     — working together on one call.
func demoBoundMiddlewareSplit(ctx context.Context, obs *observer.DemoObserver, mqtt5Client *reqreply.Client) {
	fmt.Println("\n── Demo: bound-middleware-split — reusable + bound STACKED on one route ──")

	// StackedDemoRoute is Mounted under mqtt5server.Build's shared
	// "compute" Router (docs/design/d-0008-declarative-router-groups.md)
	// — compose its RELATIVE topic ("stacked-demo") back to
	// "compute/stacked-demo" via ClientHandle(WithRouter(...)).
	stackedHandle := routes.StackedDemoRoute.
		Use(auth.BearerAuthMw.WithSend(validBearerCredFn)).
		ClientMW(nil, reqreply.Observability[routes.OAuthComputeReq, routes.OAuthComputeResp](obs)).
		ClientHandle(reqreply.WithRouter(reqreply.NewRouter("compute")))

	respAny, err := mqtt5Client.Call(ctx, stackedHandle, routes.OAuthComputeReq{X: 11, Y: 12, Token: "valid-compute-write-token"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.OAuthComputeResp)
	fmt.Printf("  ✓ compute(11 + 12) = %d (bearerAuth property credential + oauth2Compute in-payload credential + observer, all composed)\n", resp.Sum)

	fmt.Println("  → same route, WRONG oauth2 token (expect rejection from the BOUND half):")
	_, err = mqtt5Client.Call(ctx, stackedHandle, routes.OAuthComputeReq{X: 1, Y: 1, Token: "expired-token"})
	if err == nil {
		fmt.Fprintln(os.Stderr, "unexpected success — expected a security rejection")
		os.Exit(1)
	}
	fmt.Printf("  ✓ rejected as expected: %v\n", err)
}
