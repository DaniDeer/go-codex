package reqreply_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase C:
// Middleware.SetContextFieldFromIn/SetContextFieldFromOut — reqreply's
// mirror of adapters/nethttp/contextfield_dispatch_test.go's dispatch-
// order tests (reqreply is fully symmetric with REST — BOTH directions
// have Out — so this is a more direct port of REST's exact pattern than
// events' asymmetric Subscribe-has-no-Out case was).

var cfReqreplyTenantField = reqreply.NewContextField(codex.String())

// TestSetContextFieldFromIn_DispatchOrder_BeforeFn proves the field is
// published DURING DecodeIn — before the attached middleware's own Fn
// runs.
func TestSetContextFieldFromIn_DispatchOrder_BeforeFn(t *testing.T) {
	var fnSawTenant string
	mw := newBoundTenantMiddleware("tenant-ctx-policy",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			fnSawTenant, _ = cfReqreplyTenantField.Get(ctx)
			return tfOut{}, nil
		}).
		WithRequestTopic(reqreply.NewTopicParam("tenantID", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s })).
		SetContextFieldFromIn(cfReqreplyTenantField, func(in tfIn) any { return in.TenantID })

	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx := middleware.EnsureContextFields(context.Background())
	req := computeReq{X: 1, Y: 2}
	_, _, _, _, _, err = reqreply.DispatchServerMiddlewareHandlers(ctx, reflect.ValueOf(&req), h.MiddlewareHandlers, map[string]string{"tenantID": "acme-corp"}, nil)
	if err != nil {
		t.Fatalf("DispatchServerMiddlewareHandlers: %v", err)
	}
	if fnSawTenant != "acme-corp" {
		t.Errorf("want Fn to see tenantID %q, got %q", "acme-corp", fnSawTenant)
	}
	got, _ := cfReqreplyTenantField.Get(ctx)
	if got != "acme-corp" {
		t.Errorf("want ctx to carry tenantID %q after dispatch, got %q", "acme-corp", got)
	}
}

// TestSetContextFieldFromOut_DispatchOrder_AfterFnReturns proves the Out
// side publishes from the middleware's OWN returned Out value, available
// AFTER Fn returns (the server/receiving role — EncodeOut).
func TestSetContextFieldFromOut_DispatchOrder_AfterFnReturns(t *testing.T) {
	echoField := reqreply.NewContextField(codex.String())
	mw := newBoundTenantMiddleware("echo-ctx-policy",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			return tfOut{Echo: "processed"}, nil
		}).
		SetContextFieldFromOut(echoField, func(out tfOut) any { return out.Echo })

	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx := middleware.EnsureContextFields(context.Background())
	req := computeReq{X: 1, Y: 2}
	_, _, _, _, _, err = reqreply.DispatchServerMiddlewareHandlers(ctx, reflect.ValueOf(&req), h.MiddlewareHandlers, nil, nil)
	if err != nil {
		t.Fatalf("DispatchServerMiddlewareHandlers: %v", err)
	}
	got, ok := echoField.Get(ctx)
	if !ok || got != "processed" {
		t.Errorf("want (%q, true) after dispatch, got (%q, %v)", "processed", got, ok)
	}
}
