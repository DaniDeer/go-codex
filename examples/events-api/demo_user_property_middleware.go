package main

import (
	"context"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/validate"
)

// demoUserPropertyMiddleware demonstrates 3 mqtt5-specific features: User
// Properties + ContentType auto-format-selection + UserPropertyParam
// validation — ALL now reachable through Client.Attach+Client.Subscribe/
// Publish (docs/design/d-0006-protocol-native-capabilities.md's Phase 4e
// closed the UserPropertyParams/UserProperties/ContentType gaps; its own
// addendum ALSO added the ctx-injection this demo's
// UserPropertiesFromContext call needs, which was the one genuinely
// remaining gap). No adapter-specific NewSubscribeTransport/
// NewPublishTransport escape hatch needed anymore.
func demoUserPropertyMiddleware(ctx context.Context) {
	fmt.Println("--- Demo: mqtt5 User Properties + ContentType + UserPropertyParam ---")

	// This demo wires its OWN broker/router pair directly instead of going
	// through mqtt5broker.Build (which attaches the FULL routes set with
	// security) — it needs a plain, unsecured channel to isolate the
	// User Properties/ContentType/UserPropertyParam capabilities.
	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)

	evClient := events.NewClient(events.WithInfo(events.Info{Title: "User property demo", Version: "1.0.0"}))
	if err := evClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}

	sub := routes.PlainReadingsSub.WithOptions(mqtt5adapter.SubscribeOptions{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
		OnError: func(e mqtt5adapter.SubscribeError) {
			fmt.Printf("  [error] kind=%s: %v\n", e.Kind, e.Err)
		},
		UserPropertyParams: []mqtt5adapter.UserPropertyParam{
			mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: true}.
				WithCodec(codex.String().Refine(validate.NonEmptyString)),
		},
	})
	go func() {
		_ = evClient.Subscribe(ctx, sub, func(hCtx context.Context, r routes.SensorReading) error {
			if props, ok := mqtt5adapter.UserPropertiesFromContext(hCtx); ok {
				tenantID := props.Get("TenantID")
				fmt.Printf("  ✓ received: sensorId=%s value=%.1f tenant=%s\n", r.SensorID, r.Value, tenantID)
			}
			return nil
		})
	}()
	router.WaitHandler(routes.PlainReadingsTopic)

	pub := routes.PlainReadingsPub.WithOptions(mqtt5adapter.PublishOptions[routes.SensorReading]{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
		ContentType:  "application/json",
		UserProperties: []mqtt5adapter.UserProperty{
			{Key: "TenantID", Value: "acme"},
		},
	})
	if err := evClient.Publish(ctx, pub,
		routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5},
	); err != nil {
		fmt.Printf("  [error] Publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
