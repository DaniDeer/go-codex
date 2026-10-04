package chi

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
// chi mirror of adapters/nethttp/contextfield_dispatch_test.go's 2
// SERVER-side tests (Gap-2 review fix — this repo's established
// nethttp/chi 1:1 test-pairing convention had no chi mirror for this
// mechanism). The 3rd nethttp test (client-side round-trip via
// rest.Client.Call) is NOT mirrored here: client dispatch lives entirely
// in api/rest + adapters/nethttp's ClientTransport, independent of which
// server adapter (nethttp vs chi) is used — chi itself is server-only
// (a gochi.Router wrapper), so there is no chi-specific client behavior
// to test.

var chiCfTenantIDField = middleware.NewContextField(codex.String())

// TestChiSetContextFieldFromIn_DispatchOrder_BeforeFnAndHandler mirrors
// nethttp's identical test — proves the field is published DURING
// DecodeIn, before BOTH the attached middleware's own Fn AND the route's
// own handler run.
func TestChiSetContextFieldFromIn_DispatchOrder_BeforeFnAndHandler(t *testing.T) {
	var fnSawTenant, handlerSawTenant string
	mw := rest.NewMiddleware(newTDDeclaration("tenant-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Tenant-Id", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		SetContextFieldFromIn(chiCfTenantIDField, func(in tdIn) any { return in.Key })

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).HandleMW(mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		fnSawTenant, _ = chiCfTenantIDField.Get(ctx)
		return tdOut{Value: "ok"}, nil
	}).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		handlerSawTenant, _ = chiCfTenantIDField.Get(ctx)
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

// TestChiSetContextFieldFromOut_DispatchOrder_AfterFnReturns mirrors
// nethttp's identical test — proves the Out side publishes from the
// middleware's OWN returned Out value, available by the time EncodeOut
// composes the response (after Fn returns).
func TestChiSetContextFieldFromOut_DispatchOrder_AfterFnReturns(t *testing.T) {
	var policyVersionField = middleware.NewContextField(codex.String())

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
}
