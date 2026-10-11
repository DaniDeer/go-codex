package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoRedirect exercises docs/features/rest-redirects.md's Typed HTTP
// Redirects end to end against BOTH chiClient AND nethttpClient —
// POST /users-redirect-demo creates a user then 303-redirects to the
// REAL Router-grouped GetUserRoute ("/users/{id}", composed from its own
// relative "/{id}" declaration only via usersRouter — see
// routes.CreateUserRedirectRoute's own doc comment for why
// rest.WithRouter(usersRouter) is required server-side for this to
// resolve correctly).
//
// Two scenarios, demonstrating the registry's two distinct population
// paths (examples/rest-redirect's own standalone example demonstrates
// the OTHER one — auto-populate-via-Call — in isolation):
//
//  1. Primary workflow: Client.Call transparently auto-follows the
//     redirect, because GetUserRoute was ALREADY known to chiClient/
//     nethttpClient from restapiclient.Build()'s CONSTRUCTION-TIME
//     Client.RegisterRoute loop — not from incidental demo-call order.
//     demoGetUser (which also calls GetUserRoute directly) runs AFTER
//     this demo in main.go's sequence, confirming the registration
//     really did come from Build(), not a lucky ordering coincidence.
//  2. Escape hatch: rest.CallWithTransport stays registry-free — it
//     returns the typed rest.RedirectError directly, with NO auto-follow,
//     even though the SAME underlying route/credential is used.
func demoRedirect(chiClient, nethttpClient *rest.Client, chiBaseURL, nethttpBaseURL string) {
	ctx := context.Background()
	clients := []struct {
		label   string
		c       *rest.Client
		baseURL string
	}{
		{"chi", chiClient, chiBaseURL},
		{"net/http", nethttpClient, nethttpBaseURL},
	}

	fmt.Println("=== POST /users-redirect-demo — primary workflow: Client.Call auto-follows the 303 (registry populated at Build() construction time) ===")
	for _, cl := range clients {
		respAny, err := cl.c.Call(ctx, restapiclient.CreateUserRedirectRouteAsAlice,
			routes.CreateUserReq{Name: "Redirected Dave", Email: "redirected-dave@example.com"})
		if err != nil {
			fmt.Printf("  [%s] error: %v\n", cl.label, err)
			continue
		}
		u, ok := respAny.(routes.User)
		if !ok {
			fmt.Printf("  [%s] unexpected response type %T\n", cl.label, respAny)
			continue
		}
		fmt.Printf("  [%s] auto-followed to GetUserRoute's own response: id=%s name=%s\n", cl.label, u.ID, u.Name)
	}
	fmt.Println()

	fmt.Println("=== POST /users-redirect-demo — escape hatch: CallWithTransport stays registry-free, returns the typed RedirectError directly ===")
	for _, cl := range clients {
		transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: cl.baseURL})
		_, err := rest.CallWithTransport(ctx, transport, restapiclient.CreateUserRedirectRouteAsAlice,
			routes.CreateUserReq{Name: "Escape Hatch", Email: "escape-hatch@example.com"})
		var redirErr rest.RedirectError
		if !errors.As(err, &redirErr) {
			fmt.Printf("  [%s] want rest.RedirectError, got %T: %v\n", cl.label, err, err)
			continue
		}
		fmt.Printf("  [%s] got %d redirect to %q (NOT auto-followed — no *rest.Client/registry involved)\n",
			cl.label, redirErr.Status, redirErr.Location)
	}
	fmt.Println()
}
