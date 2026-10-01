package mqtt

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// This file tests docs/design/d-0006-protocol-native-capabilities.md's
// Phase 4e (Stage D): [events.Client.Subscribe]/[events.Client.Publish]'s
// reflection shim (transport.go) now runs the FULL [subscribeHandler][T]/
// [publish][T] pipeline (Implementations-based SubscribeMW/PublishMW
// security, codec-backed Middleware/Transform dispatch, general-purpose
// wrapping, per-call format overrides) — not just Capabilities
// (Phase 4c). mqtt v3 has NO property-vocabulary axis and NO built-in
// codec-based credential check (unlike mqtt5) — those 2 pipeline steps
// simply don't exist for this adapter, matching adapter.go's own
// narrower real pipeline. Each test exercises ONE pipeline step via the
// real Client.Attach + Client.Subscribe/Publish path.

func attachedTestClient(t *testing.T, client *mockClient) *events.Client {
	t.Helper()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return c
}

// dispatchSubscribed waits (up to 1s) for client's subscribed handler to
// be registered, then invokes it with msg — the shared helper every
// subscribe-side test below uses to simulate an incoming broker message.
func dispatchSubscribed(client *mockClient, msg *mockMessage) {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			h(client, msg)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ── Subscribe-side ──────────────────────────────────────────────────────────

// TestClientSubscribe_SubscribeMW_SecurityImpl_Reject proves a
// declarative SubscribeMW-paired security implementation now runs
// through Client.Subscribe.
func TestClientSubscribe_SubscribeMW_SecurityImpl_Reject(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var mu sync.Mutex
	var gotErr SubscribeError
	sub := plainSensorChannel("sensors/readings").
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func(_ context.Context, _ pahomqtt.Message, _ *sensorReading) (map[string][]string, error) {
			return nil, errors.New("denied")
		}).
		WithOptions(SubscribeOptions{OnError: func(e SubscribeError) { mu.Lock(); gotErr = e; mu.Unlock() }})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil }) }()

	dispatchSubscribed(client, &mockMessage{topic: "sensors/readings", payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
	<-done

	mu.Lock()
	defer mu.Unlock()
	if gotErr.Kind != KindSecurity {
		t.Fatalf("want KindSecurity, got %v (err=%v)", gotErr.Kind, gotErr.Err)
	}
}

// TestClientSubscribe_SubscribeMW_SecurityImpl_RunsBeforeHandler proves a
// PASSING SubscribeMW security implementation lets the handler run.
func TestClientSubscribe_SubscribeMW_SecurityImpl_RunsBeforeHandler(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var mu sync.Mutex
	var order []string
	sub := plainSensorChannel("sensors/readings").
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func(_ context.Context, _ pahomqtt.Message, _ *sensorReading) (map[string][]string, error) {
			mu.Lock()
			order = append(order, "security")
			mu.Unlock()
			return nil, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			mu.Lock()
			order = append(order, "handler")
			mu.Unlock()
			return nil
		})
	}()

	dispatchSubscribed(client, &mockMessage{topic: "sensors/readings", payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "security" || order[1] != "handler" {
		t.Errorf("want order [security, handler], got %v", order)
	}
}

// TestClientSubscribe_GeneralPurposeMW_WrapsHandler proves a
// general-purpose SubscribeMW-attached Fn wraps the handler through
// Client.Subscribe.
func TestClientSubscribe_GeneralPurposeMW_WrapsHandler(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var mu sync.Mutex
	var order []string
	wrap := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			mu.Lock()
			order = append(order, "wrap-before")
			mu.Unlock()
			err := next(ctx, r)
			mu.Lock()
			order = append(order, "wrap-after")
			mu.Unlock()
			return err
		}
	}
	sub := plainSensorChannel("sensors/readings").
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, wrap)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			mu.Lock()
			order = append(order, "handler")
			mu.Unlock()
			return nil
		})
	}()

	dispatchSubscribed(client, &mockMessage{topic: "sensors/readings", payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
	<-done

	mu.Lock()
	defer mu.Unlock()
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
// codec-backed Middleware (Transform) attached via .Use() now dispatches
// through Client.Subscribe, before the handler runs.
func TestClientSubscribe_MiddlewareDispatch_RunsBeforeHandler(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var mu sync.Mutex
	var order []string
	emw := newTDEmptyDeclaration("full-pipeline-policy")
	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{Summary: "test"})
	sub = events.Transform(sub, emw, func(_ context.Context, _ *sensorReading, _ tdEmpty) error {
		mu.Lock()
		order = append(order, "middleware")
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			mu.Lock()
			order = append(order, "handler")
			mu.Unlock()
			return nil
		})
	}()

	dispatchSubscribed(client, &mockMessage{topic: "sensors/readings", payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "middleware" || order[1] != "handler" {
		t.Errorf("want dispatch order [middleware, handler], got %v", order)
	}
}

// TestClientSubscribe_FormatOverride proves a per-call
// [events.ClientSubscribeOptions.Formats] override is now honored.
func TestClientSubscribe_FormatOverride(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var mu sync.Mutex
	callCount := 0
	jsonFmt := format.JSON(sensorCodec)
	overrideFmt := format.NewTyped(sensorCodec,
		func(r sensorReading) ([]byte, error) { return jsonFmt.Marshal(r) },
		func(b []byte) (sensorReading, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return jsonFmt.Unmarshal(b)
		},
		"application/x-custom",
	)

	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{Summary: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil },
			events.ClientSubscribeOptions{Formats: []format.Format[sensorReading]{overrideFmt}})
	}()

	dispatchSubscribed(client, &mockMessage{topic: "sensors/readings", payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
	<-done

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Errorf("want override format's Unmarshal called once, got %d", callCount)
	}
}

// TestClientSubscribe_MalformedImplementationFn_ReturnsShapeErrorEagerly
// proves a malformed SubscribeMW Fn fails LOUDLY and IMMEDIATELY.
// TestClientSubscribe_MessageFromContext_Retrievable is a regression
// test for a REAL, previously-untracked gap found while refactoring
// examples/events-api's escape-hatch demos: the ctx passed to a
// Client.Subscribe handler never carried the raw pahomqtt.Message the
// way the escape hatch (subscribeHandle) always has —
// [MessageFromContext] silently returned (nil, false) through
// Client.Subscribe. Fixed by injecting the SAME context.WithValue call
// adapter.go's handler already does, at the SAME pre-decode placement.
func TestClientSubscribe_MessageFromContext_Retrievable(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var gotMsg pahomqtt.Message
	var gotOK bool
	done := make(chan struct{})
	sub := plainSensorChannel("sensors/readings").WithSubscribe(events.Subscribe{Summary: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(hCtx context.Context, _ sensorReading) error {
			gotMsg, gotOK = MessageFromContext(hCtx)
			close(done)
			return nil
		})
	}()
	dispatchSubscribed(client, &mockMessage{topic: "sensors/readings", payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})
	<-done

	if !gotOK || gotMsg == nil {
		t.Fatal("want MessageFromContext to retrieve the raw pahomqtt.Message through Client.Subscribe")
	}
	if gotMsg.Topic() != "sensors/readings" {
		t.Errorf("want retrieved message Topic() = %q, got %q", "sensors/readings", gotMsg.Topic())
	}
}

func TestClientSubscribe_MalformedImplementationFn_ReturnsShapeErrorEagerly(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	sub := plainSensorChannel("sensors/readings").
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func() {}) // wrong shape entirely

	err := c.Subscribe(context.Background(), sub, func(_ context.Context, _ sensorReading) error { return nil })
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v (%T)", err, err)
	}
}

// ── Publish-side ─────────────────────────────────────────────────────────────

// TestClientPublish_PublishMW_SecurityImpl_ValidFormat_Passes proves a
// PublishMW-paired security implementation now runs through
// Client.Publish.
func TestClientPublish_PublishMW_SecurityImpl_ValidFormat_Passes(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	implCalled := false
	pub := plainSensorChannel("sensors/readings").
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, func(_ context.Context, _ *sensorReading, _ []route.SecurityRequirement) error {
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
	if client.publishedTopicSnapshot() == "" {
		t.Fatal("want 1 published message")
	}
}

// TestClientPublish_PublishMW_SecurityImpl_Reject proves a rejecting
// PublishMW-paired security implementation blocks the publish.
func TestClientPublish_PublishMW_SecurityImpl_Reject(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	pub := plainSensorChannel("sensors/readings").
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, func(_ context.Context, _ *sensorReading, _ []route.SecurityRequirement) error {
			return errors.New("denied")
		})

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading)
	if err == nil {
		t.Fatal("want an error from the rejecting PublishMW implementation")
	}
	if client.publishedTopicSnapshot() != "" {
		t.Error("want no message actually published when the security impl rejects")
	}
}

// TestClientPublish_GeneralPurposeMW_WrapsEncodeAndTransmit proves a
// general-purpose PublishMW-attached Fn wraps the "encode and transmit"
// step through Client.Publish.
func TestClientPublish_GeneralPurposeMW_WrapsEncodeAndTransmit(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	var order []string
	wrap := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			order = append(order, "wrap-before")
			err := next(ctx, r)
			order = append(order, "wrap-after")
			return err
		}
	}
	pub := plainSensorChannel("sensors/readings").
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
// codec-backed Middleware (ClientTransform) attached via .Use() now
// dispatches through Client.Publish.
func TestClientPublish_MiddlewareDispatch_RunsBeforeSend(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	mwCalled := false
	emw := newTDEmptyDeclaration("full-pipeline-out-policy")
	pub := plainSensorChannel("sensors/readings").WithPublish(events.Publish{Summary: "test"})
	pub = events.ClientTransform(pub, emw, func(_ context.Context, _ sensorReading) (tdEmpty, error) {
		mwCalled = true
		return tdEmpty{}, nil
	})

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !mwCalled {
		t.Error("want codec-backed middleware Fn called")
	}
	if client.publishedTopicSnapshot() == "" {
		t.Fatal("want 1 published message")
	}
}

// TestClientPublish_FormatOverride proves a per-call
// [events.ClientPublishOptions.Formats] override is now honored.
func TestClientPublish_FormatOverride(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

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

	pub := plainSensorChannel("sensors/readings").WithPublish(events.Publish{Summary: "test"})
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
	client := &mockClient{token: newCompletedToken(nil)}
	c := attachedTestClient(t, client)

	pub := plainSensorChannel("sensors/readings").
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, func() {}) // wrong shape entirely

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading)
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v (%T)", err, err)
	}
	if client.publishedTopicSnapshot() != "" {
		t.Error("want no message published when shape validation fails eagerly")
	}
}
