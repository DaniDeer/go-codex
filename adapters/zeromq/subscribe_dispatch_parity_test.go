package zeromq

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// These tests close the two gaps found (and fixed) by Phase 4's events/
// reqreply family, Workstream C's parity fix for `transport.go`'s
// (*transport).Subscribe (the ports.Pattern-binding path) — mirroring
// mqtt5's/mqtt v3's own case-6-style reconciliations. See
// docs/roadmap/adapter-integration-architecture.md's "Workstream C"
// progress subsection for the full investigation and decision record.

type mockDispositionObserverEvents struct {
	stats.NoopObserver
	dispositions []stats.Disposition
}

func (o *mockDispositionObserverEvents) RecordDisposition(_ string, d stats.Disposition) {
	o.dispositions = append(o.dispositions, d)
}

// noForbidden rejects any topic containing the substring "forbidden" —
// used as a deterministic topic constraint below. The DECLARED channel
// topic template "sensors/{sensorID}/readings" never contains
// "forbidden" (so [Subscriber.Handle]'s own template-stripped-topic
// validation at declare time passes unconditionally), but a specific
// CONCRETE delivered topic is crafted to contain it — isolating the
// receive-time [events.ChannelHandle.ValidateTopic] check (this test's
// actual target) from declare-time validation.
var noForbidden = codex.Constraint[string]{
	Name:    "no-forbidden",
	Check:   func(v string) bool { return !strings.Contains(v, "forbidden") },
	Message: func(v string) string { return fmt.Sprintf("topic must not contain 'forbidden', got %q", v) },
}

// TestAttach_ClientSubscribe_ValidateTopic_NowSupported closes the
// confirmed gap on `transport.go`'s Subscribe: it previously never
// called [events.ChannelHandle.ValidateTopic] on a RECEIVED concrete
// topic at all, unlike ServeSubscribers'/
// (*subscriberRoute).processMessage, which has always run it before
// decode — a channel-level topic constraint (declared via
// [events.WithTopicConstraints]/[events.WithTopicCodec]) was silently
// never enforced against incoming messages on this path.
func TestAttach_ClientSubscribe_ValidateTopic_NowSupported(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/forbidden/readings"), []byte(validSensorJSON)}},
	}
	evClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}), events.WithTopicConstraints(noForbidden))
	if err := evClient.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", codex.String(),
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{})

	var handlerCalled bool
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := evClient.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
		handlerCalled = true
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if handlerCalled {
		t.Error("handler must not be called when ValidateTopic rejects the concrete topic")
	}
}

// TestAttach_ClientSubscribe_Disposition_NowSupported closes the
// confirmed gap on `transport.go`'s Subscribe: it previously had NO
// Disposition tracking at all, unlike ServeSubscribers'/
// (*subscriberRoute).processMessage, which already had it.
func TestAttach_ClientSubscribe_Disposition_NowSupported(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{{[]byte("sensors/readings"), []byte(validSensorJSON)}},
	}
	evClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evClient.Attach(NewTransport(TransportOptions{Socket: sock})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	obs := &mockDispositionObserverEvents{}

	sub := sensorChannel("sensors/readings").WithSubscribe(events.Subscribe{})
	ctx, cancel := context.WithTimeout(stats.WithObserver(context.Background(), obs), 300*time.Millisecond)
	defer cancel()
	if err := evClient.Subscribe(ctx, sub, func(ctx context.Context, _ sensorReading) error {
		stats.SetDisposition(ctx, stats.DispositionNackDiscard)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if len(obs.dispositions) != 1 || obs.dispositions[0] != stats.DispositionNackDiscard {
		t.Errorf("want [DispositionNackDiscard], got %v", obs.dispositions)
	}
}
