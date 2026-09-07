package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

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

// tdEmpty is an In/Out shape with NO required fields.
type tdEmpty struct{}

var tdEmptyCodec = codex.Struct[tdEmpty]()

func newTDEmptyDeclaration(name string) events.Middleware[tdEmpty, tdEmpty] {
	return events.NewMiddleware(middleware.NewDeclaration(name, tdEmptyCodec, tdEmptyCodec))
}

func newSubscriberHandle(subscriber events.Subscriber[sensorReading]) *events.ChannelHandle[sensorReading] {
	h, err := subscriber.Handle(nil)
	if err != nil {
		panic(err)
	}
	return h
}

func runSubscribeWithHandle(handle *events.ChannelHandle[sensorReading], sock *mockSocket, fn func(context.Context, sensorReading) error, opts SubscribeOptions[sensorReading]) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return subscribeWithHandle(ctx, sock, handle, fn, opts)
}

// ── Transform: happy path, enrichment ─────────────────────────────────────

func TestSubscribe_Transform_HappyPath_EnrichesMsg(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	subscriber := events.NewChannel[sensorReading]("sensors/{region}/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *sensorReading, in tdIn) error {
		msg.SensorID = in.Key + "-enriched"
		return nil
	})
	handle := newSubscriberHandle(subscriber)

	var received sensorReading
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/us-east/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error {
		received = r
		return nil
	}, SubscribeOptions[sensorReading]{})

	if received.SensorID != "us-east-enriched" {
		t.Errorf("want enriched SensorID %q, got %q", "us-east-enriched", received.SensorID)
	}
}

// ── Transform: In-decode failure short-circuits, handler never called ───

func TestSubscribe_Transform_InDecodeFailure_HandlerNotCalled(t *testing.T) {
	mw := newTDDeclaration("region-policy").
		WithSubscribeTopic(events.NewTopicParam("region", codex.String().Refine(validate.UUID),
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
	handle := newSubscriberHandle(subscriber)

	var gotErr SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			// "not-a-uuid" is a well-formed topic segment (matches the
			// template structurally) but fails mw's own InCodec (UUID).
			{[]byte("sensors/not-a-uuid/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{OnError: func(e SubscribeError) { gotErr = e }})

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
	handle := newSubscriberHandle(subscriber)

	var gotErr SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error { handlerCalled = true; return nil },
		SubscribeOptions[sensorReading]{OnError: func(e SubscribeError) { gotErr = e }})

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
	handle := newSubscriberHandle(subscriber)

	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribeWithHandle(handle, sock, func(_ context.Context, r sensorReading) error { return nil }, SubscribeOptions[sensorReading]{})

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

	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := publish(context.Background(), sock, handle, reading, nil, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 send, got %d", len(sock.sentFrames))
	}
	if got := string(sock.sentFrames[0][0]); got != "sensors/us-west/readings" {
		t.Errorf("want topic %q, got %q", "sensors/us-west/readings", got)
	}
}

// ── ClientTransform: fn error aborts before publish ──────────────────────

func TestPublish_ClientTransform_FnError_AbortsBeforePublish(t *testing.T) {
	mw := newTDEmptyDeclaration("region-policy")
	publisher := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg sensorReading) (tdEmpty, error) {
		return tdEmpty{}, errors.New("boom")
	})
	handle, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	pubErr := publish(context.Background(), sock, handle, reading, nil, PublishOptions[sensorReading]{})
	if pubErr == nil {
		t.Fatal("want error from ClientTransform fn")
	}
	if len(sock.sentFrames) != 0 {
		t.Error("want no message published when middleware fn errors")
	}
}
