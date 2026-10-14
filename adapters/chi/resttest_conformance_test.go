package chi

import (
	"net/http"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
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

func TestResttestConformance_MiddlewareChain(t *testing.T) {
	resttest.AssertMiddlewareChainRoute(t, func(order *[]string) http.Handler {
		h, err := serveOne(resttest.NewMiddlewareChainRoute(order))
		if err != nil {
			t.Fatalf("serveOne: %v", err)
		}
		return h
	})
}

func TestResttestConformance_ErrorMapping(t *testing.T) {
	h, err := serveOne(resttest.ErrorMappingRoute())
	if err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	resttest.AssertErrorMappingRoute(t, h)
}

func TestResttestConformance_CapabilityRejection(t *testing.T) {
	_, err := serveOne(resttest.CapabilityRejectionRoute())
	resttest.AssertCapabilityRejected(t, err)
}

func TestResttestConformance_Observability(t *testing.T) {
	// chi has no Observability function of its own — it deliberately
	// reuses nethttp.Observability as-is, since both operate on the
	// framework-agnostic func(http.Handler) http.Handler shape (see
	// adapters/chi/adapter.go's own doc comment pointing to
	// nethttp.Observability).
	spy := &resttest.SpyObserver{}
	h, err := serveOne(resttest.ObservabilityRoute(nethttp.Observability(spy)))
	if err != nil {
		t.Fatalf("serveOne: %v", err)
	}
	resttest.AssertObservabilityRoute(t, h, spy)
}
