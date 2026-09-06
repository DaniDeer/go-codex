package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/stats"
)

// tokenScopes is a mock credential store — real code would look this up
// against a database or an identity provider.
var tokenScopes = map[string][]string{
	"valid-user-token":  {"profile"},
	"valid-admin-token": {"profile", "admin"},
}

// ExtractScopes is a PURE AUTHENTICATION step — the mechanical scope-match
// against the route's declared requirement is done ONCE by the adapter
// (via middleware.CheckScopes), not here. path is used only for
// SecurityObserver rejection reporting. Works identically whether nethttp
// or chi supplies r — both give an ordinary *http.Request.
func ExtractScopes(ctx context.Context, r *http.Request, path string) (map[string][]string, error) {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		recordRejection(ctx, path)
		return nil, errors.New("missing or malformed Authorization header")
	}
	scopes, ok := tokenScopes[token]
	if !ok {
		recordRejection(ctx, path)
		return nil, fmt.Errorf("unknown or expired token %q", token)
	}
	return map[string][]string{"bearerAuth": scopes}, nil
}

func recordRejection(ctx context.Context, path string) {
	if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
		secObs.RecordSecurityRejection(path, "bearerAuth")
	}
}

// ScopesImpl builds a security middleware.ServerImplementation wrapping
// extract — the runtime counterpart to a route's declare-time
// middleware.SecurityScheme (routes.ProfileScopeMw/AdminScopeMw), matched
// by schemeName. Passed to Route.HandleMW(&declMw, ScopesImpl(...).Fn),
// which pairs it against the matching .Use()-declared scheme (see
// docs/design/d-0001-rest-middleware-workflow-simplification.md).
func ScopesImpl[Req any](schemeName string, extract func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error)) middleware.ServerImplementation {
	return middleware.ServerImplementation{
		Name:      "implement-scopes:" + schemeName,
		Satisfies: []string{schemeName},
		Fn:        extract,
	}
}
