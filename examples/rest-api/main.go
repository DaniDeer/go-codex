// Package rest-api demonstrates go-codex's declare → assemble workflow as
// a small, real, multi-package project — not one big file — showing that
// route/middleware declarations, business logic, and server assembly are
// genuinely separable, adapter-agnostic concerns:
//
//	routes/         — domain models, codecs, and PLAIN business route
//	                  declarations — every rest.Route is an UNATTACHED
//	                  spec value here, no handler/HandleMW/ClientMW yet.
//	auth/           — a SELF-CONTAINED auth module: codecs + middleware
//	                  declarations + verifier/handler IMPLEMENTATIONS +
//	                  auth-flow demo ROUTES (LoginRoute, ComputeGSRoute)
//	                  all together — models how a real service would
//	                  factor out a reusable auth library. routes/ imports
//	                  auth.X to ATTACH its middleware to plain business
//	                  routes (one-way dependency, no cycle).
//	observer/       — a reusable, "standardized" observer/timing
//	                  middleware module (general-purpose timing Fn +
//	                  CountingObserver) — this example's own mock stands
//	                  in for what would be a real OTEL integration reused
//	                  across services.
//	requestid/      — a SELF-CONTAINED, generic (Req-agnostic) reusable-
//	                  class middleware module (ReusableRequestIDMw) —
//	                  logs an optional "X-Demo-Request-Id" header,
//	                  attachable to ANY route without per-route wrapping,
//	                  demonstrating Class 1 (reusable) of the bound-
//	                  middleware split alongside auth/'s Class 2 (bound)
//	                  examples.
//	handlers/       — SERVER-side business logic, adapter-agnostic (works
//	                  identically whether nethttp or chi supplies the
//	                  *http.Request).
//	chiserver/      — assembles routes/+auth/+handlers/ onto adapters/chi.
//	nethttpserver/  — assembles the SAME routes/+handlers/ onto
//	                  adapters/nethttp — proving the declarations
//	                  themselves are adapter-agnostic.
//	client/         — CLIENT-side credential + general-purpose middleware
//	                  variants of the SAME routes/ declarations, built on
//	                  adapters/nethttp's rest.Client — used to call BOTH
//	                  servers, since a declared rest.Route/rest.Client
//	                  pair is transport-agnostic on the wire.
//	demo_*.go       — one file per route/concern, assembling the demo
//	                  calls that exercise the above.
//	main.go         — this file: builds both servers, builds the client,
//	                  runs every demo in narrative order.
//
// See also examples/rest-builder (transport-agnostic builder core, no
// adapter), examples/rest-schema-docs (schema-only, no routes), and
// examples/rest-nested-binary (nested-struct merge + non-JSON body format).
//
// Run with: go run ./examples/rest-api
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/DaniDeer/go-codex/examples/rest-api/chiserver"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/handlers"
	"github.com/DaniDeer/go-codex/examples/rest-api/nethttpserver"
	"github.com/DaniDeer/go-codex/examples/rest-api/observer"
	"github.com/DaniDeer/go-codex/stats"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	store := handlers.NewUserStore()
	metrics := &observer.CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "http")))

	// ── Build both servers — SAME routes/handlers, different adapters ──────
	chiAddr := mustFreeAddr()
	chiBuilt, err := chiserver.Build(store, obs, logger, chiAddr)
	must(err, "build chi server")
	chiCtx, chiCancel := context.WithCancel(context.Background())
	defer chiCancel()
	go func() { _ = chiBuilt.Server.Serve(chiCtx) }()
	waitForReady(chiAddr)

	nethttpAddr := mustFreeAddr()
	nethttpBuilt, err := nethttpserver.Build(store, obs, logger, nethttpAddr)
	must(err, "build net/http server")
	nethttpCtx, nethttpCancel := context.WithCancel(context.Background())
	defer nethttpCancel()
	go func() { _ = nethttpBuilt.Server.Serve(nethttpCtx) }()
	waitForReady(nethttpAddr)

	// ── Build the client(s) — adapters/nethttp against EITHER server ───────
	chiClient, err := restapiclient.Build(http.DefaultClient, "http://"+chiAddr)
	must(err, "build chi client")
	nethttpClient, err := restapiclient.Build(http.DefaultClient, "http://"+nethttpAddr)
	must(err, "build net/http client")

	fmt.Println("=== rest-api demo: declare → assemble (chi + net/http) → client tests server ===")
	fmt.Println()

	demoLogin(chiClient, nethttpClient)
	demoCreateUser(chiClient, "http://"+chiAddr)
	demoGetUser(chiClient, chiBuilt.GetUserHandle)
	demoUpdateUser(chiClient)
	demoListUsers(chiClient, "http://"+chiAddr)
	demoProfile(chiClient)
	demoAdminAction(chiClient)
	demoGrantedScopesContextField(chiClient)
	demoBoundMiddlewareSplit(chiClient)
	demoRouterGroups()
	demoRouterWithScoping()
	demoResponseHeaderCookieViolation()
	demoResponseBodyViolation()
	demoErrorPatternDeclarationMechanisms()
	demoErrorPatternActions()
	demoErrorPatternClientMatchMechanisms()
	demoErrorPatternMiddlewareCombo()
	demoErrorPatternPortAdapter()
	demoSetCookie()
	demoCapabilityMechanism()
	demoSpecEndpoint(chiAddr, nethttpAddr)

	fmt.Println("=== Observer summary (merged: both servers) ===")
	metrics.Print()
	fmt.Println()

	fmt.Println("=== OpenAPI 3.1 spec (chi server — identical to net/http server, same routes/) ===")
	doc, err := chiBuilt.Server.OpenAPISpec()
	must(err, "build OpenAPI spec")
	yamlBytes, err := doc.MarshalYAML()
	must(err, "marshal OpenAPI spec")
	fmt.Print(string(yamlBytes))

}
