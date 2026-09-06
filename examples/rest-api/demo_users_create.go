package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
	"github.com/DaniDeer/go-codex/format"
)

// demoCreateUser exercises POST /users (requires the "admin" scope)
// against chiClient — security gating (both "no credential" and "wrong
// scope" resolve to 401 in go-codex — there's no separate 403 concept;
// see middleware.CheckScopes/rest.SecurityError), multi-format
// request/response bodies, codec-validated response header + cookie, and
// a client-side pre-flight body-constraint rejection.
func demoCreateUser(chiClient *rest.Client, baseURL string) {
	ctx := context.Background()

	fmt.Println("=== POST /users — unauthenticated (expect 401) ===")
	_, err := chiClient.Call(ctx, restapiclient.CreateUserRouteUnauthenticated, routes.CreateUserReq{Name: "Dave", Email: "dave@example.com"})
	printStatusErr(err)
	fmt.Println()

	fmt.Println("=== POST /users — wrong scope (Alice lacks admin, expect 401 — go-codex has no separate 403) ===")
	_, err = chiClient.Call(ctx, restapiclient.CreateUserRouteAsAlice, routes.CreateUserReq{Name: "Dave", Email: "dave@example.com"})
	printStatusErr(err)
	fmt.Println()

	fmt.Println("=== POST /users — admin, JSON (expect 201) ===")
	respAny, err := chiClient.Call(ctx, restapiclient.CreateUserRouteAsAdmin, routes.CreateUserReq{Name: "Alice", Email: "alice@example.com"})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  user: %+v\n", respAny.(routes.User))
	}
	fmt.Println()

	fmt.Println("=== POST /users — admin, YAML response (ClientCallOptions.ResponseFormats) ===")
	respAny, err = chiClient.Call(ctx, restapiclient.CreateUserRouteAsAdmin, routes.CreateUserReq{Name: "Bob", Email: "bob@example.com"},
		rest.ClientCallOptions{ResponseFormats: []format.Format[routes.User]{format.YAML(routes.UserCodec)}})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  user (decoded from YAML response): %+v\n", respAny.(routes.User))
	}
	fmt.Println()

	fmt.Println("=== POST /users — admin, YAML request body (ClientCallOptions.RequestFormats) ===")
	respAny, err = chiClient.Call(ctx, restapiclient.CreateUserRouteAsAdmin, routes.CreateUserReq{Name: "Carol", Email: "carol@example.com"},
		rest.ClientCallOptions{RequestFormats: []format.Format[routes.CreateUserReq]{format.YAML(routes.CreateUserReqCodec)}})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  user: %+v\n", respAny.(routes.User))
	}
	fmt.Println()

	fmt.Println("=== POST /users — body constraint violation (client-side PRE-FLIGHT rejection) ===")
	// The SAME codec enforces the rule on BOTH sides — a well-behaved
	// go-codex client never even sends this: CreateUserReqCodec's Refine
	// constraints fail during EncodeRequestWithFormats, LOCALLY, before
	// any network call is made.
	_, err = chiClient.Call(ctx, restapiclient.CreateUserRouteAsAdmin, routes.CreateUserReq{Name: "", Email: "bad"})
	fmt.Printf("  client-side rejection (never reached the network): %v\n", err)
	fmt.Println()

	fmt.Println("=== POST /users — unsupported Content-Type (raw, non-go-codex client → 415) ===")
	// A typed go-codex client cannot express an unsupported Content-Type
	// at all — this demo uses a RAW http.Client to show what happens when
	// a non-go-codex caller (curl, another language) sends something the
	// route doesn't accept.
	func() {
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/users", strings.NewReader(`<name>Dave</name>`)) //nolint:noctx
		req.Header.Set("Content-Type", "application/xml")
		req.Header.Set("Authorization", "Bearer valid-admin-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("  error: %v\n", err)
			return
		}
		defer resp.Body.Close()
		fmt.Printf("  Status: %s\n", resp.Status)
	}()
	fmt.Println()
}

// printStatusErr prints an UnexpectedStatusError's status code, or the
// raw error if it's not that type (e.g. a client-side validation error).
func printStatusErr(err error) {
	var statusErr nethttp.UnexpectedStatusError
	if errors.As(err, &statusErr) {
		fmt.Printf("  Status: %d\n", statusErr.StatusCode)
		return
	}
	fmt.Printf("  error: %v\n", err)
}
