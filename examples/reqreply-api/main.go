// Package reqreply-api demonstrates go-codex's request/reply declare →
// assemble workflow as a small, real, multi-package project — not one big
// file — mirroring examples/rest-api's own layout:
//
//	routes/              — domain models, codecs, and route declarations
//	                       (plain routes, route-level security, global-
//	                       security-only) — every reqreply.Route is an
//	                       UNATTACHED spec value here, no handler yet.
//	handlers/            — SERVER-side business logic, adapter-agnostic.
//	mqtt5server/          — assembles routes/+handlers/ onto adapters/mqtt5
//	                       (in-process mock broker, no real MQTT 5 broker
//	                       needed).
//	zeromqserver/         — assembles ComputeRoute/DoubleRoute/TripleRoute
//	                       onto adapters/zeromq's REQ/REP socket family (one
//	                       socket pair per route — REQ/REP is point-to-
//	                       point).
//	zeromqrouterserver/   — assembles RouterComputeRoute onto adapters/
//	                       zeromq's ROUTER/DEALER socket family (one ROUTER
//	                       can multiplex many DEALER clients).
//	client/               — reqreply.Client constructors for each of the 3
//	                       adapter-attachment modes above.
//	demo_*.go             — one file per concern, assembling the demo calls
//	                       that exercise the above.
//	main.go               — this file: builds every server, builds every
//	                       client, runs every demo in narrative order.
//
// Run with: go run ./examples/reqreply-api
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	reqreplyapiclient "github.com/DaniDeer/go-codex/examples/reqreply-api/client"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/zeromqserver"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Build every server ──────────────────────────────────────────────
	mqtt5Built, err := mqtt5server.Build()
	must(err, "build mqtt5 server")
	go func() {
		_ = mqtt5Built.Server.Serve(ctx)
	}()
	// mqtt5's ServerTransport.Serve registers its router handler
	// synchronously — but ONLY once its dispatch goroutine (started by
	// Server.Serve above) actually runs; a brief sleep here avoids a
	// startup race against the very first Client.Call below (mirrors
	// examples/adapters-mqtt5's own time.Sleep(50ms) convention after
	// registering a handler/subscription).
	time.Sleep(50 * time.Millisecond)

	zeromqBuilt, err := zeromqserver.Build()
	must(err, "build zeromq REQ/REP server")
	go func() {
		_ = zeromqBuilt.Server.Serve(ctx)
	}()

	// ── Build clients attached to those servers ─────────────────────────
	mqtt5Client, err := reqreplyapiclient.BuildMQTT5(mqtt5Built.Broker, mqtt5Built.Router)
	must(err, "build mqtt5 client")

	zeromqClient, err := reqreplyapiclient.BuildZeroMQ(zeromqBuilt.ClientSockets)
	must(err, "build zeromq client")

	// ── Run every demo in narrative order ───────────────────────────────
	demoBasicCallAndServe(ctx, mqtt5Client)
	demoGlobalSecurityDualModeCall(ctx, mqtt5Built, mqtt5Client, mqtt5Built.GlobalHandle)
	demoRouteLevelSecurityCredentialError(ctx, mqtt5Built, mqtt5Built.SecuredHandle)
	demoConcurrentMultiRouteDispatch(ctx, zeromqClient)
	demoCallAsyncFuture(ctx, mqtt5Client)
	demoSpecPrintingAsyncAPI(mqtt5Built.Server)
	demoZeroMQDealerRouterVariant(ctx)

	fmt.Println("\n✓ all reqreply-api demos completed successfully")
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
