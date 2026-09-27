package events

import "testing"

func TestTopicAddress_ImplementsAddress(t *testing.T) {
	var _ Address = TopicAddress{}
	a := TopicAddress{Topic: "sensors/{sensorID}/data"}
	if got := a.Template(); got != "sensors/{sensorID}/data" {
		t.Errorf("want Template() to return the Topic field, got %q", got)
	}
}
