package mqtt5

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

func newTopicVarsTestHandle(topic string) *events.ChannelHandle[struct{}] {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1"}))
	c := codex.Struct[struct{}]()
	h, err := events.NewChannel[struct{}](topic, c).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

// EV4: TopicVarsFromMessage happy path (single var, multiple vars,
// static topic) mirrors adapters/mqtt's existing TopicVarsFromMessage tests.
func TestTopicVarsFromMessage_SingleVar(t *testing.T) {
	h := newTopicVarsTestHandle("sensors/{sensorID}/measurements")
	msg := &pahomqtt5.Publish{Topic: "sensors/f47ac10b/measurements"}

	vars, err := TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["sensorID"] != "f47ac10b" {
		t.Fatalf("want sensorID=f47ac10b, got %q", vars["sensorID"])
	}
}

func TestTopicVarsFromMessage_MultipleVars(t *testing.T) {
	h := newTopicVarsTestHandle("buildings/{buildingID}/rooms/{roomID}/sensors/{sensorID}")
	msg := &pahomqtt5.Publish{Topic: "buildings/b1/rooms/r2/sensors/s3"}

	vars, err := TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["buildingID"] != "b1" || vars["roomID"] != "r2" || vars["sensorID"] != "s3" {
		t.Fatalf("unexpected vars: %v", vars)
	}
}

func TestTopicVarsFromMessage_StaticTopic(t *testing.T) {
	h := newTopicVarsTestHandle("user/created")
	msg := &pahomqtt5.Publish{Topic: "user/created"}

	vars, err := TopicVarsFromMessage(h, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vars) != 0 {
		t.Fatalf("want empty map, got %v", vars)
	}
}

// EV4 (mismatch): wrong segment count / literal mismatch returns TopicMismatchError.
func TestTopicVarsFromMessage_Mismatch(t *testing.T) {
	h := newTopicVarsTestHandle("sensors/{sensorID}/measurements")
	msg := &pahomqtt5.Publish{Topic: "sensors/f47ac10b/extra/measurements"}

	_, err := TopicVarsFromMessage(h, msg)
	var mm TopicMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("want TopicMismatchError, got %T: %v", err, err)
	}
}

func TestTopicMismatchError_LogValue(t *testing.T) {
	err := TopicMismatchError{Template: "sensors/{id}", Topic: "sensors/a/b"}
	v := err.LogValue()
	if v.Kind().String() != "Group" {
		t.Fatalf("want Group kind, got %v", v.Kind())
	}
	attrs := v.Group()
	found := map[string]bool{}
	for _, a := range attrs {
		found[a.Key] = true
	}
	if !found["template"] || !found["topic"] {
		t.Fatalf("want template and topic keys, got %+v", attrs)
	}
}
