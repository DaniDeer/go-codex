package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/auth"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// validBearerCredFn/malformedBearerCredFn are auth.BearerAuthMw's
// reusable-class WithSend Fns (client side) — REPLACE the OLD
// mqtt5adapter.CallOptions.CredentialFunc entirely (Phase 1 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum,
// BREAKING removal) and the OLD legacy-shaped paired ClientMW Fn
// permanently closed by docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Phase
// C. Two distinct Fns (rather than one parameterized function) mirror
// examples/rest-api/client/client.go's AliceCredFn/AdminCredFn pattern —
// each demonstrates a DIFFERENT credential outcome when attached via
// auth.BearerAuthMw.WithSend(...), itself attached via .Use(...).
func validBearerCredFn(context.Context) (auth.BearerAuthIn, error) {
	return auth.BearerAuthIn{Token: "Bearer ******"}, nil
}

func malformedBearerCredFn(context.Context) (auth.BearerAuthIn, error) {
	return auth.BearerAuthIn{Token: "Bearer "}, nil
}

// demoRouteLevelSecurityCredentialError uses routes.SecuredComputeRoute,
// which declares its OWN Security via .Use(auth.BearerAuthMw) (not
// relying on GlobalSecurity), called with a deliberately malformed/
// missing credential. Each credential Fn is attached to its OWN Route
// variant (via .Use(auth.BearerAuthMw.WithSend(...))) — [Route] and
// [reqreply.Middleware] are both immutable, so chaining twice with
// different Fns produces two independent Go values sharing the same
// topic, exactly mirroring examples/rest-api's AliceCredFn/AdminCredFn
// dual-variant pattern.
//
// NOTE on the current, correct behavior (docs/design/
// d-0001-rest-middleware-workflow-simplification.md's Addendum 8 mqtt5
// adapter fix closed a genuine gap here): a malformed credential produced by a Security-
// carrying middleware/sending attachment (bound OR agnostic — any
// attachment whose own Satisfies is non-empty) is now caught CLIENT-SIDE,
// via the SAME validateSecurityCredentials codec-format check the
// server independently also enforces — the request is never even
// published. This surfaces to the caller as a client-side-typed
// reqreply.SecurityCredentialError directly (not wrapped in
// mqtt5adapter.CallError, which only wraps SERVER reply errors — the
// client-side check short-circuits before a reply round-trip ever
// happens). Before this round's fix, `auth.BearerAuthMw`'s "agnostic"
// (`.Use()` + `.WithSend()`) attachment style was NOT recognized by the
// client-side "did a Security-carrying attachment actually run"
// signal — only the legacy clientImpls path was — so a malformed
// credential supplied this way silently reached the server instead,
// masking the SAME bug Round 163/164 already found and fixed for
// `api/rest`'s client dispatch.
func demoRouteLevelSecurityCredentialError(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo 3: route-level security — SecurityCredentialError ──")

	client := reqreply.NewClient()
	if err := client.Attach(mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{Client: built.Broker, Router: built.Router})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	// SecuredComputeRoute is Mounted under mqtt5server.Build's shared
	// "compute" Router (docs/design/d-0008-declarative-router-groups.md)
	// — compose its RELATIVE topic ("secured-add") back to
	// "compute/secured-add" via ClientHandle(WithRouter(...)).
	computeRouter := reqreply.NewRouter("compute")

	fmt.Println("\n  → call with a valid bearer token:")
	validHandle := routes.SecuredComputeRoute.Use(auth.BearerAuthMw.WithSend(validBearerCredFn)).ClientHandle(reqreply.WithRouter(computeRouter))
	respAny, err := client.Call(ctx, validHandle, routes.ComputeReq{X: 7, Y: 8})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ compute(7 + 8) = %d (credential accepted)\n", resp.Sum)

	fmt.Println("\n  → call with a malformed (empty) bearer token:")
	malformedHandle := routes.SecuredComputeRoute.Use(auth.BearerAuthMw.WithSend(malformedBearerCredFn)).ClientHandle(reqreply.WithRouter(computeRouter))
	_, err = client.Call(ctx, malformedHandle, routes.ComputeReq{X: 1, Y: 2})
	var credErr reqreply.SecurityCredentialError
	if errors.As(err, &credErr) && strings.Contains(credErr.Error(), "invalid credential") {
		fmt.Printf("  ✓ rejected client-side (credential format invalid, never published): %v\n", credErr)
	} else {
		fmt.Fprintf(os.Stderr, "expected a client-side credential-format rejection, got: %v\n", err)
		os.Exit(1)
	}
}
