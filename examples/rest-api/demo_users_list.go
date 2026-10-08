package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoListUsers exercises GET /users (requires the "profile" scope) —
// against BOTH chiClient AND nethttpClient, the SAME usersRouter-grouped
// declaration assembled onto either adapter — query params auto-derived
// from ListUsersReq via the freshly-shipped RouteHandle.EncodeQueryVars.
// The invalid-page demo stays a RAW HTTP call — a typed client's Page
// field is a Go int and literally cannot express "abc", so that
// rejection can only be shown from a non-go-codex caller's perspective.
func demoListUsers(chiClient, nethttpClient *rest.Client, chiBaseURL, nethttpBaseURL string) {
	fmt.Println("=== GET /users?page=2&search=alice — profile scope, query auto-derived ===")
	clients := []struct {
		label string
		c     *rest.Client
	}{
		{"chi", chiClient},
		{"net/http", nethttpClient},
	}
	for _, cl := range clients {
		respAny, err := cl.c.Call(context.Background(), restapiclient.ListUsersRouteAsAlice, routes.ListUsersReq{Page: 2, Search: "alice"})
		if err != nil {
			fmt.Printf("  [%s] error: %v\n", cl.label, err)
		} else {
			fmt.Printf("  [%s] result: %+v\n", cl.label, respAny.(routes.PagedUsersResp))
		}
	}
	fmt.Println()

	fmt.Println("=== GET /users?page=abc (raw, non-go-codex client → 400) ===")
	baseURLs := []struct {
		label string
		url   string
	}{
		{"chi", chiBaseURL},
		{"net/http", nethttpBaseURL},
	}
	for _, b := range baseURLs {
		func() {
			req, _ := http.NewRequest(http.MethodGet, b.url+"/users?page=abc", nil) //nolint:noctx
			req.Header.Set("Authorization", "******")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Printf("  [%s] error: %v\n", b.label, err)
				return
			}
			defer resp.Body.Close()
			fmt.Printf("  [%s] Status: %s\n", b.label, resp.Status)
		}()
	}
	fmt.Println()
}
