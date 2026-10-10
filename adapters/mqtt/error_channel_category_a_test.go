package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests Topic 1's Category A full enumeration fix for events
// (see docs/design/d-0005-error-handling.md): mqtt v3's
// subscribe-side failure points beyond handler/middleware-Fn errors are
// now events.ErrorChannel-eligible too — mirrors mqtt5's own equivalent
// test file, one representative row (payload decode) since the
// underlying mechanism (ObserveErrorResponseFor + tryPublishErrorChannel)
// is identical, already fully proven there.

func TestErrorChannel_PayloadDecode_Matched_Publishes(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.ErrorChannel[codex.ValidationErrors, userErrPayload](
			"user/created/errors", userErrPayloadCodec,
			func(e codex.ValidationErrors) (userErrPayload, error) {
				return userErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		),
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

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a decode failure")
	}
	if client.publishedTopicSnapshot() != "user/created/errors" {
		t.Fatalf("published topic = %q, want user/created/errors", client.publishedTopicSnapshot())
	}
}

// TestErrorChannel_SecurityImplFn_Matched_Publishes_ViaSubscribeHandle
// tests F2's fix (session review finding): mqtt v3's PRIMARY, documented
// subscribe workflow (subscribeHandle/NewSubscribeTransport, NOT the
// low-level subscribeHandler tested directly above) now wraps an
// Implementations-based security Fn rejection in events.SecurityError
// and routes it through the SAME ErrorChannel/DeadLetter consultation as
// mqtt5/zeromq — previously the rejection was indistinguishable from a
// plain handler error (misreported as KindHandler, never
// events.SecurityError, never ErrorChannel-eligible under its own
// security classification) because the check lived inside caller.go's
// fn-wrapping instead of adapter.go's subscribeHandler dispatch.
func TestErrorChannel_SecurityImplFn_Matched_Publishes_ViaSubscribeHandle(t *testing.T) {
	handle, err := newSecuredHandleWithErrorChannel(func(_ context.Context, _ *userEvent) (mqttSecOut, error) {
		return mqttSecOut{}, errors.New("rejected by security impl")
	})
	if err != nil {
		t.Fatalf("newSecuredHandleWithErrorChannel: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeHandle(ctx, caller.client, handle,
		func(_ context.Context, _ userEvent) error {
			t.Fatal("handler must not be called when the security implementation rejects")
			return nil
		},
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }}); err != nil {
		t.Fatalf("subscribeHandle: %v", err)
	}

	handler := client.subscribedHandlerSnapshot()
	if handler == nil {
		t.Fatal("expected client.Subscribe to have been called with a handler")
	}
	handler(client, &mockMessage{payload: []byte(validPayload)})

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a security implementation rejection")
	}
	if client.publishedTopicSnapshot() != "user/created/security-errors" {
		t.Fatalf("published topic = %q, want user/created/security-errors", client.publishedTopicSnapshot())
	}
}

// newSecuredHandleWithErrorChannel mirrors newSecuredHandle but ALSO
// declares an events.ErrorChannel[events.SecurityError, B] — proving the
// rejection is wrapped in events.SecurityError (not a bare error) all
// the way through subscribeHandle's dispatch. Security-carrying
// attachment migrated to the Bound mechanism per
// docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 8 — SubscribeMW now
// rejects a Security-carrying mw.
func newSecuredHandleWithErrorChannel(impl func(context.Context, *userEvent) (mqttSecOut, error)) (*events.ChannelHandle[userEvent], error) {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	bm := events.BoundSecuritySubscribeMiddleware[userEvent, tdEmpty, mqttSecOut]("bearerAuth",
		events.BearerScheme("JWT"), nil,
		func(ctx context.Context, msg *userEvent, in tdEmpty) (mqttSecOut, error) { return impl(ctx, msg) })
	return events.NewChannel[userEvent]("user/created", userEventCodec,
		events.ErrorChannel[events.SecurityError, userErrPayload](
			"user/created/security-errors", userErrPayloadCodec,
			func(e events.SecurityError) (userErrPayload, error) {
				return userErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).
		WithSubscribe(events.Subscribe{
			Summary:  "User created",
			Security: []events.SecurityRequirement{events.Require("bearerAuth")},
		}).
		SubscribeBoundMW(bm).
		Handle(b)
}

// TestErrorChannel_BoundSecurityMiddlewareFn_Matched_Publishes covers the
// Middleware-dispatched (`SubscribeBoundMW`-attached bound class)
// Security Fn failure case — distinct from
// TestErrorChannel_SecurityImplFn_Matched_Publishes_ViaSubscribeHandle
// above, which only exercises the LEGACY Implementations-based path
// (bare middleware.Middleware + SubscribeMW(&mw, rawFn)). This test
// closes mqtt v3's own version of the blind spot that let a confirmed
// cross-pattern inconsistency (events wrapping a Security-carrying
// Middleware Fn's failure as the GENERIC events.MiddlewareError, rather
// than events.SecurityError like REST's own isSecuritySatisfyingHandler-
// gated behavior) go undetected — see this session's cross-phase review
// round for the full writeup. Also asserts the previously-missing
// stats.SecurityObserver.RecordSecurityRejection call now fires for this
// specific failure mode on mqtt v3 too.
func TestErrorChannel_BoundSecurityMiddlewareFn_Matched_Publishes(t *testing.T) {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	rejectingMw := events.BoundSecuritySubscribeMiddleware[userEvent, struct{}, struct{}](
		"bearerAuth2", events.BearerScheme("JWT"), nil,
		func(context.Context, *userEvent, struct{}) (struct{}, error) {
			return struct{}{}, errors.New("rejected by security impl")
		},
	)
	handle, err := events.NewChannel[userEvent]("user/created-bound-security", userEventCodec,
		events.ErrorChannel[events.SecurityError, userErrPayload](
			"user/created-bound-security/errors", userErrPayloadCodec,
			func(e events.SecurityError) (userErrPayload, error) {
				return userErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).
		WithSubscribe(events.Subscribe{
			Summary:  "User created (bound security demo)",
			Security: []events.SecurityRequirement{events.Require("bearerAuth2")},
		}).
		SubscribeBoundMW(rejectingMw).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)
	obs := &mockSecurityObserver{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeHandle(ctx, caller.client, handle,
		func(_ context.Context, _ userEvent) error {
			t.Fatal("handler must not be called when the security Fn rejects")
			return nil
		},
		SubscribeOptions{Observer: obs}); err != nil {
		t.Fatalf("subscribeHandle: %v", err)
	}

	handler := client.subscribedHandlerSnapshot()
	if handler == nil {
		t.Fatal("expected client.Subscribe to have been called with a handler")
	}
	handler(client, &mockMessage{topic: "user/created-bound-security", payload: []byte(validPayload)})

	if client.publishedTopicSnapshot() != "user/created-bound-security/errors" {
		t.Fatalf("published topic = %q, want user/created-bound-security/errors", client.publishedTopicSnapshot())
	}
	if obs.scheme == "" {
		t.Error("want RecordSecurityRejection to have been called")
	}
}
