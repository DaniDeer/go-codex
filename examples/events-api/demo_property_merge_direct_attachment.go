package main

import (
	"context"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// demoPropertyMergeDirectAttachment demonstrates the SIMPLE, direct
// (Middleware-free) attachment of a MergedPropertyParam to a channel —
// routes.PropertyMergeChannel declares events.NewPropertyParam("tenantID",
// ...) directly on NewChannel, exactly the way routes.MeasurementChannel
// declares events.NewTopicParam for its own {sensorID} topic var. No
// events.Middleware[In,Out]/Transform/ClientTransform wrapper is involved
// here — contrast with demo_user_property_middleware.go's User Property
// VALIDATION-only escape hatch (UserPropertyParam, ctx-based retrieval,
// no struct-field merge).
//
// Before this fix (see docs/roadmap history for the "property vocabulary
// axis symmetry bug"), a MergedPropertyParam attached directly to a
// channel silently registered spec metadata only and NEVER merged the
// property's value into the decoded struct — the ONLY way to get real
// merging was the heavier Middleware[In,Out] + WithSubscribeProperty/
// WithPublishProperty ceremony (see demo_security_subscribemw.go's
// sibling demos for that path). This demo proves the direct-attachment
// path now genuinely merges, end to end, over real mqtt5.
func demoPropertyMergeDirectAttachment(ctx context.Context) {
	fmt.Println("--- Demo: MergedPropertyParam direct channel attachment (no Middleware) ---")

	// This demo wires its OWN broker/router pair (mirrors
	// demo_user_property_middleware.go's own isolation rationale) —
	// routes.PropertyMergeChannel isn't part of mqtt5broker.Build's
	// shared routes set.
	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)

	subTransport := mqtt5adapter.NewSubscribeTransport[routes.TenantSensorReading](broker, router, 1,
		mqtt5adapter.SubscribeOptions{
			OnError: func(e mqtt5adapter.SubscribeError) {
				fmt.Printf("  [error] kind=%s: %v\n", e.Kind, e.Err)
			},
		},
	)
	if err := events.SubscribeHandle(ctx, routes.PropertyMergeSub, subTransport,
		func(_ context.Context, r routes.TenantSensorReading) error {
			fmt.Printf("  ✓ received: sensorId=%s value=%.1f tenantId=%s (merged directly, no Middleware)\n",
				r.SensorID, r.Value, r.TenantID)
			return nil
		},
	); err != nil {
		fmt.Printf("  [error] SubscribeHandle: %v\n", err)
		return
	}

	pubTransport := mqtt5adapter.NewPublishTransport[routes.TenantSensorReading](broker, 1, false,
		mqtt5adapter.PublishOptions[routes.TenantSensorReading]{},
	)
	// TenantID is set on the OUTGOING struct value — PropertyMergeChannel's
	// directly-attached MergedPropertyParam derives it into a real MQTT5
	// User Property here (no ClientTransform needed), and the subscribe
	// side above merges that SAME real User Property back into
	// TenantSensorReading.TenantID on receipt.
	if err := events.PublishHandle(ctx, routes.PropertyMergePub, pubTransport,
		routes.TenantSensorReading{
			SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			Value:    19.5,
			TenantID: "acme",
		},
	); err != nil {
		fmt.Printf("  [error] PublishHandle: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
