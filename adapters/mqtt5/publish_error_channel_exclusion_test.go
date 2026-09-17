package mqtt5

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

// This file tests F5's fix (session review finding): Topic 7's role
// clarification (docs/design/d-0005-error-handling.md) —
// a publish-side middleware Fn error is returned DIRECTLY to the caller,
// NEVER checked against a declared events.ErrorChannel, even when one
// exists on the same channel and would type-match. No test previously
// guarded this invariant against a future regression.

func TestPublish_ClientMiddlewareFnError_NeverConsultsErrorChannel(t *testing.T) {
	mw := events.NewMiddleware(newMqttMdDeclaration("fn-error-policy"))
	fnErr := errors.New("business rule violated")
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		// Declares an ErrorChannel that WOULD match events.MiddlewareError
		// (the wrapped shape the publish-side Fn error becomes) — if
		// Topic 7's exclusion were ever broken, this publish would
		// erroneously succeed with a typed error published to this topic
		// instead of returning the error to the caller.
		events.ErrorChannel[events.MiddlewareError, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e events.MiddlewareError) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "should_never_fire", Message: e.Error()}, nil
			},
		),
	).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg sensorReading) (mqttMdOut, error) {
		return mqttMdOut{}, fnErr
	})
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}
	pubErr := publishHandle(context.Background(), client, handle, 1, false, reading, PublishOptions[sensorReading]{})

	if pubErr == nil {
		t.Fatal("want the middleware Fn error returned DIRECTLY to the caller, got nil")
	}
	var mwErr events.MiddlewareError
	if !errors.As(pubErr, &mwErr) {
		t.Fatalf("want errors.As to match events.MiddlewareError, got %v", pubErr)
	}
	if !errors.Is(pubErr, fnErr) {
		t.Errorf("want the original fn error wrapped via Unwrap, got %v", pubErr)
	}

	// The declared ErrorChannel must NEVER have been consulted — nothing
	// should have been published to "sensors/readings/errors" (or ANY
	// topic at all, since a publish-side Fn error aborts before
	// transmission).
	if len(client.published) != 0 {
		t.Errorf("want NO publish to any topic (ErrorChannel must stay excluded on the publish side), got %d publish(es)", len(client.published))
	}
}
