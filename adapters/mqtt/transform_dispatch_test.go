package mqtt

import (
	"context"
	"errors"
	"testing"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
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

func newTDDeclaration(name string) middleware.Declaration[tdIn, tdOut] {
	return middleware.NewDeclaration(name, tdInCodec, tdOutCodec)
}

// tdEmpty is an In/Out shape with NO required fields — used where a test
// needs a SubscribeBoundMW-attached middleware whose fn ignores In/Out entirely.
type tdEmpty struct{}

var tdEmptyCodec = codex.Struct[tdEmpty]()

func newTDEmptyDeclaration(name string) middleware.Declaration[tdEmpty, tdEmpty] {
	return middleware.NewDeclaration(name, tdEmptyCodec, tdEmptyCodec)
}

func newSubscriberChannelHandle(subscriber events.Subscriber[userEvent]) *events.ChannelHandle[userEvent] {
	h, err := subscriber.Handle(nil)
	if err != nil {
		panic(err)
	}
	return h
}

// ── SubscribeBoundMW: happy path, enrichment ─────────────────────────────

func TestSubscribeHandler_SubscribeBoundMW_HappyPath_EnrichesMsg(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTDDeclaration("region-policy"),
		func(ctx context.Context, msg *userEvent, in tdIn) (tdOut, error) {
			msg.ID = in.Key + "-enriched"
			return tdOut{Value: "unused"}, nil
		}).
		WithSubscribeTopic(events.NewTopicParam("region", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = subscriber.SubscribeBoundMW(bm)
	handle := newSubscriberChannelHandle(subscriber)

	var received userEvent
	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, e userEvent) error { received = e; return nil }, SubscribeOptions{})
	handler(nil, &mockMessage{topic: "user/us-east/created", payload: []byte(validPayload)})

	if received.ID != "us-east-enriched" {
		t.Errorf("want enriched ID %q, got %q", "us-east-enriched", received.ID)
	}
}

// ── SubscribeBoundMW: In-decode failure short-circuits, handler never called ──

func TestSubscribeHandler_SubscribeBoundMW_InDecodeFailure_HandlerNotCalled(t *testing.T) {
	handlerCalled := false
	bm := events.NewBoundSubscribeMiddleware(newTDDeclaration("region-policy"),
		func(ctx context.Context, msg *userEvent, in tdIn) (tdOut, error) {
			handlerCalled = true
			return tdOut{Value: "unused"}, nil
		}).
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = subscriber.SubscribeBoundMW(bm)
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

// ── SubscribeBoundMW: fn error surfaces as events.MiddlewareError ───────

func TestSubscribeHandler_SubscribeBoundMW_FnError_WrapsAsMiddlewareError(t *testing.T) {
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	bm := events.NewBoundSubscribeMiddleware(newTDEmptyDeclaration("region-policy"),
		func(ctx context.Context, msg *userEvent, in tdEmpty) (tdEmpty, error) {
			return tdEmpty{}, errors.New("boom")
		})
	subscriber = subscriber.SubscribeBoundMW(bm)
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
	mw := events.NewMiddleware(newTDEmptyDeclaration("agnostic-policy")).
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

// ── PublishBoundMW: happy path, encodes Out into topic vars ─────────────

func TestPublish_PublishBoundMW_HappyPath_EncodesOutIntoTopicVars(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTDDeclaration("region-policy"),
		func(ctx context.Context, msg userEvent) (tdOut, error) {
			return tdOut{Value: "us-west"}, nil
		}).
		WithPublishTopic(events.NewTopicParam("region", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
	publisher := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = publisher.PublishBoundMW(bm)
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	if err := publish(context.Background(), client, handle, event, nil, PublishOptions[userEvent]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := client.publishedTopicSnapshot(); got != "user/us-west/created" {
		t.Errorf("want topic %q, got %q", "user/us-west/created", got)
	}
}

// ── PublishBoundMW: fn error aborts before publish ───────────────────────

func TestPublish_PublishBoundMW_FnError_AbortsBeforePublish(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("region-policy"),
		func(ctx context.Context, msg userEvent) (tdEmpty, error) {
			return tdEmpty{}, errors.New("boom")
		})
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = publisher.PublishBoundMW(bm)
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	pubErr := publish(context.Background(), client, handle, event, nil, PublishOptions[userEvent]{})
	if pubErr == nil {
		t.Fatal("want error from PublishBoundMW fn")
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
	bm := events.NewBoundSubscribeMiddleware(newTDDeclaration("region-policy"),
		func(ctx context.Context, msg *userEvent, in tdIn) (tdOut, error) { return tdOut{Value: "unused"}, nil }).
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = subscriber.SubscribeBoundMW(bm)
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
	bm := events.NewBoundSubscribeMiddleware(newTDEmptyDeclaration("fn-error-policy"),
		func(ctx context.Context, msg *userEvent, in tdEmpty) (tdEmpty, error) {
			return tdEmpty{}, codex.ValidationErrors{{Field: "region", Err: errors.New("boom")}}
		})
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = subscriber.SubscribeBoundMW(bm)
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
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("fn-error-policy"),
		func(ctx context.Context, msg userEvent) (tdEmpty, error) {
			return tdEmpty{}, codex.ValidationErrors{{Field: "region", Err: errors.New("boom")}}
		})
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = publisher.PublishBoundMW(bm)
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	pubErr := publish(context.Background(), client, handle, event, nil, PublishOptions[userEvent]{Observer: obs})

	found := false
	for _, ve := range obs.valErrors {
		if ve.location == "middleware:fn" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obs.valErrors)
	}
	// pubErr must now be errors.As-able into events.MiddlewareError (see
	// mqtt5's identical fix, docs/design/d-0003-codec-declared-middlewares.md's
	// Addendum 2).
	var mwErr events.MiddlewareError
	if !errors.As(pubErr, &mwErr) {
		t.Fatalf("want errors.As to match events.MiddlewareError, got %v", pubErr)
	}
	if mwErr.Name != "fn-error-policy" {
		t.Errorf("want Name %q, got %q", "fn-error-policy", mwErr.Name)
	}
}

// This EncodeOut failure is reported as its own "middleware:out" location
// — distinct from "middleware:fn" (a real fn business error) — symmetric
// with REST's own "middleware:out".
func TestPublish_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTDDeclaration("tenant-required-policy"),
		func(ctx context.Context, msg userEvent) (tdOut, error) {
			// Empty Value fails tdOutCodec's NonEmptyString refinement at
			// EncodeOut/OutCodec.Validate time, NOT the fn itself.
			return tdOut{Value: ""}, nil
		})
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = publisher.PublishBoundMW(bm)
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	pubErr := publish(context.Background(), client, handle, event, nil, PublishOptions[userEvent]{Observer: obs})

	found := false
	for _, ve := range obs.valErrors {
		if ve.location == "middleware:out" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:out", obs.valErrors)
	}
	// pubErr must now be errors.As-able into events.MiddlewareOutputError.
	var outputErr events.MiddlewareOutputError
	if !errors.As(pubErr, &outputErr) {
		t.Fatalf("want errors.As to match events.MiddlewareOutputError, got %v", pubErr)
	}
	if outputErr.Name != "tenant-required-policy" {
		t.Errorf("want Name %q, got %q", "tenant-required-policy", outputErr.Name)
	}
}

// D1: codec-backed middleware dispatch (SubscribeBoundMW) runs AFTER the
// paired security Fn, both pre-handler — confirms mqtt v3's dispatch
// order matches mqtt5's/zeromq's/REST's established order (ported during
// a dedicated cross-adapter parity review round — mqtt5/zeromq already
// had this exact test, mqtt v3 did not).
func TestSubscribeHandler_MiddlewareDispatch_RunsAfterPairedSecurity(t *testing.T) {
	var order []string
	bm := events.NewBoundSubscribeMiddleware(newTDEmptyDeclaration("order-policy"),
		func(ctx context.Context, msg *userEvent, in tdEmpty) (tdEmpty, error) {
			order = append(order, "middleware")
			return tdEmpty{}, nil
		})

	mw := events.FromSecurityScheme("bearerAuth", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{
			Summary:  "test",
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		}).
		Use(mw).
		SubscribeMW(&mw, func(_ context.Context, _ pahomqtt.Message, _ *userEvent) (map[string][]string, error) {
			order = append(order, "security")
			return map[string][]string{"bearerAuth": nil}, nil
		})
	subscriber = subscriber.SubscribeBoundMW(bm)
	handle := newSubscriberChannelHandle(subscriber)

	handler := subscribeHandler(context.Background(), nil, handle,
		func(_ context.Context, _ userEvent) error { return nil }, SubscribeOptions{})
	handler(nil, &mockMessage{topic: "user/created", payload: []byte(validPayload)})

	if len(order) != 2 || order[0] != "security" || order[1] != "middleware" {
		t.Errorf("want dispatch order [security, middleware], got %v", order)
	}
}

// D3-equivalent precedence verification: when publish is called with a
// non-nil vars map containing "region" AND a middleware that would ALSO
// derive "region", the vars-param value wins (events.OverrideDerivedVars
// in adapter.go's own publish — "explicit/channel-own vars... wins over
// middleware-derived vars on a key collision"). mqtt v3's own publish has
// no separate isExplicitVars bool (unlike mqtt5/zeromq) — this test
// exercises the ONE precedence tier it does have. Ported during a
// dedicated cross-adapter parity review round — mqtt5 already had this
// test (as D3Precedence), mqtt v3 did not.
func TestPublish_PublishBoundMW_VarsParamWinsOverMiddleware(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTDDeclaration("region-policy"),
		func(ctx context.Context, msg userEvent) (tdOut, error) {
			return tdOut{Value: "mw-region"}, nil
		}).
		WithPublishTopic(events.NewTopicParam("region", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
	publisher := events.NewChannel[userEvent]("user/{region}/created", userEventCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = publisher.PublishBoundMW(bm)
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{token: newCompletedToken(nil)}
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	if err := publish(context.Background(), client, handle, event,
		map[string]string{"region": "explicit-region"}, PublishOptions[userEvent]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := client.publishedTopicSnapshot(); got != "user/explicit-region/created" {
		t.Errorf("want explicit vars to win, got topic %q", got)
	}
}
