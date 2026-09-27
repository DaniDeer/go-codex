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
// mechanism (docs/design/d-0006-protocol-native-capabilities.md): a sealed,
// compile-time-checked adapter-owned type (mqtt5.QoS/mqtt5.Retained)
// supplied at DECLARE time via SubscribeOptions.Capabilities/
// PublishOptions.Capabilities — NOT a new Attach-time parameter — plus:
//   - events.CapabilitySpec (declared on routes.CapabilityChannel) renders
//     as the AsyncAPI "x-capabilities" vendor extension (see
//     demo_spec_printing_asyncapi.go for spec output).
//   - events.CheckCapabilityCoverage confirms the declared spec and the
//     actually-supplied Capabilities agree.
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

	// Coverage check: the channel declares a CapabilitySpec for "QoS" (see
	// routes.CapabilityChannel) — the subscribe side's supplied
	// Capabilities (below) satisfies it. This SAME check runs
	// AUTOMATICALLY inside ServeSubscribers; called here too just to show
	// it explicitly.
	specHandle, err := routes.CapabilitySub.Handle(nil)
	if err != nil {
		fmt.Printf("  [error] Handle: %v\n", err)
		return
	}
	if err := events.CheckCapabilityCoverage(routes.CapabilityTopic,
		specHandle.CapabilitySpecs,
		[]any{mqtt5adapter.QoSAtLeastOnce},
	); err != nil {
		fmt.Printf("  [error] CheckCapabilityCoverage: %v\n", err)
		return
	}
	fmt.Println("  ✓ CheckCapabilityCoverage: declared CapabilitySpecs satisfied")

	// The Capability mechanism's QoS override only takes effect on the
	// Attach/ServeSubscribers path (it has no call-time qos parameter to
	// otherwise prefer) — unlike subscribeWithHandle/NewSubscribeTransport,
	// whose OWN call-time qos parameter always wins by design (see
	// SubscribeOptions.QoS's own doc comment).
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
	if err := mqtt5adapter.Attach(evClient, broker, router); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}
	go func() { _ = evClient.ServeSubscribers(ctx) }()
	router.WaitHandler(routes.CapabilityTopic)

	pubTransport := mqtt5adapter.NewPublishTransport[routes.SensorReading](broker, 0, false,
		mqtt5adapter.PublishOptions[routes.SensorReading]{
			Capabilities: []mqtt5adapter.Capability{mqtt5adapter.Retained(true)},
		},
	)
	if err := events.PublishHandle(ctx, routes.CapabilityPub, pubTransport,
		routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5},
	); err != nil {
		fmt.Printf("  [error] PublishHandle: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
