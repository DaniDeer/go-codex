package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"
)

// G2-1: TopicVarsFromMessage happy path + mismatch.
func TestTopicVarsFromMessage_HappyPath(t *testing.T) {
	handle := newMergeChannelHandle()
	vars, err := TopicVarsFromMessage(handle, "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("sensorID: want %q, got %q", "f47ac10b-58cc-4372-a567-0e02b2c3d479", vars["sensorID"])
	}
}

func TestTopicVarsFromMessage_Mismatch(t *testing.T) {
	handle := newMergeChannelHandle()
	_, err := TopicVarsFromMessage(handle, "sensors/extra/segments/readings")
	var mm TopicMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("want TopicMismatchError, got %T: %v", err, err)
	}
}

func TestTopicVarsFromMessage_InvalidTopicVar(t *testing.T) {
	handle := newMergeChannelHandle()
	_, err := TopicVarsFromMessage(handle, "sensors/not-a-uuid/readings")
	if err == nil {
		t.Fatal("want error for invalid sensorID (not a UUID), got nil")
	}
}

// G2-2: Subscribe auto-merges topic vars into the payload when the
// channel declares merge fields — no manual TopicVarsFromMessage call
// needed in the handler function.
func TestSubscribe_MergeFields_AutoMergesTopicVars(t *testing.T) {
	handle := newMergeChannelHandle()
	// Payload JSON deliberately carries a DIFFERENT sensor_id — merge must
	// OVERWRITE it with the value extracted from the concrete topic frame.
	payload := []byte(`{"sensor_id":"00000000-0000-0000-0000-000000000000","value":22.5}`)
	sock := &mockSocket{inFrames: [][][]byte{
		{[]byte("sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"), payload},
	}}

	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = subscribeWithHandle(ctx, sock, handle, func(_ context.Context, r sensorReading) error {
		received = r
		cancel()
		return nil
	}, SubscribeOptions[sensorReading]{})

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("SensorID: want merged from topic, got %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", received.Value)
	}
}

// G2-2 (regression guard): channels WITHOUT merge fields behave identically
// to before — Subscribe does not attempt any topic-var merge.
func TestSubscribe_NoMergeFields_NoTopicVarMergeAttempted(t *testing.T) {
	handle := newSubscribeHandle()
	payload := []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.5}`)
	sock := &mockSocket{inFrames: [][][]byte{
		{[]byte("sensors/readings"), payload},
	}}

	var received sensorReading
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = subscribeWithHandle(ctx, sock, handle, func(_ context.Context, r sensorReading) error {
		received = r
		cancel()
		return nil
	}, SubscribeOptions[sensorReading]{})

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" || received.Value != 1.5 {
		t.Errorf("want decoded payload unchanged, got %+v", received)
	}
}

// G2-3: PublishHandle single-call convenience — one struct in, no
// manual vars map, derives the topic from msg's own merge fields.
func TestPublishHandle_DerivesTopicFromMsg(t *testing.T) {
	sock := &mockSocket{}
	handle := newMergeChannelHandle()

	err := publishHandle(context.Background(), sock, handle,
		sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 3.0},
		PublishOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := sock.sentSnapshot()
	if len(sent) != 1 {
		t.Fatalf("want 1 sent message, got %d", len(sent))
	}
	gotTopic := string(sent[0][0])
	if gotTopic != "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings" {
		t.Errorf("topic: want derived from msg, got %q", gotTopic)
	}
}

// PublishHandle with no merge fields declared behaves identically to a bare
// publish(..., nil, ...) call (regression guard).
func TestPublishHandle_NoMergeFields_MatchesPlainPublish(t *testing.T) {
	sock := &mockSocket{}
	handle := newPublishHandle()

	err := publishHandle(context.Background(), sock, handle,
		sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 3.0},
		PublishOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := sock.sentSnapshot()
	if len(sent) != 1 || string(sent[0][0]) != "sensors/readings" {
		t.Errorf("want static topic, got %v", sent)
	}
}
