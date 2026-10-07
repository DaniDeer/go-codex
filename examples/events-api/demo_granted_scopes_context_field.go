package main

import (
	"context"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/auth"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// demoGrantedScopesContextField exercises auth.GrantedScopesSub — the
// channel-BOUND class (auth.NewGrantedScopesSensorMw, built via
// events.BoundSecuritySubscribeMiddleware with auth.VerifyAPIKeyGS
// embedded at construction), attached via Subscriber.SubscribeBoundMW,
// with a REAL GrantedScopes-carrying Out enforced by
// middleware.CheckScopes, AND the authenticated API key propagated to
// the real subscribe handler via middleware.ContextField (routes.
// GrantedScopesUserIDField) — zero manual re-decoding inside the
// handler. See docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's events/
// BoundSubscribeMiddleware section for the full design this demo
// exercises end-to-end.
func demoGrantedScopesContextField(ctx context.Context) {
	fmt.Println("--- Demo: GrantedScopes + ContextField (SubscribeBoundMW) ---")

	runOnce := func(label, apiKey string, wantHandled bool) {
		router := mqtt5broker.NewMockRouter()
		broker := mqtt5broker.NewMockBroker(router)

		client := events.NewClient(events.WithInfo(events.Info{Title: "GrantedScopes demo", Version: "1.0.0"}))
		if err := client.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
			fmt.Printf("  [error] Attach: %v\n", err)
			return
		}

		var rejected error
		sub := auth.GrantedScopesSub.
			SubscribeBoundMW(auth.NewGrantedScopesSensorMw(auth.VerifyAPIKeyGS)).
			WithHandler(auth.PrintReadingGS(label)).
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
		router.WaitHandler("sensor/data-gs")

		pub := auth.GrantedScopesPub.WithOptions(mqtt5adapter.PublishOptions[routes.SensorReading]{
			UserProperties: []mqtt5adapter.UserProperty{{Key: "X-API-Key", Value: apiKey}},
		})
		if err := client.Publish(ctx, pub, routes.SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 19.5}); err != nil {
			fmt.Printf("  [error] Publish: %v\n", err)
			return
		}
		time.Sleep(30 * time.Millisecond)

		switch {
		case rejected != nil:
			fmt.Printf("  %s: rejected as expected=%v — %v\n", label, !wantHandled, rejected)
		case wantHandled:
			fmt.Printf("  %s: handled as expected\n", label)
		default:
			fmt.Printf("  %s: ✗ unexpectedly handled with no rejection — BUG\n", label)
		}
	}

	fmt.Println("  → correct scope granted (read:sensors):")
	runOnce("correct-scope", "sensor-key-readonly", true)

	fmt.Println("  → known key, WRONG scope granted (write:sensors, not read:sensors):")
	runOnce("wrong-scope", "sensor-key-writeonly", false)

	fmt.Println()
}
