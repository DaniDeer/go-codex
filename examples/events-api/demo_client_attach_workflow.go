package main

import (
	"context"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/client"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/mqttbroker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// demoClientAttachWorkflow demonstrates the Client.Attach PREFERRED
// workflow — mqtt v3 AND mqtt5, same shape: build a broker-side
// events.Client (ServeSubscribers running), a SEPARATE publisher-side
// events.Client attached to the SAME mock transport, then Publish once
// and watch the subscriber's handler print the delivered reading.
func demoClientAttachWorkflow(ctx context.Context) {
	fmt.Println("--- Demo: Client.Attach workflow (mqtt v3 + mqtt5) ---")

	// mqtt5
	built5, err := mqtt5broker.Build()
	if err != nil {
		fmt.Printf("  [error] mqtt5broker.Build: %v\n", err)
		return
	}
	pub5, err := client.BuildMQTT5(events.Info{Title: "Publisher (mqtt5)", Version: "1.0.0"}, built5.Broker, built5.Router)
	if err != nil {
		fmt.Printf("  [error] client.BuildMQTT5: %v\n", err)
		return
	}
	go func() { _ = built5.Client.ServeSubscribers(ctx) }()
	built5.Router.WaitHandler(routes.PlainReadingsTopic)
	if err := pub5.Publish(ctx, routes.PlainReadingsPub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 21.5}); err != nil {
		fmt.Printf("  [error] mqtt5 publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)

	// mqtt v3
	store := &handlers.TimeSeriesStore{}
	builtV3, err := mqttbroker.Build("sensor-key-abc123", store, 100)
	if err != nil {
		fmt.Printf("  [error] mqttbroker.Build: %v\n", err)
		return
	}
	pubV3, err := client.BuildMQTT(events.Info{Title: "Publisher (mqtt v3)", Version: "1.0.0"}, builtV3.MQTTClient)
	if err != nil {
		fmt.Printf("  [error] client.BuildMQTT: %v\n", err)
		return
	}
	go func() { _ = builtV3.Client.ServeSubscribers(ctx) }()
	builtV3.MQTTClient.WaitForSubscription(routes.PlainReadingsTopic, time.Second)
	if err := pubV3.Publish(ctx, routes.PlainReadingsPub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 30.0}); err != nil {
		fmt.Printf("  [error] mqtt v3 publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)
	fmt.Println()
}
