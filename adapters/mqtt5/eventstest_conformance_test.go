package mqtt5

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/events/eventstest"
)

// TestEventstestConformance_SensorRoundTrip runs adapters/mqtt5 against
// api/events/eventstest's canonical publish/subscribe conformance
// scenario — this package's own, concrete proof that it satisfies
// api/events's contract, independent of this package's own hand-written
// tests. Phase 5's "API-defined conformance test suites" mechanism
// (docs/roadmap/adapter-integration-architecture.md, Round 3 decision 1),
// events-family instance.
func TestEventstestConformance_SensorRoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	eventstest.AssertPublishSubscribeRoundTrip(t, evtClient,
		func() bool { return router.hasHandler(eventstest.SensorTopic) },
		func() { router.dispatch(eventstest.SensorTopic, client.lastPublished()) },
	)
}

func TestEventstestConformance_TopicVarRoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// mockRouter.RegisterHandler keys on the DERIVED WILDCARD FILTER
	// ("eventstest/topicvar/+/reading"), not the raw "{id}" template or
	// the concrete topic — the dispatched message's own .Topic field
	// (set by client.lastPublished()) still carries the CONCRETE topic
	// used for topic-var extraction. Mirrors this package's own existing
	// merge-field tests' documented convention exactly.
	filter := deriveWildcardFilter(eventstest.TopicVarTopicTemplate)
	eventstest.AssertTopicVarMergeRoundTrip(t, evtClient,
		func() bool { return router.hasHandler(filter) },
		func() { router.dispatch(filter, client.lastPublished()) },
	)
}

func TestEventstestConformance_CapabilityRejection(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
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

func TestEventstestConformance_SecurityRejection(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	eventstest.AssertSecurityRejectionRoundTrip(t, evtClient,
		func() bool { return router.hasHandler(eventstest.SecurityRejectionTopic) },
		func() { router.dispatch(eventstest.SecurityRejectionTopic, client.lastPublished()) },
	)
}
