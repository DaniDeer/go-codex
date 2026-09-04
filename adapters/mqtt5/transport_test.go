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
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
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

// ── Observer + ErrorChannel parity for Client.Attach (Decision 8) ──────────
//
// Confirmed gap (see docs/roadmap/pubsub-workflow-simplification.md's
// Decision 8): transport.Publish/Subscribe used to call neither
// stats.Observer NOR consult a declared events.ErrorChannel on a
// subscribe handler's returned error — these tests lock in the fix.

func TestAttach_ClientPublish_RecordsObserver(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obs := &testObserver{}
	ctx := stats.WithObserver(context.Background(), obs)
	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(ctx, pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(obs.publishes) != 1 || !obs.publishes[0] {
		t.Fatalf("RecordPublish calls = %v, want exactly one successful call", obs.publishes)
	}
	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.publish" {
		t.Errorf("TraceObserver.StartSpan calls = %v, want [mqtt5.publish]", obs.startSpanOps)
	}
}

func TestAttach_ClientSubscribe_RecordsObserver(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obs := &testObserver{}
	ctx, cancel := context.WithTimeout(stats.WithObserver(context.Background(), obs), 300*time.Millisecond)
	defer cancel()

	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil })
	}()

	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})
	<-done

	if len(obs.subscribes) != 1 || !obs.subscribes[0] {
		t.Fatalf("RecordSubscribe calls = %v, want exactly one successful call", obs.subscribes)
	}
}

// TestAttach_ClientSubscribe_HandlerError_MatchedErrorChannel_PublishesTypedPayload
// confirms a subscribe handler's returned domain error is redirected to a
// declared events.ErrorChannel's error-output topic — mirrors
// mqtt5PublishAdapter.handleUpstreamError's existing action dispatch,
// extended here to the Client.Attach Subscribe path.
func TestAttach_ClientSubscribe_HandlerError_MatchedErrorChannel_PublishesTypedPayload(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[sensorValidationErr, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e sensorValidationErr) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "out_of_range", Message: e.msg}, nil
			},
		),
	)
	sub := ch.WithSubscribe(events.Subscribe{})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			return sensorValidationErr{msg: "value too high"}
		})
	}()

	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})
	<-done

	time.Sleep(50 * time.Millisecond) // handler runs on the router's own goroutine
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
			if !strings.Contains(string(p.Payload), "out_of_range") {
				t.Errorf("error payload = %s, want it to contain out_of_range", p.Payload)
			}
		}
	}
	if !found {
		t.Fatal("expected a message published to the declared error-output topic")
	}
}

// ── Client.Attach honors the channel's declared format (pubsub-workflow-simplification.md Decision 9) ──
//
// Confirmed gap (see docs/roadmap/pubsub-workflow-simplification.md's Decision 9): Client.Attach's
// Publish/Subscribe used to ALWAYS assume JSON, silently ignoring a
// channel's declared WithFormats/WithPublishFormats/WithSubscribeFormats
// — this test locks in the fix (round-trips YAML through Client.Attach).
func TestAttach_ClientPublishSubscribe_HonorsDeclaredYAMLFormat(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(c, client, router); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.Formats(format.YAML(sensorCodec)),
	)
	sub := ch.WithSubscribe(events.Subscribe{})
	pub := ch.WithPublish(events.Publish{})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	received := make(chan sensorReading, 1)
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, r sensorReading) error {
			received <- r
			return nil
		})
	}()
	router.waitHandler("sensors/readings")

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(ctx, pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Confirm the WIRE bytes are actually YAML, not JSON — proves
	// EncodeWithFormats (not plain Encode) was used.
	got := client.lastPublished()
	if got == nil {
		t.Fatal("want 1 published message, got none")
	}
	if strings.HasPrefix(strings.TrimSpace(string(got.Payload)), "{") {
		t.Errorf("expected YAML wire payload, got JSON-shaped: %s", got.Payload)
	}
	if !strings.Contains(string(got.Payload), "value: 22.5") {
		t.Errorf("expected YAML payload containing 'value: 22.5', got: %s", got.Payload)
	}

	// The mock client's Publish does not loop back to the router (unlike a
	// real broker) — dispatch the just-published wire bytes to the
	// subscriber handler directly, as other tests in this file do.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: got.Payload,
	})

	select {
	case r := <-received:
		if r.SensorID != reading.SensorID || r.Value != reading.Value {
			t.Errorf("round-tripped reading = %+v, want %+v", r, reading)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the YAML-decoded message via Client.Subscribe")
	}
}
