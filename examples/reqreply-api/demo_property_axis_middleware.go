package main

import (
	"context"
	"fmt"
	"os"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/mqtt5server"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/zeromqserver"
)

// demoPropertyAxisMiddleware exercises docs/roadmap/reqreply-codec-
// declared-middleware.md's NEW property vocabulary axis
// (WithRequestProperty/WithResponseProperty) — a SECOND, additive
// codec-declared middleware mechanism (D-0003 parity), distinct from
// Phase 1b's flat mqtt5adapter.FromUserPropertyParam bridge shown in
// Demo 6.
//
// The DECLARATION (routes.TenantPropertyMw) and IMPLEMENTATION
// (handlers.ProcessTenant) are BOTH adapter-agnostic — see
// routes/middleware.go and handlers/middleware.go. THIS demo shows the
// SAME declaration+implementation pair, attached via reqreply.Transform
// to routes.PropertyAxisComputeRoute, registered against TWO completely
// different transports (mqtt5server.Build, zeromqserver.Build) with ZERO
// changes to either the declaration or the implementation — only the
// adapter-specific WIRING in each {adapter}server package differs.
func demoPropertyAxisMiddleware(ctx context.Context, mqtt5Built *mqtt5server.Built, zeromqBuilt *zeromqserver.Built) {
	fmt.Println("\n── Demo 10: property vocabulary axis — same declaration, two transports ──")

	demoPropertyAxisMQTT5(ctx, mqtt5Built)
	demoPropertyAxisZeroMQ(ctx, zeromqBuilt)
}

// demoPropertyAxisMQTT5 calls PropertyAxisComputeRoute over mqtt5, WITH
// the "X-Tenant-Id" User Property supplied — mqtt5 has a real wire
// mechanism for properties, so the request-side value merges in AND the
// Middleware's produced response-side value reaches the ACTUAL outgoing
// reply's MQTT5 User Properties.
//
// This half specifically proves the design's OWN most significant
// finding across 19 review rounds ("Write-side wiring", Case 3): a
// server-side WithResponseProperty declaration must ACTUALLY reach the
// real outgoing reply — not just decode correctly in isolation.
// mqtt5server.Built.LastReplyUserProperties (a thin recording wrapper
// around the mock broker's Publish) makes that real wire content
// directly observable here, mirroring
// TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty's own
// assertion but as a visible, narrated demo instead of a unit test.
func demoPropertyAxisMQTT5(ctx context.Context, built *mqtt5server.Built) {
	resp, err := mqtt5adapter.Call(ctx, built.Broker, built.Router, built.PropertyAxisHandle,
		routes.ComputeReq{X: 5, Y: 7}, mqtt5adapter.CallOptions{
			UserProperties: []mqtt5adapter.UserProperty{{Key: "X-Tenant-Id", Value: "tenant-42"}},
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  mqtt5: ✓ compute(5 + 7) = %d (request X-Tenant-Id merged server-side)\n", resp.Sum)

	// Confirm the reply's ACTUAL wire-level User Properties carry the
	// Middleware-produced "X-Ack" value — this is the write-side
	// capability that did not exist anywhere in this codebase before
	// this design shipped (see "Write-side wiring" Case 3 in the
	// roadmap doc).
	var ackValue string
	for _, p := range built.LastReplyUserProperties() {
		if p.Key == "X-Ack" {
			ackValue = p.Value
		}
	}
	if ackValue != "processed-for-tenant-42" {
		fmt.Fprintf(os.Stderr, "expected reply User Property X-Ack=processed-for-tenant-42, got %q\n", ackValue)
		os.Exit(1)
	}
	fmt.Printf("  mqtt5: ✓ reply's ACTUAL wire User Property X-Ack = %q (Write-side wiring Case 3 confirmed)\n", ackValue)
}

// demoPropertyAxisZeroMQ calls the SAME PropertyAxisComputeRoute over
// zeromq — a transport with ZERO property mechanism (REQ/REP frames
// carry only [status, payload], no side channel of any kind). There is
// no way for a zeromq caller to supply "X-Tenant-Id" at all.
//
// Because routes.TenantPropertyMw declares that property OPTIONAL (not
// required — see routes/middleware.go's own reasoning), this call
// SUCCEEDS anyway: TenantIn.TenantID is simply left at its zero value,
// and handlers.ProcessTenant — the EXACT SAME implementation mqtt5 used
// above — runs unmodified, producing TenantAck{Ack: "processed-for-"}.
// This is the honest, correct behavior for a transport that structurally
// cannot carry the property: graceful degradation, not a wire mechanism
// zeromq doesn't have. (Had the property been declared REQUIRED instead,
// this exact call would fail with reqreply.MiddlewareInputError — see
// TestMiddleware_WithRequestProperty_RequiredButAdapterSuppliesNoPropertyMap
// for that scenario, unit-tested at the package level.)
func demoPropertyAxisZeroMQ(ctx context.Context, built *zeromqserver.Built) {
	resp, err := zeromq.Call(ctx, built.ClientSockets["compute/property-axis-add"], built.PropertyAxisHandle,
		routes.ComputeReq{X: 5, Y: 7}, zeromq.CallOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unexpected error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  zeromq: ✓ compute(5 + 7) = %d (SAME route+middleware+handler, no property mechanism on this transport — declared optional, so it just gracefully degrades)\n", resp.Sum)
}
