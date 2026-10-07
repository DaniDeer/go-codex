package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoBasicCallAndServe is the simplest possible shape: mqtt5Client.Call
// against a RAW, unregistered routes.ComputeRoute — no reqreply.Server
// involvement client-side at all, mirroring rest.Route.ClientHandle's own
// zero-Server usage. The ONE addition Router-grouping the server side
// requires: ComputeRoute's declared topic is now RELATIVE ("add"),
// composed back to "compute/add" only via mqtt5server.Build's "compute"
// Mount (docs/design/d-0008-declarative-router-groups.md) — so the
// client must apply the SAME composition via
// ClientHandle(WithRouter(...)) before calling, still with NO
// reqreply.Server involved client-side at all.
func demoBasicCallAndServe(ctx context.Context, mqtt5Client *reqreply.Client) {
	fmt.Println("\n── Demo 1: basic call + serve (raw Route, no Server needed client-side) ──")

	req := routes.ComputeReq{X: 3, Y: 4}
	respAny, err := mqtt5Client.Call(ctx, routes.ComputeRoute.ClientHandle(reqreply.WithRouter(reqreply.NewRouter("compute"))), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "call error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  compute(%d + %d) = %d\n", req.X, req.Y, resp.Sum)
}
