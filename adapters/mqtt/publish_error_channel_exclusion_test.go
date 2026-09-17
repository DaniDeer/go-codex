package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

// This file tests F5's fix (session review finding): Topic 7's role
// clarification (docs/roadmap/error-handling-rest-events-reqreply.md) —
// a publish-side middleware Fn error is returned DIRECTLY to the caller,
// NEVER checked against a declared events.ErrorChannel, even when one
// exists on the same channel and would type-match. No test previously
// guarded this invariant against a future regression.

func TestPublish_ClientMiddlewareFnError_NeverConsultsErrorChannel(t *testing.T) {
	mw := newTDEmptyDeclaration("fn-error-policy")
	fnErr := errors.New("business rule violated")
	pub := events.NewChannel[userEvent]("user/created", userEventCodec,
		// Declares an ErrorChannel that WOULD match events.MiddlewareError
		// (the wrapped shape the publish-side Fn error becomes) — if
		// Topic 7's exclusion were ever broken, this publish would
		// erroneously succeed with a typed error published to this topic
		// instead of returning the error to the caller.
		events.ErrorChannel[events.MiddlewareError, userErrPayload](
			"user/created/errors", userErrPayloadCodec,
			func(e events.MiddlewareError) (userErrPayload, error) {
				return userErrPayload{Code: "should_never_fire", Message: e.Error()}, nil
			},
		),
	).WithPublish(events.Publish{Summary: "test"})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg userEvent) (tdEmpty, error) {
		return tdEmpty{}, fnErr
	})
	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	pubErr := publish(context.Background(), client, handle, 1, false, event, nil, PublishOptions[userEvent]{})

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
	// should have been published at all, since a publish-side Fn error
	// aborts before transmission.
	if client.publishedTopicSnapshot() != "" {
		t.Errorf("want NO publish to any topic (ErrorChannel must stay excluded on the publish side), got topic=%q", client.publishedTopicSnapshot())
	}
}
