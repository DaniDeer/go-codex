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

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

func TestMQTT5SubscribeAdapter_ValidPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newSensorHandle()
	broker := &mockClient{}
	router := newMockRouter()
	p := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 8})
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
	p := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 8})
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

	p := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
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
	p := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
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
