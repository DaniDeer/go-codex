package reqreply_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// registryFakeClientTransport is a minimal reqreply.ClientTransport fake
// — just enough for Client.Call/CallAsync to delegate through it, so the
// registry's auto-populate step (which runs BEFORE delegating) can be
// exercised without a real adapter round trip. Mirrors api/rest's
// identical fakeClientTransport convention.
type registryFakeClientTransport struct {
	calls int
}

func (f *registryFakeClientTransport) Call(ctx context.Context, route any, req any, opts ...reqreply.ClientCallOptions) (any, error) {
	f.calls++
	return computeResp{}, nil
}

func (f *registryFakeClientTransport) CallRaw(ctx context.Context, topic string, payload []byte, properties map[string]string) ([]byte, error) {
	f.calls++
	return payload, nil
}

func TestClient_RegisterRoute_WarmsRegistryWithoutLiveCall(t *testing.T) {
	c := reqreply.NewClient()
	target := reqreply.NewRoute[computeReq, computeResp]("compute/registered-only", reqCodec, respCodec)

	if err := c.RegisterRoute(target); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}
	// No public read accessor exists (Option B — adapters/callers stay
	// registry-unaware, see docs/roadmap/reqreply-client-registry.md's
	// "Adapter↔registry bridge"); re-registering the SAME topic with the
	// SAME concrete type is observable indirectly as a safe no-op.
	if err := c.RegisterRoute(target); err != nil {
		t.Fatalf("RegisterRoute (re-register, same type): want safe no-op, got error: %v", err)
	}
}

func TestClient_RegisterRoute_InvalidType_ReturnsTransportTypeMismatchError(t *testing.T) {
	c := reqreply.NewClient()
	var mismatchErr reqreply.TransportTypeMismatchError
	if err := c.RegisterRoute("not a route"); !errors.As(err, &mismatchErr) {
		t.Fatalf("RegisterRoute(wrong type): want TransportTypeMismatchError, got %v", err)
	}
}

func TestClient_RegisterRoute_SameTopic_DifferentType_ReturnsDuplicateTopicTypeMismatchError(t *testing.T) {
	c := reqreply.NewClient()
	first := reqreply.NewRoute[computeReq, computeResp]("compute/shared-topic", reqCodec, respCodec)
	// Genuinely different route type sharing the SAME topic — two
	// unrelated features colliding by coincidence, the exact scenario
	// docs/roadmap/reqreply-client-registry.md's collision detection
	// guards against (REST's own first-registered-wins policy is safe
	// only because re-registrations are guaranteed to be "the same
	// route"; reqreply has no such guarantee).
	conflicting := reqreply.NewRoute[tenantComputeReq, computeResp]("compute/shared-topic", tenantComputeReqCodec, respCodec)

	if err := c.RegisterRoute(first); err != nil {
		t.Fatalf("RegisterRoute(first): %v", err)
	}
	var dupErr reqreply.DuplicateTopicTypeMismatchError
	err := c.RegisterRoute(conflicting)
	if !errors.As(err, &dupErr) {
		t.Fatalf("RegisterRoute(conflicting): want DuplicateTopicTypeMismatchError, got %v", err)
	}
	if dupErr.Topic != "compute/shared-topic" {
		t.Errorf("DuplicateTopicTypeMismatchError.Topic: want %q, got %q", "compute/shared-topic", dupErr.Topic)
	}
	if dupErr.ExistingType == "" || dupErr.NewType == "" || dupErr.ExistingType == dupErr.NewType {
		t.Errorf("DuplicateTopicTypeMismatchError: want distinct, non-empty ExistingType/NewType, got %q / %q", dupErr.ExistingType, dupErr.NewType)
	}

	// The existing entry must NOT be clobbered by the rejected
	// registration — re-registering the ORIGINAL (first) type again must
	// still be a safe no-op, not a NEW conflict against itself.
	if err := c.RegisterRoute(first); err != nil {
		t.Fatalf("RegisterRoute(first again, after rejected conflict): want safe no-op, got error: %v", err)
	}
}

func TestClient_Call_AutoPopulatesRegistry(t *testing.T) {
	c := reqreply.NewClient()
	ft := &registryFakeClientTransport{}
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	route := reqreply.NewRoute[computeReq, computeResp]("compute/auto-populate", reqCodec, respCodec)

	if _, err := c.Call(context.Background(), route, computeReq{}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if ft.calls != 1 {
		t.Fatalf("registryFakeClientTransport.calls: want 1, got %d", ft.calls)
	}
	// Auto-populate ran (indirectly observable): registering the SAME
	// topic+type explicitly afterward is a safe no-op, not a fresh
	// first-time insert that a differently-shaped assertion might hide.
	if err := c.RegisterRoute(route); err != nil {
		t.Fatalf("RegisterRoute after auto-populating Call: want safe no-op, got error: %v", err)
	}
}

func TestClient_Call_AutoPopulate_ConflictingType_DoesNotErrorOrPanic(t *testing.T) {
	// registerAutoFromCall's OWN contract (mirrors Call/CallAsync's
	// "never errors" requirement): a detected conflict marks the
	// registry entry poisoned WITHOUT surfacing anything through
	// Call/CallAsync itself — there is no real consumer yet (Phase 1/2
	// are not implemented) to observe the poisoned marker directly, so
	// this test asserts the OBSERVABLE contract instead: Call keeps
	// succeeding normally, using whichever handler the fake transport
	// provides, regardless of the internal poisoning.
	c := reqreply.NewClient()
	ft := &registryFakeClientTransport{}
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	first := reqreply.NewRoute[computeReq, computeResp]("compute/conflict-topic", reqCodec, respCodec)
	conflicting := reqreply.NewRoute[tenantComputeReq, computeResp]("compute/conflict-topic", tenantComputeReqCodec, respCodec)

	if _, err := c.Call(context.Background(), first, computeReq{}); err != nil {
		t.Fatalf("Call(first): %v", err)
	}
	if _, err := c.Call(context.Background(), conflicting, tenantComputeReq{}); err != nil {
		t.Fatalf("Call(conflicting): want no error despite internal poisoning, got %v", err)
	}
	if ft.calls != 2 {
		t.Fatalf("registryFakeClientTransport.calls: want 2, got %d", ft.calls)
	}
}

func TestClient_CallAsync_AutoPopulatesRegistry(t *testing.T) {
	c := reqreply.NewClient()
	ft := &registryFakeClientTransport{}
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	route := reqreply.NewRoute[computeReq, computeResp]("compute/auto-populate-async", reqCodec, respCodec)

	futureAny, err := c.CallAsync(context.Background(), route, computeReq{})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	// CallAsync now wraps Client.Call in a background goroutine (the
	// confirmed, settled restructure — see docs/roadmap/
	// reqreply-forward-bounce.md's "CallAsync/Future" section) — the
	// transport call itself (ft.calls++) happens ASYNCHRONOUSLY, so this
	// must await the Future before asserting ft.calls, unlike the
	// synchronous registry auto-populate step above it (which still runs
	// BEFORE the goroutine launches).
	future := futureAny.(*reqreply.Future[computeResp])
	if _, err := future.Wait(context.Background()); err != nil {
		t.Fatalf("future.Wait: %v", err)
	}
	if ft.calls != 1 {
		t.Fatalf("registryFakeClientTransport.calls: want 1, got %d", ft.calls)
	}
	if err := c.RegisterRoute(route); err != nil {
		t.Fatalf("RegisterRoute after auto-populating CallAsync: want safe no-op, got error: %v", err)
	}
}

func TestClient_RegisterRoute_AcceptsRouteHandle(t *testing.T) {
	c := reqreply.NewClient()
	handle := reqreply.NewRoute[computeReq, computeResp]("compute/handle-form", reqCodec, respCodec).ClientHandle()
	if err := c.RegisterRoute(handle); err != nil {
		t.Fatalf("RegisterRoute(*RouteHandle): %v", err)
	}
	// Re-registering via the RAW Route form (recovering to the SAME
	// concrete handle type through ClientHandle()) must be the SAME
	// safe no-op RegisterRoute's doc comment promises, confirming
	// recoverRegistryEntry's dual-mode acceptance is consistent for the
	// SAME underlying route regardless of which form is passed.
	route := reqreply.NewRoute[computeReq, computeResp]("compute/handle-form", reqCodec, respCodec)
	if err := c.RegisterRoute(route); err != nil {
		t.Fatalf("RegisterRoute(Route, same topic+type as prior *RouteHandle): want safe no-op, got error: %v", err)
	}
}

func TestDuplicateTopicTypeMismatchError_ErrorAndLogValue(t *testing.T) {
	err := reqreply.DuplicateTopicTypeMismatchError{
		Topic:        "compute/shared-topic",
		ExistingType: "*reqreply.RouteHandle[foo.FooReq,foo.FooResp]",
		NewType:      "*reqreply.RouteHandle[bar.BarReq,bar.BarResp]",
	}
	if err.Error() == "" {
		t.Fatal("Error(): want non-empty message")
	}
	lv := err.LogValue()
	if lv.Kind().String() != "Group" {
		t.Fatalf("LogValue(): want a Group, got %s", lv.Kind())
	}
}
