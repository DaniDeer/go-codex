package reqreplytest

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
)

// ComputeReq/ComputeResp are [NewComputeRoute]'s request/response types —
// the SAME canonical "/compute" contract (X+Y=Sum) Phase 3 case 4 of
// docs/roadmap/adapter-integration-architecture.md already established,
// cited identically in both adapters/mqtt5's and adapters/zeromq's own
// existing TestCrossTransportContract_ComputeRoundTrip tests. Reusing it
// here (rather than inventing a new contract) keeps this package's
// canonical example consistent with the roadmap's own prior precedent.
type ComputeReq struct{ X, Y int }
type ComputeResp struct{ Sum int }

var computeReqCodec = codex.Struct[ComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r ComputeReq) int { return r.X },
		func(r *ComputeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r ComputeReq) int { return r.Y },
		func(r *ComputeReq, v int) { r.Y = v },
	),
)

var computeRespCodec = codex.Struct[ComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r ComputeResp) int { return r.Sum },
		func(r *ComputeResp, v int) { r.Sum = v },
	),
)

// ComputeTopic is [NewComputeRoute]'s topic.
const ComputeTopic = "/compute"

// NewComputeRoute is the canonical, undecorated route declaration every
// conforming reqreply adapter must correctly Serve and Call — no
// registration/Server step needed: both
// [reqreply.ServerTransport.Serve] and [reqreply.ClientTransport.Call]
// accept a raw, unregistered [reqreply.Route] directly.
func NewComputeRoute() reqreply.Route[ComputeReq, ComputeResp] {
	return reqreply.NewRoute[ComputeReq, ComputeResp](ComputeTopic, computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "reqreplytestCompute"},
	)
}

// AssertComputeRoundTrip proves server/client (an already live-wired
// [reqreply.ServerTransport]/[reqreply.ClientTransport] pair — e.g. both
// built against the SAME adapter-specific mock broker fixture) correctly
// serve and call [NewComputeRoute]: the server's handler computes
// X+Y=Sum, and the client receives that exact value back.
//
// Unlike [eventstest]'s AssertPublishSubscribeRoundTrip, this function
// takes NO adapter-specific bridging callbacks — see this package's own
// doc comment for why none are needed.
func AssertComputeRoundTrip(t *testing.T, server reqreply.ServerTransport, client reqreply.ClientTransport) {
	t.Helper()

	route := NewComputeRoute()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Serve(ctx, route, func(_ context.Context, req ComputeReq) (ComputeResp, error) {
		return ComputeResp{Sum: req.X + req.Y}, nil
	}); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	respAny, err := client.Call(ctx, route, ComputeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, ok := respAny.(ComputeResp)
	if !ok {
		t.Fatalf("Call returned %T, want ComputeResp", respAny)
	}
	if got.Sum != 7 {
		t.Errorf("Sum = %d, want 7 (3+4)", got.Sum)
	}
}
