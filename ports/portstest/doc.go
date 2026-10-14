// Package portstest is the ports package's importable conformance-test
// suite — the ports-family instance of the "API-defined conformance
// test suites" mechanism (docs/roadmap/adapter-integration-architecture.md,
// Round 3 decision 1), the same mechanism api/rest/resttest,
// api/events/eventstest, and api/reqreply/reqreplytest implement for
// their own families.
//
// This family is structurally different from the other three: Phase 1's
// audit confirmed adapters/file, adapters/redis, and adapters/sql have
// NO capability-requirement/coverage-check mechanism at all — so there
// is no capability-rejection or dispatch-core-duplication scenario to
// prove here (that part of the roadmap's ambiguity about this family is
// confirmed closed, not deferred). What this package instead proves is
// [ports.SourcePort]'s own binding-lifecycle contract
// ([ports.SourceAdapter.Activate] → [ports.SourcePort.Bind] →
// [ports.SourcePort.Stream]) — a genuinely shared Go interface
// implemented by many structurally-independent adapters across the
// whole codebase (confirmed via [ports.SourceAdapter]'s own doc comment:
// adapters/mqtt5, adapters/mqtt, adapters/nethttp, adapters/zeromq,
// adapters/chi, adapters/websocket, adapters/file, and adapters/sql all
// implement it) — not a request/response protocol family the way REST,
// events, and reqreply are.
//
// Usage (mirrors adapters/sql's own existing SourcePort tests):
//
//	func TestPortstestConformance_SourceAdapterRoundTrip(t *testing.T) {
//		queryFn := func(_ context.Context) ([]portstest.Reading, error) { return portstest.WantReadings, nil }
//		adapter := sql.QueryAdapter(portstest.ReadingCodec, queryFn, 20*time.Millisecond, sql.QueryStreamOptions{})
//		portstest.AssertSourceAdapterRoundTrip(t, portstest.ReadingCodec, adapter, portstest.WantReadings)
//	}
//
// Coverage is intentionally ONE scenario this round
// ([ports.SourceAdapter] only) — [ports.SinkAdapter] and
// [ports.IOAdapter] conformance scenarios are explicitly left for a
// future round, mirroring every other conformance package's own
// "start with 1, expand later" pacing. adapters/redis is scoped out of
// THIS specific scenario (its [ports.Cache] read-through/write-through
// pattern is a different shape than a plain [ports.SourceAdapter]), not
// excluded from the ports family generally.
//
// A real behavioral difference handled deliberately: some
// [ports.SourceAdapter] implementations emit their items exactly once
// then terminate (e.g. adapters/file.ScanAdapter reaching EOF), while
// others poll continuously and re-emit the SAME items every tick (e.g.
// adapters/sql.QueryAdapter, confirmed directly from its own existing
// tests). [AssertSourceAdapterRoundTrip] therefore asserts that the
// FIRST len(want) collected items match want exactly, not that exactly
// len(want) items were collected in total — accommodating both emission
// shapes without forcing a false assumption about either.
package portstest
