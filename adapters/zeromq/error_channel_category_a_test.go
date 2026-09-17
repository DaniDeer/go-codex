package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests Topic 1's Category A full enumeration fix for events
// (see docs/roadmap/error-handling-rest-events-reqreply.md): zeromq's
// subscribe-side failure points beyond handler/middleware-Fn errors are
// now events.ErrorChannel-eligible too — mirrors mqtt5's own equivalent
// test file, one representative row (payload decode) since the
// underlying mechanism (ObserveErrorResponseFor + tryPublishErrorChannel)
// is identical, already fully proven there.

func TestErrorChannel_PayloadDecode_Matched_Publishes(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(`{}`)}, // missing required fields -> codex.ValidationErrors
		},
	}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorZmqErrPayload](
			"sensors/readings/errors", sensorZmqErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorZmqErrPayload, error) {
				return sensorZmqErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		),
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

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a decode failure")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	found := false
	for _, frames := range sock.sentFrames {
		if len(frames) >= 1 && string(frames[0]) == "sensors/readings/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
}
