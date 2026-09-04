package mqtt5

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// ── Client.Attach/Publish/Subscribe (Decision 5) ────────────────────────────

// mergeSensorChannel declares sensorReading's SensorID as a merge-capable
// topic param, so Client.Publish's reflection shim can exercise the
// EncodeVars/BuildTopic path end-to-end ("one struct, one call").
func mergeSensorChannel(topic string) events.Channel[sensorReading] {
	return events.NewChannel[sensorReading](topic, sensorCodec,
		events.NewTopicParam("sensorID", codex.String(),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v },
		),
	)
}

func plainSensorChannel(topic string) events.Channel[sensorReading] {
	return events.NewChannel[sensorReading](topic, sensorCodec)
}

func TestAttach_ClientPublish_RoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := client.lastPublished()
	if got == nil {
		t.Fatal("want 1 published message, got none")
	}
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if got.Topic != wantTopic {
		t.Errorf("topic = %q, want %q (EncodeVars/BuildTopic derivation failed)", got.Topic, wantTopic)
	}
}

func TestAttach_ClientPublish_WrongPubType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := c.Publish(context.Background(), "not-a-publisher", sensorReading{})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientPublish_WrongMsgType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	pub := plainSensorChannel("sensors/readings").WithPublish(events.Publish{})
	err := c.Publish(context.Background(), pub, "wrong-type")
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientSubscribe_RoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	received := make(chan sensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, r sensorReading) error {
			received <- r
			return nil
		})
	}()

	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if err := <-done; err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected non-empty SensorID")
		}
	default:
		t.Fatal("expected message to be delivered via Client.Subscribe")
	}
}

// TestAttach_ClientSubscribe_RegistersSpecIntoRealClient mirrors
// adapters/zeromq's identical regression test.
func TestAttach_ClientSubscribe_RegistersSpecIntoRealClient(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{OperationID: "receiveReading"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Subscribe(ctx, sub, func(context.Context, sensorReading) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	doc, err := c.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	out := string(yamlBytes)
	for _, want := range []string{"sensors/readings:", "receiveReading", "action: receive"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected client.AsyncAPISpec() to include the subscribe channel/operation; missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestAttach_ClientSubscribe_WrongSubType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := c.Subscribe(context.Background(), "not-a-subscriber", func() {})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

// ── NewPublishTransport/NewSubscribeTransport (Decision 7) ─────────────────

func TestNewPublishTransport_PublishHandle_RoundTrip(t *testing.T) {
	client := &mockClient{}
	transport := NewPublishTransport[sensorReading](client, 1, false, PublishOptions[sensorReading]{})

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := events.PublishHandle(context.Background(), pub, transport, reading); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}

	got := client.lastPublished()
	if got == nil {
		t.Fatal("want 1 published message, got none")
	}
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if got.Topic != wantTopic {
		t.Errorf("topic = %q, want %q", got.Topic, wantTopic)
	}
}

func TestNewSubscribeTransport_SubscribeHandle_RoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	subTransport := NewSubscribeTransport[sensorReading](client, router, 1, SubscribeOptions{})

	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var received sensorReading
	// mqtt5's subscribeWithHandle registers the broker subscription and
	// returns immediately (unlike zeromq's own blocking receive loop) —
	// SubscribeHandle therefore also returns as soon as registration
	// succeeds; the mock router's dispatch (mirroring an incoming broker
	// message) happens synchronously right after, same pattern as
	// TestSubscribe_ValueBased_DeliversMessage in caller_test.go.
	if err := events.SubscribeHandle(ctx, sub, subTransport, func(_ context.Context, r sensorReading) error {
		received = r
		return nil
	}); err != nil {
		t.Fatalf("SubscribeHandle: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.SensorID == "" {
		t.Fatal("expected message delivered to handler")
	}
}
