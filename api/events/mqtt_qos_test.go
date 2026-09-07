package events_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

// TestSubscribe_QoS_DefaultsToAtMostOnce confirms the zero value matches
// the prior, undeclared default exactly.
func TestSubscribe_QoS_DefaultsToAtMostOnce(t *testing.T) {
	var s events.Subscribe
	if s.QoS != events.QoSAtMostOnce {
		t.Errorf("want zero-value QoS to equal QoSAtMostOnce, got %v", s.QoS)
	}
}

func TestSubscribe_QoS_Declared(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created", QoS: events.QoSExactlyOnce}).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.Descriptor.Subscribe == nil {
		t.Fatal("want non-nil Subscribe descriptor")
	}
}

// TestPublisher_WithAttributes_ResolvesDeclaredAttributes exercises
// the declarative MQTT QoS/Retained mechanism: QoS and
// Retained derived from the outgoing message itself.
func TestPublisher_WithAttributes_ResolvesDeclaredAttributes(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "Publish user event"}).
		WithAttributes(func(e userEvent) events.PublishAttributes {
			return events.PublishAttributes{QoS: events.QoSAtLeastOnce, Retained: true}
		}).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	attrs := h.ResolvePublishAttributes(userEvent{ID: "1", Name: "Alice"})
	if attrs.QoS != events.QoSAtLeastOnce {
		t.Errorf("want QoS %v, got %v", events.QoSAtLeastOnce, attrs.QoS)
	}
	if !attrs.Retained {
		t.Error("want Retained true")
	}
}

// TestPublisher_NoAttributes_ResolvesZeroValue confirms the
// no-WithAttributes-called path returns the zero value (100% backward
// compatible with the prior, undeclared default).
func TestPublisher_NoAttributes_ResolvesZeroValue(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created2", userEventCodec).
		WithPublish(events.Publish{Summary: "Publish user event"}).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	attrs := h.ResolvePublishAttributes(userEvent{ID: "1", Name: "Alice"})
	if attrs.QoS != events.QoSAtMostOnce || attrs.Retained {
		t.Errorf("want zero-value PublishAttributes, got %+v", attrs)
	}
}
