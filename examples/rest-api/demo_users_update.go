package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoUpdateUser exercises PUT /users/{id} (requires the "admin" scope)
// against chiClient — MIXED body+path merge on ONE struct:
// UpdateUserReq.ID is auto-derived into the URL path, Name/Email are
// auto-encoded into the JSON body, all from ONE client.Call.
func demoUpdateUser(chiClient *rest.Client) {
	fmt.Println("=== PUT /users/{id} — admin scope, mixed body+path merge on ONE struct ===")
	respAny, err := chiClient.Call(context.Background(), restapiclient.UpdateUserRouteAsAdmin, routes.UpdateUserReq{
		ID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Name:  "Alice Updated",
		Email: "alice.updated@example.com",
	})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  user: %+v\n", respAny.(routes.User))
	}
	fmt.Println()
}
