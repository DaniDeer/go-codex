package rest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
)

// fakeClientTransport is a minimal rest.ClientTransport fake — just
// enough for Client.Call/Consume to delegate through it, so the
// registry's auto-populate step (which runs BEFORE delegating) can be
// exercised without a real HTTP round trip.
type fakeClientTransport struct {
	calls int
}

func (f *fakeClientTransport) Call(ctx context.Context, route any, req any, opts ...rest.ClientCallOptions) (any, error) {
	f.calls++
	return userResp{}, nil
}

func (f *fakeClientTransport) Consume(ctx context.Context, sseRoute any, req any, fn any, opts ...rest.ClientConsumeOptions) error {
	f.calls++
	return nil
}

func TestClient_RegisterRoute_WarmsRegistryWithoutLiveCall(t *testing.T) {
	c := rest.NewClient()
	target := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"})

	if err := c.RegisterRoute(target); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}
	handle, ok := c.MatchRedirectRoute("GET", "/users/f47ac10b")
	if !ok {
		t.Fatal("MatchRedirectRoute: want match after RegisterRoute, got none")
	}
	if _, ok := handle.(*rest.RouteHandle[createReq, userResp]); !ok {
		t.Errorf("MatchRedirectRoute: want *rest.RouteHandle[createReq, userResp], got %T", handle)
	}
}

func TestClient_Call_AutoPopulatesRegistry(t *testing.T) {
	c := rest.NewClient()
	ft := &fakeClientTransport{}
	if err := c.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	route := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"})

	// Before any Call, the registry has nothing.
	if _, ok := c.MatchRedirectRoute("GET", "/users/abc"); ok {
		t.Fatal("MatchRedirectRoute: want no match before any Call")
	}

	if _, err := c.Call(context.Background(), route, createReq{}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if ft.calls != 1 {
		t.Fatalf("fakeClientTransport.calls: want 1, got %d", ft.calls)
	}
	if _, ok := c.MatchRedirectRoute("GET", "/users/abc"); !ok {
		t.Fatal("MatchRedirectRoute: want match after Call (auto-populate), got none")
	}
}

func TestClient_RegisterRoute_SameRouteKey_DifferentVariant_IsSafeNoOp(t *testing.T) {
	c := rest.NewClient()
	base := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"})
	// A "different variant" of the identical route — same Method+Path,
	// different attached general-purpose middleware (stands in for a
	// different credential-bound ClientMW variant; what matters here is
	// it's a DIFFERENT Go value with the IDENTICAL Method+Path).
	variant := base.Use()

	if err := c.RegisterRoute(base); err != nil {
		t.Fatalf("RegisterRoute(base): %v", err)
	}
	if err := c.RegisterRoute(variant); err != nil {
		t.Fatalf("RegisterRoute(variant): want safe no-op, got error: %v", err)
	}
	if _, ok := c.MatchRedirectRoute("GET", "/users/xyz"); !ok {
		t.Fatal("MatchRedirectRoute: want match after re-registering the same routeKey")
	}
}

func TestClient_MatchRedirectRoute_NoMatch(t *testing.T) {
	c := rest.NewClient()
	target := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"})
	if err := c.RegisterRoute(target); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}
	if _, ok := c.MatchRedirectRoute("GET", "/orders/123"); ok {
		t.Error("MatchRedirectRoute: want no match for an unregistered path")
	}
	if _, ok := c.MatchRedirectRoute("POST", "/users/123"); ok {
		t.Error("MatchRedirectRoute: want no match for a registered path with a different Method")
	}
}

func TestClient_RegisterRoute_SSERoute_GoesToSeparateRegistry(t *testing.T) {
	c := rest.NewClient()
	target := rest.NewSSERoute[createReq, userResp]("/stream/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"})
	if err := c.RegisterRoute(target); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}
	if _, ok := c.MatchRedirectRoute("GET", "/stream/abc"); ok {
		t.Error("MatchRedirectRoute (Call-decodable registry): want NO match for an SSE-only registration")
	}
	handle, ok := c.MatchRedirectSSERoute("GET", "/stream/abc")
	if !ok {
		t.Fatal("MatchRedirectSSERoute: want match for the SSE registration")
	}
	if _, ok := handle.(*rest.SSERouteHandle[createReq, userResp]); !ok {
		t.Errorf("MatchRedirectSSERoute: want *rest.SSERouteHandle[createReq, userResp], got %T", handle)
	}
}

func TestClient_MatchRedirectRoute_AmbiguousMatch_FirstRegisteredWins(t *testing.T) {
	c := rest.NewClient()
	// Two DIFFERENT routes whose templates both happen to match the same
	// concrete path+Method ("/users/static" matches both the literal
	// "/users/static" route and the templated "/users/{id}" route) —
	// distinguished below by the matched handle's OWN Descriptor.Path
	// (its declared template), independent of Go's unordered map
	// iteration.
	first := rest.NewRoute[createReq, userResp]("GET", "/users/static", createReqCodec, userCodec)
	second := rest.NewRoute[createReq, userResp]("GET", "/users/{id}", createReqCodec, userCodec, rest.PathParam{Name: "id"})

	if err := c.RegisterRoute(first); err != nil {
		t.Fatalf("RegisterRoute(first): %v", err)
	}
	if err := c.RegisterRoute(second); err != nil {
		t.Fatalf("RegisterRoute(second): %v", err)
	}
	handleAny, ok := c.MatchRedirectRoute("GET", "/users/static")
	if !ok {
		t.Fatal("MatchRedirectRoute: want a match")
	}
	handle, ok := handleAny.(*rest.RouteHandle[createReq, userResp])
	if !ok {
		t.Fatalf("MatchRedirectRoute: want *rest.RouteHandle[createReq, userResp], got %T", handleAny)
	}
	if handle.Descriptor.Path != "/users/static" {
		t.Errorf("MatchRedirectRoute: want the FIRST-registered route's own template (/users/static), got %q", handle.Descriptor.Path)
	}
}

func TestClient_RegisterRoute_InvalidType_ReturnsTransportTypeMismatchError(t *testing.T) {
	c := rest.NewClient()
	err := c.RegisterRoute("not a route")
	var mmErr rest.TransportTypeMismatchError
	if !errors.As(err, &mmErr) {
		t.Fatalf("RegisterRoute: want TransportTypeMismatchError, got %T: %v", err, err)
	}
}
