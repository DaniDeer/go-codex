package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoUserPropertyParamMiddleware exercises Phase 1b of docs/roadmap/
// reqreply-middleware.md — the User-Property param-as-middleware
// mechanism. Unlike security schemes (Demo 2/3), RequestHeaderParams
// need no HandleMW pairing: mqtt5adapter.AttachServer validates the
// declared "X-API-Key" User Property automatically, BEFORE the handler
// ever runs.
//
// Uses mqtt5adapter.Call directly (the thin escape-hatch wrapper kept by
// Phase 0b — zero duplicate logic, delegates straight to the SAME
// AttachServer/AttachClient dispatch this demo already exercises
// elsewhere) rather than reqreply.Client, since attaching a raw,
// non-security User Property on a single call is exactly what
// [mqtt5adapter.CallOptions.UserProperties] is for — there is no
// client-side auto-supply mechanism for a plain (non-security)
// User-Property param, only server-side validation.
func demoUserPropertyParamMiddleware(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo 6: Phase 1b — User-Property param-as-middleware ──")

	fmt.Println("\n  → call WITHOUT the required X-API-Key User Property:")
	_, err := mqtt5adapter.Call(ctx, built.Broker, built.Router, built.HeaderParamHandle,
		routes.ComputeReq{X: 3, Y: 4}, mqtt5adapter.CallOptions{})
	var callErr mqtt5adapter.CallError
	if errors.As(err, &callErr) && callErr.Kind == mqtt5adapter.KindHandler && strings.Contains(callErr.Error(), "X-API-Key") {
		fmt.Printf("  ✓ rejected server-side: %v\n", callErr)
	} else {
		fmt.Fprintf(os.Stderr, "expected a KindHandler CallError mentioning X-API-Key, got: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n  → call WITH the required X-API-Key User Property:")
	resp, err := mqtt5adapter.Call(ctx, built.Broker, built.Router, built.HeaderParamHandle,
		routes.ComputeReq{X: 3, Y: 4}, mqtt5adapter.CallOptions{
			UserProperties: []mqtt5adapter.UserProperty{{Key: "X-API-Key", Value: "demo-secret"}},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(3 + 4) = %d (X-API-Key accepted)\n", resp.Sum)
}
