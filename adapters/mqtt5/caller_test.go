package mqtt5

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── caller / newCaller ────────────────────────────────────────────────────────

// var _ events.SubscriberServer = (*caller)(nil) is also asserted in
// caller.go itself — repeated here as a compile-time interface-compliance
// check colocated with this file's tests.
var _ events.SubscriberServer = (*caller)(nil)

func TestNewCaller_NilEventsClient_IsSpecFree(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	caller := newCaller(client, router, nil)

	// ServeSubscribers on a spec-free caller (nil events client) must be a
	// safe, immediate no-op — nothing to walk.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("unexpected error on spec-free ServeSubscribers: %v", err)
	}
}

// ── Subscribe (value-based) parity with SubscribeWithHandle ──────────────────

func TestSubscribe_ValueBased_DeliversMessage(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	caller := newCaller(client, router, nil)

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{Summary: "test"})
	_ = b

	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribe(ctx, caller, sub, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.SensorID == "" {
		t.Fatal("expected message delivered to handler")
	}
}

// ── Bug-fix regression: templated topic derives a wildcard broker filter ─────

func TestSubscribe_TopicFilter_DerivesWildcardFromTemplate(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(context.Context, sensorReading) error { return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("SubscribeWithHandle: %v", err)
	}

	// The mock BROKER (not just the router) must have received the
	// DERIVED WILDCARD FILTER, not the raw "{sensorID}" template — this is
	// the filter-checking assertion the wildcard bug fix requires; a
	// permissive mock that ignores the filter value would let this pass
	// even with the bug present, so we assert on the actual filter string
	// captured by mockClient.subscribedFilters().
	filters := client.subscribedFilters()
	if len(filters) != 1 || filters[0] != "sensors/+/readings" {
		t.Fatalf("want broker filter %q, got %v", "sensors/+/readings", filters)
	}
	if len(filters) > 0 && filters[0] == "sensors/{sensorID}/readings" {
		t.Fatal("regression: raw {sensorID} template sent VERBATIM as broker filter")
	}
}

func TestSubscribe_TopicFilter_ExplicitOverrideWins(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(context.Context, sensorReading) error { return nil },
		SubscribeOptions{TopicFilter: "sensors/#"}); err != nil {
		t.Fatalf("SubscribeWithHandle: %v", err)
	}

	filters := client.subscribedFilters()
	if len(filters) != 1 || filters[0] != "sensors/#" {
		t.Fatalf("want explicit override filter %q, got %v", "sensors/#", filters)
	}
}

// ── ServeSubscribers ───────────────────────────────────────────────────────────

func TestServeSubscribers_MultiRoute_Dispatch(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	chA := events.NewChannel[sensorReading]("sensors/a", sensorCodec)
	chB := events.NewChannel[sensorReading]("sensors/b", sensorCodec)

	var gotA, gotB sensorReading
	subA := chA.WithSubscribe(events.Subscribe{}).WithHandler(
		func(_ context.Context, r sensorReading) error { gotA = r; return nil })
	subB := chB.WithSubscribe(events.Subscribe{}).WithHandler(
		func(_ context.Context, r sensorReading) error { gotB = r; return nil })

	if err := subA.Register(evtClient); err != nil {
		t.Fatalf("Register A: %v", err)
	}
	if err := subB.Register(evtClient); err != nil {
		t.Fatalf("Register B: %v", err)
	}

	caller := newCaller(client, router, evtClient)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	router.waitHandler("sensors/a")
	router.waitHandler("sensors/b")

	router.dispatch("sensors/a", &pahomqtt5.Publish{Topic: "sensors/a", Payload: []byte(validSensorJSON)})
	router.dispatch("sensors/b", &pahomqtt5.Publish{Topic: "sensors/b", Payload: []byte(validSensorJSON)})

	// Give both goroutines a moment to process before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeSubscribers returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeSubscribers did not return after ctx cancellation")
	}

	if gotA.SensorID == "" {
		t.Error("expected route A handler invoked")
	}
	if gotB.SensorID == "" {
		t.Error("expected route B handler invoked")
	}
}

func TestServeSubscribers_HandlerOpts_QoSDispatch(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	ch := events.NewChannel[sensorReading]("sensors/qos", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions(SubscribeOptions{QoS: 2})

	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	router.waitHandler("sensors/qos")
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.subscribed) != 1 || len(client.subscribed[0].Subscriptions) != 1 {
		t.Fatalf("expected exactly 1 subscription, got %+v", client.subscribed)
	}
	if got := client.subscribed[0].Subscriptions[0].QoS; got != 2 {
		t.Errorf("want QoS 2 from HandlerOpts dispatch, got %d", got)
	}
}

func TestServeSubscribers_WrongHandlerOptsType_ReturnsOptionsShapeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	ch := events.NewChannel[sensorReading]("sensors/badopts", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions("not-a-SubscribeOptions")

	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	err := caller.ServeSubscribers(context.Background())
	var shapeErr OptionsShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want OptionsShapeError, got %v", err)
	}
}

func TestServeOneSubscriber_Shortcut(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	caller := newCaller(client, router, nil)

	ch := events.NewChannel[sensorReading]("sensors/one", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{})

	var received sensorReading
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveOneSubscriber(ctx, caller, sub, 1,
			func(_ context.Context, r sensorReading) error { received = r; return nil },
			SubscribeOptions{})
	}()

	router.waitHandler("sensors/one")
	router.dispatch("sensors/one", &pahomqtt5.Publish{Topic: "sensors/one", Payload: []byte(validSensorJSON)})
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveOneSubscriber returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveOneSubscriber did not return after ctx cancellation")
	}

	if received.SensorID == "" {
		t.Error("expected handler invoked")
	}
}

// ── Eager Fn-shape validation ────────────────────────────────────────────────

func TestSubscribeMW_WrongShape_ReturnsMiddlewareShapeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, func() {}) // wrong shape entirely

	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	err = subscribeWithHandle(context.Background(), client, router, handle, 1,
		func(context.Context, sensorReading) error { return nil },
		SubscribeOptions{})
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v", err)
	}
}

func TestPublishMW_WrongShape_ReturnsMiddlewareShapeError(t *testing.T) {
	client := &mockClient{}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	pub := ch.WithPublish(events.Publish{}).
		PublishMW(nil, func() {}) // wrong shape entirely

	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err = publish(context.Background(), client, handle, 1, false, reading, nil, PublishOptions[sensorReading]{})
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v", err)
	}
}

func TestServeSubscribers_WrongImplShape_ReturnsMiddlewareShapeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	ch := events.NewChannel[sensorReading]("sensors/badshape", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, func() {}). // wrong shape entirely
		WithHandler(func(context.Context, sensorReading) error { return nil })

	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	err := caller.ServeSubscribers(context.Background())
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v", err)
	}
}

func TestSubscribeMW_GeneralShape_WrapsHandler(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	var wrapped bool
	generalMW := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			wrapped = true
			return next(ctx, r)
		}
	}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).SubscribeMW(nil, generalMW)
	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("SubscribeWithHandle: %v", err)
	}
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	if !wrapped {
		t.Error("expected general-purpose SubscribeMW Fn to wrap the handler")
	}
	if received.SensorID == "" {
		t.Error("expected handler still invoked through the wrap")
	}
}

func TestPublishMW_SecurityShape_WritesIntoPayload(t *testing.T) {
	client := &mockClient{}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	pub := ch.WithPublish(events.Publish{Security: []route.SecurityRequirement{route.Require("bearer")}}).
		PublishMW(nil, // unpaired, general-purpose — runs unconditionally
			func(_ context.Context, msg *sensorReading, _ []route.SecurityRequirement) ([]UserProperty, error) {
				msg.SensorID = "f47ac10b-58cc-4372-a567-0e02b2c3d479" // in-payload write
				return nil, nil
			})

	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "00000000-0000-0000-0000-000000000000", Value: 1.0}
	if err := publish(context.Background(), client, handle, 1, false, reading, nil, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	pub2 := client.lastPublished()
	if pub2 == nil {
		t.Fatal("expected a published message")
	}
	if got := string(pub2.Payload); !contains(got, "f47ac10b-58cc-4372-a567-0e02b2c3d479") {
		t.Errorf("want payload to contain the in-payload-written credential, got %s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// ── Observability ──────────────────────────────────────────────────────────────

func TestObservability_Subscribe_RecordsSuccessAndFailure(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	obs := &testObserver{}

	// Observability's own general-purpose shape
	// (func(next func(context.Context, T) error) func(context.Context, T) error)
	// is attached directly via SubscribeMW — [wrapSubscribeGeneral] wraps
	// the REAL handler with it internally.
	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, Observability[sensorReading]("sensors/readings", obs))
	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(context.Context, sensorReading) error { return nil },
		// NOTE: opts.Observer intentionally left nil here — Observability's
		// own obs (closed over above) is independent of opts.Observer;
		// both fire, so obs.subscribes accumulates from Observability's
		// wrap only (opts.Observer nil => ctx-resolved NoopObserver for
		// the OUTER RecordSubscribe call, which does not append to obs).
		SubscribeOptions{}); err != nil {
		t.Fatalf("SubscribeWithHandle: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	if len(obs.subscribes) != 2 {
		t.Fatalf("want 2 RecordSubscribe calls (from the general-purpose Observability MW), got %d", len(obs.subscribes))
	}
	for i, ok := range obs.subscribes {
		if !ok {
			t.Errorf("call %d: want success, got failure", i)
		}
	}
}

func TestObservability_NilObserver_FallsBackToCtxInjection_NoPanic(t *testing.T) {
	obs := &testObserver{}
	ctx := stats.WithObserver(context.Background(), obs)

	mw := Observability[sensorReading]("sensors/readings", stats.ObserverFromContext(ctx))
	wrapped := mw(func(context.Context, sensorReading) error { return nil })

	if err := wrapped(ctx, sensorReading{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs.publishes) != 1 {
		t.Fatalf("want 1 RecordPublish (no *pahomqtt5.Publish in ctx => publish direction), got %d", len(obs.publishes))
	}
}

// ── Example (pkg.go.dev documentation) ────────────────────────────────────────

// ExampleSubscribe demonstrates the value-based caller/Subscribe workflow:
// build a caller bundling an already-connected client+router with an
// optional events.Client registry, declare a Subscriber, and hand it plus a
// handler func straight to Subscribe — no manual makeSubscribeMessageHandler-
// plus-router.RegisterHandler wiring required.
func ExampleSubscribe() {
	client := &mockClient{}
	router := newMockRouter()
	caller := newCaller(client, router, nil) // nil = no AsyncAPI spec

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{Summary: "Sensor readings"})

	ctx := context.Background()
	err := subscribe(ctx, caller, sub, 1,
		func(_ context.Context, r sensorReading) error {
			fmt.Printf("received reading from %s: %.1f\n", r.SensorID, r.Value)
			return nil
		}, SubscribeOptions{})
	if err != nil {
		fmt.Println("subscribe error:", err)
		return
	}

	// Simulate the broker delivering one message (a real broker connection
	// would invoke this automatically once subscribed).
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	// Output:
	// received reading from f47ac10b-58cc-4372-a567-0e02b2c3d479: 22.5
}
