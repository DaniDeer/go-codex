package main

import (
	"context"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// demoErrorPathErgonomics demonstrates the pub/sub analogue of
// rest.ErrorPattern: a declared events.ErrorChannel on
// routes.ReadingsWithErrorsChannel causes mqtt5.PublishAdapter to publish
// a typed error payload to a dedicated error-output topic whenever a
// matching domain error reaches it — instead of only calling
// MQTT5DrainPublishOptions.OnError.
func demoErrorPathErgonomics(ctx context.Context) {
	fmt.Println("--- Demo: error-path ergonomics (events.ErrorChannel) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)

	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
	handle, err := routes.ReadingsWithErrorsPub.Handle(evtClient)
	if err != nil {
		fmt.Printf("  [error] register: %v\n", err)
		return
	}

	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	port, err := ports.NewSinkPort[routes.SensorReading]("readings-with-errors", routes.SensorReadingCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		fmt.Printf("  [error] construct port: %v\n", err)
		return
	}
	var onErrorCalled bool
	port.Bind(ctx, mqtt5adapter.PublishAdapter(broker, handle, format.JSON(routes.SensorReadingCodec),
		mqtt5adapter.MQTT5DrainPublishOptions{
			Vars: map[string]string{"sensorID": sensorID},
			OnError: func(e error) {
				onErrorCalled = true
				fmt.Printf("  ✗ OnError fallback called (unexpected for a matched pattern): %v\n", e)
			},
		}))

	errCh := make(chan error, 1)
	valCh := make(chan routes.SensorReading)
	errCh <- routes.SensorOutOfRangeError{SensorID: sensorID, Value: 999.9}
	close(errCh)
	close(valCh)
	port.Feed(ctx, gstream.Stream[routes.SensorReading]{Values: valCh, Errors: errCh})

	time.Sleep(50 * time.Millisecond)
	if !onErrorCalled {
		fmt.Printf("  ✓ matched SensorOutOfRangeError → published typed payload to %q (OnError NOT called)\n",
			"sensors/"+sensorID+"/readings/errors")
	}
	fmt.Println()
}
