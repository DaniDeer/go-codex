package reqreply_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── docs/roadmap/bound-middleware-split.md: BoundMiddleware[Req, In, Out]/
// BoundClientMiddleware[Req, In, Out] are the explicit, compile-time-
// distinct route-BOUND class for reqreply — attached via
// Route.HandleBoundMW/Route.ClientBoundMW. These tests mirror
// api/rest's handlemw_bound_test.go scenarios (REST and reqreply share
// the SAME structural shape — ONE Route value carries both server and
// client attachments, unlike events' independent Subscriber/Publisher),
// written here from the start (not backfilled after a later review
// round, carrying forward every lesson from REST's 3 review rounds and
// events' 2).

// otherComputeReq is a SECOND Req type, deliberately different from
// computeReq, used by the mismatch tests below.
type otherComputeReq struct{ Name string }

func newBoundSecureDeclaration(name string) middleware.Declaration[mdAuthIn, mdAuthOut] {
	decl := middleware.NewDeclaration(name, mdAuthInCodec, mdAuthOutCodec)
	decl.Security = middleware.NewSecurityDeclaration("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	return decl
}

// TestHandleBoundMW_PopulatesSatisfiesAndSpec proves a BoundMiddleware
// attached via HandleBoundMW populates BOTH MiddlewareHandlers[i].Satisfies
// (from its own Security declaration) AND the spec contribution (topic
// param), exactly like the agnostic (.Use()) path already does.
func TestHandleBoundMW_PopulatesSatisfiesAndSpec(t *testing.T) {
	bm := reqreply.NewBoundMiddleware[computeReq](newBoundSecureDeclaration("bound-bearer-policy"),
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{OK: true}, nil
		}).
		WithRequestProperty(reqreply.NewPropertyParam("Authorization", codex.String(),
			func(in mdAuthIn) string { return in.Token },
			func(in *mdAuthIn, v string) { in.Token = v },
		))

	h, err := newMWTestRoute().HandleBoundMW(bm).Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
	if got := h.MiddlewareHandlers[0].Satisfies; len(got) != 1 || got[0] != "bearerAuth" {
		t.Errorf("want Satisfies [bearerAuth], got %v", got)
	}
}

// TestHandleBoundMW_ReqMismatch_ReturnsTypedError confirms a
// BoundMiddleware[otherComputeReq, ...] (built for the WRONG Req)
// attached to a Route[computeReq,...] is rejected via
// BoundMiddlewareReqMismatchError at Register time — never a panic,
// never silently dropped — exercising the boundReqWitness discriminator
// directly.
func TestHandleBoundMW_ReqMismatch_ReturnsTypedError(t *testing.T) {
	bm := reqreply.NewBoundMiddleware[otherComputeReq](newAuthDeclaration("mismatch-policy"),
		func(ctx context.Context, req *otherComputeReq, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{}, nil
		})

	route := newMWTestRoute().HandleBoundMW(bm)
	_, err := route.Register(newBuilder())
	var mismatchErr reqreply.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	if mismatchErr.Name != "mismatch-policy" {
		t.Errorf("want Name %q, got %q", "mismatch-policy", mismatchErr.Name)
	}
	// LogValue must not panic.
	_ = mismatchErr.LogValue()
}

// TestClientBoundMW_ReqMismatch_ReturnsTypedError mirrors
// TestHandleBoundMW_ReqMismatch_ReturnsTypedError for the client role.
func TestClientBoundMW_ReqMismatch_ReturnsTypedError(t *testing.T) {
	bm := reqreply.NewBoundClientMiddleware[otherComputeReq](newAuthDeclaration("mismatch-policy-client"),
		func(ctx context.Context, req otherComputeReq) (mdAuthIn, error) {
			return mdAuthIn{}, nil
		})

	route := newMWTestRoute().ClientBoundMW(bm)
	_, err := route.Register(newBuilder())
	var mismatchErr reqreply.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	if mismatchErr.Name != "mismatch-policy-client" {
		t.Errorf("want Name %q, got %q", "mismatch-policy-client", mismatchErr.Name)
	}
	_ = mismatchErr.LogValue()
}

// TestHandleBoundMW_PlainMiddleware_ReturnsTypedError confirms passing a
// PLAIN Middleware[In,Out] (never a BoundMiddleware at all, not merely
// the wrong Req) to HandleBoundMW is rejected via
// BoundMiddlewareReqMismatchError — never a panic, never a silent no-op.
func TestHandleBoundMW_PlainMiddleware_ReturnsTypedError(t *testing.T) {
	plainMw := reqreply.NewMiddleware(newAuthDeclaration("plain-policy")).
		WithReceive(func(ctx context.Context, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	route := newMWTestRoute().HandleBoundMW(plainMw)
	_, err := route.Register(newBuilder())
	var mismatchErr reqreply.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	// LogValue must not panic even though Got is the wrong CLASS (not
	// just the wrong Req).
	_ = mismatchErr.LogValue()
}

// TestHandleBoundMW_NilBm_ReturnsTypedError confirms HandleBoundMW(nil)
// returns a typed error (not a panic) at Register time, AND that the
// returned error's LogValue() does not panic on a nil Got.
func TestHandleBoundMW_NilBm_ReturnsTypedError(t *testing.T) {
	route := newMWTestRoute().HandleBoundMW(nil)
	_, err := route.Register(newBuilder())
	var mismatchErr reqreply.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	// Must not panic.
	_ = mismatchErr.LogValue()
}

// TestClientBoundMW_NilBm_ReturnsTypedError mirrors
// TestHandleBoundMW_NilBm_ReturnsTypedError for the client role.
func TestClientBoundMW_NilBm_ReturnsTypedError(t *testing.T) {
	route := newMWTestRoute().ClientBoundMW(nil)
	_, err := route.Register(newBuilder())
	var mismatchErr reqreply.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	_ = mismatchErr.LogValue()
}

// TestClientHandle_BoundMismatch_Panics confirms a Req-mismatched
// BoundMiddleware is NEVER silently dropped by the infallible
// Route.ClientHandle — ClientHandle panics (matching that method's own
// existing FormatOptError panic precedent) rather than staying silently
// broken.
func TestClientHandle_BoundMismatch_Panics(t *testing.T) {
	bm := reqreply.NewBoundMiddleware[otherComputeReq](newAuthDeclaration("panic-policy"),
		func(ctx context.Context, req *otherComputeReq, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{}, nil
		})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("want ClientHandle to panic on a Req-mismatched BoundMiddleware")
		}
	}()
	newMWTestRoute().HandleBoundMW(bm).ClientHandle()
}

// TestHandleBoundMW_SharedAcrossMultipleRoutes_SameReqType confirms a
// capability the roadmap doc explicitly claims is preserved: ONE
// BoundMiddleware[Req,...] value (built once) attaches cleanly to
// MULTIPLE DIFFERENT routes sharing that SAME Req type.
func TestHandleBoundMW_SharedAcrossMultipleRoutes_SameReqType(t *testing.T) {
	bm := reqreply.NewBoundMiddleware[computeReq](newAuthDeclaration("shared-policy"),
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{}, nil
		})

	b := newBuilder()
	hA, errA := reqreply.NewRoute[computeReq, computeResp]("compute/shared-a", mwTestReqCodec, mwTestRespCodec).
		HandleBoundMW(bm).Register(b)
	if errA != nil {
		t.Fatalf("routeA Register: %v", errA)
	}
	hB, errB := reqreply.NewRoute[computeReq, computeResp]("compute/shared-b", mwTestReqCodec, mwTestRespCodec).
		HandleBoundMW(bm).Register(b)
	if errB != nil {
		t.Fatalf("routeB Register: %v", errB)
	}
	if len(hA.MiddlewareHandlers) != 1 || len(hB.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler on each route, got %d / %d", len(hA.MiddlewareHandlers), len(hB.MiddlewareHandlers))
	}
}

// TestHandleBoundMW_PlusClientBoundMW_SameScheme_SameRoute_Conflicts
// confirms the CONFLICT this doc's own godoc claims (CONFIRMED applicable
// to reqreply, unlike events' independent Subscriber/Publisher values) —
// a HandleBoundMW + ClientBoundMW pairing for the SAME scheme name on
// ONE shared Route value always fails with DuplicateMiddlewareNameError,
// mirroring REST's identical confirmed behavior.
func TestHandleBoundMW_PlusClientBoundMW_SameScheme_SameRoute_Conflicts(t *testing.T) {
	serverBm := reqreply.BoundSecurityMiddleware[computeReq, mdAuthIn, mdAuthOut]("bearerAuth",
		reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) {
			return mdAuthOut{}, nil
		})
	clientBm := reqreply.BoundSecurityClientMiddleware[computeReq, mdAuthIn, mdAuthOut]("bearerAuth",
		reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
		func(ctx context.Context, req computeReq) (mdAuthIn, error) {
			return mdAuthIn{}, nil
		})

	route := newMWTestRoute().HandleBoundMW(serverBm).ClientBoundMW(clientBm)
	_, err := route.Register(newBuilder())
	var dupErr reqreply.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
}

// TestHandleBoundMW_StackedWithUse_BothDispatch confirms a reusable
// Middleware (attached via .Use()) and a BoundMiddleware (attached via
// HandleBoundMW) can be STACKED on ONE Route, composing rather than
// conflicting — declaration order is dispatch order.
func TestHandleBoundMW_StackedWithUse_BothDispatch(t *testing.T) {
	var order []string

	reusable := reqreply.NewMiddleware(newAuthDeclaration("generic-policy")).
		WithReceive(func(ctx context.Context, in mdAuthIn) (mdAuthOut, error) {
			order = append(order, "generic")
			return mdAuthOut{}, nil
		})
	bound := reqreply.NewBoundMiddleware[computeReq](newAuthDeclaration("specific-policy"),
		func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) {
			order = append(order, "specific")
			return mdAuthOut{}, nil
		})

	h, err := newMWTestRoute().Use(reusable).HandleBoundMW(bound).Register(newBuilder())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.MiddlewareHandlers) != 2 {
		t.Fatalf("want 2 MiddlewareHandlers (stacked), got %d", len(h.MiddlewareHandlers))
	}
	if h.MiddlewareHandlers[0].Name != "generic-policy" || h.MiddlewareHandlers[1].Name != "specific-policy" {
		t.Errorf("want registration order [generic-policy, specific-policy], got [%s, %s]",
			h.MiddlewareHandlers[0].Name, h.MiddlewareHandlers[1].Name)
	}
}

// TestHandleMW_PlainCodecBackedMiddleware_Rejected confirms HandleMW
// (general-purpose) rejects a codec-backed Middleware outright via
// MiddlewareMisattachedError — the permanently-closed legacy raw-
// adapter-Fn-pairing escape hatch.
func TestHandleMW_PlainCodecBackedMiddleware_Rejected(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("dual-policy")).
		WithReceive(func(ctx context.Context, in mdAuthIn) (mdAuthOut, error) { return mdAuthOut{}, nil })

	route := newMWTestRoute().HandleMW(mw, func(ctx context.Context, req *computeReq, in mdAuthIn) (mdAuthOut, error) {
		return mdAuthOut{}, nil
	})
	_, err := route.Register(newBuilder())
	var misErr reqreply.MiddlewareMisattachedError
	if !errors.As(err, &misErr) {
		t.Fatalf("want MiddlewareMisattachedError, got %v", err)
	}
	if misErr.Name != "dual-policy" {
		t.Errorf("want Name %q, got %q", "dual-policy", misErr.Name)
	}
}

// TestClientMW_PlainCodecBackedMiddleware_Rejected mirrors
// TestHandleMW_PlainCodecBackedMiddleware_Rejected for ClientMW.
func TestClientMW_PlainCodecBackedMiddleware_Rejected(t *testing.T) {
	mw := reqreply.NewMiddleware(newAuthDeclaration("dual-policy-client")).
		WithSend(func(ctx context.Context) (mdAuthIn, error) { return mdAuthIn{}, nil })

	route := newMWTestRoute().ClientMW(mw, func(ctx context.Context, req computeReq) (mdAuthIn, error) {
		return mdAuthIn{}, nil
	})
	_, err := route.Register(newBuilder())
	var misErr reqreply.MiddlewareMisattachedError
	if !errors.As(err, &misErr) {
		t.Fatalf("want MiddlewareMisattachedError, got %v", err)
	}
	if misErr.Name != "dual-policy-client" {
		t.Errorf("want Name %q, got %q", "dual-policy-client", misErr.Name)
	}
}
