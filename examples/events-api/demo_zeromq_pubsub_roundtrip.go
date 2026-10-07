package main

import (
	"context"
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/examples/events-api/zeromqbroker"
)

// demoZeromqPubSubRoundtrip demonstrates a full ZeroMQ PUB/SUB roundtrip:
// a publisher-side events.Client and a subscriber-side events.Client,
// wired via an in-process PipeSocket pair (see zeromqbroker.NewPipe),
// proving publish→subscribe delivery end-to-end with no real ZeroMQ
// broker/socket library required.
func demoZeromqPubSubRoundtrip(ctx context.Context) {
	fmt.Println("--- Demo: ZeroMQ PUB/SUB full roundtrip ---")

	built, err := zeromqbroker.Build()
	if err != nil {
		fmt.Printf("  [error] zeromqbroker.Build: %v\n", err)
		return
	}

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- built.Subscriber.ServeSubscribers(handleCtx) }()
	time.Sleep(20 * time.Millisecond) // let SetSubscription register

	// routes.ZeromqReadingsChannel is Mounted under zeromqbroker.Build's
	// shared "sensor" Router (docs/design/d-0008-declarative-router-groups.md)
	// — compose the SAME relative topic ("zeromq/readings") back to
	// "sensor/zeromq/readings" via Handle(nil, events.WithRouter(...)).
	pubHandle, err := routes.ZeromqReadingsPub.Handle(nil, events.WithRouter(events.NewRouter("sensor")))
	if err != nil {
		fmt.Printf("  [error] building publish handle: %v\n", err)
		return
	}
	reading := routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 24.5}
	if err := built.Publisher.Publish(ctx, pubHandle, reading); err != nil {
		fmt.Printf("  [error] publish: %v\n", err)
	}
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	fmt.Println()
}
