package mqtt5_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	mqtt5 "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/format"
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

// ── SubscribeStream ───────────────────────────────────────────────────────────

func TestMQTT5SubscribeStream_ValidPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newSensorHandle()

	s, handler := mqtt5.SubscribeStream(ctx, handle, format.JSON(sensorCodec),
		gstream.SourceOptions{Buffer: 4},
		mqtt5.SubscribeOptions{})

	payload, _ := json.Marshal(map[string]any{
		"sensor_id": "550e8400-e29b-41d4-a716-446655440000",
		"value":     42.0,
	})
	handler(newMsg("sensors/data", payload))
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
}

func TestMQTT5SubscribeStream_DecodeErrorGoesToStreamErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newSensorHandle()

	s, handler := mqtt5.SubscribeStream(ctx, handle, format.JSON(sensorCodec),
		gstream.SourceOptions{Buffer: 4},
		mqtt5.SubscribeOptions{})

	handler(newMsg("sensors/data", []byte("not-json")))
	cancel()

	_, errs := gstream.Collect(context.Background(), s)
	if len(errs) == 0 {
		t.Fatal("want decode error in Stream.Errors, got none")
	}
	var se mqtt5.SubscribeError
	if !errors.As(errs[0], &se) {
		t.Errorf("want SubscribeError, got %T: %v", errs[0], errs[0])
	}
	if se.Kind != mqtt5.KindDecode {
		t.Errorf("Kind: want KindDecode, got %v", se.Kind)
	}
}

func TestMQTT5SubscribeStream_MultipleMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newSensorHandle()

	s, handler := mqtt5.SubscribeStream(ctx, handle, format.JSON(sensorCodec),
		gstream.SourceOptions{Buffer: 8},
		mqtt5.SubscribeOptions{})

	good, _ := json.Marshal(map[string]any{"sensor_id": "550e8400-e29b-41d4-a716-446655440000", "value": 1.0})
	handler(newMsg("sensors/data", []byte("bad")))
	handler(newMsg("sensors/data", good))
	handler(newMsg("sensors/data", []byte("also-bad")))
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
	if len(errs) != 2 {
		t.Errorf("want 2 errors, got %d", len(errs))
	}
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

func TestMQTT5DrainPublish_PublishesEachItem(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{}
	handle := newSensorHandle()

	ch := make(chan sensorReading, 1)
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 1.0}
	close(ch)

	mqtt5.DrainPublish(ctx, client, handle,
		gstream.From(ctx, ch),
		format.JSON(sensorCodec),
		mqtt5.MQTT5DrainPublishOptions{})

	client.mu.Lock()
	n := len(client.published)
	client.mu.Unlock()
	if n != 1 {
		t.Errorf("want 1 published, got %d", n)
	}
}

func TestMQTT5DrainPublish_StreamErrorsForwardedToOnError(t *testing.T) {
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
	mqtt5.DrainPublish(ctx, client, handle, src, format.JSON(sensorCodec),
		mqtt5.MQTT5DrainPublishOptions{OnError: func(e error) { gotErr = e }})

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

// ── CallStream ────────────────────────────────────────────────────────────────

func TestMQTT5CallStream_ErrorsForwardedFromSrc(t *testing.T) {
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

	out := mqtt5.CallStream(ctx, client, router, handle, src, mqtt5.CallOptions{})
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Errorf("want 1 forwarded error, got %d", len(errs))
	}
}
