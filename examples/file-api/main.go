// Package main demonstrates [file.Client], go-codex's transport-agnostic
// file-resource API (docs/roadmap/file-api.md), as a small, real,
// multi-package project — not one big file — mirroring
// examples/rest-api/events-api/reqreply-api's own layout:
//
//	resources/   — the shared contract package: domain models, codecs,
//	               and [file.Resource] declarations (Notes, Archive,
//	               CategorizedNotes, MetadataNotes, StreamableNotes) —
//	               every Resource here is an UNATTACHED, relative-path
//	               declaration; main.go's [file.Router] supplies the
//	               runtime temp-directory prefix at Handle-build time.
//	observer/    — a reusable [stats.FileObserver] implementation
//	               (CountingObserver), mirrors examples/rest-api/
//	               observer's own CountingObserver.
//	demo_*.go    — one file per concern: declare+attach, CRUD verbs,
//	               list+exists, copy/move/mkdir/batchdelete, router
//	               groups, the BoundMiddleware Class 1/2 split, granted
//	               scopes, the Capability mechanism, ErrorPattern,
//	               Observability, the file-metadata merge-field axis,
//	               Conditional writes, Recursive WithList, and
//	               Streaming/chunked I/O (both mechanisms).
//	main.go      — this file: temp-dir + Router + Client/Backend setup,
//	               Observer wiring via context, runs every demo in
//	               narrative order, prints the final summary.
//
// Run with: go run ./examples/file-api
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	fileapiobserver "github.com/DaniDeer/go-codex/examples/file-api/observer"
	"github.com/DaniDeer/go-codex/stats"
)

func main() {
	// NOTE: deliberately NOT calling slog.SetDefault here — doing so
	// bridges the PLAIN "log" package's own output (log.Fatalf, used
	// throughout this example's demo_*.go files for fail-fast error
	// checks) through slog's level filter, which would silently
	// swallow every log.Fatalf message below the configured level
	// (discovered the hard way while building this example — see
	// git history for the full story). logger is passed DIRECTLY to
	// stats.NewLoggingObserver below instead, which takes a *slog.Logger
	// parameter and needs no global default at all.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dir, err := os.MkdirTemp("", "file-api-example-*")
	if err != nil {
		log.Fatalf("setup: %v", err)
	}
	defer os.RemoveAll(dir)

	// Router: a genuine value type (mirrors events.Router) — its prefix
	// IS the runtime temp directory, replacing manual filepath.Join
	// entirely. A real application would use this exact mechanism for a
	// multi-tenant path prefix ("tenants/{tenant}") or any other
	// runtime-determined directory.
	rt := file.NewRouter(dir)

	// Client + Backend: WHERE notes actually live. Swapping this one
	// line for a future adapters/s3.NewBackend()/adapters/smb.NewBackend()
	// is the entire migration a new backend requires — every Resource
	// declaration in resources/ never changes.
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		log.Fatalf("attach: %v", err)
	}

	// Observer: a fanout of CountingObserver (this example's own summary
	// counter) + stats.LoggingObserver — wired into ctx BEFORE any demo
	// runs, so every dispatch call below is observed uniformly.
	counting := &fileapiobserver.CountingObserver{}
	obs := stats.NewFanout(counting, stats.NewLoggingObserver(logger))
	ctx := stats.WithObserver(context.Background(), obs)

	fmt.Println("=== file-api demo: declare -> attach -> dispatch, all 12 verbs + Router + Middleware + Capability + ErrorPattern + Observer ===")
	fmt.Println()

	demoDeclareAttach(ctx, client, rt)
	demoCrudVerbs(ctx, client, rt)
	demoListAndExists(ctx, client, rt, dir)
	demoCopyMoveMkdirBatchDelete(ctx, client, rt)
	demoRouterGroups(ctx, client, dir)
	demoBoundMiddlewareSplit(ctx, client, rt)
	demoGrantedScopesContextField(ctx, client, rt)
	demoCapabilityMechanism(ctx, client, rt, dir)
	demoErrorPattern(ctx, client, rt)
	demoObservability(ctx, client, rt)
	demoMetadataAxis(ctx, client, rt)
	demoConditionalWrites(ctx, client, rt)
	demoRecursiveList(ctx, client, rt, dir)
	demoStreamingIO(ctx, client, rt)

	fmt.Println()
	fmt.Println("=== Observer summary ===")
	counting.Print()
}
