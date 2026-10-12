package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoForward demonstrates docs/roadmap/reqreply-forward-bounce.md's
// Forward/Bounce feature end to end: ForwardOriginatingRoute's own
// handler (handlers.ForwardToDouble) ALWAYS returns
// reqreply.Forward(routes.ForwardTargetRoute, ...) instead of a normal
// response — the SERVER signals "ask ForwardTargetRoute instead" within
// the SAME correlation-ID round trip, and mqtt5Client.Call's CENTRALIZED
// chain-following loop (api/reqreply.Client, not the adapter) resolves
// the target against its registry, republishes, and decodes
// ForwardTargetRoute's OWN typed response — all transparently to this
// demo's caller, which sees ONE ordinary Call return ForwardTargetRoute's
// response directly.
//
// ForwardTargetRoute must be REGISTERED on the client BEFORE the forward
// is followed — mqtt5Client.RegisterRoute "warms" the registry without a
// live call, exactly docs/roadmap/reqreply-client-registry.md's own
// documented use case for a route "only ever reached as a Forward
// target."
func demoForward(ctx context.Context, mqtt5Client *reqreply.Client) {
	fmt.Println("\n── Demo: Forward/Bounce — the server redirects, the client auto-follows ──")

	if err := mqtt5Client.RegisterRoute(routes.ForwardTargetRoute); err != nil {
		fmt.Fprintf(os.Stderr, "RegisterRoute(ForwardTargetRoute): %v\n", err)
		os.Exit(1)
	}

	respAny, err := mqtt5Client.Call(ctx, routes.ForwardOriginatingRoute, routes.ComputeReq{X: 1, Y: 2})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Call(ForwardOriginatingRoute) error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	// ForwardToDouble forwards {X:1*10, Y:2*10} = {X:10, Y:20} to
	// ForwardTargetRoute (handlers.Double: (X+Y)*2) → (10+20)*2 = 60.
	fmt.Printf("  ✓ Call(ForwardOriginatingRoute) auto-followed to ForwardTargetRoute, resolved: Sum=%d\n", resp.Sum)
	if resp.Sum != 60 {
		fmt.Fprintf(os.Stderr, "  unexpected Sum: want 60, got %d\n", resp.Sum)
		os.Exit(1)
	}
}
