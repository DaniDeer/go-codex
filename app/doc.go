// Package app is a minimal application lifecycle manager for go-codex
// services: one root context with the observer pre-injected, supervised
// goroutines with fail-fast semantics, and ordered (LIFO) shutdown hooks.
//
// It is a shutdown-ordering helper, not a framework: ports and adapters know
// nothing about it — App owns the context they are bound with and runs the
// teardown hooks main() would otherwise choreograph by hand.
//
//	a := app.New(app.Options{Observer: obs, Logger: logger})
//	ctx := a.Context() // observer pre-injected; cancelled on shutdown
//
//	exports.Bind(ctx, file.DrainWriteFileAdapter(exportFile, varsFor, opts))
//	exports.Start(ctx)
//	a.OnShutdown("exports", func(context.Context) error { return exports.Close() })
//
//	a.Go("alerts-feed", func(ctx context.Context) error {
//	    alerts.Feed(ctx, alertPayloads) // returns on ctx cancel
//	    return nil
//	})
//
//	if err := a.Run(context.Background()); err != nil { // SIGINT/SIGTERM → ordered teardown
//	    slog.Error("shutdown finished with errors", "error", err)
//	}
//
// Demos and tests that are not signal-driven call [App.Shutdown] directly
// instead of [App.Run] — both share the same teardown path.
//
// Error policy is fail-fast, errgroup-style: the first supervised goroutine
// that returns a non-nil error cancels the app; all goroutine and hook errors
// are still collected into the errors.Join result of Run/Shutdown. Adapters
// that should survive errors handle them internally (per-adapter OnError) and
// return nil.
package app
