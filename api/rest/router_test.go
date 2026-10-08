package rest_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type routerTestReq struct{ Name string }

var routerTestReqCodec = codex.Struct[routerTestReq](
	codex.RequiredField("name", codex.String(),
		func(r routerTestReq) string { return r.Name },
		func(r *routerTestReq, v string) { r.Name = v },
	),
)

type routerTestResp struct{ Greeting string }

var routerTestRespCodec = codex.Struct[routerTestResp](
	codex.RequiredField("greeting", codex.String(),
		func(r routerTestResp) string { return r.Greeting },
		func(r *routerTestResp, v string) { r.Greeting = v },
	),
)

func routerTestHandler(_ context.Context, req routerTestReq) (routerTestResp, error) {
	return routerTestResp{Greeting: "hello " + req.Name}, nil
}

func newRouterTestRoute(method, path string) rest.Route[routerTestReq, routerTestResp] {
	return rest.NewRoute[routerTestReq, routerTestResp](method, path, routerTestReqCodec, routerTestRespCodec).
		WithHandler(routerTestHandler)
}

// ── tests ────────────────────────────────────────────────────────────────

func TestRouter_Route_ComposesPrefixCorrectly(t *testing.T) {
	route := newRouterTestRoute("GET", "/users")
	rt := rest.NewRouter("/api/v1").Route(route)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Path != "/api/v1/users" {
		t.Errorf("want path %q, got %q", "/api/v1/users", entries[0].Path)
	}
	if entries[0].Method != "GET" {
		t.Errorf("want method GET, got %q", entries[0].Method)
	}
}

func TestRouter_Mount_ComposesNestedPrefixesTransitively(t *testing.T) {
	inner := rest.NewRouter("/users").Route(newRouterTestRoute("GET", "/{id}"))
	outer := rest.NewRouter("/api/v1").Mount(inner)

	entries := outer.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	want := "/api/v1/users/{id}"
	if entries[0].Path != want {
		t.Errorf("want path %q, got %q", want, entries[0].Path)
	}
}

func TestRouter_Use_AppliesToEveryGroupedLeaf(t *testing.T) {
	var dispatched []string
	mw := middleware.Middleware{Name: "audit"}

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	rt := rest.NewRouter("/api").
		Use(mw).
		Route(newRouterTestRoute("GET", "/a")).
		Route(newRouterTestRoute("GET", "/b"))

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
			t.Errorf("path %q missing Router-contributed middleware %q", e.Path, "audit")
		}
	}
	_ = dispatched
}

func TestRouter_Use_DeclarationOrderIsDispatchOrder(t *testing.T) {
	outer := middleware.Middleware{Name: "outer"}
	route := newRouterTestRoute("GET", "/x").Use(middleware.Middleware{Name: "inner"})
	rt := rest.NewRouter("/api").Use(outer).Route(route)

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
	middle := middleware.Middleware{Name: "middle"}
	inner := middleware.Middleware{Name: "inner"}
	route := newRouterTestRoute("GET", "/x").Use(middleware.Middleware{Name: "leaf-own"})

	innerRouter := rest.NewRouter("/inner").Use(inner).Route(route)
	middleRouter := rest.NewRouter("/middle").Use(middle).Mount(innerRouter)
	outerRouter := rest.NewRouter("/outer").Use(outer).Mount(middleRouter)

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
	// Uses the codec-backed rest.Middleware[In,Out] family (via
	// rest.NewMiddleware) rather than the bare legacy middleware.Middleware
	// marker type: only codec-backed middleware values contribute a
	// middlewareSpecContribution, which is what the existing dedup check
	// (checkMiddlewareNameUniquenessAndAttachment) actually walks. Router's
	// .Use()/the leaf's own .Use() accept either via the shared
	// middleware.RouteMiddleware interface, but the dedup check is scoped
	// to the codec-backed family — see docs/design/d-0003-codec-declared-middlewares.md.
	dup := rest.NewMiddleware(middleware.Declaration[struct{}, struct{}]{Name: "dup"})
	route := newRouterTestRoute("GET", "/x").Use(rest.NewMiddleware(middleware.Declaration[struct{}, struct{}]{Name: "dup"}))
	rt := rest.NewRouter("/api").Use(dup).Route(route)

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	err := rt.Register(server)
	var dupErr rest.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v (%T)", err, err)
	}
	var prefixErr rest.RouterPrefixError
	if errors.As(err, &prefixErr) {
		t.Errorf("want DuplicateMiddlewareNameError to propagate unwrapped, got it wrapped in RouterPrefixError")
	}
}

func TestRouter_Routes_IncludesBoundMiddlewareNames(t *testing.T) {
	bm := rest.NewBoundMiddleware[routerTestReq](
		middleware.Declaration[mdTestIn, mdTestOut]{Name: "bound-audit"},
		func(_ context.Context, _ *routerTestReq, _ mdTestIn) (mdTestOut, error) { return mdTestOut{}, nil },
	)
	route := newRouterTestRoute("GET", "/bound").HandleBoundMW(bm)
	rt := rest.NewRouter("/api").Route(route)

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
	rt := rest.NewRouter("/api").
		Route(newRouterTestRoute("GET", "/open")).
		Group(func(sub rest.Router) rest.Router {
			return sub.Use(scoped).Route(newRouterTestRoute("POST", "/open"))
		})

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Path != "/api/open" {
			t.Errorf("want path %q (Group adds NO new segment), got %q", "/api/open", e.Path)
		}
		hasScoped := false
		for _, n := range e.MiddlewareNames {
			if n == "scoped" {
				hasScoped = true
			}
		}
		if e.Method == "POST" && !hasScoped {
			t.Errorf("POST leaf (inside Group) should have scoped middleware")
		}
		if e.Method == "GET" && hasScoped {
			t.Errorf("GET leaf (outside Group) should NOT have scoped middleware")
		}
	}
}

func TestRouter_With_AppliesToNextRouteOnlyNotSiblings(t *testing.T) {
	mw := middleware.Middleware{Name: "oneshot"}
	rt := rest.NewRouter("/api")
	rt = rt.With(mw).Route(newRouterTestRoute("GET", "/a"))
	rt = rt.Route(newRouterTestRoute("GET", "/b"))

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
		case "/api/a":
			if !has {
				t.Errorf("/a should have the one-shot middleware")
			}
		case "/api/b":
			if has {
				t.Errorf("/b should NOT have the one-shot middleware")
			}
		}
	}
}

func TestRouter_With_RepeatedCallsAccumulateNotOverwrite(t *testing.T) {
	mw1 := middleware.Middleware{Name: "mw1"}
	mw2 := middleware.Middleware{Name: "mw2"}
	rt := rest.NewRouter("/api").With(mw1).With(mw2).Route(newRouterTestRoute("GET", "/a"))

	entries := rt.Routes()
	names := entries[0].MiddlewareNames
	if len(names) != 2 || names[0] != "mw1" || names[1] != "mw2" {
		t.Errorf("want both mw1 and mw2 accumulated, got %v", names)
	}
}

func TestRouter_With_DiscardedByIntervalMount_NotLeakedToLaterRoute(t *testing.T) {
	suspicious := middleware.Middleware{Name: "suspicious-oneshot"}
	inner := rest.NewRouter("/inner").Route(newRouterTestRoute("GET", "/a"))
	rt := rest.NewRouter("/api").With(suspicious).Mount(inner).Route(newRouterTestRoute("GET", "/b"))

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
	rt := rest.NewRouter("/api")
	mw := middleware.Middleware{Name: "mw"}
	rt2 := rt.Use(mw).Route(newRouterTestRoute("GET", "/a"))

	if len(rt.Routes()) != 0 {
		t.Errorf("original Router mutated: want 0 routes, got %d", len(rt.Routes()))
	}
	if len(rt2.Routes()) != 1 {
		t.Errorf("want 1 route on rt2, got %d", len(rt2.Routes()))
	}
}

func TestRouter_Routes_EquivalentToWalkCollected(t *testing.T) {
	rt := rest.NewRouter("/api").
		Route(newRouterTestRoute("GET", "/a")).
		Route(newRouterTestRoute("POST", "/b"))

	var walked []rest.RouterEntry
	err := rt.Walk(func(e rest.RouterEntry) error {
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
		if a.Method != b.Method || a.Path != b.Path || len(a.MiddlewareNames) != len(b.MiddlewareNames) {
			t.Errorf("entry %d mismatch: walked=%+v routes=%+v", i, a, b)
			continue
		}
		for j := range a.MiddlewareNames {
			if a.MiddlewareNames[j] != b.MiddlewareNames[j] {
				t.Errorf("entry %d middleware name %d mismatch: %q vs %q", i, j, a.MiddlewareNames[j], b.MiddlewareNames[j])
			}
		}
	}
}

func TestRouter_Walk_StopsOnFirstError(t *testing.T) {
	sentinel := errors.New("stop")
	rt := rest.NewRouter("/api").
		Route(newRouterTestRoute("GET", "/a")).
		Route(newRouterTestRoute("GET", "/b"))

	calls := 0
	err := rt.Walk(func(rest.RouterEntry) error {
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
	serverA := rest.NewServer(rest.Info{Title: "a", Version: "1"})
	serverB := rest.NewServer(rest.Info{Title: "b", Version: "1"})

	directHandle, err := newRouterTestRoute("GET", "/users").RegisterHandle(serverA)
	if err != nil {
		t.Fatalf("direct RegisterHandle: %v", err)
	}

	var routedHandle *rest.RouteHandle[routerTestReq, routerTestResp]
	route := rest.NewRoute[routerTestReq, routerTestResp]("GET", "/users", routerTestReqCodec, routerTestRespCodec,
		rest.WithHandleCallback(func(h *rest.RouteHandle[routerTestReq, routerTestResp]) { routedHandle = h }),
	).WithHandler(routerTestHandler)
	rt := rest.NewRouter("").Route(route)
	if err := rt.Register(serverB); err != nil {
		t.Fatalf("Router Register: %v", err)
	}
	if routedHandle == nil {
		t.Fatal("want WithHandleCallback to fire")
	}
	if directHandle.Descriptor.Path != routedHandle.Descriptor.Path {
		t.Errorf("want same path, got %q vs %q", directHandle.Descriptor.Path, routedHandle.Descriptor.Path)
	}

	// Beyond spec (path) equality: verify the two handles actually DISPATCH
	// identically — same decode/encode round trip for a success case, and
	// the same decode failure for an invalid request body.
	reqBytes := []byte(`{"name":"ada"}`)
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

	badBytes := []byte(`{"name":123}`)
	_, directDecodeErr := directHandle.Decode(badBytes)
	_, routedDecodeErr := routedHandle.Decode(badBytes)
	if (directDecodeErr == nil) != (routedDecodeErr == nil) {
		t.Errorf("want same error presence for invalid body, direct=%v routed=%v", directDecodeErr, routedDecodeErr)
	}
}

func TestRouter_InvalidComposedPath_ReturnsRouterPrefixError(t *testing.T) {
	denyDoubleSlash := codex.Constraint[string]{
		Name:    "no-double-slash",
		Check:   func(v string) bool { return !containsDoubleSlash(v) },
		Message: func(v string) string { return "path must not contain //" },
	}
	server := rest.NewServer(rest.Info{Title: "t", Version: "1"}, rest.WithPathConstraints(denyDoubleSlash))
	// A leaf whose OWN path already starts with "/" composed under a
	// Router prefix that ALSO ends with "/" would normally be correctly
	// de-duplicated by joinRouterPath's own normalization — construct the
	// failure via the leaf's declared path containing the forbidden
	// substring directly in its post-composition form instead.
	rt := rest.NewRouter("/api").Route(newRouterTestRoute("GET", "//users"))

	err := rt.Register(server)
	var prefixErr rest.RouterPrefixError
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
	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	// A Req-mismatched HandleBoundMW value -> a genuine, Router-unrelated
	// BoundMiddlewareReqMismatchError — confirms Router.Register does NOT
	// wrap every possible leaf error in RouterPrefixError, only
	// path/topic-composition failures.
	route := newRouterTestRoute("GET", "/x").HandleBoundMW("not-a-bound-middleware")
	rt := rest.NewRouter("/api").Route(route)

	err := rt.Register(server)
	var prefixErr rest.RouterPrefixError
	if errors.As(err, &prefixErr) {
		t.Fatalf("non-prefix errors must propagate UNWRAPPED, got RouterPrefixError: %v", err)
	}
	var mismatchErr rest.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v (%T)", err, err)
	}
}

func TestRouter_ConcurrentReads_SafeByImmutability(t *testing.T) {
	base := rest.NewRouter("/api")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r := base.Route(newRouterTestRoute("GET", "/x"))
			_ = r.Routes()
		}(i)
	}
	wg.Wait()
	if len(base.Routes()) != 0 {
		t.Errorf("base Router must remain untouched by concurrent derivations")
	}
}

func TestWithHandleCallback_FiresOnRegister(t *testing.T) {
	var got *rest.RouteHandle[routerTestReq, routerTestResp]
	route := rest.NewRoute[routerTestReq, routerTestResp]("GET", "/x", routerTestReqCodec, routerTestRespCodec,
		rest.WithHandleCallback(func(h *rest.RouteHandle[routerTestReq, routerTestResp]) { got = h }),
	).WithHandler(routerTestHandler)

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	rt := rest.NewRouter("/api").Route(route)
	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got == nil {
		t.Fatal("want WithHandleCallback to fire, got nil handle")
	}
}

func TestRoute_WithOpt_AttachesHandleCallbackToAlreadyDeclaredRoute(t *testing.T) {
	// Mirrors the real examples/rest-api/chiserver scenario: a route
	// declared ONCE in a shared, transport-agnostic var (no
	// WithHandleCallback at declaration time) later needs a
	// server-local handle-recovery hook attached — without
	// re-declaring its method/path/codecs/RouteMeta from scratch.
	declared := newRouterTestRoute("GET", "/x").WithHandler(routerTestHandler)

	var got *rest.RouteHandle[routerTestReq, routerTestResp]
	decorated := declared.WithOpt(rest.WithHandleCallback(
		func(h *rest.RouteHandle[routerTestReq, routerTestResp]) { got = h },
	))

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	if err := decorated.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got == nil {
		t.Fatal("want WithHandleCallback (attached via WithOpt) to fire")
	}

	// The ORIGINAL, undecorated value must remain untouched — Route is
	// immutable, same guarantee every other decoration method provides.
	server2 := rest.NewServer(rest.Info{Title: "t2", Version: "1"})
	got = nil
	if err := declared.Register(server2); err != nil {
		t.Fatalf("Register (original): %v", err)
	}
	if got != nil {
		t.Error("original, undecorated Route must NOT have the callback attached")
	}
}

func TestClientHandle_WithRouter_MatchesRegisteredPath(t *testing.T) {
	route := newRouterTestRoute("GET", "/users")
	rt := rest.NewRouter("/api/v1")

	handle := route.ClientHandle(rest.WithRouter(rt))
	if handle == nil {
		t.Fatal("want non-nil handle")
	}
	const want = "/api/v1/users"
	if handle.Descriptor.Path != want {
		t.Errorf("want path %q, got %q", want, handle.Descriptor.Path)
	}

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	var registeredHandle *rest.RouteHandle[routerTestReq, routerTestResp]
	routeWithCallback := rest.NewRoute[routerTestReq, routerTestResp]("GET", "/users", routerTestReqCodec, routerTestRespCodec,
		rest.WithHandleCallback(func(h *rest.RouteHandle[routerTestReq, routerTestResp]) { registeredHandle = h }),
	).WithHandler(routerTestHandler)
	if err := rt.Route(routeWithCallback).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if registeredHandle == nil {
		t.Fatal("want WithHandleCallback to fire")
	}
	if registeredHandle.Descriptor.Path != handle.Descriptor.Path {
		t.Errorf("want ClientHandle(WithRouter(rt)) to match the registered path exactly: %q vs %q",
			handle.Descriptor.Path, registeredHandle.Descriptor.Path)
	}
}

func TestClientHandle_ZeroArgs_StillCompilesAndWorks(t *testing.T) {
	route := newRouterTestRoute("GET", "/users")
	handle := route.ClientHandle()
	if handle == nil {
		t.Fatal("want non-nil handle")
	}
}

func TestRouter_Tags_AccumulateNotOverwrite(t *testing.T) {
	rt := rest.NewRouter("/api").Tags("a").Tags("b").Route(newRouterTestRoute("GET", "/x"))
	entries := rt.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("want both tags accumulated, got %v", tags)
	}
}

func TestRouter_Tags_SurviveLeafsOwnRouteMeta(t *testing.T) {
	route := rest.NewRoute[routerTestReq, routerTestResp]("GET", "/x", routerTestReqCodec, routerTestRespCodec,
		rest.RouteMeta{OperationID: "x", Tags: []string{"own-tag"}},
	).WithHandler(routerTestHandler)
	rt := rest.NewRouter("/api").Tags("router-tag").Route(route)

	entries := rt.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "router-tag" || tags[1] != "own-tag" {
		t.Errorf("want [router-tag own-tag] (Router's own first, then the leaf's), got %v", tags)
	}

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestRouter_Tags_PreviewMatchesRegisteredSpec_WithMultipleRouteMetaOpts(t *testing.T) {
	// A route declaring 2+ SEPARATE RouteMeta opts is unusual but legal
	// (variadic RouteOpt list) — RouteMeta.applyRoute is a WHOLE-STRUCT
	// OVERWRITE, so only the LAST one's Tags survive into the real
	// registered spec. Routes()'s preview must match that exactly, not
	// incorrectly merge every declared RouteMeta's Tags together.
	route := rest.NewRoute[routerTestReq, routerTestResp]("GET", "/x", routerTestReqCodec, routerTestRespCodec,
		rest.RouteMeta{OperationID: "x", Tags: []string{"a"}},
		rest.RouteMeta{OperationID: "x", Tags: []string{"b"}},
	).WithHandler(routerTestHandler)
	rt := rest.NewRouter("/api").Tags("ancestor").Route(route)

	entries := rt.Routes()
	previewTags := entries[0].Tags
	wantTags := []string{"ancestor", "b"}
	if len(previewTags) != len(wantTags) {
		t.Fatalf("want preview tags %v, got %v", wantTags, previewTags)
	}
	for i, want := range wantTags {
		if previewTags[i] != want {
			t.Errorf("want preview tags %v, got %v", wantTags, previewTags)
			break
		}
	}

	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	spec, err := server.OpenAPISpec()
	if err != nil {
		t.Fatalf("OpenAPISpec: %v", err)
	}
	y, err := spec.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	got := string(y)
	if !strings.Contains(got, "- ancestor") || !strings.Contains(got, "- b") {
		t.Errorf("want registered spec to contain tags [ancestor b], got:\n%s", got)
	}
	if strings.Contains(got, "- a\n") {
		t.Errorf("want registered spec to NOT contain the overwritten RouteMeta's tag \"a\" (preview must match reality), got:\n%s", got)
	}
}

func TestRouter_Tags_AccumulateAcrossMount(t *testing.T) {
	inner := rest.NewRouter("/users").Tags("users").Route(newRouterTestRoute("GET", "/{id}"))
	outer := rest.NewRouter("/api").Tags("api").Mount(inner)

	entries := outer.Routes()
	tags := entries[0].Tags
	if len(tags) != 2 || tags[0] != "api" || tags[1] != "users" {
		t.Errorf("want [api users] (outermost ancestor first), got %v", tags)
	}
}

func TestRouter_Tags_RoutesEquivalentToWalk(t *testing.T) {
	rt := rest.NewRouter("/api").Tags("t1").Route(newRouterTestRoute("GET", "/a"))
	var walked []rest.RouterEntry
	_ = rt.Walk(func(e rest.RouterEntry) error {
		walked = append(walked, e)
		return nil
	})
	routes := rt.Routes()
	if len(walked[0].Tags) != len(routes[0].Tags) || walked[0].Tags[0] != routes[0].Tags[0] {
		t.Errorf("Walk and Routes disagree on Tags: %v vs %v", walked[0].Tags, routes[0].Tags)
	}
}

// ── SSERoute composition ─────────────────────────────────────────────────
//
// [SSERoute] is [Router]'s SECOND [routable] implementer (alongside
// [Route]) — these tests exercise the SAME Mount/Group/Tags/middleware
// composition the Route-based tests above already cover extensively, to
// confirm SSERoute's own withRouterPrefix/routeMethod/middlewareNames/
// tags/registerAny implementation behaves identically, not just Route's.

func newRouterTestSSERoute(path string) rest.SSERoute[routerTestReq, routerTestResp] {
	return rest.NewSSERoute[routerTestReq, routerTestResp](path, routerTestReqCodec, routerTestRespCodec).
		WithHandler(func(_ context.Context, _ routerTestReq, send func(routerTestResp) error) error {
			return send(routerTestResp{Greeting: "hello"})
		})
}

func TestRouter_Mount_ComposesSSERoute(t *testing.T) {
	sseRoute := newRouterTestSSERoute("/stream")
	rt := rest.NewRouter("/api/v1").
		Use(middleware.Middleware{Name: "sse-mw"}).
		Tags("sse-tag").
		Route(sseRoute)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Path != "/api/v1/stream" {
		t.Errorf("want composed path %q, got %q", "/api/v1/stream", entry.Path)
	}
	// SSE routes are always GET — routeMethod() must report this
	// regardless of Router involvement.
	if entry.Method != "GET" {
		t.Errorf("want method GET, got %q", entry.Method)
	}
	if len(entry.MiddlewareNames) != 1 || entry.MiddlewareNames[0] != "sse-mw" {
		t.Errorf("want [sse-mw], got %v", entry.MiddlewareNames)
	}
	if len(entry.Tags) != 1 || entry.Tags[0] != "sse-tag" {
		t.Errorf("want [sse-tag], got %v", entry.Tags)
	}

	// registerAny must actually delegate to SSERoute.Register — a Router-
	// grouped SSE route must register onto a Server exactly like a
	// directly-registered one would.
	server := rest.NewServer(rest.Info{Title: "t", Version: "1"})
	if err := rt.Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	sseEntries := server.SSEEntries()
	if len(sseEntries) != 1 {
		t.Fatalf("want 1 registered SSE route, got %d", len(sseEntries))
	}
	if sseEntries[0].Path() != "/api/v1/stream" {
		t.Errorf("want registered SSE path %q, got %q", "/api/v1/stream", sseEntries[0].Path())
	}
}

func TestRouter_Group_ComposesSSERouteAlongsideRoute(t *testing.T) {
	// A Group mixing BOTH leaf kinds under one scoped middleware set —
	// confirms the routable interface dispatches correctly per-leaf, not
	// just for a Router holding only one kind.
	rt := rest.NewRouter("/api").Group(func(sub rest.Router) rest.Router {
		return sub.
			Use(middleware.Middleware{Name: "shared-mw"}).
			Route(newRouterTestRoute("GET", "/users")).
			Route(newRouterTestSSERoute("/stream"))
	})

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if len(e.MiddlewareNames) != 1 || e.MiddlewareNames[0] != "shared-mw" {
			t.Errorf("entry %q: want [shared-mw], got %v", e.Path, e.MiddlewareNames)
		}
	}
	if entries[0].Path != "/api/users" || entries[1].Path != "/api/stream" {
		t.Errorf("want [/api/users /api/stream] in declaration order, got [%q %q]", entries[0].Path, entries[1].Path)
	}
}
