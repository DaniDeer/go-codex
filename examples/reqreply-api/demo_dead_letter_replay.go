package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file demonstrates docs/design/d-0004-reqreply-workflow-simplification.md's
// Phase 2.1 end to end: a dead-lettered message — captured by the SAME
// reqreply.DeadLetter mechanism demo_error_pattern.go's
// demoErrorPatternDeadLetterFallback already shows — can be RECOVERED via
// Client.ReplayDeadLetter, an ops/replay workflow that resolves the
// dead-lettered envelope's SourceTopic back against the registry and
// reissues the ORIGINAL request bytes verbatim. Also shows Phase 2.0's
// own wire-level properties addendum: a header-delivered credential
// present on the ORIGINAL failed request survives capture into the
// envelope AND republish on replay — not just the body.
//
// Decodes the real published DLQ payload via [reqreply.
// DecodeDeadLetterEnvelope] — the sanctioned decode path (`deep-dive-
// bug-hunt` Round 206), guaranteed to stay in sync with the encode side
// [reqreply.RouteHandle.DeadLetterFor] uses.

// demoDeadLetterReplay demonstrates the full recovery workflow: the
// route's handler fails on its FIRST invocation (an unmatched business
// error, dead-lettering the request and capturing the real
// "Authorization" User Property — attached to EVERY call made through
// this demo's client transport, mirroring demo_user_property_param_
// middleware.go's own mqtt5adapter.CallOptions.UserProperties mechanism
// — into the published envelope's Properties field) and succeeds on its
// SECOND — Client.ReplayDeadLetter republishes the dead-lettered
// envelope, the now-succeeding handler processes it, and the caller gets
// back a REAL, typed response, with the credential intact.
func demoDeadLetterReplay(ctx context.Context) {
	fmt.Println("\n── Demo: Client.ReplayDeadLetter — recovering a dead-lettered message, credential intact ──")

	broker, router := mqtt5server.NewMockBrokerRouter()

	var attempt int
	server := reqreply.NewServer(reqreply.Info{Title: "Dead-Letter Replay demo", Version: "1.0.0"})
	replayRoute := routes.DeadLetterReplayComputeRoute.WithHandler(
		func(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
			attempt++
			if attempt == 1 {
				return routes.ComputeResp{}, routes.TimeoutError{}
			}
			return routes.ComputeResp{Sum: req.X + req.Y}, nil
		},
	)
	computeRouter := reqreply.NewRouter("compute").
		Tags("dead-letter-replay").
		Route(replayRoute)
	if err := computeRouter.Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering route: %v\n", err)
		os.Exit(1)
	}

	var dlqPayload []byte
	dlqReceived := make(chan struct{}, 1)
	router.RegisterHandler("compute/add-deadletter-replay-demo/dlq", func(msg *pahomqtt5.Publish) {
		dlqPayload = msg.Payload
		select {
		case dlqReceived <- struct{}{}:
		default:
		}
	})

	if err := server.Attach(mqtt5adapter.NewServerTransport(mqtt5adapter.ServerTransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching server: %v\n", err)
		os.Exit(1)
	}
	go func() { _ = server.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Every call through this client transport attaches a real
	// "Authorization" User Property — the credential this demo proves
	// survives dead-letter capture AND replay.
	client := reqreply.NewClient()
	if err := client.Attach(mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{
		Client: broker, Router: router,
		Call: mqtt5adapter.CallOptions{
			UserProperties: []mqtt5adapter.UserProperty{{Key: "Authorization", Value: "Bearer demo-credential"}},
		},
	})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}

	replayHandle := routes.DeadLetterReplayComputeRoute.ClientHandle(reqreply.WithRouter(reqreply.NewRouter("compute")))
	_, err := client.Call(ctx, replayHandle, routes.ComputeReq{X: 3, Y: 4})
	if err == nil {
		fmt.Println("  ✗ expected the FIRST attempt to fail, got none")
		os.Exit(1)
	}
	fmt.Printf("  ✓ FIRST attempt failed (business error, caller still got a reply): %v\n", err)

	select {
	case <-dlqReceived:
		fmt.Printf("  ✓ ADDITIONALLY dead-lettered — durable envelope published to the DLQ topic (%d bytes)\n", len(dlqPayload))
	case <-time.After(300 * time.Millisecond):
		fmt.Println("  ✗ timed out waiting for the dead-letter publish")
		os.Exit(1)
	}

	envelope, decErr := reqreply.DecodeDeadLetterEnvelope(dlqPayload)
	if decErr != nil {
		fmt.Fprintf(os.Stderr, "DecodeDeadLetterEnvelope: %v\n", decErr)
		os.Exit(1)
	}
	fmt.Printf("  ✓ decoded the DLQ envelope off the wire: SourceTopic=%q Error=%q Properties=%v\n",
		envelope.SourceTopic, envelope.Error, envelope.Properties)
	if envelope.Properties["Authorization"] != "Bearer demo-credential" {
		fmt.Fprintf(os.Stderr, "  unexpected envelope Properties: want Authorization=Bearer demo-credential, got %v\n", envelope.Properties)
		os.Exit(1)
	}

	if err := client.RegisterRoute(routes.DeadLetterReplayComputeRoute); err != nil {
		fmt.Fprintf(os.Stderr, "RegisterRoute(DeadLetterReplayComputeRoute): %v\n", err)
		os.Exit(1)
	}

	respAny, err := client.ReplayDeadLetter(ctx, envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ReplayDeadLetter: want the SECOND (now-succeeding) attempt to resolve, got error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	fmt.Printf("  ✓ ReplayDeadLetter recovered the message: Sum=%d (handler's SECOND invocation succeeded)\n", resp.Sum)
	if resp.Sum != 7 {
		fmt.Fprintf(os.Stderr, "  unexpected Sum: want 7, got %d\n", resp.Sum)
		os.Exit(1)
	}

	// A SECOND, smaller scenario: replaying an envelope whose SourceTopic
	// was never registered on this Client surfaces a distinct, typed
	// error — UnrecognizedDeadLetterSourceError — rather than silently
	// failing or being confused with Forward's own UnrecognizedForwardError.
	unknownEnvelope := reqreply.DeadLetterEnvelope{
		SourceTopic: "compute/never-registered-anywhere",
		Payload:     []byte(`{}`),
	}
	_, unrecognizedErr := client.ReplayDeadLetter(ctx, unknownEnvelope)
	var unrecognized reqreply.UnrecognizedDeadLetterSourceError
	if !errors.As(unrecognizedErr, &unrecognized) {
		fmt.Fprintf(os.Stderr, "  expected UnrecognizedDeadLetterSourceError, got %v (%T)\n", unrecognizedErr, unrecognizedErr)
		os.Exit(1)
	}
	fmt.Printf("  ✓ replaying an envelope with an unregistered SourceTopic fails fast: %v\n", unrecognizedErr)
}
