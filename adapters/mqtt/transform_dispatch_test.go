package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type tdIn struct{ Key string }

var tdInCodec = codex.Struct[tdIn](
	codex.RequiredField("key", codex.String().Refine(validate.NonEmptyString),
		func(in tdIn) string { return in.Key },
		func(in *tdIn, v string) { in.Key = v },
	),
)

type tdOut struct{ Value string }

var tdOutCodec = codex.Struct[tdOut](
	codex.RequiredField("value", codex.String().Refine(validate.NonEmptyString),
		func(out tdOut) string { return out.Value },
		func(out *tdOut, v string) { out.Value = v },
	),
)

func newTDDeclaration(name string) events.Middleware[tdIn, tdOut] {
	return events.NewMiddleware(middleware.NewDeclaration(name, tdInCodec, tdOutCodec))
}

// tdEmpty is an In/Out shape with NO required fields — used where a test
// needs a Transform-attached middleware whose fn ignores In/Out entirely.
type tdEmpty struct{}

var tdEmptyCodec = codex.Struct[tdEmpty]()

func newTDEmptyDeclaration(name string) events.Middleware[tdEmpty, tdEmpty] {
	return events.NewMiddleware(middleware.NewDeclaration(name, tdEmptyCodec, tdEmptyCodec))
}

func newSubscriberChannelHandle(subscriber events.Subscriber[userEvent]) *events.ChannelHandle[userEvent] {
	h, err := subscriber.Handle(nil)
	if err != nil {
		panic(err)
	}
	return h
}

// ── Transform: happy path, enrichment ────────────────────────────────────

func TestSubscribeHandler_Transform_HappyPath_EnrichesMsg(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in tdIn) error {
		msg.ID = in.Key + "-enriched"
		return nil
	})
	handle := newSubscriberChannelHandle(subscriber)

	var received userEvent
	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { received = e; return nil }, SubscribeOptions{})
	handler(nil, &mockMessage{topic: "user/us-east/created", payload: []byte(validPayload)})

	if received.ID != "us-east-enriched" {
		t.Errorf("want enriched ID %q, got %q", "us-east-enriched", received.ID)
	}
}

// ── Transform: In-decode failure short-circuits, handler never called ───

func TestSubscribeHandler_Transform_InDecodeFailure_HandlerNotCalled(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in tdIn) error {
		handlerCalled = true
		return nil
	})
	handle := newSubscriberChannelHandle(subscriber)

	var gotErr SubscribeError
	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { return nil },
		SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})
	// "region" left empty in the topic — fails mw's own InCodec
	// (NonEmptyString).
	handler(nil, &mockMessage{topic: "user//created", payload: []byte(validPayload)})

	if handlerCalled {
		t.Error("want handler NOT called when middleware In-decode fails")
	}
	var mie events.MiddlewareInputError
	if !errors.As(gotErr.Err, &mie) {
		t.Errorf("want MiddlewareInputError, got %v", gotErr.Err)
	}
}

// ── Transform: fn error surfaces as events.MiddlewareError ──────────────

func TestSubscribeHandler_Transform_FnError_WrapsAsMiddlewareError(t *testing.T) {
	mw := newTDEmptyDeclaration("region-policy")
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in tdEmpty) error {
		return errors.New("boom")
	})
	handle := newSubscriberChannelHandle(subscriber)

	var gotErr SubscribeError
	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { handlerCalled = true; return nil },
		SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})
	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if handlerCalled {
		t.Error("want handler NOT called when middleware fn returns an error")
	}
	var mwErr events.MiddlewareError
	if !errors.As(gotErr.Err, &mwErr) {
		t.Fatalf("want events.MiddlewareError, got %v", gotErr.Err)
	}
	if mwErr.Name != "region-policy" {
		t.Errorf("want Name %q, got %q", "region-policy", mwErr.Name)
	}
}

// ── Channel-agnostic .Use(mw) dispatch ───────────────────────────────────

func TestSubscribeHandler_Use_AgnosticMiddleware_Dispatches(t *testing.T) {
	callCount := 0
	mw := newTDEmptyDeclaration("agnostic-policy").
		WithReceive(func(ctx context.Context, in tdEmpty) error {
			callCount++
			return nil
		})
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).Use(mw)
	handle := newSubscriberChannelHandle(subscriber)

	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { return nil }, SubscribeOptions{})
	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if callCount != 1 {
		t.Errorf("want bundled receiveFn called exactly once, got %d", callCount)
	}
}

// ── ClientTransform (publish side): happy path, encodes Out into topic vars ──

func TestPublish_ClientTransform_HappyPath_EncodesOutIntoTopicVars(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithPublishTopic(events.NewTopicParam("region", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
	publisher := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg userEvent) (tdOut, error) {
		return tdOut{Value: "us-west"}, nil
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	if err := publish(context.Background(), client, handle, 1, false, event, nil, PublishOptions[userEvent]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := client.publishedTopicSnapshot(); got != "user/us-west/created" {
		t.Errorf("want topic %q, got %q", "user/us-west/created", got)
	}
}

// ── ClientTransform: fn error aborts before publish ──────────────────────

func TestPublish_ClientTransform_FnError_AbortsBeforePublish(t *testing.T) {
	mw := newTDEmptyDeclaration("region-policy")
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg userEvent) (tdEmpty, error) {
		return tdEmpty{}, errors.New("boom")
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	pubErr := publish(context.Background(), client, handle, 1, false, event, nil, PublishOptions[userEvent]{})
	if pubErr == nil {
		t.Fatal("want error from ClientTransform fn")
	}
	if client.publishedTopicSnapshot() != "" {
		t.Error("want no message published when middleware fn errors")
	}
}

// Rest-middleware-conflict-detection-improvements' adapter-dispatch review
// found adapters/mqtt (v3) had ZERO "middleware:in"/"middleware:fn"
// reporting anywhere — this package's subscribe/publish dispatch never
// called stats.ReportErrors for its own middleware failures at all,
// unlike adapters/mqtt5/adapters/zeromq which already had (mislabeled, in
// some cases) coverage. This test suite closes that gap, mirroring the
// mqtt5/zeromq test patterns exactly.

func TestSubscribeHandler_Observer_ReportsMiddlewareInLocation(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in tdIn) error { return nil })
	handle := newSubscriberChannelHandle(subscriber)

	obs := &mqttSpyObserver{}
	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { return nil },
		SubscribeOptions{Observer: obs})
	// "region" left empty in the topic — fails mw's own InCodec (NonEmptyString).
	handler(nil, &mockMessage{topic: "user//created", payload: []byte(validPayload)})

	found := false
	for _, ve := range obs.valErrors {
		if ve.location == "middleware:in" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:in", obs.valErrors)
	}
}

func TestSubscribeHandler_Observer_ReportsMiddlewareFnLocation(t *testing.T) {
	mw := newTDEmptyDeclaration("fn-error-policy")
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in tdEmpty) error {
		return codex.ValidationErrors{{Field: "region", Err: errors.New("boom")}}
	})
	handle := newSubscriberChannelHandle(subscriber)

	obs := &mqttSpyObserver{}
	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { return nil },
		SubscribeOptions{Observer: obs})
	handler(nil, &mockMessage{payload: []byte(validPayload)})

	found := false
	for _, ve := range obs.valErrors {
		if ve.location == "middleware:fn" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obs.valErrors)
	}
}

func TestPublish_Observer_ReportsMiddlewareFnLocation(t *testing.T) {
	mw := newTDEmptyDeclaration("fn-error-policy")
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg userEvent) (tdEmpty, error) {
		return tdEmpty{}, codex.ValidationErrors{{Field: "region", Err: errors.New("boom")}}
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	_ = publish(context.Background(), client, handle, 1, false, event, nil, PublishOptions[userEvent]{Observer: obs})

	found := false
	for _, ve := range obs.valErrors {
		if ve.location == "middleware:fn" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obs.valErrors)
	}
}

// This EncodeOut failure is reported as its own "middleware:out" location
// — distinct from "middleware:fn" (a real fn business error) — symmetric
// with REST's own "middleware:out".
func TestPublish_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	mw := newTDDeclaration("tenant-required-policy")
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg userEvent) (tdOut, error) {
		// Empty Value fails tdOutCodec's NonEmptyString refinement at
		// EncodeOut/OutCodec.Validate time, NOT the fn itself.
		return tdOut{Value: ""}, nil
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	_ = publish(context.Background(), client, handle, 1, false, event, nil, PublishOptions[userEvent]{Observer: obs})

	found := false
	for _, ve := range obs.valErrors {
		if ve.location == "middleware:out" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:out", obs.valErrors)
	}
}
