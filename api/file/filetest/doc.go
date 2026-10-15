// Package filetest provides API-defined conformance scenarios for any
// [file.Backend] implementation — mirrors
// [github.com/DaniDeer/go-codex/api/rest/resttest]/
// [github.com/DaniDeer/go-codex/api/events/eventstest]/
// [github.com/DaniDeer/go-codex/api/reqreply/reqreplytest]'s established
// 7-scenario taxonomy and signature convention (D-0010's "API-defined
// conformance test suite" mechanism), adapted for api/file: each
// Assert* function is parameterized over a [file.Backend] (the thing
// that varies per adapter — adapters/file today, adapters/s3/adapters/smb
// in Phase 2) rather than an http.Handler, giving every current and
// future Backend implementation the SAME proof-of-conformance suite to
// run against, instead of each adapter inventing its own ad hoc tests.
//
// Usage (see adapters/file's own conformance_test.go for the real,
// wired-in example):
//
//	func TestConformance(t *testing.T) {
//	    backend := adapterfile.NewBackend()
//	    filetest.AssertReadWriteRoundTrip(t, backend)
//	    filetest.AssertPathParamMergeField(t, backend)
//	    filetest.AssertSecurityGate(t, backend)
//	    filetest.AssertOrderedMiddlewareChain(t, backend)
//	    filetest.AssertErrorPatternMapping(t, backend)
//	    filetest.AssertObservability(t, backend, spy)
//	}
package filetest
