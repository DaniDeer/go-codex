// Package reqreply-api demonstrates go-codex's request/reply declare →
// assemble workflow as a small, real, multi-package project — not one big
// file — mirroring examples/rest-api's own layout:
//
//	routes/              — domain models, codecs, and PLAIN business route
//	                       declarations — every reqreply.Route is an
//	                       UNATTACHED spec value here, no handler yet.
//	auth/                — a SELF-CONTAINED auth module: codecs +
//	                       middleware declarations + verifier/handler
//	                       IMPLEMENTATIONS + auth-flow demo routes
//	                       (ComputeGSRoute) all together — models how a
//	                       real service would factor out a reusable auth
//	                       library. routes/ imports auth.X to ATTACH its
//	                       middleware to plain business routes (one-way
//	                       dependency, no cycle).
//	propertyaxis/        — a SELF-CONTAINED, non-security property-
//	                       vocabulary-axis module (NewTenantPropertyMw +
//	                       its In/Out vocabulary/codecs + ProcessTenant
//	                       implementation) — the enrichment-middleware
//	                       analogue of auth/, kept separate since it is
//	                       NOT a credential/security concern.
//	handlers/            — SERVER-side business logic, adapter-agnostic.
//	observer/             — this example's [stats.Observer] implementation
//	                       ([observer.DemoObserver]), kept out of
//	                       routes/ (pure declaration) and handlers/
//	                       (per-route business logic) — the shipped,
//	                       library-owned [reqreply.Observability] general-
//	                       purpose middleware is used directly at every
//	                       zeromqserver/zeromqrouterserver/demo .HandleMW/
//	                       .ClientMW attachment point (mqtt5server attaches
//	                       none — its server-side ctx-ambient Observer,
//	                       injected once below, already covers it; see
//	                       mqtt5server.Build's own doc comment and
//	                       docs/features/observer.md's reqreply section).
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
	"log/slog"
	"os"
	"time"

	reqreplyapiclient "github.com/DaniDeer/go-codex/examples/reqreply-api/client"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/observer"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/zeromqserver"
	"github.com/DaniDeer/go-codex/stats"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// obs is shared across every layer: the ADAPTER layer (adapters/
	// mqtt5, adapters/zeromq's reqreply transports already call
	// stats.Observer on every dispatch path) and any route that
	// additionally attaches the shipped [reqreply.Observability](obs)
	// via .HandleMW(nil, ...)/.ClientMW(nil, ...) BOTH read/write through
	// this ONE value — stats.WithObserver injects it into ctx ONCE,
	// here, before any server/demo runs; mqtt5server.Build in particular
	// relies SOLELY on this ctx injection (see its own doc comment for
	// why it attaches no per-route observer helper).
	obs := observer.NewDemoObserver(slog.Default())
	ctx = stats.WithObserver(ctx, obs)

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
	// examples/events-api's own time.Sleep(20-50ms) convention after
	// registering a handler/subscription).
	time.Sleep(50 * time.Millisecond)

	zeromqBuilt, err := zeromqserver.Build(obs)
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
	demoGlobalSecurityDualModeCall(ctx, mqtt5Built, mqtt5Client)
	demoRouteLevelSecurityCredentialError(ctx, mqtt5Built)
	demoConcurrentMultiRouteDispatch(ctx, zeromqClient)
	demoCallAsyncFuture(ctx, mqtt5Client)
	demoUserPropertyParamMiddleware(ctx, mqtt5Built)
	demoPropertyAxisMiddleware(ctx, mqtt5Built, zeromqBuilt)
	demoSpecPrintingAsyncAPI(mqtt5Built.Server)
	demoServeSpecCall(ctx, mqtt5Client, mqtt5Built.SpecHandle)
	demoRouterGroups()
	demoZeroMQDealerRouterVariant(ctx, obs)
	demoCrossAPIOAuth2Sharing(ctx, zeromqBuilt)
	demoGrantedScopesContextField(ctx, zeromqClient)
	demoConnectSecuritySchemeRegistration()
	demoObserverMiddleware(ctx, obs, mqtt5Built, mqtt5Client, zeromqClient)
	demoBoundMiddlewareSplit(ctx, obs, mqtt5Client)
	demoErrorPatternDeclarationMechanisms(ctx)
	demoErrorPatternClientMatchMechanisms(ctx, mqtt5Built)
	demoErrorPatternDeadLetterFallback(ctx)
	demoErrorPatternMiddlewareCombo(ctx)
	demoErrorPatternPortAdapter(ctx)
	demoCapabilityMechanism(ctx, mqtt5Built)

	fmt.Println("\n✓ all reqreply-api demos completed successfully")
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
