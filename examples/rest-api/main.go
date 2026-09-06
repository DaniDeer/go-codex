// Package rest-api demonstrates go-codex's declare → assemble workflow as
// a small, real, multi-package project — not one big file — showing that
// route/middleware declarations, business logic, and server assembly are
// genuinely separable, adapter-agnostic concerns:
//
//	routes/         — domain models, codecs, and THREE middleware kinds
//	                  (security, observer, general-purpose timing) — every
//	                  rest.Route is an UNATTACHED spec value here, no
//	                  handler/HandleMW/ClientMW yet.
//	handlers/       — SERVER-side business logic + security enforcement,
//	                  adapter-agnostic (works identically whether nethttp
//	                  or chi supplies the *http.Request).
//	chiserver/      — assembles routes/+handlers/ onto adapters/chi.
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
	"github.com/DaniDeer/go-codex/stats"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	store := handlers.NewUserStore()
	metrics := &CountingObserver{}
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
	demoResponseHeaderCookieViolation()
	demoResponseBodyViolation()
	demoSetCookie()
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
