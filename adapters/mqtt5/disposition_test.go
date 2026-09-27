package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

type mockDispositionObserverEvents struct {
	stats.NoopObserver
	dispositions []middleware.Disposition
}

func (o *mockDispositionObserverEvents) RecordDisposition(_ string, d middleware.Disposition) {
	o.dispositions = append(o.dispositions, d)
}

// TestServeSubscribers_Disposition_ExplicitSignalResolvedAndObserved
// confirms a subscribe handler's middleware.SetDisposition call is
// resolved via middleware.ResolveDisposition and reported via
// stats.DispositionObserver — proving the Handler Disposition plumbing
// (docs/design/d-0006-protocol-native-capabilities.md's §8) end-to-end through
// adapters/mqtt5's real ServeSubscribers dispatch path, even though mqtt5
// itself has no acknowledgement concept of its own.
func TestServeSubscribers_Disposition_ExplicitSignalResolvedAndObserved(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	obs := &mockDispositionObserverEvents{}

	ch := events.NewChannel[sensorReading]("sensors/disposition", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(ctx context.Context, r sensorReading) error {
			middleware.SetDisposition(ctx, middleware.DispositionNackDiscard)
			return nil
		}).
		WithOptions(SubscribeOptions{Observer: obs})

	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	router.waitHandler("sensors/disposition")
	router.dispatch("sensors/disposition", &pahomqtt5.Publish{
		Topic: "sensors/disposition", Payload: []byte(validSensorJSON),
	})
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if len(obs.dispositions) != 1 || obs.dispositions[0] != middleware.DispositionNackDiscard {
		t.Errorf("want [DispositionNackDiscard], got %v", obs.dispositions)
	}
}
