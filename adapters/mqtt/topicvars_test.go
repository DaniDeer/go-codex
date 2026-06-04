package mqtt_test

import (
	"errors"
	"testing"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// mockMsg is a minimal pahomqtt.Message stub that only provides Topic().
type mockMsg struct {
	topic   string
	payload []byte
}

func (m *mockMsg) Duplicate() bool   { return false }
func (m *mockMsg) Qos() byte         { return 0 }
func (m *mockMsg) Retained() bool    { return false }
func (m *mockMsg) Topic() string     { return m.topic }
func (m *mockMsg) MessageID() uint16 { return 0 }
func (m *mockMsg) Payload() []byte   { return m.payload }
func (m *mockMsg) Ack()              {}

func newTestHandle(topic string) *events.ChannelHandle[struct{}] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1"})
	c := codex.Struct[struct{}]()
	h, err := events.NewChannel[struct{}](topic, c).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestTopicVarsFromMessage_SingleVar(t *testing.T) {
	h := newTestHandle("sensors/{sensorID}/measurements")
	msg := &mockMsg{topic: "sensors/f47ac10b/measurements"}

	vars, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "f47ac10b" {
		t.Fatalf("want sensorID=f47ac10b, got %q", vars["sensorID"])
	}
}

func TestTopicVarsFromMessage_MultipleVars(t *testing.T) {
	h := newTestHandle("buildings/{buildingID}/rooms/{roomID}/sensors/{sensorID}")
	msg := &mockMsg{topic: "buildings/b1/rooms/r2/sensors/s3"}

	vars, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["buildingID"] != "b1" {
		t.Fatalf("want buildingID=b1, got %q", vars["buildingID"])
	}
	if vars["roomID"] != "r2" {
		t.Fatalf("want roomID=r2, got %q", vars["roomID"])
	}
	if vars["sensorID"] != "s3" {
		t.Fatalf("want sensorID=s3, got %q", vars["sensorID"])
	}
}

func TestTopicVarsFromMessage_StaticTopic(t *testing.T) {
	h := newTestHandle("user/created")
	msg := &mockMsg{topic: "user/created"}

	vars, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vars) != 0 {
		t.Fatalf("want empty map, got %v", vars)
	}
}

func TestTopicVarsFromMessage_PlusSingleLevelWildcard(t *testing.T) {
	h := newTestHandle("sensors/+/measurements")
	msg := &mockMsg{topic: "sensors/abc123/measurements"}

	vars, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// + is anonymous — should not be in the map
	if _, exists := vars["+"]; exists {
		t.Fatalf("want + not captured, but got entry in map")
	}
	if len(vars) != 0 {
		t.Fatalf("want empty map, got %v", vars)
	}
}

func TestTopicVarsFromMessage_HashMultiLevelWildcard(t *testing.T) {
	h := newTestHandle("sensors/{sensorID}/#")
	msg := &mockMsg{topic: "sensors/abc123/measurements/raw/v1"}

	vars, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "abc123" {
		t.Fatalf("want sensorID=abc123, got %q", vars["sensorID"])
	}
	if vars["#"] != "measurements/raw/v1" {
		t.Fatalf("want #=measurements/raw/v1, got %q", vars["#"])
	}
}

func TestTopicVarsFromMessage_Mismatch_ExtraSegments(t *testing.T) {
	h := newTestHandle("sensors/{sensorID}/data")
	msg := &mockMsg{topic: "sensors/abc/extra/data"}

	_, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	var mm adaptermqtt.TopicMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("want TopicMismatchError, got %T: %v", err, err)
	}
}

func TestTopicVarsFromMessage_Mismatch_WrongLiteral(t *testing.T) {
	h := newTestHandle("sensors/{sensorID}/data")
	msg := &mockMsg{topic: "devices/abc/data"}

	_, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	var mm adaptermqtt.TopicMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("want TopicMismatchError, got %T: %v", err, err)
	}
	if mm.Template != "sensors/{sensorID}/data" {
		t.Fatalf("want Template=sensors/{sensorID}/data, got %q", mm.Template)
	}
}

func TestTopicVarsFromMessage_Mismatch_FewerSegments(t *testing.T) {
	h := newTestHandle("sensors/{sensorID}/data")
	msg := &mockMsg{topic: "sensors/abc"}

	_, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	var mm adaptermqtt.TopicMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("want TopicMismatchError, got %T: %v", err, err)
	}
}

// newTestHandleWithUUIDParam creates a channel handle with a UUID codec on {sensorID}.
func newTestHandleWithUUIDParam(topic string) *events.ChannelHandle[struct{}] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1"})
	c := codex.Struct[struct{}]()
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[struct{}](topic, c,
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newTestHandleWithMQTTConstraint creates a channel handle with MQTTPublishTopic constraint.
func newTestHandleWithMQTTConstraint(topic string) *events.ChannelHandle[struct{}] {
	b := events.NewBuilder(
		events.Info{Title: "Test", Version: "1"},
		events.WithTopicConstraints(validate.MQTTPublishTopic),
	)
	c := codex.Struct[struct{}]()
	h, err := events.NewChannel[struct{}](topic, c).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestTopicVarsFromMessage_UUIDParamCodecPasses(t *testing.T) {
	h := newTestHandleWithUUIDParam("sensors/{sensorID}/measurements")
	msg := &mockMsg{topic: "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/measurements"}

	vars, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("want UUID, got %q", vars["sensorID"])
	}
}

func TestTopicVarsFromMessage_UUIDParamCodecFails_TopicParamError(t *testing.T) {
	h := newTestHandleWithUUIDParam("sensors/{sensorID}/measurements")
	msg := &mockMsg{topic: "sensors/not-a-uuid/measurements"}

	_, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err == nil {
		t.Fatal("expected TopicParamError, got nil")
	}
	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "sensorID" {
		t.Errorf("TopicParamError.Name = %q, want sensorID", paramErr.Name)
	}
	if paramErr.Value != "not-a-uuid" {
		t.Errorf("TopicParamError.Value = %q, want not-a-uuid", paramErr.Value)
	}
}

func TestTopicVarsFromMessage_TopicCodecFails_InvalidTopicError(t *testing.T) {
	// Channel registered with template topic (stripped form passes MQTTPublishTopic).
	// A wildcard concrete topic must fail codec validation.
	h := newTestHandleWithMQTTConstraint("sensors/{sensorID}/measurements")
	// Simulate a corrupt/wildcard topic arriving on the wire.
	msg := &mockMsg{topic: "sensors/+/measurements"}

	_, err := adaptermqtt.TopicVarsFromMessage(h, msg)
	if err == nil {
		t.Fatal("expected InvalidTopicError, got nil")
	}
	var topicErr events.InvalidTopicError
	if !errors.As(err, &topicErr) {
		t.Fatalf("expected InvalidTopicError, got %T: %v", err, err)
	}
	if topicErr.Topic != "sensors/+/measurements" {
		t.Errorf("InvalidTopicError.Topic = %q, want sensors/+/measurements", topicErr.Topic)
	}
}
