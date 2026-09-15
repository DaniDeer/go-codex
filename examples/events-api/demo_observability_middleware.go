package main

import (
	"context"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/client"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/observability"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// demoObservabilityMiddleware demonstrates post-Phase-1
// events.Observability[T]: attached via .SubscribeMW(nil,
// events.Observability[T](obs))/.PublishMW(nil, ...), it wires
// observability.DemoObserver — the same Observer instance used
// throughout this example (see main.go) — into every subsequent
// SubscribeMW/PublishMW-attached implementation's ctx via
// stats.ObserverFromContext, mirroring examples/reqreply-api's own Demo
// 11 (demo_observer_middleware.go).
func demoObservabilityMiddleware(ctx context.Context, obs *observability.DemoObserver) {
	fmt.Println("--- Demo: events.Observability[T] middleware ---")

	built, err := mqtt5broker.Build()
	if err != nil {
		fmt.Printf("  [error] mqtt5broker.Build: %v\n", err)
		return
	}

	// A fresh channel handle with events.Observability[T] attached
	// UNPAIRED (mw=nil) — runs unconditionally, injecting obs into ctx so
	// any downstream code (including a paired security Fn) can resolve
	// the SAME Observer via stats.ObserverFromContext.
	observedSub := routes.ObservedSub.
		SubscribeMW(nil, events.Observability[routes.SensorReading](obs)).
		WithHandler(func(_ context.Context, r routes.SensorReading) error {
			fmt.Printf("  ✓ observed handler: sensorId=%s value=%.1f\n", r.SensorID, r.Value)
			return nil
		})
	if err := observedSub.Register(built.Client); err != nil {
		fmt.Printf("  [error] Register: %v\n", err)
		return
	}

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	go func() { _ = built.Client.ServeSubscribers(handleCtx) }()
	built.Router.WaitHandler(routes.ObservedTopic)

	pub, err := client.BuildMQTT5(events.Info{Title: "Publisher", Version: "1.0.0"}, built.Broker, built.Router)
	if err != nil {
		fmt.Printf("  [error] client.BuildMQTT5: %v\n", err)
		return
	}
	observedPub := routes.ObservedPub.PublishMW(nil, events.Observability[routes.SensorReading](obs))
	if err := pub.Publish(ctx, observedPub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 26.0}); err != nil {
		fmt.Printf("  [error] publish: %v\n", err)
	}
	time.Sleep(30 * time.Millisecond)
	cancel()

	subCount, pubCount, rejCount := obs.Summary()
	fmt.Printf("  ✓ observer summary so far: subscribed=%d published=%d rejected=%d\n", subCount, pubCount, rejCount)
	fmt.Println()
}
