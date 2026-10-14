package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

// TestApplySharedSubscriptionGroup_EmptyGroup_LeavesFilterUnchanged is the
// zero-value regression: empty group leaves filter unchanged — no
// behavior change for existing routes (docs/roadmap/
// mqtt5-capability-extensions.md's design history: Option B was chosen
// specifically because it needs no reordering/behavior change for
// callers who never set SharedSubscriptionGroup).
func TestApplySharedSubscriptionGroup_EmptyGroup_LeavesFilterUnchanged(t *testing.T) {
	if got := applySharedSubscriptionGroup("sensors/+/data", ""); got != "sensors/+/data" {
		t.Errorf("want unchanged filter, got %q", got)
	}
}

// TestApplySharedSubscriptionGroup_NonEmptyGroup_PrefixesFilter proves the
// `$share/<group>/` prefix is applied correctly.
func TestApplySharedSubscriptionGroup_NonEmptyGroup_PrefixesFilter(t *testing.T) {
	want := "$share/workers/sensors/+/data"
	if got := applySharedSubscriptionGroup("sensors/+/data", "workers"); got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// TestSubscribeAdapter_SharedSubscriptionGroup_PrefixesFilter proves
// [SubscribeAdapterOptions.SharedSubscriptionGroup] prefixes BOTH the
// router-registered filter AND the broker subscription's Topic with the
// SAME `$share/<group>/`-wrapped filter — the ports.SourceAdapter
// binding call site.
func TestSubscribeAdapter_SharedSubscriptionGroup_PrefixesFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle := newSensorSubscribeHandle()
	broker := &mockClient{}
	router := newMockRouter()
	p, err := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, SubscribeAdapter(broker, router, handle, format.JSON(sensorCodec),
		SubscribeAdapterOptions{SharedSubscriptionGroup: "workers"}))
	_ = p.Stream(ctx)

	wantFilter := "$share/workers/sensors/data"
	router.waitHandler(wantFilter)

	broker.mu.Lock()
	defer broker.mu.Unlock()
	if len(broker.subscribed) != 1 || len(broker.subscribed[0].Subscriptions) != 1 {
		t.Fatalf("want exactly 1 subscription, got %+v", broker.subscribed)
	}
	if got := broker.subscribed[0].Subscriptions[0].Topic; got != wantFilter {
		t.Errorf("want broker subscribe Topic %q, got %q", wantFilter, got)
	}
}

// TestServeSubscribers_SharedSubscriptionGroup_PrefixesFilter proves
// [SubscribeOptions.SharedSubscriptionGroup] prefixes the broker
// subscription's Topic on the ServeSubscribers (Client.Attach) path.
func TestServeSubscribers_SharedSubscriptionGroup_PrefixesFilter(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))

	ch := events.NewChannel[sensorReading]("sensors/shared", sensorCodec)
	sub := ch.WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, sensorReading) error { return nil }).
		WithOptions(SubscribeOptions{SharedSubscriptionGroup: "workers"})

	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	caller := newCaller(client, router, evtClient)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- caller.ServeSubscribers(ctx) }()

	router.waitHandler("$share/workers/sensors/shared")
	cancel()
	<-done

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.subscribed) != 1 || len(client.subscribed[0].Subscriptions) != 1 {
		t.Fatalf("expected exactly 1 subscription, got %+v", client.subscribed)
	}
	wantFilter := "$share/workers/sensors/shared"
	if got := client.subscribed[0].Subscriptions[0].Topic; got != wantFilter {
		t.Errorf("want broker subscribe Topic %q, got %q", wantFilter, got)
	}
}

// TestTransportSubscribe_SharedSubscriptionGroup_PrefixesFilter proves
// the ports.Pattern-binding `transport.Subscribe` path also honors
// SharedSubscriptionGroup, extracted via
// [subscribeHandlerOptsFields]'s reflection.
func TestTransportSubscribe_SharedSubscriptionGroup_PrefixesFilter(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	tr := NewTransport(TransportOptions{Client: client, Router: router})

	sub := events.NewChannel[sensorReading]("sensors/transport-shared", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		WithOptions(SubscribeOptions{SharedSubscriptionGroup: "workers"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = tr.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil })
	}()

	wantFilter := "$share/workers/sensors/transport-shared"
	router.waitHandler(wantFilter)

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		client.mu.Lock()
		n := len(client.subscribed)
		client.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for a broker subscription")
		}
		time.Sleep(5 * time.Millisecond)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if got := client.subscribed[0].Subscriptions[0].Topic; got != wantFilter {
		t.Errorf("want broker subscribe Topic %q, got %q", wantFilter, got)
	}
}
