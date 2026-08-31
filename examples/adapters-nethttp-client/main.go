// Package adapters-nethttp-client demonstrates the HTTP client-side adapter.
//
// go-codex routes are declarative values — the same [rest.Route] that drives
// the server (WithHandler + Register, wired via nethttp.Serve) can be used on
// the client to make typed HTTP calls with full codec validation.
//
// The shared contract/ sub-package defines types, codecs, and route specs.
// Both server and client import it. The Go compiler enforces the contract:
// any change breaks compilation on both sides immediately — no stale YAML.
//
// # Caller — the recommended pattern for many calls to one API
//
// This example makes over a dozen calls, all against the SAME
// (client, baseURL) pair — exactly the repeated-boilerplate case
// [nethttp.Caller] exists to remove. [nethttp.NewCaller] is built ONCE,
// right after the server starts, and every call below uses [nethttp.Call]
// directly with the SAME contract.Route value the server registered — no
// separate "build a client copy" step, and no repeating srv.Client()/
// srv.URL at each call site.
//
// [nethttp.CallWithHandle] — the lower-level, handle-based primitive
// [nethttp.Call] wraps internally — remains available for callers that
// already have a *rest.RouteHandle but no rest.Route value, e.g.
// adapters/nethttp/binding.go's ports.Pattern REST binding machinery
// (DrainCallAdapter/CallAdapter), which owns its own client/baseURL via
// PortOptions, not a Caller.
//
// Credential fulfillment is declared PER-ROUTE via [rest.Route.ClientMW]
// (paired against the SAME [middleware.Middleware] the route's security
// requirement was declared with) — there is no per-call credential
// override anymore (a deliberate design tradeoff: ClientMW mirrors the
// server side's declare-then-register discipline exactly). Each distinct
// credential behavior demonstrated in section 4 below builds its OWN
// route value via a fresh .ClientMW(...) call.
//
// The example covers all CallOptions fields in five sections:
//
//  1. Body — POST /users (request body codec, shared-contract pattern)
//     1b. Client-side typed error decode — Call returns a decoded
//     nethttp.ErrorPatternResponse (instead of the untyped
//     UnexpectedStatusError) when the response status matches a
//     rest.ErrorPattern declared on the route — errors.As extracts the
//     typed payload directly, no manual status-switch or json.Unmarshal
//  2. Path params — GET /users/{id} (a path MERGE field via
//     rest.NewPathParam, so Call derives the path value directly from
//     req.ID — an invalid value surfaces as codex.ValidationError, caught
//     at merge-derive time, before any HTTP call)
//     2b. Client encode with role-aware merge fields + response merge —
//     GET /users/{id}/activity (rest.NewPathParam/NewOptionalQueryParam
//     derive BOTH the path var and the query param from ONE req value,
//     each scoped to its own HTTP location so values never leak across
//     roles; rest.NewRequiredResponseHeaderParam decodes the response
//     X-Request-Id header straight back into Resp — the full
//     request+response, single-call story)
//  3. Cookies + headers — GET /profile (CookieParam + HeaderParam validation)
//  4. Security — GET /data (a credential-providing Fn attached via
//     [rest.Route.ClientMW], paired against the route's declared
//     middleware.Middleware, injects the bearer Authorization header)
//     4b. Caching a credential-providing Fn — nethttp.NewCachingCredentialFunc
//     wraps any [nethttp.CredentialFunc] with TTL-based caching, then the
//     result is attached via ClientMW; CallOptions.OnCredentialRejected +
//     the returned invalidate func implement the explicit
//     retry-once-on-401 pattern (Call never retries automatically)
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
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// ── Observer ──────────────────────────────────────────────────────────────────

// CountingObserver is an in-memory implementation of [stats.Observer].
// It records call counts, HTTP status codes, validation error locations, and
// latencies. In production replace the counters with Prometheus / OpenTelemetry
// instruments — the interface is identical.
//
// The observer is for metrics collection only. Structured error logging is done
// separately in application code using [errors.As] after each call.
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

func (s *userStore) create(req contract.CreateUserReq) (contract.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Email == req.Email {
			return contract.User{}, contract.EmailConflictError{Email: req.Email}
		}
	}
	s.seq++
	id := fmt.Sprintf("u%d", s.seq)
	u := contract.User{ID: id, Name: req.Name, Email: req.Email}
	s.users[id] = u
	return u, nil
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
	// Store obs in the context once — every call (via CallVia/CallHandleVia
	// or nethttp.Call/CallHandle directly) that receives this ctx picks it
	// up automatically when CallOptions.Observer is nil.
	clientCtx := stats.WithObserver(context.Background(), obs)

	db := &userStore{users: make(map[string]contract.User)}

	// ── Server setup ──────────────────────────────────────────────────────────
	//
	// Register all routes from the shared contract. The client will import the
	// same route specs — the compiler enforces the contract at both ends.

	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})

	// The bearer security scheme (contract.BearerAuthScheme/BearerCredentialCodec)
	// is shared spec metadata; the actual verification logic lives HERE, in
	// the server's own runtime implementation — the contract package stays
	// adapter-agnostic. securedMw is the DECLARE-TIME-ONLY half (chained via
	// .Use(mw) inside contract.GetSecuredData) — it declares the Security
	// requirement but cannot run anything. securedImplMw is the SEPARATE
	// runtime enforcement half, attached below via Route.HandleMW, paired
	// against this same securedMw value.
	const validToken = "secret-token"
	securedMw := middleware.SecurityScheme("bearerAuth", contract.BearerAuthScheme, nil, &contract.BearerCredentialCodec)
	securedImplMw := middleware.ServerImplementation{
		Name:      "implement-scopes:bearerAuth",
		Satisfies: []string{"bearerAuth"},
		Fn: func(_ context.Context, r *http.Request, _ *struct{}) (map[string][]string, error) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token != validToken {
				return nil, fmt.Errorf("invalid token")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	}

	createRoute := contract.CreateUser.WithHandler(func(ctx context.Context, req contract.CreateUserReq) (contract.User, error) {
		u, err := db.create(req)
		if err != nil {
			// Returned as a plain Go error — the adapter consults the
			// route's declared rest.ErrorPattern automatically and writes
			// the typed EmailConflictError body + 409 status. No manual
			// status/body handling needed here.
			return contract.User{}, err
		}
		if h, ok := nethttp.ResponseHeadersFromContext(ctx); ok {
			h.Set("Location", "/users/"+u.ID)
		}
		return u, nil
	})
	mustServe(createRoute.Register(b), "register createUser")

	getRoute := contract.GetUser.WithHandler(func(_ context.Context, req contract.GetUserReq) (contract.User, error) {
		u, ok := db.get(req.ID)
		if !ok {
			return contract.User{}, fmt.Errorf("user not found")
		}
		return u, nil
	})
	mustServe(getRoute.Register(b), "register getUser")

	// GET /users/{id}/activity?filter=... — id is decoded from the path AND
	// filter from the query string, both merged into req by the adapter
	// (rest.NewPathParam/NewOptionalQueryParam declared them). The
	// X-Request-Id RESPONSE header is populated automatically from
	// u.RequestID by the adapter (rest.NewRequiredResponseHeaderParam
	// declared it) — no nethttp.WithResponseHeaders call needed here.
	activityRoute := contract.GetUserActivity.WithHandler(func(_ context.Context, req contract.GetUserActivityReq) (contract.User, error) {
		u, ok := db.get(req.ID)
		if !ok {
			return contract.User{}, fmt.Errorf("user not found")
		}
		u.Name = u.Name + " (filter=" + req.Filter + ")" // prove req.Filter reached the handler
		u.RequestID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		return u, nil
	})
	mustServe(activityRoute.Register(b), "register getUserActivity")

	// GET /profile — adapter validates session_token cookie and X-Request-ID
	// header automatically before this handler is called.
	// The adapter validates session_token cookie and X-Request-Id header before
	// calling this handler. The handler can read them via RequestFromContext if needed.
	profileRoute := contract.GetProfile.WithHandler(func(_ context.Context, _ struct{}) (contract.Profile, error) {
		return contract.Profile{
			ID:    "p1",
			Name:  "Alice",
			Email: "alice@example.com",
			Role:  "user",
		}, nil
	})
	mustServe(profileRoute.Register(b), "register getProfile")

	// GET /data — secured route; securedImplMw's Fn runs after codec
	// validation. HandleMW pairs the runtime enforcement half against the
	// same securedMw value declared (via .Use) inside GetSecuredData.
	securedRoute := contract.GetSecuredData(securedMw).WithHandler(func(_ context.Context, _ struct{}) (contract.Profile, error) {
		return contract.Profile{ID: "p1", Name: "Alice", Email: "alice@example.com", Role: "admin"}, nil
	}).HandleMW(&securedMw, securedImplMw.Fn)
	mustServe(securedRoute.Register(b), "register getSecuredData")

	mux := http.NewServeMux()
	mustServe(nethttp.Serve(mux, b), "Serve")

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// caller is built ONCE, right after the server starts, and reused by
	// every Call below — the recommended pattern for many calls to the
	// same (client, baseURL) pair.
	caller := nethttp.NewCaller(srv.Client(), srv.URL)

	// ── 1. Body — POST /users ─────────────────────────────────────────────────
	fmt.Println("=== 1. Body: POST /users (request body codec) ===")

	// Call takes the SAME contract.CreateUser rest.Route value directly —
	// it derives a client handle internally via Route.ClientHandle(), so
	// there is no separate "register a client copy" step needed at all.
	alice, err := nethttp.Call(clientCtx, caller, contract.CreateUser,
		contract.CreateUserReq{Name: "Alice", Email: "alice@example.com"},
		nethttp.CallOptions{}) // observer from clientCtx
	if err != nil {
		fmt.Fprintln(os.Stderr, "create alice:", err)
		os.Exit(1)
	}
	fmt.Printf("created: %+v\n\n", alice)

	// ── 1b. Client-side typed error decode (ErrorPatternResponse) ─────────────
	//
	// CreateUser declares rest.ErrorPattern[EmailConflictError, EmailConflictError](409, ...)
	// — the SAME codec drives the server's automatic typed body write AND the
	// client's automatic typed body decode. Calling with a duplicate email
	// gets back a nethttp.ErrorPatternResponse (not the untyped
	// nethttp.UnexpectedStatusError) whose Value already holds a decoded
	// contract.EmailConflictError — no manual status-switch or
	// json.Unmarshal needed.
	fmt.Println("=== 1b. Client-side typed error decode ===")

	_, err = nethttp.Call(clientCtx, caller, contract.CreateUser,
		contract.CreateUserReq{Name: "Alice Again", Email: "alice@example.com"}, // duplicate email
		nethttp.CallOptions{})
	if err == nil {
		fmt.Fprintln(os.Stderr, "expected email-conflict error, got nil")
		os.Exit(1)
	}
	var conflictResp nethttp.ErrorPatternResponse
	if errors.As(err, &conflictResp) {
		conflict, ok := conflictResp.Value.(contract.EmailConflictError)
		if !ok {
			fmt.Fprintf(os.Stderr, "unexpected Value type %T\n", conflictResp.Value)
			os.Exit(1)
		}
		logger.Warn("email conflict (typed, decoded automatically)",
			"status", conflictResp.StatusCode,
			"email", conflict.Email,
		)
		fmt.Printf("conflict: status=%d email=%s\n\n", conflictResp.StatusCode, conflict.Email)
	} else {
		// Falls back here for any status with no matching ErrorPattern, or
		// when the body fails to decode against the declared codec.
		fmt.Fprintln(os.Stderr, "expected ErrorPatternResponse, got:", err)
		os.Exit(1)
	}

	// ── 2. Path params — GET /users/{id} ──────────────────────────────────────
	//
	// contract.GetUserReq declares a path MERGE field (id, via
	// rest.NewPathParam) — Call derives the path value directly from
	// req.ID; there is no manual vars map anymore (every route intended
	// for client use must declare merge fields for the values it needs).
	fmt.Println("=== 2. Path params: GET /users/{id} ===")

	fetched, err := nethttp.Call(clientCtx, caller, contract.GetUser,
		contract.GetUserReq{ID: alice.ID},
		nethttp.CallOptions{}) // observer from clientCtx
	if err != nil {
		fmt.Fprintln(os.Stderr, "get alice:", err)
		os.Exit(1)
	}
	fmt.Printf("fetched: %+v\n", fetched)

	// Path param validation happens client-side before any HTTP call is
	// sent — but now surfaces as codex.ValidationError (from the merge
	// field's own codec, checked at DERIVE time), not rest.PathParamError
	// (which only ever fires from BuildPath's re-validation, unreachable
	// here since the merge field already rejects the value first).
	_, err = nethttp.Call(clientCtx, caller, contract.GetUser,
		contract.GetUserReq{ID: ""}, // empty — fails NonEmptyString codec
		nethttp.CallOptions{})       // observer from clientCtx
	if err != nil {
		var valErr codex.ValidationError
		if errors.As(err, &valErr) {
			logger.Warn("path param rejected (no request sent)",
				"field", valErr.Field,
				"cause", valErr.Err,
			)
		}
	}
	fmt.Println()

	// ── 2b. Client encode with role-aware merge fields + response merge ───────
	//
	// GetUserActivityReq declares BOTH a path merge field (id) and a query
	// merge field (filter) via rest.NewPathParam/NewOptionalQueryParam.
	// Call derives BOTH the URL path AND the query string from the SAME
	// request value automatically — RouteHandle.PathMergeFields()/
	// QueryMergeFields() (used internally) each return only their own
	// role's fields, so a path value can never leak into the query string
	// (or vice versa) even when both are declared on the same route.
	//
	// GetUserActivity ALSO declares a response header merge field
	// (User.RequestID via rest.NewRequiredResponseHeaderParam): the
	// X-Request-Id header the server sets is merged straight into
	// activity.RequestID automatically — the full request+response,
	// single-call story, with zero codex.EncodeVars calls needed at all.
	fmt.Println("=== 2b. Client encode: role-aware merge fields + response merge ===")

	activityReq := contract.GetUserActivityReq{ID: alice.ID, Filter: "logins"}
	activity, err := nethttp.Call(clientCtx, caller, contract.GetUserActivity,
		activityReq, nethttp.CallOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get user activity:", err)
		os.Exit(1)
	}
	fmt.Printf("activity: %+v (X-Request-Id merged into RequestID)\n\n", activity)

	// ── 3. Cookies + headers — GET /profile ───────────────────────────────────
	//
	// CookieParams and HeaderParams are codec-validated before the request is
	// sent. A violation aborts the call and returns the typed error — no network
	// round-trip wasted on a request that the server would reject anyway.
	fmt.Println("=== 3. Cookies + headers: GET /profile ===")

	// Happy path: valid session_token cookie and X-Request-Id header.
	profile, err := nethttp.Call(clientCtx, caller, contract.GetProfile, struct{}{},
		nethttp.CallOptions{
			CookieParams: map[string]string{
				"session_token": "my-valid-session-abc123",
			},
			HeaderParams: map[string]string{
				"X-Request-Id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			// observer from clientCtx
		})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get profile:", err)
		os.Exit(1)
	}
	fmt.Printf("profile: %+v\n", profile)

	// Cookie validation failure: empty session_token fails NonEmptyString codec.
	_, err = nethttp.Call(clientCtx, caller, contract.GetProfile, struct{}{},
		nethttp.CallOptions{
			CookieParams: map[string]string{"session_token": ""}, // invalid
			HeaderParams: map[string]string{"X-Request-Id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
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
	_, err = nethttp.Call(clientCtx, caller, contract.GetProfile, struct{}{},
		nethttp.CallOptions{
			CookieParams: map[string]string{"session_token": "my-valid-session-abc123"},
			HeaderParams: map[string]string{"X-Request-Id": "not-a-uuid"}, // invalid
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

	// ── 4. Security — GET /data (credential-providing ClientMW) ──────────────
	//
	// Route.ClientMW(mw, fn) declares the CLIENT-side fulfillment of a
	// security requirement, PAIRED against the SAME middleware.Middleware
	// value (securedMw, built above with contract.BearerAuthScheme/
	// BearerCredentialCodec) that also declares it server-side — fn
	// receives the resolved []route.SecurityRequirement and must return
	// headers to merge into the request. An error from fn aborts the call
	// before any network activity.
	//
	// Server declares, client fulfills: contract.GetSecuredData(securedMw)
	// attaches securedMw via .Use(...) INSIDE the contract package — the
	// SAME Security requirement then applies to BOTH Register (server) and
	// ClientHandle (client, called internally by Call). Since there is NO
	// per-call credential override anymore (a deliberate design tradeoff —
	// ClientMW declares fulfillment on the ROUTE itself, mirroring the
	// server side's declare-then-register discipline exactly), each
	// distinct credential behavior below builds its OWN route value via a
	// fresh .ClientMW(...) call rather than overriding at the call site.
	fmt.Println("=== 4. Security: GET /data (credential-providing ClientMW) ===")

	securedRouteWithAuth := contract.GetSecuredData(securedMw).ClientMW(&securedMw,
		func(_ context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
			// reqs contains the declared security requirements from the route spec.
			// In production: look up the token from a token store / refresh if expired.
			h := make(http.Header)
			h.Set("Authorization", "Bearer "+validToken)
			return h, nil
		})

	// Happy path: the credential fn declared via ClientMW runs automatically.
	data, err := nethttp.Call(clientCtx, caller, securedRouteWithAuth, struct{}{}, nethttp.CallOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "get secured data:", err)
		os.Exit(1)
	}
	fmt.Printf("secured data: %+v\n", data)

	// No credential-providing ClientMW attached: request is sent without
	// Authorization → server returns 401. This is NOT itself a client-side
	// error — the credential-format codec check only activates when a
	// credential mechanism actually supplies something (see the malformed
	// case right below); declining to supply one at all is a deliberate,
	// unchanged non-error, symmetric with server-side security enforcement.
	securedRouteNoAuth := contract.GetSecuredData(securedMw)
	_, err = nethttp.Call(clientCtx, caller, securedRouteNoAuth, struct{}{}, nethttp.CallOptions{})
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

	// The credential-providing ClientMW returns a MALFORMED credential
	// (empty after the "Bearer " prefix is stripped): contract.GetSecuredData's
	// declared Codec now rejects this LOCALLY, before any request is sent —
	// the same rest.SecurityCredentialError the SERVER would otherwise have
	// returned (401) is now caught client-side too, symmetric with the
	// server check.
	securedRouteMalformed := contract.GetSecuredData(securedMw).ClientMW(&securedMw,
		func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
			h := make(http.Header)
			h.Set("Authorization", "Bearer ") // strips to an empty credential
			return h, nil
		})
	_, err = nethttp.Call(clientCtx, caller, securedRouteMalformed, struct{}{}, nethttp.CallOptions{})
	if err != nil {
		var credErr rest.SecurityCredentialError
		if errors.As(err, &credErr) {
			logger.Error("malformed credential rejected locally (no request sent)",
				"scheme", credErr.Scheme,
				"cause", credErr.Err,
			)
		}
	}

	// The credential-providing ClientMW itself returns an error: aborts
	// the call client-side.
	tokenExpiredErr := fmt.Errorf("token expired")
	securedRouteErrCred := contract.GetSecuredData(securedMw).ClientMW(&securedMw,
		func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
			return nil, tokenExpiredErr
		})
	_, err = nethttp.Call(clientCtx, caller, securedRouteErrCred, struct{}{}, nethttp.CallOptions{})
	if err != nil {
		if errors.Is(err, tokenExpiredErr) {
			logger.Warn("credential error (no request sent)", "cause", err)
		}
	}
	fmt.Println()

	// ── 4b. Security — caching a CredentialFunc ────────────────────────────────
	//
	// Re-authenticating on every Call is wasteful when the underlying
	// credential fetch does real work (an OAuth token endpoint, a registry
	// token exchange, etc.). NewCachingCredentialFunc wraps any CredentialFunc
	// with TTL-based caching; concurrent callers during a cache miss share the
	// same in-flight call (hand-rolled single-flight). OnCredentialRejected +
	// the returned invalidate func implement the retry-once-on-401 pattern
	// explicitly — Call itself never retries automatically.
	fmt.Println("=== 4b. Security: caching a CredentialFunc ===")

	var innerCalls int
	// Simulates a token store that returns a STALE token once (already
	// rotated server-side) before refreshing to the current valid one —
	// demonstrating OnCredentialRejected forcing exactly one extra inner call.
	innerCredFn := func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
		innerCalls++
		h := make(http.Header)
		if innerCalls == 1 {
			h.Set("Authorization", "stale-token")
		} else {
			h.Set("Authorization", "Bearer "+validToken)
		}
		return h, nil
	}
	cachedCredFn, invalidateCred := nethttp.NewCachingCredentialFunc(innerCredFn, nethttp.CachingCredentialFuncOptions{
		TTL: time.Hour,
	})
	cachedCallOpts := nethttp.CallOptions{
		OnCredentialRejected: invalidateCred,
	}
	// Declared ONCE via .ClientMW(...) — every Call below reuses the SAME
	// route value, so the cached credential function is shared across
	// calls automatically.
	securedRouteCached := contract.GetSecuredData(securedMw).ClientMW(&securedMw, cachedCredFn)

	// First call: the stale cached token is rejected (401). OnCredentialRejected
	// purges the cache, so an explicit retry fetches a fresh credential.
	_, err = nethttp.Call(clientCtx, caller, securedRouteCached, struct{}{}, cachedCallOpts)
	var rejectedErr nethttp.UnexpectedStatusError
	if errors.As(err, &rejectedErr) && rejectedErr.StatusCode == http.StatusUnauthorized {
		logger.Info("credential rejected — retrying once with a refreshed credential")
		data, err = nethttp.Call(clientCtx, caller, securedRouteCached, struct{}{}, cachedCallOpts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "retry after credential refresh:", err)
			os.Exit(1)
		}
		fmt.Printf("secured data (after credential refresh): %+v\n", data)
	}

	// Second call reuses the now-valid cached credential — inner is NOT
	// invoked again.
	data, err = nethttp.Call(clientCtx, caller, securedRouteCached, struct{}{}, cachedCallOpts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cached call:", err)
		os.Exit(1)
	}
	fmt.Printf("secured data (cache hit, inner invoked %d time(s) total): %+v\n", innerCalls, data)
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

// mustServe exits the process if registering a route or wiring the final
// mux via nethttp.Serve fails — both return an error (e.g. duplicate
// path/method) that a real server must not silently ignore.
func mustServe(err error, what string) {
	if err != nil {
		fmt.Fprintln(os.Stderr, what+":", err)
		os.Exit(1)
	}
}
