package mqtt

import (
	"context"
	"errors"
	"testing"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
)

// This file tests Topic 4's DeadLetter fallback (see docs/roadmap/
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

	err = publish(context.Background(), client, handle, 1, false,
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
