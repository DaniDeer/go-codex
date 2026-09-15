package main

import (
	"context"
	"fmt"
	"time"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/mqttbroker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/format"
)

// demoEscapeHatchWorkflow demonstrates mqtt v3's handle-based escape
// hatch — a custom OnError callback and a non-default (YAML) payload
// format — both capabilities Client.Attach's reflection-based workflow
// doesn't support, requiring NewSubscribeTransport/events.SubscribeHandle
// directly.
func demoEscapeHatchWorkflow(ctx context.Context) {
	fmt.Println("--- Demo: mqtt v3 handle-based escape hatch (OnError, non-default format) ---")

	client := mqttbroker.NewMockClient()

	var lastErr error
	opts := adaptermqtt.SubscribeOptions{
		OnError: func(e adaptermqtt.SubscribeError) {
			lastErr = e
			fmt.Printf("  [error] kind=%s topic=%s: %v\n", e.Kind, e.Topic, e.Err)
		},
	}
	yamlFormat := format.YAML(routes.SensorReadingCodec)
	transport := adaptermqtt.NewSubscribeTransport[routes.SensorReading](client, 1, opts, yamlFormat)

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- events.SubscribeHandle(handleCtx, routes.PlainReadingsSub, transport,
			func(_ context.Context, r routes.SensorReading) error {
				fmt.Printf("  ✓ handler (YAML payload): sensorId=%s value=%.1f\n", r.SensorID, r.Value)
				return nil
			})
	}()

	client.WaitForSubscription(routes.PlainReadingsTopic, time.Second)
	yamlPayload, err := yamlFormat.Marshal(routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 19.5})
	if err != nil {
		fmt.Printf("  [error] marshal: %v\n", err)
	} else {
		client.Deliver(routes.PlainReadingsTopic, yamlPayload)
	}
	client.Deliver(routes.PlainReadingsTopic, []byte("not: [valid"))
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if lastErr == nil {
		fmt.Println("  [unexpected] expected an OnError callback for the malformed payload")
	}
	fmt.Println()
}
