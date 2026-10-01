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

// demoCapabilityMechanism demonstrates the protocol-native Capability
// mechanism, REWRITTEN by Phase 1 of
// docs/design/d-0006-protocol-native-capabilities.md into the doc's
// three-tier vocabulary (Baseline/Implicit/Explicit — this is the
// Explicit tier): a sealed, compile-time-checked adapter-owned type
// (mqtt5.QoS/mqtt5.Retained) supplied at DECLARE time via
// SubscribeOptions.Capabilities/PublishOptions.Capabilities — NOT a new
// Attach-time parameter — plus:
//   - events.RequireQoS (sugar over events.CapabilityRequirement, declared
//     on routes.CapabilityChannel) renders as the AsyncAPI "x-capabilities"
//     vendor extension (see demo_spec_printing_asyncapi.go for spec
//     output).
//   - events.CheckCapabilityCoverage confirms the declared requirement and
//     the actually-supplied Capabilities agree — now GENUINELY value-aware
//     via the new events.LeveledCapability interface (mqtt5.QoS.Level()):
//     a supplied QoS BELOW the declared minimum is caught as Insufficient,
//     not just name-matched (see the second demonstration below).
//   - stats.CapabilityObserver.RecordCapabilityApplied reports each
//     exercised capability through the SAME shared Observer this example's
//     other demos already use (see observability.DemoObserver).
//
// A zeromq.Capability (e.g. zeromq.HWM) cannot be supplied to
// mqtt5.SubscribeOptions.Capabilities — the Go compiler rejects the
// mismatch at build time, with no custom error type needed.
func demoCapabilityMechanism(ctx context.Context) {
	fmt.Println("--- Demo: protocol-native Capability mechanism (mqtt5.QoS/mqtt5.Retained) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)

	// Coverage check: the channel declares a requirement for "QoS" (see
	// routes.CapabilityChannel's events.RequireQoS) — the subscribe side's
	// supplied Capabilities (below) satisfies it. This SAME check runs
	// AUTOMATICALLY inside ServeSubscribers; called here too just to show
	// it explicitly.
	reqHandle, err := routes.CapabilitySub.Handle(nil)
	if err != nil {
		fmt.Printf("  [error] Handle: %v\n", err)
		return
	}
	if err := events.CheckCapabilityCoverage(routes.CapabilityTopic,
		reqHandle.Requirements,
		[]any{mqtt5adapter.QoSAtLeastOnce},
	); err != nil {
		fmt.Printf("  [error] CheckCapabilityCoverage: %v\n", err)
		return
	}
	fmt.Println("  ✓ CheckCapabilityCoverage: declared Requirements satisfied")

	// NEW this round — value-aware coverage checking: the channel
	// requires AT LEAST QoS AtLeastOnce, but a hypothetical adapter only
	// supplies QoSAtMostOnce (a LOWER level). Before this rewrite, this
	// mismatch was NOT caught (only the NAME "QoS" was checked). Now it
	// is, via mqtt5.QoS's new Level() method (events.LeveledCapability).
	if err := events.CheckCapabilityCoverage(routes.CapabilityTopic,
		reqHandle.Requirements,
		[]any{mqtt5adapter.QoSAtMostOnce},
	); err != nil {
		fmt.Printf("  ✓ CheckCapabilityCoverage correctly rejects an insufficient QoS level: %v\n", err)
	} else {
		fmt.Println("  [error] expected an Insufficient-level error, got nil")
	}

	// Capabilities is the SOLE mechanism (docs/roadmap/
	// d-0006-protocol-native-capabilities.md's Phase 4/4b) — there is no
	// call-time qos parameter to otherwise prefer, on either the
	// subscribe or publish side.
	evClient := events.NewClient(events.WithInfo(events.Info{Title: "Capability demo", Version: "1.0.0"}))
	sub := routes.CapabilitySub.WithHandler(func(ctx context.Context, r routes.SensorReading) error {
		fmt.Printf("  ✓ received: sensorId=%s value=%.1f\n", r.SensorID, r.Value)
		return nil
	}).WithOptions(mqtt5adapter.SubscribeOptions{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
	})
	if err := sub.Register(evClient); err != nil {
		fmt.Printf("  [error] Register: %v\n", err)
		return
	}
	if err := evClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}
	go func() { _ = evClient.ServeSubscribers(ctx) }()
	router.WaitHandler(routes.CapabilityTopic)

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4c:
	// evClient is ALREADY attached (above, for the subscribe side) —
	// Publisher.WithOptions declares Capabilities the SAME way
	// Subscriber.WithOptions does, so evClient.Publish itself now
	// resolves and applies them. No adapter-specific
	// NewPublishTransport/events.PublishHandle escape hatch needed here
	// anymore — the api-layer-owned Client is the ONLY thing this demo
	// touches for both directions.
	pub := routes.CapabilityPub.WithOptions(mqtt5adapter.PublishOptions[routes.SensorReading]{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.Retained(true)},
	})
	if err := evClient.Publish(ctx, pub,
		routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5},
	); err != nil {
		fmt.Printf("  [error] Publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
