package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/mqtt5broker"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file is the comprehensive events.ErrorChannel/DeadLetter showcase —
// consolidates what was previously 2 separate demo files into one,
// covering EVERY facet of pub/sub's declarative error-path mechanism on
// BOTH the publish side (upstream pipeline error) and the subscribe side
// (handler business error) wherever the underlying mechanism supports it:
//
//  1. demoErrorChannelPublishSide — Mapped mode, UPSTREAM pipeline error →
//     typed publish, with an observer spy proving H2's fix (session
//     review round-5).
//  2. demoErrorChannelSubscribeSideAndConsumer — Mapped mode, subscribe-
//     side HANDLER business error → typed publish → a downstream
//     CONSUMER decodes it as an ordinary typed channel (pub/sub's
//     analogue of REST/reqreply's client-side match — no synchronous
//     caller exists in pub/sub).
//  3. demoErrorChannelDirectMode — Direct mode (the 2nd of events' 2
//     declaration mechanisms), on BOTH the subscribe side AND (via
//     demoErrorChannelDirectModePublishSide) the publish side — this
//     symmetry was completed in a follow-up review round (previously
//     subscribe-side only).
//  4. demoErrorChannelActions — the 3 ErrorActions (Respond/Handle/Log),
//     on BOTH the publish side (demoErrorChannelActionsPublishSide) AND
//     the subscribe side (demoErrorChannelActionsSubscribeSide) — the
//     subscribe-side half was also completed in the same follow-up round
//     (previously publish-side only).
//  5. demoErrorChannelDeadLetterFallback — the two-tier fallback:
//     a MATCHED error type (→ typed ErrorChannel publish) vs a genuinely
//     UNMATCHED one (→ events.DeadLetter), dispatched side-by-side, on
//     the subscribe side (the ONLY side DeadLetter applies to for an
//     upstream-pipeline-style error — see PublishAdapter's own scope note
//     in demoErrorChannelDeadLetterFallback's doc comment).
//  6. demoErrorChannelMiddlewareCombo — ErrorChannel matching a SECURITY
//     MIDDLEWARE Fn failure (auto-wrapped in events.SecurityError), not
//     just a subscribe-handler failure. SUBSCRIBE side only, BY DESIGN —
//     see this function's own doc comment for why no publish-side
//     equivalent exists (a documented Category-C scope boundary, not a
//     gap).

// errorPatternMatchSpy proves H2's fix (session review round-5 finding):
// PublishAdapter.Activate's handleUpstreamError closure previously
// hand-rolled its own events.ErrorChannel dispatch, silently skipping
// stats.ErrorPatternObserver observability — now it delegates to the SAME
// tryPublishErrorChannel helper the subscribe side already used, so
// RecordErrorPatternMatch fires here too.
type errorPatternMatchSpy struct {
	stats.NoopObserver
	matches int
}

func (s *errorPatternMatchSpy) RecordErrorPatternMatch(_, _, _ string) {
	s.matches++
}

// demoErrorChannelPublishSide demonstrates the pub/sub analogue of
// rest.ErrorPattern on the PUBLISH side: a declared events.ErrorChannel on
// routes.ReadingsWithErrorsChannel causes mqtt5.PublishAdapter to publish
// a typed error payload to a dedicated error-output topic whenever a
// matching UPSTREAM PIPELINE error reaches it — instead of only calling
// MQTT5DrainPublishOptions.OnError.
func demoErrorChannelPublishSide(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel — publish side (upstream pipeline error) ---")

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
	spy := &errorPatternMatchSpy{}
	port.Bind(ctx, mqtt5adapter.PublishAdapter(broker, handle, format.JSON(routes.SensorReadingCodec),
		mqtt5adapter.MQTT5DrainPublishOptions{
			Vars:     map[string]string{"sensorID": sensorID},
			Observer: spy,
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
	if spy.matches == 1 {
		fmt.Println("  ✓ stats.ErrorPatternObserver.RecordErrorPatternMatch fired for the upstream error (H2 fix)")
	} else {
		fmt.Printf("  ✗ expected 1 RecordErrorPatternMatch call, got %d\n", spy.matches)
	}
	fmt.Println()
}

// demoErrorChannelSubscribeSideAndConsumer demonstrates the OTHER
// Category-A events.ErrorChannel dispatch point — a SUBSCRIBE-side
// HANDLER (fn) business error, as opposed to the publish-side upstream
// error above. It also completes the loop end-to-end: the SAME mock
// broker auto-dispatches the published error payload to a second
// subscriber (routes.SensorErrorTopicSub), proving a downstream consumer
// decodes the typed error notification exactly like any other declared
// message — pub/sub's natural analogue to REST/reqreply's client-side
// errors.As recovery (no special decode step needed).
func demoErrorChannelSubscribeSideAndConsumer(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel — subscribe-side handler error + consumer decode ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4d: ONE
	// api-layer-owned Client, attached once, used for every Subscribe
	// below — no adapter-specific NewSubscribeTransport escape hatch
	// needed. (A prior attempt at this conversion hit a real, now-fixed
	// MockRouter.WaitHandler bug — see that function's own doc comment
	// — that masked handler-registration latency behind an up-to-1s
	// worst-case wait, racing handleCtx's own short lifetime.)
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Error channel demo", Version: "1.0.0"}))
	if err := evtClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}
	errorSub := routes.SensorErrorTopicSub.WithOptions(mqtt5adapter.SubscribeOptions{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
	})
	dataSub := routes.ReadingsWithErrorsSub.WithOptions(mqtt5adapter.SubscribeOptions{
		Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
	})

	handleCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	// ── Consumer: subscribes to the error-output topic as an ORDINARY
	// typed channel — no errors.As, no special decode step, just the
	// SAME declarative Subscribe mechanism as any other message.
	received := make(chan routes.SensorErrorPayload, 1)
	go func() {
		_ = evtClient.Subscribe(handleCtx, errorSub,
			func(_ context.Context, payload routes.SensorErrorPayload) error {
				received <- payload
				return nil
			})
	}()
	router.WaitHandler("sensors/{sensorID}/readings/errors")

	// ── Publisher-of-error: subscribes to the DATA topic; its handler
	// decodes successfully then returns SensorOutOfRangeError as its OWN
	// business error — the declared ErrorChannel matches it and publishes
	// the typed payload to the error topic (which the consumer above is
	// already listening on).
	go func() {
		_ = evtClient.Subscribe(handleCtx, dataSub,
			func(_ context.Context, r routes.SensorReading) error {
				return routes.SensorOutOfRangeError(r)
			})
	}()
	router.WaitHandler("sensors/{sensorID}/readings")

	router.Dispatch(&pahomqtt5.Publish{
		Topic:   "sensors/" + sensorID + "/readings",
		Payload: []byte(fmt.Sprintf(`{"sensor_id":%q,"value":999.9}`, sensorID)),
	})

	select {
	case payload := <-received:
		fmt.Printf("  ✓ handler business error → typed payload published → consumer decoded it: code=%q message=%q\n",
			payload.Code, payload.Message)
	case <-time.After(500 * time.Millisecond):
		fmt.Println("  ✗ timed out waiting for consumer to receive the typed error payload")
	}
	fmt.Println()
}

// demoErrorChannelDirectMode demonstrates events.ErrorChannel's DIRECT
// mode (no mapFn — routes.SensorMaintenanceError itself IS the payload) —
// the 2nd of events' 2 declaration mechanisms — on BOTH the SUBSCRIBE
// side (a handler business error) AND the PUBLISH side (an upstream
// pipeline error), mirroring how Mapped mode is demoed on both sides
// above (demoErrorChannelPublishSide/demoErrorChannelSubscribeSideAndConsumer).
func demoErrorChannelDirectMode(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel — Direct mode (subscribe side) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4d: ONE
	// api-layer-owned Client, attached once.
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Direct mode demo", Version: "1.0.0"}))
	if err := evtClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}

	received := make(chan routes.SensorMaintenanceError, 1)
	errorTopicSub := events.NewChannel[routes.SensorMaintenanceError](
		"sensors/{sensorID}/maintenance-demo/errors", routes.SensorMaintenanceErrorCodec,
		events.TopicParam{Name: "sensorID"},
	).WithSubscribe(events.Subscribe{OperationID: "receiveMaintenanceError"}).
		WithOptions(mqtt5adapter.SubscribeOptions{Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce}})
	go func() {
		_ = evtClient.Subscribe(handleCtx, errorTopicSub,
			func(_ context.Context, e routes.SensorMaintenanceError) error {
				received <- e
				return nil
			})
	}()
	router.WaitHandler("sensors/{sensorID}/maintenance-demo/errors")

	dataSub := routes.MaintenanceSub.WithOptions(mqtt5adapter.SubscribeOptions{Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce}})
	go func() {
		_ = evtClient.Subscribe(handleCtx, dataSub,
			func(_ context.Context, r routes.SensorReading) error {
				return routes.SensorMaintenanceError{SensorID: r.SensorID}
			})
	}()
	router.WaitHandler("sensors/{sensorID}/maintenance-demo")

	router.Dispatch(&pahomqtt5.Publish{
		Topic:   "sensors/" + sensorID + "/maintenance-demo",
		Payload: []byte(fmt.Sprintf(`{"sensor_id":%q,"value":1.0}`, sensorID)),
	})

	select {
	case e := <-received:
		fmt.Printf("  ✓ Direct mode (subscribe side): E itself IS B, no mapFn — sensor_id=%q\n", e.SensorID)
	case <-time.After(300 * time.Millisecond):
		fmt.Println("  ✗ timed out waiting for Direct-mode error payload (subscribe side)")
	}
	fmt.Println()

	demoErrorChannelDirectModePublishSide(ctx)
}

// demoErrorChannelDirectModePublishSide is Direct mode's PUBLISH-side
// counterpart to demoErrorChannelDirectMode's subscribe-side demo above —
// an UPSTREAM PIPELINE error (routes.SensorMaintenanceError) reaches
// mqtt5.PublishAdapter bound to routes.MaintenancePub, proving Direct
// mode is consulted on this side too, not just subscribe.
func demoErrorChannelDirectModePublishSide(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel — Direct mode (publish side) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Direct mode publish demo", Version: "1.0.0"}))
	handle, err := routes.MaintenancePub.Handle(evtClient)
	if err != nil {
		fmt.Printf("  [error] register: %v\n", err)
		return
	}
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	port, err := ports.NewSinkPort[routes.SensorReading]("maintenance-direct-publish", routes.SensorReadingCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		fmt.Printf("  [error] construct port: %v\n", err)
		return
	}
	var onErrorCalled bool
	port.Bind(ctx, mqtt5adapter.PublishAdapter(broker, handle, format.JSON(routes.SensorReadingCodec),
		mqtt5adapter.MQTT5DrainPublishOptions{
			Vars:    map[string]string{"sensorID": sensorID},
			OnError: func(e error) { onErrorCalled = true },
		}))

	errCh := make(chan error, 1)
	valCh := make(chan routes.SensorReading)
	errCh <- routes.SensorMaintenanceError{SensorID: sensorID}
	close(errCh)
	close(valCh)
	port.Feed(ctx, gstream.Stream[routes.SensorReading]{Values: valCh, Errors: errCh})

	time.Sleep(30 * time.Millisecond)
	published := len(broker.PublishedSnapshot()) > 0
	if published && !onErrorCalled {
		fmt.Println("  ✓ Direct mode (publish side): upstream SensorMaintenanceError → typed payload published (OnError NOT called)")
	} else {
		fmt.Printf("  ✗ Direct mode (publish side): published=%v onErrorCalled=%v (unexpected)\n", published, onErrorCalled)
	}
	fmt.Println()
}

// demoErrorChannelActions shows the 3 ErrorActions on 3 otherwise-identical
// sibling channels: ErrorRespond (default, typed publish), ErrorHandle
// (OnError callback runs instead, no publish), ErrorLog (adapter's normal
// error-forwarding path only, no publish) — on BOTH the PUBLISH side
// (upstream pipeline error) and the SUBSCRIBE side (handler business
// error), mirroring how Mapped mode is demoed on both sides.
func demoErrorChannelActions(ctx context.Context) {
	demoErrorChannelActionsPublishSide(ctx)
	demoErrorChannelActionsSubscribeSide(ctx)
}

// demoErrorChannelActionsPublishSide exercises the 3 ErrorActions via
// PublishAdapter's upstream-pipeline-error path.
func demoErrorChannelActionsPublishSide(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel — 3 ErrorActions (publish side) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Actions demo", Version: "1.0.0"}))
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	run := func(label string, pub events.Publisher[routes.SensorReading]) {
		handle, err := pub.Handle(evtClient)
		if err != nil {
			fmt.Printf("  [error] register %s: %v\n", label, err)
			return
		}
		var onErrorCalled bool
		var published bool
		port, err := ports.NewSinkPort[routes.SensorReading](label, routes.SensorReadingCodec, ports.PortOptions{Buffer: 4})
		if err != nil {
			fmt.Printf("  [error] construct port %s: %v\n", label, err)
			return
		}
		snapshotBefore := len(broker.PublishedSnapshot())
		port.Bind(ctx, mqtt5adapter.PublishAdapter(broker, handle, format.JSON(routes.SensorReadingCodec),
			mqtt5adapter.MQTT5DrainPublishOptions{
				Vars:    map[string]string{"sensorID": sensorID},
				OnError: func(e error) { onErrorCalled = true },
			}))
		errCh := make(chan error, 1)
		valCh := make(chan routes.SensorReading)
		errCh <- routes.SensorOutOfRangeError{SensorID: sensorID, Value: 999.9}
		close(errCh)
		close(valCh)
		port.Feed(ctx, gstream.Stream[routes.SensorReading]{Values: valCh, Errors: errCh})
		time.Sleep(30 * time.Millisecond)
		published = len(broker.PublishedSnapshot()) > snapshotBefore
		fmt.Printf("  %s: published=%v onErrorCalled=%v\n", label, published, onErrorCalled)
	}

	run("ErrorRespond (default, typed publish expected)", routes.ActionRespondPub)
	run("ErrorHandle (no publish, OnError expected)", routes.ActionHandlePub)
	run("ErrorLog (no publish, OnError expected — same fallback as Handle)", routes.ActionLogPub)
	fmt.Println()
}

// demoErrorChannelActionsSubscribeSide exercises the SAME 3 ErrorActions
// via a SUBSCRIBE-side HANDLER business error, the counterpart missing
// from the publish-side-only demo above until this review round.
func demoErrorChannelActionsSubscribeSide(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel — 3 ErrorActions (subscribe side) ---")
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	run := func(label string, sub events.Subscriber[routes.SensorReading], topicSuffix string) {
		router := mqtt5broker.NewMockRouter()
		broker := mqtt5broker.NewMockBroker(router)
		handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()

		// Converted to Client.Attach+Client.Subscribe (docs/roadmap/
		// d-0006-protocol-native-capabilities.md's Phase 4e closed the
		// gap this demo used to document — Client.Subscribe's
		// reflection shim now calls a declared SubscribeOptions.OnError
		// callback, same as the escape hatch always did).
		var onErrorCalled bool
		evClient := events.NewClient(events.WithInfo(events.Info{Title: "Error pattern demo", Version: "1.0.0"}))
		if err := evClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
			fmt.Printf("  %s: Attach error: %v\n", label, err)
			return
		}
		sub = sub.WithOptions(mqtt5adapter.SubscribeOptions{
			Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce},
			OnError:      func(mqtt5adapter.SubscribeError) { onErrorCalled = true },
		})
		go func() {
			_ = evClient.Subscribe(handleCtx, sub,
				func(_ context.Context, _ routes.SensorReading) error {
					return routes.SensorOutOfRangeError{SensorID: sensorID, Value: 999.9}
				})
		}()
		router.WaitHandler("sensors/{sensorID}/" + topicSuffix)

		before := len(broker.PublishedSnapshot())
		router.Dispatch(&pahomqtt5.Publish{
			Topic:   "sensors/" + sensorID + "/" + topicSuffix,
			Payload: []byte(fmt.Sprintf(`{"sensor_id":%q,"value":999.9}`, sensorID)),
		})
		time.Sleep(50 * time.Millisecond)
		published := len(broker.PublishedSnapshot()) > before
		fmt.Printf("  %s: published=%v onErrorCalled=%v\n", label, published, onErrorCalled)
	}

	run("ErrorRespond (default, typed publish expected)", routes.ActionRespondSub, "action-respond-demo")
	run("ErrorHandle (no publish, OnError expected)", routes.ActionHandleSub, "action-handle-demo")
	run("ErrorLog (no publish, OnError expected — same fallback as Handle)", routes.ActionLogSub, "action-log-demo")
	fmt.Println()
}

// demoErrorChannelDeadLetterFallback demonstrates the two-tier fallback
// (Topic 4) on the SUBSCRIBE side — the mechanism tryDeadLetter is wired
// into: routes.ReadingsWithErrorsChannel declares BOTH an
// events.ErrorChannel (matches SensorOutOfRangeError) AND an
// events.DeadLetter (catches anything else). The subscribe handler
// returns SensorOutOfRangeError for one message (matched → typed
// ErrorChannel publish) and SensorOfflineError for another (a type the
// ErrorChannel does NOT declare — a genuine miss, falls through to the
// dead-letter topic instead). NOTE: PublishAdapter's own upstream-
// pipeline-error path (see demoErrorChannelPublishSide) does NOT
// dead-letter — there is no raw payload to dead-letter once the pipeline
// has already replaced the value with an error (see H2's own scope note)
// — DeadLetter only applies to subscribe-side/encode-failure paths.
func demoErrorChannelDeadLetterFallback(ctx context.Context) {
	fmt.Println("--- Demo: events.DeadLetter — two-tier fallback (matched vs unmatched, subscribe side) ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	// nextErr controls which business error the handler returns per
	// dispatch, alternating between a MATCHED and an UNMATCHED type.
	var nextErr error
	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4d: the
	// api-layer-owned Client.Attach workflow — no adapter-specific
	// NewSubscribeTransport escape hatch needed for this plain-Capabilities
	// demo.
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Dead-letter demo", Version: "1.0.0"}))
	if err := evtClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}
	dataSub := routes.ReadingsWithErrorsSub.WithOptions(mqtt5adapter.SubscribeOptions{Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce}})
	go func() {
		_ = evtClient.Subscribe(handleCtx, dataSub,
			func(_ context.Context, _ routes.SensorReading) error {
				return nextErr
			})
	}()
	router.WaitHandler("sensors/{sensorID}/readings")

	dispatch := func(label string, businessErr error) {
		nextErr = businessErr
		before := len(broker.PublishedSnapshot())
		router.Dispatch(&pahomqtt5.Publish{
			Topic:   "sensors/" + sensorID + "/readings",
			Payload: []byte(fmt.Sprintf(`{"sensor_id":%q,"value":999.9}`, sensorID)),
		})
		time.Sleep(50 * time.Millisecond)
		after := broker.PublishedSnapshot()
		if len(after) <= before {
			fmt.Printf("  ✗ %s: expected a publish, got none\n", label)
			return
		}
		fmt.Printf("  %s → published to %q\n", label, after[len(after)-1].Topic)
	}

	dispatch("MATCHED (SensorOutOfRangeError, declared ErrorChannel)", routes.SensorOutOfRangeError{SensorID: sensorID, Value: 999.9})
	dispatch("UNMATCHED (SensorOfflineError, no declared ErrorChannel → DeadLetter)", routes.SensorOfflineError{SensorID: sensorID})
	fmt.Println()
}

// demoErrorChannelMiddlewareCombo proves events.ErrorChannel intercepts a
// SECURITY-MIDDLEWARE Fn failure — the security Fn below ALWAYS rejects,
// and the adapter auto-wraps its returned error in events.SecurityError,
// which is matched by routes.SecuredReadingsChannel's declared
// ErrorChannel BEFORE the subscribe handler ever runs.
//
// SUBSCRIBE side ONLY, BY DESIGN — there is no publish-side equivalent to
// demo: adapters/mqtt5's publish() function calls runPublishSecurityImpls
// (the PublishMW-attached credential Fn) and, on failure, returns that
// error DIRECTLY, with no ErrorChannel consultation at that call site.
// This is NOT an oversight — it's a DELIBERATE, already-documented scope
// boundary (docs/design/d-0005-error-handling.md's Topic 4
// "Failed publish" discussion): a publish-side ClientImplementations
// security-Fn rejection is a PRE-TRANSMISSION validation/authorization
// failure of the caller's OWN outgoing message — structurally closer to
// REST's client-side credential validation (Category C, permanently
// excluded from Category-A coverage) than to a message that entered
// dispatch and then failed. The SUBSCRIBE side's security Fn failure is
// eligible because it rejects an INCOMING message already in dispatch —
// not a symmetric case, so no publish-side demo is added here.
func demoErrorChannelMiddlewareCombo(ctx context.Context) {
	fmt.Println("--- Demo: events.ErrorChannel + SubscribeMW security combo ---")

	router := mqtt5broker.NewMockRouter()
	broker := mqtt5broker.NewMockBroker(router)
	sensorID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

	handleCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	received := make(chan routes.SecurityRejectedPayload, 1)
	// docs/design/d-0006-protocol-native-capabilities.md's Phase 4d: the
	// error-topic CONSUMER doesn't need SubscribeMW/security, so it's
	// safely converted to the api-layer-owned Client.Attach workflow.
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Middleware combo demo", Version: "1.0.0"}))
	if err := evtClient.Attach(mqtt5adapter.NewTransport(mqtt5adapter.TransportOptions{Client: broker, Router: router})); err != nil {
		fmt.Printf("  [error] Attach: %v\n", err)
		return
	}
	errorTopicSub := events.NewChannel[routes.SecurityRejectedPayload](
		"sensors/{sensorID}/secured-errorchannel-demo/errors", routes.SecurityRejectedPayloadCodec,
		events.TopicParam{Name: "sensorID"},
	).WithSubscribe(events.Subscribe{OperationID: "receiveSecurityRejected"}).
		WithOptions(mqtt5adapter.SubscribeOptions{Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce}})
	go func() {
		_ = evtClient.Subscribe(handleCtx, errorTopicSub,
			func(_ context.Context, p routes.SecurityRejectedPayload) error {
				received <- p
				return nil
			})
	}()
	router.WaitHandler("sensors/{sensorID}/secured-errorchannel-demo/errors")

	alwaysRejectFn := func(_ context.Context, _ *pahomqtt5.Publish, _ *routes.SensorReading) (map[string][]string, error) {
		return nil, errors.New("access denied for demo")
	}
	securedSub := routes.SecuredReadingsSub.Use(routes.APIKeyAuthMW).SubscribeMW(&routes.APIKeyAuthMW, alwaysRejectFn).
		WithOptions(mqtt5adapter.SubscribeOptions{Capabilities: []mqtt5adapter.Capability{mqtt5adapter.QoSAtLeastOnce}})
	// Converted to Client.Attach+Client.Subscribe (docs/roadmap/
	// d-0006-protocol-native-capabilities.md's Phase 4e closed the gap
	// this demo used to document — Client.Subscribe's reflection shim
	// now runs Implementations-based SubscribeMW security enforcement,
	// same as the escape hatch always did). Reuses the SAME evtClient/
	// attached transport as the error-topic consumer above — one
	// Client.Attach, many Client.Subscribe calls.
	go func() {
		_ = evtClient.Subscribe(handleCtx, securedSub,
			func(_ context.Context, _ routes.SensorReading) error {
				fmt.Println("  ✗ subscribe handler ran (unexpected — security Fn should have rejected first)")
				return nil
			})
	}()
	router.WaitHandler("sensors/{sensorID}/secured-errorchannel-demo")

	router.Dispatch(&pahomqtt5.Publish{
		Topic:   "sensors/" + sensorID + "/secured-errorchannel-demo",
		Payload: []byte(fmt.Sprintf(`{"sensor_id":%q,"value":1.0}`, sensorID)),
	})

	select {
	case payload := <-received:
		fmt.Printf("  ✓ security-middleware Fn error (events.SecurityError) matched by ErrorChannel: code=%q\n", payload.Code)
	case <-time.After(300 * time.Millisecond):
		fmt.Println("  ✗ timed out waiting for security-rejected error payload")
	}
	fmt.Println()
}
