package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// validBearerCredFn/malformedBearerCredFn are routes.BearerAuthMw's
// reusable-class WithSend Fns (client side) — REPLACE the OLD
// mqtt5adapter.CallOptions.CredentialFunc entirely (Phase 1 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum,
// BREAKING removal) and the OLD legacy-shaped paired ClientMW Fn
// permanently closed by docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Phase
// C. Two distinct Fns (rather than one parameterized function) mirror
// examples/rest-api/client/client.go's AliceCredFn/AdminCredFn pattern —
// each demonstrates a DIFFERENT credential outcome when attached via
// routes.BearerAuthMw.WithSend(...), itself attached via .Use(...).
func validBearerCredFn(context.Context) (routes.BearerAuthIn, error) {
	return routes.BearerAuthIn{Token: "Bearer ******"}, nil
}

func malformedBearerCredFn(context.Context) (routes.BearerAuthIn, error) {
	return routes.BearerAuthIn{Token: "Bearer "}, nil
}

// demoRouteLevelSecurityCredentialError uses routes.SecuredComputeRoute,
// which declares its OWN Security via .Use(routes.BearerAuthMw) (not
// relying on GlobalSecurity), called with a deliberately malformed/
// missing credential. Each credential Fn is attached to its OWN Route
// variant (via .Use(routes.BearerAuthMw.WithSend(...))) — [Route] and
// [reqreply.Middleware] are both immutable, so chaining twice with
// different Fns produces two independent Go values sharing the same
// topic, exactly mirroring examples/rest-api's AliceCredFn/AdminCredFn
// dual-variant pattern.
//
// NOTE on a confirmed behavioral change from the OLD legacy
// clientImpls-paired mechanism (permanently closed by
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Phase C): the OLD mechanism's
// credential-format pre-check ran CLIENT-SIDE (via
// mergeCredentialUserProperties/validateSecurityCredentials,
// request never published) — a capability that was NEVER available to
// Middleware-dispatched (clientMiddlewareHandlers) attachments, bound or
// agnostic, even before this redesign. Migrating routes.BearerAuthMw's
// client-side credential supply onto the declarative .WithSend()
// mechanism therefore means a malformed credential is now caught
// SERVER-SIDE instead (the SAME server-side format check
// SecuredComputeRoute's own security scheme always enforced), surfacing
// to the caller as a generic reqreply.CallError wrapping the server's
// error reply text — NOT a client-side-typed reqreply.
// SecurityCredentialError anymore, since Go error TYPES are not
// preserved across the wire without a declared reqreply.ErrorPattern
// (see demo_error_pattern.go's own ErrorPattern + security combo demo
// for how to recover a TYPED error from a security rejection reply).
func demoRouteLevelSecurityCredentialError(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo 3: route-level security — SecurityCredentialError ──")

	client := reqreply.NewClient()
	if err := client.Attach(mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{Client: built.Broker, Router: built.Router})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n  → call with a valid bearer token:")
	validRoute := routes.SecuredComputeRoute.Use(routes.BearerAuthMw.WithSend(validBearerCredFn))
	respAny, err := client.Call(ctx, validRoute, routes.ComputeReq{X: 7, Y: 8})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(7 + 8) = %d (credential accepted)\n", resp.Sum)

	fmt.Println("\n  → call with a malformed (empty) bearer token:")
	malformedRoute := routes.SecuredComputeRoute.Use(routes.BearerAuthMw.WithSend(malformedBearerCredFn))
	_, err = client.Call(ctx, malformedRoute, routes.ComputeReq{X: 1, Y: 2})
	var callErr mqtt5adapter.CallError
	if errors.As(err, &callErr) && strings.Contains(callErr.Error(), "invalid credential") {
		fmt.Printf("  ✓ rejected server-side (credential format invalid): %v\n", callErr)
	} else {
		fmt.Fprintf(os.Stderr, "expected a server-side credential-format rejection, got: %v\n", err)
		os.Exit(1)
	}
}
