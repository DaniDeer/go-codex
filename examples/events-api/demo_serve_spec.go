package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/observer"
	"github.com/DaniDeer/go-codex/format"
)

// specTopicChannel declares the ONE shared []byte channel shape ServeSpec
// publishes to — a subscriber built from this channel can receive EITHER
// spec topic below (the payload codec is the same either way; only the
// topic differs).
var specTopicChannel = events.NewChannel[[]byte]("sensor/spec", codex.Bytes())

// specSubscribeFormats is the SAME format.Binary(codex.Bytes()) format
// [events.Client.ServeSpec] publishes with — a subscriber MUST declare
// this on its own side (via a per-call [events.ClientSubscribeOptions]
// override here), or decoding fails: the default JSON encoding of a
// []byte base64-wraps it, which is wrong for reading the raw spec text.
var specSubscribeFormats = []format.Format[[]byte]{format.Binary(codex.Bytes())}

// demoServeSpecPublish demonstrates [events.Client.ServeSpec] end-to-end:
// unlike rest/reqreply, pub/sub has no format-negotiation mechanism at
// publish time, so publishing BOTH YAML and JSON means calling ServeSpec
// TWICE, with two different topics — this demo SUBSCRIBES to both
// topics first, then publishes, then prints what was actually received
// on each, proving the spec reaches a real subscriber, not just "was
// published without error". The YAML publish ALSO attaches
// [events.WithSpecMiddleware] (the shared [events.Observability] general-
// purpose decorator every other demo's `.PublishMW(nil, ...)` already
// uses) — demonstrating the spec endpoint composes with the SAME
// middleware capabilities as any other channel, mirroring
// examples/rest-api's real `rest.WithSpecMiddleware(nil, timingFn)` wiring.
func demoServeSpecPublish(ctx context.Context, obs *observer.DemoObserver) {
	fmt.Println("--- Demo: ServeSpec — self-serving AsyncAPI spec (pub/sub, two formats, two topics) ---")

	built, err := mqtt5broker.Build()
	if err != nil {
		fmt.Printf("  [error] mqtt5broker.Build: %v\n", err)
		return
	}

	handleCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	var receivedYAML, receivedJSON []byte
	doneYAML := make(chan error, 1)
	doneJSON := make(chan error, 1)

	go func() {
		doneYAML <- built.Client.Subscribe(handleCtx,
			specTopicChannel.WithSubscribe(events.Subscribe{}),
			func(_ context.Context, body []byte) error {
				receivedYAML = body
				return nil
			},
			events.ClientSubscribeOptions{Formats: specSubscribeFormats})
	}()
	go func() {
		doneJSON <- built.Client.Subscribe(handleCtx,
			events.NewChannel[[]byte]("sensor/spec-json", codex.Bytes()).WithSubscribe(events.Subscribe{}),
			func(_ context.Context, body []byte) error {
				receivedJSON = body
				return nil
			},
			events.ClientSubscribeOptions{Formats: specSubscribeFormats})
	}()

	built.Router.WaitHandler("sensor/spec")
	built.Router.WaitHandler("sensor/spec-json")

	if err := built.Client.ServeSpec(ctx, "sensor/spec",
		events.WithSpecMiddleware(nil, events.Observability[[]byte](obs))); err != nil {
		fmt.Printf("  [error] ServeSpec (yaml): %v\n", err)
		return
	}
	if err := built.Client.ServeSpec(ctx, "sensor/spec-json", events.WithSpecFormat(events.SpecFormatJSON)); err != nil {
		fmt.Printf("  [error] ServeSpec (json): %v\n", err)
		return
	}

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-doneYAML
	<-doneJSON

	snippet := func(b []byte) string {
		s := strings.TrimSpace(string(b))
		if len(s) > 60 {
			s = s[:60] + "..."
		}
		return s
	}
	fmt.Printf("  ✓ [sensor/spec]      bytes: %d, snippet: %q\n", len(receivedYAML), snippet(receivedYAML))
	fmt.Printf("  ✓ [sensor/spec-json] bytes: %d, snippet: %q\n", len(receivedJSON), snippet(receivedJSON))
	fmt.Println()
}
