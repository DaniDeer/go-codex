package mqtt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

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
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if got := client.subscribedTopicSnapshot(); got == wantTopic {
		t.Fatalf("subscribedTopicSnapshot should be empty for a publish-only test, got %q", got)
	}
	if client.publishedTopic != wantTopic {
		t.Errorf("published topic = %q, want %q (EncodeVars/BuildTopic derivation failed)", client.publishedTopic, wantTopic)
	}
}

func TestAttach_ClientPublish_WrongPubType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := c.Publish(context.Background(), "not-a-publisher", sensorReading{})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientPublish_WrongMsgType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client); err != nil {
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
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	var mu sync.Mutex
	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, r sensorReading) error {
			mu.Lock()
			received = r
			mu.Unlock()
			return nil
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			h(client, &mockMessage{topic: "sensors/readings",
				payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := <-done; err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if received.SensorID == "" {
		t.Fatal("expected message to be delivered via Client.Subscribe")
	}
}

// TestAttach_ClientSubscribe_RegistersSpecIntoRealClient mirrors
// adapters/zeromq's/adapters/mqtt5's identical regression test.
func TestAttach_ClientSubscribe_RegistersSpecIntoRealClient(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client); err != nil {
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
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := c.Subscribe(context.Background(), "not-a-subscriber", func() {})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

// ── NewPublishTransport/NewSubscribeTransport (Decision 7) ──────────────────

func TestNewPublishTransport_PublishHandle_RoundTrip(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	transport := NewPublishTransport[sensorReading](client, 1, false, PublishOptions[sensorReading]{})

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := events.PublishHandle(context.Background(), pub, transport, reading); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}

	gotTopic := client.publishedTopicSnapshot()
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if gotTopic != wantTopic {
		t.Errorf("topic = %q, want %q", gotTopic, wantTopic)
	}
}

func TestNewSubscribeTransport_SubscribeHandle_RoundTrip(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	subTransport := NewSubscribeTransport[sensorReading](client, 1, SubscribeOptions{})

	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var mu sync.Mutex
	var received sensorReading
	done := make(chan error, 1)
	// mqtt v3's subscribeTransport registers the broker subscription
	// (returning as soon as the SUBACK completed token resolves) and THEN
	// blocks on ctx.Done() — replicating caller.ServeSubscribers' own
	// "register everything, then block" pattern (see handletransport.go's
	// doc comment). So SubscribeHandle itself blocks here; we deliver a
	// message through the registered handler once it's available, then
	// cancel ctx to let SubscribeHandle return.
	go func() {
		done <- events.SubscribeHandle(ctx, sub, subTransport, func(_ context.Context, r sensorReading) error {
			mu.Lock()
			received = r
			mu.Unlock()
			return nil
		})
	}()

	var handler pahomqtt.MessageHandler
	deadline := time.After(time.Second)
	for handler == nil {
		handler = client.subscribedHandlerSnapshot()
		if handler != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for subscription registration")
		case <-time.After(time.Millisecond):
		}
	}
	handler(client, &mockMessage{topic: "sensors/readings",
		payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})

	mu.Lock()
	gotID := received.SensorID
	mu.Unlock()
	if gotID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("expected message delivered to handler, got %+v", received)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("SubscribeHandle: %v", err)
	}
}
