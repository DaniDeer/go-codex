package mqtt5

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

func TestQoS_ImplementsLeveledCapability(t *testing.T) {
	var _ events.LeveledCapability = QoSAtLeastOnce
	if got := QoSAtMostOnce.Level(); got != 0 {
		t.Errorf("want Level 0, got %d", got)
	}
	if got := QoSAtLeastOnce.Level(); got != 1 {
		t.Errorf("want Level 1, got %d", got)
	}
	if got := QoSExactlyOnce.Level(); got != 2 {
		t.Errorf("want Level 2, got %d", got)
	}
}

func TestRetained_ImplementsCapability(t *testing.T) {
	var _ Capability = Retained(true)
	if got := Retained(true).CapabilityName(); got != "Retained" {
		t.Errorf("want CapabilityName %q, got %q", "Retained", got)
	}
}

// resolveCapabilities (this package's own hand-rolled extraction
// function) was REMOVED — replaced by the generic
// [events.ResolveCapabilityValue], tested once in api/events.

// TestServeSubscribers_CapabilitiesSetsQoS confirms
// SubscribeOptions.Capabilities (the SOLE mechanism as of
// docs/roadmap/capability-requirement-composition.md's Phase 4) sets
// the wire-level QoS and reports the applied capability via
// stats.CapabilityObserver.
func TestServeSubscribers_CapabilitiesSetsQoS(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	obs := &mockCapabilityObserver{}

	ch := events.NewChannel[sensorReading]("sensors/capabilities-qos", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions(SubscribeOptions{Capabilities: []Capability{QoSExactlyOnce}, Observer: obs})

	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	router.waitHandler("sensors/capabilities-qos")
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.subscribed) != 1 || len(client.subscribed[0].Subscriptions) != 1 {
		t.Fatalf("expected exactly 1 subscription, got %+v", client.subscribed)
	}
	if got := client.subscribed[0].Subscriptions[0].QoS; got != 2 {
		t.Errorf("want QoS 2 from Capabilities, got %d", got)
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

// TestPublish_CapabilitiesSetsRetained confirms
// PublishOptions.Capabilities (the SOLE mechanism as of
// docs/roadmap/capability-requirement-composition.md's Phase 4) sets
// the wire-level Retain flag and reports it via CapabilityObserver.
func TestPublish_CapabilitiesSetsRetained(t *testing.T) {
	client := &mockClient{}
	obs := &mockCapabilityObserver{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	err := publish(context.Background(), client, newChannelHandle(), reading, nil, true,
		PublishOptions[sensorReading]{
			Capabilities: []Capability{Retained(true)},
			Observer:     obs,
		})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	pub := client.lastPublished()
	if pub == nil || !pub.Retain {
		t.Fatalf("want Retained(true) capability to set the Retain flag, got %v", pub)
	}
	found := false
	for _, a := range obs.applied {
		if a == "sensors/readings:Retained" {
			found = true
		}
	}
	if !found {
		t.Errorf("want RecordCapabilityApplied(topic, \"Retained\"), got %v", obs.applied)
	}
}
