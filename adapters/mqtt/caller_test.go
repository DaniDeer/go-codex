package mqtt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared channel type for this file ─────────────────────────────────────────

type sensorReading struct {
	SensorID string
	Value    float64
}

var sensorCodec = codex.Struct[sensorReading](
	codex.RequiredField("sensorID", codex.String().Refine(validate.UUID),
		func(r sensorReading) string { return r.SensorID },
		func(r *sensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value", codex.Float64(),
		func(r sensorReading) float64 { return r.Value },
		func(r *sensorReading, v float64) { r.Value = v },
	),
)

// ── Caller / newCaller ────────────────────────────────────────────────────────

// var _ events.SubscriberServer = (*caller)(nil) is also
// asserted in caller.go itself — repeated here colocated with this file's
// tests.
var _ events.SubscriberServer = (*caller)(nil)

func TestNewCaller_NilEventsClient_IsSpecFree(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := caller.ServeSubscribers(ctx); err != nil {
		t.Fatalf("unexpected error on spec-free ServeSubscribers: %v", err)
	}
}

// ── Subscribe (value-based), built on the UNCHANGED SubscribeHandler primitive ─

func TestSubscribe_ValueBased_DeliversMessage(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{Summary: "test"})

	var received sensorReading
	var mu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribe(ctx, caller, sub, 1,
		func(_ context.Context, r sensorReading) error {
			mu.Lock()
			received = r
			mu.Unlock()
			return nil
		}, SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	handler := client.subscribedHandlerSnapshot()
	if handler == nil {
		t.Fatal("expected client.Subscribe to have been called with a handler")
	}
	handler(client, &mockMessage{topic: "sensors/readings",
		payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})

	mu.Lock()
	defer mu.Unlock()
	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("want delivered sensorID, got %+v", received)
	}
}

// ── TopicFilter regression: templated topic derives a wildcard broker filter ─

// filterCheckingClient is a mock [pahomqtt.Client] that REJECTS the
// permissive-mock shortcut this regression test explicitly must avoid: it
// stores each Subscribe call's filter, and Publish DELIVERS a message to a
// subscription ONLY if the concrete topic actually matches the stored
// filter using REAL single-level MQTT wildcard semantics ("+" matches
// exactly one topic level; a literal segment must match exactly). A mock
// that merely records the filter string (without dispatching through it)
// would let the pre-fix bug (raw "{sensorID}" sent verbatim) pass this
// test undetected — this mock's Publish only succeeds via genuine
// wildcard matching, so it fails loudly if [subscribe] ever
// regresses to sending the raw template.
type filterCheckingClient struct {
	mu   sync.Mutex
	subs map[string]pahomqtt.MessageHandler
}

func newFilterCheckingClient() *filterCheckingClient {
	return &filterCheckingClient{subs: make(map[string]pahomqtt.MessageHandler)}
}

func (c *filterCheckingClient) IsConnected() bool       { return true }
func (c *filterCheckingClient) IsConnectionOpen() bool  { return true }
func (c *filterCheckingClient) Connect() pahomqtt.Token { return newCompletedToken(nil) }
func (c *filterCheckingClient) Disconnect(_ uint)       {}
func (c *filterCheckingClient) Subscribe(filter string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.subs[filter] = handler
	c.mu.Unlock()
	return newCompletedToken(nil)
}
func (c *filterCheckingClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newCompletedToken(nil)
}
func (c *filterCheckingClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newCompletedToken(nil) }
func (c *filterCheckingClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *filterCheckingClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}
func (c *filterCheckingClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	for filter, handler := range c.subs {
		if matchMQTTSingleLevelWildcard(filter, topic) {
			b, _ := payload.([]byte)
			handler(c, &mockMessage{topic: topic, payload: b})
		}
	}
	return newCompletedToken(nil)
}

// matchMQTTSingleLevelWildcard reports whether topic matches filter using
// ONLY the single-level "+" wildcard (sufficient for this regression test
// — deriveWildcardFilter never emits "#"). A REAL broker's matching rule:
// same number of segments, each segment either identical or filter
// segment is "+".
func matchMQTTSingleLevelWildcard(filter, topic string) bool {
	fSegs := strings.Split(filter, "/")
	tSegs := strings.Split(topic, "/")
	if len(fSegs) != len(tSegs) {
		return false
	}
	for i, f := range fSegs {
		if f != "+" && f != tSegs[i] {
			return false
		}
	}
	return true
}

func TestSubscribe_TopicFilter_DerivesWildcardFromTemplate(t *testing.T) {
	client := newFilterCheckingClient()
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/{sensorID}/data", sensorCodec,
		events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	)
	sub := ch.WithSubscribe(events.Subscribe{})

	var received sensorReading
	var mu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribe(ctx, caller, sub, 1,
		func(_ context.Context, r sensorReading) error {
			mu.Lock()
			received = r
			mu.Unlock()
			return nil
		}, SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Publish to a CONCRETE topic — only dispatched by filterCheckingClient
	// if the derived filter genuinely wildcard-matches it.
	client.Publish("sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/data", 1, false,
		[]byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`))

	mu.Lock()
	defer mu.Unlock()
	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("want message delivered via wildcard-matched filter, got %+v (regression: raw {sensorID} template sent verbatim, never matches a real broker topic)", received)
	}
}

func TestSubscribe_TopicFilter_ExplicitOverrideWins(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/{sensorID}/data", sensorCodec,
		events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	)
	sub := ch.WithSubscribe(events.Subscribe{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribe(ctx, caller, sub, 1,
		func(context.Context, sensorReading) error { return nil },
		SubscribeOptions{TopicFilter: "sensors/#"}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got := client.subscribedTopicSnapshot(); got != "sensors/#" {
		t.Fatalf("want explicit override filter %q, got %q", "sensors/#", got)
	}
}

// ── ServeSubscribers / serveOneSubscriber ─────────────────────────────────────

func TestServeSubscribers_SubscribesEveryEntryAndBlocks(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(client, ev)

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	var mu sync.Mutex
	var received sensorReading
	sub := ch.WithSubscribe(events.Subscribe{}).WithHandler(func(_ context.Context, r sensorReading) error {
		mu.Lock()
		received = r
		mu.Unlock()
		return nil
	})
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	// Wait for the subscription to be wired, then deliver a message
	// directly through the recorded handler.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			h(client, &mockMessage{topic: "sensors/readings",
				payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.5}`)})
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := <-done; err != nil {
		t.Fatalf("ServeSubscribers: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("want message dispatched via reflect-based ServeSubscribers, got %+v", received)
	}
}

// TestServeSubscribers_SecurityRejection_CallsSecurityObserver is a
// regression test for a finding from the pubsub-workflow-simplification
// consistency review: ServeSubscribers's security-rejection path must
// call the guarded stats.SecurityObserver.RecordSecurityRejection, the
// same way adapters/mqtt5's and adapters/zeromq's identical code paths
// already do — it must not only call obs.RecordSubscribe(false).
func TestServeSubscribers_SecurityRejection_CallsSecurityObserver(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(client, ev)

	rejectingMW := events.FromSecurityScheme("apiKeyAuth",
		events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")}, nil)
	securityFn := func(_ context.Context, _ pahomqtt.Message, _ *sensorReading) (map[string][]string, error) {
		return nil, errors.New("unauthorized")
	}

	ch := events.NewChannel[sensorReading]("sensors/secured", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{
		Security: []route.SecurityRequirement{route.Require("apiKeyAuth")},
	}).Use(rejectingMW).
		SubscribeMW(&rejectingMW, securityFn).
		WithHandler(func(context.Context, sensorReading) error {
			t.Fatal("handler must not be called when security rejects")
			return nil
		})
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	obs := &mockSecurityObserver{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- caller.ServeSubscribers(stats.WithObserver(ctx, obs))
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			h(client, &mockMessage{topic: "sensors/secured",
				payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.5}`)})
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-done

	if obs.location != "sensors/secured" {
		t.Errorf("want SecurityObserver.RecordSecurityRejection location=sensors/secured, got %q", obs.location)
	}
	if obs.scheme != "apiKeyAuth" {
		t.Errorf("want SecurityObserver.RecordSecurityRejection scheme=apiKeyAuth, got %q", obs.scheme)
	}
}

func TestServeOneSubscriber_ServesSingleChannel(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/one", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{})

	var mu sync.Mutex
	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serveOneSubscriber(ctx, caller, sub, 1,
			func(_ context.Context, r sensorReading) error {
				mu.Lock()
				received = r
				mu.Unlock()
				return nil
			}, SubscribeOptions{})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			h(client, &mockMessage{topic: "sensors/one",
				payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":9}`)})
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := <-done; err != nil {
		t.Fatalf("serveOneSubscriber: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("want message delivered, got %+v", received)
	}
}

// ── Publish-side CredentialFunc (NEW capability) ──────────────────────────────

func TestPublish_CredentialFunc_WritesIntoPayload(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Security: []route.SecurityRequirement{route.Require("apiKey")}}).
		Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	opts := PublishOptions[userEvent]{
		CredentialFunc: func(_ context.Context, msg *userEvent, reqs []route.SecurityRequirement) error {
			if len(reqs) == 0 {
				t.Fatal("want non-empty security requirements passed to CredentialFunc")
			}
			// Embed a credential AS AN ORDINARY PAYLOAD FIELD — MQTT 3.1.1 has
			// no protocol-native attach point, unlike mqtt5's UserProperties.
			msg.Email = "credentialed-" + msg.Email
			return nil
		},
	}
	if err := publish(context.Background(), client, handle, 1, false, event, nil, opts); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !strings.Contains(string(client.publishedPayloadSnapshot()), "credentialed-alice@example.com") {
		t.Fatalf("want CredentialFunc-mutated payload, got %s", client.publishedPayloadSnapshot())
	}
}

func TestPublish_CredentialFunc_ErrorAborts(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Security: []route.SecurityRequirement{route.Require("apiKey")}}).
		Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	wantErr := errors.New("bad credential")
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	opts := PublishOptions[userEvent]{
		CredentialFunc: func(context.Context, *userEvent, []route.SecurityRequirement) error {
			return wantErr
		},
	}
	if err := publish(context.Background(), client, handle, 1, false, event, nil, opts); !errors.Is(err, wantErr) {
		t.Fatalf("want wantErr, got %v", err)
	}
	if len(client.publishedTopicsSnapshot()) != 0 {
		t.Fatal("want no broker Publish call when CredentialFunc rejects")
	}
}

// ── Eager shape-validation rejection: SubscribeMW / PublishMW ─────────────────

func TestSubscribeMW_WrongShape_RejectedEagerly(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, func(string) string { return "" }) // wrong shape entirely

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := subscribe(ctx, caller, sub, 1,
		func(context.Context, sensorReading) error { return nil },
		SubscribeOptions{})
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %v", err)
	}
}

func TestPublishMW_WrongShape_RejectedEagerly(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	pub := ch.WithPublish(events.Publish{}).
		PublishMW(nil, func(int) int { return 0 }) // wrong shape entirely
	handle, err := pub.Handle(ev)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}
	err = publish(context.Background(), client, handle, 1, false, reading, nil,
		PublishOptions[sensorReading]{})
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %v", err)
	}
}

// ── Observability helper ──────────────────────────────────────────────────────

type spyObs struct {
	stats.NoopObserver
	subs, pubs int
}

func (s *spyObs) RecordSubscribe(topic string, success bool, dur time.Duration) { s.subs++ }
func (s *spyObs) RecordPublish(topic string, success bool, dur time.Duration)   { s.pubs++ }

func TestObservability_RecordsSubscribeAndPublish(t *testing.T) {
	obs := &spyObs{}

	// No MessageFromContext in ctx -> publish direction (RecordPublish).
	// See TestObservability_RecordsSubscribe below for the subscribe-side case.
	subFn := Observability[userEvent]("user/created", obs)(
		func(context.Context, userEvent) error { return nil },
	)
	if err := subFn(context.Background(), userEvent{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.pubs != 1 {
		t.Fatalf("want 1 RecordPublish call (no MessageFromContext in ctx -> publish direction), got subs=%d pubs=%d", obs.subs, obs.pubs)
	}
}

// TestObservability_RecordsSubscribe exercises the SUBSCRIBE direction
// end-to-end: attached via SubscribeMW (unpaired), it sees the
// MessageFromContext-populated ctx that [SubscribeHandler] always sets
// before calling fn, so it must record via RecordSubscribe, not
// RecordPublish.
func TestObservability_RecordsSubscribe(t *testing.T) {
	obs := &spyObs{}
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/obs", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, Observability[sensorReading]("sensors/obs", obs))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribe(ctx, caller, sub, 1,
		func(context.Context, sensorReading) error { return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	handler := client.subscribedHandlerSnapshot()
	if handler == nil {
		t.Fatal("expected client.Subscribe to have been called with a handler")
	}
	handler(client, &mockMessage{topic: "sensors/obs",
		payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1}`)})

	if obs.subs != 1 {
		t.Fatalf("want 1 RecordSubscribe call (MessageFromContext present -> subscribe direction), got subs=%d pubs=%d", obs.subs, obs.pubs)
	}
}

// ── Example (pkg.go.dev documentation) ────────────────────────────────────────

// ExampleSubscribe demonstrates the value-based Caller/Subscribe workflow:
// build a Caller bundling an already-connected client with an events.Client
// registry, declare a Subscriber, and hand it plus a handler func straight
// to Subscribe — no manual SubscribeHandler-closure-plus-client.Subscribe
// wiring required.
func ExampleSubscribe() {
	client := &mockClient{token: newCompletedToken(nil)}
	caller := newCaller(client, nil)

	ch := events.NewChannel[sensorReading]("sensors/readings", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{Summary: "Sensor readings"})

	ctx := context.Background()
	err := subscribe(ctx, caller, sub, 1,
		func(_ context.Context, r sensorReading) error {
			fmt.Printf("received reading from %s: %.1f\n", r.SensorID, r.Value)
			return nil
		}, SubscribeOptions{})
	if err != nil {
		fmt.Println("subscribe error:", err)
		return
	}

	// Simulate the broker delivering one message (a real broker connection
	// would invoke this automatically once subscribed).
	handler := client.subscribedHandlerSnapshot()
	handler(client, &mockMessage{topic: "sensors/readings",
		payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`)})

	// Output:
	// received reading from f47ac10b-58cc-4372-a567-0e02b2c3d479: 22.5
}

// ── Regression: pre-existing SubscribeHandler primitive stays UNCHANGED ───────

// TestSubscribeHandler_PreExisting_StillWorksIdentically exercises the OLD,
// pre-existing SubscribeHandler usage pattern directly (build the closure,
// wire it into client.Subscribe by hand) — EXACTLY the pattern
// examples/adapters-mqtt-security/main.go and this package's own
// pre-existing tests already use — proving the internal caller/
// subscribe wrapping logic does not alter
// SubscribeHandler's own behavior in any way (subscribe/ServeSubscribers
// are built ON TOP of it, never modifying it).
func TestSubscribeHandler_PreExisting_StillWorksIdentically(t *testing.T) {
	handle := newHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	var received userEvent
	handler := subscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error { received = e; return nil },
		SubscribeOptions{},
	)
	// The caller wires the closure into client.Subscribe BY HAND — unchanged
	// from every pre-existing call site in this package/examples.
	token := client.Subscribe(handle.Topic, 1, handler)
	token.Wait()
	if token.Error() != nil {
		t.Fatalf("unexpected broker error: %v", token.Error())
	}

	handler(client, &mockMessage{payload: []byte(validPayload)})
	if received.ID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("want decoded event via unchanged SubscribeHandler, got %+v", received)
	}
}
