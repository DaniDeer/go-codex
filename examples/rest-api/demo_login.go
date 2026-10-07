package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/rest-api/auth"
	restapiclient "github.com/DaniDeer/go-codex/examples/rest-api/client"
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
		respAny, err := cl.c.Call(context.Background(), restapiclient.LoginRoute, auth.LoginReq{Username: "alice", Password: "secret"})
		if err != nil {
			fmt.Printf("  [%s] login (alice) error: %v\n", cl.label, err)
			continue
		}
		fmt.Printf("  [%s] login (alice) → token=%q\n", cl.label, respAny.(auth.TokenResp).Token)
	}
	fmt.Println()

	// ── invalid credentials — proves routes.LoginRoute's declared
	// rest.ErrorPattern (replacing a FORMER hand-rolled errors.As dispatch
	// inside chiserver/nethttpserver's shared adapter ErrorHandler) is
	// consulted, and that the client recovers the typed payload via
	// rest.ErrorPatternAs — this negative path had ZERO demo coverage
	// before this fix.
	fmt.Println("=== POST /login (invalid credentials — rest.ErrorPattern) ===")
	for _, cl := range clients {
		_, err := cl.c.Call(context.Background(), restapiclient.LoginRoute, auth.LoginReq{Username: "alice", Password: "wrong"})
		if err == nil {
			fmt.Printf("  [%s] ✗ expected an error, got none\n", cl.label)
			continue
		}
		payload, ok := rest.ErrorPatternAs[auth.LoginErrorPayload](err)
		if !ok {
			fmt.Printf("  [%s] ✗ expected auth.LoginErrorPayload, got: %v\n", cl.label, err)
			continue
		}
		fmt.Printf("  [%s] ✓ recovered typed 401 payload client-side: message=%q\n", cl.label, payload.Message)
	}
	fmt.Println()
}
