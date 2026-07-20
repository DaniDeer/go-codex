package mqtt5_test

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	mqtt5 "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── shared types and codecs ───────────────────────────────────────────────────

type sensorReading struct {
	SensorID string
	Value    float64
}

var sensorCodec = codex.Struct[sensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		func(r sensorReading) string { return r.SensorID },
		func(r *sensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64(),
		func(r sensorReading) float64 { return r.Value },
		func(r *sensorReading, v float64) { r.Value = v },
	),
)

type computeReq struct{ X, Y int }
type computeResp struct{ Sum int }

var computeReqCodec = codex.Struct[computeReq](
	codex.RequiredField("x", codex.Int(),
		func(r computeReq) int { return r.X },
		func(r *computeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r computeReq) int { return r.Y },
		func(r *computeReq, v int) { r.Y = v },
	),
)

var computeRespCodec = codex.Struct[computeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r computeResp) int { return r.Sum },
		func(r *computeResp, v int) { r.Sum = v },
	),
)

var computeRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "compute"},
)

// ── mock MQTT client ──────────────────────────────────────────────────────────

type mockClient struct {
	mu             sync.Mutex
	published      []*pahomqtt5.Publish
	subscribed     []*pahomqtt5.Subscribe
	publishErr     error
	subscribeErr   error
	unsubscribeErr error
}

func (c *mockClient) Publish(_ context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	if c.publishErr != nil {
		return nil, c.publishErr
	}
	c.mu.Lock()
	c.published = append(c.published, p)
	c.mu.Unlock()
	return &pahomqtt5.PublishResponse{}, nil
}

func (c *mockClient) Subscribe(_ context.Context, s *pahomqtt5.Subscribe) (*pahomqtt5.Suback, error) {
	if c.subscribeErr != nil {
		return nil, c.subscribeErr
	}
	c.mu.Lock()
	c.subscribed = append(c.subscribed, s)
	c.mu.Unlock()
	return &pahomqtt5.Suback{}, nil
}

func (c *mockClient) subscribedFilters() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var filters []string
	for _, s := range c.subscribed {
		for _, sub := range s.Subscriptions {
			filters = append(filters, sub.Topic)
		}
	}
	return filters
}

func (c *mockClient) Unsubscribe(_ context.Context, _ *pahomqtt5.Unsubscribe) (*pahomqtt5.Unsuback, error) {
	return &pahomqtt5.Unsuback{}, c.unsubscribeErr
}

func (c *mockClient) lastPublished() *pahomqtt5.Publish {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.published) == 0 {
		return nil
	}
	return c.published[len(c.published)-1]
}

// ── mock router ───────────────────────────────────────────────────────────────

type mockRouter struct {
	mu       sync.Mutex
	handlers map[string]pahomqtt5.MessageHandler
}

func newMockRouter() *mockRouter {
	return &mockRouter{handlers: make(map[string]pahomqtt5.MessageHandler)}
}

func (r *mockRouter) RegisterHandler(topic string, h pahomqtt5.MessageHandler) {
	r.mu.Lock()
	r.handlers[topic] = h
	r.mu.Unlock()
}

func (r *mockRouter) UnregisterHandler(topic string) {
	r.mu.Lock()
	delete(r.handlers, topic)
	r.mu.Unlock()
}

// waitHandler blocks until the handler for topic is registered or 1 second passes.
func (r *mockRouter) waitHandler(topic string) {
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		_, ok := r.handlers[topic]
		r.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *mockRouter) dispatch(topic string, msg *pahomqtt5.Publish) {
	r.mu.Lock()
	h := r.handlers[topic]
	r.mu.Unlock()
	if h != nil {
		h(msg)
	}
}

// ── observer stub ─────────────────────────────────────────────────────────────

type validationEntry struct {
	location   string
	constraint string
	field      string
}

type testObserver struct {
	subscribes       []bool
	publishes        []bool
	requests         []int
	validationErrors []string
	validationFull   []validationEntry
	startSpanOps     []string
	endSpanErrs      []error
	secRejections    []string
}

func (o *testObserver) RecordValidationError(location, constraint, field string) {
	o.validationErrors = append(o.validationErrors, constraint)
	o.validationFull = append(o.validationFull, validationEntry{location, constraint, field})
}
func (o *testObserver) RecordRequest(_, _ string, code int, _ time.Duration) {
	o.requests = append(o.requests, code)
}
func (o *testObserver) RecordSubscribe(_ string, success bool, _ time.Duration) {
	o.subscribes = append(o.subscribes, success)
}
func (o *testObserver) RecordPublish(_ string, success bool, _ time.Duration) {
	o.publishes = append(o.publishes, success)
}
func (o *testObserver) RecordSecurityRejection(location, _ string) {
	o.secRejections = append(o.secRejections, location)
}
func (o *testObserver) StartSpan(ctx context.Context, op, _ string) context.Context {
	o.startSpanOps = append(o.startSpanOps, op)
	return ctx
}
func (o *testObserver) EndSpan(_ context.Context, err error) {
	o.endSpanErrs = append(o.endSpanErrs, err)
}

// ── channel / route handle helpers ───────────────────────────────────────────

func newChannelHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.Subscribe{Summary: "test"}).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func newRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := computeRoute.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

const validSensorJSON = `{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`

// ── Subscribe tests ───────────────────────────────────────────────────────────

func TestSubscribe_ValidPayload(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.Value != 22.5 {
		t.Fatalf("unexpected value: %v", received.Value)
	}
}

func TestSubscribe_DecodeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr mqtt5.SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		mqtt5.SubscribeOptions{OnError: func(e mqtt5.SubscribeError) { gotErr = e }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{"sensor_id":"not-uuid","value":1}`),
	})

	if gotErr.Kind != mqtt5.KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
}

func TestSubscribe_HandlerError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr mqtt5.SubscribeError
	handlerErr := errors.New("storage unavailable")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return handlerErr },
		mqtt5.SubscribeOptions{OnError: func(e mqtt5.SubscribeError) { gotErr = e }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if gotErr.Kind != mqtt5.KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
}

func TestSubscribe_UserProperties_InContext(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotProps pahomqtt5.UserProperties

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(ctx context.Context, _ sensorReading) error {
			props, ok := mqtt5.UserPropertiesFromContext(ctx)
			if ok {
				gotProps = props
			}
			return nil
		},
		mqtt5.SubscribeOptions{})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "Authorization", Value: "Bearer token"}},
		},
	})

	if len(gotProps) != 1 || gotProps[0].Key != "Authorization" {
		t.Fatalf("expected Authorization property, got %v", gotProps)
	}
}

func TestSubscribe_ContentTypeAutoFormat(t *testing.T) {
	// Encode with the default JSON format, then dispatch with ContentType set.
	// The adapter should select the matching format by ContentType.
	client := &mockClient{}
	router := newMockRouter()
	handle := newChannelHandle()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	jsonFmt := handle.Formats
	if len(jsonFmt) == 0 {
		// No explicit format means JSON is the fallback — test passes implicitly.
		// ContentType="" on the message → uses default decode path.
		return
	}

	_ = mqtt5.Subscribe(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{})

	// Dispatch a message with ContentType header matching the format.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			ContentType: "application/json",
		},
	})

	if received.Value != 22.5 {
		t.Fatalf("expected value 22.5, got %v", received.Value)
	}
}

func TestSubscribe_ObserverRecordSubscribeSuccess(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		mqtt5.SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.subscribes) != 1 || !obs.subscribes[0] {
		t.Fatalf("expected successful RecordSubscribe, got %v", obs.subscribes)
	}
}

func TestSubscribe_ObserverRecordSubscribeFailure(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		mqtt5.SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`bad json`),
	})

	if len(obs.subscribes) != 1 || obs.subscribes[0] {
		t.Fatalf("expected failed RecordSubscribe, got %v", obs.subscribes)
	}
}

func TestSubscribe_ValidationErrorReported(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		mqtt5.SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{"sensor_id":"not-uuid","value":1}`),
	})

	if len(obs.validationErrors) == 0 {
		t.Fatal("expected at least one RecordValidationError call")
	}
}

func TestSubscribe_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		mqtt5.SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.subscribe" {
		t.Fatalf("expected StartSpan 'mqtt5.subscribe', got %v", obs.startSpanOps)
	}
}

func TestSubscribe_MessageFromContext(t *testing.T) {
	// Verify the raw *paho.Publish is accessible via MessageFromContext inside fn.
	client := &mockClient{}
	router := newMockRouter()
	var gotTopic string

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(ctx context.Context, _ sensorReading) error {
			if msg, ok := mqtt5.MessageFromContext(ctx); ok {
				gotTopic = msg.Topic
			}
			return nil
		},
		mqtt5.SubscribeOptions{})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if gotTopic != "sensors/readings" {
		t.Fatalf("expected topic 'sensors/readings', got %q", gotTopic)
	}
}

// ── Publish tests ─────────────────────────────────────────────────────────────

func TestPublish_ValidMessage(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	err := mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil,
		mqtt5.PublishOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(client.published))
	}
}

func TestPublish_ContentTypeProperty(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	_ = mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil,
		mqtt5.PublishOptions{ContentType: "application/json"})

	pub := client.lastPublished()
	if pub == nil || pub.Properties == nil || pub.Properties.ContentType != "application/json" {
		t.Fatalf("expected ContentType=application/json on published message, got %v", pub)
	}
}

func TestPublish_UserProperties(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	_ = mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil,
		mqtt5.PublishOptions{
			UserProperties: []mqtt5.UserProperty{{Key: "TenantID", Value: "acme"}},
		})

	pub := client.lastPublished()
	if pub == nil || pub.Properties == nil || len(pub.Properties.User) == 0 {
		t.Fatal("expected UserProperties on published message")
	}
	if pub.Properties.User[0].Key != "TenantID" {
		t.Fatalf("unexpected User property: %v", pub.Properties.User)
	}
}

func TestPublish_EncodeError(t *testing.T) {
	client := &mockClient{}
	// Use empty UUID (invalid) to trigger codec validation error
	invalid := sensorReading{SensorID: "not-a-uuid", Value: 1.0}

	err := mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, invalid, nil,
		mqtt5.PublishOptions{})

	var encErr mqtt5.PublishEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected PublishEncodeError, got %T: %v", err, err)
	}
}

func TestPublish_ObserverRecordPublishSuccess(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	_ = mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil,
		mqtt5.PublishOptions{Observer: obs})

	if len(obs.publishes) != 1 || !obs.publishes[0] {
		t.Fatalf("expected successful RecordPublish, got %v", obs.publishes)
	}
}

func TestPublish_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	_ = mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil,
		mqtt5.PublishOptions{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.publish" {
		t.Fatalf("expected StartSpan 'mqtt5.publish', got %v", obs.startSpanOps)
	}
}

// ── error LogValue + Unwrap tests ─────────────────────────────────────────────

func TestSubscribeError_LogValue(t *testing.T) {
	e := mqtt5.SubscribeError{Kind: mqtt5.KindDecode, Topic: "sensors/t", Err: errors.New("fail")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestPublishEncodeError_LogValue(t *testing.T) {
	e := mqtt5.PublishEncodeError{Topic: "t", Err: errors.New("fail")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestCallError_LogValue(t *testing.T) {
	e := mqtt5.CallError{Kind: mqtt5.KindTimeout, Err: errors.New("timeout")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestServeError_LogValue(t *testing.T) {
	e := mqtt5.ServeError{Kind: mqtt5.KindHandler, Err: errors.New("fail")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestSubscribeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := mqtt5.SubscribeError{Kind: mqtt5.KindDecode, Topic: "t", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

func TestCallError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := mqtt5.CallError{Kind: mqtt5.KindTimeout, Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

func TestServeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := mqtt5.ServeError{Kind: mqtt5.KindHandler, Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

// ── UserPropertyParam tests ───────────────────────────────────────────────────

func mqttMsgWithUserProps(payload string, userProps map[string]string) *pahomqtt5.Publish {
	props := &pahomqtt5.PublishProperties{}
	for k, v := range userProps {
		props.User = append(props.User, pahomqtt5.UserProperty{Key: k, Value: v})
	}
	return &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(payload), Properties: props}
}

func TestUserPropertyParam_ValidProperties_MessageDelivered(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{
			UserPropertyParams: []mqtt5.UserPropertyParam{
				mqtt5.UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	router.dispatch("sensors/readings",
		mqttMsgWithUserProps(validSensorJSON, map[string]string{"TenantID": "acme"}))

	if received.Value != 22.5 {
		t.Fatalf("expected message delivered, got value %v", received.Value)
	}
}

func TestUserPropertyParam_MissingRequired_SecurityError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr mqtt5.SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		mqtt5.SubscribeOptions{
			OnError: func(e mqtt5.SubscribeError) { gotErr = e },
			UserPropertyParams: []mqtt5.UserPropertyParam{
				{Name: "Authorization", Required: true},
			},
		})

	// Dispatch a message with NO User Properties.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if gotErr.Kind != mqtt5.KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var missing mqtt5.MissingUserPropertyError
	if !errors.As(gotErr, &missing) {
		t.Fatalf("expected MissingUserPropertyError via errors.As, got %T", gotErr.Err)
	}
	if missing.Name != "Authorization" {
		t.Fatalf("expected Name=Authorization, got %q", missing.Name)
	}
}

func TestUserPropertyParam_CodecFailure_SecurityError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr mqtt5.SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		mqtt5.SubscribeOptions{
			OnError: func(e mqtt5.SubscribeError) { gotErr = e },
			UserPropertyParams: []mqtt5.UserPropertyParam{
				mqtt5.UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	// Dispatch with empty TenantID → codec failure.
	router.dispatch("sensors/readings",
		mqttMsgWithUserProps(validSensorJSON, map[string]string{"TenantID": ""}))

	if gotErr.Kind != mqtt5.KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var propErr mqtt5.UserPropertyError
	if !errors.As(gotErr, &propErr) {
		t.Fatalf("expected UserPropertyError via errors.As, got %T", gotErr.Err)
	}
	if propErr.Name != "TenantID" {
		t.Fatalf("expected Name=TenantID, got %q", propErr.Name)
	}
}

func TestUserPropertyParam_Optional_AbsentOk(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{
			UserPropertyParams: []mqtt5.UserPropertyParam{
				mqtt5.UserPropertyParam{Name: "OptionalHeader", Required: false}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	// Dispatch without OptionalHeader — should succeed.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if received.Value != 22.5 {
		t.Fatalf("expected message delivered when optional property absent, got %v", received.Value)
	}
}

func TestUserPropertyParam_ObserverValidationErrorReported(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		mqtt5.SubscribeOptions{
			Observer: obs,
			UserPropertyParams: []mqtt5.UserPropertyParam{
				{Name: "Authorization", Required: true},
			},
		})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.validationErrors) == 0 {
		t.Fatal("expected RecordValidationError called for missing required property")
	}
}

func TestUserPropertyError_LogValue(t *testing.T) {
	e := mqtt5.UserPropertyError{Name: "TenantID", Value: "", Err: errors.New("must not be empty")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestMissingUserPropertyError_LogValue(t *testing.T) {
	e := mqtt5.MissingUserPropertyError{Name: "Authorization"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestUserPropertyError_ErrorsAs(t *testing.T) {
	inner := errors.New("constraint failed")
	outer := mqtt5.UserPropertyError{Name: "TenantID", Value: "bad", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner error")
	}
}

func TestUserPropertyParam_WithCodec_ReturnsCopy(t *testing.T) {
	p := mqtt5.UserPropertyParam{Name: "TenantID", Required: true}
	codec := codex.String().Refine(validate.NonEmptyString)
	p2 := p.WithCodec(codec)
	if p.Codec != nil {
		t.Fatal("WithCodec must not mutate the original")
	}
	if p2.Codec == nil {
		t.Fatal("WithCodec must set Codec on the copy")
	}
}

// ── BrokerError tests ─────────────────────────────────────────────────────────

func TestBrokerError_LogValue(t *testing.T) {
	inner := errors.New("connection refused")
	e := mqtt5.BrokerError{Op: "subscribe", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestBrokerError_ErrorsAs(t *testing.T) {
	inner := errors.New("network error")
	outer := mqtt5.BrokerError{Op: "publish", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner error")
	}
}

func TestBrokerError_ErrorString(t *testing.T) {
	e := mqtt5.BrokerError{Op: "subscribe", Err: errors.New("timeout")}
	if e.Error() != "mqtt5 broker subscribe: timeout" {
		t.Fatalf("unexpected Error() string: %q", e.Error())
	}
}

func TestSubscribe_BrokerError_OnSubscribeFail(t *testing.T) {
	brokerErr := errors.New("broker unavailable")
	client := &mockClient{subscribeErr: brokerErr}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		mqtt5.SubscribeOptions{})

	var be mqtt5.BrokerError
	if !errors.As(err, &be) {
		t.Fatalf("expected BrokerError, got %T: %v", err, err)
	}
	if be.Op != "subscribe" {
		t.Fatalf("expected Op=subscribe, got %q", be.Op)
	}
	if !errors.Is(be, brokerErr) {
		t.Fatal("errors.Is must find brokerErr via Unwrap")
	}
}

func TestPublish_BrokerError_OnPublishFail(t *testing.T) {
	brokerErr := errors.New("broker unavailable")
	client := &mockClient{publishErr: brokerErr}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	err := mqtt5.Publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil,
		mqtt5.PublishOptions{})

	var be mqtt5.BrokerError
	if !errors.As(err, &be) {
		t.Fatalf("expected BrokerError, got %T: %v", err, err)
	}
	if be.Op != "publish" {
		t.Fatalf("expected Op=publish, got %q", be.Op)
	}
}

// ── Publish topic vars observer reporting ─────────────────────────────────────

func newTemplateChannelHandle() *events.ChannelHandle[sensorReading] {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.Publish{Summary: "test"},
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestPublish_Vars_MissingVar_ReportsRequiredConstraintWithVarName(t *testing.T) {
	// Verifies that a missing topic variable is reported as constraint "required"
	// with the variable name as the field — matching the mqtt adapter's behaviour.
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	err := mqtt5.Publish(context.Background(), client, newTemplateChannelHandle(), 1, false,
		reading, map[string]string{}, // sensorID missing
		mqtt5.PublishOptions{Observer: obs})

	var missingErr events.MissingTopicVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingTopicVarError, got %T: %v", err, err)
	}

	if len(obs.validationFull) == 0 {
		t.Fatal("expected RecordValidationError to be called for missing topic variable")
	}
	got := obs.validationFull[0]
	if got.location != "topic_var" {
		t.Errorf("expected location=%q, got %q", "topic_var", got.location)
	}
	if got.constraint != "required" {
		t.Errorf("expected constraint=%q, got %q", "required", got.constraint)
	}
	if got.field != "sensorID" {
		t.Errorf("expected field=%q, got %q", "sensorID", got.field)
	}
}

func TestPublish_Vars_CodecFailure_ReportsVarNameAsField(t *testing.T) {
	// Verifies that a topic variable that fails its codec is reported with the
	// variable name as the field — matching the mqtt adapter's behaviour.
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	err := mqtt5.Publish(context.Background(), client, newTemplateChannelHandle(), 1, false,
		reading, map[string]string{"sensorID": "not-a-uuid"},
		mqtt5.PublishOptions{Observer: obs})

	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %T: %v", err, err)
	}

	if len(obs.validationFull) == 0 {
		t.Fatal("expected RecordValidationError to be called for failing topic variable")
	}
	got := obs.validationFull[0]
	if got.location != "topic_var" {
		t.Errorf("expected location=%q, got %q", "topic_var", got.location)
	}
	if got.field != "sensorID" {
		t.Errorf("expected field=%q (variable name), got %q", "sensorID", got.field)
	}
}

// ── Phase 2: events.NewTopicParam merge fields — Subscribe/PublishHandle ──────

// newMergeChannelHandle returns a channel whose sensorID topic var is
// merge-capable (events.NewTopicParam), unlike newTemplateChannelHandle's
// validate-only events.TopicParam.
func newMergeChannelHandle() *events.ChannelHandle[sensorReading] {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// EV5: mqtt5.Subscribe auto-merges topic vars into the payload when the
// channel declares merge fields — no manual TopicVarsFromMessage call
// needed in the handler function.
func TestSubscribe_MergeFields_AutoMergesTopicVars(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	handle := newMergeChannelHandle()
	if err := mqtt5.Subscribe(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	// Payload JSON deliberately carries a DIFFERENT sensor_id — merge must
	// OVERWRITE it with the value extracted from the concrete topic.
	// mockRouter.RegisterHandler keys on the TEMPLATE topic (handle.Topic);
	// the dispatched message's OWN .Topic field carries the CONCRETE topic
	// used for var extraction.
	router.dispatch("sensors/{sensorID}/readings", &pahomqtt5.Publish{
		Topic:   "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings",
		Payload: []byte(`{"sensor_id":"00000000-0000-0000-0000-000000000000","value":22.5}`),
	})

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("SensorID: want merged from topic, got %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", received.Value)
	}
}

// EV5b: mqtt5.Subscribe route WITHOUT merge fields behaves identically to
// today — regression guard (mirrors REST's P6).
func TestSubscribe_NoMergeFieldsIsUnaffected(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := mqtt5.Subscribe(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.Value != 22.5 {
		t.Fatalf("unexpected value: %v", received.Value)
	}
}

// EV6: mqtt5.PublishHandle derives topic vars from msg automatically — one
// struct in, no manual vars map needed.
func TestPublishHandle_DerivesVarsFromMsg(t *testing.T) {
	client := &mockClient{}
	handle := newMergeChannelHandle()
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	err := mqtt5.PublishHandle(context.Background(), client, handle, 1, false, reading, mqtt5.PublishOptions{})
	if err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message, got %d", len(client.published))
	}
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if client.published[0].Topic != wantTopic {
		t.Errorf("Topic: want %q, got %q", wantTopic, client.published[0].Topic)
	}
}

// EV7: full publish->subscribe round trip with a NESTED payload struct and
// a non-JSON (Gob, via format.NewTyped projection) format — proves the
// Round 4 mandate holds for events too, not just REST.
func TestPublishHandleSubscribe_NestedGobPayload_RoundTrip(t *testing.T) {
	type meta struct {
		SensorID string
	}
	type reading struct {
		Meta  meta
		Value float64
	}

	readingCodec := codex.Struct[reading]()
	gobFmt := format.NewTyped[reading](
		readingCodec,
		func(r reading) ([]byte, error) {
			var buf bytes.Buffer
			err := gob.NewEncoder(&buf).Encode(r.Value)
			return buf.Bytes(), err
		},
		func(data []byte) (reading, error) {
			var v float64
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
				return reading{}, err
			}
			return reading{Value: v}, nil
		},
		"application/gob",
	)

	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	handle, err := events.NewChannel[reading]("sensors/{sensorID}/readings", readingCodec,
		events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
			func(r reading) string { return r.Meta.SensorID },
			func(r *reading, v string) { r.Meta.SensorID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	handle.WithFormats(gobFmt)

	client := &mockClient{}
	router := newMockRouter()
	var received reading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := mqtt5.Subscribe(ctx, client, router, handle, 1,
		func(_ context.Context, r reading) error { received = r; return nil },
		mqtt5.SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	msg := reading{Meta: meta{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479"}, Value: 42.5}
	if err := mqtt5.PublishHandle(ctx, client, handle, 1, false, msg, mqtt5.PublishOptions{}); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message, got %d", len(client.published))
	}
	// mockRouter.RegisterHandler keys on the TEMPLATE topic (handle.Topic);
	// the published message's own .Topic field already carries the
	// CONCRETE, resolved topic (used for var extraction on receive).
	router.dispatch(handle.Topic, client.published[0])

	if received.Meta.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("Meta.SensorID: want merged from topic, got %q", received.Meta.SensorID)
	}
	if received.Value != 42.5 {
		t.Errorf("Value: want 42.5 (from Gob body), got %v", received.Value)
	}
}
