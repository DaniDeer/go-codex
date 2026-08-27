// Package adapters-nethttp-security demonstrates authentication and authorization
// for REST APIs built with go-codex and the net/http adapter.
//
// Security is declared in two layers:
//
//  1. Spec layer (api/rest + route): security schemes and per-route requirements
//     are declared on the Builder and RouteHandle. The OpenAPI spec output
//     includes components/securitySchemes and per-operation security.
//
//  2. Runtime layer (adapters/nethttp): the adapter extracts credentials from
//     the request (Authorization header for Bearer), runs the optional Codec
//     format-validation (e.g. validate.JWT), then calls SecurityFunc for
//     application-level verification (signature check, scope check, etc.).
//
// Routes:
//   - POST /login          — public (no security), returns a mock token
//   - GET  /profile        — secured with bearerAuth (JWT format + user scope)
//   - POST /admin/action   — secured with bearerAuth + admin scope
//
// SecurityFunc in this example performs a mock check (no real crypto):
//   - Accepts any token that starts with "valid-"
//   - Checks that the required scopes are present in a mock token store
//
// A SecurityObserver is wired to log every credential rejection with the
// route path and scheme name.
//
// Run with: go run ./examples/adapters-nethttp-security
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ─────────────────────────────────────────────────────────────

type loginReq struct {
	Username string
	Password string
}

type tokenResp struct {
	Token string
}

// invalidCredentialsError is returned by the login handler when the username or
// password is wrong. The ErrorHandler maps it to 401 Unauthorized.
type invalidCredentialsError struct{ Err error }

func (e invalidCredentialsError) Error() string { return e.Err.Error() }
func (e invalidCredentialsError) Unwrap() error { return e.Err }

type profileResp struct {
	UserID string
	Name   string
	Email  string
}

type adminActionReq struct {
	Action string
}

type adminActionResp struct {
	Result string
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var loginReqCodec = codex.Struct[loginReq](
	codex.RequiredField("username",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Username."),
		func(r loginReq) string { return r.Username },
		func(r *loginReq, v string) { r.Username = v },
	),
	codex.RequiredField("password",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Password."),
		func(r loginReq) string { return r.Password },
		func(r *loginReq, v string) { r.Password = v },
	),
)

var tokenRespCodec = codex.Struct[tokenResp](
	codex.RequiredField("token",
		codex.String().WithDescription("Bearer token for subsequent requests."),
		func(r tokenResp) string { return r.Token },
		func(r *tokenResp, v string) { r.Token = v },
	),
)

var profileRespCodec = codex.Struct[profileResp](
	codex.OptionalField("userId",
		codex.String().WithDescription("User UUID."),
		func(r profileResp) string { return r.UserID },
		func(r *profileResp, v string) { r.UserID = v },
	),
	codex.OptionalField("name",
		codex.String().WithDescription("Display name."),
		func(r profileResp) string { return r.Name },
		func(r *profileResp, v string) { r.Name = v },
	),
	codex.OptionalField("email",
		codex.String().WithDescription("Email address."),
		func(r profileResp) string { return r.Email },
		func(r *profileResp, v string) { r.Email = v },
	),
)

var adminActionReqCodec = codex.Struct[adminActionReq](
	codex.RequiredField("action",
		codex.String().Refine(validate.NonEmptyString).WithDescription("Admin action name."),
		func(r adminActionReq) string { return r.Action },
		func(r *adminActionReq, v string) { r.Action = v },
	),
)

var adminActionRespCodec = codex.Struct[adminActionResp](
	codex.OptionalField("result",
		codex.String().WithDescription("Outcome of the admin action."),
		func(r adminActionResp) string { return r.Result },
		func(r *adminActionResp, v string) { r.Result = v },
	),
)

// ── Mock token store ─────────────────────────────────────────────────────────

// tokenScopes maps mock tokens to their granted scopes.
// In production this would be a JWT claims verifier.
var tokenScopes = map[string][]string{
	"valid-user-token":  {"profile"},
	"valid-admin-token": {"profile", "admin"},
}

// extractScopes is a PURE AUTHENTICATION step — it extracts and validates
// the bearer token, returning the scopes it grants. It does NOT decide
// pass/fail against a route's required scopes: that mechanical scope-match
// is now done ONCE by the adapter (via middleware.CheckScopes), after
// merging every attached security Fn's grants — see "L4" in
// docs/roadmap/declarative-middleware.md. A rejection HERE (missing/unknown
// token) is a genuine authentication failure and is reported via
// RecordSecurityRejection directly, since it's this Fn's own responsibility
// now (the adapter no longer calls it automatically for Fn-driven
// failures — only for the credential-FORMAT check that runs before any Fn).
func extractScopes(ctx context.Context, r *http.Request, path string) (map[string][]string, error) {
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

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver implements [stats.Observer] and [stats.SecurityObserver].
// It accumulates counters for a final summary.
// Combine with [stats.NewLoggingObserver] via [stats.NewFanout] for logging.
type CountingObserver struct {
	stats.NoopObserver // satisfies RecordSubscribe / RecordPublish (unused for HTTP)

	mu         sync.Mutex
	requests   int
	byStatus   map[int]int
	valErrors  int
	rejections int
}

func (o *CountingObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
	o.mu.Lock()
	o.requests++
	if o.byStatus == nil {
		o.byStatus = make(map[int]int)
	}
	o.byStatus[statusCode]++
	o.mu.Unlock()
}

func (o *CountingObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	o.valErrors++
	o.mu.Unlock()
}

func (o *CountingObserver) RecordSecurityRejection(location, scheme string) {
	o.mu.Lock()
	o.rejections++
	o.mu.Unlock()
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total requests    : %d\n", o.requests)
	for code, n := range o.byStatus {
		fmt.Printf("  HTTP %-3d           : %d\n", code, n)
	}
	fmt.Printf("  validation errors : %d\n", o.valErrors)
	fmt.Printf("  security rejections: %d\n", o.rejections)
}

// ── Builder and routes ────────────────────────────────────────────────────────

func buildAPI() (
	loginHandle *rest.RouteHandle[loginReq, tokenResp],
	profileHandle *rest.RouteHandle[struct{}, profileResp],
	adminHandle *rest.RouteHandle[adminActionReq, adminActionResp],
	b *rest.Builder,
) {
	b = rest.NewBuilder(rest.Info{
		Title:       "Secure API Demo",
		Version:     "1.0.0",
		Description: "Demonstrates Bearer JWT authentication with per-route scope enforcement.",
	})

	// The bearer credential format codec, shared by both secured routes'
	// RequireScopes middleware below — validates non-empty, no whitespace
	// BEFORE the middleware's Fn is ever invoked, catching malformed tokens
	// early (the client-side mirror of this same codec would be used
	// identically on any Call to these routes).
	bearerCodec := codex.String().Refine(validate.BearerToken)

	// POST /login — public, no security requirement.
	loginHandle, _ = rest.NewRoute[loginReq, tokenResp]("POST", "/login",
		loginReqCodec, tokenRespCodec,
		rest.RouteMeta{
			OperationID: "login",
			Summary:     "Authenticate and receive a bearer token",
			Tags:        []string{"auth"},
		},
	).Register(b)

	// GET /profile — secured: bearerAuth with "profile" scope. RequireScopes
	// is BOTH the spec declaration (Security + securitySchemes, via
	// rest.WithMiddleware below) AND the runtime enforcement (Fn) — no
	// separate WithSecurityScheme/RouteMeta.Security call needed at all.
	profileScopesMw := nethttp.RequireScopes[struct{}]("bearerAuth", route.BearerScheme("JWT"), []string{"profile"}, &bearerCodec,
		func(ctx context.Context, r *http.Request, _ *struct{}) (map[string][]string, error) {
			return extractScopes(ctx, r, "/profile")
		},
	)
	profileHandle, _ = rest.NewRoute[struct{}, profileResp]("GET", "/profile",
		codex.Empty, profileRespCodec,
		rest.RouteMeta{
			OperationID: "getProfile",
			Summary:     "Get the authenticated user's profile",
			Tags:        []string{"user"},
		},
		rest.WithMiddleware(profileScopesMw),
	).Register(b)

	// POST /admin/action — secured: bearerAuth with "admin" scope.
	adminScopesMw := nethttp.RequireScopes[adminActionReq]("bearerAuth", route.BearerScheme("JWT"), []string{"admin"}, &bearerCodec,
		func(ctx context.Context, r *http.Request, _ *adminActionReq) (map[string][]string, error) {
			return extractScopes(ctx, r, "/admin/action")
		},
	)
	adminHandle, _ = rest.NewRoute[adminActionReq, adminActionResp]("POST", "/admin/action",
		adminActionReqCodec, adminActionRespCodec,
		rest.RouteMeta{
			OperationID: "adminAction",
			Summary:     "Perform a privileged admin action",
			Tags:        []string{"admin"},
		},
		rest.WithMiddleware(adminScopesMw),
	).Register(b)

	return
}

// mustRegister exits the program if nethttp.Register returns an error.
func mustRegister(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "nethttp.Register failed: %v\n", err)
		os.Exit(1)
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	loginHandle, profileHandle, adminHandle, b := buildAPI()
	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "http-security")))

	// RequireScopes is attached at Register time in buildAPI (see there) —
	// both the spec declaration and the runtime enforcement. Options only
	// carries the ErrorHandler now; ObservabilityMiddleware supplies obs.
	opts := nethttp.Options{
		// ErrorHandler maps invalidCredentialsError to 401; all other errors
		// fall through to the default JSON envelope with their suggested status.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, status int, err error) {
			var credErr invalidCredentialsError
			if errors.As(err, &credErr) {
				status = http.StatusUnauthorized
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		},
	}
	obsMw := nethttp.ObservabilityMiddleware(obs)

	mux := http.NewServeMux()

	mustRegister(nethttp.Register(mux, loginHandle, func(_ context.Context, req loginReq) (tokenResp, error) {
		// Mock: accept alice/secret → user token, admin/secret → admin token.
		switch {
		case req.Username == "alice" && req.Password == "secret":
			return tokenResp{Token: "valid-user-token"}, nil
		case req.Username == "admin" && req.Password == "secret":
			return tokenResp{Token: "valid-admin-token"}, nil
		default:
			return tokenResp{}, invalidCredentialsError{Err: errors.New("invalid credentials")}
		}
	}, opts, obsMw))

	mustRegister(nethttp.Register(mux, profileHandle, func(_ context.Context, _ struct{}) (profileResp, error) {
		return profileResp{
			UserID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			Name:   "Alice",
			Email:  "alice@example.com",
		}, nil
	}, opts, obsMw))

	mustRegister(nethttp.Register(mux, adminHandle, func(_ context.Context, req adminActionReq) (adminActionResp, error) {
		return adminActionResp{Result: "action " + req.Action + " executed"}, nil
	}, opts, obsMw))

	// ── Demo requests ─────────────────────────────────────────────────────────
	fmt.Println("=== adapters-nethttp-security demo ===")
	fmt.Println()

	do := func(method, path, body, authHeader, label string) {
		var bodyReader *strings.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		} else {
			bodyReader = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, bodyReader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		fmt.Printf("[%s] %s %s → %d\n", label, method, path, rec.Code)
		if rec.Code != http.StatusNoContent {
			var out any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err == nil {
				b, _ := json.MarshalIndent(out, "  ", "  ")
				fmt.Printf("  %s\n", b)
			}
		}
		fmt.Println()
	}

	// 1. Login as alice → user token.
	do("POST", "/login", `{"username":"alice","password":"secret"}`, "", "login (alice)")

	// 2. Login as admin → admin token.
	do("POST", "/login", `{"username":"admin","password":"secret"}`, "", "login (admin)")

	// 3. GET /profile without token → 401.
	do("GET", "/profile", "", "", "profile (no token)")

	// 4. GET /profile with user token → 200.
	do("GET", "/profile", "", "Bearer valid-user-token", "profile (user token)")

	// 5. GET /profile with invalid token → 401.
	do("GET", "/profile", "", "Bearer wrong-token", "profile (bad token)")

	// 6. POST /admin/action with user token (lacks admin scope) → 401.
	do("POST", "/admin/action", `{"action":"reindex"}`, "Bearer valid-user-token", "admin action (user token, insufficient scope)")

	// 7. POST /admin/action with admin token → 201.
	do("POST", "/admin/action", `{"action":"reindex"}`, "Bearer valid-admin-token", "admin action (admin token)")

	// ── Observer summary ──────────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	metrics.Print()
	fmt.Println()

	// ── OpenAPI spec ──────────────────────────────────────────────────────────
	fmt.Println("=== OpenAPI 3.1 spec (security section) ===")
	fmt.Println()
	spec, err := b.OpenAPISpec()
	if err != nil {
		fmt.Println("spec error:", err)
		return
	}
	specJSON, _ := json.MarshalIndent(spec, "", "  ")
	// Print only the security-relevant parts to keep output focused.
	var specMap map[string]any
	_ = json.Unmarshal(specJSON, &specMap)
	if comp, ok := specMap["components"].(map[string]any); ok {
		if schemes, ok := comp["securitySchemes"]; ok {
			b2, _ := json.MarshalIndent(schemes, "", "  ")
			fmt.Println("components.securitySchemes:")
			fmt.Println(string(b2))
			fmt.Println()
		}
	}
	if paths, ok := specMap["paths"].(map[string]any); ok {
		for path, ops := range paths {
			if opsMap, ok := ops.(map[string]any); ok {
				for method, op := range opsMap {
					if opMap, ok := op.(map[string]any); ok {
						if sec, ok := opMap["security"]; ok {
							secB, _ := json.MarshalIndent(sec, "", "  ")
							fmt.Printf("%s %s security: %s\n", strings.ToUpper(method), path, secB)
						}
					}
				}
			}
		}
	}
}
