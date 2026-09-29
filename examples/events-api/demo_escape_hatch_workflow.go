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
// hatch — NewSubscribeTransport/events.SubscribeHandle directly, instead
// of Client.Attach+Client.Subscribe.
//
// NOTE: a custom OnError callback and a non-default (YAML) payload
// format are BOTH now supported through Client.Attach's reflection-based
// workflow too (docs/roadmap/capability-requirement-composition.md's
// Phase 4e closed that gap) — this demo's escape-hatch usage is no
// longer justified by either capability. It remains on the escape hatch
// for a DIFFERENT, still-genuinely-permanent reason, confirmed via
// [adapters/mqtt.NewSubscribeTransport]'s own doc comment: this
// lower-tier primitive is (1) fully compile-time type-safe — no
// reflection/`any`-typing, no runtime [events.TransportTypeMismatchError]
// risk; (2) fully spec-free — no `*events.Client` construction/
// registration ceremony required at all; and (3) explicitly
// NON-BLOCKING (registers with the broker and returns immediately),
// a DELIBERATE, structural difference from Client.Subscribe's
// block-until-cancelled contract that Client.Subscribe cannot provide.
// This mirrors adapters/nethttp's `ServeOne` — an analogous
// adapter-owned, lower-tier primitive existing alongside api/rest's
// Client.Call/Attach for the identical reason (REST/reqreply's OWN
// "drive" verb, `CallWithHandle`, moved onto the API layer as
// `rest.CallWithTransport`/`reqreply.CallWithTransport` — see below).
//
// RESOLVED (docs/roadmap/capability-requirement-composition.md's Phase
// 5a): `adapters/nethttp.CallWithHandle` and `adapters/mqtt5`/
// `adapters/zeromq`'s `Serve[Req,Resp]`/`Call[Req,Resp]` were deleted;
// `api/rest.CallWithTransport`/`api/reqreply.CallWithTransport`/
// `ServeWithTransport` now own the "drive" step, taking an adapter-built
// `New*Transport` value — matching THIS demo's `events.SubscribeHandle`/
// `PublishHandle` shape exactly. `ServeOne` was investigated too and
// confirmed to stay adapter-owned (its return type, `http.Handler`, is
// inherently HTTP-specific — no reqreply/events equivalent concept
// exists to compare it against). This demo's own premise (a
// compile-time type-safe, spec-free, non-blocking primitive existing
// alongside the spec-driven Client workflow) is UNCHANGED and now the
// established, final shape across all three APIs.
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
	opts.Capabilities = []adaptermqtt.Capability{adaptermqtt.QoSAtLeastOnce}
	transport := adaptermqtt.NewSubscribeTransport[routes.SensorReading](client, opts, yamlFormat)

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
