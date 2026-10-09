package reqreply_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type routerTestReq struct{ X int }

var routerTestReqCodec = codex.Struct[routerTestReq](
	codex.RequiredField("x", codex.Int(),
		func(r routerTestReq) int { return r.X },
		func(r *routerTestReq, v int) { r.X = v },
	),
)

type routerTestResp struct{ Y int }

var routerTestRespCodec = codex.Struct[routerTestResp](
	codex.RequiredField("y", codex.Int(),
		func(r routerTestResp) int { return r.Y },
		func(r *routerTestResp, v int) { r.Y = v },
	),
)

func routerTestHandler(_ context.Context, req routerTestReq) (routerTestResp, error) {
	return routerTestResp{Y: req.X * 2}, nil
}

func newRouterTestRoute(topic string) reqreply.Route[routerTestReq, routerTestResp] {
	return reqreply.NewRoute[routerTestReq, routerTestResp](topic, routerTestReqCodec, routerTestRespCodec).
		WithHandler(routerTestHandler)
}

// ── tests ────────────────────────────────────────────────────────────────

func TestRouter_Route_ComposesPrefixCorrectly(t *testing.T) {
	route := newRouterTestRoute("add")
	rt := reqreply.NewRouter("compute").Route(route)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Path != "compute/add" {
		t.Errorf("want path %q, got %q", "compute/add", entries[0].Path)
	}
}

func TestRouter_Route_NoLeadingSlashForced(t *testing.T) {
	rt := reqreply.NewRouter("").Route(newRouterTestRoute("x"))
	entries := rt.Routes()
	if entries[0].Path != "x" {
		t.Errorf("reqreply topics must never get a forced leading '/', got %q", entries[0].Path)
	}
}

func TestRouter_Mount_ComposesNestedPrefixesTransitively(t *testing.T) {
	inner := reqreply.NewRouter("v1").Route(newRouterTestRoute("add"))
	outer := reqreply.NewRouter("compute").Mount(inner)

	entries := outer.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	want := "compute/v1/add"
	if entries[0].Path != want {
		t.Errorf("want path %q, got %q", want, entries[0].Path)
	}
}

func TestRouter_Use_AppliesToEveryGroupedLeaf(t *testing.T) {
	mw := middleware.Middleware{Name: "audit"}

	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	rt := reqreply.NewRouter("compute").
		Use(mw).
		Route(newRouterTestRoute("a")).
		Route(newRouterTestRoute("b"))

	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	for _, e := range rt.Routes() {
		found := false
		for _, name := range e.MiddlewareNames {
			if name == "audit" {
				found = true
			}
		}
		if !found {
			t.Errorf("topic %q missing Router-contributed middleware %q", e.Path, "audit")
		}
	}
}

func TestRouter_Use_DeclarationOrderIsDispatchOrder(t *testing.T) {
	outer := middleware.Middleware{Name: "outer"}
	route := newRouterTestRoute("x").Use(middleware.Middleware{Name: "inner"})
	rt := reqreply.NewRouter("compute").Use(outer).Route(route)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	names := entries[0].MiddlewareNames
	if len(names) != 2 || names[0] != "outer" || names[1] != "inner" {
		t.Errorf("want [outer inner], got %v", names)
	}
}

func TestRouter_Use_NestedMountAccumulatesOuterToInner(t *testing.T) {
	outer := middleware.Middleware{Name: "outer"}
	middleMw := middleware.Middleware{Name: "middle"}
	inner := middleware.Middleware{Name: "inner"}
	route := newRouterTestRoute("x").Use(middleware.Middleware{Name: "leaf-own"})

	innerRouter := reqreply.NewRouter("inner").Use(inner).Route(route)
	middleRouter := reqreply.NewRouter("middle").Use(middleMw).Mount(innerRouter)
	outerRouter := reqreply.NewRouter("outer").Use(outer).Mount(middleRouter)

	entries := outerRouter.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	wantNames := []string{"outer", "middle", "inner", "leaf-own"}
	names := entries[0].MiddlewareNames
	if len(names) != len(wantNames) {
		t.Fatalf("want %v, got %v", wantNames, names)
	}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("want middleware order %v, got %v", wantNames, names)
			break
		}
	}
}

func TestRouter_DuplicateMiddlewareName_ReturnsTypedError(t *testing.T) {
	// Uses the codec-backed reqreply.Middleware[In,Out] family (via
	// reqreply.NewMiddleware) rather than the bare legacy
	// middleware.Middleware marker type — mirrors api/rest's identical
	// scoping (checkMiddlewareNameUniquenessAndAttachment walks
	// middlewareSpecContributions, populated unconditionally regardless
	// of whether a receive/send fn is attached).
	dup := reqreply.NewMiddleware(middleware.Declaration[struct{}, struct{}]{Name: "dup"})
	route := newRouterTestRoute("x").Use(reqreply.NewMiddleware(middleware.Declaration[struct{}, struct{}]{Name: "dup"}))
	rt := reqreply.NewRouter("compute").Use(dup).Route(route)

	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	err := rt.Register(server)
	var dupErr reqreply.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v (%T)", err, err)
	}
	var prefixErr reqreply.RouterPrefixError
	if errors.As(err, &prefixErr) {
		t.Errorf("want DuplicateMiddlewareNameError to propagate unwrapped, got it wrapped in RouterPrefixError")
	}
}

func TestRouter_Routes_IncludesBoundMiddlewareNames(t *testing.T) {
	bm := reqreply.NewBoundMiddleware[routerTestReq](
		middleware.Declaration[struct{}, struct{}]{Name: "bound-audit"},
		func(_ context.Context, _ *routerTestReq, _ struct{}) (struct{}, error) { return struct{}{}, nil },
	)
	route := newRouterTestRoute("bound").HandleBoundMW(bm)
	rt := reqreply.NewRouter("compute").Route(route)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	names := entries[0].MiddlewareNames
	found := false
	for _, n := range names {
		if n == "bound-audit" {
			found = true
		}
	}
	if !found {
		t.Errorf("want MiddlewareNames to include bound middleware name %q, got %v", "bound-audit", names)
	}
}

func TestRouter_Group_SharesPrefixAddsScopedMiddlewareOnly(t *testing.T) {
	scoped := middleware.Middleware{Name: "scoped"}
	rt := reqreply.NewRouter("compute").
		Route(newRouterTestRoute("open")).
		Group(func(sub reqreply.Router) reqreply.Router {
			return sub.Use(scoped).Route(newRouterTestRoute("guarded"))
		})

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		hasScoped := false
		for _, n := range e.MiddlewareNames {
			if n == "scoped" {
				hasScoped = true
			}
		}
		switch e.Path {
		case "compute/open":
			if hasScoped {
				t.Errorf("open leaf (outside Group) should NOT have scoped middleware")
			}
		case "compute/guarded":
			if !hasScoped {
				t.Errorf("guarded leaf (inside Group) should have scoped middleware")
			}
		default:
			t.Errorf("unexpected path %q", e.Path)
		}
	}
}

func TestRouter_With_AppliesToNextRouteOnlyNotSiblings(t *testing.T) {
	mw := middleware.Middleware{Name: "oneshot"}
	rt := reqreply.NewRouter("compute")
	rt = rt.With(mw).Route(newRouterTestRoute("a"))
	rt = rt.Route(newRouterTestRoute("b"))

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		has := false
		for _, n := range e.MiddlewareNames {
			if n == "oneshot" {
				has = true
			}
		}
		switch e.Path {
		case "compute/a":
			if !has {
				t.Errorf("a should have the one-shot middleware")
			}
		case "compute/b":
			if has {
				t.Errorf("b should NOT have the one-shot middleware")
			}
		}
	}
}

func TestRouter_With_RepeatedCallsAccumulateNotOverwrite(t *testing.T) {
	mw1 := middleware.Middleware{Name: "mw1"}
	mw2 := middleware.Middleware{Name: "mw2"}
	rt := reqreply.NewRouter("compute").With(mw1).With(mw2).Route(newRouterTestRoute("a"))

	entries := rt.Routes()
	names := entries[0].MiddlewareNames
	if len(names) != 2 || names[0] != "mw1" || names[1] != "mw2" {
		t.Errorf("want both mw1 and mw2 accumulated, got %v", names)
	}
}

func TestRouter_With_DiscardedByIntervalMount_NotLeakedToLaterRoute(t *testing.T) {
	suspicious := middleware.Middleware{Name: "suspicious-oneshot"}
	inner := reqreply.NewRouter("inner").Route(newRouterTestRoute("a"))
	rt := reqreply.NewRouter("compute").With(suspicious).Mount(inner).Route(newRouterTestRoute("b"))

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		for _, name := range e.MiddlewareNames {
			if name == "suspicious-oneshot" {
				t.Errorf("path %q: want .With() before Mount() to be discarded, not leaked, got middleware %q", e.Path, name)
			}
		}
	}
}

func TestRouter_Immutability_OriginalValueUnaffectedByChaining(t *testing.T) {
	rt := reqreply.NewRouter("compute")
	mw := middleware.Middleware{Name: "mw"}
	rt2 := rt.Use(mw).Route(newRouterTestRoute("a"))

	if len(rt.Routes()) != 0 {
		t.Errorf("original Router mutated: want 0 routes, got %d", len(rt.Routes()))
	}
	if len(rt2.Routes()) != 1 {
		t.Errorf("want 1 route on rt2, got %d", len(rt2.Routes()))
	}
}

func TestRouter_Routes_EquivalentToWalkCollected(t *testing.T) {
	rt := reqreply.NewRouter("compute").
		Route(newRouterTestRoute("a")).
		Route(newRouterTestRoute("b"))

	var walked []reqreply.RouterEntry
	err := rt.Walk(func(e reqreply.RouterEntry) error {
		walked = append(walked, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	routes := rt.Routes()
	if len(walked) != len(routes) {
		t.Fatalf("want %d walked entries, got %d", len(routes), len(walked))
	}
	for i := range routes {
		a, b := walked[i], routes[i]
		if a.Path != b.Path || len(a.MiddlewareNames) != len(b.MiddlewareNames) {
			t.Errorf("entry %d mismatch: walked=%+v routes=%+v", i, a, b)
		}
	}
}

func TestRouter_Walk_StopsOnFirstError(t *testing.T) {
	sentinel := errors.New("stop")
	rt := reqreply.NewRouter("compute").
		Route(newRouterTestRoute("a")).
		Route(newRouterTestRoute("b"))

	calls := 0
	err := rt.Walk(func(reqreply.RouterEntry) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("want sentinel error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("want exactly 1 call before stopping, got %d", calls)
	}
}

func TestRouter_Register_IndistinguishableFromDirectRegister(t *testing.T) {
	serverA := reqreply.NewServer(reqreply.Info{Title: "a", Version: "1"})
	serverB := reqreply.NewServer(reqreply.Info{Title: "b", Version: "1"})

	directHandle, err := newRouterTestRoute("compute/add").Register(serverA)
	if err != nil {
		t.Fatalf("direct Register: %v", err)
	}

	var routedHandle *reqreply.RouteHandle[routerTestReq, routerTestResp]
	route := reqreply.NewRoute[routerTestReq, routerTestResp]("add", routerTestReqCodec, routerTestRespCodec,
		reqreply.WithHandleCallback(func(h *reqreply.RouteHandle[routerTestReq, routerTestResp]) { routedHandle = h }),
	).WithHandler(routerTestHandler)
	rt := reqreply.NewRouter("compute").Route(route)
	if err := rt.Register(serverB); err != nil {
		t.Fatalf("Router Register: %v", err)
	}
	if routedHandle == nil {
		t.Fatal("want WithHandleCallback to fire")
	}
	if directHandle.Topic != routedHandle.Topic {
		t.Errorf("want same topic, got %q vs %q", directHandle.Topic, routedHandle.Topic)
	}

	// Beyond spec (topic) equality: verify the two handles actually
	// DISPATCH identically — same decode/encode round trip for a success
	// case, and the same decode failure for an invalid payload.
	reqBytes := []byte(`{"x":21}`)
	directReq, err := directHandle.Decode(reqBytes)
	if err != nil {
		t.Fatalf("direct Decode: %v", err)
	}
	routedReq, err := routedHandle.Decode(reqBytes)
	if err != nil {
		t.Fatalf("routed Decode: %v", err)
	}
	if directReq != routedReq {
		t.Errorf("want identical decode result, got %+v vs %+v", directReq, routedReq)
	}

	directResp, directErr := routerTestHandler(context.Background(), directReq)
	routedResp, routedErr := routerTestHandler(context.Background(), routedReq)
	if directErr != nil || routedErr != nil {
		t.Fatalf("handler errors: direct=%v routed=%v", directErr, routedErr)
	}
	directOut, err := directHandle.Encode(directResp)
	if err != nil {
		t.Fatalf("direct Encode: %v", err)
	}
	routedOut, err := routedHandle.Encode(routedResp)
	if err != nil {
		t.Fatalf("routed Encode: %v", err)
	}
	if string(directOut) != string(routedOut) {
		t.Errorf("want identical encode output, got %q vs %q", directOut, routedOut)
	}

	badBytes := []byte(`{"x":"not-a-number"}`)
	_, directDecodeErr := directHandle.Decode(badBytes)
	_, routedDecodeErr := routedHandle.Decode(badBytes)
	if (directDecodeErr == nil) != (routedDecodeErr == nil) {
		t.Errorf("want same error presence for invalid payload, direct=%v routed=%v", directDecodeErr, routedDecodeErr)
	}
}

func TestRouter_InvalidComposedTopic_ReturnsRouterPrefixError(t *testing.T) {
	denyDoubleSlash := codex.Constraint[string]{
		Name:    "no-double-slash",
		Check:   func(v string) bool { return !containsDoubleSlash(v) },
		Message: func(v string) string { return "topic must not contain //" },
	}
	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"}, reqreply.WithTopicConstraints(denyDoubleSlash))
	rt := reqreply.NewRouter("compute").Route(newRouterTestRoute("//add"))

	err := rt.Register(server)
	var prefixErr reqreply.RouterPrefixError
	if !errors.As(err, &prefixErr) {
		t.Fatalf("want RouterPrefixError, got %v (%T)", err, err)
	}
}

func containsDoubleSlash(v string) bool {
	for i := 0; i+1 < len(v); i++ {
		if v[i] == '/' && v[i+1] == '/' {
			return true
		}
	}
	return false
}

func TestRouter_NonPrefixErrors_PropagateUnwrapped(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	route := newRouterTestRoute("x")
	rt := reqreply.NewRouter("compute").Route(route).Route(route)

	err := rt.Register(server)
	var prefixErr reqreply.RouterPrefixError
	if errors.As(err, &prefixErr) {
		t.Fatalf("non-prefix errors must propagate UNWRAPPED, got RouterPrefixError: %v", err)
	}
	var dupErr reqreply.DuplicateRouteError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateRouteError, got %v (%T)", err, err)
	}
}

func TestRouter_ConcurrentReads_SafeByImmutability(t *testing.T) {
	base := reqreply.NewRouter("compute")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := base.Route(newRouterTestRoute("x"))
			_ = r.Routes()
		}()
	}
	wg.Wait()
	if len(base.Routes()) != 0 {
		t.Errorf("base Router must remain untouched by concurrent derivations")
	}
}

func TestWithHandleCallback_FiresOnRegister(t *testing.T) {
	var got *reqreply.RouteHandle[routerTestReq, routerTestResp]
	route := reqreply.NewRoute[routerTestReq, routerTestResp]("x", routerTestReqCodec, routerTestRespCodec,
		reqreply.WithHandleCallback(func(h *reqreply.RouteHandle[routerTestReq, routerTestResp]) { got = h }),
	).WithHandler(routerTestHandler)

	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	rt := reqreply.NewRouter("compute").Route(route)
	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got == nil {
		t.Fatal("want WithHandleCallback to fire, got nil handle")
	}
}

func TestRoute_WithOpt_AttachesHandleCallbackToAlreadyDeclaredRoute(t *testing.T) {
	// Mirrors the real examples/reqreply-api/mqtt5server scenario:
	// a route declared ONCE in a shared, transport-agnostic var (no
	// WithHandleCallback at declaration time) later needs a
	// server-local handle-recovery hook attached — without
	// re-declaring its topic/codecs/RouteMeta from scratch.
	declared := newRouterTestRoute("x")

	var got *reqreply.RouteHandle[routerTestReq, routerTestResp]
	decorated := declared.WithOpt(reqreply.WithHandleCallback(
		func(h *reqreply.RouteHandle[routerTestReq, routerTestResp]) { got = h },
	))

	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	if _, err := decorated.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got == nil {
		t.Fatal("want WithHandleCallback (attached via WithOpt) to fire")
	}

	// The ORIGINAL, undecorated value must remain untouched — Route is
	// immutable, same guarantee every other decoration method provides.
	server2 := reqreply.NewServer(reqreply.Info{Title: "t2", Version: "1"})
	got = nil
	if _, err := declared.Register(server2); err != nil {
		t.Fatalf("Register (original): %v", err)
	}
	if got != nil {
		t.Error("original, undecorated Route must NOT have the callback attached")
	}
}

func TestClientHandle_WithRouter_MatchesRegisteredPath(t *testing.T) {
	route := newRouterTestRoute("add")
	rt := reqreply.NewRouter("compute")

	handle := route.ClientHandle(reqreply.WithRouter(rt))
	if handle == nil {
		t.Fatal("want non-nil handle")
	}
	const want = "compute/add"
	if handle.Topic != want {
		t.Errorf("want topic %q, got %q", want, handle.Topic)
	}

	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	var registeredHandle *reqreply.RouteHandle[routerTestReq, routerTestResp]
	routeWithCallback := reqreply.NewRoute[routerTestReq, routerTestResp]("add", routerTestReqCodec, routerTestRespCodec,
		reqreply.WithHandleCallback(func(h *reqreply.RouteHandle[routerTestReq, routerTestResp]) { registeredHandle = h }),
	).WithHandler(routerTestHandler)
	if err := rt.Route(routeWithCallback).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registeredHandle == nil {
		t.Fatal("want WithHandleCallback to fire")
	}
	if registeredHandle.Topic != handle.Topic {
		t.Errorf("want ClientHandle(WithRouter(rt)) to match the registered topic exactly: %q vs %q",
			handle.Topic, registeredHandle.Topic)
	}
}

func TestClientHandle_ZeroArgs_StillCompilesAndWorks(t *testing.T) {
	route := newRouterTestRoute("add")
	handle := route.ClientHandle()
	if handle == nil {
		t.Fatal("want non-nil handle")
	}
}

func TestRouter_Tags_AccumulateNotOverwrite(t *testing.T) {
	rt := reqreply.NewRouter("compute").Tags("a").Tags("b").Route(newRouterTestRoute("x"))
	entries := rt.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("want both tags accumulated, got %v", tags)
	}
}

func TestRouter_Tags_SurviveLeafsOwnRouteMeta(t *testing.T) {
	route := reqreply.NewRoute[routerTestReq, routerTestResp]("x", routerTestReqCodec, routerTestRespCodec,
		reqreply.RouteMeta{OperationID: "x", Tags: []string{"own-tag"}},
	).WithHandler(routerTestHandler)
	rt := reqreply.NewRouter("compute").Tags("router-tag").Route(route)

	entries := rt.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "router-tag" || tags[1] != "own-tag" {
		t.Errorf("want [router-tag own-tag] (Router's own first, then the leaf's), got %v", tags)
	}

	server := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1"})
	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRouter_Tags_AccumulateAcrossMount(t *testing.T) {
	inner := reqreply.NewRouter("v1").Tags("v1").Route(newRouterTestRoute("add"))
	outer := reqreply.NewRouter("compute").Tags("compute").Mount(inner)

	entries := outer.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "compute" || tags[1] != "v1" {
		t.Errorf("want [compute v1] (outermost ancestor first), got %v", tags)
	}
}

func TestRouter_Tags_RoutesEquivalentToWalk(t *testing.T) {
	rt := reqreply.NewRouter("compute").Tags("t1").Route(newRouterTestRoute("a"))
	var walked []reqreply.RouterEntry
	_ = rt.Walk(func(e reqreply.RouterEntry) error {
		walked = append(walked, e)
		return nil
	})
	routes := rt.Routes()
	if len(walked[0].Tags) != len(routes[0].Tags) || walked[0].Tags[0] != routes[0].Tags[0] {
		t.Errorf("Walk and Routes disagree on Tags: %v vs %v", walked[0].Tags, routes[0].Tags)
	}
}

// TestRoute_DoesNotImplementMethodReporter is a regression test for
// docs/design/d-0009-internalize-shared-mechanics.md's Phase 1 Design Decision #4
// (the RouterEntry Method/Role/neither mismatch): reqreply.Route
// genuinely has no method/role-equivalent concept — it must NOT satisfy
// [router.MethodReporter] at all, confirming RouterEntry's absent method
// field reflects a real structural absence, not an oversight.
func TestRoute_DoesNotImplementMethodReporter(t *testing.T) {
	route := newRouterTestRoute("a")
	if _, ok := any(route).(interface{ RouteMethod() string }); ok {
		t.Fatalf("reqreply.Route unexpectedly implements RouteMethod() string (MethodReporter) — it should have no method/role concept at all")
	}
}
