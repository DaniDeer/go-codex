package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// alwaysGrantAdmin is a trivial ServerImplementation Fn for the ISOLATED
// scratch servers below — these demos test RESPONSE-side codec
// enforcement (header/cookie/body), not security, so authentication is
// bypassed by always granting the "admin" scope regardless of the
// incoming request.
func alwaysGrantAdmin(_ context.Context, _ *http.Request, _ *routes.CreateUserReq) (map[string][]string, error) {
	return map[string][]string{"bearerAuth": {"admin"}}, nil
}

// demoResponseHeaderCookieViolation proves the SAME codec that rejects an
// invalid REQUEST body at 400 also rejects an invalid RESPONSE
// header/cookie at 500 — server-side symmetric validation. Uses its OWN
// scratch server (routes.CreateUserRoute reused unchanged, but with a
// deliberately-broken handler) so this never touches the primary servers
// built in main().
func demoResponseHeaderCookieViolation() {
	fmt.Println("=== ResponseHeaderParam + ResponseCookieParam — contract violation (chi) ===")

	violationRoute := routes.CreateUserRoute.WithHandler(
		func(ctx context.Context, _ routes.CreateUserReq) (routes.User, error) {
			h := make(http.Header)
			h.Set("Location", "") // empty → fails NonEmptyString → 500
			chiadapter.WithResponseHeaders(ctx, h)
			chiadapter.WithResponseCookies(ctx, chiadapter.PendingCookie{
				Name:  "session",
				Value: "short", // < 8 chars → fails MinLen(8) → 500
				Opts:  chiadapter.CookieOptions{Insecure: true},
			})
			return routes.User{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Carol", Email: "carol@example.com"}, nil
		},
	).HandleMW(&routes.AdminScopeMw, alwaysGrantAdmin)

	b := rest.NewServer(rest.Info{Title: "violation demo", Version: "1.0.0"})
	must(violationRoute.Register(b), "register violation route")
	router := gochi.NewRouter()
	addr := mustFreeAddr()
	must(chiadapter.AttachRouter(b, router, addr), "AttachRouter violation")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Serve(ctx) }()
	waitForReady(addr)

	// alwaysGrantAdmin bypasses the ACTUAL scope check, but the built-in
	// credential FORMAT check (BearerCodec) still runs on whatever
	// Authorization header IS present — a well-formed-but-fake token is
	// required so that format check passes and alwaysGrantAdmin is
	// reached at all.
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/users", strings.NewReader(`{"name":"Carol","email":"carol@example.com"}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any-well-formed-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	var errBody map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	fmt.Printf("  Status: %s, error: %s\n", resp.Status, errBody["error"])
	fmt.Println()
}

// demoResponseBodyViolation proves Refine constraints run on ENCODE too,
// not just decode: a handler returning an invalid User triggers a 500,
// not a silently-corrupted response. Uses nethttp.ServeOne (the sugar
// wrapper around a scratch, single-route Server — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md's
// "Decision: Serve is the only public server-side entry point").
func demoResponseBodyViolation() {
	fmt.Println("=== Response body encode violation — symmetric validation (net/http) ===")

	violationRoute := routes.CreateUserRoute.WithHandler(
		func(_ context.Context, _ routes.CreateUserReq) (routes.User, error) {
			// Deliberately returns a User with invalid field values to
			// demonstrate that handle.Encode now validates the response body.
			return routes.User{
				ID:    "not-a-uuid",   // fails UUID constraint
				Name:  "",             // fails NonEmptyString constraint
				Email: "not-an-email", // fails Email constraint
			}, nil
		},
	).HandleMW(&routes.AdminScopeMw, alwaysGrantAdmin)

	handler, err := nethttp.ServeOne(violationRoute)
	must(err, "ServeOne body-violation route")
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/users", strings.NewReader(`{"name":"Dave","email":"dave@example.com"}`)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer any-well-formed-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	var errBody map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	fmt.Printf("  Status: %s, error: %s\n", resp.Status, errBody["error"])
	fmt.Println()
}
