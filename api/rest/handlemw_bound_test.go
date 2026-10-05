package rest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── docs/design/d-0003-codec-declared-middlewares.md's Addendum 7: BoundMiddleware[Req,In,Out]
// is the explicit, compile-time-distinct route/channel-BOUND class —
// attached via Route.HandleBoundMW/SSERoute.HandleBoundMW/
// Route.ClientBoundMW/SSERoute.ClientBoundMW. These tests confirm:
// Satisfies population, D6(b) still fires (D7 is now structurally
// impossible), CheckCoverage sees a HandleBoundMW-attached middleware as
// covering, and a plain Middleware[In,Out] passed to HandleMW/ClientMW
// is rejected (the permanently-closed legacy escape hatch).

func newBoundSecureDeclaration(name string) middleware.Declaration[mdTestIn, mdTestOut] {
	decl := middleware.NewDeclaration(name, mdTestInCodec, mdTestOutCodec)
	decl.Security = middleware.NewSecurityDeclaration("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	return decl
}

// TestHandleBoundMW_PopulatesSatisfiesAndSpec proves a BoundMiddleware
// attached via HandleBoundMW populates BOTH
// MiddlewareHandlers[i].Satisfies (from its own Security declaration) AND
// the spec contribution (header param), exactly like the agnostic
// (.Use()) path already does.
func TestHandleBoundMW_PopulatesSatisfiesAndSpec(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newBoundSecureDeclaration("bound-bearer-policy"),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/bound-secure", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "boundSecure"},
	).HandleBoundMW(bm).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	if len(h.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
	if got := h.MiddlewareHandlers[0].Satisfies; len(got) != 1 || got[0] != "bearerAuth" {
		t.Errorf("want Satisfies [bearerAuth], got %v", got)
	}
	found := false
	for _, p := range h.Descriptor.HeaderParams {
		if p.Name == "Authorization" {
			found = true
		}
	}
	if !found {
		t.Errorf("want Authorization header param layered into spec, got %+v", h.Descriptor.HeaderParams)
	}
}

// TestCheckCoverage_SeesMiddlewareHandlerSatisfies proves CheckCoverage's
// handlers parameter closes coverage for a scheme ONLY satisfied by a
// MiddlewareHandler (no matching impls entry at all).
func TestCheckCoverage_SeesMiddlewareHandlerSatisfies(t *testing.T) {
	secReqs := []route.SecurityRequirement{{"bearerAuth": nil}}
	handlers := []rest.MiddlewareHandler{{Name: "x", Satisfies: []string{"bearerAuth"}}}

	if err := rest.CheckCoverage("GET /x", secReqs, nil, handlers); err != nil {
		t.Errorf("want nil error (covered by handlers), got %v", err)
	}
}

// TestCheckCoverage_MissingWhenNeitherImplsNorHandlersSatisfy is the
// negative-path regression guard — confirms a scheme with NO covering
// entry in EITHER list still correctly fails.
func TestCheckCoverage_MissingWhenNeitherImplsNorHandlersSatisfy(t *testing.T) {
	secReqs := []route.SecurityRequirement{{"bearerAuth": nil}}
	handlers := []rest.MiddlewareHandler{{Name: "x", Satisfies: []string{"otherScheme"}}}

	err := rest.CheckCoverage("GET /x", secReqs, nil, handlers)
	var missingErr rest.MissingSecurityMiddlewareError
	if !errors.As(err, &missingErr) {
		t.Fatalf("want MissingSecurityMiddlewareError, got %v", err)
	}
	if missingErr.Scheme != "bearerAuth" {
		t.Errorf("want Scheme %q, got %q", "bearerAuth", missingErr.Scheme)
	}
}

// TestHandleBoundMW_DuplicateNameRejected is D6(b)'s own check, exercised
// via HandleBoundMW — confirming the SAME name-uniqueness pass applies to
// the bound attachment style.
func TestHandleBoundMW_DuplicateNameRejected(t *testing.T) {
	fn := func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	}
	bmA := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(), fn).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))
	bmB := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(), fn) // SAME Declaration.Name "test-policy"

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/dup-handlemw", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "dupHandleMW"},
	).HandleBoundMW(bmA).HandleBoundMW(bmB)

	err := route.Register(rest.NewServer(testInfo))
	var dupErr rest.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
	if dupErr.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", dupErr.Name)
	}
}

// TestHandleBoundMW_ReqMismatch_ReturnsTypedError confirms attaching a
// BoundMiddleware constructed for a DIFFERENT Req type returns
// BoundMiddlewareReqMismatchError, not a silent no-op or panic.
func TestHandleBoundMW_ReqMismatch_ReturnsTypedError(t *testing.T) {
	type otherReq struct{ X int }
	bm := rest.NewBoundMiddleware[otherReq](newTestDeclaration(),
		func(ctx context.Context, req *otherReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/mismatch", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "mismatch"},
	).HandleBoundMW(bm)

	err := route.Register(rest.NewServer(testInfo))
	var mismatchErr rest.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	if mismatchErr.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", mismatchErr.Name)
	}
}

// TestHandleBoundMW_SingleAttachmentStyleSucceeds mirrors
// TestRegister_SingleAttachmentStyleSucceeds — a BoundMiddleware used
// ONLY via HandleBoundMW must register successfully.
func TestHandleBoundMW_SingleAttachmentStyleSucceeds(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/single-handlemw", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "singleHandleMW"},
	).HandleBoundMW(bm)
	if err := route.Register(rest.NewServer(testInfo)); err != nil {
		t.Fatalf("want successful Register, got %v", err)
	}
}

// TestBoundMiddleware_SharedAcrossMultipleRoutes_SameReqType confirms a
// capability the roadmap doc explicitly claims is preserved: ONE
// BoundMiddleware[Req,...] value (built once) attaches cleanly to
// MULTIPLE DIFFERENT routes sharing that SAME Req type — the bound
// class's Req-parameterization was never meant to force a fresh
// construction per ROUTE, only per distinct Req TYPE.
func TestBoundMiddleware_SharedAcrossMultipleRoutes_SameReqType(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	routeA := rest.NewRoute[mwTestReq, userResp]("GET", "/shared-a", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "sharedA"},
	).HandleBoundMW(bm)
	routeB := rest.NewRoute[mwTestReq, userResp]("GET", "/shared-b", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "sharedB"},
	).HandleBoundMW(bm)

	hA, errA := routeA.RegisterHandle(rest.NewServer(testInfo))
	if errA != nil {
		t.Fatalf("routeA RegisterHandle: %v", errA)
	}
	hB, errB := routeB.RegisterHandle(rest.NewServer(testInfo))
	if errB != nil {
		t.Fatalf("routeB RegisterHandle: %v", errB)
	}
	if len(hA.MiddlewareHandlers) != 1 || len(hB.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler on each route, got %d / %d", len(hA.MiddlewareHandlers), len(hB.MiddlewareHandlers))
	}
}

// TestHandleBoundMW_PlainMiddleware_ReturnsTypedError confirms passing a
// PLAIN Middleware[In,Out] (never a BoundMiddleware at all, not merely
// the wrong Req) to HandleBoundMW is rejected via
// BoundMiddlewareReqMismatchError — never a panic, never a silent no-op
// — exercising the SAME boundMismatchOpt fallback a true Req mismatch
// uses, but for the "wrong CLASS entirely" case the roadmap's own
// TestMiddleware_NoLongerBindable_RuntimeRejected test-plan entry
// describes.
func TestHandleBoundMW_PlainMiddleware_ReturnsTypedError(t *testing.T) {
	plainMw := rest.SecurityMiddleware[mdTestIn, mdTestOut]("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil).
		WithReceive(func(ctx context.Context, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/plain-to-bound", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "plainToBound"},
	).HandleBoundMW(plainMw)

	err := route.Register(rest.NewServer(testInfo))
	var mismatchErr rest.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	// LogValue must not panic even though Got is the wrong CLASS (not
	// just the wrong Req) — regression guard for a confirmed nil-safety
	// fix; Got here is non-nil, but exercising LogValue at all confirms
	// the method stays callable for every BoundMiddlewareReqMismatchError
	// shape, not just the nil-Got case TestHandleBoundMW_NilBm_ReturnsTypedError covers.
	_ = mismatchErr.LogValue()
}

// TestHandleBoundMW_NilBm_ReturnsTypedError confirms HandleBoundMW(nil)
// returns a typed error (not a panic) at Register time, AND that the
// returned error's LogValue() does not panic on a nil Got — a confirmed,
// previously-real crash (reflect.TypeOf(nil).String() panics) this test
// guards against regressing.
func TestHandleBoundMW_NilBm_ReturnsTypedError(t *testing.T) {
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/nil-bound", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "nilBound"},
	).HandleBoundMW(nil)

	err := route.Register(rest.NewServer(testInfo))
	var mismatchErr rest.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	// Must not panic.
	_ = mismatchErr.LogValue()
}

// TestClientHandle_BoundMismatch_Panics confirms a Req-mismatched
// ClientBoundMW is NEVER silently dropped by the infallible
// Route.ClientHandle — a confirmed, previously-real bug: ClientHandle
// used to build a handle with ZERO ClientMiddlewareHandlers and ZERO
// error/panic anywhere, silently missing the intended credential. Now
// ClientHandle panics (matching this same method's own existing
// FormatOptError panic precedent), rather than staying silently broken.
func TestClientHandle_BoundMismatch_Panics(t *testing.T) {
	type otherReq struct{ X int }
	bm := rest.NewBoundClientMiddleware[otherReq](newTestDeclaration(),
		func(ctx context.Context, req otherReq) (mdTestIn, error) {
			return mdTestIn{Key: "x"}, nil
		})

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/client-mismatch", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "clientMismatch"},
	).ClientBoundMW(bm)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("want ClientHandle to panic on a Req-mismatched ClientBoundMW, got no panic")
		}
	}()
	_ = route.ClientHandle()
}

// TestHandleBoundMW_PlusClientBoundMW_SameScheme_SameRoute_Conflicts
// documents (and locks in, as a regression guard) the current,
// confirmed behavior: a server-side BoundMiddleware (HandleBoundMW) and
// a client-side BoundClientMiddleware (ClientBoundMW) for the SAME
// scheme name CANNOT be combined on ONE shared route value — unlike the
// reusable class ([Middleware], which can carry both a receiveFn and a
// sendFn and be .Use()'d ONCE for both roles), the bound class's two
// types each independently contribute a spec entry under the scheme's
// Declaration Name, tripping D6(b)'s DuplicateMiddlewareNameError. See
// BoundMiddleware's own doc comment and docs/features/security.md for
// the documented two-separate-route-values workaround.
func TestHandleBoundMW_PlusClientBoundMW_SameScheme_SameRoute_Conflicts(t *testing.T) {
	serverBm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})
	clientBm := rest.NewBoundClientMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req mwTestReq) (mdTestIn, error) {
			return mdTestIn{Key: "x"}, nil
		})

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/combo", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "combo"},
	).HandleBoundMW(serverBm).ClientBoundMW(clientBm)

	err := route.Register(rest.NewServer(testInfo))
	var dupErr rest.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError (current, documented behavior), got %v", err)
	}
}

// TestHandleMW_SecurityMiddleware_CredentialShape_Rejected is a
// REGRESSION GUARD confirming the legacy raw-adapter-Fn-pairing escape
// hatch is PERMANENTLY CLOSED: SecurityMiddleware[struct{},struct{}] (a
// codec-backed carrier satisfying routeMiddlewareContributor) paired
// with a legacy credential-shaped fn via HandleMW — exactly like
// examples/go-edge-models/app/registry/auth.go's basicAuthMw USED to —
// must now be REJECTED via MiddlewareMisattachedError, never silently
// dispatched through the (now-removed) legacy path.
func TestHandleMW_SecurityMiddleware_CredentialShape_Rejected(t *testing.T) {
	secMw := rest.SecurityMiddleware[struct{}, struct{}]("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/sec-mw-legacy", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "secMWLegacy"},
	).Use(secMw).HandleMW(&secMw, func(_ context.Context, r *httpRequestStub, _ *mwTestReq) (map[string][]string, error) {
		return map[string][]string{"bearerAuth": nil}, nil
	}).RegisterHandle(rest.NewServer(testInfo))
	var misErr rest.MiddlewareMisattachedError
	if !errors.As(err, &misErr) {
		t.Fatalf("want MiddlewareMisattachedError, got %v", err)
	}
}

// httpRequestStub stands in for *http.Request in the above test's
// credential fn signature — api/rest itself never imports net/http, and
// this test only needs a DISTINCT pointer type (not *mwTestReq).
type httpRequestStub struct{}

// TestClientBoundMW_PopulatesSatisfiesAndSpec is
// [TestHandleBoundMW_PopulatesSatisfiesAndSpec]'s SENDING-role mirror —
// ClientBoundMW.
func TestClientBoundMW_PopulatesSatisfiesAndSpec(t *testing.T) {
	bm := rest.NewBoundClientMiddleware[mwTestReq](newBoundSecureDeclaration("bound-bearer-client-policy"),
		func(ctx context.Context, req mwTestReq) (mdTestIn, error) {
			return mdTestIn{Key: "secret"}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))

	h := rest.NewRoute[mwTestReq, userResp]("GET", "/bound-secure-client", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "boundSecureClient"},
	).ClientBoundMW(bm).ClientHandle()

	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want 1 ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}
	if got := h.ClientMiddlewareHandlers[0].Satisfies; len(got) != 1 || got[0] != "bearerAuth" {
		t.Errorf("want Satisfies [bearerAuth], got %v", got)
	}
}

// ── SecurityMiddleware[In,Out] with real (non-struct{}) In/Out ──────────

type credentialWithToken struct{ Token string }

// TestSecurityMiddleware_RealInType_DoesNotPanicOnDispatch is a
// REGRESSION GUARD for a confirmed, real bug a prior phase's migration
// work caught: SecurityMiddleware[In,Out] generalized over In/Out but
// left InCodec/OutCodec at their Go ZERO VALUE — a zero-value codex.Codec
// PANICS the first time Middleware dispatch calls InCodec.Validate (nil
// Encode/Decode funcs) for any NON-struct{} In. Fixed: InCodec/OutCodec
// default to codex.Struct[In]()/codex.Struct[Out]() (a fieldless struct
// codec — confirmed safe no-op round-trip). This test exercises the FULL
// dispatch path (the reusable class's agnostic EncodeIn, attached via
// .Use()/.WithSend), not just construction, to catch the panic directly.
func TestSecurityMiddleware_RealInType_DoesNotPanicOnDispatch(t *testing.T) {
	mw := rest.SecurityMiddleware[credentialWithToken, struct{}]("bearerAuth",
		rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
	).WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", codex.String(),
		func(c credentialWithToken) string { return c.Token },
		func(c *credentialWithToken, v string) { c.Token = v },
	)).WithSend(func(ctx context.Context) (credentialWithToken, error) {
		return credentialWithToken{Token: "abc123"}, nil
	})

	h := rest.NewRoute[mwTestReq, userResp]("GET", "/sec-real-in", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "secRealIn"},
	).Use(mw).ClientHandle()

	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want 1 ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}
	// Exercises EncodeIn (which calls InCodec.Validate internally) — must
	// NOT panic.
	headers, _, _, err := h.ClientMiddlewareHandlers[0].EncodeIn(context.Background(), credentialWithToken{Token: "abc123"})
	if err != nil {
		t.Fatalf("EncodeIn: %v", err)
	}
	if headers["Authorization"] != "abc123" {
		t.Errorf("want Authorization %q, got %q", "abc123", headers["Authorization"])
	}
}
