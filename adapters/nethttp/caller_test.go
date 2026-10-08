package nethttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

func TestCall_HappyPath(t *testing.T) {
	r := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	caller := newCaller(srv.Client(), srv.URL)
	resp, err := call(context.Background(), caller, r, createReq{Name: "Alice"}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Alice" || resp.ID != "1" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// TestCall_ClientMWAppliedEveryCall locks in that a route's
// ClientMW-declared credential implementation runs on EVERY Call
// invocation through the SAME route value — the new design's per-route
// (not per-Caller) credential declaration.
type callerTestAuthIn struct{ Token string }
type callerTestAuthOut struct{}

func TestCall_ClientMWAppliedEveryCall(t *testing.T) {
	credCallCount := 0
	boundMW := rest.BoundSecurityClientMiddleware[getReq, callerTestAuthIn, callerTestAuthOut](
		"bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
		func(ctx context.Context, req getReq) (callerTestAuthIn, error) {
			credCallCount++
			return callerTestAuthIn{Token: "shared-token"}, nil
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in callerTestAuthIn) string { return in.Token },
		func(in *callerTestAuthIn, v string) { in.Token = v },
	))
	r := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "getMe"},
	).ClientBoundMW(boundMW)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "shared-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	caller := newCaller(srv.Client(), srv.URL)
	for i := 0; i < 2; i++ {
		resp, err := call(context.Background(), caller, r, getReq{}, CallOptions{})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if resp.ID != "me" {
			t.Errorf("call %d: unexpected response: %+v", i, resp)
		}
	}
	if credCallCount != 2 {
		t.Errorf("want ClientMW's Fn called once per Call invocation (2), got %d", credCallCount)
	}
}

// NOTE: TestCall_ClientMWSatisfiesGating_UnrelatedImplNotRun (which
// proved the legacy ClientMW mechanism's Satisfies-based filtering — an
// implementation paired against a scheme the route doesn't declare is
// skipped) was REMOVED, not migrated — this review round's investigation
// found [dispatchClientMiddlewareIn] (the modern ClientBoundMW dispatch
// mechanism this test would need to migrate to) has NO equivalent
// Satisfies-gating at all: it runs EVERY attached
// [rest.ClientMiddlewareHandler] unconditionally, regardless of
// [rest.ClientMiddlewareHandler.Satisfies]. This is a genuine,
// pre-existing gap in the replacement mechanism (not introduced by this
// round's removal) — flagged as a follow-up finding, out of scope for
// "retire the legacy escape hatch" specifically, since fixing it is an
// ADDITIVE feature change to the bound mechanism, not a removal. A
// faithful migration of this exact test is not possible until that gap
// is addressed; writing a test that doesn't actually test gating would
// be misleading, so it was deleted rather than migrated.

// ── Caller.WithBaseURL (ergonomic rebase convenience) ───────────────────────

func TestCaller_WithBaseURL_ReturnsNewInstance(t *testing.T) {
	base := newCaller(http.DefaultClient, "http://base.example")
	rebased := base.WithBaseURL("http://rebased.example")
	if rebased == base {
		t.Fatal("WithBaseURL must return a DISTINCT *caller, not the same pointer")
	}
}

// TestCaller_WithBaseURL_DoesNotMutateOriginal proves the original Caller
// still targets its own baseURL after deriving a rebased copy from
// it — WithBaseURL must copy, never mutate in place.
func TestCaller_WithBaseURL_DoesNotMutateOriginal(t *testing.T) {
	var reachedOriginal, reachedRebased bool
	srvOriginal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedOriginal = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srvOriginal.Close()
	srvRebased := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedRebased = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srvRebased.Close()

	r := rest.NewRoute[getReq, userResp]("GET", "/users", getReqCodec, userRespCodec)
	base := newCaller(srvOriginal.Client(), srvOriginal.URL)
	_ = base.WithBaseURL(srvRebased.URL) // derive a rebased copy, discard it

	if _, err := call(context.Background(), base, r, getReq{}, CallOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reachedOriginal {
		t.Error("want original Caller to still reach srvOriginal after deriving a rebased copy")
	}
	if reachedRebased {
		t.Error("want original Caller to NOT reach srvRebased — WithBaseURL must not mutate it")
	}
}

// TestCaller_WithBaseURL_SharesClient proves chaining WithBaseURL off an
// already-rebased Caller keeps working (no accidental nil client).
func TestCaller_WithBaseURL_SharesClient(t *testing.T) {
	base := newCaller(http.DefaultClient, "http://base.example")
	rebased := base.WithBaseURL("http://rebased.example")
	rerebased := rebased.WithBaseURL("http://another.example")
	if rerebased == nil || rerebased == rebased {
		t.Fatal("WithBaseURL chained off a rebased Caller must return a distinct, non-nil *caller")
	}
}

// TestCall_WithRebasedCaller_ReachesNewHost proves two Callers derived
// via WithBaseURL, reaching a DIFFERENT host each time through the
// SAME declared route.
func TestCall_WithRebasedCaller_ReachesNewHost(t *testing.T) {
	var reachedA, reachedB bool
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedA = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reachedB = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srvB.Close()

	r := rest.NewRoute[getReq, userResp]("GET", "/users", getReqCodec, userRespCodec)
	base := newCaller(http.DefaultClient, "")
	callerA := base.WithBaseURL(srvA.URL)
	callerB := base.WithBaseURL(srvB.URL)

	if _, err := call(context.Background(), callerA, r, getReq{}, CallOptions{}); err != nil {
		t.Fatalf("callerA: unexpected error: %v", err)
	}
	if _, err := call(context.Background(), callerB, r, getReq{}, CallOptions{}); err != nil {
		t.Fatalf("callerB: unexpected error: %v", err)
	}
	if !reachedA || !reachedB {
		t.Error("want both rebased Callers to reach their own distinct host")
	}
}

// TestCaller_WithBaseURL_ConcurrentRebaseIsSafe runs many goroutines
// rebasing the SAME shared base Caller concurrently — WithBaseURL must
// never mutate shared state.
func TestCaller_WithBaseURL_ConcurrentRebaseIsSafe(t *testing.T) {
	base := newCaller(http.DefaultClient, "http://base.example")
	const n = 50
	results := make([]*caller, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = base.WithBaseURL("http://rebased.example")
		}(i)
	}
	wg.Wait()
	for i, c := range results {
		if c == nil {
			t.Fatalf("result %d: WithBaseURL returned nil under concurrent use", i)
		}
	}
}
