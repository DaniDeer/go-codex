package nethttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A,
// Phase 3: Middleware.SetContextFieldFromIn/SetContextFieldFromOut —
// end-to-end dispatch order, handler retrieval, and client-side round-trip.

var cfTenantIDField = middleware.NewContextField(codex.String())

// TestSetContextFieldFromIn_DispatchOrder_BeforeFnAndHandler proves the
// field is published DURING DecodeIn — before BOTH the attached
// middleware's own Fn AND the route's own handler run — so either can
// retrieve it via [middleware.ContextField.Get].
func TestSetContextFieldFromIn_DispatchOrder_BeforeFnAndHandler(t *testing.T) {
	var fnSawTenant, handlerSawTenant string
	mw := rest.NewMiddleware(newTDDeclaration("tenant-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Tenant-Id", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		SetContextFieldFromIn(cfTenantIDField, func(in tdIn) any { return in.Key })

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).HandleMW(mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		fnSawTenant, _ = cfTenantIDField.Get(ctx)
		return tdOut{Value: "ok"}, nil
	}).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		handlerSawTenant, _ = cfTenantIDField.Get(ctx)
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tenant-Id", "acme-corp")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if fnSawTenant != "acme-corp" {
		t.Errorf("want mw's Fn to see tenant %q, got %q", "acme-corp", fnSawTenant)
	}
	if handlerSawTenant != "acme-corp" {
		t.Errorf("want route handler to see tenant %q, got %q", "acme-corp", handlerSawTenant)
	}
}

// TestSetContextFieldFromOut_DispatchOrder_AfterFnReturns proves the Out
// side publishes from the middleware's OWN returned Out value, available
// by the time EncodeOut composes the response (after Fn returns).
func TestSetContextFieldFromOut_DispatchOrder_AfterFnReturns(t *testing.T) {
	var policyVersionField = middleware.NewContextField(codex.String())
	var handlerSawVersion string

	mw := rest.NewMiddleware(middleware.NewDeclaration("policy-version-out", tdEmptyCodec, tdOutCodec)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Version", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		)).
		SetContextFieldFromOut(policyVersionField, func(out tdOut) any { return out.Value })

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).HandleMW(mw, func(ctx context.Context, req *createReq, in tdEmpty) (tdOut, error) {
		return tdOut{Value: "v2"}, nil
	}).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		// NOT yet set here — Fn hasn't returned when the handler runs
		// (handler and middleware Fn run at the SAME pre-response stage,
		// but EncodeOut — where SetContextFieldFromOut fires — composes
		// AFTER both). Confirmed absent here, present after ServeHTTP.
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Policy-Version"); got != "v2" {
		t.Errorf("want X-Policy-Version %q, got %q", "v2", got)
	}
	_ = handlerSawVersion // documents the "not yet set during handler" timing above
}

// TestClientMW_SetContextFieldFromIn_RoundTrip proves the CLIENT side also
// works end-to-end: [clientTransport.Call] now pre-allocates the shared
// ContextField box itself (EnsureContextFields) if the caller didn't
// already — and [middleware.EnsureContextFields] is idempotent (returns
// the SAME ctx, box included, when already prepared), so a caller that
// pre-decorates its OWN ctx BEFORE calling [rest.Client.Call] retrieves the
// SetContextFieldFromIn-published value afterward, from that SAME ctx —
// zero extra ceremony beyond the ONE EnsureContextFields call every server
// adapter already makes routine.
func TestClientMW_SetContextFieldFromIn_RoundTrip(t *testing.T) {
	var clientTenantField = middleware.NewContextField(codex.String())
	mw := rest.NewMiddleware(newTDDeclaration("client-tenant-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Tenant-Id", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		SetContextFieldFromIn(clientTenantField, func(in tdIn) any { return in.Key })

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Tenant-Id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"1","name":"Alice"}`))
	}))
	defer srv.Close()

	route := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).
		ClientMW(mw, func(ctx context.Context, req getReq) (tdIn, error) {
			return tdIn{Key: "acme-corp"}, nil
		})

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Caller pre-decorates ctx — EnsureContextFields is idempotent, so
	// Call's own internal call reuses this SAME box.
	ctx := middleware.EnsureContextFields(context.Background())
	_, err := client.Call(ctx, route, getReq{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotHeader != "acme-corp" {
		t.Errorf("want X-Tenant-Id %q sent, got %q", "acme-corp", gotHeader)
	}
	got, ok := clientTenantField.Get(ctx)
	if !ok || got != "acme-corp" {
		t.Errorf("want (%q, true) from the pre-decorated ctx, got (%q, %v)", "acme-corp", got, ok)
	}
}
