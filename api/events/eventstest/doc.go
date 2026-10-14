// Package eventstest is api/events's importable conformance-test suite —
// the events-family instance of the "API-defined conformance test
// suites" mechanism (docs/roadmap/adapter-integration-architecture.md,
// Round 3 decision 1), the same mechanism api/rest/resttest implements
// for the REST family.
//
// Unlike REST — where every adapter speaks a uniform [net/http.Handler],
// testable identically via [net/http/httptest] — events adapters have no
// uniform mock-broker mechanism: each adapter's own test suite implements
// its own in-process mock client/router (or mock socket) to simulate a
// broker, and these mocks are NOT structurally interchangeable. This
// package therefore owns everything that genuinely IS shared (the
// canonical channel declaration, registering a subscriber, starting
// [events.Client.ServeSubscribers], calling the real
// [events.Client.Publish] encode path, and asserting the decoded result)
// and accepts exactly two small, adapter-specific bridging callbacks for
// the one piece that cannot be unified: moving a published message into
// the adapter's own mock-delivery mechanism.
//
// Coverage in this round is intentionally representative, not exhaustive
// — one scenario (a basic publish/subscribe round trip), validating the
// whole deliver-callback mechanism design before investing in more
// scenarios, mirroring how api/rest/resttest itself started at 3
// scenarios and only expanded to 7 in a later, separate round.
//
// adapters/zeromq cannot use this suite at full rigor: its mock socket
// test harness is fundamentally one-way/scripted (canned input frames in,
// recorded output frames out), not a live two-way loopback broker like
// adapters/mqtt5's and adapters/mqtt's own mocks — the same limitation
// Phase 3 case 4 of the roadmap already documented and worked around with
// a reduced-rigor, explicitly cross-referenced pair of split-half tests
// (adapters/zeromq's own TestCrossTransportContract_ComputeRoundTrip).
// adapters/zeromq is deliberately NOT wired into this package; its own
// existing split-half tests remain its accepted conformance evidence
// until a live broker is available in this environment.
//
// Usage (mirrors adapters/mqtt5's and adapters/mqtt's own conformance
// tests):
//
//	func TestEventstestConformance_SensorRoundTrip(t *testing.T) {
//		client := &mockClient{}
//		router := newMockRouter()
//		evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
//		if err := evtClient.Attach(mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})); err != nil {
//			t.Fatalf("Attach: %v", err)
//		}
//		eventstest.AssertPublishSubscribeRoundTrip(t, evtClient,
//			func() bool { return router.hasHandler(eventstest.SensorTopic) },
//			func() { router.dispatch(eventstest.SensorTopic, client.lastPublished()) },
//		)
//	}
package eventstest
