package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/client"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/zeromqrouterserver"
)

// demoZeroMQDealerRouterVariant shows the ROUTER/DEALER socket-topology
// variant (carried over from the deleted
// examples/adapters-zeromq-dealer-router): one ROUTER can multiplex many
// DEALER clients, unlike the point-to-point REQ/REP topology used by Demo
// 4. It also demonstrates zeromq.AttachRouterServer's upfront
// [zeromq.MissingSocketError] when a registered route has no socket
// wired for it — returned BEFORE reqreply.Server.Serve ever runs.
func demoZeroMQDealerRouterVariant(ctx context.Context) {
	fmt.Println("\n── Demo 8: zeromq ROUTER/DEALER variant + MissingSocketError ──")

	built, err := zeromqrouterserver.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zeromqrouterserver.Build error: %v\n", err)
		os.Exit(1)
	}
	go func() {
		_ = built.Server.Serve(ctx)
	}()

	dealerClient, err := client.BuildZeroMQDealer("compute/router-add", built.ClientSocket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BuildZeroMQDealer error: %v\n", err)
		os.Exit(1)
	}
	respAny, err := dealerClient.Call(ctx, routes.RouterComputeRoute, routes.ComputeReq{X: 9, Y: 10})
	if err != nil {
		fmt.Fprintf(os.Stderr, "dealer call error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ ROUTER/DEALER compute(9 + 10) = %d\n", resp.Sum)

	fmt.Println("\n  → deliberately misconfigured server (MissingSocketRoute registered, no socket wired):")
	_, err = zeromqrouterserver.BuildWithMissingSocket()
	var missingErr zeromq.MissingSocketError
	if errors.As(err, &missingErr) {
		fmt.Printf("  ✓ zeromq.AttachRouterServer rejected upfront: topic=%q (before Serve ever ran)\n", missingErr.Topic)
	} else {
		fmt.Fprintf(os.Stderr, "expected MissingSocketError, got: %v\n", err)
		os.Exit(1)
	}
}
