// Package resttest is api/rest's importable conformance-test suite —
// the mechanism Round 3 of docs/roadmap/adapter-integration-architecture.md
// designed and this package implements: "a shared, importable `_test`-only
// helper package per API family... exposing a function-shaped entry point
// rather than a type hierarchy," mirroring Go stdlib conventions
// (net/http/httptest, testing/fstest, testing/iotest).
//
// Any adapter claiming to implement api/rest's contract (adapters/nethttp,
// adapters/chi, and any future REST adapter) builds an [http.Handler] from
// one of this package's canonical routes using its OWN ServeOne/serveOne
// function, then passes that handler to the matching Assert function.
// Each Assert function fires a scripted battery of requests at the handler
// and asserts the exact behavior every conforming adapter must produce —
// this is the "last enforcement point" the roadmap's Phase 5 describes:
// a regression that silently breaks one adapter's handling of a capability
// another adapter already implements correctly is caught here, at
// `go test` time, not by a new example exercising an unguarded path.
//
// This is deliberately NOT a testify-style Suite type (an object-model
// layer go-codex doesn't use elsewhere) and NOT a CI-generated checklist
// (not self-enforcing at test time) — both were explicitly considered and
// rejected during the roadmap's design round.
//
// Usage (mirrors adapters/nethttp's and adapters/chi's own conformance
// tests):
//
//	func TestConformance_Echo(t *testing.T) {
//		h, err := nethttp.ServeOne(resttest.EchoRoute())
//		if err != nil {
//			t.Fatalf("ServeOne: %v", err)
//		}
//		resttest.AssertEchoRoute(t, h)
//	}
//
// Coverage in this round is intentionally representative, not exhaustive:
// a basic decode/encode route (EchoRoute), a path-param merge-field route
// (PathParamRoute), and a general-purpose security-gate route
// (SecurityRoute). Further canonical routes (middleware chains, error
// patterns, capability rejection) can be added the same way in future
// rounds without breaking this package's existing exported surface.
package resttest
