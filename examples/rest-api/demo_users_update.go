package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoUpdateUser exercises PUT /users/{id} (requires the "admin" scope) —
// against BOTH chiClient AND nethttpClient, the SAME usersRouter-grouped
// declaration assembled onto either adapter — MIXED body+path merge on
// ONE struct: UpdateUserReq.ID is auto-derived into the URL path,
// Name/Email are auto-encoded into the JSON body, all from ONE
// client.Call.
func demoUpdateUser(chiClient, nethttpClient *rest.Client) {
	fmt.Println("=== PUT /users/{id} — admin scope, mixed body+path merge on ONE struct ===")
	clients := []struct {
		label string
		c     *rest.Client
	}{
		{"chi", chiClient},
		{"net/http", nethttpClient},
	}
	for _, cl := range clients {
		respAny, err := cl.c.Call(context.Background(), restapiclient.UpdateUserRouteAsAdmin, routes.UpdateUserReq{
			ID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			Name:  "Alice Updated",
			Email: "alice.updated@example.com",
		})
		if err != nil {
			fmt.Printf("  [%s] error: %v\n", cl.label, err)
		} else {
			fmt.Printf("  [%s] user: %+v\n", cl.label, respAny.(routes.User))
		}
	}
	fmt.Println()
}
