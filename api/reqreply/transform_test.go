package reqreply_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

type tfIn struct{ TenantID string }
type tfOut struct{ Echo string }

var tfInCodec = codex.Struct[tfIn]()
var tfOutCodec = codex.Struct[tfOut]()

// newBoundTenantMiddleware is [newTenantMiddleware]'s route-BOUND
// counterpart — fn is embedded at construction (per [reqreply.BoundMiddleware]'s
// shape), With* merge-field chaining happens afterward.
func newBoundTenantMiddleware(name string, fn func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error)) reqreply.BoundMiddleware[computeReq, tfIn, tfOut] {
	return reqreply.NewBoundMiddleware[computeReq](reqreply.NewDeclaration(name, tfInCodec, tfOutCodec), fn)
}

// newBoundTenantClientMiddleware mirrors [newBoundTenantMiddleware] for
// the client/sending role, attached via Route.ClientBoundMW.
func newBoundTenantClientMiddleware(name string, fn func(ctx context.Context, req computeReq) (tfIn, error)) reqreply.BoundClientMiddleware[computeReq, tfIn, tfOut] {
	return reqreply.NewBoundClientMiddleware[computeReq](reqreply.NewDeclaration(name, tfInCodec, tfOutCodec), fn)
}

// TestMiddleware_WithRequestTopic_MergesIn confirms HandleBoundMW's fn
// receives correctly-decoded In from request topic vars.
func TestMiddleware_WithRequestTopic_MergesIn(t *testing.T) {
	mw := newBoundTenantMiddleware("with-req-topic",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			return tfOut{Echo: in.TenantID}, nil
		}).
		WithRequestTopic(reqreply.NewTopicParam("tenantID", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s }))

	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	mwh := h.MiddlewareHandlers[0]
	inAny, err := mwh.DecodeIn(context.Background(), map[string]string{"tenantID": "acme"}, nil)
	if err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}
	in := inAny.(tfIn)
	if in.TenantID != "acme" {
		t.Fatalf("want TenantID=acme, got %q", in.TenantID)
	}
}

// TestMiddleware_WithResponseTopic_EncodesOutIntoReply confirms
// HandleBoundMW's returned Out correctly encodes into the reply's topic
// vars (server-side EncodeOut).
func TestMiddleware_WithResponseTopic_EncodesOutIntoReply(t *testing.T) {
	mw := newBoundTenantMiddleware("with-resp-topic",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			return tfOut{Echo: "hello"}, nil
		}).
		WithResponseTopic(reqreply.NewTopicParam("echo", codex.String(),
			func(v tfOut) string { return v.Echo },
			func(v *tfOut, s string) { v.Echo = s }))

	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	mwh := h.MiddlewareHandlers[0]
	topicVars, _, err := mwh.EncodeOut(context.Background(), tfOut{Echo: "hello"})
	if err != nil {
		t.Fatalf("EncodeOut: %v", err)
	}
	if topicVars["echo"] != "hello" {
		t.Fatalf("want topicVars[echo]=hello, got %+v", topicVars)
	}
}

// TestMiddleware_WithResponseTopic_DecodesOutFromReply confirms
// ClientBoundMW's DecodeOut mechanically decodes Out from the actual
// reply's topic vars, no Fn involved.
func TestMiddleware_WithResponseTopic_DecodesOutFromReply(t *testing.T) {
	mw := newBoundTenantClientMiddleware("client-resp-topic",
		func(ctx context.Context, req computeReq) (tfIn, error) { return tfIn{}, nil }).
		WithResponseTopic(reqreply.NewTopicParam("echo", codex.String(),
			func(v tfOut) string { return v.Echo },
			func(v *tfOut, s string) { v.Echo = s }))

	r := newMWTestRoute().ClientBoundMW(mw)
	h := r.ClientHandle()
	cmwh := h.ClientMiddlewareHandlers[0]
	outAny, err := cmwh.DecodeOut(context.Background(), map[string]string{"echo": "world"}, nil)
	if err != nil {
		t.Fatalf("DecodeOut: %v", err)
	}
	out := outAny.(tfOut)
	if out.Echo != "world" {
		t.Fatalf("want Echo=world, got %q", out.Echo)
	}
}

// TestHandleBoundMW_EnrichesReqPointer confirms HandleBoundMW's fn can
// both READ and WRITE *Req.
func TestHandleBoundMW_EnrichesReqPointer(t *testing.T) {
	mw := newBoundTenantMiddleware("enrich-req",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			req.X = 100
			return tfOut{}, nil
		})
	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	fn := h.MiddlewareHandlers[0].Fn.(func(context.Context, *computeReq, tfIn) (tfOut, error))
	req := computeReq{X: 1, Y: 2}
	if _, err := fn(context.Background(), &req, tfIn{}); err != nil {
		t.Fatalf("fn: %v", err)
	}
	if req.X != 100 {
		t.Fatalf("want req.X enriched to 100, got %d", req.X)
	}
}

// TestClientBoundMW_ProducesInFromReq confirms ClientBoundMW's fn
// receives the caller's own Req value and produces In.
func TestClientBoundMW_ProducesInFromReq(t *testing.T) {
	mw := newBoundTenantClientMiddleware("client-produce-in",
		func(ctx context.Context, req computeReq) (tfIn, error) {
			return tfIn{TenantID: "from-req"}, nil
		})
	r := newMWTestRoute().ClientBoundMW(mw)
	h := r.ClientHandle()
	fn := h.ClientMiddlewareHandlers[0].Fn.(func(context.Context, computeReq) (tfIn, error))
	in, err := fn(context.Background(), computeReq{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("fn: %v", err)
	}
	if in.TenantID != "from-req" {
		t.Fatalf("want TenantID=from-req, got %q", in.TenantID)
	}
}

// TestRoute_MultipleHandleBoundMWAttachments_DispatchInRegistrationOrder
// (Round 12): TWO SEPARATE HandleBoundMW calls chained onto the SAME route
// both dispatch, in registration order.
func TestRoute_MultipleHandleBoundMWAttachments_DispatchInRegistrationOrder(t *testing.T) {
	var order []string
	mw1 := newBoundTenantMiddleware("first",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			order = append(order, "first")
			return tfOut{}, nil
		})
	mw2 := newBoundTenantMiddleware("second",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			order = append(order, "second")
			return tfOut{}, nil
		})
	r := newMWTestRoute().HandleBoundMW(mw1)
	r = r.HandleBoundMW(mw2)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.MiddlewareHandlers) != 2 {
		t.Fatalf("want 2 accumulated MiddlewareHandlers, got %d", len(h.MiddlewareHandlers))
	}
	req := computeReq{}
	for _, mwh := range h.MiddlewareHandlers {
		fn := mwh.Fn.(func(context.Context, *computeReq, tfIn) (tfOut, error))
		if _, err := fn(context.Background(), &req, tfIn{}); err != nil {
			t.Fatalf("fn: %v", err)
		}
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("want dispatch order [first second], got %v", order)
	}
}

// TestHandleBoundMW_D6c_TwoMiddlewaresEnrichSameReqField_LastAppliedWins
// (Round 12): two HandleBoundMW-attached middlewares' own fns BOTH write
// to the SAME *Req field — attachment-order, last-applied-wins, NOT an
// error.
func TestHandleBoundMW_D6c_TwoMiddlewaresEnrichSameReqField_LastAppliedWins(t *testing.T) {
	mw1 := newBoundTenantMiddleware("enrich-1",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			req.X = 1
			return tfOut{}, nil
		})
	mw2 := newBoundTenantMiddleware("enrich-2",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			req.X = 2
			return tfOut{}, nil
		})
	r := newMWTestRoute().HandleBoundMW(mw1)
	r = r.HandleBoundMW(mw2)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	req := computeReq{}
	for _, mwh := range h.MiddlewareHandlers {
		fn := mwh.Fn.(func(context.Context, *computeReq, tfIn) (tfOut, error))
		if _, err := fn(context.Background(), &req, tfIn{}); err != nil {
			t.Fatalf("fn: %v", err)
		}
	}
	if req.X != 2 {
		t.Fatalf("want last-applied-wins (req.X=2), got %d", req.X)
	}
}

// TestMiddleware_PropertyMergeComposesWithGobRequestFormat mirrors rest's
// own TestGobBodyFormat_ComposesWithNestedMergeFields reference pattern
// (Round 14) — a route's payload format set to Gob (non-JSON), with a
// WithRequestProperty merge field ALSO declared, confirming payload-
// format and var-merge remain fully orthogonal.
func TestMiddleware_PropertyMergeComposesWithGobRequestFormat(t *testing.T) {
	gobRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/gob-mw-test", mwTestReqCodec, mwTestRespCodec,
		reqreply.RequestFormats(format.Gob(mwTestReqCodec)),
	)
	mw := newBoundTenantMiddleware("gob-compose",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) {
			return tfOut{Echo: in.TenantID}, nil
		}).
		WithRequestProperty(reqreply.NewPropertyParam("tenantID", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s }))

	r := gobRoute.HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.RequestFormats) != 1 {
		t.Fatalf("want the Gob RequestFormats to survive alongside the property-merge Middleware, got %d", len(h.RequestFormats))
	}
	inAny, err := h.MiddlewareHandlers[0].DecodeIn(context.Background(), nil, map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}
	if inAny.(tfIn).TenantID != "acme" {
		t.Fatalf("want TenantID=acme (property-merge unaffected by Gob payload format), got %+v", inAny)
	}
}

// TestMiddleware_WithRequestProperty_MergesIn confirms HandleBoundMW's
// fn receives correctly-decoded In, decoded from its OWN separate
// property-value map, alongside (never combined with) any topic vars.
func TestMiddleware_WithRequestProperty_MergesIn(t *testing.T) {
	mw := newBoundTenantMiddleware("prop-merge",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) { return tfOut{}, nil }).
		WithRequestProperty(reqreply.NewPropertyParam("X-Tenant", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s }))
	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	mwh := h.MiddlewareHandlers[0]
	inAny, err := mwh.DecodeIn(context.Background(), nil, map[string]string{"X-Tenant": "acme"})
	if err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}
	if inAny.(tfIn).TenantID != "acme" {
		t.Fatalf("want TenantID=acme, got %+v", inAny)
	}
}

// TestMiddleware_WithRequestProperty_RequiredButAdapterSuppliesNoPropertyMap
// confirms a route declaring a REQUIRED property on an adapter with no
// property mechanism (simulated empty map) fails with the SAME
// MiddlewareInputError a missing topic var would.
func TestMiddleware_WithRequestProperty_RequiredButAdapterSuppliesNoPropertyMap(t *testing.T) {
	mw := newBoundTenantMiddleware("prop-required",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) { return tfOut{}, nil }).
		WithRequestProperty(reqreply.NewPropertyParam("X-Tenant", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s }))
	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err = h.MiddlewareHandlers[0].DecodeIn(context.Background(), nil, nil)
	var inputErr reqreply.MiddlewareInputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("want MiddlewareInputError for missing required property, got %T: %v", err, err)
	}
}

// TestMiddleware_WithOptionalRequestProperty_PresentMergesCorrectly.
func TestMiddleware_WithOptionalRequestProperty_PresentMergesCorrectly(t *testing.T) {
	mw := newBoundTenantMiddleware("prop-optional-present",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) { return tfOut{}, nil }).
		WithRequestProperty(reqreply.NewOptionalPropertyParam("X-Tenant", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s }))
	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	inAny, err := h.MiddlewareHandlers[0].DecodeIn(context.Background(), nil, map[string]string{"X-Tenant": "acme"})
	if err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}
	if inAny.(tfIn).TenantID != "acme" {
		t.Fatalf("want TenantID=acme, got %+v", inAny)
	}
}

// TestMiddleware_WithOptionalRequestProperty_AbsentLeavesZeroValueNoError.
func TestMiddleware_WithOptionalRequestProperty_AbsentLeavesZeroValueNoError(t *testing.T) {
	mw := newBoundTenantMiddleware("prop-optional-absent",
		func(ctx context.Context, req *computeReq, in tfIn) (tfOut, error) { return tfOut{}, nil }).
		WithRequestProperty(reqreply.NewOptionalPropertyParam("X-Tenant", codex.String(),
			func(v tfIn) string { return v.TenantID },
			func(v *tfIn, s string) { v.TenantID = s }))
	r := newMWTestRoute().HandleBoundMW(mw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	inAny, err := h.MiddlewareHandlers[0].DecodeIn(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("want no error when optional property absent, got %v", err)
	}
	if inAny.(tfIn).TenantID != "" {
		t.Fatalf("want zero value, got %+v", inAny)
	}
}

// TestClientBoundMW_ValueConflict_MiddlewareDerivedWins mirrors D3:
// when the route's OWN topic merge (from Req) and a Middleware's
// WithRequestTopic (from In) both target the SAME var name with
// DIFFERING runtime values, the middleware-derived value wins.
func TestClientBoundMW_ValueConflict_MiddlewareDerivedWins(t *testing.T) {
	type tvReq struct{ TenantID string }
	tvReqCodec := codex.Struct[tvReq](
		codex.RequiredField("tenantID", codex.String(),
			func(r tvReq) string { return r.TenantID },
			func(r *tvReq, v string) { r.TenantID = v }),
	)
	route := reqreply.NewRoute[tvReq, computeResp](
		"compute/{tenantID}/add", tvReqCodec, mwTestRespCodec,
		reqreply.NewTopicParam("tenantID", codex.String(),
			func(r tvReq) string { return r.TenantID },
			func(r *tvReq, v string) { r.TenantID = v }),
	)
	type tvIn struct{ TenantID string }
	type tvOut struct{}
	tvMw := reqreply.NewBoundClientMiddleware[tvReq](reqreply.NewDeclaration("tv-conflict", codex.Struct[tvIn](), codex.Struct[tvOut]()),
		func(ctx context.Context, req tvReq) (tvIn, error) {
			return tvIn{TenantID: "mw-derived"}, nil
		}).
		WithRequestTopic(reqreply.NewTopicParam("tenantID", codex.String(),
			func(v tvIn) string { return v.TenantID },
			func(v *tvIn, s string) { v.TenantID = s }))

	r := route.ClientBoundMW(tvMw)
	h := r.ClientHandle()

	routeOwnVars, err := h.EncodeVars(tvReq{TenantID: "route-own"})
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}

	cmwh := h.ClientMiddlewareHandlers[0]
	fn := cmwh.Fn.(func(context.Context, tvReq) (tvIn, error))
	in, err := fn(context.Background(), tvReq{TenantID: "route-own"})
	if err != nil {
		t.Fatalf("fn: %v", err)
	}
	mwTopicVars, _, err := cmwh.EncodeIn(context.Background(), in)
	if err != nil {
		t.Fatalf("EncodeIn: %v", err)
	}

	merged := make(map[string]string)
	for k, v := range routeOwnVars {
		merged[k] = v
	}
	for k, v := range mwTopicVars {
		merged[k] = v
	}
	if merged["tenantID"] != "mw-derived" {
		t.Fatalf("want middleware-derived value to win, got %q", merged["tenantID"])
	}
}

// TestHandleBoundMW_ValueConflict_MiddlewareDerivedWins applies D3's
// precedence rule to HandleBoundMW's reply-side encode: a Middleware's
// WithResponseTopic value overrides the route's own Resp-derived value
// for the SAME var name.
func TestHandleBoundMW_ValueConflict_MiddlewareDerivedWins(t *testing.T) {
	type tvResp struct{ Echo string }
	tvRespCodec := codex.Struct[tvResp](
		codex.RequiredField("echo", codex.String(),
			func(r tvResp) string { return r.Echo },
			func(r *tvResp, v string) { r.Echo = v }),
	)
	type tvOut struct{ Echo string }
	tvMw := reqreply.NewBoundMiddleware[computeReq](reqreply.NewDeclaration("tv-resp-conflict", codex.Struct[tfIn](), codex.Struct[tvOut]()),
		func(ctx context.Context, req *computeReq, in tfIn) (tvOut, error) {
			return tvOut{Echo: "mw-derived"}, nil
		}).
		WithResponseTopic(reqreply.NewTopicParam("echo", codex.String(),
			func(v tvOut) string { return v.Echo },
			func(v *tvOut, s string) { v.Echo = s }))

	r := newMWTestRoute2(tvRespCodec).HandleBoundMW(tvMw)
	b := newBuilder()
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	routeOwnVars, err := codex.EncodeVars(tvResp{Echo: "route-own"},
		codex.RequiredField[tvResp, string]("echo",
			codex.String(),
			func(r tvResp) string { return r.Echo },
			func(r *tvResp, v string) { r.Echo = v }))
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	mwTopicVars, _, err := h.MiddlewareHandlers[0].EncodeOut(context.Background(), tvOut{Echo: "mw-derived"})
	if err != nil {
		t.Fatalf("EncodeOut: %v", err)
	}
	merged := make(map[string]string)
	for k, v := range routeOwnVars {
		merged[k] = v
	}
	for k, v := range mwTopicVars {
		merged[k] = v
	}
	if merged["echo"] != "mw-derived" {
		t.Fatalf("want middleware-derived value to win, got %q", merged["echo"])
	}
}

// newMWTestRoute2 mirrors newMWTestRoute but with a caller-supplied Resp
// codec, so TestHandleBoundMW_ValueConflict_MiddlewareDerivedWins can declare
// its own tvOut-based Middleware sharing computeReq as the route's Req.
func newMWTestRoute2[Resp any](respCodec codex.Codec[Resp]) reqreply.Route[computeReq, Resp] {
	return reqreply.NewRoute[computeReq, Resp](
		"compute/mw-test-2",
		mwTestReqCodec, respCodec,
		reqreply.RouteMeta{OperationID: "mwTest2"},
	)
}
