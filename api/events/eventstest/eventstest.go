package eventstest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// SensorReading is the canonical message type [AssertPublishSubscribeRoundTrip]
// publishes and subscribes — deliberately the SAME shape
// (SensorID/Value) already used identically, independently, across
// adapters/mqtt5's, adapters/mqtt's, and adapters/zeromq's own test
// fixtures, so a conforming adapter's test author has nothing new to
// learn about the message shape itself.
type SensorReading struct {
	SensorID string
	Value    float64
}

var sensorReadingCodec = codex.Struct[SensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		func(r SensorReading) string { return r.SensorID },
		func(r *SensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64(),
		func(r SensorReading) float64 { return r.Value },
		func(r *SensorReading, v float64) { r.Value = v },
	),
)

// SensorTopic is the topic [AssertPublishSubscribeRoundTrip]'s canonical
// channel publishes/subscribes on — namespaced under "eventstest/" to
// avoid colliding with any adapter's own test-fixture topics (e.g.
// "sensors/readings").
const SensorTopic = "eventstest/sensor/reading"

// NewSensorChannel is the canonical, undecorated channel declaration
// every conforming events adapter must correctly publish and subscribe.
func NewSensorChannel() events.Channel[SensorReading] {
	return events.NewChannel[SensorReading](SensorTopic, sensorReadingCodec)
}

// AssertPublishSubscribeRoundTrip proves client (with a transport already
// [events.Client.Attach]ed) correctly delivers a message published via
// the real [events.Client.Publish] encode path to a subscriber registered
// via [events.Client.ServeSubscribers], with the decoded value intact.
//
// isReady and deliver are the ONLY adapter-specific pieces this function
// needs — every events adapter's own mock-broker test harness implements
// these two steps differently (e.g. adapters/mqtt5's
// mockRouter.hasHandler/dispatch vs. adapters/mqtt's
// subscribedHandlerSnapshot/direct field reads), and a shared package
// cannot unify them without assuming one adapter's internal mock shape:
//
//   - isReady reports whether the subscriber's handler is registered on
//     the adapter's mock broker yet (ServeSubscribers runs in a
//     background goroutine — registration is asynchronous). Polled with
//     a short sleep loop, mirroring the SAME idiom every adapter's own
//     test suite already uses internally.
//   - deliver is called once, immediately after Publish, and must move
//     the REAL message [events.Client.Publish] just encoded (read back
//     from the adapter's own mock client, e.g. `client.lastPublished()`)
//     into the adapter's own mock-dispatch mechanism (e.g.
//     `router.dispatch(topic, msg)`) — proving Publish's real encode
//     path reaches Subscribe's real decode path, not a hand-crafted
//     payload bypassing one half of the round trip.
func AssertPublishSubscribeRoundTrip(t *testing.T, client *events.Client, isReady func() bool, deliver func()) {
	t.Helper()

	var mu sync.Mutex
	var got SensorReading
	var handlerCalled bool
	sub := NewSensorChannel().WithSubscribe(events.Subscribe{}).WithHandler(func(_ context.Context, msg SensorReading) error {
		mu.Lock()
		got = msg
		handlerCalled = true
		mu.Unlock()
		return nil
	})
	if err := sub.Register(client); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.ServeSubscribers(ctx) }()

	deadline := time.Now().Add(time.Second)
	for !isReady() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to be registered (isReady never returned true)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	want := SensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 42.5}
	pub := NewSensorChannel().WithPublish(events.Publish{})
	if err := client.Publish(ctx, pub, want); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	deliver()

	deadline = time.Now().Add(time.Second)
	for {
		mu.Lock()
		called := handlerCalled
		mu.Unlock()
		if called {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to run after deliver()")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if got != want {
		t.Errorf("subscriber received %+v, want %+v (the exact value published)", got, want)
	}
}
