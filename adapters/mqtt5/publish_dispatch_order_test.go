package mqtt5

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

// TestClientPublish_RequireQoS_NoCapability_FailsBeforeMiddlewareRuns is
// a REGRESSION GUARD for Phase 4's events/reqreply family, Workstream A
// step 6: `transport.Publish` (the reflect path, backing
// Client.Attach's Client.Publish) previously ran the capability-coverage
// check LAST — AFTER property-var merge, PublishBoundMW dispatch, and
// security credential validation had already executed — a confirmed
// ordering divergence from `publish[T]` (the generic path), which checks
// coverage FIRST. This proves the fix: a declared-but-unmet
// [events.CapabilityRequirement] now fails BEFORE a PublishBoundMW Fn
// runs, mirroring the generic path's behavior exactly. A caller that
// assumes "coverage failure means nothing else ran" is now correct on
// BOTH Publish paths, not just the generic one.
func TestClientPublish_RequireQoS_NoCapability_FailsBeforeMiddlewareRuns(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var middlewareRan bool
	bm := events.NewBoundPublishMiddleware(newMqttMdDeclaration("side-effect"),
		func(_ context.Context, _ sensorReading) (mqttMdOut, error) {
			middlewareRan = true
			return mqttMdOut{TenantID: "acme"}, nil
		})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.RequireQoS(events.AtLeastOnce),
	).WithPublish(events.Publish{Summary: "test"}).PublishBoundMW(bm)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	// Deliberately supply ZERO matching Capability.
	err := c.Publish(context.Background(), pub, reading)
	if err == nil {
		t.Fatal("want a capability coverage error, got nil")
	}
	var covErr *events.CapabilityCoverageError
	if !errors.As(err, &covErr) {
		t.Fatalf("want events.CapabilityCoverageError, got %T: %v", err, err)
	}
	if middlewareRan {
		t.Error("PublishBoundMW Fn must NOT run when capability coverage fails — coverage check must run FIRST")
	}
	if len(client.published) != 0 {
		t.Errorf("want 0 published messages, got %d", len(client.published))
	}
}

// TestClientPublish_RequireQoS_SuppliedCapability_RunsMiddlewareAndPublishes
// confirms the ordering fix is not a regression: supplying a matching
// QoS Capability still runs the PublishBoundMW Fn and publishes
// successfully through Client.Attach's reflect Publish path.
func TestClientPublish_RequireQoS_SuppliedCapability_RunsMiddlewareAndPublishes(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var middlewareRan bool
	bm := events.NewBoundPublishMiddleware(newMqttMdDeclaration("side-effect"),
		func(_ context.Context, _ sensorReading) (mqttMdOut, error) {
			middlewareRan = true
			return mqttMdOut{TenantID: "acme"}, nil
		})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.RequireQoS(events.AtLeastOnce),
	).WithPublish(events.Publish{Summary: "test"}).
		WithOptions(PublishOptions[sensorReading]{Capabilities: []Capability{QoS(1)}}).
		PublishBoundMW(bm)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !middlewareRan {
		t.Error("PublishBoundMW Fn must run when capability coverage passes")
	}
	if len(client.published) != 1 {
		t.Errorf("want 1 published message, got %d", len(client.published))
	}
}
