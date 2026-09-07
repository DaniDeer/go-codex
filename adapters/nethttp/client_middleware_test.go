package nethttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ── ClientTransform: happy path, header/query composition, Out decode ───

func TestCallWithHandle_ClientTransform_HappyPath_EncodesInAndDecodesOut(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Version", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.ClientTransform(route, mw, func(ctx context.Context, req createReq) (tdIn, error) {
		return tdIn{Key: "secret-" + req.Name}, nil
	})
	handle := route.ClientHandle()

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Policy-Version", "v1")
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	ctx := WithClientMiddlewareOut(context.Background())
	resp, err := CallWithHandle(ctx, srv.Client(), srv.URL, handle, createReq{Name: "Alice"}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Alice" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if gotHeader != "secret-Alice" {
		t.Errorf("want X-Api-Key %q sent, got %q", "secret-Alice", gotHeader)
	}

	outs := ClientMiddlewareOutFromContext(ctx)
	out, ok := outs["api-key-policy"].(tdOut)
	if !ok {
		t.Fatalf("want decoded tdOut for api-key-policy, got %+v", outs)
	}
	if out.Value != "v1" {
		t.Errorf("want decoded Out.Value %q, got %q", "v1", out.Value)
	}
}

// ── ClientTransform: fn error aborts before any network activity ────────

func TestCallWithHandle_ClientTransform_FnError_AbortsBeforeNetwork(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.ClientTransform(route, mw, func(ctx context.Context, req createReq) (tdIn, error) {
		return tdIn{}, errBadAPIKey
	})
	handle := route.ClientHandle()

	serverCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
	}))
	defer srv.Close()

	obs := &testObserver{}
	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL, handle, createReq{Name: "Alice"}, CallOptions{Observer: obs})
	if err == nil {
		t.Fatal("want error from ClientTransform fn")
	}
	var mwErr rest.MiddlewareError
	if got := err; got == nil {
		t.Fatal("want non-nil error")
	} else if !asMiddlewareError(got, &mwErr) {
		t.Fatalf("want rest.MiddlewareError, got %T: %v", got, got)
	}
	if serverCalled {
		t.Error("want server NOT called when ClientTransform fn errors")
	}
	if !obs.called || obs.status != 0 {
		t.Errorf("want observer.RecordRequest called with status 0, got called=%v status=%d", obs.called, obs.status)
	}
}

func asMiddlewareError(err error, target *rest.MiddlewareError) bool {
	if me, ok := err.(rest.MiddlewareError); ok {
		*target = me
		return true
	}
	return false
}

// ── Route-agnostic .Use(mw) client dispatch (WithSend) ───────────────────

func TestCallWithHandle_AgnosticMiddleware_DispatchesSendFn(t *testing.T) {
	callCount := 0
	mw := rest.NewMiddleware(newTDDeclaration("agnostic-send-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithSend(func(ctx context.Context) (tdIn, error) {
			callCount++
			return tdIn{Key: "sent-value"}, nil
		})

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).Use(mw)
	handle := route.ClientHandle()

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL, handle, createReq{Name: "Alice"}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "sent-value" {
		t.Errorf("want X-Api-Key %q sent, got %q", "sent-value", gotHeader)
	}
	if callCount != 1 {
		t.Errorf("want bundled sendFn called exactly once, got %d", callCount)
	}
}

// ── ClientTransformSSE: encodes In into the connect request, decodes Out
// ONCE at connection-open time ──

func TestConsumeSSE_ClientTransformSSE_EncodesInAndDecodesOutOnce(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Version", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))

	route := rest.NewSSERoute[sseTestReq, userResp]("/stream/{id}", sseTestReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String(),
			func(r sseTestReq) string { return r.ID },
			func(r *sseTestReq, v string) { r.ID = v }),
	)
	route = rest.ClientTransformSSE(route, mw, func(ctx context.Context, req sseTestReq) (tdIn, error) {
		return tdIn{Key: "secret-" + req.ID}, nil
	})
	handle := route.ClientHandle()

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Policy-Version", "v1")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"name\":\"Alice\"}\n\n")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = WithClientMiddlewareOut(ctx)

	done := make(chan struct{})
	_ = consumeSSE(ctx, srv.Client(), srv.URL, handle, sseTestReq{ID: "machine-1"},
		func(_ context.Context, e userResp) error {
			close(done)
			cancel()
			return nil
		}, ConsumeOptions{})

	select {
	case <-done:
	default:
		t.Fatal("want fn to be called with the decoded event")
	}
	if gotHeader != "secret-machine-1" {
		t.Errorf("want X-Api-Key %q sent, got %q", "secret-machine-1", gotHeader)
	}

	outs := ClientMiddlewareOutFromContext(ctx)
	out, ok := outs["api-key-policy"].(tdOut)
	if !ok {
		t.Fatalf("want decoded tdOut for api-key-policy, got %+v", outs)
	}
	if out.Value != "v1" {
		t.Errorf("want decoded Out.Value %q, got %q", "v1", out.Value)
	}
}

// ── D3: three-tier client precedence (explicit CallOptions > middleware- ──
// ── derived (ClientTransform) > route-own-derived), all exercised together ──

// TestCallWithHandle_ClientTransform_D3Precedence_AllThreeTiers exercises
// docs/design/d-0003-codec-declared-middlewares.md's D3 resolution:
// explicit CallOptions wins over a ClientTransform middleware's derived
// value, which in turn wins over the route's own req-derived value — all
// three tiers for the SAME query param name, in one test.
func TestCallWithHandle_ClientTransform_D3Precedence_AllThreeTiers(t *testing.T) {
	// Route's own query merge field derives "filter" from req.Filter — the
	// LOWEST tier.
	route := rest.NewRoute[getUserActivityReq, userResp]("GET", "/users/{id}/activity",
		codex.Struct[getUserActivityReq](), userRespCodec,
		rest.RouteMeta{OperationID: "getUserActivity"},
		rest.NewPathParam("id", codex.String(),
			func(r getUserActivityReq) string { return r.ID },
			func(r *getUserActivityReq, v string) { r.ID = v }),
		rest.NewOptionalQueryParam("filter", codex.String(),
			func(r getUserActivityReq) string { return r.Filter },
			func(r *getUserActivityReq, v string) { r.Filter = v }),
	)

	// Middleware's ClientTransform derives "filter" from its OWN In — the
	// MIDDLE tier.
	mw := rest.NewMiddleware(newTDDeclaration("filter-policy")).
		WithRequestQuery(rest.NewOptionalQueryParam("filter", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	route = rest.ClientTransform(route, mw, func(ctx context.Context, req getUserActivityReq) (tdIn, error) {
		return tdIn{Key: "middleware-derived"}, nil
	})
	handle := route.ClientHandle()

	var gotFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFilter = r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	// Tier 1 (highest): explicit CallOptions wins over BOTH lower tiers.
	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL, handle,
		getUserActivityReq{ID: "u1", Filter: "route-derived"},
		CallOptions{QueryParams: map[string]string{"filter": "explicit-wins"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotFilter != "explicit-wins" {
		t.Errorf("want explicit CallOptions to win over middleware/route-derived, got %q", gotFilter)
	}

	// Tier 2: middleware-derived wins over route-own-derived when no
	// explicit override is set.
	_, err = CallWithHandle(context.Background(), srv.Client(), srv.URL, handle,
		getUserActivityReq{ID: "u1", Filter: "route-derived"}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotFilter != "middleware-derived" {
		t.Errorf("want middleware-derived to win over route-own-derived, got %q", gotFilter)
	}
}
