package nethttp

import (
	"net/http"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest/resttest"
)

// TestResttestConformance_Echo/PathParam/Security run adapters/nethttp
// against api/rest/resttest's canonical conformance suite (Phase 5's
// "API-defined conformance test suites" mechanism, designed in Round 3
// of docs/roadmap/adapter-integration-architecture.md and implemented by
// api/rest/resttest) — this package's own, concrete proof that it
// satisfies api/rest's contract, independent of this package's own
// hand-written tests.
func TestResttestConformance_Echo(t *testing.T) {
	h, err := ServeOne(resttest.EchoRoute())
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}
	resttest.AssertEchoRoute(t, h)
}

func TestResttestConformance_PathParam(t *testing.T) {
	h, err := ServeOne(resttest.PathParamRoute())
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}
	resttest.AssertPathParamRoute(t, h)
}

func TestResttestConformance_Security(t *testing.T) {
	h, err := ServeOne(resttest.SecurityRoute())
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}
	resttest.AssertSecurityRoute(t, h)
}

func TestResttestConformance_MiddlewareChain(t *testing.T) {
	resttest.AssertMiddlewareChainRoute(t, func(order *[]string) http.Handler {
		h, err := ServeOne(resttest.NewMiddlewareChainRoute(order))
		if err != nil {
			t.Fatalf("ServeOne: %v", err)
		}
		return h
	})
}

func TestResttestConformance_ErrorMapping(t *testing.T) {
	h, err := ServeOne(resttest.ErrorMappingRoute())
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}
	resttest.AssertErrorMappingRoute(t, h)
}

func TestResttestConformance_CapabilityRejection(t *testing.T) {
	_, err := ServeOne(resttest.CapabilityRejectionRoute())
	resttest.AssertCapabilityRejected(t, err)
}

func TestResttestConformance_Observability(t *testing.T) {
	spy := &resttest.SpyObserver{}
	h, err := ServeOne(resttest.ObservabilityRoute(Observability(spy)))
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}
	resttest.AssertObservabilityRoute(t, h, spy)
}
