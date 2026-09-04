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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
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

// scopesImpl builds a security middleware.ServerImplementation wrapping
// extract — the runtime counterpart to a route's declare-time
// middleware.SecurityScheme, matched by schemeName. Passed to
// Route.HandleMW(&declMw, scopesImpl(...).Fn), which pairs it against the
// matching .Use()-declared scheme (see docs/design/middleware-workflow-simplification.md).
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
		Title:       "Secure API Demo",
		Version:     "1.0.0",
		Description: "Demonstrates ****** authentication with per-route scope enforcement.",
	})

	// The bearer credential format codec, shared by both secured routes'
	// security declarations below — validates non-empty, no whitespace
	// BEFORE the runtime implementation's Fn is ever invoked, catching
	// malformed tokens early (the client-side mirror of this same codec
	// would be used identically on any Call to these routes).
	bearerCodec := codex.String().Refine(validate.BearerToken)

	// POST /login — public, no security requirement.
	loginRoute = rest.NewRoute[loginReq, tokenResp]("POST", "/login",
		loginReqCodec, tokenRespCodec,
		rest.RouteMeta{
			OperationID: "login",
			Summary:     "Authenticate and receive a bearer token",
			Tags:        []string{"auth"},
		},
	)

	// GET /profile — secured: bearerAuth with "profile" scope.
	// middleware.SecurityScheme is the declare-time half — attached via
	// Use() it contributes the spec declaration (Security +
	// securitySchemes) — no separate WithSecurityScheme/RouteMeta.Security
	// call needed at all. The runtime enforcement is a SEPARATE
	// middleware.ServerImplementation, attached via Route.HandleMW in
	// main below, paired against this same declMw value.
	profileDeclMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), []string{"profile"}, &bearerCodec)
	profileRoute = rest.NewRoute[struct{}, profileResp]("GET", "/profile",
		codex.Empty, profileRespCodec,
		rest.RouteMeta{
			OperationID: "getProfile",
			Summary:     "Get the authenticated user's profile",
			Tags:        []string{"user"},
		},
	).Use(profileDeclMw)

	// POST /admin/action — secured: bearerAuth with "admin" scope.
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
// releases it immediately so AttachMux's own *http.Server can bind to it.
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
// mux synchronously before starting its listener goroutine, so a successful
// dial here also guarantees mux is fully wired and safe to call
// mux.ServeHTTP directly against, as this example's demo requests do below.
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
	obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "http-security")))

	// The declare-time security requirement is attached in buildAPI (see
	// there); the runtime enforcement half (Scopes) is supplied
	// separately, below, via HandleMW. Options only carries the
	// ErrorHandler now; Observability supplies obs.
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
	obsFn := nethttp.Observability(obs)

	// The runtime enforcement half of each secured route's declared
	// security requirement — attached here, via HandleMW, separately
	// from the declare-time middleware.SecurityScheme attached in
	// buildAPI.
	profileImplMw := scopesImpl[struct{}]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *struct{}) (map[string][]string, error) {
			return extractScopes(ctx, r, "/profile")
		},
	)
	adminImplMw := scopesImpl[adminActionReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *adminActionReq) (map[string][]string, error) {
			return extractScopes(ctx, r, "/admin/action")
		},
	)

	loginRoute = loginRoute.WithHandler(func(_ context.Context, req loginReq) (tokenResp, error) {
		// Mock: accept alice/secret → user token, admin/secret → admin token.
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

	profileRoute = profileRoute.WithHandler(func(_ context.Context, _ struct{}) (profileResp, error) {
		return profileResp{
			UserID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			Name:   "Alice",
			Email:  "alice@example.com",
		}, nil
	}).HandleMW(nil, obsFn).HandleMW(&profileDeclMw, profileImplMw.Fn).WithOptions(opts)
	mustServe(profileRoute.Register(b), "register /profile")

	adminRoute = adminRoute.WithHandler(func(_ context.Context, req adminActionReq) (adminActionResp, error) {
		return adminActionResp{Result: "action " + req.Action + " executed"}, nil
	}).HandleMW(nil, obsFn).HandleMW(&adminDeclMw, adminImplMw.Fn).WithOptions(opts)
	mustServe(adminRoute.Register(b), "register /admin/action")

	mux := http.NewServeMux()
	addr := mustFreeAddr()
	mustServe(nethttp.AttachMux(b, mux, addr), "AttachMux")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Serve(ctx) }()
	defer cancel()
	waitForReady(addr)

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
