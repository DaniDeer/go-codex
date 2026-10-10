package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
)

// This file tests docs/design/d-0006-protocol-native-capabilities.md's
// Phase 4e (Stage D): [events.Client.Subscribe]/[events.Client.Publish]'s
// reflection shim (transport.go) now runs the FULL [subscribeHandler][T]/
// [publish][T] pipeline (Implementations-based SubscribeMW/PublishMW
// security, codec-backed Middleware dispatch (via .Use()/SubscribeBoundMW/
// PublishBoundMW), general-purpose
// wrapping, per-call format overrides) — not just Capabilities
// (Phase 4c). zeromq has NO property-vocabulary axis and NO built-in
// codec-based credential check (unlike mqtt5) — those 2 pipeline steps
// simply don't exist for this adapter, matching adapter.go's own
// narrower real pipeline. Each test exercises ONE pipeline step via the
// real Client.Attach + Client.Subscribe/Publish path (never the lower
// [subscribeWithHandle]/[publish] escape hatch).

func attachedTestClient(t *testing.T, sock *mockSocket) *events.Client {
	t.Helper()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return c
}

func newMockSocketWithReading(payload []byte, topic string) *mockSocket {
	return &mockSocket{inFrames: [][][]byte{{[]byte(topic), payload}}}
}

// ── Subscribe-side ──────────────────────────────────────────────────────────

// TestClientSubscribe_SubscribeMW_SecurityImpl_Reject proves a
// declarative SubscribeMW-paired security implementation now runs
// through Client.Subscribe.
func TestClientSubscribe_SubscribeMW_SecurityImpl_Reject(t *testing.T) {
	payload := []byte(validSensorJSON)
	sock := newMockSocketWithReading(payload, "sensors/readings")
	c := attachedTestClient(t, sock)

	var gotErr SubscribeError
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func(_ context.Context, _ *sensorReading, _ []events.SecurityRequirement) error {
			return errors.New("denied")
		}).
		WithOptions(SubscribeOptions[sensorReading]{OnError: func(e SubscribeError) { gotErr = e }})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil }); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	if gotErr.Kind != KindSecurity {
		t.Fatalf("want KindSecurity, got %v (err=%v)", gotErr.Kind, gotErr.Err)
	}
}

// TestClientSubscribe_SubscribeMW_SecurityImpl_RunsBeforeHandler proves
// a PASSING SubscribeMW security implementation lets the handler run.
func TestClientSubscribe_SubscribeMW_SecurityImpl_RunsBeforeHandler(t *testing.T) {
	payload := []byte(validSensorJSON)
	sock := newMockSocketWithReading(payload, "sensors/readings")
	c := attachedTestClient(t, sock)

	var order []string
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func(_ context.Context, _ *sensorReading, _ []events.SecurityRequirement) error {
			order = append(order, "security")
			return nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
		order = append(order, "handler")
		return nil
	})

	if len(order) != 2 || order[0] != "security" || order[1] != "handler" {
		t.Errorf("want order [security, handler], got %v", order)
	}
}

// TestClientSubscribe_GeneralPurposeMW_WrapsHandler proves a
// general-purpose SubscribeMW-attached Fn wraps the handler through
// Client.Subscribe.
func TestClientSubscribe_GeneralPurposeMW_WrapsHandler(t *testing.T) {
	payload := []byte(validSensorJSON)
	sock := newMockSocketWithReading(payload, "sensors/readings")
	c := attachedTestClient(t, sock)

	var order []string
	wrap := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			order = append(order, "wrap-before")
			err := next(ctx, r)
			order = append(order, "wrap-after")
			return err
		}
	}
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, wrap)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
		order = append(order, "handler")
		return nil
	})

	want := []string{"wrap-before", "handler", "wrap-after"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

// TestClientSubscribe_MiddlewareDispatch_RunsBeforeHandler proves a
// codec-backed Middleware attached via .Use()/SubscribeBoundMW now dispatches
// through Client.Subscribe, before the handler runs.
func TestClientSubscribe_MiddlewareDispatch_RunsBeforeHandler(t *testing.T) {
	payload := []byte(validSensorJSON)
	sock := newMockSocketWithReading(payload, "sensors/readings")
	c := attachedTestClient(t, sock)

	var order []string
	bm := events.NewBoundSubscribeMiddleware(newTDEmptyDeclaration("full-pipeline-policy"),
		func(_ context.Context, _ *sensorReading, _ tdEmpty) (tdEmpty, error) {
			order = append(order, "middleware")
			return tdEmpty{}, nil
		})
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	sub = sub.SubscribeBoundMW(bm)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
		order = append(order, "handler")
		return nil
	})

	if len(order) != 2 || order[0] != "middleware" || order[1] != "handler" {
		t.Errorf("want dispatch order [middleware, handler], got %v", order)
	}
}

// TestClientSubscribe_FormatOverride proves a per-call
// [events.ClientSubscribeOptions.Formats] override is now honored.
func TestClientSubscribe_FormatOverride(t *testing.T) {
	jsonFmt := format.JSON(sensorCodec)
	payload, _ := jsonFmt.Marshal(sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1})
	sock := newMockSocketWithReading(payload, "sensors/readings")
	c := attachedTestClient(t, sock)

	callCount := 0
	overrideFmt := format.NewTyped(sensorCodec,
		func(r sensorReading) ([]byte, error) { return jsonFmt.Marshal(r) },
		func(b []byte) (sensorReading, error) {
			callCount++
			return jsonFmt.Unmarshal(b)
		},
		"application/x-custom",
	)

	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil },
		events.ClientSubscribeOptions{Formats: []format.Format[sensorReading]{overrideFmt}})

	if callCount != 1 {
		t.Errorf("want override format's Unmarshal called once, got %d", callCount)
	}
}

// TestClientSubscribe_MalformedImplementationFn_ReturnsShapeErrorEagerly
// proves a malformed SubscribeMW Fn fails LOUDLY and IMMEDIATELY.
func TestClientSubscribe_MalformedImplementationFn_ReturnsShapeErrorEagerly(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func() {}) // wrong shape entirely

	err := c.Subscribe(context.Background(), sub, func(_ context.Context, _ sensorReading) error { return nil })
	var shapeErr events.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want events.MiddlewareShapeError, got %v (%T)", err, err)
	}
}

// ── Publish-side ─────────────────────────────────────────────────────────────

// TestClientPublish_PublishMW_SecurityImpl_ValidFormat_Passes proves a
// PublishMW-paired security implementation now runs through
// Client.Publish.
func TestClientPublish_PublishMW_SecurityImpl_ValidFormat_Passes(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	implCalled := false
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		PublishMW(nil, func(_ context.Context, _ *sensorReading, _ []events.SecurityRequirement) error {
			implCalled = true
			return nil
		})

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !implCalled {
		t.Error("want PublishMW-paired implementation called")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
}

// TestClientPublish_PublishMW_SecurityImpl_Reject proves a rejecting
// PublishMW-paired security implementation blocks the publish.
func TestClientPublish_PublishMW_SecurityImpl_Reject(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		PublishMW(nil, func(_ context.Context, _ *sensorReading, _ []events.SecurityRequirement) error {
			return errors.New("denied")
		})

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading)
	if err == nil {
		t.Fatal("want an error from the rejecting PublishMW implementation")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 0 {
		t.Error("want no message actually sent when the security impl rejects")
	}
}

// TestClientPublish_GeneralPurposeMW_WrapsEncodeAndTransmit proves a
// general-purpose PublishMW-attached Fn wraps the "encode and transmit"
// step through Client.Publish.
func TestClientPublish_GeneralPurposeMW_WrapsEncodeAndTransmit(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	var order []string
	wrap := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			order = append(order, "wrap-before")
			err := next(ctx, r)
			order = append(order, "wrap-after")
			return err
		}
	}
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, wrap)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(order) != 2 || order[0] != "wrap-before" || order[1] != "wrap-after" {
		t.Errorf("order = %v, want [wrap-before, wrap-after]", order)
	}
}

// TestClientPublish_MiddlewareDispatch_RunsBeforeSend proves a
// codec-backed Middleware attached via .Use()/PublishBoundMW now
// dispatches through Client.Publish.
func TestClientPublish_MiddlewareDispatch_RunsBeforeSend(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	mwCalled := false
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("full-pipeline-out-policy"),
		func(_ context.Context, _ sensorReading) (tdEmpty, error) {
			mwCalled = true
			return tdEmpty{}, nil
		})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	pub = pub.PublishBoundMW(bm)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !mwCalled {
		t.Error("want codec-backed middleware Fn called")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sock.sentFrames))
	}
}

// TestClientPublish_FormatOverride proves a per-call
// [events.ClientPublishOptions.Formats] override is now honored.
func TestClientPublish_FormatOverride(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	callCount := 0
	jsonFmt := format.JSON(sensorCodec)
	overrideFmt := format.NewTyped(sensorCodec,
		func(r sensorReading) ([]byte, error) {
			callCount++
			return jsonFmt.Marshal(r)
		},
		func(b []byte) (sensorReading, error) { return jsonFmt.Unmarshal(b) },
		"application/x-custom",
	)

	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading,
		events.ClientPublishOptions{Formats: []format.Format[sensorReading]{overrideFmt}})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if callCount != 1 {
		t.Errorf("want override format's Marshal called once, got %d", callCount)
	}
}

// TestClientPublish_MalformedImplementationFn_ReturnsShapeErrorEagerly
// proves a malformed PublishMW Fn fails LOUDLY and IMMEDIATELY.
func TestClientPublish_MalformedImplementationFn_ReturnsShapeErrorEagerly(t *testing.T) {
	sock := &mockSocket{}
	c := attachedTestClient(t, sock)

	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, func() {}) // wrong shape entirely

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading)
	var shapeErr events.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want events.MiddlewareShapeError, got %v (%T)", err, err)
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 0 {
		t.Error("want no message sent when shape validation fails eagerly")
	}
}
