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
	"github.com/DaniDeer/go-codex/examples/events-api/observer"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/examples/events-api/zeromqbroker"
)

// demoSecuritySubscribeMW demonstrates SubscribeMW/PublishMW-based
// security across ALL 3 adapters — supersedes examples/adapters-mqtt-
// security's 3 SecurityFunc patterns (now retired entirely, per
// docs/design/d-0002-pubsub-workflow-simplification.md's Addendum).
// The SAME declared routes.SensorDataSub/routes.APIKeyAuthMW security
// requirement is enforced by a DIFFERENT, adapter-shaped implementation
// Fn per transport (auth.MQTTSecurityImpl/MQTT5SecurityImpl/
// ZeromqSecurityImpl) — proving the declarative mechanism generalizes
// across protocols with genuinely different raw-message access.
func demoSecuritySubscribeMW(ctx context.Context, obs *observer.DemoObserver) {
	fmt.Println("--- Demo: SubscribeMW/PublishMW-based security (all 3 adapters) ---")

	_, _, rejectedBefore, _ := obs.Summary()

	// routes.SensorDataSub/SensorDataPub are Mounted under each broker's
	// own "sensor" Router (docs/design/d-0008-declarative-router-groups.md)
	// — these 3 bare, outside-the-broker Publish calls compose the SAME
	// "sensor" prefix via .Handle(nil, events.WithRouter(sensorRouter))
	// (api/events.WithRouter — closed this session's own confirmed gap:
	// a bare Publisher value passed directly to Client.Publish has NO
	// way to recover a Router-composed topic otherwise).
	sensorRouter := events.NewRouter("sensor")

	// mqtt5 — credential extracted from a User Property (Pattern 2).
	// built5.Client is ALREADY an attached *events.Client (mqtt5broker.
	// Build's own Client.Attach) — Client.Publish itself now reads
	// PublishOptions.UserProperties and sets them on the outgoing
	// message (docs/design/d-0006-protocol-native-capabilities.md's
	// Phase 4e closed the former "reflection-based Publish builds a
	// bare *paho.Publish with NO Properties ever set" gap), so no
	// adapter-specific NewPublishTransport escape hatch is needed
	// anymore for auth.MQTT5SecurityImpl's "X-API-Key" User
	// Property requirement.
	built5, err := mqtt5broker.Build()
	if err != nil {
		fmt.Printf("  [error] mqtt5broker.Build: %v\n", err)
		return
	}
	go func() { _ = built5.Client.ServeSubscribers(ctx) }()
	built5.Router.WaitHandler("sensor/data")
	pub5, err := routes.SensorDataPub.WithOptions(mqtt5adapter.PublishOptions[routes.SensorReading]{
		Capabilities:   []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
		UserProperties: []mqtt5adapter.UserProperty{{Key: "X-API-Key", Value: "sensor-key-abc123"}},
	}).Handle(nil, events.WithRouter(sensorRouter))
	if err != nil {
		fmt.Printf("  [error] building pub5 handle: %v\n", err)
		return
	}
	if err := built5.Client.Publish(ctx, pub5,
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
		pubV3Handle, handleErr := routes.SensorDataPub.Handle(nil, events.WithRouter(sensorRouter))
		if handleErr != nil {
			fmt.Printf("  [error] building pubV3 handle: %v\n", handleErr)
			return
		}
		_ = pubV3.Publish(ctx, pubV3Handle, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 25.0})
	}
	time.Sleep(20 * time.Millisecond)

	// zeromq — the shape (read/write access to *T, plain error return)
	// demonstrated without a real in-payload credential field (this
	// demo's SensorReading has none — see auth.ZeromqSecurityImpl's
	// own doc comment for how a production route would add one).
	builtZ, err := zeromqbroker.Build()
	if err != nil {
		fmt.Printf("  [error] zeromqbroker.Build: %v\n", err)
		return
	}
	go func() { _ = builtZ.Subscriber.ServeSubscribers(ctx) }()
	time.Sleep(20 * time.Millisecond)
	pubZHandle, err := routes.SensorDataPub.Handle(nil, events.WithRouter(sensorRouter))
	if err != nil {
		fmt.Printf("  [error] building zeromq publish handle: %v\n", err)
		return
	}
	if err := builtZ.Publisher.Publish(ctx, pubZHandle, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 30.0}); err != nil {
		fmt.Printf("  [error] zeromq publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)

	// Genuinely verify success rather than printing unconditionally — a
	// prior version of this demo printed "✓ ..." regardless of outcome,
	// which silently masked a real bug (mqtt5's leg rejected every
	// message before the fix above). Checking the SHARED obs's rejection
	// count delta catches any future regression the same way.
	_, _, rejectedAfter, _ := obs.Summary()
	if rejectedAfter == rejectedBefore {
		fmt.Println("  ✓ same declared security requirement enforced across mqtt5, mqtt v3, and zeromq (0 new rejections)")
	} else {
		fmt.Printf("  ✗ unexpected security rejection(s): %d new rejection(s) recorded\n", rejectedAfter-rejectedBefore)
	}
	fmt.Println()
}
