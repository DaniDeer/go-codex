package main

import (
	"context"
	"fmt"
	"time"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/mqttbroker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// demoWildcardSubscription demonstrates a "sensors/#"-style wildcard
// topic subscription — ONE subscription receives messages published to
// several DIFFERENT concrete sensor topics, proving the multi-level MQTT
// wildcard actually spans them.
func demoWildcardSubscription(ctx context.Context) {
	fmt.Println("--- Demo: wildcard topic subscription (sensors/#) ---")

	client := mqttbroker.NewMockClient()
	transport := adaptermqtt.NewSubscribeTransport[routes.SensorReading](client, 1, adaptermqtt.SubscribeOptions{})

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	var received []string
	done := make(chan error, 1)
	go func() {
		done <- events.SubscribeHandle(handleCtx, routes.WildcardSub, transport,
			func(_ context.Context, r routes.SensorReading) error {
				received = append(received, r.SensorID)
				fmt.Printf("  ✓ received from wildcard: sensorId=%s value=%.1f\n", r.SensorID, r.Value)
				return nil
			})
	}()

	client.WaitForSubscription("sensors/#", time.Second)
	client.Deliver("sensors/temp-01/readings", []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":19.5}`))
	client.Deliver("sensors/temp-02/measurements", []byte(`{"sensor_id":"a1a2a3a4-58cc-4372-a567-0e02b2c3d479","value":21.0}`))
	client.Deliver("sensors/temp-03/alerts", []byte(`{"sensor_id":"b1b2b3b4-58cc-4372-a567-0e02b2c3d479","value":99.9}`))
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	fmt.Printf("  ✓ received readings from %d distinct sensor topics via ONE wildcard subscription\n", len(received))
	fmt.Println()
}
