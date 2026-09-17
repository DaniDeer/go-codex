package zeromq

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
)

// This file tests Topic 4's DeadLetter fallback (see docs/roadmap/
// error-handling-rest-events-reqreply.md): a channel that declares
// events.DeadLetter dead-letters a subscribe-side failure (when no
// ErrorChannel matches, or none is declared) AND a failed publish.

func TestDeadLetter_SubscribeDecodeFailure_Published(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{
		{[]byte("sensors/readings"), []byte(`{}`)}, // missing required fields
	}}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = subscribeWithHandle(ctx, sock, handle,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{OnError: func(SubscribeError) { onErrorCalled = true }},
	)

	if onErrorCalled {
		t.Error("OnError should NOT be called when DeadLetter absorbs a decode failure")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	found := false
	for _, frames := range sock.sentFrames {
		if len(frames) > 0 && string(frames[0]) == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic")
	}
}

// topicFailingSocket fails SendFrames only when the FIRST frame (topic)
// equals failTopic, so a subsequent dead-letter publish attempt (a
// DIFFERENT topic) still succeeds and can be observed.
type topicFailingSocket struct {
	*mockSocket
	failTopic []byte
	failErr   error
}

func (s *topicFailingSocket) SendFrames(frames [][]byte) error {
	if len(frames) > 0 && bytes.Equal(frames[0], s.failTopic) {
		return s.failErr
	}
	return s.mockSocket.SendFrames(frames)
}

func TestDeadLetter_PublishFailure_Published(t *testing.T) {
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.DeadLetter("sensors/readings/dlq"),
	).WithPublish(events.Publish{Summary: "test"}).Handle(
		events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"})),
	)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sock := &topicFailingSocket{mockSocket: &mockSocket{}, failTopic: []byte("sensors/readings"), failErr: errors.New("socket unavailable")}

	err = publish(context.Background(), sock, handle,
		sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}, nil, false, PublishOptions[sensorReading]{})
	if err == nil {
		t.Fatal("want a publish error to propagate to the caller unchanged")
	}

	sock.mockSocket.mu.Lock()
	defer sock.mockSocket.mu.Unlock()
	found := false
	for _, frames := range sock.mockSocket.sentFrames {
		if len(frames) > 0 && string(frames[0]) == "sensors/readings/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic even though the ORIGINAL publish failed")
	}
}
