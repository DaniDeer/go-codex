package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Decision 7: PublishTransport[T]/SubscribeTransport[T], PublishHandle/SubscribeHandle ──

type htSensorReading struct {
	SensorID string
	Value    float64
}

var htSensorCodec = codex.Struct[htSensorReading](
	codex.RequiredField("sensor_id", codex.String().Refine(validate.UUID),
		func(r htSensorReading) string { return r.SensorID },
		func(r *htSensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value", codex.Float64(),
		func(r htSensorReading) float64 { return r.Value },
		func(r *htSensorReading, v float64) { r.Value = v },
	),
)

var htSensorChannel = events.NewChannel[htSensorReading]("sensors/{sensorID}/readings", htSensorCodec,
	events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
		func(r htSensorReading) string { return r.SensorID },
		func(r *htSensorReading, v string) { r.SensorID = v },
	),
)

const htSensorID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"

// mockPublishTransport is a minimal events.PublishTransport[T] for testing
// PublishHandle's dispatch.
type mockPublishTransport[T any] struct {
	called bool
	handle *events.ChannelHandle[T]
	msg    T
	err    error
}

func (m *mockPublishTransport[T]) Publish(_ context.Context, handle *events.ChannelHandle[T], msg T) error {
	m.called = true
	m.handle = handle
	m.msg = msg
	return m.err
}
func (m *mockPublishTransport[T]) AdapterName() string { return "mock" }

var _ events.PublishTransport[htSensorReading] = (*mockPublishTransport[htSensorReading])(nil)

// mockSubscribeTransport is a minimal events.SubscribeTransport[T] for
// testing SubscribeHandle's dispatch — synchronously invokes fn once with
// a caller-supplied value, mirroring a single delivered message.
type mockSubscribeTransport[T any] struct {
	called  bool
	deliver T
	err     error
}

func (m *mockSubscribeTransport[T]) Subscribe(ctx context.Context, _ *events.ChannelHandle[T], fn func(context.Context, T) error) error {
	m.called = true
	if m.err != nil {
		return m.err
	}
	return fn(ctx, m.deliver)
}
func (m *mockSubscribeTransport[T]) AdapterName() string { return "mock" }

var _ events.SubscribeTransport[htSensorReading] = (*mockSubscribeTransport[htSensorReading])(nil)

func TestPublishHandle_DelegatesToTransport(t *testing.T) {
	pub := htSensorChannel.WithPublish(events.Publish{})
	mt := &mockPublishTransport[htSensorReading]{}
	reading := htSensorReading{SensorID: htSensorID, Value: 22.5}

	if err := events.PublishHandle(context.Background(), pub, mt, reading); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}
	if !mt.called {
		t.Fatal("expected PublishTransport.Publish to be called")
	}
	if mt.msg != reading {
		t.Errorf("msg = %+v, want %+v", mt.msg, reading)
	}
	if mt.handle == nil || mt.handle.Topic != "sensors/{sensorID}/readings" {
		t.Errorf("handle.Topic = %+v, want the channel's declared template", mt.handle)
	}
}

func TestPublishHandle_TransportError_Propagates(t *testing.T) {
	pub := htSensorChannel.WithPublish(events.Publish{})
	wantErr := errors.New("boom")
	mt := &mockPublishTransport[htSensorReading]{err: wantErr}

	err := events.PublishHandle(context.Background(), pub, mt, htSensorReading{SensorID: htSensorID})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestSubscribeHandle_DelegatesToTransport(t *testing.T) {
	sub := htSensorChannel.WithSubscribe(events.Subscribe{})
	want := htSensorReading{SensorID: htSensorID, Value: 19.5}
	mt := &mockSubscribeTransport[htSensorReading]{deliver: want}

	var got htSensorReading
	err := events.SubscribeHandle(context.Background(), sub, mt, func(_ context.Context, r htSensorReading) error {
		got = r
		return nil
	})
	if err != nil {
		t.Fatalf("SubscribeHandle: %v", err)
	}
	if !mt.called {
		t.Fatal("expected SubscribeTransport.Subscribe to be called")
	}
	if got != want {
		t.Errorf("got = %+v, want %+v", got, want)
	}
}

func TestSubscribeHandle_TransportError_Propagates(t *testing.T) {
	sub := htSensorChannel.WithSubscribe(events.Subscribe{})
	wantErr := errors.New("boom")
	mt := &mockSubscribeTransport[htSensorReading]{err: wantErr}

	err := events.SubscribeHandle(context.Background(), sub, mt, func(context.Context, htSensorReading) error { return nil })
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestEncodeAndBuildTopic_DerivesTopicAndPayload(t *testing.T) {
	handle, err := htSensorChannel.WithPublish(events.Publish{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	reading := htSensorReading{SensorID: htSensorID, Value: 22.5}

	topic, payload, err := events.EncodeAndBuildTopic(handle, reading)
	if err != nil {
		t.Fatalf("EncodeAndBuildTopic: %v", err)
	}
	wantTopic := "sensors/" + htSensorID + "/readings"
	if topic != wantTopic {
		t.Errorf("topic = %q, want %q", topic, wantTopic)
	}
	if len(payload) == 0 {
		t.Error("expected non-empty payload")
	}
}

func TestDecodeAndMergeVars_DecodesPayloadAndMergesTopicVars(t *testing.T) {
	handle, err := htSensorChannel.WithSubscribe(events.Subscribe{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	reading := htSensorReading{SensorID: htSensorID, Value: 22.5}
	_, payload, err := events.EncodeAndBuildTopic(handle, reading)
	if err != nil {
		t.Fatalf("EncodeAndBuildTopic: %v", err)
	}

	got, err := events.DecodeAndMergeVars(handle, payload, map[string]string{"sensorID": htSensorID})
	if err != nil {
		t.Fatalf("DecodeAndMergeVars: %v", err)
	}
	if got != reading {
		t.Errorf("got = %+v, want %+v", got, reading)
	}
}

func TestDecodeAndMergeVars_InvalidPayload_ReturnsDecodeError(t *testing.T) {
	handle, err := htSensorChannel.WithSubscribe(events.Subscribe{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	_, err = events.DecodeAndMergeVars(handle, []byte(`{"sensor_id":"not-a-uuid","value":1}`), map[string]string{"sensorID": htSensorID})
	if err == nil {
		t.Fatal("expected a decode/validation error, got nil")
	}
}

// ── Decision 7: NewClient's Info becomes optional ────────────────────────────

func TestNewClient_NoWithInfo_DefaultsToZeroInfo(t *testing.T) {
	c := events.NewClient()
	doc, err := c.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if strings.Contains(string(yamlBytes), testInfo.Title) {
		t.Errorf("expected no title in spec, got:\n%s", yamlBytes)
	}
}

func TestNewClient_WithInfo_SetsInfo(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	doc, err := c.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), testInfo.Title) {
		t.Errorf("expected title %q in spec, got:\n%s", testInfo.Title, yamlBytes)
	}
}
