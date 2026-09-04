package zeromq

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// ── shared helpers ─────────────────────────────────────────────────────────────

// sensorChannel returns a fresh, unregistered Channel[sensorReading] declared
// against topic — used to build [events.Subscriber]/[events.Publisher] values
// for the value-based subscribe tests below.
func sensorChannel(topic string) events.Channel[sensorReading] {
	return events.NewChannel[sensorReading](topic, sensorCodec)
}

// ── Caller / newCaller ──────────────────────────────────────────────────────────

func TestNewCaller_BuildsCaller(t *testing.T) {
	sock := &mockSocket{}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(sock, ev)
	if caller == nil {
		t.Fatal("newCaller returned nil")
	}
}

func TestNewCaller_NilEventsClient(t *testing.T) {
	// caller.events MAY be nil — subscribe must still work,
	// producing spec-free handles (see events.Subscriber.Handle's own
	// nil-client contract).
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	caller := newCaller(sock, nil)
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	received := make(chan sensorReading, 1)
	err := subscribe(ctx, caller, sub, func(_ context.Context, r sensorReading) error {
		received <- r
		return nil
	}, SubscribeOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected a decoded reading")
		}
	default:
		t.Fatal("expected the handler to have received a message")
	}
}

// ── Two-tier Subscribe / SubscribeWithHandle parity ─────────────────────────────

func TestSubscribe_ValueBased_ParityWithSubscribeWithHandle(t *testing.T) {
	// Both tiers, given equivalent inputs, must decode+dispatch identically.
	frame := [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}}

	sockA := &mockSocket{inFrames: append([][][]byte{}, frame...)}
	var gotA sensorReading
	ctxA, cancelA := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelA()
	_ = subscribeWithHandle(ctxA, sockA, newSubscribeHandle(),
		func(_ context.Context, r sensorReading) error { gotA = r; return nil },
		SubscribeOptions[sensorReading]{})

	sockB := &mockSocket{inFrames: append([][][]byte{}, frame...)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(sockB, ev)
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	var gotB sensorReading
	ctxB, cancelB := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancelB()
	_ = subscribe(ctxB, caller, sub,
		func(_ context.Context, r sensorReading) error { gotB = r; return nil },
		SubscribeOptions[sensorReading]{})

	if gotA != gotB {
		t.Fatalf("value-based Subscribe and SubscribeWithHandle diverged: %+v vs %+v", gotA, gotB)
	}
}

// ── deriveTopicPrefix (indirect — via mockSocket.subTopic) ──────────────────────

func TestDeriveTopicPrefix_SinglePlaceholder(t *testing.T) {
	sock := &mockSocket{}
	handle := newMergeChannelHandle() // topic: "sensors/{sensorID}/readings"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{})
	if sock.subTopic != "sensors/" {
		t.Fatalf("want derived prefix %q, got %q", "sensors/", sock.subTopic)
	}
}

func TestDeriveTopicPrefix_MultiplePlaceholders(t *testing.T) {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("a/{x}/b/{y}", sensorCodec).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatal(err)
	}
	sock := &mockSocket{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{})
	if sock.subTopic != "a/" {
		t.Fatalf("want derived prefix %q, got %q", "a/", sock.subTopic)
	}
}

func TestDeriveTopicPrefix_NoPlaceholders(t *testing.T) {
	sock := &mockSocket{}
	handle := newSubscribeHandle() // topic: "sensors/readings" (no placeholders)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{})
	if sock.subTopic != "sensors/readings" {
		t.Fatalf("want unchanged topic %q, got %q", "sensors/readings", sock.subTopic)
	}
}

// ── TopicFilter regression — top-level SubscribeOptions ─────────────────────────

func TestSubscribeOptions_TopicFilter_ExplicitOverride(t *testing.T) {
	sock := &mockSocket{}
	handle := newMergeChannelHandle() // topic: "sensors/{sensorID}/readings"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{TopicFilter: "sensors/explicit/"})
	if sock.subTopic != "sensors/explicit/" {
		t.Fatalf("explicit TopicFilter must win over derived prefix: got %q", sock.subTopic)
	}
}

func TestSubscribeOptions_TopicFilter_EmptyDerivesPrefix(t *testing.T) {
	// Regression guard for the bug fixed this pass: before TopicFilter
	// existed, a templated topic's placeholders were sent VERBATIM as the
	// subscription filter ("sensors/{sensorID}/readings"), which never
	// matches any real published topic. With TopicFilter empty, the
	// derived prefix ("sensors/") must be used instead.
	sock := &mockSocket{}
	handle := newMergeChannelHandle()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{})
	if sock.subTopic == "sensors/{sensorID}/readings" {
		t.Fatal("BUG: raw template sent verbatim as subscription filter")
	}
	if sock.subTopic != "sensors/" {
		t.Fatalf("want derived prefix %q, got %q", "sensors/", sock.subTopic)
	}
}

// ── TopicFilter regression — ports-binding SubscribeAdapterOptions ──────────────
//
// SEPARATE, explicit regression test for the ports-binding layer's fix —
// confirmed via docs/roadmap/pubsub-workflow-simplification.md that this
// was an INDEPENDENTLY broken spot from the top-level SubscribeOptions fix
// above (SubscribeAdapterOptions was "just {Buffer int}", zero filter
// override field, before this pass) — do not assume the top-level test
// covers this path too.

func TestSubscribeAdapterOptions_TopicFilter_EmptyDerivesPrefix(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"), []byte(validSensorJSON)},
		},
	}
	handle := newMergeChannelHandle()
	adapter := SubscribeAdapter(sock, handle, format.JSON(sensorCodec), SubscribeAdapterOptions{Buffer: 4})

	dst := make(chan sensorReading, 4)
	errs := make(chan error, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	adapter.Activate(ctx, dst, errs)

	if sock.subTopic == "sensors/{sensorID}/readings" {
		t.Fatal("BUG: ports-binding layer sent raw template verbatim as subscription filter")
	}
	if sock.subTopic != "sensors/" {
		t.Fatalf("want derived prefix %q from ports-binding layer, got %q", "sensors/", sock.subTopic)
	}
}

func TestSubscribeAdapterOptions_TopicFilter_ExplicitOverride(t *testing.T) {
	sock := &mockSocket{}
	handle := newMergeChannelHandle()
	adapter := SubscribeAdapter(sock, handle, format.JSON(sensorCodec),
		SubscribeAdapterOptions{Buffer: 4, TopicFilter: "sensors/custom/"})

	dst := make(chan sensorReading, 4)
	errs := make(chan error, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	adapter.Activate(ctx, dst, errs)

	if sock.subTopic != "sensors/custom/" {
		t.Fatalf("explicit ports-binding TopicFilter must win: got %q", sock.subTopic)
	}
}

// ── ServeSubscribers / serveOneSubscriber ────────────────────────────────────────

func TestServeSubscribers_NilEventsClient_ReturnsNilImmediately(t *testing.T) {
	sock := &mockSocket{}
	caller := newCaller(sock, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServeSubscribers_NoRegisteredSubscribers_ReturnsNilImmediately(t *testing.T) {
	sock := &mockSocket{}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(sock, ev)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServeSubscribers_SingleChannel_DispatchesToHandler(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	received := make(chan sensorReading, 1)
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, r sensorReading) error {
			received <- r
			return nil
		})
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}
	caller := newCaller(sock, ev)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("ServeSubscribers: %v", err)
	}
	select {
	case r := <-received:
		if r.SensorID == "" {
			t.Fatal("expected decoded reading")
		}
	default:
		t.Fatal("handler was never invoked")
	}
}

func TestServeSubscribers_MultipleChannels_SharedReceiveLoop(t *testing.T) {
	// Two DIFFERENT channels registered against the SAME events.Client and
	// the SAME mockSocket — confirms ServeSubscribers dispatches by
	// topic-template match on a single shared receive loop, per this
	// package's design note in serve_subscribers.go.
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
			{[]byte("alerts/topic"), []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":9.9}`)},
		},
	}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	gotSensors := make(chan sensorReading, 1)
	subSensors := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, r sensorReading) error { gotSensors <- r; return nil })
	if err := subSensors.Register(ev); err != nil {
		t.Fatalf("Register sensors: %v", err)
	}

	gotAlerts := make(chan sensorReading, 1)
	subAlerts := sensorChannel("alerts/topic").WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, r sensorReading) error { gotAlerts <- r; return nil })
	if err := subAlerts.Register(ev); err != nil {
		t.Fatalf("Register alerts: %v", err)
	}

	caller := newCaller(sock, ev)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("ServeSubscribers: %v", err)
	}

	select {
	case <-gotSensors:
	default:
		t.Fatal("sensors handler was never invoked")
	}
	select {
	case <-gotAlerts:
	default:
		t.Fatal("alerts handler was never invoked")
	}

	subs := sock.subTopicsSnapshot()
	if len(subs) != 2 {
		t.Fatalf("want 2 SetSubscription calls (one per registered channel), got %d: %v", len(subs), subs)
	}
}

func TestServeSubscribers_MalformedImplementationShape_ErrorsEagerly(t *testing.T) {
	sock := &mockSocket{}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, _ sensorReading) error { return nil }).
		SubscribeMW(nil, func() {}) // wrong shape entirely
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}
	caller := newCaller(sock, ev)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := caller.ServeSubscribers(ctx)
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %T: %v", err, err)
	}
}

// TestServeSubscribers_WrongHandlerOptsType_ReturnsOptionsShapeError is a
// regression test for a finding from the pubsub-workflow-simplification
// consistency review: resolveSubscribeOptsReflect previously discarded a
// wrong-typed [events.Subscriber.WithOptions] value SILENTLY (no error,
// no diagnostic) instead of returning [OptionsShapeError], unlike
// adapters/mqtt5's and adapters/mqtt's identical caller-mistake check.
func TestServeSubscribers_WrongHandlerOptsType_ReturnsOptionsShapeError(t *testing.T) {
	sock := &mockSocket{}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	type unrelatedOptions struct {
		TopicFilter string
	}

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, _ sensorReading) error { return nil }).
		WithOptions(unrelatedOptions{TopicFilter: "sensors/#"}) // wrong type entirely
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(sock, ev)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := caller.ServeSubscribers(ctx)
	var shapeErr OptionsShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want OptionsShapeError, got %T: %v", err, err)
	}
	if shapeErr.Topic != "sensors/readings" {
		t.Errorf("want Topic=sensors/readings, got %q", shapeErr.Topic)
	}
}

func TestServeOneSubscriber_ConsumesExactlyOneChannel(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	caller := newCaller(sock, nil)
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})

	received := make(chan sensorReading, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := serveOneSubscriber(ctx, caller, sub,
		func(_ context.Context, r sensorReading) error { received <- r; return nil },
		SubscribeOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("serveOneSubscriber: %v", err)
	}
	select {
	case <-received:
	default:
		t.Fatal("handler was never invoked")
	}
}

// ── Eager shape-validation rejection (both directions) ──────────────────────────

func TestSubscribeWithHandle_RejectsMalformedSubscribeMWShape(t *testing.T) {
	sock := &mockSocket{}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, func(int) int { return 0 }) // wrong shape entirely
	handle, err := sub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	err = subscribeWithHandle(context.Background(), sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{})
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %T: %v", err, err)
	}
}

func TestPublish_RejectsMalformedPublishMWShape(t *testing.T) {
	sock := &mockSocket{}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{}).
		PublishMW(nil, "not a function") // wrong shape entirely
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	err = publish(context.Background(), sock, handle, reading, nil, PublishOptions[sensorReading]{})
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %T: %v", err, err)
	}
}

func TestSubscribeWithHandle_SecurityFunc_Rejects(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	handle := newSubscribeHandle()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	called := false
	err := subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { called = true; return nil },
		SubscribeOptions[sensorReading]{
			SecurityFunc: func(_ context.Context, _ *sensorReading, _ []route.SecurityRequirement) error {
				return errors.New("rejected")
			},
		})
	if err != nil {
		// SubscribeWithHandle's own loop returns nil on ctx-timeout; the
		// rejection is delivered via OnError, not the return value —
		// tolerate either.
		t.Logf("SubscribeWithHandle returned: %v", err)
	}
	if called {
		t.Fatal("handler must not run when SecurityFunc rejects")
	}
}

// ── Observability ────────────────────────────────────────────────────────────────

func TestObservability_WrapsSubscribeHandler(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	obs := &testObserver{}
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, Observability[sensorReading](obs))
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	builtHandle, err := sub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var called bool
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, builtHandle,
		func(_ context.Context, _ sensorReading) error { called = true; return nil },
		SubscribeOptions[sensorReading]{})
	if !called {
		t.Fatal("handler wrapped by Observability must still run")
	}
}

func TestObservability_PublishSide(t *testing.T) {
	sock := &mockSocket{}
	obs := &testObserver{}
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	pub := sensorChannel("sensors/readings").WithPublish(events.Publish{}).
		PublishMW(nil, Observability[sensorReading](obs))
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 5.0}
	if err := publish(context.Background(), sock, handle, reading, nil, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(sock.sentSnapshot()) != 1 {
		t.Fatalf("expected 1 send through the Observability wrapper, got %d", len(sock.sentSnapshot()))
	}
}

// ── stats import guard (used by testObserver in adapter_test.go; keeps
// this file's own stats import live even if the above tests change) ──────────
var _ stats.Observer = (*testObserver)(nil)

// ── Example (pkg.go.dev documentation) ────────────────────────────────────────

// ExampleSubscribe demonstrates the value-based caller/subscribe workflow:
// build a caller bundling a FramedSocket with an optional events.Client
// registry, declare a Subscriber, and hand it plus a handler func straight
// to subscribe.
func ExampleSubscribe() {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	caller := newCaller(sock, nil) // nil = no AsyncAPI spec
	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{Summary: "Sensor readings"})

	ctx, cancel := context.WithCancel(context.Background())
	err := subscribe(ctx, caller, sub,
		func(_ context.Context, r sensorReading) error {
			fmt.Printf("received reading from %s: %.1f\n", r.SensorID, r.Value)
			cancel() // stop the blocking receive loop after the one message
			return nil
		}, SubscribeOptions[sensorReading]{})
	if err != nil && err != context.Canceled {
		fmt.Println("subscribe error:", err)
	}

	// Output:
	// received reading from f47ac10b-58cc-4372-a567-0e02b2c3d479: 22.5
}
