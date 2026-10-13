package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// These tests close the two gaps found (and fixed) by Phase 3 case 6's
// 3-way reconciliation of mqtt5's Subscribe dispatch paths — see
// docs/roadmap/adapter-integration-architecture.md's "Phase 3 results"
// for the full investigation and decision record. Each test proves a
// capability that was PREVIOUSLY missing on one specific dispatch path,
// now present via parity with the other(s).

// TestServeSubscribers_TopicVarMerge_NowSupported closes the confirmed
// gap on the ServeSubscribers/Client.Attach dispatch path (caller.go's
// makeErasedSubscribeMessageHandler): it previously had NO
// topic-template-var support at all, unlike transport.go's Subscribe
// (ports.Pattern binding) and the generic subscribeWithHandle escape
// hatch, both of which already supported this (via two DIFFERENT
// mechanisms — see the roadmap's case 6 writeup). Mirrors
// adapter.go's TestSubscribe_MergeFields_AutoMergesTopicVars (the
// generic path's own regression test) — SAME channel topic/merge-field
// shape, dispatched through ServeSubscribers instead.
func TestServeSubscribers_TopicVarMerge_NowSupported(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	uuidCodec := codex.String().Refine(validate.UUID)
	var received sensorReading
	ch := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{}).WithHandler(func(_ context.Context, r sensorReading) error {
		received = r
		return nil
	})
	if err := ch.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	router.waitHandler("sensors/+/readings")
	// Payload JSON deliberately carries a DIFFERENT sensor_id — merge
	// must OVERWRITE it with the value extracted from the concrete
	// topic, exactly like the generic path's own identical regression
	// test.
	router.dispatch("sensors/+/readings", &pahomqtt5.Publish{
		Topic:   "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings",
		Payload: []byte(`{"sensor_id":"00000000-0000-0000-0000-000000000000","value":22.5}`),
	})
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("SensorID: want merged from topic, got %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", received.Value)
	}
}

// TestAttach_ClientSubscribe_Disposition_NowSupported closes the
// confirmed gap on transport.go's Subscribe (the ports.Pattern-binding
// reflection path, reached via events.Client.Attach +
// events.Client.Subscribe): it previously had NO Disposition tracking at
// all, unlike ServeSubscribers' own dispatch (caller.go), which already
// wires stats.EnsureDispositionBox/ResolveDisposition/RecordDisposition.
// Mirrors disposition_test.go's
// TestServeSubscribers_Disposition_ExplicitSignalResolvedAndObserved —
// SAME explicit stats.SetDisposition signal, dispatched through
// Client.Attach's Transport.Subscribe path instead.
func TestAttach_ClientSubscribe_Disposition_NowSupported(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	obs := &mockDispositionObserverEvents{}

	sub := plainSensorChannel("sensors/disposition").WithSubscribe(events.Subscribe{})
	// transport.go's Subscribe resolves its Observer from ctx (via
	// stats.ObserverFromContext), NOT from SubscribeOptions.Observer —
	// mirrors TestAttach_ClientSubscribe_RecordsObserver's identical
	// wiring, a pre-existing asymmetry with ServeSubscribers (which reads
	// opts.Observer) unrelated to this case-6 fix.
	ctx, cancel := context.WithTimeout(stats.WithObserver(context.Background(), obs), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(ctx context.Context, _ sensorReading) error {
			stats.SetDisposition(ctx, stats.DispositionNackDiscard)
			return nil
		})
	}()

	router.waitHandler("sensors/disposition")
	router.dispatch("sensors/disposition", &pahomqtt5.Publish{
		Topic: "sensors/disposition", Payload: []byte(validSensorJSON),
	})
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if len(obs.dispositions) != 1 || obs.dispositions[0] != stats.DispositionNackDiscard {
		t.Errorf("want [DispositionNackDiscard], got %v", obs.dispositions)
	}
}

// TestSubscribeWithHandle_Disposition_NowSupported closes the confirmed
// gap on the generic subscribeWithHandle/subscribe[T] escape hatch: it
// previously had NO Disposition tracking at all either. Mirrors
// disposition_test.go's identical structure, dispatched through the
// generic escape hatch instead.
func TestSubscribeWithHandle_Disposition_NowSupported(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	obs := &mockDispositionObserverEvents{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	handle := newChannelHandle()
	if err := subscribeWithHandle(ctx, client, router, handle,
		func(ctx context.Context, _ sensorReading) error {
			stats.SetDisposition(ctx, stats.DispositionNackDiscard)
			return nil
		},
		SubscribeOptions{Observer: obs}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if len(obs.dispositions) != 1 || obs.dispositions[0] != stats.DispositionNackDiscard {
		t.Errorf("want [DispositionNackDiscard], got %v", obs.dispositions)
	}
}
