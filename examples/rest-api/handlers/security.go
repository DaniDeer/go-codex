package handlers

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
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
// the Authorization header by routes.BoundScopeServerMW's merge field —
// no manual r.Header.Get/TrimPrefix needed (mirrors VerifyBearerGS in
// grantedscopes.go, the SAME convention). Required-header-missing cases
// never reach this Fn at all — the merge field's own codec rejects those
// before any attached middleware Fn runs.
func VerifyScopes(ctx context.Context, path string, in routes.AuthIn) (routes.AuthOut, error) {
	scopes, ok := tokenScopes[in.Token]
	if !ok {
		recordRejection(ctx, path)
		return routes.AuthOut{}, fmt.Errorf("unknown or expired token %q", in.Token)
	}
	return routes.AuthOut{GrantedScopes: map[string][]string{"bearerAuth": scopes}}, nil
}

func recordRejection(ctx context.Context, path string) {
	if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
		secObs.RecordSecurityRejection(path, "bearerAuth")
	}
}

// ScopesBoundFn adapts [VerifyScopes] into the
// func(ctx, *Req, routes.AuthIn) (routes.AuthOut, error) shape
// [routes.BoundScopeServerMW] expects — generic over the attaching
// route's own Req type, which this Fn discards entirely (the scope check
// never needs to read the request body/path/query, only the decoded
// credential). One instantiation per route (docs/roadmap/
// bound-middleware-split.md).
func ScopesBoundFn[Req any](path string) func(ctx context.Context, req *Req, in routes.AuthIn) (routes.AuthOut, error) {
	return func(ctx context.Context, _ *Req, in routes.AuthIn) (routes.AuthOut, error) {
		return VerifyScopes(ctx, path, in)
	}
}
