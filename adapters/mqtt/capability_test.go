package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/stats"
)

// mockCapabilityObserver spies on RecordCapabilityApplied calls.
type mockCapabilityObserver struct {
	stats.NoopObserver
	applied []string
}

func (o *mockCapabilityObserver) RecordCapabilityApplied(location, capability string) {
	o.applied = append(o.applied, location+":"+capability)
}

func TestQoS_ImplementsCapability(t *testing.T) {
	var _ Capability = QoSAtLeastOnce
	if got := QoSAtLeastOnce.CapabilityName(); got != "QoS" {
		t.Errorf("want CapabilityName %q, got %q", "QoS", got)
	}
}

func TestRetained_ImplementsCapability(t *testing.T) {
	var _ Capability = Retained(true)
	if got := Retained(true).CapabilityName(); got != "Retained" {
		t.Errorf("want CapabilityName %q, got %q", "Retained", got)
	}
}

func TestResolveCapabilities(t *testing.T) {
	qos, qosSet, retained, retainedSet := resolveCapabilities([]Capability{QoSAtLeastOnce, Retained(true)})
	if !qosSet || qos != QoSAtLeastOnce {
		t.Errorf("want QoSAtLeastOnce set, got %v set=%v", qos, qosSet)
	}
	if !retainedSet || !bool(retained) {
		t.Errorf("want Retained(true) set, got %v set=%v", retained, retainedSet)
	}

	_, qosSet2, _, retainedSet2 := resolveCapabilities(nil)
	if qosSet2 || retainedSet2 {
		t.Error("want no capabilities set for empty slice")
	}
}

// TestServeSubscribers_CapabilitiesOverridesQoS confirms
// SubscribeOptions.Capabilities (the RECOMMENDED path) overrides the
// legacy SubscribeOptions.QoS field, and reports the applied capability
// via stats.CapabilityObserver.
func TestServeSubscribers_CapabilitiesOverridesQoS(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(client, ev)
	obs := &mockCapabilityObserver{}

	ch := events.NewChannel[sensorReading]("sensors/capabilities-qos", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions(SubscribeOptions{QoS: 1, Capabilities: []Capability{QoSExactlyOnce}, Observer: obs})
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()
	<-done

	if got := client.subscribedQoSSnapshot(); got != 2 {
		t.Errorf("want QoS 2 from Capabilities override, got %d", got)
	}
	found := false
	for _, a := range obs.applied {
		if a == "sensors/capabilities-qos:QoS" {
			found = true
		}
	}
	if !found {
		t.Errorf("want RecordCapabilityApplied(topic, \"QoS\"), got %v", obs.applied)
	}
}

// TestPublish_CapabilitiesRetainedFallback confirms
// PublishOptions.Capabilities supplies a fallback Retained value when the
// caller passes retained=false, and reports it via CapabilityObserver.
func TestPublish_CapabilitiesRetainedFallback(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	obs := &mockCapabilityObserver{}
	handle, err := events.NewChannel[sensorReading]("sensors/capabilities-retained", sensorCodec).
		WithPublish(events.Publish{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	err = publishHandle(context.Background(), client, handle, 0, false,
		sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}, PublishOptions[sensorReading]{
			Capabilities: []Capability{Retained(true)},
			Observer:     obs,
		})
	if err != nil {
		t.Fatalf("publishHandle: %v", err)
	}

	_, retained := client.publishedQoSRetainedSnapshot()
	if !retained {
		t.Error("want Retained(true) capability to set the retained flag")
	}
	found := false
	for _, a := range obs.applied {
		if a == "sensors/capabilities-retained:Retained" {
			found = true
		}
	}
	if !found {
		t.Errorf("want RecordCapabilityApplied(topic, \"Retained\"), got %v", obs.applied)
	}
}
