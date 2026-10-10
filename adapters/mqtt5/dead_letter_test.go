package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests Topic 4's DeadLetter fallback (see docs/design/
// d-0005-error-handling.md): a channel that declares
// events.DeadLetter dead-letters a subscribe-side failure (when no
// ErrorChannel matches, or none is declared) AND a failed publish.

func TestDeadLetter_SubscribeDecodeFailure_Published(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{}`), // missing required fields
	})

	if onErrorCalled {
		t.Error("OnError should NOT be called when DeadLetter absorbs a decode failure")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic")
	}
}

// topicFailingClient fails Publish only for the ORIGINAL source topic, so
// a subsequent dead-letter publish attempt (a DIFFERENT topic) still
// succeeds and can be observed — mirrors mockClient otherwise.
type topicFailingClient struct {
	mockClient
	failTopic string
	failErr   error
}

func (c *topicFailingClient) Publish(ctx context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	if p.Topic == c.failTopic {
		return nil, c.failErr
	}
	return c.mockClient.Publish(ctx, p)
}

func TestDeadLetter_PublishFailure_Published(t *testing.T) {
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithPublish(events.Publish{Summary: "test"}).Handle(
		events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"})),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &topicFailingClient{failTopic: "sensors/readings", failErr: errors.New("broker unavailable")}

	err = publish(context.Background(), client, handle,
		sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}, nil, false, PublishOptions[sensorReading]{})
	if err == nil {
		t.Fatal("want a publish error to propagate to the caller unchanged")
	}

	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic even though the ORIGINAL publish failed")
	}
}

// TestPublish_SecurityImpl_MalformedFormat_WithDeadLetter_Published is a
// deep-dive-bug-hunt reproduction test: the publish-side built-in
// credential FORMAT check (validateSecurityCredentials, gating on
// SecurityScheme.Codec — the client-side mirror of Subscribe's own
// built-in check, already fixed for ErrorChannel/DeadLetter in Round
// 196) was the only publish-side Category-A failure branch in this
// function still returning the error directly, bypassing tryDeadLetter
// entirely — every OTHER failure branch in publish() already dead-
// letters per "Topic 4: a failed publish ... ALSO dead-letterable".
func TestPublish_SecurityImpl_MalformedFormat_WithDeadLetter_Published(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	declMw := events.SecurityMiddleware[struct{}, struct{}]("bearer", securedBearerScheme, nil)
	mw := pubSecBoundMw(func(context.Context, sensorReading) (pubSecOut, error) {
		// Empty credential value -> fails the non-empty-string Codec.
		return pubSecOut{GrantedScopes: map[string][]string{"bearer": {}}, Authorization: ""}, nil
	})
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithPublish(events.Publish{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		Use(declMw).
		PublishBoundMW(mw).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pubErr := publish(context.Background(), client, handle, reading, nil, true, PublishOptions[sensorReading]{})
	var credErr events.SecurityCredentialError
	if !errors.As(pubErr, &credErr) {
		t.Fatalf("want events.SecurityCredentialError to still propagate to the caller, got %v", pubErr)
	}

	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic even though credential format validation failed")
	}
}
