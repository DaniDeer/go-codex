package main

import (
	"context"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/examples/events-api/client"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/mqttbroker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"

	"github.com/DaniDeer/go-codex/api/events"
)

// demoDomainBoundaryPipeline preserves adapters-mqtt's own
// MeasurementEvent→TimeSeriesRecord→AlertEvent three-layer codec pipeline
// scenario — the same domain model docs/concepts/codec-as-domain-
// boundary.md illustrates itself with. A measurement arrives, is written
// to a (mock) time-series store, and — if it breaches a threshold — an
// AlertEvent is published on a SEPARATE topic.
//
// NOTE on interleaved "[wildcard] ..." console output: mqttbroker.Build
// (shared infrastructure reused by several demos) ALSO registers
// routes.WildcardSub ("sensors/#"), which overlaps with this demo's own
// "sensors/{sensorID}/measurements"/"sensors/{sensorID}/alerts" topics —
// every message this demo publishes ALSO reaches the (otherwise
// unrelated) wildcard handler, printing an extra "[wildcard] ..." line
// per publish. This is expected cross-talk from the shared builder, not
// a bug — the pipeline's own correctness is verified independently below
// via store.Count().
func demoDomainBoundaryPipeline(ctx context.Context) {
	fmt.Println("--- Demo: domain-boundary pipeline (MeasurementEvent → TSDB → AlertEvent) ---")

	store := &handlers.TimeSeriesStore{}
	built, err := mqttbroker.Build("sensor-key-abc123", store, 100.0)
	if err != nil {
		fmt.Printf("  [error] mqttbroker.Build: %v\n", err)
		return
	}
	go func() { _ = built.Client.ServeSubscribers(ctx) }()
	built.MQTTClient.WaitForSubscription("sensors/+/measurements", time.Second)

	pub, err := client.BuildMQTT(events.Info{Title: "Sensor gateway", Version: "1.0.0"}, built.MQTTClient)
	if err != nil {
		fmt.Printf("  [error] client.BuildMQTT: %v\n", err)
		return
	}

	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	now := time.Now().UTC().Format(time.RFC3339)

	fmt.Println("  → publishing a normal measurement (below threshold):")
	if err := pub.Publish(ctx, routes.MeasurementChannel.WithPublish(events.Publish{}), routes.MeasurementEvent{
		SensorID: sensorID, Value: 42.0, Unit: "celsius", Timestamp: now,
	}); err != nil {
		fmt.Printf("  [error] publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)

	fmt.Println("  → publishing a threshold-breaching measurement:")
	if err := pub.Publish(ctx, routes.MeasurementChannel.WithPublish(events.Publish{}), routes.MeasurementEvent{
		SensorID: sensorID, Value: 150.0, Unit: "celsius", Timestamp: now,
	}); err != nil {
		fmt.Printf("  [error] publish: %v\n", err)
	}
	time.Sleep(20 * time.Millisecond)

	fmt.Printf("  ✓ time series store now has %d record(s)\n", store.Count())
	fmt.Println()
}
