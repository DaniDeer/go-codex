package mqtt

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests G4's fix (session review round-3 finding): a
// type-matched events.ErrorChannel with a non-Respond action
// (ErrorHandle/ErrorLog) must NOT ALSO trigger DeadLetter — Topic 4's
// own scope decision (docs/roadmap/error-handling-rest-events-reqreply.md)
// covers ONLY the genuinely UNMATCHED case ("a business error whose
// type matches no declared ErrorChannel"), and a type match with a
// non-Respond action is still a match.

func TestErrorChannel_HandleAction_Matched_DoesNotAlsoDeadLetter(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.ErrorChannel[codex.ValidationErrors, userErrPayload](
			"user/created/errors", userErrPayloadCodec,
			func(e codex.ValidationErrors) (userErrPayload, error) {
				return userErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		).WithAction(events.ErrorHandle),
		events.DeadLetter("user/created/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	handler := subscribeHandler(context.Background(), client, handle,
		func(_ context.Context, _ userEvent) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }},
	)

	handler(client, &mockMessage{payload: []byte(`{}`)}) // missing required fields -> codex.ValidationErrors

	if !onErrorCalled {
		t.Error("OnError SHOULD be called for a matched ErrorHandle-action pattern (the 'handle' realization)")
	}
	for _, topic := range client.publishedTopicsSnapshot() {
		if topic == "user/created/errors" {
			t.Error("want NO publish to the ErrorChannel topic for a non-Respond action")
		}
		if topic == "user/created/dlq" {
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
	client := &mockClient{token: newCompletedToken(nil)}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.DeadLetter("user/created/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	handler := subscribeHandler(context.Background(), client, handle,
		func(_ context.Context, _ userEvent) error { return nil },
		SubscribeOptions{},
	)

	handler(client, &mockMessage{payload: []byte(`{}`)})

	found := false
	for _, topic := range client.publishedTopicsSnapshot() {
		if topic == "user/created/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the DeadLetter topic for a genuinely unmatched failure")
	}
}
