package main

import (
	"context"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoCapabilityMechanism demonstrates Phase 2 of docs/design/
// d-0006-protocol-native-capabilities.md: the SAME protocol-native
// Capability mechanism examples/events-api's demo_capability_mechanism.go
// exercises for pub/sub, now applied to reqreply — reusing the SAME 2
// sealed adapter-owned types (mqtt5.QoS/mqtt5.Retained) with ZERO new
// adapter-side capability values needed.
//
//   - reqreply.RequireQoS (sugar over reqreply.CapabilityRequirement,
//     declared on routes.CapabilityRoute) renders as the AsyncAPI
//     "x-capabilities" vendor extension on the route's REQUEST channel.
//   - reqreply.CheckCapabilityCoverage confirms the declared requirement
//     and the actually-supplied Capabilities agree — value-aware via
//     mqtt5.QoS's Level() method (reqreply.LeveledCapability): a supplied
//     QoS BELOW the declared minimum is caught as Insufficient, not just
//     name-matched.
//   - mqtt5server.Build already supplies mqtt5.QoSAtLeastOnce at
//     NewServerTransport time (see its own Build() doc comment) — this demo's
//     Call additionally supplies one at CALL time, applied to the
//     OUTGOING REQUEST publish (client/Call side), closing Phase 2's
//     server+client plumbing gap end to end.
func demoCapabilityMechanism(ctx context.Context, built *mqtt5server.Built) {
	fmt.Println("\n── Demo: protocol-native Capability mechanism (mqtt5.QoS), Phase 2 ──")

	// Coverage check: CapabilityRoute declares a requirement for "QoS"
	// (see routes.CapabilityRoute's reqreply.RequireQoS) — the supplied
	// Capabilities below satisfy it. This SAME check runs AUTOMATICALLY
	// inside NewServerTransport/NewClientTransport; called here too just to show it
	// explicitly.
	reqHandle := routes.CapabilityRoute.ClientHandle()
	if err := reqreply.CheckCapabilityCoverage(reqHandle.Topic,
		reqHandle.Requirements,
		[]any{mqtt5adapter.QoSAtLeastOnce},
	); err != nil {
		fmt.Fprintf(os.Stderr, "CheckCapabilityCoverage: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  ✓ CheckCapabilityCoverage: declared Requirements satisfied")

	// Value-aware coverage checking: the route requires AT LEAST QoS
	// AtLeastOnce, but a hypothetical adapter only supplies QoSAtMostOnce
	// (a LOWER level) — caught via mqtt5.QoS's Level() method
	// (reqreply.LeveledCapability), not just a name match.
	if err := reqreply.CheckCapabilityCoverage(reqHandle.Topic,
		reqHandle.Requirements,
		[]any{mqtt5adapter.QoSAtMostOnce},
	); err != nil {
		fmt.Printf("  ✓ CheckCapabilityCoverage correctly rejects an insufficient QoS level: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "expected an Insufficient-level error, got nil")
		os.Exit(1)
	}

	// The CALL side additionally supplies mqtt5.Retained(true), applied
	// to the OUTGOING REQUEST publish — Phase 2's client-side fix (this
	// field previously did not exist on CallOptions at all).
	transport := mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{
		Client: built.Broker, Router: built.Router,
		Call: mqtt5adapter.CallOptions{
			Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce, mqtt5adapter.Retained(true)},
		},
	})
	resp, err := reqreply.CallWithTransport(ctx, transport, built.CapabilityHandle, routes.ComputeReq{X: 5, Y: 6})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Call: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ compute(5 + 6) = %d (request published with QoS=1, Retained=true; server's reply honors its own supplied QoS)\n", resp.Sum)
}
