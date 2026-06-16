// Package adapters-nethttp-client demonstrates the HTTP client-side adapter.
//
// go-codex routes are declarative values — the same [rest.Route] that drives
// the server (nethttp.Handler / nethttp.Register) can be used on the client
// (nethttp.Call) to make typed HTTP calls with full codec validation.
//
// The shared contract/ sub-package defines types, codecs, and route specs.
// Both server and client import it. The Go compiler enforces the contract:
// any change breaks compilation on both sides immediately — no stale YAML.
//
// The example covers all CallOptions fields in five sections:
//
//  1. Body — POST /users (request body codec, shared-contract pattern)
//  2. Path params — GET /users/{id} (codec-validated path variable)
//  3. Cookies + headers — GET /profile (CookieParam + HeaderParam validation)
//  4. Security — GET /data (CredentialFunc injects bearer Authorization header)
//  5. Structured error logging — errors.As + slog for all typed error types
//
// Run with: go run ./examples/adapters-nethttp-client
package main

import (
	"context"
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
	"github.com/DaniDeer/go-codex/examples/adapters-nethttp-client/contract"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// It records call counts, HTTP status codes, validation error locations, and
// latencies. In production replace the counters with Prometheus / OpenTelemetry
// instruments — the interface is identical.
//
// The observer is for metrics collection only. Structured error logging is done
// separately in application code using [errors.As] after each [nethttp.Call].
type CountingObserver struct {
	mu             sync.Mutex
	total          int
	byStatus       map[int]int
	valErrorsByLoc map[string]int // keyed by location: "path", "query", "cookie", "header", "body"
	latencies      []time.Duration
}

func (o *CountingObserver) RecordRequest(_ string, _ string, statusCode int, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total++
	if o.byStatus == nil {
		o.byStatus = make(map[int]int)
	}
	o.byStatus[statusCode]++
	o.latencies = append(o.latencies, d)
}

func (o *CountingObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (o *CountingObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

func (o *CountingObserver) RecordValidationError(location, constraintName, field string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.valErrorsByLoc == nil {
		o.valErrorsByLoc = make(map[string]int)
	}
	o.valErrorsByLoc[location]++
	// No logging — use stats.NewLoggingObserver via stats.NewFanout for structured logging.
}

func (o *CountingObserver) Print() {
	o.mu.Lock()
	defer o.mu.Unlock()
	fmt.Printf("  total calls    : %d\n", o.total)
	for code, n := range o.byStatus {
		label := "ok"
		if code == 0 {
			label = "pre-flight abort"
		} else if code >= 400 {
			label = "error"
		}
		fmt.Printf("  HTTP %-3d %-16s: %d\n", code, "("+label+")", n)
	}
	for loc, n := range o.valErrorsByLoc {
		fmt.Printf("  val errs %-8s: %d\n", "("+loc+")", n)
	}
	if len(o.latencies) > 0 {
		var sum time.Duration
		for _, l := range o.latencies {
			sum += l
		}
		fmt.Printf("  avg latency    : %v\n", sum/time.Duration(len(o.latencies)))
	}
}

// ── Server store ──────────────────────────────────────────────────────────────

type userStore struct {
	mu    sync.RWMutex
	users map[string]contract.User
	seq   int
}

func (s *userStore) create(req contract.CreateUserReq) contract.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := fmt.Sprintf("u%d", s.seq)
	u := contract.User{ID: id, Name: req.Name, Email: req.Email}
	s.users[id] = u
	return u
}

func (s *userStore) get(id string) (contract.User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	return u, ok
}

func main() {
	// logger is the structured logger for all HTTP client-side events.
	// In production attach trace IDs, tenant, or user via logger.With(...).
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	logger = logger.With("transport", "http-client")

	// metrics collects per-call counters. In production replace with Prometheus / OTel.
	// Logging is handled separately by stats.NewLoggingObserver — no mixing of concerns.
	metrics := &CountingObserver{}
	obs := stats.NewFanout(
		metrics,
		stats.NewLoggingObserver(logger),
	)

	db := &userStore{users: make(map[string]contract.User)}

	// ── Server setup ──────────────────────────────────────────────────────────
	//
	// Register all routes from the shared contract. The client will import the
	// same route specs — the compiler enforces the contract at both ends.

	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})

	// Register the bearer security scheme so the OpenAPI spec documents it.
	// The codec validates the raw credential format before SecurityFunc runs.
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	}.WithCodec(codex.String().Refine(validate.NonEmptyString)))

	createHandle, err := contract.CreateUser.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register createUser:", err)
		os.Exit(1)
	}
	getHandle, err := contract.GetUser.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register getUser:", err)
		os.Exit(1)
	}
	profileHandle, err := contract.GetProfile.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register getProfile:", err)
		os.Exit(1)
	}
	securedHandle, err := contract.GetSecuredData.Register(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register getSecuredData:", err)
		os.Exit(1)
	}

	// SecurityFunc checks the Authorization header for the secured route.
	// In production verify a signed JWT; here a fixed token suffices.
	const validToken = "secret-token"
	srvOpts := nethttp.Options{
		SecurityFunc: func(ctx context.Context, r *http.Request, _ []route.SecurityRequirement) error {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token != validToken {
				return fmt.Errorf("invalid token")
			}
			return nil
		},
	}

	mux := http.NewServeMux()

	nethttp.Register(mux, createHandle, func(ctx context.Context, req contract.CreateUserReq) (contract.User, error) {
		u := db.create(req)
		if h, ok := nethttp.ResponseHeadersFromContext(ctx); ok {
			h.Set("Location", "/users/"+u.ID)
		}
		return u, nil
	}, nethttp.Options{})

	nethttp.Register(mux, getHandle, func(ctx context.Context, _ struct{}) (contract.User, error) {
		r, _ := nethttp.RequestFromContext(ctx)
		u, ok := db.get(r.PathValue("id"))
		if !ok {
			return contract.User{}, fmt.Errorf("user not found")
		}
		return u, nil
	}, nethttp.Options{})

	// GET /profile — adapter validates session_token cookie and X-Request-ID
	// header automatically before this handler is called.
	// The adapter validates session_token cookie and X-Request-Id header before
	// calling this handler. The handler can read them via RequestFromContext if needed.
	nethttp.Register(mux, profileHandle, func(_ context.Context, _ struct{}) (contract.Profile, error) {
		return contract.Profile{
			ID:    "p1",
			Name:  "Alice",
			Email: "alice@example.com",
			Role:  "user",
		}, nil
	}, nethttp.Options{})

	// GET /data — secured route; SecurityFunc runs after codec validation.
	nethttp.Register(mux, securedHandle, func(_ context.Context, _ struct{}) (contract.Profile, error) {
		return contract.Profile{ID: "p1", Name: "Alice", Email: "alice@example.com", Role: "admin"}, nil
	}, srvOpts)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// ── 1. Body — POST /users ─────────────────────────────────────────────────
	fmt.Println("=== 1. Body: POST /users (request body codec) ===")

	clientCreate, err := contract.CreateUser.Register(
		rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client register:", err)
		os.Exit(1)
	}

	alice, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientCreate,
		contract.CreateUserReq{Name: "Alice", Email: "alice@example.com"},
		nil, nethttp.CallOptions{Observer: obs})
	if err != nil {
		fmt.Fprintln(os.Stderr, "create alice:", err)
		os.Exit(1)
	}
	fmt.Printf("created: %+v\n\n", alice)

	// ── 2. Path params — GET /users/{id} ──────────────────────────────────────
	fmt.Println("=== 2. Path params: GET /users/{id} ===")

	// ClientHandle() — no builder needed for client-only usage.
	clientGet := contract.GetUser.ClientHandle()

	fetched, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientGet, struct{}{},
		map[string]string{"id": alice.ID},
		nethttp.CallOptions{Observer: obs})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get alice:", err)
		os.Exit(1)
	}
	fmt.Printf("fetched: %+v\n", fetched)

	// Path param validation happens client-side before any HTTP call is sent.
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientGet, struct{}{},
		map[string]string{"id": ""}, // empty — fails NonEmptyString codec
		nethttp.CallOptions{Observer: obs})
	if err != nil {
		var pathErr rest.PathParamError
		if errors.As(err, &pathErr) {
			logger.Warn("path param rejected (no request sent)",
				"param", pathErr.Name,
				"value", pathErr.Value,
				"cause", pathErr.Err,
			)
		}
	}
	fmt.Println()

	// ── 3. Cookies + headers — GET /profile ───────────────────────────────────
	//
	// CookieParams and HeaderParams are codec-validated before the request is
	// sent. A violation aborts the call and returns the typed error — no network
	// round-trip wasted on a request that the server would reject anyway.
	fmt.Println("=== 3. Cookies + headers: GET /profile ===")

	clientProfile := contract.GetProfile.ClientHandle()

	// Happy path: valid session_token cookie and X-Request-Id header.
	profile, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientProfile, struct{}{}, nil,
		nethttp.CallOptions{
			CookieParams: map[string]string{
				"session_token": "my-valid-session-abc123",
			},
			HeaderParams: map[string]string{
				"X-Request-Id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			Observer: obs,
		})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get profile:", err)
		os.Exit(1)
	}
	fmt.Printf("profile: %+v\n", profile)

	// Cookie validation failure: empty session_token fails NonEmptyString codec.
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientProfile, struct{}{}, nil,
		nethttp.CallOptions{
			CookieParams: map[string]string{"session_token": ""}, // invalid
			HeaderParams: map[string]string{"X-Request-Id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
			Observer:     obs,
		})
	if err != nil {
		var cookieErr rest.CookieParamError
		if errors.As(err, &cookieErr) {
			logger.Warn("cookie rejected (no request sent)",
				"param", cookieErr.Name,
				"value", cookieErr.Value,
				"cause", cookieErr.Err,
			)
		}
	}

	// Header validation failure: non-UUID value fails UUID codec.
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientProfile, struct{}{}, nil,
		nethttp.CallOptions{
			CookieParams: map[string]string{"session_token": "my-valid-session-abc123"},
			HeaderParams: map[string]string{"X-Request-Id": "not-a-uuid"}, // invalid
			Observer:     obs,
		})
	if err != nil {
		var headerErr rest.HeaderParamError
		if errors.As(err, &headerErr) {
			logger.Warn("header rejected (no request sent)",
				"param", headerErr.Name,
				"value", headerErr.Value,
				"cause", headerErr.Err,
			)
		}
	}
	fmt.Println()

	// ── 4. Security — GET /data (CredentialFunc) ──────────────────────────────
	//
	// CredentialFunc is called when the route declares Security requirements.
	// It receives the resolved []route.SecurityRequirement and must return
	// headers to merge into the request — typically Authorization.
	// A CredentialFunc error aborts the call before any network activity.
	fmt.Println("=== 4. Security: GET /data (CredentialFunc) ===")

	clientSecured := contract.GetSecuredData.ClientHandle()

	// Happy path: CredentialFunc injects the bearer token.
	data, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientSecured, struct{}{}, nil,
		nethttp.CallOptions{
			CredentialFunc: func(_ context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
				// reqs contains the declared security requirements from the route spec.
				// In production: look up the token from a token store / refresh if expired.
				h := make(http.Header)
				h.Set("Authorization", "Bearer "+validToken)
				return h, nil
			},
			Observer: obs,
		})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get secured data:", err)
		os.Exit(1)
	}
	fmt.Printf("secured data: %+v\n", data)

	// No CredentialFunc: request is sent without Authorization → server returns 401.
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientSecured, struct{}{}, nil,
		nethttp.CallOptions{Observer: obs}) // no CredentialFunc
	if err != nil {
		var statusErr nethttp.UnexpectedStatusError
		if errors.As(err, &statusErr) {
			logger.Error("unauthorized (no credentials supplied)",
				"method", statusErr.Method,
				"path", statusErr.Path,
				"status", statusErr.StatusCode,
			)
		}
	}

	// CredentialFunc returning an error: aborts the call client-side.
	tokenExpiredErr := fmt.Errorf("token expired")
	_, err = nethttp.Call(context.Background(), srv.Client(), srv.URL,
		clientSecured, struct{}{}, nil,
		nethttp.CallOptions{
			CredentialFunc: func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
				return nil, tokenExpiredErr
			},
			Observer: obs,
		})
	if err != nil {
		if errors.Is(err, tokenExpiredErr) {
			logger.Warn("credential error (no request sent)", "cause", err)
		}
	}
	fmt.Println()

	// ── 5. OpenAPI spec ───────────────────────────────────────────────────────
	//
	// The same builder that registered routes for runtime use also generates
	// the full OpenAPI 3.1 spec — one definition drives both. Routes declared
	// with headers, cookies, path params, query params, and security schemes
	// all flow into the spec automatically.
	fmt.Println("=== 5. OpenAPI spec (derived from shared contract) ===")

	doc, err := b.OpenAPISpec()
	if err != nil {
		fmt.Fprintln(os.Stderr, "OpenAPISpec error:", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintln(os.Stderr, "MarshalYAML error:", err)
		os.Exit(1)
	}
	fmt.Println(string(yamlBytes))

	// ── 6. Observer summary ───────────────────────────────────────────────────
	fmt.Println("=== Observer summary ===")
	metrics.Print()
	fmt.Println()
	fmt.Println("done.")
}
