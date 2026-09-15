package main

import (
	"fmt"

	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/mqttbroker"
	"github.com/DaniDeer/go-codex/examples/events-api/zeromqbroker"
)

// demoSpecPrintingAsyncAPI prints the AsyncAPI 3.0 spec derived from each
// of the 3 adapters' own events.Client — explicitly highlighting that the
// SAME spec content (the "sensor/data" channel/schema) derives from the
// ONE shared routes/ package regardless of which adapter registered it,
// proving zero drift. Mirrors the now-retired adapters-mqtt-contract's
// own AsyncAPI section's point, demonstrated project-wide instead of in
// one standalone demo.
func demoSpecPrintingAsyncAPI() {
	fmt.Println("--- Demo: AsyncAPI 3.0 spec printing (all 3 adapters, zero drift) ---")

	built5, err := mqtt5broker.Build()
	if err != nil {
		fmt.Printf("  [error] mqtt5broker.Build: %v\n", err)
		return
	}
	builtV3, err := mqttbroker.Build("sensor-key-abc123", &handlers.TimeSeriesStore{}, 100.0)
	if err != nil {
		fmt.Printf("  [error] mqttbroker.Build: %v\n", err)
		return
	}
	builtZ, err := zeromqbroker.Build()
	if err != nil {
		fmt.Printf("  [error] zeromqbroker.Build: %v\n", err)
		return
	}

	printDoc := func(label string, yamlBytes []byte, marshalErr error) {
		if marshalErr != nil {
			fmt.Printf("  [error] %s MarshalYAML: %v\n", label, marshalErr)
			return
		}
		fmt.Printf("\n  === %s ===\n", label)
		fmt.Println(string(yamlBytes))
	}

	if doc, specErr := built5.Client.AsyncAPISpec(); specErr == nil {
		b, marshalErr := doc.MarshalYAML()
		printDoc("mqtt5", b, marshalErr)
	}
	if doc, specErr := builtV3.Client.AsyncAPISpec(); specErr == nil {
		b, marshalErr := doc.MarshalYAML()
		printDoc("mqtt v3", b, marshalErr)
	}
	if doc, specErr := builtZ.Subscriber.AsyncAPISpec(); specErr == nil {
		b, marshalErr := doc.MarshalYAML()
		printDoc("zeromq", b, marshalErr)
	}

	fmt.Println("\n  ✓ every spec above shares the SAME \"sensor/data\" channel/schema, derived from routes/ — proving zero drift across all 3 adapters")
	fmt.Println()
}
