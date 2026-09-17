package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

var errSecurityRejected = errors.New("rejected by security impl")

// This file tests Topic 1's Category A full enumeration fix for events
// (see docs/design/d-0005-error-handling.md): the
// subscribe-side failure points beyond handler/middleware-Fn errors
// (already covered elsewhere) are now events.ErrorChannel-eligible too.

func TestErrorChannel_PayloadDecode_Matched_Publishes(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		),
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

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a decode failure")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
}

func TestErrorChannel_TopicMismatch_Matched_Publishes(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", codex.String(),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
		events.ErrorChannel[TopicMismatchError, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e TopicMismatchError) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "topic_mismatch", Message: "bad topic"}, nil
			},
		),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})
	router.waitHandler("sensors/+/readings")

	router.mu.Lock()
	h := router.handlers["sensors/+/readings"]
	router.mu.Unlock()
	if h == nil {
		t.Fatal("expected a registered handler for sensors/+/readings")
	}
	// "sensors/readings" has 2 segments; the template requires 3 — a
	// genuine structural mismatch, mirroring
	// TestObserver_RecordValidationError_topicMismatch_subscribe's own
	// direct-handler-invocation technique.
	h(&pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a topic-mismatch failure")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
}

func TestErrorChannel_MiddlewareDecodeIn_Matched_Publishes(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec,
		events.ErrorChannel[events.MiddlewareInputError, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e events.MiddlewareInputError) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "middleware_input", Message: e.Error()}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdIn) error {
		return nil
	})
	handle := newSubscriberChannelHandle(subscriber)

	client := &mockClient{}
	router := newMockRouter()
	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	// "region" resolves to "" here since the concrete topic's segment IS
	// empty-string-shaped for this deliberately malformed dispatch.
	router.dispatch("sensors/+/readings", &pahomqtt5.Publish{
		Topic: "sensors//readings", Payload: []byte(validSensorJSON),
	})

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a middleware DecodeIn failure")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
}

func TestErrorChannel_SecurityMiddlewareFn_Matched_Publishes(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	// Deliberately NO .WithCodec(...) on this scheme — the built-in
	// codec-based credential check is then skipped entirely ("nil Codec
	// means no format validation"), so dispatch reaches the
	// Implementations-based impl below, which is what this test needs to
	// exercise (isolating the security MIDDLEWARE Fn failure from the
	// unrelated built-in credential check).
	bareBearerScheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	mw := events.FromSecurityScheme("bearer", bareBearerScheme, nil)
	impl := func(_ context.Context, _ *pahomqtt5.Publish, _ *sensorReading) (map[string][]string, error) {
		return nil, errSecurityRejected
	}
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[events.SecurityError, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e events.SecurityError) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		),
	).
		WithSubscribe(events.Subscribe{Summary: "test", Security: []route.SecurityRequirement{route.Require("bearer")}}).
		Use(mw).
		SubscribeMW(&mw, impl).
		Handle(b)
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
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a security middleware Fn failure")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
}

// TestErrorChannel_UserPropertyParam_Matched_Publishes tests F3's fix
// (session review finding): User Property param validation failures are
// now ErrorChannel/DeadLetter-eligible, closing an asymmetry with REST's
// own wired header-param validation.
func TestErrorChannel_UserPropertyParam_Matched_Publishes(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[MissingUserPropertyError, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e MissingUserPropertyError) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "bad_user_property", Message: e.Error()}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{
			OnError: func(SubscribeError) { onErrorCalled = true },
			UserPropertyParams: []UserPropertyParam{
				UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	router.dispatch("sensors/readings",
		mqttMsgWithUserProps(validSensorJSON, nil)) // missing required TenantID

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches a user-property validation failure")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared error-output topic")
	}
}
