package rest_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

type mwTestReq struct{ ID string }

var mwTestReqCodec = codex.Struct[mwTestReq](
	codex.OptionalField("id", codex.String(),
		func(r mwTestReq) string { return r.ID },
		func(r *mwTestReq, v string) { r.ID = v },
	),
)

// requireScopesMW builds a declare-time-only Middleware (no runtime
// Fn/Satisfies — those live on a separately-supplied
// middleware.ServerImplementation, attached at adapter Register/Handler
// time, not here). Tests in this file only exercise Route.RegisterHandle(builder)/
// ValidateRoute, which never consult an implementation.
func requireScopesMW(name string, scopes []string) middleware.Middleware {
	return middleware.SecurityScheme(name, route.BearerScheme("JWT"), scopes, nil)
}

// ── WithMiddleware: basic wiring ─────────────────────────────────────────────

func TestWithMiddleware_PopulatesHandleAndSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	mw := requireScopesMW("bearerAuth", []string{"read"})

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
		rest.WithMiddleware(mw),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.Middlewares) != 1 || h.Middlewares[0].Name != mw.Name {
		t.Fatalf("want Middlewares to carry the attached middleware, got %+v", h.Middlewares)
	}
	if len(h.Descriptor.Security) != 1 || h.Descriptor.Security[0]["bearerAuth"] == nil {
		t.Fatalf("want the route's spec Security to include bearerAuth, got %+v", h.Descriptor.Security)
	}
	if _, ok := h.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatalf("want SecuritySchemes to include bearerAuth, got %+v", h.SecuritySchemes)
	}
}

func TestWithMiddleware_NoSpecEffectForNonSecurityMiddleware(t *testing.T) {
	b := rest.NewServer(testInfo)
	mw := middleware.Middleware{Name: "logging"} // no Security, no params

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/plain", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.Descriptor.Security) != 0 {
		t.Errorf("want no Security entries for a non-security middleware, got %+v", h.Descriptor.Security)
	}
}

// ── Two ANDed security schemes from two attached middlewares (L4's scenario) ─

func TestWithMiddleware_TwoSchemesANDCombined(t *testing.T) {
	b := rest.NewServer(testInfo)
	mwA := requireScopesMW("bearerAuth", nil)
	mwB := requireScopesMW("apiKey", nil)

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/both", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.Descriptor.Security) != 1 {
		t.Fatalf("want ONE combined (AND) requirement, got %d entries: %+v", len(h.Descriptor.Security), h.Descriptor.Security)
	}
	req := h.Descriptor.Security[0]
	if _, ok := req["bearerAuth"]; !ok {
		t.Error("want bearerAuth in the combined requirement")
	}
	if _, ok := req["apiKey"]; !ok {
		t.Error("want apiKey in the combined requirement")
	}
}

// ── L10's worked example: conflicting security declarations actually firing ──

func TestWithMiddleware_ConflictingSecurityDeclarations(t *testing.T) {
	b := rest.NewServer(testInfo)
	mwA := requireScopesMW("oauth2", []string{"profile:read"})
	mwB := requireScopesMW("oauth2", []string{"profile:read", "profile:admin"})

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	).RegisterHandle(b)

	var conflictErr rest.ConflictingSecurityDeclarationError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingSecurityDeclarationError, got %T: %v", err, err)
	}
	if conflictErr.Scheme != "oauth2" {
		t.Errorf("unexpected scheme: %q", conflictErr.Scheme)
	}
	if conflictErr.FirstSource != mwA.Name || conflictErr.SecondSource != mwB.Name {
		t.Errorf("unexpected sources: %q vs %q", conflictErr.FirstSource, conflictErr.SecondSource)
	}
	if conflictErr.LogValue().String() == "" {
		t.Error("want non-empty LogValue")
	}
}

func TestWithMiddleware_IdenticalDeclarationsAllowedSilently(t *testing.T) {
	b := rest.NewServer(testInfo)
	mw := requireScopesMW("oauth2", []string{"profile:read"})
	// Two DIFFERENT Middleware values, but IDENTICAL scheme+scopes.
	mwDup := requireScopesMW("oauth2", []string{"profile:read"})
	mwDup.Name = "different-name-same-declaration"

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
		rest.WithMiddleware(mwDup),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("want identical redundant declarations to be allowed silently, got %v", err)
	}
}

// ── Drift-closing validation: manual Security with no attached middleware ───

func TestRegister_ManualSecurityWithNoMiddlewareNoLongerFailsAtRegister(t *testing.T) {
	// Post-Revision-2, the coverage check (that every declared scheme has an
	// enforcing ServerImplementation) has moved to adapter Register/Handler
	// time — see rest.CheckCoverage. Route.RegisterHandle(builder) never
	// sees any ServerImplementation values, so it correctly ALLOWS a route
	// declaring Security with no attached middleware at all; the adapter is
	// where the drift-closing check now happens.
	b := rest.NewServer(testInfo)
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/secure", mwTestReqCodec, userCodec,
		rest.RouteMeta{Security: []route.SecurityRequirement{{"bearerAuth": nil}}},
		// No WithMiddleware attached at all.
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegister_ManualSecurityWithAttachedMiddlewareOK(t *testing.T) {
	b := rest.NewServer(testInfo)
	mw := requireScopesMW("bearerAuth", nil)
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/secure", mwTestReqCodec, userCodec,
		rest.RouteMeta{Security: []route.SecurityRequirement{{"bearerAuth": nil}}},
		rest.WithMiddleware(mw),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── RequestParams/ResponseParams contribution ────────────────────────────────

func TestWithMiddleware_RequestParamsContribution(t *testing.T) {
	b := rest.NewServer(testInfo)
	mw := middleware.Middleware{
		Name: "require-api-key",
		RequestHeaderParams: []middleware.HeaderParamSpec{
			{Name: "X-API-Key", Required: true},
		},
	}
	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/keyed", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.HeaderParams {
		if p.Name == "X-API-Key" {
			found = true
		}
	}
	if !found {
		t.Errorf("want X-API-Key header param in spec, got %+v", h.Descriptor.HeaderParams)
	}
}

func TestWithMiddleware_ConflictingParamContribution(t *testing.T) {
	b := rest.NewServer(testInfo)
	mwA := middleware.Middleware{
		Name:                "mw-a",
		RequestHeaderParams: []middleware.HeaderParamSpec{{Name: "X-Trace", Required: true}},
	}
	mwB := middleware.Middleware{
		Name:                "mw-b",
		RequestCookieParams: []middleware.CookieParamSpec{{Name: "X-Trace", Required: true}}, // DIFFERENT kind, same name
	}
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/traced", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	).RegisterHandle(b)

	var conflictErr rest.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError, got %T: %v", err, err)
	}
	if conflictErr.ParamName != "X-Trace" {
		t.Errorf("unexpected param name: %q", conflictErr.ParamName)
	}
}

// NOTE: TestWithMiddleware_ParamContributionShapeError was REMOVED —
// RequestParams/ResponseParams were generalized from type-erased []any to
// typed per-kind fields (RequestHeaderParams/RequestCookieParams/etc.), so
// a wrong-shape entry is now a Go COMPILE error, not a runtime
// rest.ParamContributionShapeError (removed along with the type-erased
// fields). See docs/design/d-0001-rest-middleware-workflow-simplification.md's
// "Decision: typed RequestParams/ResponseParams fields".

// ── ValidateRoute (L1/L8): full opts list, no live Builder needed ───────────

func TestValidateRoute_CatchesTheSameConflictAsRegister(t *testing.T) {
	mwA := requireScopesMW("oauth2", []string{"profile:read"})
	mwB := requireScopesMW("oauth2", []string{"profile:read", "profile:admin"})

	err := rest.ValidateRoute[mwTestReq, userResp](rest.RouteMeta{},
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	)
	var conflictErr rest.ConflictingSecurityDeclarationError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingSecurityDeclarationError, got %T: %v", err, err)
	}
}

func TestValidateRoute_CatchesManualVsMiddlewareConflict(t *testing.T) {
	// This is the gap L8 closed: a MANUAL RouteMeta.Security declaration
	// conflicting with a middleware's Security, NOT just middleware-vs-
	// middleware — ValidateRoute must see the FULL opts list, not just mws.
	mw := requireScopesMW("oauth2", []string{"profile:admin"})

	err := rest.ValidateRoute[mwTestReq, userResp](
		rest.RouteMeta{Security: []route.SecurityRequirement{{"oauth2": {"profile:read"}}}},
		rest.WithMiddleware(mw),
	)
	var conflictErr rest.ConflictingSecurityDeclarationError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingSecurityDeclarationError for manual-vs-middleware conflict, got %T: %v", err, err)
	}
	if conflictErr.FirstSource != "manual" {
		t.Errorf("want FirstSource=manual, got %q", conflictErr.FirstSource)
	}
}

func TestValidateRoute_NoErrorForValidDeclaration(t *testing.T) {
	mw := requireScopesMW("bearerAuth", nil)
	err := rest.ValidateRoute[mwTestReq, userResp](rest.RouteMeta{}, rest.WithMiddleware(mw))
	if err != nil {
		t.Errorf("want no error for a valid declaration, got %v", err)
	}
}

// ── ClientHandle picks up middleware-declared Security (server/client symmetry) ──

func TestClientHandle_PicksUpMiddlewareDeclaredSecurity(t *testing.T) {
	mw := requireScopesMW("bearerAuth", []string{"read"})
	handle := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
	).ClientHandle()

	if _, ok := handle.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatalf("want ClientHandle to populate SecuritySchemes from middleware, got %+v", handle.SecuritySchemes)
	}
	if len(handle.Descriptor.Security) != 1 || handle.Descriptor.Security[0]["bearerAuth"] == nil {
		t.Fatalf("want ClientHandle's Descriptor.Security to include bearerAuth, got %+v", handle.Descriptor.Security)
	}
}

func TestValidateRoute_NoLongerCatchesMissingSecurityMiddleware(t *testing.T) {
	// Same rationale as TestRegister_ManualSecurityWithNoMiddlewareNoLongerFailsAtRegister:
	// ValidateRoute runs the identical (coverage-free) validation Route.Register does.
	err := rest.ValidateRoute[mwTestReq, userResp](
		rest.RouteMeta{Security: []route.SecurityRequirement{{"bearerAuth": nil}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── Route.Use / SSERoute.Use: chi-style chaining sugar ──────────────────────

func TestRouteUse_EquivalentToWithMiddleware(t *testing.T) {
	mw := requireScopesMW("bearerAuth", []string{"read"})

	viaUse, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(mw).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	viaOpt, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
		rest.WithMiddleware(mw),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(viaUse.Middlewares) != 1 || viaUse.Middlewares[0].Name != mw.Name {
		t.Fatalf("want Use to carry the attached middleware, got %+v", viaUse.Middlewares)
	}
	if len(viaUse.Descriptor.Security) != len(viaOpt.Descriptor.Security) {
		t.Fatalf("want Use and WithMiddleware to produce the same Security, got %+v vs %+v",
			viaUse.Descriptor.Security, viaOpt.Descriptor.Security)
	}
	if _, ok := viaUse.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatalf("want Use to populate SecuritySchemes, got %+v", viaUse.SecuritySchemes)
	}
}

func TestRouteUse_ChainedCallsEquivalentToVariadic(t *testing.T) {
	mw1 := requireScopesMW("bearerAuth", []string{"read"})
	obsMw := middleware.Middleware{Name: "observability"}

	chained, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(mw1).Use(obsMw).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	variadic, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(mw1, obsMw).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(chained.Middlewares) != 2 || len(variadic.Middlewares) != 2 {
		t.Fatalf("want 2 middlewares each, got chained=%d variadic=%d", len(chained.Middlewares), len(variadic.Middlewares))
	}
	for i := range chained.Middlewares {
		if chained.Middlewares[i].Name != variadic.Middlewares[i].Name {
			t.Errorf("index %d: want same order/names, got %q vs %q",
				i, chained.Middlewares[i].Name, variadic.Middlewares[i].Name)
		}
	}
}

func TestRouteUse_DoesNotMutateBaseRoute(t *testing.T) {
	mw1 := requireScopesMW("bearerAuth", []string{"read"})
	mw2 := requireScopesMW("apiKey", []string{"write"})

	base := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	)

	branchA := base.Use(mw1)
	branchB := base.Use(mw2)

	handleA, err := branchA.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handleB, err := branchB.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baseHandle, err := base.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(handleA.Middlewares) != 1 || handleA.Middlewares[0].Name != mw1.Name {
		t.Fatalf("branchA: want only mw1, got %+v", handleA.Middlewares)
	}
	if len(handleB.Middlewares) != 1 || handleB.Middlewares[0].Name != mw2.Name {
		t.Fatalf("branchB: want only mw2, got %+v", handleB.Middlewares)
	}
	if len(baseHandle.Middlewares) != 0 {
		t.Fatalf("base: want zero middlewares (unaffected by branchA/branchB), got %+v", baseHandle.Middlewares)
	}
}

func TestSSERouteUse_EquivalentToWithMiddleware(t *testing.T) {
	obsMw := middleware.Middleware{Name: "observability"}

	viaUse, err := rest.NewSSERoute[struct{}, userResp]("/stream", codex.Empty, userCodec,
		rest.RouteMeta{OperationID: "streamUsers"},
	).Use(obsMw).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(viaUse.Middlewares) != 1 || viaUse.Middlewares[0].Name != obsMw.Name {
		t.Fatalf("want Use to carry the attached middleware, got %+v", viaUse.Middlewares)
	}
}

// ── Route.ClientMW / ClientImplementations ───────────────────────────────────

func TestRouteClientMW_PopulatesClientImplementationsOnRegister(t *testing.T) {
	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).ClientMW(nil, func() {}).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.ClientImplementations) != 1 {
		t.Fatalf("want ClientImplementations to carry the attached implementation, got %+v", h.ClientImplementations)
	}
	// ClientMW(nil, ...) must NEVER affect the spec.
	if len(h.Descriptor.Security) != 0 {
		t.Errorf("want no Security contributed by ClientMW(nil, ...), got %+v", h.Descriptor.Security)
	}
}

func TestRouteClientMW_PopulatesClientImplementationsOnClientHandle(t *testing.T) {
	h := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).ClientMW(nil, func() {}).ClientHandle()

	if len(h.ClientImplementations) != 1 {
		t.Fatalf("want ClientImplementations to carry the attached implementation, got %+v", h.ClientImplementations)
	}
}

func TestRouteClientMW_CombinesWithServerDeclaredSecurity(t *testing.T) {
	scheme := route.BearerScheme("")
	declareMw := middleware.SecurityScheme("bearerAuth", scheme, nil, nil)

	h := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(declareMw).ClientMW(&declareMw, func() {}).ClientHandle()

	if _, ok := h.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatalf("want SecuritySchemes populated by the server-side Use(SecurityScheme(...)) declaration, got %+v", h.SecuritySchemes)
	}
	if len(h.ClientImplementations) != 1 {
		t.Fatalf("want ClientImplementations populated by ClientMW, got %+v", h.ClientImplementations)
	}
	if len(h.ClientImplementations[0].Satisfies) != 1 || h.ClientImplementations[0].Satisfies[0] != "bearerAuth" {
		t.Fatalf("want ClientMW to derive Satisfies from the paired mw, got %+v", h.ClientImplementations[0].Satisfies)
	}
}

// ── SSERoute.ClientMW / ClientHandle (mirrors Route.ClientMW/ClientHandle) ──

// TestSSERouteClientMW_PopulatesClientImplementationsOnClientHandle is C1's
// happy path: SSERoute.ClientMW attaches a credential-providing
// implementation that SSERoute.ClientHandle carries through — mirrors
// TestRouteClientMW_PopulatesClientImplementationsOnClientHandle exactly.
func TestSSERouteClientMW_PopulatesClientImplementationsOnClientHandle(t *testing.T) {
	h := rest.NewSSERoute[struct{}, userResp]("/stream", codex.Empty, userCodec,
		rest.RouteMeta{OperationID: "streamUsers"},
	).ClientMW(nil, func() {}).ClientHandle()

	if len(h.ClientImplementations) != 1 {
		t.Fatalf("want ClientImplementations to carry the attached implementation, got %+v", h.ClientImplementations)
	}
}

// TestSSERouteClientMW_SatisfiesGating is C2: a ClientMW paired against a
// scheme the route did NOT declare via Use must still carry a Satisfies
// value — mirrors nethttp's TestCall_ClientMWSatisfiesGating_UnrelatedImplNotRun
// rationale at the api/rest layer (the actual "does NOT run" behavior is
// nethttp.Consume's job to enforce at call time; this test only confirms
// SSERoute.ClientHandle correctly derives and carries Satisfies).
func TestSSERouteClientMW_SatisfiesGating(t *testing.T) {
	declareMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme(""), nil, nil)
	otherMw := middleware.SecurityScheme("apiKey", route.APIKeyScheme("X-API-Key", "header"), nil, nil)

	h := rest.NewSSERoute[struct{}, userResp]("/stream", codex.Empty, userCodec,
		rest.RouteMeta{OperationID: "streamUsers"},
	).Use(declareMw).
		ClientMW(&declareMw, func() {}).
		ClientMW(&otherMw, func() {}).
		ClientHandle()

	if len(h.ClientImplementations) != 2 {
		t.Fatalf("want both ClientMW attachments carried, got %+v", h.ClientImplementations)
	}
	if len(h.ClientImplementations[0].Satisfies) != 1 || h.ClientImplementations[0].Satisfies[0] != "bearerAuth" {
		t.Fatalf("want first ClientMW's Satisfies to be [bearerAuth], got %+v", h.ClientImplementations[0].Satisfies)
	}
	if len(h.ClientImplementations[1].Satisfies) != 1 || h.ClientImplementations[1].Satisfies[0] != "apiKey" {
		t.Fatalf("want second ClientMW's Satisfies to be [apiKey], got %+v", h.ClientImplementations[1].Satisfies)
	}
}

func TestSecurityScheme_AloneIsALegitimateIntermediateStateAtRegister(t *testing.T) {
	// A route using ONLY middleware.SecurityScheme (no ServerImplementation
	// supplied anywhere yet) is a legitimate, intermediate declare-time
	// state — Route.RegisterHandle(builder) no longer checks coverage (that check
	// is deliberately deferred to adapter Register/Handler time, once a
	// []middleware.ServerImplementation has ALSO been supplied — see
	// rest.CheckCoverage and TestSecurityScheme_AloneFailsAdapterCoverageCheck
	// in adapters/nethttp).
	declareMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme(""), nil, nil)

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(declareMw).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── UnknownMiddlewareImplementationError (reverse-Satisfies check) ──────────

func TestRoute_HandleMW_PairedAgainstUndeclaredScheme_ReturnsUnknownMiddlewareImplementationError(t *testing.T) {
	// declMw declares "bearerAuth" but the route below pairs its HandleMW
	// implementation against a DIFFERENT scheme name ("otherAuth") that was
	// never .Use()'d on this route — a copy-paste mistake reusing a
	// different route's middleware.Middleware. checkImplementationsDeclared
	// must catch this at Register/RegisterHandle time, not silently accept it.
	declMw := requireScopesMW("bearerAuth", nil)
	mismatchedMw := requireScopesMW("otherAuth", nil)

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(declMw).HandleMW(&mismatchedMw, func(_ context.Context, _ *http.Request, _ *mwTestReq) (map[string][]string, error) {
		return nil, nil
	}).RegisterHandle(rest.NewServer(testInfo))

	var unknownErr rest.UnknownMiddlewareImplementationError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("want UnknownMiddlewareImplementationError, got %v (%T)", err, err)
	}
	if unknownErr.Scheme != "otherAuth" {
		t.Errorf("want Scheme %q, got %q", "otherAuth", unknownErr.Scheme)
	}
}

func TestRoute_HandleMW_PairedAgainstDeclaredScheme_NoError(t *testing.T) {
	// The mirror-image happy path: HandleMW paired against a scheme that
	// WAS .Use()'d on the same route must succeed.
	declMw := requireScopesMW("bearerAuth", nil)

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(declMw).HandleMW(&declMw, func(_ context.Context, _ *http.Request, _ *mwTestReq) (map[string][]string, error) {
		return nil, nil
	}).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSSERoute_HandleMW_PairedAgainstUndeclaredScheme_ReturnsUnknownMiddlewareImplementationError(t *testing.T) {
	declMw := requireScopesMW("bearerAuth", nil)
	mismatchedMw := requireScopesMW("otherAuth", nil)

	_, err := rest.NewSSERoute[mwTestReq, sseEvent]("/stream", mwTestReqCodec, sseEventCodec).Use(declMw).HandleMW(&mismatchedMw, func(_ context.Context, _ *http.Request, _ *mwTestReq) (map[string][]string, error) {
		return nil, nil
	}).RegisterHandle(rest.NewServer(testInfo))

	var unknownErr rest.UnknownMiddlewareImplementationError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("want UnknownMiddlewareImplementationError, got %v (%T)", err, err)
	}
	if unknownErr.Scheme != "otherAuth" {
		t.Errorf("want Scheme %q, got %q", "otherAuth", unknownErr.Scheme)
	}
}

func TestUnknownMiddlewareImplementationError_LogValue(t *testing.T) {
	e := rest.UnknownMiddlewareImplementationError{Route: "GET /profile", Scheme: "otherAuth"}
	if e.Error() == "" {
		t.Error("want non-empty Error() string")
	}
	v := e.LogValue()
	if v.Kind().String() != "Group" {
		t.Errorf("want LogValue() Kind Group, got %v", v.Kind())
	}
}

// ── FromHeaderParam/FromCookieParam/FromQueryParam/FromResponseHeaderParam/
//    FromResponseCookieParam (bridge an existing rest.XParam into a Middleware)

func TestFromHeaderParam_Register_ContributesHeaderParamToSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	c := codex.String().Refine(validate.NonEmptyString)
	apiKeyHeader := rest.HeaderParam{Name: "X-API-Key", Required: true}.WithCodec(c)

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/keyed", mwTestReqCodec, userCodec).Use(rest.FromHeaderParam(apiKeyHeader)).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.HeaderParams {
		if p.Name == "X-API-Key" {
			found = true
		}
	}
	if !found {
		t.Errorf("want X-API-Key header param in spec, got %+v", h.Descriptor.HeaderParams)
	}
	// Codec propagation proven via runtime validation: a non-empty-string
	// constraint fires against an empty header value only if the bridged
	// HeaderParam's Codec actually reached the handle.
	if err := h.ValidateHeaders(map[string]string{"X-API-Key": ""}); err == nil {
		t.Error("want validation error for empty X-API-Key (Codec not propagated?)")
	}
	if err := h.ValidateHeaders(map[string]string{"X-API-Key": "secret"}); err != nil {
		t.Errorf("want nil for valid X-API-Key, got %v", err)
	}
}

func TestFromCookieParam_Register_ContributesCookieParamToSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	sessionCookie := rest.CookieParam{Name: "session_token", Required: true}

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/session", mwTestReqCodec, userCodec).Use(rest.FromCookieParam(sessionCookie)).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.CookieParams {
		if p.Name == "session_token" {
			found = true
		}
	}
	if !found {
		t.Errorf("want session_token cookie param in spec, got %+v", h.Descriptor.CookieParams)
	}
}

func TestFromQueryParam_Register_ContributesQueryParamToSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	pageParam := rest.QueryParam{Name: "page"}

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/list", mwTestReqCodec, userCodec).Use(rest.FromQueryParam(pageParam)).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.QueryParams {
		if p.Name == "page" {
			found = true
		}
	}
	if !found {
		t.Errorf("want page query param in spec, got %+v", h.Descriptor.QueryParams)
	}
}

func TestFromResponseHeaderParam_Register_ContributesResponseHeaderParamToSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	locationHeader := rest.ResponseHeaderParam{Name: "Location", Required: true, Codec: &uuidCodec}

	h, err := rest.NewRoute[mwTestReq, userResp]("POST", "/created", mwTestReqCodec, userCodec).Use(rest.FromResponseHeaderParam(locationHeader)).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Required + missing surfaces an error ONLY if the param actually
	// attached — proves the middleware contribution reached the handle.
	if err := h.ValidateResponseHeaders(map[string]string{}); err == nil {
		t.Error("want error for missing required Location response header, got nil")
	}
	if err := h.ValidateResponseHeaders(map[string]string{"Location": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("want nil for valid Location header, got %v", err)
	}
}

func TestFromResponseCookieParam_Register_ContributesResponseCookieParamToSpec(t *testing.T) {
	b := rest.NewServer(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	sessionCookie := rest.ResponseCookieParam{Name: "session_token", Required: true, Codec: &uuidCodec}

	h, err := rest.NewRoute[mwTestReq, userResp]("POST", "/login", mwTestReqCodec, userCodec).Use(rest.FromResponseCookieParam(sessionCookie)).RegisterHandle(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := h.ValidateResponseCookies(map[string]string{}); err == nil {
		t.Error("want error for missing required session_token response cookie, got nil")
	}
	if err := h.ValidateResponseCookies(map[string]string{"session_token": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Errorf("want nil for valid session_token cookie, got %v", err)
	}
}

func TestFromHeaderParam_ConflictsWithManualDeclaration(t *testing.T) {
	b := rest.NewServer(testInfo)
	manualHeader := rest.HeaderParam{Name: "X-Trace", Required: false}
	bridgedHeader := rest.HeaderParam{Name: "X-Trace", Required: true} // different Required

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/traced2", mwTestReqCodec, userCodec,
		manualHeader,
	).Use(rest.FromHeaderParam(bridgedHeader)).RegisterHandle(b)

	var conflictErr rest.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError, got %T: %v", err, err)
	}
}
