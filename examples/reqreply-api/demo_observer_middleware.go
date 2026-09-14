package main

import (
	"context"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/observability"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoObserverMiddleware demonstrates the CLIENT-side half of the
// shipped, library-owned [reqreply.Observability] general-purpose
// middleware — the SERVER-side half is already exercised by every OTHER
// demo in this file (zeromqserver.Build/zeromqrouterserver.Build attach
// [reqreply.Observability] via .HandleMW(nil, ...) on every route;
// mqtt5server.Build attaches none, relying solely on the ctx-ambient
// Observer main.go injects before Serve — see mqtt5server.Build's own
// doc comment for why).
//
// Three scenarios:
//  1. A plain route (ComputeRoute) with ONLY .ClientMW(nil, ...)
//     attached, called through BOTH the mqtt5 AND zeromq clients —
//     proving [reqreply.Observability]'s ONE generic implementation is
//     reused, unchanged, across adapters (mirrors its own doc comment).
//  2. SecuredComputeRoute with BOTH a paired security ClientMW
//     (validBearerCredFn, reused from demo_route_level_security_
//     credential_error.go) AND the general-purpose observer ClientMW
//     attached together — proving the two mechanisms compose freely on
//     the client side exactly as they already do server-side.
//  3. Printing the shared [observability.DemoObserver]'s accumulated
//     Summary() — confirming events recorded by the ADAPTER layer
//     (adapters/mqtt5's/adapters/zeromq's own existing RecordRequest
//     calls, already reachable because [reqreply.Observability] injects
//     the SAME obs into ctx) landed in the SAME Observer value
//     [reqreply.Observability] itself was constructed with.
func demoObserverMiddleware(ctx context.Context, obs *observability.DemoObserver, mqtt5Built *mqtt5server.Built, mqtt5Client, zeromqClient *reqreply.Client) {
	fmt.Println("\n── Demo 11: general-purpose observer middleware (.HandleMW/.ClientMW) ──")

	fmt.Println("\n  → ComputeRoute + .ClientMW(nil, reqreply.Observability) via mqtt5:")
	observedRoute := routes.ComputeRoute.
		ClientMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs))
	respAny, err := mqtt5Client.Call(ctx, observedRoute, routes.ComputeReq{X: 3, Y: 4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(3 + 4) = %d (mqtt5, logged via shared Observer)\n", resp.Sum)

	fmt.Println("\n  → the SAME reqreply.Observability[Req,Resp] implementation, via zeromq:")
	respAny, err = zeromqClient.Call(ctx, observedRoute, routes.ComputeReq{X: 5, Y: 6})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp = respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(5 + 6) = %d (zeromq, same decorator reused unchanged)\n", resp.Sum)

	fmt.Println("\n  → SecuredComputeRoute: security ClientMW + observer ClientMW composed together:")
	securedObservedRoute := routes.SecuredComputeRoute.
		Use(routes.BearerAuthMw).
		ClientMW(&routes.BearerAuthMw, validBearerCredFn).
		ClientMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs))
	securedClient := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(securedClient, mqtt5Built.Broker, mqtt5Built.Router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}
	respAny, err = securedClient.Call(ctx, securedObservedRoute, routes.ComputeReq{X: 9, Y: 10})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp = respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(9 + 10) = %d (credential + observer middleware both ran, in attachment order)\n", resp.Sum)

	requests, rejected := obs.Summary()
	fmt.Printf("\n  ✓ shared Observer summary: %d requests recorded (adapter layer, reached via reqreply.Observability's ctx injection), %d security rejections\n", requests, rejected)
}
