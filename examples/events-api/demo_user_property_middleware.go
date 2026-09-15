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
// validation — all capabilities the plain Client.Attach reflection shim
// doesn't support, requiring the handle-based
// NewSubscribeTransport/NewPublishTransport escape hatch instead.
func demoUserPropertyMiddleware(ctx context.Context) {
	fmt.Println("--- Demo: mqtt5 User Properties + ContentType + UserPropertyParam ---")

	// This demo wires its OWN broker/router pair directly instead of going
	// through mqtt5broker.Build (which attaches the FULL routes set with
	// security) — it needs a plain, unsecured channel to isolate the
	// User Properties/ContentType/UserPropertyParam capabilities.
	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)

	subTransport := mqtt5adapter.NewSubscribeTransport[routes.SensorReading](broker, router, 1,
		mqtt5adapter.SubscribeOptions{
			OnError: func(e mqtt5adapter.SubscribeError) {
				fmt.Printf("  [error] kind=%s: %v\n", e.Kind, e.Err)
			},
			UserPropertyParams: []mqtt5adapter.UserPropertyParam{
				mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: true}.
					WithCodec(codex.String().Refine(validate.NonEmptyString)),
			},
		},
	)
	if err := events.SubscribeHandle(ctx, routes.PlainReadingsSub, subTransport,
		func(ctx context.Context, r routes.SensorReading) error {
			if props, ok := mqtt5adapter.UserPropertiesFromContext(ctx); ok {
				tenantID := props.Get("TenantID")
				fmt.Printf("  ✓ received: sensorId=%s value=%.1f tenant=%s\n", r.SensorID, r.Value, tenantID)
			}
			return nil
		},
	); err != nil {
		fmt.Printf("  [error] SubscribeHandle: %v\n", err)
		return
	}

	pubTransport := mqtt5adapter.NewPublishTransport[routes.SensorReading](broker, 1, false,
		mqtt5adapter.PublishOptions[routes.SensorReading]{
			ContentType: "application/json",
			UserProperties: []mqtt5adapter.UserProperty{
				{Key: "TenantID", Value: "acme"},
			},
		},
	)
	if err := events.PublishHandle(ctx, routes.PlainReadingsPub, pubTransport,
		routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5},
	); err != nil {
		fmt.Printf("  [error] PublishHandle: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
