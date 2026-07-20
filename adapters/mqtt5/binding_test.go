package mqtt5_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	mqtt5 "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── shared helpers ────────────────────────────────────────────────────────────

func newSensorHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[sensorReading]("sensors/data", sensorCodec,
		events.Subscribe{Summary: "Sensor reading"},
		events.Publish{Summary: "Sensor reading"},
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func newMsg(topic string, payload []byte) *pahomqtt5.Publish {
	return &pahomqtt5.Publish{Topic: topic, Payload: payload}
}

func newTemplatedSensorHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[sensorReading]("sensors/{sensorID}/data", sensorCodec,
		events.Subscribe{Summary: "Sensor reading"},
		events.TopicParam{Name: "sensorID"},
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

// TestMQTT5SubscribeAdapter_AutoDerivesWildcardFilter verifies that when
// TopicFilter is empty and the handle's topic has {var} placeholders, the
// adapter subscribes using the derived MQTT wildcard filter
// ("sensors/{sensorID}/data" -> "sensors/+/data") instead of the raw,
// brace-containing topic string.
func TestMQTT5SubscribeAdapter_AutoDerivesWildcardFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newTemplatedSensorHandle()
	broker := &mockClient{}
	router := newMockRouter()
	p, err := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.SubscribeAdapter(broker, router, handle, 0, format.JSON(sensorCodec), mqtt5.SubscribeAdapterOptions{}))
	s := p.Stream(ctx)
	router.waitHandler("sensors/+/data") // derived wildcard filter, not "sensors/{sensorID}/data"

	payload, _ := json.Marshal(map[string]any{"sensor_id": "550e8400-e29b-41d4-a716-446655440000", "value": 42.0})
	router.dispatch("sensors/+/data", newMsg("sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/data", payload))
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d: errs=%v", len(vals), errs)
	}
}

func TestMQTT5SubscribeAdapter_ValidPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newSensorHandle()
	broker := &mockClient{}
	router := newMockRouter()
	p, err := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.SubscribeAdapter(broker, router, handle, 0, format.JSON(sensorCodec), mqtt5.SubscribeAdapterOptions{}))
	s := p.Stream(ctx)                 // must call before cancel
	router.waitHandler("sensors/data") // wait for handler registration in Activate goroutine

	payload, _ := json.Marshal(map[string]any{"sensor_id": "550e8400-e29b-41d4-a716-446655440000", "value": 42.0})
	router.dispatch("sensors/data", newMsg("sensors/data", payload))
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestMQTT5SubscribeAdapter_DecodeErrorGoesToStreamErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newSensorHandle()
	broker := &mockClient{}
	router := newMockRouter()
	p, err := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.SubscribeAdapter(broker, router, handle, 0, format.JSON(sensorCodec), mqtt5.SubscribeAdapterOptions{}))
	s := p.Stream(ctx)                 // must call before cancel
	router.waitHandler("sensors/data") // wait for handler registration

	router.dispatch("sensors/data", newMsg("sensors/data", []byte("not-json")))
	cancel()

	_, errs := gstream.Collect(context.Background(), s)
	if len(errs) == 0 {
		t.Fatal("want decode error in Stream.Errors, got none")
	}
	var se mqtt5.SubscribeError
	if !errors.As(errs[0], &se) {
		t.Errorf("want SubscribeError, got %T: %v", errs[0], errs[0])
	}
}

// ── PublishAdapter ────────────────────────────────────────────────────────────

func TestMQTT5PublishAdapter_PublishesEachItem(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}
	handle := newSensorHandle()

	ch := make(chan sensorReading, 1)
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 1.0}
	close(ch)

	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.PublishAdapter(client, handle, format.JSON(sensorCodec), mqtt5.MQTT5DrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	client.mu.Lock()
	n := len(client.published)
	client.mu.Unlock()
	if n != 1 {
		t.Errorf("want 1 published, got %d", n)
	}
}

func TestMQTT5PublishAdapter_StreamErrorsForwardedToOnError(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}
	handle := newSensorHandle()

	errCh := make(chan error, 1)
	valCh := make(chan sensorReading)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := gstream.Stream[sensorReading]{Values: valCh, Errors: errCh}

	var gotErr error
	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.PublishAdapter(client, handle, format.JSON(sensorCodec),
		mqtt5.MQTT5DrainPublishOptions{OnError: func(e error) { gotErr = e }}))
	p.Feed(ctx, src)

	if gotErr == nil {
		t.Error("want upstream error forwarded to OnError, got nil")
	}
}

// ── AsPipelineFunc ────────────────────────────────────────────────────────────

func TestMQTT5AsPipelineFunc_ReturnsFirstValue(t *testing.T) {
	fn := mqtt5.AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		return gstream.Single(ctx, computeResp{Sum: req.X + req.Y})
	})

	resp, err := fn(context.Background(), computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("Sum: want 7, got %d", resp.Sum)
	}
}

func TestMQTT5AsPipelineFunc_ErrorTakesPrecedence(t *testing.T) {
	fn := mqtt5.AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		errCh := make(chan error, 1)
		valCh := make(chan computeResp)
		errCh <- fmt.Errorf("compute failed")
		close(errCh)
		close(valCh)
		return gstream.Stream[computeResp]{Values: valCh, Errors: errCh}
	})

	_, err := fn(context.Background(), computeReq{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestMQTT5AsPipelineFunc_NoValueReturnsPipelineNoResponseError(t *testing.T) {
	fn := mqtt5.AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		errCh := make(chan error)
		valCh := make(chan computeResp)
		close(errCh)
		close(valCh)
		return gstream.Stream[computeResp]{Values: valCh, Errors: errCh}
	})

	_, err := fn(context.Background(), computeReq{})
	var pnr mqtt5.PipelineNoResponseError
	if !errors.As(err, &pnr) {
		t.Errorf("want PipelineNoResponseError, got %T: %v", err, err)
	}
}

// ── G1: per-item vars derivation (shipped) ───────────────────────────────────

// G1-3: mqtt5.PublishAdapter derives topic vars PER-ITEM from each item's
// own merge fields when opts.Vars is nil — two items with different sensor
// IDs must publish to two different concrete topics.
func TestMQTT5PublishAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}
	handle := newMergeChannelHandle()

	ch := make(chan sensorReading, 2)
	ch <- sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 2.0}
	close(ch)

	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// opts.Vars left nil -> per-item derivation via PublishHandle.
	p.Bind(ctx, mqtt5.PublishAdapter(client, handle, format.JSON(sensorCodec), mqtt5.MQTT5DrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.published) != 2 {
		t.Fatalf("want 2 published, got %d", len(client.published))
	}
	if client.published[0].Topic != "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings" ||
		client.published[1].Topic != "sensors/550e8400-e29b-41d4-a716-446655440000/readings" {
		t.Errorf("want per-item resolved topics, got %q, %q", client.published[0].Topic, client.published[1].Topic)
	}
}

// G1-3: an explicit (non-nil) MQTT5DrainPublishOptions.Vars still wins —
// regression guard matching today's static-vars behavior when set.
func TestMQTT5PublishAdapter_ExplicitVarsStillWins(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}
	handle := newMergeChannelHandle()

	ch := make(chan sensorReading, 2)
	ch <- sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 2.0}
	close(ch)

	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.PublishAdapter(client, handle, format.JSON(sensorCodec),
		mqtt5.MQTT5DrainPublishOptions{Vars: map[string]string{"sensorID": "static-sensor"}}))
	p.Feed(ctx, gstream.From(ctx, ch))

	client.mu.Lock()
	defer client.mu.Unlock()
	for _, msg := range client.published {
		if msg.Topic != "sensors/static-sensor/readings" {
			t.Errorf("want static topic for every item, got %q", msg.Topic)
		}
	}
}

// templatedBrokerClient simulates a broker for a route with a TEMPLATE topic
// (e.g. "compute/{tenantID}/add"): mockRouter.RegisterHandler keys handlers
// by the raw template string (see mqtt5.Serve), while each concrete publish
// carries the resolved topic (e.g. "compute/acme/add") — so dispatch must
// use the fixed template key regardless of the concrete publish topic.
type templatedBrokerClient struct {
	mockClient
	router   *mockRouter
	template string
}

func (c *templatedBrokerClient) Publish(ctx context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	resp, err := c.mockClient.Publish(ctx, p)
	if err != nil {
		return nil, err
	}
	// Reply publishes are registered under their own concrete topic
	// (client.Subscribe -> router.RegisterHandler(concreteReplyTopic, ...));
	// request publishes are registered under the raw template by mqtt5.Serve.
	if c.router.hasHandler(p.Topic) {
		go c.router.dispatch(p.Topic, p)
	} else {
		go c.router.dispatch(c.template, p)
	}
	return resp, nil
}

// G1-3: mqtt5.CallAdapter derives request-topic vars PER-ITEM from each
// item's own merge fields when opts.Vars is nil — two items with different
// tenant IDs must publish to two different concrete request topics.
func TestMQTT5CallAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	router := newMockRouter()
	client := &templatedBrokerClient{router: router, template: "compute/{tenantID}/add"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	handle := newTenantRouteHandle()
	_ = mqtt5.Serve(ctx, client, router, handle,
		func(_ context.Context, req tenantReq) (tenantResp, error) {
			return tenantResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{})

	ch := make(chan tenantReq, 2)
	ch <- tenantReq{TenantID: "acme", X: 1, Y: 2}
	ch <- tenantReq{TenantID: "globex", X: 3, Y: 4}
	close(ch)

	p, err := ports.NewIOPort[tenantReq, tenantResp]("test", tenantReqCodec, tenantRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// opts.Vars left nil -> per-item derivation via CallHandle.
	p.Bind(ctx, mqtt5.CallAdapter(client, router, handle, mqtt5.CallOptions{ReplyTopicPrefix: "replies"})) //nolint:errcheck
	out := p.Connect(ctx, gstream.From(ctx, ch))
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 || vals[0].Sum != 3 || vals[1].Sum != 7 {
		t.Errorf("want [Sum=3, Sum=7], got %v", vals)
	}

	client.mockClient.mu.Lock()
	defer client.mockClient.mu.Unlock()
	var reqTopics []string
	for _, msg := range client.mockClient.published {
		if msg.Topic == "compute/acme/add" || msg.Topic == "compute/globex/add" {
			reqTopics = append(reqTopics, msg.Topic)
		}
	}
	if len(reqTopics) != 2 {
		t.Errorf("want 2 per-item resolved request topics, got %v", reqTopics)
	}
}

// ── CallAdapter ───────────────────────────────────────────────────────────────

func TestMQTT5CallAdapter_ErrorsForwardedFromSrc(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}
	router := newMockRouter()

	rb := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, err := computeRoute.Register(rb)
	if err != nil {
		t.Fatalf("register route: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan computeReq)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := gstream.Stream[computeReq]{Values: valCh, Errors: errCh}

	p, err := ports.NewIOPort[computeReq, computeResp]("test", computeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, mqtt5.CallAdapter(client, router, handle, mqtt5.CallOptions{})) //nolint:errcheck
	out := p.Connect(ctx, src)
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Errorf("want 1 forwarded error, got %d", len(errs))
	}
}

// ── ServeAdapter ──────────────────────────────────────────────────────────────

func TestMQTT5ServeAdapter_HandlesRequestViaToolPort(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	rb := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, err := computeRoute.Register(rb)
	if err != nil {
		t.Fatalf("register route: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	p, err := ports.NewToolPort[computeReq, computeResp]("compute", computeReqCodec, computeRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, req computeReq) gstream.Stream[computeResp] {
		return gstream.Single(context.Background(), computeResp{Sum: req.X + req.Y})
	})

	if err := p.Bind(ctx, mqtt5.ServeAdapter(client, router, handle, mqtt5.ServeOptions{})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// Bind spawns Serve in a background goroutine; give it time to register
	// its subscription with the router before dispatching.
	time.Sleep(20 * time.Millisecond)

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-42"),
		},
	})

	// Give the async Serve goroutine time to process the dispatched message.
	time.Sleep(50 * time.Millisecond)

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if pub.Topic != "replies/client-1" {
		t.Fatalf("expected reply to 'replies/client-1', got %q", pub.Topic)
	}
	var resp computeResp
	if err := json.Unmarshal(pub.Payload, &resp); err != nil {
		t.Fatalf("unmarshal reply payload: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("want Sum=7, got %d", resp.Sum)
	}
}

func TestMQTT5ServeAdapter_NoPipelineError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	rb := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, err := computeRoute.Register(rb)
	if err != nil {
		t.Fatalf("register route: %v", err)
	}

	ctx := context.Background()
	p, err := ports.NewToolPort[computeReq, computeResp]("compute-nopipeline", computeReqCodec, computeRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if err := p.Bind(ctx, mqtt5.ServeAdapter(client, router, handle, mqtt5.ServeOptions{})); err == nil {
		t.Fatal("want error when no pipeline set")
	}
}
