package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
)

// This file tests Topic 4's DeadLetter fallback (see docs/design/
// d-0005-error-handling.md): a channel that declares
// events.DeadLetter dead-letters a subscribe-side failure (when no
// ErrorChannel matches, or none is declared) AND a failed publish.

func TestDeadLetter_SubscribeDecodeFailure_Published(t *testing.T) {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.DeadLetter("user/created/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	onErrorCalled := false
	handler := subscribeHandler(context.Background(), client, handle,
		func(_ context.Context, _ userEvent) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})

	handler(client, &mockMessage{payload: []byte(`{}`)}) // missing required fields

	if onErrorCalled {
		t.Error("OnError should NOT be called when DeadLetter absorbs a decode failure")
	}
	found := false
	for _, topic := range client.publishedTopicsSnapshot() {
		if topic == "user/created/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic")
	}
}

// topicFailingClient fails Publish only for the ORIGINAL source topic, so
// a subsequent dead-letter publish attempt (a DIFFERENT topic) still
// succeeds and can be observed.
type topicFailingClient struct {
	*mockClient
	failTopic string
	failErr   error
}

func (c *topicFailingClient) Publish(topic string, qos byte, retained bool, payload interface{}) pahomqtt.Token {
	if topic == c.failTopic {
		return newCompletedToken(c.failErr)
	}
	return c.mockClient.Publish(topic, qos, retained, payload)
}

func TestDeadLetter_PublishFailure_Published(t *testing.T) {
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.DeadLetter("user/created/dlq"),
	).WithPublish(events.Publish{Summary: "test"}).Handle(
		events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"})),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &topicFailingClient{mockClient: &mockClient{token: newCompletedToken(nil)}, failTopic: "user/created", failErr: errors.New("broker unavailable")}

	err = publish(context.Background(), client, handle,
		userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}, nil, PublishOptions[userEvent]{})
	if err == nil {
		t.Fatal("want a publish error to propagate to the caller unchanged")
	}

	found := false
	for _, topic := range client.mockClient.publishedTopicsSnapshot() {
		if topic == "user/created/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic even though the ORIGINAL publish failed")
	}
}

// TestServeSubscribers_HandlerError_WithDeadLetter_PublishesDeadLetter is
// a REGRESSION GUARD: subscribeEntryReflect (ServeSubscribers' per-entry
// dispatch — the PRIMARY recommended Client.Attach workflow) never
// consulted a declared events.DeadLetter on ANY failure path, unlike
// subscribeHandler (the escape hatch), which already does (see
// TestDeadLetter_SubscribeDecodeFailure_Published above). A channel
// declaring DeadLetter whose handler fails should publish a
// DeadLetterEnvelope to the dead-letter topic via ServeSubscribers too.
func TestServeSubscribers_HandlerError_WithDeadLetter_PublishesDeadLetter(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(client, ev)

	handlerErr := errors.New("handler boom")
	ch := events.NewChannel[sensorReading]("sensors/dl", sensorCodec,
		events.DeadLetter("sensors/dead-letter"),
	)
	sub := ch.WithSubscribe(events.Subscribe{}).WithHandler(
		func(context.Context, sensorReading) error { return handlerErr })
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			h(client, &mockMessage{topic: "sensors/dl",
				payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.5}`)})
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-done

	found := false
	for _, topic := range client.publishedTopicsSnapshot() {
		if topic == "sensors/dead-letter" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a dead-letter message published to sensors/dead-letter, got topics: %+v", client.publishedTopicsSnapshot())
	}
}
