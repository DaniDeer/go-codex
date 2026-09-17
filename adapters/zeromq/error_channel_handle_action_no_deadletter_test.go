package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests G4's fix (session review round-3 finding): a
// type-matched events.ErrorChannel with a non-Respond action
// (ErrorHandle/ErrorLog) must NOT ALSO trigger DeadLetter — Topic 4's
// own scope decision (docs/design/d-0005-error-handling.md)
// covers ONLY the genuinely UNMATCHED case ("a business error whose
// type matches no declared ErrorChannel"), and a type match with a
// non-Respond action is still a match.

func TestErrorChannel_HandleAction_Matched_DoesNotAlsoDeadLetter(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(`{}`)}, // missing required fields
		},
	}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorZmqErrPayload](
			"sensors/readings/errors", sensorZmqErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorZmqErrPayload, error) {
				return sensorZmqErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		).WithAction(events.ErrorHandle),
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{OnError: func(SubscribeError) { onErrorCalled = true }},
	)

	if !onErrorCalled {
		t.Error("OnError SHOULD be called for a matched ErrorHandle-action pattern (the 'handle' realization)")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	for _, frames := range sock.sentFrames {
		if len(frames) >= 1 && string(frames[0]) == "sensors/readings/errors" {
			t.Error("want NO publish to the ErrorChannel topic for a non-Respond action")
		}
		if len(frames) >= 1 && string(frames[0]) == "sensors/readings/dlq" {
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
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(`{}`)},
		},
	}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{},
	)

	sock.mu.Lock()
	defer sock.mu.Unlock()
	found := false
	for _, frames := range sock.sentFrames {
		if len(frames) >= 1 && string(frames[0]) == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the DeadLetter topic for a genuinely unmatched failure")
	}
}
