package main

import (
	"context"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/events-api/auth"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/observer"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/examples/events-api/tracing"
)

// demoBoundMiddlewareSplit exercises routes/bound_middleware_split_demo.go
// (mqtt5 only, mirroring demoGrantedScopesContextField's own
// self-contained-client precedent) — a direct, side-by-side contrast of
// the two classes docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 introduced:
//
//  1. ReusableAloneSub — the reusable class (Class 1) attached ALONE,
//     via plain .Use(). No declared Subscribe.Security at all.
//  2. BOUND ALONE (Class 2) — already fully exercised by
//     demoSecuritySubscribeMW/demoGrantedScopesContextField above, where
//     [auth.NewAPIKeyAuthMW] is reused UNCHANGED across BOTH
//     [routes.SensorDataSub] AND [routes.SecuredReadingsSub] — not
//     repeated here.
//  3. StackedDemoSub — reusable AND bound attached TOGETHER on ONE
//     subscriber — and, to show the composition is genuinely open-ended
//     (not just a fixed 2-layer story), a THIRD, orthogonal kind is
//     layered in too: the general-purpose, UNPAIRED
//     [events.Observability] middleware (.SubscribeMW(nil, ...),
//     mirrors demoObservabilityMiddleware's own attachment exactly) —
//     injecting obs (the SAME Observer instance used throughout this
//     example) into ctx BEFORE either the reusable or bound Fn runs, so
//     tracing.ReusablePresenceMw/auth.NewAPIKeyAuthMW's own rejection
//     path (if it were to reject) would report through the identical
//     stats.ObserverFromContext mechanism. Attach order on
//     StackedDemoSub is `.SubscribeMW(nil, observability).Use(reusable).
//     SubscribeBoundMW(bound)` — observer first (outermost,
//     cross-cutting), reusable second (generic), bound last
//     (route/channel-specific) — proving all THREE attachment KINDS
//     (unpaired general-purpose, reusable Class 1, bound Class 2)
//     compose on one subscriber, in declaration order.
func demoBoundMiddlewareSplit(ctx context.Context, obs *observer.DemoObserver) {
	fmt.Println("--- Demo: bound-middleware-split — reusable alone / bound alone / stacked ---")

	// ── 1. Reusable class ALONE ──────────────────────────────────────────
	func() {
		router := mqtt5broker.NewMockRouter()
		broker := mqtt5broker.NewMockBroker(router)
		client := events.NewClient(events.WithInfo(events.Info{Title: "bound-middleware-split demo", Version: "1.0.0"}))
		if err := client.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
			fmt.Printf("  [error] Attach: %v\n", err)
			return
		}

		sub := routes.ReusableAloneSub.
			SubscribeMW(nil, events.Observability[routes.SensorReading](obs)).
			Use(tracing.ReusablePresenceMw).
			WithHandler(handlers.PrintReading("mqtt5-reusable-alone"))
		if err := sub.Register(client); err != nil {
			fmt.Printf("  [error] Register: %v\n", err)
			return
		}

		subCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		go func() { _ = client.ServeSubscribers(subCtx) }()
		router.WaitHandler("sensor/data-reusable-alone")

		pub := routes.ReusableAlonePub.
			PublishMW(nil, events.Observability[routes.SensorReading](obs)).
			WithOptions(mqtt5adapter.PublishOptions[routes.SensorReading]{
				UserProperties: []mqtt5adapter.UserProperty{{Key: "X-Demo-Trace-Id", Value: "trace-abc"}},
			})
		if err := client.Publish(ctx, pub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}); err != nil {
			fmt.Printf("  [error] Publish: %v\n", err)
			return
		}
		time.Sleep(30 * time.Millisecond)
	}()

	// ── 3. Reusable + bound STACKED on one subscriber ───────────────────
	func() {
		router := mqtt5broker.NewMockRouter()
		broker := mqtt5broker.NewMockBroker(router)
		client := events.NewClient(events.WithInfo(events.Info{Title: "bound-middleware-split demo", Version: "1.0.0"}))
		if err := client.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
			fmt.Printf("  [error] Attach: %v\n", err)
			return
		}

		var rejected error
		sub := routes.StackedDemoSub.
			SubscribeMW(nil, events.Observability[routes.SensorReading](obs)).
			Use(tracing.ReusablePresenceMw).
			SubscribeBoundMW(auth.NewAPIKeyAuthMW(auth.MQTT5SecurityImpl).
				WithSubscribeProperty(events.NewPropertyParam("X-API-Key", codex.String(),
					func(in auth.APIKeyAuthIn) string { return in.Key },
					func(in *auth.APIKeyAuthIn, v string) { in.Key = v },
				))).
			WithHandler(handlers.PrintReading("mqtt5-stacked")).
			WithOptions(mqtt5adapter.SubscribeOptions{
				OnError: func(e mqtt5adapter.SubscribeError) { rejected = e },
			})
		if err := sub.Register(client); err != nil {
			fmt.Printf("  [error] Register: %v\n", err)
			return
		}

		subCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		go func() { _ = client.ServeSubscribers(subCtx) }()
		router.WaitHandler("sensor/data-stacked")

		pub := routes.StackedDemoPub.
			PublishMW(nil, events.Observability[routes.SensorReading](obs)).
			WithOptions(mqtt5adapter.PublishOptions[routes.SensorReading]{
				UserProperties: []mqtt5adapter.UserProperty{
					{Key: "X-Demo-Trace-Id", Value: "trace-def"},
					{Key: "X-API-Key", Value: "sensor-key-abc123"},
				},
			})
		if err := client.Publish(ctx, pub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 2.0}); err != nil {
			fmt.Printf("  [error] Publish: %v\n", err)
			return
		}
		time.Sleep(30 * time.Millisecond)

		if rejected != nil {
			fmt.Printf("  stacked: ✗ unexpectedly rejected — %v\n", rejected)
		} else {
			fmt.Println("  stacked: handled as expected (reusable + bound both ran)")
		}
	}()

	fmt.Println()
}
