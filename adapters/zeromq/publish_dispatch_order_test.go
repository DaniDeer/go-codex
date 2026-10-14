package zeromq

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

// TestClientPublish_RequireHWM_NoCapability_FailsBeforeMiddlewareRuns is
// a REGRESSION GUARD for Phase 4's events/reqreply family, Workstream C:
// `transport.go`'s (*transport).Publish (the reflect path, backing
// Client.Attach's Client.Publish) previously ran the capability-coverage
// check LAST — AFTER middleware dispatch and security implementations
// dispatch had already executed — the SAME ordering divergence from
// [publish][T] (the generic path, which checks coverage FIRST) mqtt5's
// Workstream A step 6 found and fixed. This proves the fix: a
// declared-but-unmet [events.CapabilityRequirement] now fails BEFORE a
// PublishBoundMW Fn runs, mirroring the generic path's behavior exactly.
func TestClientPublish_RequireHWM_NoCapability_FailsBeforeMiddlewareRuns(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	var middlewareRan bool
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("side-effect"),
		func(_ context.Context, _ sensorReading) (tdEmpty, error) {
			middlewareRan = true
			return tdEmpty{}, nil
		})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.RequireHWM(10),
	).WithPublish(events.Publish{Summary: "test"}).PublishBoundMW(bm)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	// Deliberately supply ZERO matching Capability.
	err := client.Publish(context.Background(), pub, reading)
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
	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 0 {
		t.Errorf("want 0 sent frames, got %d", len(sock.sentFrames))
	}
}

// TestClientPublish_RequireHWM_SuppliedCapability_RunsMiddlewareAndPublishes
// confirms the ordering fix is not a regression: supplying a matching
// HWM Capability still runs the PublishBoundMW Fn and publishes
// successfully through Client.Attach's reflect Publish path.
func TestClientPublish_RequireHWM_SuppliedCapability_RunsMiddlewareAndPublishes(t *testing.T) {
	sock := &mockSocket{}
	client := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := client.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	var middlewareRan bool
	bm := events.NewBoundPublishMiddleware(newTDEmptyDeclaration("side-effect"),
		func(_ context.Context, _ sensorReading) (tdEmpty, error) {
			middlewareRan = true
			return tdEmpty{}, nil
		})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.RequireHWM(10),
	).WithPublish(events.Publish{Summary: "test"}).
		WithOptions(PublishOptions[sensorReading]{Capabilities: []Capability{HWM(10)}}).
		PublishBoundMW(bm)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := client.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !middlewareRan {
		t.Error("PublishBoundMW Fn must run when capability coverage passes")
	}
	sock.mu.Lock()
	defer sock.mu.Unlock()
	if len(sock.sentFrames) != 1 {
		t.Errorf("want 1 sent message, got %d", len(sock.sentFrames))
	}
}
