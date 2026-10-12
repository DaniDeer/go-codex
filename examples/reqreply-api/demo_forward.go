package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/handlers"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// demoForward demonstrates docs/roadmap/reqreply-forward-bounce.md's
// Forward/Bounce feature end to end: ForwardOriginatingRoute's own
// handler (handlers.ForwardToDouble) ALWAYS returns
// reqreply.Forward(routes.ForwardTargetRoute, ...) instead of a normal
// response — the SERVER signals "ask ForwardTargetRoute instead" within
// the SAME correlation-ID round trip, and mqtt5Client.Call's CENTRALIZED
// chain-following loop (api/reqreply.Client, not the adapter) resolves
// the target against its registry, republishes, and decodes
// ForwardTargetRoute's OWN typed response — all transparently to this
// demo's caller, which sees ONE ordinary Call return ForwardTargetRoute's
// response directly.
//
// ForwardTargetRoute must be REGISTERED on the client BEFORE the forward
// is followed — mqtt5Client.RegisterRoute "warms" the registry without a
// live call, exactly docs/roadmap/reqreply-client-registry.md's own
// documented use case for a route "only ever reached as a Forward
// target."
func demoForward(ctx context.Context, mqtt5Client *reqreply.Client) {
	fmt.Println("\n── Demo: Forward/Bounce — the server redirects, the client auto-follows ──")

	if err := mqtt5Client.RegisterRoute(routes.ForwardTargetRoute); err != nil {
		fmt.Fprintf(os.Stderr, "RegisterRoute(ForwardTargetRoute): %v\n", err)
		os.Exit(1)
	}

	respAny, err := mqtt5Client.Call(ctx, routes.ForwardOriginatingRoute, routes.ComputeReq{X: 1, Y: 2})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Call(ForwardOriginatingRoute) error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	// ForwardToDouble forwards {X:1*10, Y:2*10} = {X:10, Y:20} to
	// ForwardTargetRoute (handlers.Double: (X+Y)*2) → (10+20)*2 = 60.
	fmt.Printf("  ✓ Call(ForwardOriginatingRoute) auto-followed to ForwardTargetRoute, resolved: Sum=%d\n", resp.Sum)
	if resp.Sum != 60 {
		fmt.Fprintf(os.Stderr, "  unexpected Sum: want 60, got %d\n", resp.Sum)
		os.Exit(1)
	}
}

// forwardPropertyRecordingClient wraps the mock broker's Publish to keep
// the LAST message published BY THE CLIENT SIDE's own ClientTransport
// observable — mirrors examples/reqreply-api/mqtt5server's own unexported
// recordingBroker (used there for a REPLY's User Properties), just
// wired on the CLIENT side here instead of the server side, and scoped
// to THIS demo file rather than imported (examples are standalone
// `package main` programs, not shared importable packages). Wrapping
// only the Client's own outgoing Publish — not the shared router's
// subscription mechanism — avoids colliding with the SERVER's own
// subscription for the SAME topic (a single mock router handler per
// topic), which an earlier revision of this demo got wrong.
type forwardPropertyRecordingClient struct {
	mqtt5adapter.MQTTClient
	mu   sync.Mutex
	last *pahomqtt5.Publish
}

func (c *forwardPropertyRecordingClient) Publish(ctx context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	c.mu.Lock()
	c.last = p
	c.mu.Unlock()
	return c.MQTTClient.Publish(ctx, p)
}

func (c *forwardPropertyRecordingClient) lastUserProperty(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil || c.last.Properties == nil {
		return "", false
	}
	for _, p := range c.last.Properties.User {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

// demoForwardWithProperties demonstrates Phase 1.5's own wire-level
// properties addendum (docs/roadmap/reqreply-forward-bounce.md's
// "Addendum"): a handler can supply an explicit properties map to
// reqreply.Forward, and that map reaches the target as REAL wire-level
// MQTT5 User Properties — not just the request body. Self-contained (own
// scratch broker/server/client, mirrors demo_error_pattern.go's
// demoErrorPatternDeadLetterFallback shape), wrapping the CLIENT's own
// outgoing Publish (via forwardPropertyRecordingClient) so the actual
// republished MQTT5 message can be inspected directly — proving the
// claim against the REAL wire rather than just the decoded response.
func demoForwardWithProperties(ctx context.Context) {
	fmt.Println("\n── Demo: Forward's properties — a wire-level credential/header survives the republish ──")

	broker, router := mqtt5server.NewMockBrokerRouter()

	server := reqreply.NewServer(reqreply.Info{Title: "Forward properties demo", Version: "1.0.0"})
	sourceRoute := routes.ForwardWithPropertiesSourceRoute.WithHandler(
		func(_ context.Context, req routes.ComputeReq) (routes.ComputeResp, error) {
			return routes.ComputeResp{}, reqreply.Forward(routes.ForwardTargetRoute, req, nil,
				map[string]string{"X-Replay-Reason": "demo"})
		},
	)
	targetRoute := routes.ForwardTargetRoute.WithHandler(handlers.Double)
	if _, err := sourceRoute.Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering source route: %v\n", err)
		os.Exit(1)
	}
	if _, err := targetRoute.Register(server); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error registering target route: %v\n", err)
		os.Exit(1)
	}

	if err := server.Attach(mqtt5adapter.NewServerTransport(mqtt5adapter.ServerTransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching server: %v\n", err)
		os.Exit(1)
	}
	go func() { _ = server.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	recordingClient := &forwardPropertyRecordingClient{MQTTClient: broker}
	client := reqreply.NewClient()
	if err := client.Attach(mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{Client: recordingClient, Router: router})); err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error attaching client: %v\n", err)
		os.Exit(1)
	}
	if err := client.RegisterRoute(routes.ForwardTargetRoute); err != nil {
		fmt.Fprintf(os.Stderr, "RegisterRoute(ForwardTargetRoute): %v\n", err)
		os.Exit(1)
	}

	respAny, err := client.Call(ctx, routes.ForwardWithPropertiesSourceRoute, routes.ComputeReq{X: 3, Y: 4})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Call(ForwardWithPropertiesSourceRoute) error: %v\n", err)
		os.Exit(1)
	}
	resp := respAny.(routes.ComputeResp)
	// ForwardWithPropertiesSourceRoute forwards {X:3,Y:4} to
	// ForwardTargetRoute (handlers.Double: (X+Y)*2) → (3+4)*2 = 14.
	fmt.Printf("  ✓ Call auto-followed to ForwardTargetRoute, resolved: Sum=%d\n", resp.Sum)

	gotProperty, ok := recordingClient.lastUserProperty("X-Replay-Reason")
	if !ok {
		fmt.Println("  ✗ the republished request carries NO X-Replay-Reason User Property")
		os.Exit(1)
	}
	fmt.Printf("  ✓ the REPUBLISHED request carries the real MQTT5 User Property X-Replay-Reason=%q\n", gotProperty)
	if gotProperty != "demo" {
		fmt.Fprintf(os.Stderr, "  unexpected property value: want %q, got %q\n", "demo", gotProperty)
		os.Exit(1)
	}
}
