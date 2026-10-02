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

// ── docs/roadmap/declarative-middleware-layering.md's Rollout Phase A:
// Route.HandleMW/SSERoute.HandleMW/Route.ClientMW/SSERoute.ClientMW now
// support a codec-backed Middleware[In,Out] (BOUND, *Req-aware dispatch)
// in addition to the legacy middleware.Middleware path — reached through
// the ordinary method-chain API instead of the Transform/ClientTransform
// free functions. These tests confirm: Satisfies population, D6(b)/D7
// still fire for the new attachment style, and CheckCoverage sees a
// HandleMW-attached codec-backed middleware as covering.

func newBoundSecureDeclaration(name string) middleware.Declaration[mdTestIn, mdTestOut] {
	decl := middleware.NewDeclaration(name, mdTestInCodec, mdTestOutCodec)
	decl.Security = middleware.NewSecurityDeclaration("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	return decl
}

// TestHandleMW_CodecBackedMiddleware_PopulatesSatisfiesAndSpec proves a
// codec-backed Middleware[In,Out] attached via HandleMW (not Transform)
// populates BOTH MiddlewareHandlers[i].Satisfies (from mw's own Security
// declaration) AND the spec contribution (header param), exactly like the
// pre-existing agnostic (.Use()) and Transform-bound paths already do.
func TestHandleMW_CodecBackedMiddleware_PopulatesSatisfiesAndSpec(t *testing.T) {
	mw := rest.NewMiddleware(newBoundSecureDeclaration("bound-bearer-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/bound-secure", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "boundSecure"},
	).HandleMW(mw, func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	}).RegisterHandle(rest.NewServer(testInfo))
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
// new handlers parameter closes coverage for a scheme ONLY satisfied by a
// MiddlewareHandler (no matching impls entry at all) — confirming the
// signature extension actually works, not just compiles.
func TestCheckCoverage_SeesMiddlewareHandlerSatisfies(t *testing.T) {
	secReqs := []route.SecurityRequirement{{"bearerAuth": nil}}
	handlers := []rest.MiddlewareHandler{{Name: "x", Satisfies: []string{"bearerAuth"}}}

	if err := rest.CheckCoverage("GET /x", secReqs, nil, handlers); err != nil {
		t.Errorf("want nil error (covered by handlers), got %v", err)
	}
}

// TestCheckCoverage_MissingWhenNeitherImplsNorHandlersSatisfy is the
// negative-path regression guard — confirms a scheme with NO covering
// entry in EITHER list still correctly fails, i.e. the extension didn't
// accidentally make coverage checking vacuously true.
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

// TestHandleMW_CodecBackedMiddleware_DuplicateNameRejected is D6(b)'s own
// check, exercised via HandleMW instead of Transform — confirming the
// SAME name-uniqueness pass already applied to Transform-attached
// middleware also fires for the new bound-via-HandleMW attachment style
// (both populate rb.middlewareSpecContributions identically).
func TestHandleMW_CodecBackedMiddleware_DuplicateNameRejected(t *testing.T) {
	mwA := rest.NewMiddleware(newTestDeclaration()).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))
	mwB := rest.NewMiddleware(newTestDeclaration()) // SAME Declaration.Name "test-policy"

	fn := func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	}
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/dup-handlemw", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "dupHandleMW"},
	).HandleMW(mwA, fn).HandleMW(mwB, fn)

	err := route.Register(rest.NewServer(testInfo))
	var dupErr rest.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
	if dupErr.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", dupErr.Name)
	}
}

// TestHandleMW_CodecBackedMiddleware_AmbiguousDualAttachmentRejected is
// D7's own check, exercised via HandleMW instead of Transform — a mw
// bundled via WithReceive (agnostic-ready) that is ALSO attached via
// .Use().HandleMW() (bound) is an ambiguous dual attachment, exactly like
// the equivalent Transform-based scenario already rejects.
func TestHandleMW_CodecBackedMiddleware_AmbiguousDualAttachmentRejected(t *testing.T) {
	mw := rest.NewMiddleware(newTestDeclaration()).
		WithReceive(func(ctx context.Context, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/ambiguous-handlemw", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "ambiguousHandleMW"},
	).HandleMW(mw, func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	})

	err := route.Register(rest.NewServer(testInfo))
	var ambErr rest.AmbiguousMiddlewareAttachmentError
	if !errors.As(err, &ambErr) {
		t.Fatalf("want AmbiguousMiddlewareAttachmentError, got %v", err)
	}
	if ambErr.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", ambErr.Name)
	}
}

// TestHandleMW_CodecBackedMiddleware_SingleAttachmentStyleSucceeds mirrors
// TestRegister_SingleAttachmentStyleSucceeds via HandleMW instead of
// Transform — a mw used ONLY via HandleMW (bound, not bundled) must NOT
// trip D7.
func TestHandleMW_CodecBackedMiddleware_SingleAttachmentStyleSucceeds(t *testing.T) {
	mw := rest.NewMiddleware(newTestDeclaration())
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/single-handlemw", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "singleHandleMW"},
	).HandleMW(mw, func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	})
	if err := route.Register(rest.NewServer(testInfo)); err != nil {
		t.Fatalf("want successful Register, got %v", err)
	}
}

// TestHandleMW_SecurityMiddleware_CredentialShape_UsesLegacyPath is a
// REGRESSION GUARD for a confirmed, real bug this phase's own test suite
// caught: [SecurityMiddleware] returns a [Middleware][struct{}, struct{}]
// — a codec-backed carrier satisfying routeMiddlewareContributor — used
// PURELY to carry a Security declaration, paired with a LEGACY
// credential-shaped fn (func(ctx, *http.Request, *Req) (map[string][]string,
// error)), exactly like examples/go-edge-models/app/registry/auth.go's
// basicAuthMw. mw satisfying routeMiddlewareContributor is NOT by itself
// sufficient to route into the bound path — fn's shape must ALSO match;
// otherwise this credential fn is wrongly dispatched as a bound EncodeIn
// (confirmed: this exact scenario panics with "reflect: Call using ...
// as type []route.SecurityRequirement" before the isBound*Shape fix).
func TestHandleMW_SecurityMiddleware_CredentialShape_UsesLegacyPath(t *testing.T) {
	secMw := rest.SecurityMiddleware[struct{}, struct{}]("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)

	credCalled := false
	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/sec-mw-legacy", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "secMWLegacy"},
	).Use(secMw).HandleMW(&secMw, func(_ context.Context, r *httpRequestStub, _ *mwTestReq) (map[string][]string, error) {
		credCalled = true
		return map[string][]string{"bearerAuth": nil}, nil
	}).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	// The credential fn must have landed in Implementations (legacy path),
	// NOT MiddlewareHandlers (bound path) — confirms the shape-detector
	// correctly classified it despite secMw satisfying
	// routeMiddlewareContributor.
	if len(h.MiddlewareHandlers) != 0 {
		t.Errorf("want 0 MiddlewareHandlers (legacy path expected), got %d", len(h.MiddlewareHandlers))
	}
	if len(h.Implementations) != 1 {
		t.Fatalf("want 1 Implementation (legacy path), got %d", len(h.Implementations))
	}
	if got := h.Implementations[0].Satisfies; len(got) != 1 || got[0] != "bearerAuth" {
		t.Errorf("want Satisfies [bearerAuth], got %v", got)
	}
	_ = credCalled // dispatched by the adapter, not exercised at this layer
}

// httpRequestStub stands in for *http.Request in this test's credential fn
// signature — api/rest itself never imports net/http, and this test only
// needs a DISTINCT pointer type (not *mwTestReq) to exercise the
// isBoundHandleMWShape 2nd-param-type check realistically; the real
// shape's 2nd param is *http.Request, but any type distinct from *Req
// exercises the exact same branch.
type httpRequestStub struct{}

// TestClientMW_CodecBackedMiddleware_PopulatesSatisfiesAndSpec is
// [TestHandleMW_CodecBackedMiddleware_PopulatesSatisfiesAndSpec]'s
// SENDING-role mirror — ClientMW's bound path.
func TestClientMW_CodecBackedMiddleware_PopulatesSatisfiesAndSpec(t *testing.T) {
	mw := rest.NewMiddleware(newBoundSecureDeclaration("bound-bearer-client-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))

	h := rest.NewRoute[mwTestReq, userResp]("GET", "/bound-secure-client", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "boundSecureClient"},
	).ClientMW(mw, func(ctx context.Context, req mwTestReq) (mdTestIn, error) {
		return mdTestIn{Key: "secret"}, nil
	}).ClientHandle()

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
// REGRESSION GUARD for a confirmed, real bug this phase's own migration
// work caught: SecurityMiddleware[In,Out] generalized over In/Out
// (docs/roadmap/declarative-middleware-layering.md's Rollout Phase A) but
// left InCodec/OutCodec at their Go ZERO VALUE — a zero-value codex.Codec
// PANICS the first time Middleware dispatch calls InCodec.Validate (nil
// Encode/Decode funcs) for any NON-struct{} In, confirmed via an actual
// panic migrating examples/go-edge-models' BearerAuthDeclaration onto a
// real In type. Fixed: InCodec/OutCodec default to codex.Struct[In]()/
// codex.Struct[Out]() (a fieldless struct codec — confirmed safe no-op
// round-trip). This test exercises the FULL dispatch path (ClientMW's
// bound EncodeIn), not just construction, to catch the panic directly.
func TestSecurityMiddleware_RealInType_DoesNotPanicOnDispatch(t *testing.T) {
	mw := rest.SecurityMiddleware[credentialWithToken, struct{}]("bearerAuth",
		rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
	).WithRequestHeader(rest.NewOmitEmptyHeaderParam("Authorization", codex.String(),
		func(c credentialWithToken) string { return c.Token },
		func(c *credentialWithToken, v string) { c.Token = v },
	))

	h := rest.NewRoute[mwTestReq, userResp]("GET", "/sec-real-in", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "secRealIn"},
	).ClientMW(mw, func(ctx context.Context, req mwTestReq) (credentialWithToken, error) {
		return credentialWithToken{Token: "abc123"}, nil
	}).ClientHandle()

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
