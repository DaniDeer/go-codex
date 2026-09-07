package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
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
// needs a Transform-attached middleware whose fn ignores In/Out entirely
// (only enrichment or a deliberate fn error matters), so
// InCodec.Validate never fails on a zero value.
type tdEmpty struct{}

var tdEmptyCodec = codex.Struct[tdEmpty]()

func newTDEmptyDeclaration(name string) events.Middleware[tdEmpty, tdEmpty] {
	return events.NewMiddleware(middleware.NewDeclaration(name, tdEmptyCodec, tdEmptyCodec))
}

func newSubscriberChannelHandle(subscriber events.Subscriber[sensorReading]) *events.ChannelHandle[sensorReading] {
	h, err := subscriber.Handle(nil)
	if err != nil {
		panic(err)
	}
	return h
}

// ── Transform: happy path, enrichment ─────────────────────────────────────

func TestSubscribe_Transform_HappyPath_EnrichesMsg(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	// NOTE: "region" must be part of the topic template to be extractable
	// — reuse a template with an extra var the channel's own Item doesn't
	// claim.
	subscriber := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdIn) error {
		msg.SensorID = in.Key + "-enriched"
		return nil
	})
	handle := newSubscriberChannelHandle(subscriber)

	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/+/readings", &pahomqtt5.Publish{
		Topic:   "sensors/us-east/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.SensorID != "us-east-enriched" {
		t.Errorf("want enriched SensorID %q, got %q", "us-east-enriched", received.SensorID)
	}
}

// ── Transform: In-decode failure short-circuits, handler never called ───

func TestSubscribe_Transform_InDecodeFailure_HandlerNotCalled(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdIn) error {
		handlerCalled = true
		return nil
	})
	handle := newSubscriberChannelHandle(subscriber)

	client := &mockClient{}
	router := newMockRouter()
	var gotErr SubscribeError
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { return nil },
		SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})

	// "region" left empty in the topic — fails mw's own InCodec
	// (NonEmptyString), which the channel's own template-var codec
	// (plain string, no constraint) does not.
	router.dispatch("sensors/+/readings", &pahomqtt5.Publish{
		Topic:   "sensors//readings",
		Payload: []byte(validSensorJSON),
	})

	if handlerCalled {
		t.Error("want handler NOT called when middleware In-decode fails")
	}
	var mie events.MiddlewareInputError
	if !errors.As(gotErr.Err, &mie) {
		t.Errorf("want MiddlewareInputError, got %v", gotErr.Err)
	}
}

// ── Transform: fn error surfaces as events.MiddlewareError ──────────────

func TestSubscribe_Transform_FnError_WrapsAsMiddlewareError(t *testing.T) {
	mw := newTDEmptyDeclaration("region-policy")
	subscriber := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	handlerCalled := false
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdEmpty) error {
		return errors.New("boom")
	})
	handle := newSubscriberChannelHandle(subscriber)

	client := &mockClient{}
	router := newMockRouter()
	var gotErr SubscribeError
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { handlerCalled = true; return nil },
		SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

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

func TestSubscribe_Use_AgnosticMiddleware_Dispatches(t *testing.T) {
	callCount := 0
	mw := newTDEmptyDeclaration("agnostic-policy").
		WithReceive(func(ctx context.Context, in tdEmpty) error {
			callCount++
			return nil
		})
	subscriber := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).Use(mw)
	handle := newSubscriberChannelHandle(subscriber)

	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

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
	publisher := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg sensorReading) (tdOut, error) {
		return tdOut{Value: "us-west"}, nil
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{}
	reading := sensorReading{SensorID: "11111111-1111-1111-1111-111111111111", Value: 1.5}
	if err := publish(context.Background(), client, handle, 1, false, reading, nil, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message, got %d", len(client.published))
	}
	if got := client.published[0].Topic; got != "sensors/us-west/readings" {
		t.Errorf("want topic %q, got %q", "sensors/us-west/readings", got)
	}
}

// ── ClientTransform: fn error aborts before publish ──────────────────────

func TestPublish_ClientTransform_FnError_AbortsBeforePublish(t *testing.T) {
	mw := newTDDeclaration("region-policy")
	publisher := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg sensorReading) (tdOut, error) {
		return tdOut{}, errors.New("boom")
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{}
	reading := sensorReading{SensorID: "11111111-1111-1111-1111-111111111111", Value: 1.5}
	pubErr := publish(context.Background(), client, handle, 1, false, reading, nil, PublishOptions[sensorReading]{})
	if pubErr == nil {
		t.Fatal("want error from ClientTransform fn")
	}
	if len(client.published) != 0 {
		t.Error("want no message published when middleware fn errors")
	}
}

// ── D3: explicit vars > middleware-derived > channel-derived precedence ──

func TestPublish_ClientTransform_D3Precedence_ExplicitVarsWinOverMiddleware(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithPublishTopic(events.NewTopicParam("region", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
	publisher := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg sensorReading) (tdOut, error) {
		return tdOut{Value: "mw-region"}, nil
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{}
	reading := sensorReading{SensorID: "11111111-1111-1111-1111-111111111111", Value: 1.5}
	// Explicit vars (the 6th "vars" param, mirroring channel-own-derived
	// precedence) supplies its OWN "region" — must win over the
	// middleware-derived value.
	if err := publish(context.Background(), client, handle, 1, false, reading,
		map[string]string{"region": "explicit-region"}, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := client.published[0].Topic; got != "sensors/explicit-region/readings" {
		t.Errorf("want explicit vars to win, got topic %q", got)
	}
}

// ── D6(c): two subscribe middlewares both writing the SAME *T field —
// attachment-order, last-applied-wins, NOT flagged as a conflict ──

func TestSubscribe_TwoMiddlewaresEnrichSameField_LastAttachedWins(t *testing.T) {
	mwFirst := newTDEmptyDeclaration("first-policy")
	mwSecond := newTDEmptyDeclaration("second-policy")

	subscriber := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mwFirst, func(ctx context.Context, msg *sensorReading, in tdEmpty) error {
		msg.SensorID = "first"
		return nil
	})
	subscriber = events.Transform(subscriber, mwSecond, func(ctx context.Context, msg *sensorReading, in tdEmpty) error {
		msg.SensorID = "second"
		return nil
	})
	handle := newSubscriberChannelHandle(subscriber)

	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.SensorID != "second" {
		t.Errorf("want last-attached middleware's write to win (%q), got %q", "second", received.SensorID)
	}
}

// TestSubscribe_Use_AgnosticMiddleware_DispatchesOnBothChannels exercises
// docs/design/d-0003-codec-declared-middlewares.md's route-agnostic reuse
// requirement for events: the SAME bundled `mw` value (WithReceive)
// .Use()-attached to TWO channels carrying DIFFERENT message payload
// types, both ACTUALLY DISPATCHED (not just registered) and dispatching
// correctly and independently — mirrors REST's own
// TestUse_AgnosticMiddleware_DispatchesOnBothRoutes exactly, adapted to
// events/mqtt5.
func TestSubscribe_Use_AgnosticMiddleware_DispatchesOnBothChannels(t *testing.T) {
	callCount := 0
	mw := newTDEmptyDeclaration("agnostic-reuse-policy").
		WithReceive(func(ctx context.Context, in tdEmpty) error {
			callCount++
			return nil
		})

	subscriberA := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "A"}).Use(mw)
	handleA := newSubscriberChannelHandle(subscriberA)

	subscriberB := events.NewChannel[computeReq]("compute/requests", computeReqCodec).
		WithSubscribe(events.Subscribe{Summary: "B"}).Use(mw)
	handleB, err := subscriberB.Handle(nil)
	if err != nil {
		t.Fatalf("channel B Handle: %v", err)
	}

	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, handleA, 1,
		func(_ context.Context, r sensorReading) error { return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe A setup failed: %v", err)
	}
	if err := subscribeWithHandle(ctx, client, router, handleB, 1,
		func(_ context.Context, r computeReq) error { return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe B setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})
	router.dispatch("compute/requests", &pahomqtt5.Publish{
		Topic:   "compute/requests",
		Payload: []byte(`{"x":1,"y":2}`),
	})

	if callCount != 2 {
		t.Errorf("want mw's bundled receiveFn called once per channel (2 total), got %d", callCount)
	}
}
