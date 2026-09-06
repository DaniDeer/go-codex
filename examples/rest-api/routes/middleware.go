package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Middleware kind 1: security ──────────────────────────────────────────────
//
// A middleware.Middleware carrying a SecurityDeclaration is the DECLARE-TIME
// half of a security requirement — pure spec data, no runtime behavior.
// Every secured route below attaches ONE of these via .Use(...). The
// RUNTIME half (server-side verification, client-side credential supply)
// is built SEPARATELY: handlers/security.go builds the server
// ServerImplementation, client/client.go builds the client credential Fn —
// both PAIRED against the SAME middleware.Middleware value declared here,
// so a scheme-name typo is caught at Register time
// (UnknownMiddlewareImplementationError), never silently.
//
// Two scopes are used across this example: "profile" (read one's own
// profile/user data) and "admin" (privileged actions). BearerCodec
// format-validates the raw credential BEFORE any ServerImplementation Fn
// runs, on both server and client (the client-side check mirrors the
// server's, per docs/design/d-0001-rest-middleware-workflow-simplification.md).

// BearerCodec validates a raw bearer token string's FORMAT (non-empty,
// well-formed) — shared by every security declaration below so the check
// is defined once.
var BearerCodec = codex.String().Refine(validate.BearerToken)

// ProfileScopeMw declares the "bearerAuth" scheme, requiring the
// "profile" scope — attached via .Use(ProfileScopeMw) on routes any
// authenticated user may call.
var ProfileScopeMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"profile"}, &BearerCodec)

// AdminScopeMw declares the SAME "bearerAuth" scheme, requiring the
// "admin" scope — attached via .Use(AdminScopeMw) on privileged routes.
var AdminScopeMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"admin"}, &BearerCodec)

// ── Middleware kind 2: observer ──────────────────────────────────────────────
//
// Observer is NOT a middleware.Middleware value — it's a runtime
// stats.Observer, resolved from CallOptions/ClientCallOptions or ctx (see
// docs/features/observer.md). It still counts as one of this example's
// three middleware KINDS: server-side it's attached exactly like the
// general-purpose timing middleware below, via
// Route.HandleMW(nil, nethttp.Observability(obs)) / chi's identical
// reuse of the same function — see chiserver/server.go and
// nethttpserver/server.go for the wiring.

// ── Middleware kind 3: general-purpose (timing) ──────────────────────────────
//
// A THIRD, genuinely distinct concern from security and observability:
// per-request/per-call TIMING, logged independently of stats.Observer.
// Demonstrates the general-purpose (unpaired, Satisfies-empty) Fn shape
// on BOTH roles — server func(http.Handler) http.Handler (the SAME shape
// nethttp.Observability/chi's reuse of it already use) and client
// func(next func(ctx,Req)(Resp,error)) func(ctx,Req)(Resp,error) (the
// general-purpose ClientMW shape shipped in
// docs/design/d-0001-rest-middleware-workflow-simplification.md's
// Addendum 3, extended to Client.Call in Addendum 5) — attached via
// .HandleMW(nil, ...)/.ClientMW(nil, ...) respectively, never paired
// against any security scheme.

// TimingServerMW returns a general-purpose server-side middleware Fn
// (func(http.Handler) http.Handler) that logs each request's duration —
// attach via Route.HandleMW(nil, TimingServerMW(logger)).
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
// Route.ClientMW(nil, TimingClientMW[Req,Resp](logger)). A separate
// instantiation is needed per Req/Resp pair (Go forbids a value having
// its own type parameters), mirroring the shape adapters/mqtt5's
// wrapPublishGeneral's callers already instantiate per-T.
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
