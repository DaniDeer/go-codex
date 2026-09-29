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

	// Client.Attach + Client.Subscribe/Publish — property-merge, OnError,
	// and Capabilities are ALL supported through the api-layer-owned
	// Client since docs/roadmap/capability-requirement-composition.md's
	// Phase 4e; no adapter-specific NewSubscribeTransport/
	// NewPublishTransport escape hatch needed here anymore.
	evClient := events.NewClient(events.WithInfo(events.Info{Title: "Property merge demo", Version: "1.0.0"}))
	if err := evClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}

	sub := routes.PropertyMergeSub.WithOptions(mqtt5adapter.SubscribeOptions{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
		OnError: func(e mqtt5adapter.SubscribeError) {
			fmt.Printf("  [error] kind=%s: %v\n", e.Kind, e.Err)
		},
	})
	go func() {
		_ = evClient.Subscribe(ctx, sub, func(_ context.Context, r routes.TenantSensorReading) error {
			fmt.Printf("  ✓ received: sensorId=%s value=%.1f tenantId=%s (merged directly, no Middleware)\n",
				r.SensorID, r.Value, r.TenantID)
			return nil
		})
	}()
	router.WaitHandler(routes.PropertyMergeTopic)

	// TenantID is set on the OUTGOING struct value — PropertyMergeChannel's
	// directly-attached MergedPropertyParam derives it into a real MQTT5
	// User Property here (no ClientTransform needed), and the subscribe
	// side above merges that SAME real User Property back into
	// TenantSensorReading.TenantID on receipt.
	pub := routes.PropertyMergePub.WithOptions(mqtt5adapter.PublishOptions[routes.TenantSensorReading]{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
	})
	if err := evClient.Publish(ctx, pub,
		routes.TenantSensorReading{
			SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			Value:    19.5,
			TenantID: "acme",
		},
	); err != nil {
		fmt.Printf("  [error] Publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
