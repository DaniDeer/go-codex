package zeromq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestAttach_ClientPublish_RoundTrip(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(client, sock); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := client.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
	gotTopic := string(sock.sentFrames[0][0])
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if gotTopic != wantTopic {
		t.Errorf("topic = %q, want %q (EncodeVars/BuildTopic derivation failed)", gotTopic, wantTopic)
	}
}

func TestAttach_ClientPublish_WrongPubType_ReturnsTransportTypeMismatchError(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(client, sock); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := client.Publish(context.Background(), "not-a-publisher", sensorReading{})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientPublish_WrongMsgType_ReturnsTransportTypeMismatchError(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(client, sock); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	pub := sensorChannel("sensors/readings").WithPublish(events.Publish{})
	err := client.Publish(context.Background(), pub, "wrong-type")
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientSubscribe_RoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(client, sock); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	received := make(chan sensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// Subscribe blocks until ctx is cancelled, mirroring
	// ServeOneSubscriber's own documented blocking semantics.
	if err := client.Subscribe(ctx, sub, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}); err != nil {
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

// TestAttach_ClientSubscribe_RegistersSpecIntoRealClient is a regression
// test proving Client.Subscribe registers sub's spec into the SAME
// client the caller attached (not a throwaway scratch client) — an
// earlier draft of transport.Subscribe used a scratch *events.Client
// (mirroring ServeOneSubscriber), which would silently leave the
// subscribe operation out of client.AsyncAPISpec().
func TestAttach_ClientSubscribe_RegistersSpecIntoRealClient(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(client, sock); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{OperationID: "receiveReading"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.Subscribe(ctx, sub, func(context.Context, sensorReading) error { return nil }); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	doc, err := client.AsyncAPISpec()
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
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := Attach(client, sock); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := client.Subscribe(context.Background(), "not-a-subscriber", func() {})
	var mismatchErr events.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

// ── Decision 7: NewPublishTransport/NewSubscribeTransport (generic, no *events.Client needed) ──

func TestNewPublishTransport_PublishHandle_RoundTrip(t *testing.T) {
	sock := &mockSocket{}
	transport := NewPublishTransport[sensorReading](sock, PublishOptions[sensorReading]{})

	pub := mergeSensorChannel("sensors/{sensorID}/readings").WithPublish(events.Publish{})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := events.PublishHandle(context.Background(), pub, transport, reading); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}

	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
	gotTopic := string(sock.sentFrames[0][0])
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if gotTopic != wantTopic {
		t.Errorf("topic = %q, want %q", gotTopic, wantTopic)
	}
}

func TestNewSubscribeTransport_SubscribeHandle_RoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	subTransport := NewSubscribeTransport[sensorReading](sock, SubscribeOptions[sensorReading]{})

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	received := make(chan sensorReading, 1)
	// SubscribeHandle blocks until ctx is cancelled, mirroring
	// Client.Subscribe's own documented blocking semantics.
	if err := events.SubscribeHandle(ctx, sub, subTransport, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}); err != nil {
		t.Fatalf("SubscribeHandle: %v", err)
	}

	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected non-empty SensorID")
		}
	default:
		t.Fatal("expected a received message before ctx cancellation")
	}
}
