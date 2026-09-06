package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoListUsers exercises GET /users (requires the "profile" scope)
// against chiClient — query params auto-derived from ListUsersReq via
// the freshly-shipped RouteHandle.EncodeQueryVars. The invalid-page demo
// stays a RAW HTTP call — a typed client's Page field is a Go int and
// literally cannot express "abc", so that rejection can only be shown
// from a non-go-codex caller's perspective.
func demoListUsers(chiClient *rest.Client, baseURL string) {
	fmt.Println("=== GET /users?page=2&search=alice — profile scope, query auto-derived ===")
	respAny, err := chiClient.Call(context.Background(), restapiclient.ListUsersRouteAsAlice, routes.ListUsersReq{Page: 2, Search: "alice"})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  result: %+v\n", respAny.(routes.PagedUsersResp))
	}
	fmt.Println()

	fmt.Println("=== GET /users?page=abc (raw, non-go-codex client → 400) ===")
	func() {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/users?page=abc", nil) //nolint:noctx
		req.Header.Set("Authorization", "Bearer valid-user-token")
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
