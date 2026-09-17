package main

import (
	"context"
	"fmt"
	"os"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/handlers"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/route"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file is the comprehensive reqreply.ErrorPattern/DeadLetter showcase
// — consolidates what was previously 2 separate demo files into one,
// covering EVERY facet of reqreply's declarative error-path mechanism:
//
//  1. demoErrorPatternDeclarationMechanisms — reqreply's 2 DECLARATION
//     mechanisms: ErrorPattern Direct mode, ErrorPattern Mapped mode.
//     (reqreply has NO ErrorAction — a matched pattern always replies,
//     since a caller is always owed a response.)
//  2. demoErrorPatternClientMatchMechanisms — the 3 CLIENT-side recovery
//     mechanisms: reqreply.ErrorPatternAs[B], reqreply.HandleErrorPattern+Case,
//     and the declaration value's own .Match method.
//  3. demoErrorPatternDeadLetterFallback — an UNDECLARED error type falls
//     through to a generic reply AND is additionally dead-lettered (the
//     caller ALWAYS gets a reply either way — DeadLetter is a parallel,
//     durable record, not an alternative reply mechanism, unlike pub/sub).
//  4. demoErrorPatternMiddlewareCombo — ErrorPattern matching a SECURITY
//     MIDDLEWARE Fn failure (auto-wrapped in reqreply.SecurityError), not
//     just a handler failure.
//  5. demoErrorPatternPortAdapter — proves the ErrorPattern mechanism
//     works transparently through ports.ToolPort + mqtt5.ServeAdapter,
//     not just mqtt5.AttachServer's direct dispatch.

// demoErrorPatternDeclarationMechanisms shows reqreply's 2 DECLARATION
// mechanisms side-by-side: ErrorPattern Direct mode (E itself IS the
// payload) and ErrorPattern Mapped mode (mapFn transforms E into a
// distinct B) — on its OWN scratch broker/server (routes.RateLimitComputeRoute
// is a Direct-mode demo route not registered on the shared mqtt5Built
// server).
func demoErrorPatternDeclarationMechanisms(ctx context.Context) {
	fmt.Println("\n── Demo: reqreply.ErrorPattern declaration mechanisms: Direct vs Mapped ──")

	broker, router := mqtt5server.NewMockBrokerRouter()
	server := reqreply.NewServer(reqreply.Info{Title: "Declaration mechanisms demo", Version: "1.0.0"})

	if _, err := routes.RateLimitComputeRoute.WithHandler(
		func(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
			if req.X < 0 {
				return routes.ComputeResp{}, routes.RateLimitError{RetryAfterSeconds: 30}
			}
			return routes.ComputeResp{Sum: req.X + req.Y}, nil
		},
	).Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering rate-limit route: %v\n", err)
		os.Exit(1)
	}
	if _, err := routes.ErrorPatternComputeRoute.WithHandler(handlers.AddOrConflict).Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering conflict route: %v\n", err)
		os.Exit(1)
	}
	if err := mqtt5adapter.AttachServer(server, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching server: %v\n", err)
		os.Exit(1)
	}
	go func() { _ = server.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	// 1) ErrorPattern Direct mode — RateLimitError itself IS the payload.
	_, err := client.Call(ctx, routes.RateLimitComputeRoute, routes.ComputeReq{X: -1, Y: 4})
	if payload, ok := reqreply.ErrorPatternAs[routes.RateLimitError](err); ok {
		fmt.Printf("  ✓ Direct mode: retry_after_seconds=%d (E itself IS B, no mapFn)\n", payload.RetryAfterSeconds)
	} else {
		fmt.Printf("  ✗ Direct mode: unexpected result: %v\n", err)
	}

	// 2) ErrorPattern Mapped mode — ConflictError mapped into ConflictPayload.
	_, err = client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: -1, Y: 4})
	if payload, ok := reqreply.ErrorPatternAs[routes.ConflictPayload](err); ok {
		fmt.Printf("  ✓ Mapped mode: code=%q reason=%q (mapFn transforms E into a distinct B)\n", payload.Code, payload.Reason)
	} else {
		fmt.Printf("  ✗ Mapped mode: unexpected result: %v\n", err)
	}
}

// demoErrorPatternClientMatchMechanisms shows the 3 CLIENT-side recovery
// mechanisms against the SAME matched error (routes.ConflictError →
// routes.ConflictPayload):
//  1. reqreply.ErrorPatternAs[B](err) — generic one-line helper.
//  2. reqreply.HandleErrorPattern(err, Case(...), Case(...)) — typed
//     switch-like dispatch, first-matching-Case-wins.
//  3. routes.ConflictErrorPattern.Match(err) — the DECLARATION VALUE's
//     own method (declared once, used both directions).
func demoErrorPatternClientMatchMechanisms(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo: reqreply client-side ErrorPattern matching: 3 mechanisms ──")

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, built.Broker, built.Router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("  → call with a non-negative X (happy path):")
	respAny, err := client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: 3, Y: 4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(3 + 4) = %d\n", respAny.(routes.ComputeResp).Sum)

	fmt.Println("  → call with a negative X (matched ErrorPattern):")
	_, err = client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: -1, Y: 4})

	// 1) ErrorPatternAs — generic one-liner.
	if payload, ok := reqreply.ErrorPatternAs[routes.ConflictPayload](err); ok {
		fmt.Printf("  ✓ (1) reqreply.ErrorPatternAs[B]: code=%q\n", payload.Code)
	} else {
		fmt.Println("  ✗ (1) reqreply.ErrorPatternAs[B]: no match")
	}

	// 2) HandleErrorPattern + Case — switch-like dispatch, first match wins.
	handled := reqreply.HandleErrorPattern(err,
		reqreply.Case(func(p routes.SecurityRejectedPayload) {
			fmt.Println("  ✗ (2) matched wrong Case (SecurityRejectedPayload)")
		}),
		reqreply.Case(func(p routes.ConflictPayload) {
			fmt.Printf("  ✓ (2) reqreply.HandleErrorPattern+Case: code=%q\n", p.Code)
		}),
	)
	if !handled {
		fmt.Println("  ✗ (2) reqreply.HandleErrorPattern: no Case matched")
	}

	// 3) The declaration value's OWN .Match method — "declare once, use
	// both directions": routes.ConflictErrorPattern is the SAME value
	// ErrorPatternComputeRoute declared server-side.
	if payload, ok := routes.ConflictErrorPattern.Match(err); ok {
		fmt.Printf("  ✓ (3) routes.ConflictErrorPattern.Match(err): code=%q (same value declares AND matches)\n", payload.Code)
	} else {
		fmt.Println("  ✗ (3) routes.ConflictErrorPattern.Match: no match")
	}
}

// demoErrorPatternDeadLetterFallback demonstrates reqreply.DeadLetter:
// routes.DeadLetterComputeRoute declares ONLY DeadLetter (no
// ErrorPattern) — an undeclared TimeoutError falls through to a GENERIC
// error reply (the caller ALWAYS gets a reply either way) AND is
// ADDITIONALLY published to the dead-letter topic for a durable ops
// record — DeadLetter is complementary, not an alternative to the reply.
func demoErrorPatternDeadLetterFallback(ctx context.Context) {
	fmt.Println("\n── Demo: reqreply.DeadLetter — undeclared error → generic reply + durable DLQ record ──")

	broker, router := mqtt5server.NewMockBrokerRouter()

	server := reqreply.NewServer(reqreply.Info{Title: "DeadLetter demo", Version: "1.0.0"})
	if _, err := routes.DeadLetterComputeRoute.WithHandler(
		func(_ context.Context, _ routes.ComputeReq) (routes.ComputeResp, error) {
			return routes.ComputeResp{}, routes.TimeoutError{}
		},
	).Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering route: %v\n", err)
		os.Exit(1)
	}

	var dlqPayload []byte
	dlqReceived := make(chan struct{}, 1)
	router.RegisterHandler("compute/add-deadletter-demo/dlq", func(msg *pahomqtt5.Publish) {
		dlqPayload = msg.Payload
		select {
		case dlqReceived <- struct{}{}:
		default:
		}
	})

	if err := mqtt5adapter.AttachServer(server, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching server: %v\n", err)
		os.Exit(1)
	}
	go func() { _ = server.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	_, err := client.Call(ctx, routes.DeadLetterComputeRoute, routes.ComputeReq{X: 1, Y: 2})
	if err == nil {
		fmt.Println("  ✗ expected an error reply, got none")
		return
	}
	fmt.Printf("  ✓ caller received a GENERIC error reply (no declared ErrorPattern to match): %v\n", err)

	select {
	case <-dlqReceived:
		fmt.Printf("  ✓ ADDITIONALLY dead-lettered — durable envelope published to the DLQ topic (%d bytes)\n", len(dlqPayload))
	case <-time.After(300 * time.Millisecond):
		fmt.Println("  ✗ timed out waiting for the dead-letter publish")
	}
}

// demoErrorPatternMiddlewareCombo proves reqreply.ErrorPattern intercepts
// a SECURITY-MIDDLEWARE Fn failure — routes.SecuredErrorPatternRoute's
// securityFn ALWAYS rejects, and the adapter auto-wraps its returned
// error in reqreply.SecurityError, which is matched by the route's
// declared ErrorPattern BEFORE the business handler ever runs.
func demoErrorPatternMiddlewareCombo(ctx context.Context) {
	fmt.Println("\n── Demo: reqreply.ErrorPattern + security middleware Fn combo ──")

	broker, router := mqtt5server.NewMockBrokerRouter()

	server := reqreply.NewServer(reqreply.Info{Title: "Security combo demo", Version: "1.0.0"})
	if _, err := routes.SecuredErrorPatternRoute.
		WithHandler(func(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
			fmt.Println("  ✗ handler ran (unexpected — security Fn should have rejected first)")
			return routes.ComputeResp{Sum: req.X + req.Y}, nil
		}).
		HandleMW(&routes.BearerAuthMw, handlers.AlwaysRejectSecurityImpl).
		Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering route: %v\n", err)
		os.Exit(1)
	}

	if err := mqtt5adapter.AttachServer(server, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching server: %v\n", err)
		os.Exit(1)
	}
	go func() { _ = server.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	// A well-formed-but-fake bearer token is required so the BUILT-IN
	// credential FORMAT check passes and the server's securityFn (which
	// ALWAYS rejects) is actually reached — mirrors REST's
	// demo_error_pattern.go's identical pattern.
	securedRoute := routes.SecuredErrorPatternRoute.ClientMW(&routes.BearerAuthMw,
		func(context.Context, []route.SecurityRequirement) ([]mqtt5adapter.UserProperty, error) {
			return []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer fake-token-for-demo"}}, nil
		},
	)
	_, err := client.Call(ctx, securedRoute, routes.ComputeReq{X: 1, Y: 2})
	if payload, ok := reqreply.ErrorPatternAs[routes.SecurityRejectedPayload](err); ok {
		fmt.Printf("  ✓ security-middleware Fn error matched by ErrorPattern (handler NEVER ran): code=%q\n", payload.Code)
	} else {
		fmt.Printf("  ✗ expected SecurityRejectedPayload, got: %v\n", err)
	}
}

// demoErrorPatternPortAdapter proves reqreply.ErrorPattern works
// transparently through the ports binding layer — not just
// mqtt5.AttachServer's direct dispatch. Binds the SAME
// routes.ErrorPatternComputeRoute + handlers.AddOrConflict via
// ports.NewToolPort + mqtt5.ServeAdapter (instead of AttachServer), on its
// OWN scratch broker/router — proving zero additional wiring is needed:
// mqtt5.ServeAdapter delegates straight to the already-fully-wired Serve,
// confirmed by this session's review (unlike REST's now-fixed handlerFunc
// gap — see docs/roadmap/error-handling-rest-events-reqreply.md's H1).
func demoErrorPatternPortAdapter(ctx context.Context) {
	fmt.Println("\n── Demo: reqreply.ErrorPattern via ports.ToolPort + mqtt5.ServeAdapter ──")

	broker, router := mqtt5server.NewMockBrokerRouter()

	server := reqreply.NewServer(reqreply.Info{Title: "Ports ErrorPattern demo", Version: "1.0.0"})
	handle, err := routes.ErrorPatternComputeRoute.WithHandler(handlers.AddOrConflict).Register(server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering route: %v\n", err)
		os.Exit(1)
	}

	toolPort, err := ports.NewToolPort[routes.ComputeReq, routes.ComputeResp](
		"compute-error-pattern", routes.ComputeReqCodec, routes.ComputeRespCodec, ports.PortOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error constructing tool port: %v\n", err)
		os.Exit(1)
	}
	// ServeAdapter.Bind invokes the PORT's own configured fn (set via
	// SetFunc below), not handle's WithHandler-attached one — Register
	// above is still needed to build a valid *reqreply.RouteHandle (spec
	// + declared ErrorPattern), but the actual business logic dispatched
	// on each request comes from SetFunc here.
	toolPort.SetFunc(handlers.AddOrConflict)
	if err := toolPort.Bind(ctx, mqtt5adapter.ServeAdapter(broker, router, handle, mqtt5adapter.ServeOptions{})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error binding ServeAdapter: %v\n", err)
		os.Exit(1)
	}
	// ServeAdapter.Bind starts Serve in a background goroutine — its
	// router-handler registration is asynchronous, same startup race
	// main.go's own mqtt5server.Build() call avoids with an identical
	// brief sleep before the first Client.Call.
	time.Sleep(50 * time.Millisecond)

	client := reqreply.NewClient()
	if err := mqtt5adapter.AttachClient(client, broker, router); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("  → call with a non-negative X (happy path, through ports):")
	respAny, err := client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: 5, Y: 6})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(5 + 6) = %d\n", respAny.(routes.ComputeResp).Sum)

	fmt.Println("  → call with a negative X (matched ErrorPattern, through ports):")
	_, err = client.Call(ctx, routes.ErrorPatternComputeRoute, routes.ComputeReq{X: -1, Y: 6})
	if payload, ok := reqreply.ErrorPatternAs[routes.ConflictPayload](err); ok {
		fmt.Printf("  ✓ rejected with typed ConflictPayload (same mechanism, bound via ports this time): code=%q reason=%q\n",
			payload.Code, payload.Reason)
	} else {
		fmt.Fprintf(os.Stderr, "expected ConflictPayload, got: %v\n", err)
		os.Exit(1)
	}
}
