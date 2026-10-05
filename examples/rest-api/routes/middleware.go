package routes

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Middleware kind 1: security ──────────────────────────────────────────────
//
// The "bearerAuth" scheme requires a GENUINE cross-Req-type attachment —
// 6 different routes (CreateUserReq/GetUserReq/UpdateUserReq/ListUsersReq/
// ProfileReq/AdminActionReq) share ONE scheme but differ in their own Req
// type, and declare (routes.go)/implement (handlers/server.go)/fulfill
// (client/client.go) deliberately live in SEPARATE files/packages — so
// each attachment is built via [rest.BoundMiddleware]/
// [rest.BoundClientMiddleware] (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7),
// generic over the attaching route's own Req type. BoundScopeServerMW/
// BoundScopeClientMW below are the shared constructor helpers every
// attachment site (nethttpserver/server.go, chiserver/server.go,
// client/client.go, demo_violations.go) calls — one line per route,
// delegating to the SAME handlers.VerifyScopes helper underneath, so a
// scheme-name/scope typo is caught at Register time
// (UnknownMiddlewareImplementationError), never silently.
//
// Two scopes are used across this example: "profile" (read one's own
// profile/user data) and "admin" (privileged actions). BearerCodec
// format-validates the raw credential BEFORE any Fn runs, on both server
// and client.

// BearerCodec validates a raw bearer token string's FORMAT (non-empty,
// well-formed) — shared by every security declaration below so the check
// is defined once.
var BearerCodec = codex.String().Refine(validate.BearerToken)

// BearerAuthScheme is shared by both scope attachments below.
var BearerAuthScheme = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT"), Codec: &BearerCodec}

// ProfileScopes/AdminScopes are the scope requirements
// BoundScopeServerMW/BoundScopeClientMW attach the "bearerAuth" scheme
// with — "profile" for any authenticated user, "admin" for privileged
// routes.
var (
	ProfileScopes = []string{"profile"}
	AdminScopes   = []string{"admin"}
)

// authHeaderParam is the Authorization merge field shared by every
// "bearerAuth" attachment below — decodes the raw header value directly
// into AuthIn.Token (see routes/grantedscopes_demo.go for AuthIn/AuthOut).
func authHeaderParam() rest.MergedHeaderParam[AuthIn] {
	return rest.NewRequiredHeaderParam("Authorization", BearerCodec,
		func(in AuthIn) string { return in.Token },
		func(in *AuthIn, v string) { in.Token = v },
	)
}

// BoundScopeServerMW builds the SERVER-side bound "bearerAuth" Security
// middleware, generic over the attaching route's own Req type — fn is
// wrapped with the shared Authorization merge field automatically. scopes
// is the route's own required-scope list (ProfileScopes or AdminScopes).
func BoundScopeServerMW[Req any](scopes []string, fn func(ctx context.Context, req *Req, in AuthIn) (AuthOut, error)) rest.BoundMiddleware[Req, AuthIn, AuthOut] {
	return rest.BoundSecurityMiddleware[Req, AuthIn, AuthOut]("bearerAuth", BearerAuthScheme, scopes, fn).
		WithRequestHeader(authHeaderParam())
}

// BoundScopeClientMW is [BoundScopeServerMW]'s client/credential-supplying
// sibling.
func BoundScopeClientMW[Req any](scopes []string, fn func(ctx context.Context, req Req) (AuthIn, error)) rest.BoundClientMiddleware[Req, AuthIn, AuthOut] {
	return rest.BoundSecurityClientMiddleware[Req, AuthIn, AuthOut]("bearerAuth", BearerAuthScheme, scopes, fn).
		WithRequestHeader(authHeaderParam())
}

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
