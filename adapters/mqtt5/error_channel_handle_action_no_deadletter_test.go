package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests G4's fix (session review round-3 finding): a
// type-matched events.ErrorChannel with a non-Respond action
// (ErrorHandle/ErrorLog) must NOT ALSO trigger DeadLetter — Topic 4's
// own scope decision (docs/design/d-0005-error-handling.md)
// covers ONLY the genuinely UNMATCHED case ("a business error whose
// type matches no declared ErrorChannel"), and a type match with a
// non-Respond action is still a match.

func TestErrorChannel_HandleAction_Matched_DoesNotAlsoDeadLetter(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		).WithAction(events.ErrorHandle),
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{}`), // missing required fields -> codex.ValidationErrors
	})

	if !onErrorCalled {
		t.Error("OnError SHOULD be called for a matched ErrorHandle-action pattern (the 'handle' realization)")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	// Neither the declared ErrorChannel topic (ErrorHandle action never
	// auto-publishes) NOR the declared DeadLetter topic (a type match,
	// even with a non-Respond action, is NOT the "genuinely unmatched"
	// case DeadLetter is scoped to) should have received a publish.
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			t.Error("want NO publish to the ErrorChannel topic for a non-Respond action")
		}
		if p.Topic == "sensors/readings/dlq" {
			t.Error("want NO publish to the DeadLetter topic for a matched (even if non-Respond-action) ErrorChannel")
		}
	}
}

func TestErrorChannel_LogAction_Matched_DoesNotAlsoDeadLetter(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		).WithAction(events.ErrorLog),
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{}`),
	})

	if !onErrorCalled {
		t.Error("OnError SHOULD be called for a matched ErrorLog-action pattern (falls through unchanged)")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	for _, p := range client.published {
		if p.Topic == "sensors/readings/dlq" {
			t.Error("want NO publish to the DeadLetter topic for a matched (even if non-Respond-action) ErrorChannel")
		}
	}
}

// TestErrorChannel_GenuineNonMatch_StillDeadLetters is the negative-case
// control: a channel with NO ErrorChannel declared for the actual
// failure type (a genuine non-match) still reaches DeadLetter as
// before — confirming G4's fix didn't accidentally suppress the
// legitimate unmatched case too.
func TestErrorChannel_GenuineNonMatch_StillDeadLetters(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{}`),
	})

	client.mu.Lock()
	defer client.mu.Unlock()
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the DeadLetter topic for a genuinely unmatched failure")
	}
}
