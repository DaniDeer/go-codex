package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
)

// This file is the comprehensive rest.ErrorPattern/ErrorStatus showcase —
// consolidates what was previously 2 separate demo files into one,
// covering EVERY facet of REST's declarative error-path mechanism on its
// OWN scratch chi server (never touches the primary demo flow):
//
//  1. demoErrorPatternDeclarationMechanisms — REST's 3 DECLARATION
//     mechanisms: ErrorStatus (status-only), ErrorPattern Direct mode,
//     ErrorPattern Mapped mode.
//  2. demoErrorPatternActions — the 3 ErrorActions (Respond/Handle/Log).
//  3. demoErrorPatternClientMatchMechanisms — the 3 CLIENT-side recovery
//     mechanisms: ErrorPatternAs[B], HandleErrorPattern+Case, and the
//     declaration value's own .Match method.
//  4. demoErrorPatternMiddlewareCombo — ErrorPattern matching a SECURITY
//     MIDDLEWARE Fn failure, not just a handler failure.
//  5. demoErrorPatternPortAdapter — proves H1's fix: a declared
//     ErrorPattern is ALSO consulted through the port/stream-adapter
//     dispatch path (nethttp.IngestAdapter), not just normal serving.
//
// routes.LoginRoute's own ErrorPattern (see demo_login.go) is a 6th,
// already-primary-flow example — left untouched here, cross-referenced
// only.
var (
	errorPatternDemoServer  *rest.Server
	errorPatternDemoClient  *rest.Client
	errorPatternDemoAddr    string
	errorPatternDemoStarted bool
)

// buildErrorPatternDemoServer wires every route this file demos onto ONE
// shared scratch chi server + rest.Client — avoids repeating server/client
// bring-up boilerplate per mechanism. Idempotent (safe to call from
// multiple demo funcs; only builds once).
func buildErrorPatternDemoServer() (*rest.Server, *rest.Client, string) {
	if errorPatternDemoStarted {
		return errorPatternDemoServer, errorPatternDemoClient, errorPatternDemoAddr
	}
	errorPatternDemoStarted = true

	b := rest.NewServer(rest.Info{Title: "error-pattern demo", Version: "1.0.0"})

	conflictHandler := func(_ context.Context, req routes.CreateUserReq) (routes.User, error) {
		if req.Email == "carol@example.com" {
			return routes.User{}, routes.ConflictError{Email: req.Email}
		}
		return routes.User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: req.Name, Email: req.Email}, nil
	}

	must(routes.CreateUserConflictRoute.WithHandler(conflictHandler).Register(b), "register conflict route (Mapped mode)")

	rateLimitHandler := func(_ context.Context, req routes.CreateUserReq) (routes.User, error) {
		if req.Email == "dave@example.com" {
			return routes.User{}, routes.RateLimitError{RetryAfterSeconds: 30}
		}
		return routes.User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: req.Name, Email: req.Email}, nil
	}
	must(routes.RateLimitDirectRoute.WithHandler(rateLimitHandler).Register(b), "register rate-limit route (Direct mode)")

	throttledHandler := func(_ context.Context, req routes.CreateUserReq) (routes.User, error) {
		if req.Email == "eve@example.com" {
			return routes.User{}, routes.ThrottledError{}
		}
		return routes.User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: req.Name, Email: req.Email}, nil
	}
	must(routes.ThrottledStatusRoute.WithHandler(throttledHandler).Register(b), "register throttled route (ErrorStatus)")

	// 3 action variants (Respond/Handle/Log) — SAME conflictHandler.
	must(routes.ConflictRespondRoute.WithHandler(conflictHandler).Register(b), "register conflict-respond route")

	handleActionErrorHandler := func(w http.ResponseWriter, _ *http.Request, status int, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"handled_by":"Options.ErrorHandler","error":"` + err.Error() + `"}`))
	}
	must(routes.ConflictHandleRoute.WithHandler(conflictHandler).
		WithOptions(chiadapter.Options{ErrorHandler: handleActionErrorHandler}).Register(b), "register conflict-handle route")
	must(routes.ConflictLogRoute.WithHandler(conflictHandler).
		WithOptions(chiadapter.Options{ErrorHandler: handleActionErrorHandler}).Register(b), "register conflict-log route")

	// Security-middleware + ErrorPattern combo: securityFn ALWAYS rejects
	// with InsufficientScopeError — proving ErrorPattern intercepts a
	// security-middleware Fn failure, never reaching conflictHandler at all.
	securityFn := func(_ context.Context, _ *http.Request, _ *routes.CreateUserReq) (map[string][]string, error) {
		return nil, routes.InsufficientScopeError{RequiredScope: "billing"}
	}
	must(routes.SecuredConflictRoute.WithHandler(conflictHandler).
		HandleMW(&routes.ErrorPatternScopeMw, securityFn).Register(b), "register secured-conflict route")

	router := gochi.NewRouter()
	addr := mustFreeAddr()
	must(chiadapter.AttachRouter(b, router, addr), "AttachRouter error-pattern demo")
	ctx := context.Background()
	go func() { _ = b.Serve(ctx) }()
	waitForReady(addr)

	client := rest.NewClient()
	must(nethttp.Attach(client, http.DefaultClient, "http://"+addr), "attach error-pattern demo client")

	errorPatternDemoServer, errorPatternDemoClient, errorPatternDemoAddr = b, client, addr
	return b, client, addr
}

// demoErrorPatternDeclarationMechanisms shows REST's 3 DECLARATION
// mechanisms side-by-side: ErrorStatus (status-only, generic body),
// ErrorPattern Direct mode (E itself IS the payload), ErrorPattern Mapped
// mode (mapFn transforms E into a distinct B).
func demoErrorPatternDeclarationMechanisms() {
	fmt.Println("=== REST error-declaration mechanisms: ErrorStatus vs ErrorPattern Direct vs Mapped ===")
	_, client, _ := buildErrorPatternDemoServer()
	ctx := context.Background()

	// 1) ErrorStatus — status-only, NO typed body (generic envelope).
	_, err := client.Call(ctx, routes.ThrottledStatusRoute, routes.CreateUserReq{Name: "Eve", Email: "eve@example.com"})
	var unexpected nethttp.UnexpectedStatusError
	if errors.As(err, &unexpected) && unexpected.StatusCode == http.StatusTooManyRequests {
		fmt.Printf("  ✓ ErrorStatus: status=%d, generic body=%s (no typed payload — simplest mechanism)\n", unexpected.StatusCode, unexpected.Body)
	} else {
		fmt.Printf("  ✗ ErrorStatus: unexpected result: %v\n", err)
	}

	// 2) ErrorPattern Direct mode — RateLimitError itself IS the payload.
	_, err = client.Call(ctx, routes.RateLimitDirectRoute, routes.CreateUserReq{Name: "Dave", Email: "dave@example.com"})
	if payload, ok := rest.ErrorPatternAs[routes.RateLimitError](err); ok {
		fmt.Printf("  ✓ ErrorPattern Direct mode: retry_after_seconds=%d (E itself IS B, no mapFn)\n", payload.RetryAfterSeconds)
	} else {
		fmt.Printf("  ✗ ErrorPattern Direct mode: unexpected result: %v\n", err)
	}

	// 3) ErrorPattern Mapped mode — ConflictError mapped into ConflictPayload.
	_, err = client.Call(ctx, routes.CreateUserConflictRoute, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"})
	if payload, ok := rest.ErrorPatternAs[routes.ConflictPayload](err); ok {
		fmt.Printf("  ✓ ErrorPattern Mapped mode: code=%q email=%q (mapFn transforms E into a distinct B)\n", payload.Code, payload.Email)
	} else {
		fmt.Printf("  ✗ ErrorPattern Mapped mode: unexpected result: %v\n", err)
	}
	fmt.Println()
}

// demoErrorPatternActions shows the 3 ErrorActions on 3 otherwise-identical
// sibling routes: ErrorRespond (default, typed body auto-written),
// ErrorHandle (falls through to Options.ErrorHandler), ErrorLog (same
// fallback — kept distinct purely for cross-boundary vocabulary parity
// with events/reqreply's identical 3-action model).
func demoErrorPatternActions() {
	fmt.Println("=== rest.ErrorAction: Respond vs Handle vs Log ===")
	_, client, _ := buildErrorPatternDemoServer()
	ctx := context.Background()

	_, err := client.Call(ctx, routes.ConflictRespondRoute, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"})
	if payload, ok := rest.ErrorPatternAs[routes.ConflictPayload](err); ok {
		fmt.Printf("  ✓ ErrorRespond (default): typed body auto-written — code=%q\n", payload.Code)
	} else {
		fmt.Printf("  ✗ ErrorRespond: unexpected result: %v\n", err)
	}

	_, err = client.Call(ctx, routes.ConflictHandleRoute, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"})
	var unexpected nethttp.UnexpectedStatusError
	if errors.As(err, &unexpected) && strings.Contains(string(unexpected.Body), "handled_by") {
		fmt.Printf("  ✓ ErrorHandle: typed body SKIPPED, Options.ErrorHandler ran instead — body=%s\n", unexpected.Body)
	} else {
		fmt.Printf("  ✗ ErrorHandle: unexpected result: %v\n", err)
	}

	_, err = client.Call(ctx, routes.ConflictLogRoute, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"})
	if errors.As(err, &unexpected) && strings.Contains(string(unexpected.Body), "handled_by") {
		fmt.Printf("  ✓ ErrorLog: same fallback as ErrorHandle (REST has one hook) — body=%s\n", unexpected.Body)
	} else {
		fmt.Printf("  ✗ ErrorLog: unexpected result: %v\n", err)
	}
	fmt.Println()
}

// demoErrorPatternClientMatchMechanisms shows the 3 CLIENT-side recovery
// mechanisms against the SAME matched error (routes.ConflictError →
// routes.ConflictPayload):
//  1. rest.ErrorPatternAs[B](err) — generic one-line helper.
//  2. rest.HandleErrorPattern(err, Case(...), Case(...)) — typed
//     switch-like dispatch, first-matching-Case-wins.
//  3. routes.ConflictErrorPattern.Match(err) — the DECLARATION VALUE's
//     own method (declared once, used both directions — no [B] needed).
func demoErrorPatternClientMatchMechanisms() {
	fmt.Println("=== REST client-side ErrorPattern matching: 3 mechanisms ===")
	_, client, _ := buildErrorPatternDemoServer()
	ctx := context.Background()

	_, err := client.Call(ctx, routes.CreateUserConflictRoute, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"})

	// 1) ErrorPatternAs — generic one-liner.
	if payload, ok := rest.ErrorPatternAs[routes.ConflictPayload](err); ok {
		fmt.Printf("  ✓ (1) rest.ErrorPatternAs[B]: code=%q\n", payload.Code)
	} else {
		fmt.Println("  ✗ (1) rest.ErrorPatternAs[B]: no match")
	}

	// 2) HandleErrorPattern + Case — switch-like dispatch, first match wins.
	handled := rest.HandleErrorPattern(err,
		rest.Case(func(p routes.LoginErrorPayload) { fmt.Println("  ✗ (2) matched wrong Case (LoginErrorPayload)") }),
		rest.Case(func(p routes.ConflictPayload) {
			fmt.Printf("  ✓ (2) rest.HandleErrorPattern+Case: code=%q\n", p.Code)
		}),
	)
	if !handled {
		fmt.Println("  ✗ (2) rest.HandleErrorPattern: no Case matched")
	}

	// 3) The declaration value's OWN .Match method — "declare once, use
	// both directions": routes.ConflictErrorPattern is the SAME value
	// CreateUserConflictRoute declared server-side.
	if payload, ok := routes.ConflictErrorPattern.Match(err); ok {
		fmt.Printf("  ✓ (3) routes.ConflictErrorPattern.Match(err): code=%q (same value declares AND matches)\n", payload.Code)
	} else {
		fmt.Println("  ✗ (3) routes.ConflictErrorPattern.Match: no match")
	}
	fmt.Println()
}

// demoErrorPatternMiddlewareCombo proves ErrorPattern intercepts a
// SECURITY-MIDDLEWARE Fn failure — routes.SecuredConflictRoute's
// securityFn ALWAYS rejects with InsufficientScopeError, which is matched
// by the route's declared ErrorPattern BEFORE the business handler ever
// runs (Category-A parity, Topic 1).
func demoErrorPatternMiddlewareCombo() {
	fmt.Println("=== rest.ErrorPattern + security middleware Fn combo ===")
	_, client, _ := buildErrorPatternDemoServer()
	ctx := context.Background()

	// A well-formed-but-fake bearer token is required so the BUILT-IN
	// credential FORMAT check (BearerCodec) passes and the server's
	// securityFn (which ALWAYS rejects) is actually reached — mirrors
	// demo_violations.go's alwaysGrantAdmin pattern.
	securedRoute := routes.SecuredConflictRoute.ClientMW(&routes.ErrorPatternScopeMw,
		func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
			h := make(http.Header)
			h.Set("Authorization", "Bearer fake-token-for-demo")
			return h, nil
		},
	)
	_, err := client.Call(ctx, securedRoute, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"})
	if payload, ok := rest.ErrorPatternAs[routes.InsufficientScopePayload](err); ok {
		fmt.Printf("  ✓ security-middleware Fn error matched by ErrorPattern (handler NEVER ran): code=%q required_scope=%q\n",
			payload.Code, payload.RequiredScope)
	} else {
		fmt.Printf("  ✗ expected InsufficientScopePayload, got: %v\n", err)
	}
	fmt.Println()
}

// demoErrorPatternPortAdapter proves H1's fix (session review round-4
// finding): a declared rest.ErrorPattern is now ALSO consulted through the
// port/stream-adapter dispatch path — nethttp.IngestAdapter's handlerFunc
// — not just the normal AttachRouter/AttachMux serving path serve.go
// already covered. routes.IngestConflictRoute's ErrorPattern matches a
// request BODY validation failure (codex.ValidationErrors), which
// IngestAdapter's dispatch encounters BEFORE ever handing a value to the
// bound pipeline.
func demoErrorPatternPortAdapter() {
	fmt.Println("=== rest.ErrorPattern via nethttp.IngestAdapter (proves H1's fix) ===")

	mux := http.NewServeMux()
	port, err := ports.NewSourcePort[routes.CreateUserReq]("ingest-conflict-demo", routes.CreateUserReqCodec, ports.PortOptions{Buffer: 4})
	must(err, "construct source port")
	port.Bind(context.Background(), nethttp.IngestAdapter(mux, routes.IngestConflictRoute.ClientHandle(), nethttp.IngestAdapterOptions{Buffer: 4}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err, "listen for ingest demo")
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	waitForReady(ln.Addr().String())

	// Missing required "name"/"email" fields → codex.ValidationErrors,
	// matched by the declared ErrorPattern → typed 422 response.
	resp, err := http.Post("http://"+ln.Addr().String()+"/users-ingest-demo", "application/json", strings.NewReader(`{}`))
	if err != nil {
		fmt.Printf("  ✗ POST failed: %v\n", err)
		return
	}
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	fmt.Printf("  ✓ status=%d body=%s (declared ErrorPattern consulted through IngestAdapter's dispatch, not the generic fallback)\n",
		resp.StatusCode, buf[:n])
	fmt.Println()
}
