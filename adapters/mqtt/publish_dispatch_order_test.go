package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

// TestClientPublish_RequireQoS_NoCapability_FailsBeforeMiddlewareRuns is
// a REGRESSION GUARD for Phase 4's events/reqreply family, Workstream C's
// audit: `transport.go`'s (*transport).Publish (the reflect path,
// backing Client.Attach's Client.Publish) previously ran the
// capability-coverage check LAST — AFTER middleware dispatch and
// security implementations dispatch had already executed — the SAME
// ordering divergence from `publish[T]` (the generic path, which checks
// coverage FIRST) mqtt5's Workstream A step 6 and zeromq's Workstream C
// found and fixed. This proves the fix: a declared-but-unmet
// [events.CapabilityRequirement] now fails BEFORE a PublishBoundMW Fn
// runs, mirroring the generic path's behavior exactly.
func TestClientPublish_RequireQoS_NoCapability_FailsBeforeMiddlewareRuns(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	var middlewareRan bool
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("side-effect"),
		func(_ context.Context, _ sensorReading) (tdEmpty, error) {
			middlewareRan = true
			return tdEmpty{}, nil
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
	if client.publishedTopic != "" {
		t.Errorf("want 0 published messages, got publishedTopic=%q", client.publishedTopic)
	}
}

// TestClientPublish_RequireQoS_SuppliedCapability_RunsMiddlewareAndPublishes
// confirms the ordering fix is not a regression: supplying a matching
// QoS Capability still runs the PublishBoundMW Fn and publishes
// successfully through Client.Attach's reflect Publish path.
func TestClientPublish_RequireQoS_SuppliedCapability_RunsMiddlewareAndPublishes(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	var middlewareRan bool
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("side-effect"),
		func(_ context.Context, _ sensorReading) (tdEmpty, error) {
			middlewareRan = true
			return tdEmpty{}, nil
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
	if client.publishedTopic != "sensors/readings" {
		t.Errorf("want 1 published message, got publishedTopic=%q", client.publishedTopic)
	}
}
