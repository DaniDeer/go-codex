package mqtt5

import (
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
