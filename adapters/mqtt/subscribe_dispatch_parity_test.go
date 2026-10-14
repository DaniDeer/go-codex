package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// These tests close the gaps found (and fixed) by Phase 4's events/
// reqreply family, Workstream B — mqtt v3's equivalent of mqtt5's
// earlier case 6/Workstream A reconciliation. See
// docs/roadmap/adapter-integration-architecture.md's "Workstream B"
// progress subsection for the full investigation and decision record.
// Each test proves a capability that was PREVIOUSLY missing on one
// specific dispatch path, now present via parity with the other(s).

type mockDispositionObserverEvents struct {
	stats.NoopObserver
	dispositions []stats.Disposition
}

func (o *mockDispositionObserverEvents) RecordDisposition(_ string, d stats.Disposition) {
	o.dispositions = append(o.dispositions, d)
}

// TestServeSubscribers_TopicVarMerge_NowSupported closes the confirmed
// gap on the ServeSubscribers/Client.Attach dispatch path (caller.go's
// subscribeEntryReflect): it previously had NO topic-template-var
// support at all (decoded the payload directly, ignoring the channel's
// own topic template), unlike `transport.go`'s Subscribe (ports.Pattern
// binding) and the generic subscribeHandler[T] escape hatch, both of
// which already supported this. Mirrors adapters/mqtt5's identically-
// named regression test — SAME channel topic/merge-field shape,
// dispatched through ServeSubscribers instead.
func TestServeSubscribers_TopicVarMerge_NowSupported(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
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

	caller := newCaller(client, evtClient)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Payload JSON deliberately carries a DIFFERENT sensorID — merge
	// must OVERWRITE it with the value extracted from the concrete
	// topic, exactly like the generic path's own identical regression
	// test.
	client.subscribedHandlerSnapshot()(client, &mockMessage{
		topic:   "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings",
		payload: []byte(`{"sensorID":"00000000-0000-0000-0000-000000000000","value":22.5}`),
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
// path) and the generic subscribeHandler[T] escape hatch: NEITHER had
// Disposition tracking at all, unlike `subscribeEntryReflect`
// (ServeSubscribers/Client.Attach), which already had it. Mirrors
// adapters/mqtt5's identically-named regression test.
func TestAttach_ClientSubscribe_Disposition_NowSupported(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	obs := &mockDispositionObserverEvents{}

	sub := events.NewChannel[sensorReading]("sensors/disposition", sensorCodec).WithSubscribe(events.Subscribe{})
	ctx, cancel := context.WithTimeout(stats.WithObserver(context.Background(), obs), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Subscribe(ctx, sub, func(ctx context.Context, _ sensorReading) error {
			stats.SetDisposition(ctx, stats.DispositionNackDiscard)
			return nil
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if h := client.subscribedHandlerSnapshot(); h != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	client.subscribedHandlerSnapshot()(client, &mockMessage{
		topic:   "sensors/disposition",
		payload: []byte(`{"sensorID":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.5}`),
	})
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if len(obs.dispositions) != 1 || obs.dispositions[0] != stats.DispositionNackDiscard {
		t.Errorf("want [DispositionNackDiscard], got %v", obs.dispositions)
	}
}

// TestSubscribeHandler_Disposition_NowSupported closes the generic
// subscribeHandler[T] escape hatch's identical Disposition gap —
// mirrors adapters/mqtt5's TestSubscribeWithHandle_Disposition_NowSupported.
func TestSubscribeHandler_Disposition_NowSupported(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newHandle()
	obs := &mockDispositionObserverEvents{}
	ctx := stats.WithObserver(context.Background(), obs)

	h := subscribeHandler(ctx, client, handle, func(ctx context.Context, _ userEvent) error {
		stats.SetDisposition(ctx, stats.DispositionAck)
		return nil
	}, SubscribeOptions{})

	h(client, &mockMessage{payload: []byte(`{"id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","email":"a@b.com"}`)})

	if len(obs.dispositions) != 1 || obs.dispositions[0] != stats.DispositionAck {
		t.Errorf("want [DispositionAck], got %v", obs.dispositions)
	}
}
