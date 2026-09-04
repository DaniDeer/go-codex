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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
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

// extractScopes is a PURE AUTHENTICATION step — see
// docs/roadmap/declarative-middleware.md's "L4"/"L12" for why the
// mechanical scope-match is done ONCE by the adapter now, not here.
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

// scopesImpl builds a security middleware.ServerImplementation wrapping
// extract — the runtime counterpart to a route's declare-time
// middleware.SecurityScheme, matched by schemeName. Passed to
// Route.HandleMW(&declMw, scopesImpl(...).Fn), which pairs it against the
// matching .Use()-declared scheme (see docs/design/d-0001-rest-middleware-workflow-simplification.md).
func scopesImpl[Req any](schemeName string, extract func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error)) middleware.ServerImplementation {
	return middleware.ServerImplementation{
		Name:      "implement-scopes:" + schemeName,
		Satisfies: []string{schemeName},
		Fn:        extract,
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
	loginRoute rest.Route[loginReq, tokenResp],
	profileRoute rest.Route[struct{}, profileResp],
	profileDeclMw middleware.Middleware,
	adminRoute rest.Route[adminActionReq, adminActionResp],
	adminDeclMw middleware.Middleware,
	b *rest.Server,
) {
	b = rest.NewServer(rest.Info{
		Title:       "Secure API Demo (chi)",
		Version:     "1.0.0",
		Description: "Demonstrates bearer authentication with chi router and per-route scope enforcement.",
	})

	// The bearer credential format codec, shared by both secured routes'
	// security declarations below.
	bearerCodec := codex.String().Refine(validate.BearerToken)

	// POST /login — public.
	loginRoute = rest.NewRoute[loginReq, tokenResp]("POST", "/login",
		loginReqCodec, tokenRespCodec,
		rest.RouteMeta{OperationID: "login", Summary: "Authenticate and receive a bearer token", Tags: []string{"auth"}},
	)

	// GET /users/{id}/profile — secured + chi path variable.
	// middleware.SecurityScheme is the declare-time half — attached via
	// Use() it contributes the spec's Security requirement. The runtime
	// enforcement is a SEPARATE middleware.ServerImplementation (built by
	// this file's own scopesImpl helper — chi has no scheme-specific
	// constructor of its own, identical *http.Request Raw type), attached
	// via Route.HandleMW in main below.
	profileDeclMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"profile"}, &bearerCodec)
	profileRoute = rest.NewRoute[struct{}, profileResp]("GET", "/users/{id}/profile",
		codex.Empty, profileRespCodec,
		rest.RouteMeta{
			OperationID: "getUserProfile",
			Summary:     "Get a user's profile by ID",
			Tags:        []string{"user"},
		},
	).Use(profileDeclMw)

	// POST /admin/action — secured with admin scope.
	adminDeclMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"admin"}, &bearerCodec)
	adminRoute = rest.NewRoute[adminActionReq, adminActionResp]("POST", "/admin/action",
		adminActionReqCodec, adminActionRespCodec,
		rest.RouteMeta{
			OperationID: "adminAction",
			Summary:     "Perform a privileged admin action",
			Tags:        []string{"admin"},
		},
	).Use(adminDeclMw)

	return
}

// mustServe exits the program if Register or Serve returns an error.
func mustServe(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", what, err)
		os.Exit(1)
	}
}

// mustFreeAddr reserves an OS-assigned free TCP port on localhost, then
// releases it immediately so AttachRouter's own *http.Server can bind to it.
func mustFreeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reserve free port failed: %v\n", err)
		os.Exit(1)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForReady polls addr until it accepts TCP connections — b.Serve wires
// the router synchronously before starting its listener goroutine, so a
// successful dial here also guarantees router is fully wired and safe to
// call router.ServeHTTP directly against, as this example's demo requests
// do below.
func waitForReady(addr string) {
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	loginRoute, profileRoute, profileDeclMw, adminRoute, adminDeclMw, b := buildAPI()
	metrics := &CountingObserver{}
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "chi-security")))

	opts := chiadapter.Options{
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
	// chi reuses nethttp.Observability directly.
	obsFn := nethttp.Observability(obs)

	// The runtime enforcement half of each secured route's declared
	// security requirement — attached here, via HandleMW, separately
	// from the declare-time middleware.SecurityScheme attached in
	// buildAPI, built by this file's own scopesImpl helper (no
	// scheme-specific constructor of chi's own).
	profileImplMw := scopesImpl[struct{}]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *struct{}) (map[string][]string, error) {
			return extractScopes(ctx, r, "/users/{id}/profile")
		},
	)
	adminImplMw := scopesImpl[adminActionReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *adminActionReq) (map[string][]string, error) {
			return extractScopes(ctx, r, "/admin/action")
		},
	)

	loginRoute = loginRoute.WithHandler(func(_ context.Context, req loginReq) (tokenResp, error) {
		switch {
		case req.Username == "alice" && req.Password == "secret":
			return tokenResp{Token: "valid-user-token"}, nil
		case req.Username == "admin" && req.Password == "secret":
			return tokenResp{Token: "valid-admin-token"}, nil
		default:
			return tokenResp{}, invalidCredentialsError{Err: errors.New("invalid credentials")}
		}
	}).HandleMW(nil, obsFn).WithOptions(opts)
	mustServe(loginRoute.Register(b), "register /login")

	profileRoute = profileRoute.WithHandler(func(ctx context.Context, _ struct{}) (profileResp, error) {
		// chi path variable: chi.URLParam is accessible from the request context.
		id := gochi.URLParamFromCtx(ctx, "id")
		return profileResp{
			UserID: id,
			Name:   "Alice",
			Email:  "alice@example.com",
		}, nil
	}).HandleMW(nil, obsFn).HandleMW(&profileDeclMw, profileImplMw.Fn).WithOptions(opts)
	mustServe(profileRoute.Register(b), "register /users/{id}/profile")

	adminRoute = adminRoute.WithHandler(func(_ context.Context, req adminActionReq) (adminActionResp, error) {
		return adminActionResp{Result: "action " + req.Action + " executed"}, nil
	}).HandleMW(nil, obsFn).HandleMW(&adminDeclMw, adminImplMw.Fn).WithOptions(opts)
	mustServe(adminRoute.Register(b), "register /admin/action")

	router := gochi.NewRouter()
	addr := mustFreeAddr()
	mustServe(chiadapter.AttachRouter(b, router, addr), "AttachRouter")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Serve(ctx) }()
	defer cancel()
	waitForReady(addr)

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
