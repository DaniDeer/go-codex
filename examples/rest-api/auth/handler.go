package auth

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/stats"
)

// tokenScopes is a mock credential store — real code would look this up
// against a database or an identity provider.
var tokenScopes = map[string][]string{
	"valid-user-token":  {"profile"},
	"valid-admin-token": {"profile", "admin"},
}

// VerifyScopes is a PURE AUTHENTICATION step — the mechanical scope-match
// against the route's declared requirement is done ONCE by the adapter
// (via middleware.CheckScopes), not here. path is used only for
// SecurityObserver rejection reporting. in.Token is ALREADY decoded from
// the Authorization header by BoundScopeServerMW's merge field — no
// manual r.Header.Get/TrimPrefix needed (mirrors VerifyBearerGS below,
// the SAME convention). Required-header-missing cases never reach this
// Fn at all — the merge field's own codec rejects those before any
// attached middleware Fn runs.
func VerifyScopes(ctx context.Context, path string, in AuthIn) (AuthOut, error) {
	scopes, ok := tokenScopes[in.Token]
	if !ok {
		recordRejection(ctx, path)
		return AuthOut{}, fmt.Errorf("unknown or expired token %q", in.Token)
	}
	return AuthOut{GrantedScopes: map[string][]string{"bearerAuth": scopes}}, nil
}

func recordRejection(ctx context.Context, path string) {
	if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
		secObs.RecordSecurityRejection(path, "bearerAuth")
	}
}

// ScopesBoundFn adapts [VerifyScopes] into the
// func(ctx, *Req, AuthIn) (AuthOut, error) shape [BoundScopeServerMW]
// expects — generic over the attaching route's own Req type, which this
// Fn discards entirely (the scope check never needs to read the request
// body/path/query, only the decoded credential). One instantiation per
// route (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7).
func ScopesBoundFn[Req any](path string) func(ctx context.Context, req *Req, in AuthIn) (AuthOut, error) {
	return func(ctx context.Context, _ *Req, in AuthIn) (AuthOut, error) {
		return VerifyScopes(ctx, path, in)
	}
}

// gsTokenScopes is a mock credential store for the GrantedScopes demo —
// deliberately separate from tokenScopes above (a different scheme,
// "bearerAuthGS", with its own scope vocabulary).
var gsTokenScopes = map[string][]string{
	"valid-compute-token":  {"compute:write"},
	"valid-readonly-token": {"profile"}, // lacks "compute:write" — proves rejection
}

// VerifyBearerGS is GrantedScopesComputeServerMW's paired, BOUND
// HandleMW Fn — func(ctx, *Req, In) (Out, error), detected via its own
// reflected signature (docs/design/d-0007-declarative-middleware-
// layering.md's Architecture revision). It receives AuthIn ALREADY
// DECODED from the Authorization header by the shared
// middleware.DecodeLayer mechanism — no manual r.Header.Get/TrimPrefix
// needed.
func VerifyBearerGS(_ context.Context, _ *ComputeGSReq, in AuthIn) (AuthOut, error) {
	scopes, ok := gsTokenScopes[in.Token]
	if !ok {
		return AuthOut{}, fmt.Errorf("unknown or expired token %q", in.Token)
	}
	return AuthOut{GrantedScopes: map[string][]string{"bearerAuthGS": scopes}}, nil
}

// MakeComputeGSHandler is the real business handler — it never touches
// the Authorization header itself. The authenticated token reaches it
// PURELY via GrantedScopesUserIDField.Get(ctx), published by
// VerifyBearerGS above through SetContextFieldFromIn — the concrete
// "zero manual re-decoding" promise docs/design/
// d-0007-declarative-middleware-layering.md's Phase 3 makes.
func MakeComputeGSHandler() func(ctx context.Context, req ComputeGSReq) (ComputeGSResp, error) {
	return func(ctx context.Context, req ComputeGSReq) (ComputeGSResp, error) {
		token, ok := GrantedScopesUserIDField.Get(ctx)
		fmt.Printf("  [handler] authenticated token (via ContextField, ok=%v): %q\n", ok, token)
		return ComputeGSResp{Sum: req.X + req.Y}, nil
	}
}

// MakeLoginHandler issues a mock bearer token for a known username/password
// pair — "alice"/"secret" gets a profile-scoped token, "admin"/"secret"
// gets a profile+admin-scoped token.
func MakeLoginHandler() func(context.Context, LoginReq) (TokenResp, error) {
	return func(_ context.Context, req LoginReq) (TokenResp, error) {
		switch {
		case req.Username == "alice" && req.Password == "secret":
			return TokenResp{Token: "valid-user-token"}, nil
		case req.Username == "admin" && req.Password == "secret":
			return TokenResp{Token: "valid-admin-token"}, nil
		default:
			return TokenResp{}, InvalidCredentialsError{Err: fmt.Errorf("invalid credentials")}
		}
	}
}
