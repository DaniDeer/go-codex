package auth

import (
	"context"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── "bearerAuth" scheme ───────────────────────────────────────────────────
//
// The "bearerAuth" scheme requires a GENUINE cross-Req-type attachment —
// 6 different routes (CreateUserReq/GetUserReq/UpdateUserReq/ListUsersReq/
// ProfileReq/AdminActionReq) share ONE scheme but differ in their own Req
// type, and declare (here)/implement (handler.go)/fulfill
// (examples/rest-api/client/client.go) deliberately live in SEPARATE
// files/packages — so each attachment is built via [rest.BoundMiddleware]/
// [rest.BoundClientMiddleware] (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7),
// generic over the attaching route's own Req type. BoundScopeServerMW/
// BoundScopeClientMW below are the shared constructor helpers every
// attachment site (nethttpserver/server.go, chiserver/server.go,
// client/client.go, demo_violations.go) calls — one line per route,
// delegating to the SAME VerifyScopes helper underneath (handler.go), so
// a scheme-name/scope typo is caught at Register time
// (UnknownMiddlewareImplementationError), never silently.
//
// Two scopes are used across this example: "profile" (read one's own
// profile/user data) and "admin" (privileged actions). BearerCodec
// format-validates the raw credential BEFORE any Fn runs, on both server
// and client.

// AuthIn is BoundScopeServerMW's/GrantedScopesComputeServerMW's shared
// credential vocabulary — decoded from the raw "Authorization" header
// value via the required header merge field below, exactly like any
// other codec-declared middleware's In. Reused verbatim by BOTH the
// "bearerAuth" scheme (ProfileScopes/AdminScopes) and the "bearerAuthGS"
// scheme (GrantedScopes + ContextField demo below) — same vocabulary,
// different scheme names.
type AuthIn struct{ Token string }

// AuthOut carries the conventional GrantedScopes map[string][]string
// field the adapter reads via reflection and merges into the SAME
// middleware.CheckScopes call every Security attachment in this example
// uses — see docs/features/security.md's "Codec-backed Security" section.
type AuthOut struct {
	GrantedScopes map[string][]string
}

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
// into AuthIn.Token.
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

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than the "bearerAuth" scheme above. Both use the SAME underlying    ──
// ── mechanism — [rest.BoundMiddleware]/[rest.BoundClientMiddleware]     ──
// ── (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) —  ──
// ── a REAL credential type (AuthIn) and a REAL GrantedScopes-carrying   ──
// ── Out (AuthOut), dispatched via HandleBoundMW/ClientBoundMW. This     ──
// ── demo additionally publishes the decoded token via a                ──
// ── middleware.ContextField, which the "bearerAuth" scheme doesn't     ──
// ── need. See route.go for ComputeGSRoute itself.                       ──

// GrantedScopesUserIDField is a middleware.ContextField[string] — the
// authenticated token published by GrantedScopesComputeServerMW's
// embedded Fn (VerifyBearerGS, handler.go) and consumed by
// MakeComputeGSHandler via Get(ctx), with ZERO manual re-decoding inside
// the business handler. Declared once, shared by every producer/consumer
// that needs this same piece of cross-cutting data (docs/design/
// d-0007-declarative-middleware-layering.md's Phase 3).
var GrantedScopesUserIDField = middleware.NewContextField(codex.String())

// grantedScopesGSScheme is shared by both bound constructors below.
var grantedScopesGSScheme = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT"), Codec: &BearerCodec}

// GrantedScopesComputeServerMW builds the SERVER-side bound "bearerAuthGS"
// Security middleware for POST /compute-gs — fn is supplied by the
// CALLER (VerifyBearerGS, handler.go) rather than embedded here.
// SetContextFieldFromIn publishes the decoded token so the real handler
// can read it without touching the header itself.
func GrantedScopesComputeServerMW(fn func(ctx context.Context, req *ComputeGSReq, in AuthIn) (AuthOut, error)) rest.BoundMiddleware[ComputeGSReq, AuthIn, AuthOut] {
	return rest.BoundSecurityMiddleware[ComputeGSReq, AuthIn, AuthOut]("bearerAuthGS", grantedScopesGSScheme, []string{"compute:write"}, fn).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", BearerCodec,
			func(in AuthIn) string { return in.Token },
			func(in *AuthIn, v string) { in.Token = v },
		)).
		SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Token })
}

// GrantedScopesComputeClientMW is [GrantedScopesComputeServerMW]'s
// client/credential-supplying sibling.
func GrantedScopesComputeClientMW(fn func(ctx context.Context, req ComputeGSReq) (AuthIn, error)) rest.BoundClientMiddleware[ComputeGSReq, AuthIn, AuthOut] {
	return rest.BoundSecurityClientMiddleware[ComputeGSReq, AuthIn, AuthOut]("bearerAuthGS", grantedScopesGSScheme, []string{"compute:write"}, fn).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", BearerCodec,
			func(in AuthIn) string { return in.Token },
			func(in *AuthIn, v string) { in.Token = v },
		))
}
