// Package adapters-chi-security demonstrates authentication and authorization
// for REST APIs built with go-codex and the chi router adapter.
//
// Architecture mirrors adapters-nethttp-security exactly — the only difference
// is that the chi adapter is used instead of net/http, and chi path variable
// extraction works alongside security enforcement.
//
// Security is declared in two layers:
//
//  1. Spec layer (api/rest + route): security schemes and per-route requirements
//     are declared on the Builder and RouteHandle. The OpenAPI spec output
//     includes components/securitySchemes and per-operation security.
//
//  2. Runtime layer (adapters/chi): the adapter extracts credentials from the
//     request (Authorization header for Bearer), runs the optional Codec
//     format-validation (e.g. validate.BearerToken), then calls SecurityFunc.
//
// Routes:
//   - POST /login              — public (no security), returns a mock token
//   - GET  /users/{id}/profile — secured with bearerAuth + "profile" scope; id via chi path var
//   - POST /admin/action       — secured with bearerAuth + "admin" scope
//
// SecurityFunc performs a mock check (no real crypto):
//   - Accepts tokens in the mock tokenScopes store
//   - Checks all required scopes from the SecurityRequirement
//
// Run with: go run ./examples/adapters-chi-security
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

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
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

var tokenScopes = map[string][]string{
	"valid-user-token":  {"profile"},
	"valid-admin-token": {"profile", "admin"},
}

func verifyToken(r *http.Request, reqs []route.SecurityRequirement) error {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" || token == auth {
		return errors.New("missing or malformed Authorization header")
	}
	scopes, ok := tokenScopes[token]
	if !ok {
		return fmt.Errorf("unknown or expired token %q", token)
	}
	for _, req := range reqs {
		for _, required := range flatScopes(req) {
			if !hasScope(scopes, required) {
				return fmt.Errorf("token lacks required scope %q", required)
			}
		}
	}
	return nil
}

func flatScopes(req route.SecurityRequirement) []string {
	var out []string
	for _, scopes := range req {
		out = append(out, scopes...)
	}
	return out
}

func hasScope(granted []string, required string) bool {
	for _, s := range granted {
		if s == required {
			return true
		}
	}
	return false
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
		Title:       "Secure API Demo (chi)",
		Version:     "1.0.0",
		Description: "Demonstrates Bearer JWT authentication with chi router and per-route scope enforcement.",
	})

	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	}.WithCodec(codex.String().Refine(validate.BearerToken)))

	// POST /login — public.
	loginHandle, _ = rest.NewRoute[loginReq, tokenResp]("POST", "/login",
		loginReqCodec, tokenRespCodec,
		rest.RouteMeta{OperationID: "login", Summary: "Authenticate and receive a bearer token", Tags: []string{"auth"}},
	).Register(b)

	// GET /users/{id}/profile — secured + chi path variable.
	profileHandle, _ = rest.NewRoute[struct{}, profileResp]("GET", "/users/{id}/profile",
		codex.Empty, profileRespCodec,
		rest.RouteMeta{
			OperationID: "getUserProfile",
			Summary:     "Get a user's profile by ID",
			Tags:        []string{"user"},
			Security:    []route.SecurityRequirement{route.Require("bearerAuth", "profile")},
		},
	).Register(b)

	// POST /admin/action — secured with admin scope.
	adminHandle, _ = rest.NewRoute[adminActionReq, adminActionResp]("POST", "/admin/action",
		adminActionReqCodec, adminActionRespCodec,
		rest.RouteMeta{
			OperationID: "adminAction",
			Summary:     "Perform a privileged admin action",
			Tags:        []string{"admin"},
			Security:    []route.SecurityRequirement{route.Require("bearerAuth", "admin")},
		},
	).Register(b)

	return
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	loginHandle, profileHandle, adminHandle, b := buildAPI()
	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "chi-security")))

	opts := chiadapter.Options{
		Observer: obs,
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
		SecurityFunc: func(ctx context.Context, r *http.Request, reqs []route.SecurityRequirement) error {
			return verifyToken(r, reqs)
		},
	}

	router := gochi.NewRouter()
	chiadapter.Register(router, loginHandle, func(_ context.Context, req loginReq) (tokenResp, error) {
		switch {
		case req.Username == "alice" && req.Password == "secret":
			return tokenResp{Token: "valid-user-token"}, nil
		case req.Username == "admin" && req.Password == "secret":
			return tokenResp{Token: "valid-admin-token"}, nil
		default:
			return tokenResp{}, invalidCredentialsError{Err: errors.New("invalid credentials")}
		}
	}, opts)

	chiadapter.Register(router, profileHandle, func(ctx context.Context, _ struct{}) (profileResp, error) {
		// chi path variable: chi.URLParam is accessible from the request context.
		id := gochi.URLParamFromCtx(ctx, "id")
		return profileResp{
			UserID: id,
			Name:   "Alice",
			Email:  "alice@example.com",
		}, nil
	}, opts)

	chiadapter.Register(router, adminHandle, func(_ context.Context, req adminActionReq) (adminActionResp, error) {
		return adminActionResp{Result: "action " + req.Action + " executed"}, nil
	}, opts)

	// ── Demo requests ─────────────────────────────────────────────────────────
	fmt.Println("=== adapters-chi-security demo ===")
	fmt.Println()

	do := func(method, path, body, authHeader, label string) {
		bodyReader := strings.NewReader(body)
		req := httptest.NewRequest(method, path, bodyReader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		fmt.Printf("[%s] %s %s → %d\n", label, method, path, rec.Code)
		if len(rec.Body.Bytes()) > 0 {
			var out any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err == nil {
				b2, _ := json.MarshalIndent(out, "  ", "  ")
				fmt.Printf("  %s\n", b2)
			}
		}
		fmt.Println()
	}

	// 1. Login → get tokens.
	do("POST", "/login", `{"username":"alice","password":"secret"}`, "", "login (alice)")
	do("POST", "/login", `{"username":"admin","password":"secret"}`, "", "login (admin)")

	// 2. Profile with chi path var: /users/abc123/profile.
	do("GET", "/users/abc123/profile", "", "", "profile (no token)")
	do("GET", "/users/abc123/profile", "", "Bearer valid-user-token", "profile (user token, id=abc123)")
	do("GET", "/users/abc123/profile", "", "Bearer bad-token", "profile (bad token)")

	// 3. Admin action scope enforcement.
	do("POST", "/admin/action", `{"action":"reindex"}`, "Bearer valid-user-token", "admin (user token, lacks admin scope)")
	do("POST", "/admin/action", `{"action":"reindex"}`, "Bearer valid-admin-token", "admin (admin token)")

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
	var specMap map[string]any
	_ = json.Unmarshal(specJSON, &specMap)
	if comp, ok := specMap["components"].(map[string]any); ok {
		if schemes, ok := comp["securitySchemes"]; ok {
			sb, _ := json.MarshalIndent(schemes, "", "  ")
			fmt.Println("components.securitySchemes:")
			fmt.Println(string(sb))
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
