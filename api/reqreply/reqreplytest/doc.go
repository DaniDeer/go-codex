// Package reqreplytest is api/reqreply's importable conformance-test
// suite — the reqreply-family instance of the "API-defined conformance
// test suites" mechanism (docs/roadmap/adapter-integration-architecture.md,
// Round 3 decision 1), the same mechanism api/rest/resttest implements
// for REST and api/events/eventstest implements for events.
//
// Unlike resttest (every REST adapter speaks a uniform net/http.Handler)
// and eventstest (two adapters share a live, two-way loopback mock
// broker idiom under different names), this package targets exactly ONE
// real adapter this round: adapters/mqtt5. adapters/mqtt (v3) has NO
// reqreply surface at all (confirmed via Phase 1's own audit — it is an
// events-only adapter), and adapters/zeromq's mock socket is the SAME
// one-way/scripted limitation already documented for eventstest — its
// own existing TestCrossTransportContract_ComputeRoundTrip (a reduced-
// rigor, split-half test) remains its accepted conformance evidence
// until a live broker is available in this sandboxed environment.
//
// A real simplification versus resttest/eventstest: reqreply.ServerTransport.Serve
// and reqreply.ClientTransport.Call both accept EITHER a raw, unregistered
// reqreply.Route[Req,Resp] or an already-registered *reqreply.RouteHandle[Req,Resp]
// (confirmed via recoverRouteHandleValue's own dual-mode acceptance) — so
// this package's canonical route needs no scratch reqreply.Server or
// registration step at all. And because adapters/mqtt5's own existing
// mock broker fixture (brokerClient) already auto-dispatches a published
// request to its registered Serve handler internally, NO adapter-specific
// bridging callback (unlike eventstest's isReady/deliver) is needed
// either — the suite accepts the real, standard
// reqreply.ServerTransport/reqreply.ClientTransport interfaces directly.
//
// Usage (mirrors adapters/mqtt5's own Phase 3 case 4 cross-transport
// contract test, now via an importable, reusable, API-owned suite
// instead of a bespoke local test):
//
//	func TestReqreplytestConformance_ComputeRoundTrip(t *testing.T) {
//		router := newMockRouter()
//		client := &brokerClient{router: router}
//		server := mqtt5.NewServerTransport(mqtt5.ServerTransportOptions{Client: client, Router: router})
//		clientTransport := mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: client, Router: router})
//		reqreplytest.AssertComputeRoundTrip(t, server, clientTransport)
//	}
//
// Coverage is intentionally ONE scenario this round (a basic Call/Serve
// round trip) — mirroring resttest's and eventstest's own "start with 1,
// expand later" pacing. This package's proof value is lower than
// resttest's or eventstest's (only 1 real adapter can use it at full
// rigor today) — stated plainly here, not overclaimed; it still fulfills
// the "API-defined, adapter-author-facing contract test" goal Round 3
// envisioned, reusing the SAME ComputeReq/ComputeResp/"/compute" contract
// Phase 3 case 4 already established as this roadmap's canonical reqreply
// example.
package reqreplytest
