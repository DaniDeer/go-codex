// Package observer models a reusable, "standardized" observer/timing
// middleware a real service would share across routes — this example's
// own mock stands in for what would be a real OTEL integration reused
// across services (mirrors examples/reqreply-api's/examples/events-api's
// own observer/ packages, naming-consistent across all 3 example
// projects).
package observer

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// ── General-purpose (timing) middleware ──────────────────────────────────────
//
// A genuinely distinct concern from security and stats.Observer:
// per-request/per-call TIMING, logged independently. Demonstrates the
// general-purpose (unpaired, Satisfies-empty) Fn shape on BOTH roles —
// server func(http.Handler) http.Handler (the SAME shape
// nethttp.Observability/chi's reuse of it already use) and client
// func(next func(ctx,Req)(Resp,error)) func(ctx,Req)(Resp,error) (the
// general-purpose ClientMW shape shipped in docs/design/
// d-0001-rest-middleware-workflow-simplification.md's Addendum 3,
// extended to Client.Call in Addendum 5) — attached via
// .HandleMW(nil, ...)/.ClientMW(nil, ...) respectively, never paired
// against any security scheme.

// TimingServerMW returns a general-purpose server-side middleware Fn
// (func(http.Handler) http.Handler) that logs each request's duration —
// attach via Route.HandleMW(nil, observer.TimingServerMW(logger)).
func TimingServerMW(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			logger.Info("timing", "side", "server", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
		})
	}
}

// TimingClientMW returns a general-purpose client-side middleware Fn
// (func(next func(ctx,Req)(Resp,error)) func(ctx,Req)(Resp,error)) that
// logs each call's duration — attach via
// Route.ClientMW(nil, observer.TimingClientMW[Req,Resp](logger)). A
// separate instantiation is needed per Req/Resp pair (Go forbids a value
// having its own type parameters), mirroring the shape
// adapters/mqtt5's wrapPublishGeneral's callers already instantiate
// per-T.
func TimingClientMW[Req, Resp any](logger *slog.Logger) func(next func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(next func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
		return func(ctx context.Context, req Req) (Resp, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			logger.Info("timing", "side", "client", "duration", time.Since(start), "err", err)
			return resp, err
		}
	}
}
