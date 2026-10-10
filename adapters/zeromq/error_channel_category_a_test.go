package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests Topic 1's Category A full enumeration fix for events
// (see docs/design/d-0005-error-handling.md): zeromq's
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

// TestErrorChannel_BoundSecurityMiddlewareFn_Matched_Publishes covers
// the Middleware-dispatched (`SubscribeBoundMW`-attached bound class)
// Security Fn failure case — mirrors adapters/mqtt5's/adapters/mqtt's
// own, identically-named test exactly. Closes zeromq's own version of
// the test-coverage blind spot that let a confirmed cross-pattern
// inconsistency (events wrapping a Security-carrying Middleware Fn's
// failure as the GENERIC events.MiddlewareError, rather than
// events.SecurityError like REST's own isSecuritySatisfyingHandler-
// gated behavior) go undetected — see this session's cross-phase review
// round for the full writeup. Also asserts the previously-missing
// stats.SecurityObserver.RecordSecurityRejection call now fires for
// this specific failure mode on zeromq too.
func TestErrorChannel_BoundSecurityMiddlewareFn_Matched_Publishes(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings-bound-security"), []byte(validSensorJSON)},
		},
	}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	rejectingMw := events.BoundSecuritySubscribeMiddleware[sensorReading, struct{}, struct{}](
		"bearer4", events.BearerScheme("JWT"), nil,
		func(context.Context, *sensorReading, struct{}) (struct{}, error) {
			return struct{}{}, errors.New("rejected by security impl")
		},
	)
	handle, err := events.NewChannel[sensorReading]("sensors/readings-bound-security", sensorCodec,
		events.ErrorChannel[events.SecurityError, sensorZmqErrPayload](
			"sensors/readings-bound-security/errors", sensorZmqErrPayloadCodec,
			func(e events.SecurityError) (sensorZmqErrPayload, error) {
				return sensorZmqErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).
		WithSubscribe(events.Subscribe{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer4")}}).
		SubscribeBoundMW(rejectingMw).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &testObserver{}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obs},
	)

	sock.mu.Lock()
	defer sock.mu.Unlock()
	found := false
	for _, frames := range sock.sentFrames {
		if len(frames) >= 1 && string(frames[0]) == "sensors/readings-bound-security/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
	if len(obs.securityRejections) != 1 {
		t.Errorf("want 1 RecordSecurityRejection call, got %d", len(obs.securityRejections))
	}
}
