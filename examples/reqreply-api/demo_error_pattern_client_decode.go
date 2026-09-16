package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoErrorPatternClientDecode demonstrates reqreply.ErrorPattern's
// client-side decode workflow — the request-reply counterpart of
// examples/adapters-nethttp-client's "1b" REST demo. A single shared
// routes.ErrorPatternComputeRoute declaration drives BOTH the server's
// typed error reply (already shipped) AND the client's typed decode (new):
//
//   - happy path: a non-negative X succeeds normally.
//   - matched ErrorPattern: a negative X triggers routes.ConflictError,
//     matched by the route's declared ErrorPattern, and the CLIENT
//     recovers the SAME typed routes.ConflictPayload via errors.As into
//     mqtt5.ErrorPatternResponse — no manual body parsing needed.
//
// See docs/roadmap/error-handling-rest-events-reqreply.md's "Phase 0"
// section and docs/guides/asyncapi.md's "Client-side decode" section for
// the full design.
func demoErrorPatternClientDecode(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo: ErrorPattern client-side decode (mqtt5) ──")

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, built.Broker, built.Router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n  → call with a non-negative X (happy path):")
	respAny, err := client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: 3, Y: 4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(3 + 4) = %d\n", resp.Sum)

	fmt.Println("\n  → call with a negative X (matched ErrorPattern):")
	_, err = client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: -1, Y: 4})
	var epr mqtt5adapter.ErrorPatternResponse
	if !errors.As(err, &epr) {
		fmt.Fprintf(os.Stderr, "expected mqtt5.ErrorPatternResponse, got: %v\n", err)
		os.Exit(1)
	}
	payload, ok := epr.Value.(routes.ConflictPayload)
	if !ok {
		fmt.Fprintf(os.Stderr, "expected routes.ConflictPayload, got: %T\n", epr.Value)
		os.Exit(1)
	}
	fmt.Printf("  ✓ rejected with typed ConflictPayload: code=%q reason=%q (recovered via errors.As, zero manual body parsing)\n",
		payload.Code, payload.Reason)
}
