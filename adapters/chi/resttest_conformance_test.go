package chi

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/rest/resttest"
)

// TestResttestConformance_Echo/PathParam/Security run adapters/chi
// against api/rest/resttest's canonical conformance suite (Phase 5's
// "API-defined conformance test suites" mechanism, designed in Round 3
// of docs/roadmap/adapter-integration-architecture.md and implemented by
// api/rest/resttest) — this package's own, concrete proof that it
// satisfies api/rest's contract, independent of this package's own
// hand-written tests.
func TestResttestConformance_Echo(t *testing.T) {
	h, err := serveOne(resttest.EchoRoute())
	if err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	resttest.AssertEchoRoute(t, h)
}

func TestResttestConformance_PathParam(t *testing.T) {
	h, err := serveOne(resttest.PathParamRoute())
	if err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	resttest.AssertPathParamRoute(t, h)
}

func TestResttestConformance_Security(t *testing.T) {
	h, err := serveOne(resttest.SecurityRoute())
	if err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	resttest.AssertSecurityRoute(t, h)
}
