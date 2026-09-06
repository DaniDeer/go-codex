package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoGetUser exercises GET /users/{id} (requires the "profile" scope)
// against chiClient — client.Call auto-derives the {id} path segment from
// GetUserReq.ID via the freshly-shipped RouteHandle.EncodeVars (no manual
// BuildPath call needed for the happy path); BuildPath itself is
// demonstrated directly on the handle, ONLY for the invalid-UUID
// rejection case (a real uuid.UUID Go value can never itself be
// malformed, so that demo must stay at the string-keyed handle level).
func demoGetUser(chiClient *rest.Client, getUserHandle *rest.RouteHandle[routes.GetUserReq, routes.User]) {
	ctx := context.Background()

	fmt.Println("=== GET /users/{id} — profile scope, path auto-derived from GetUserReq.ID ===")
	id := uuid.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	respAny, err := chiClient.Call(ctx, restapiclient.GetUserRouteAsAlice, routes.GetUserReq{ID: id})
	if err != nil {
		fmt.Printf("  error: %v\n", err)
	} else {
		fmt.Printf("  user: %+v\n", respAny.(routes.User))
	}
	fmt.Println()

	fmt.Println("=== GET /users/{id} — BuildPath rejects an invalid UUID (handle-level) ===")
	if _, err := getUserHandle.BuildPath(map[string]string{"id": "not-a-uuid"}); err != nil {
		fmt.Printf("  BuildPath(not-a-uuid) rejected: %v\n", err)
	}
	fmt.Println()
}
