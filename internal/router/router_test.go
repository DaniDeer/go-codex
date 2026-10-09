package router_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/router"
)

// join mirrors a minimal, no-leading-slash join strategy (events/reqreply
// style) — sufficient to exercise prefix composition without pulling in
// any api package.
func join(prefix, leaf string) string {
	switch {
	case prefix == "":
		return leaf
	case leaf == "":
		return prefix
	default:
		return prefix + "/" + leaf
	}
}

// invalidPathMarker is the test's stand-in for a pattern-specific
// "this leaf's final path failed validation" error.
type invalidPathMarker struct{ msg string }

func (e invalidPathMarker) Error() string { return e.msg }

// wrappedPrefixError is the test's stand-in for a per-package
// RouterPrefixError.
type wrappedPrefixError struct {
	Prefix, Composed string
	Err              error
}

func (e wrappedPrefixError) Error() string { return "wrapped: " + e.Prefix + ": " + e.Composed }
func (e wrappedPrefixError) Unwrap() error { return e.Err }

func wrapPrefixErr(prefix, composed string, err error) (error, bool) {
	var marker invalidPathMarker
	if errors.As(err, &marker) {
		return wrappedPrefixError{Prefix: prefix, Composed: composed, Err: err}, true
	}
	return nil, false
}

// fakeLeaf is a minimal [router.Routable][string] implementation —
// Target=string is the simplest possible register-target type for
// package-level tests independent of any specific api package.
type fakeLeaf struct {
	path        string // becomes the COMPOSED path once WithRouterPrefix runs
	mwNames     []string
	tags        []string
	registerErr error
	log         *[]string // RegisterAny appends its own composed path here, for order assertions
}

func (f fakeLeaf) WithRouterPrefix(prefix string, _ []middleware.RouteMiddleware, _ []string) (router.Routable[string], string) {
	composed := join(prefix, f.path)
	nf := f
	nf.path = composed
	return nf, composed
}

func (f fakeLeaf) MiddlewareNames() []string { return f.mwNames }
func (f fakeLeaf) RouteTags() []string       { return f.tags }

func (f fakeLeaf) RegisterAny(string) error {
	if f.registerErr != nil {
		return f.registerErr
	}
	if f.log != nil {
		*f.log = append(*f.log, f.path)
	}
	return nil
}

// methodLeaf additionally implements [router.MethodReporter] — a
// SEPARATE concrete type from fakeLeaf (not a flag field), exactly
// mirroring how reqreply's leaf genuinely does NOT implement
// MethodReporter at all (a flag field would understate the real-world
// shape being tested).
type methodLeaf struct {
	fakeLeaf
	method string
}

func (m methodLeaf) RouteMethod() string { return m.method }

func (m methodLeaf) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (router.Routable[string], string) {
	inner, composed := m.fakeLeaf.WithRouterPrefix(prefix, mws, tags)
	return methodLeaf{fakeLeaf: inner.(fakeLeaf), method: m.method}, composed
}

func newRouter(prefix string) router.Router[string] {
	return router.NewRouter[string](prefix, join, wrapPrefixErr)
}

func TestRouter_Route_Register_CallsLeafInDeclarationOrder(t *testing.T) {
	var log []string
	rt := newRouter("api").
		Route(fakeLeaf{path: "a", log: &log}).
		Route(fakeLeaf{path: "b", log: &log})

	if err := rt.Register("target"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := log; len(got) != 2 || got[0] != "api/a" || got[1] != "api/b" {
		t.Fatalf("log = %v, want [api/a api/b]", got)
	}
}

func TestRouter_Use_PrependsMiddlewareBeforeLeafOwn(t *testing.T) {
	rt := newRouter("api").
		Use(middleware.Middleware{Name: "router-mw"}).
		Route(fakeLeaf{path: "a", mwNames: []string{"leaf-mw"}})

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	want := []string{"router-mw", "leaf-mw"}
	got := entries[0].MiddlewareNames
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("MiddlewareNames = %v, want %v", got, want)
	}
}

func TestRouter_With_OneShot_OnlyAppliesToNextRoute(t *testing.T) {
	rt := newRouter("api").
		With(middleware.Middleware{Name: "one-shot"}).
		Route(fakeLeaf{path: "a"}).
		Route(fakeLeaf{path: "b"})

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if len(entries[0].MiddlewareNames) != 1 || entries[0].MiddlewareNames[0] != "one-shot" {
		t.Errorf("entries[0].MiddlewareNames = %v, want [one-shot]", entries[0].MiddlewareNames)
	}
	if len(entries[1].MiddlewareNames) != 0 {
		t.Errorf("entries[1].MiddlewareNames = %v, want empty (one-shot must not leak)", entries[1].MiddlewareNames)
	}
}

func TestRouter_With_DiscardedByMount(t *testing.T) {
	child := newRouter("child").Route(fakeLeaf{path: "a"})
	rt := newRouter("api").
		With(middleware.Middleware{Name: "one-shot"}).
		Mount(child)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if len(entries[0].MiddlewareNames) != 0 {
		t.Errorf("MiddlewareNames = %v, want empty — With() must be discarded by Mount, not leaked", entries[0].MiddlewareNames)
	}
}

func TestRouter_Mount_ComposesPrefixAndMiddleware(t *testing.T) {
	child := newRouter("v1").
		Use(middleware.Middleware{Name: "child-mw"}).
		Route(fakeLeaf{path: "users"})
	rt := newRouter("api").
		Use(middleware.Middleware{Name: "parent-mw"}).
		Mount(child)

	entries := rt.Routes()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].Path != "api/v1/users" {
		t.Errorf("Path = %q, want %q", entries[0].Path, "api/v1/users")
	}
	want := []string{"parent-mw", "child-mw"}
	got := entries[0].MiddlewareNames
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("MiddlewareNames = %v, want %v (parent-first, outer-to-inner)", got, want)
	}
}

func TestRouter_Group_SharesPrefixNotMiddleware(t *testing.T) {
	rt := newRouter("api").
		Route(fakeLeaf{path: "public"}).
		Group(func(sub router.Router[string]) router.Router[string] {
			return sub.Use(middleware.Middleware{Name: "scoped-mw"}).Route(fakeLeaf{path: "private"})
		})

	entries := rt.Routes()
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Path != "api/public" || len(entries[0].MiddlewareNames) != 0 {
		t.Errorf("entries[0] = %+v, want Path=api/public, no middleware", entries[0])
	}
	if entries[1].Path != "api/private" {
		t.Errorf("entries[1].Path = %q, want %q (Group shares rt's prefix, no new segment)", entries[1].Path, "api/private")
	}
	if len(entries[1].MiddlewareNames) != 1 || entries[1].MiddlewareNames[0] != "scoped-mw" {
		t.Errorf("entries[1].MiddlewareNames = %v, want [scoped-mw] (scoped to the Group only)", entries[1].MiddlewareNames)
	}
}

func TestRouter_Tags_AppendedAfterLeafOwnNotPrepended(t *testing.T) {
	rt := newRouter("api").
		Tags("router-tag").
		Route(fakeLeaf{path: "a", tags: []string{"leaf-tag"}})

	entries := rt.Routes()
	want := []string{"router-tag", "leaf-tag"}
	got := entries[0].Tags
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
}

func TestRouter_Walk_StopsOnFirstError(t *testing.T) {
	rt := newRouter("api").
		Route(fakeLeaf{path: "a"}).
		Route(fakeLeaf{path: "b"})

	var seen []string
	sentinel := errors.New("stop")
	err := rt.Walk(func(e router.RouterEntry) error {
		seen = append(seen, e.Path)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if len(seen) != 1 {
		t.Fatalf("seen = %v, want exactly 1 leaf visited before stopping", seen)
	}
}

func TestRouter_MethodReporter_FallsBackToEmptyWhenNotImplemented(t *testing.T) {
	rt := newRouter("api").Route(fakeLeaf{path: "a"})
	entries := rt.Routes()
	if entries[0].Method != "" {
		t.Errorf("Method = %q, want empty (fakeLeaf does not implement MethodReporter)", entries[0].Method)
	}
}

func TestRouter_MethodReporter_ReportsWhenImplemented(t *testing.T) {
	rt := newRouter("api").Route(methodLeaf{fakeLeaf: fakeLeaf{path: "a"}, method: "GET"})
	entries := rt.Routes()
	if entries[0].Method != "GET" {
		t.Errorf("Method = %q, want %q", entries[0].Method, "GET")
	}
}

func TestRouter_Register_PropagatesUnwrappedErrorWhenPredicateDoesNotMatch(t *testing.T) {
	sentinel := errors.New("some other failure")
	rt := newRouter("api").Route(fakeLeaf{path: "a", registerErr: sentinel})

	err := rt.Register("target")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel propagated unwrapped", err)
	}
	var wrapped wrappedPrefixError
	if errors.As(err, &wrapped) {
		t.Fatalf("err wrongly wrapped as wrappedPrefixError: %v", err)
	}
}

func TestRouter_Register_WrapsPrefixErrorWhenPredicateMatches(t *testing.T) {
	rt := newRouter("api").Route(fakeLeaf{path: "a", registerErr: invalidPathMarker{msg: "bad path"}})

	err := rt.Register("target")
	var wrapped wrappedPrefixError
	if !errors.As(err, &wrapped) {
		t.Fatalf("err = %v, want wrappedPrefixError", err)
	}
	if wrapped.Prefix != "api" || wrapped.Composed != "api/a" {
		t.Errorf("wrapped = %+v, want Prefix=api Composed=api/a", wrapped)
	}
}

func TestRouter_Routes_MatchesWalkOrder(t *testing.T) {
	rt := newRouter("api").
		Route(fakeLeaf{path: "a"}).
		Route(fakeLeaf{path: "b"}).
		Route(fakeLeaf{path: "c"})

	entries := rt.Routes()
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	for i, want := range []string{"api/a", "api/b", "api/c"} {
		if entries[i].Path != want {
			t.Errorf("entries[%d].Path = %q, want %q", i, entries[i].Path, want)
		}
	}
}
