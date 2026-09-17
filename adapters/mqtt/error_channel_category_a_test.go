package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
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
	handle, err := newSecuredHandleWithErrorChannel(func(_ context.Context, _ pahomqtt.Message, _ *userEvent) (map[string][]string, error) {
		return nil, errors.New("rejected by security impl")
	})
	if err != nil {
		t.Fatalf("newSecuredHandleWithErrorChannel: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeHandle(ctx, caller.client, handle, 1,
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
// the way through subscribeHandle's dispatch.
func newSecuredHandleWithErrorChannel(impl func(context.Context, pahomqtt.Message, *userEvent) (map[string][]string, error)) (*events.ChannelHandle[userEvent], error) {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	mw := events.FromSecurityScheme("bearerAuth", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)
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
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		}).
		Use(mw).
		SubscribeMW(&mw, impl).
		Handle(b)
}
