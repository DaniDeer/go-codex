package mqtt5

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply/reqreplytest"
)

// TestReqreplytestConformance_ComputeRoundTrip runs adapters/mqtt5
// against api/reqreply/reqreplytest's canonical Call/Serve conformance
// scenario — this package's own, concrete proof that it satisfies
// api/reqreply's contract, independent of this package's own
// hand-written tests (including Phase 3 case 4's own
// TestCrossTransportContract_ComputeRoundTrip, which this scenario
// reuses the identical contract from). Phase 5's "API-defined
// conformance test suites" mechanism
// (docs/roadmap/adapter-integration-architecture.md, Round 3 decision
// 1), reqreply-family instance.
func TestReqreplytestConformance_ComputeRoundTrip(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}
	server := NewServerTransport(ServerTransportOptions{Client: client, Router: router})
	clientTransport := NewClientTransport(ClientTransportOptions{Client: client, Router: router})

	reqreplytest.AssertComputeRoundTrip(t, server, clientTransport)
}
