package mqtt

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/events/eventstest"
)

// TestEventstestConformance_SensorRoundTrip runs adapters/mqtt (v3)
// against api/events/eventstest's canonical publish/subscribe
// conformance scenario — this package's own, concrete proof that it
// satisfies api/events's contract, independent of this package's own
// hand-written tests. Phase 5's "API-defined conformance test suites"
// mechanism (docs/roadmap/adapter-integration-architecture.md, Round 3
// decision 1), events-family instance.
func TestEventstestConformance_SensorRoundTrip(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	eventstest.AssertPublishSubscribeRoundTrip(t, evtClient,
		func() bool { return client.subscribedHandlerSnapshot() != nil },
		func() {
			h := client.subscribedHandlerSnapshot()
			h(client, &mockMessage{
				topic:   client.publishedTopicSnapshot(),
				payload: client.publishedPayloadSnapshot(),
			})
		},
	)
}

func TestEventstestConformance_TopicVarRoundTrip(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	eventstest.AssertTopicVarMergeRoundTrip(t, evtClient,
		func() bool { return client.subscribedHandlerSnapshot() != nil },
		func() {
			h := client.subscribedHandlerSnapshot()
			h(client, &mockMessage{
				topic:   client.publishedTopicSnapshot(),
				payload: client.publishedPayloadSnapshot(),
			})
		},
	)
}

func TestEventstestConformance_CapabilityRejection(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	sub := eventstest.NewCapabilityRejectionChannel().WithSubscribe(events.Subscribe{}).
		WithHandler(func(_ context.Context, _ eventstest.CapabilityRejectionReading) error { return nil })
	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	err := evtClient.ServeSubscribers(context.Background())
	eventstest.AssertCapabilityRejected(t, err)
}
