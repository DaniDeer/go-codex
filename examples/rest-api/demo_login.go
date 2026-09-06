package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// demoLogin exercises POST /login (public, no security) against BOTH
// servers via the SAME declared route + client — proving the wire
// protocol is identical whether chi or net/http routes the request.
func demoLogin(chiClient, nethttpClient *rest.Client) {
	fmt.Println("=== POST /login (public) ===")
	clients := []struct {
		label string
		c     *rest.Client
	}{
		{"chi", chiClient},
		{"net/http", nethttpClient},
	}
	for _, cl := range clients {
		respAny, err := cl.c.Call(context.Background(), restapiclient.LoginRoute, routes.LoginReq{Username: "alice", Password: "secret"})
		if err != nil {
			fmt.Printf("  [%s] login (alice) error: %v\n", cl.label, err)
			continue
		}
		fmt.Printf("  [%s] login (alice) → token=%q\n", cl.label, respAny.(routes.TokenResp).Token)
	}
	fmt.Println()
}
