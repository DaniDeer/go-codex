package main

import (
	"context"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/client"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/mqttbroker"
	"github.com/DaniDeer/go-codex/examples/events-api/observability"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/examples/events-api/zeromqbroker"
)

// demoSecuritySubscribeMW demonstrates SubscribeMW/PublishMW-based
// security across ALL 3 adapters — supersedes examples/adapters-mqtt-
// security's 3 SecurityFunc patterns (now retired entirely, per
// docs/design/d-0002-pubsub-workflow-simplification.md's Addendum).
// The SAME declared routes.SensorDataSub/routes.APIKeyAuthMW security
// requirement is enforced by a DIFFERENT, adapter-shaped implementation
// Fn per transport (handlers.MQTTSecurityImpl/MQTT5SecurityImpl/
// ZeromqSecurityImpl) — proving the declarative mechanism generalizes
// across protocols with genuinely different raw-message access.
func demoSecuritySubscribeMW(ctx context.Context, obs *observability.DemoObserver) {
	fmt.Println("--- Demo: SubscribeMW/PublishMW-based security (all 3 adapters) ---")

	_, _, rejectedBefore := obs.Summary()

	// mqtt5 — credential extracted from a User Property (Pattern 2).
	// MUST use the handle-based escape hatch, NOT Client.Attach's
	// pub5.Publish — [mqtt5adapter.(*transport).Publish]'s reflection-
	// based workflow builds a bare *paho.Publish with NO Properties ever
	// set (documented v1 scope limitation, see demo_client_attach_
	// workflow.go), but handlers.MQTT5SecurityImpl STRICTLY requires the
	// "X-API-Key" User Property to be present — using Client.Attach here
	// would make the security check unconditionally reject every message.
	built5, err := mqtt5broker.Build()
	if err != nil {
		fmt.Printf("  [error] mqtt5broker.Build: %v\n", err)
		return
	}
	go func() { _ = built5.Client.ServeSubscribers(ctx) }()
	built5.Router.WaitHandler("sensor/data")
	pub5Transport := mqtt5adapter.NewPublishTransport[routes.SensorReading](built5.Broker, 1, false,
		mqtt5adapter.PublishOptions[routes.SensorReading]{
			UserProperties: []mqtt5adapter.UserProperty{{Key: "X-API-Key", Value: "sensor-key-abc123"}},
		},
	)
	if err := events.PublishHandle(ctx, routes.SensorDataPub, pub5Transport,
		routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 21.0}); err != nil {
		fmt.Printf("  [error] mqtt5 publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)

	// mqtt v3 — credential captured in a closure at CONNECT time (Pattern 1).
	builtV3, err := mqttbroker.Build("sensor-key-abc123", &handlers.TimeSeriesStore{}, 1e9)
	if err != nil {
		fmt.Printf("  [error] mqttbroker.Build: %v\n", err)
		return
	}
	go func() { _ = builtV3.Client.ServeSubscribers(ctx) }()
	builtV3.MQTTClient.WaitForSubscription("sensor/data", time.Second)
	pubV3, err := client.BuildMQTT(events.Info{Title: "Publisher (mqtt v3)", Version: "1.0.0"}, builtV3.MQTTClient)
	if err == nil {
		_ = pubV3.Publish(ctx, routes.SensorDataPub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 25.0})
	}
	time.Sleep(20 * time.Millisecond)

	// zeromq — the shape (read/write access to *T, plain error return)
	// demonstrated without a real in-payload credential field (this
	// demo's SensorReading has none — see handlers.ZeromqSecurityImpl's
	// own doc comment for how a production route would add one).
	builtZ, err := zeromqbroker.Build()
	if err != nil {
		fmt.Printf("  [error] zeromqbroker.Build: %v\n", err)
		return
	}
	go func() { _ = builtZ.Subscriber.ServeSubscribers(ctx) }()
	time.Sleep(20 * time.Millisecond)
	if err := builtZ.Publisher.Publish(ctx, routes.SensorDataPub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 30.0}); err != nil {
		fmt.Printf("  [error] zeromq publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)

	// Genuinely verify success rather than printing unconditionally — a
	// prior version of this demo printed "✓ ..." regardless of outcome,
	// which silently masked a real bug (mqtt5's leg rejected every
	// message before the fix above). Checking the SHARED obs's rejection
	// count delta catches any future regression the same way.
	_, _, rejectedAfter := obs.Summary()
	if rejectedAfter == rejectedBefore {
		fmt.Println("  ✓ same declared security requirement enforced across mqtt5, mqtt v3, and zeromq (0 new rejections)")
	} else {
		fmt.Printf("  ✗ unexpected security rejection(s): %d new rejection(s) recorded\n", rejectedAfter-rejectedBefore)
	}
	fmt.Println()
}
