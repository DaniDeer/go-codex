package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
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
// [events.ResolveCapabilityValue], now tested once in api/events
// (covering the same "extract last-matching value, ok=false when empty"
// behavior). See ExampleRequireQoS/api/events/capability_test.go.

// TestServeSubscribers_CapabilitiesSetsQoS confirms
// SubscribeOptions.Capabilities (the SOLE mechanism as of
// docs/design/d-0006-protocol-native-capabilities.md's Phase 5) sets the
// wire-level QoS and reports the applied capability via
// stats.CapabilityObserver.
func TestServeSubscribers_CapabilitiesSetsQoS(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(client, ev)
	obs := &mockCapabilityObserver{}

	ch := events.NewChannel[sensorReading]("sensors/capabilities-qos", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions(SubscribeOptions{Capabilities: []Capability{QoSExactlyOnce}, Observer: obs})
	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()
	<-done

	if got := client.subscribedQoSSnapshot(); got != 2 {
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

// TestServeSubscribers_FormatOverride is `deep-dive-bug-hunt`'s api/events
// follow-up regression test for the `SubscribeOptions.Formats` asymmetry
// (D-0010's own named deferred item): proves a per-channel
// `SubscribeOptions.Formats` override is now honored on the
// `ServeSubscribers` path too — mirrors
// `TestClientSubscribe_FormatOverride`'s identical proof for the
// `transport.Subscribe` (ports.Pattern-binding) path, which already
// worked before this fix.
func TestServeSubscribers_FormatOverride(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	ev := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	caller := newCaller(client, ev)

	callCount := 0
	jsonFmt := format.JSON(sensorCodec)
	overrideFmt := format.NewTyped(sensorCodec,
		func(r sensorReading) ([]byte, error) { return jsonFmt.Marshal(r) },
		func(b []byte) (sensorReading, error) {
			callCount++
			return jsonFmt.Unmarshal(b)
		},
		"application/x-custom",
	)

	done := make(chan struct{})
	ch := events.NewChannel[sensorReading]("sensors/format-override", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, _ sensorReading) error {
			close(done)
			return nil
		}).
		WithOptions(SubscribeOptions{Formats: []format.Format[sensorReading]{overrideFmt}})

	if err := sub.Register(ev); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _ = caller.ServeSubscribers(ctx) }()

	payload, _ := jsonFmt.Marshal(sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1})
	deadline := time.Now().Add(500 * time.Millisecond)
	for client.subscribedHandlerSnapshot() == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the subscriber's handler to be registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	handler := client.subscribedHandlerSnapshot()
	handler(client, &mockMessage{topic: "sensors/format-override", payload: payload})
	<-done

	if callCount != 1 {
		t.Errorf("want override format's Unmarshal called once, got %d", callCount)
	}
}

// TestPublish_CapabilitiesSetsRetained confirms
// PublishOptions.Capabilities (the SOLE mechanism as of
// docs/design/d-0006-protocol-native-capabilities.md's Phase 5) sets the
// wire-level retained flag and reports it via CapabilityObserver.
func TestPublish_CapabilitiesSetsRetained(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	obs := &mockCapabilityObserver{}
	handle, err := events.NewChannel[sensorReading]("sensors/capabilities-retained", sensorCodec).
		WithPublish(events.Publish{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	err = publishHandle(context.Background(), client, handle,
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
