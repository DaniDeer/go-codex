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

type mwTestReq struct{ ID string }

var mwTestReqCodec = codex.Struct[mwTestReq](
	codex.OptionalField("id", codex.String(),
		func(r mwTestReq) string { return r.ID },
		func(r *mwTestReq, v string) { r.ID = v },
	),
)

func requireScopesMW(name string, scopes []string) middleware.Middleware {
	return middleware.RequireScopes[*struct{}, mwTestReq](name, route.BearerScheme("JWT"), scopes, nil,
		func(ctx context.Context, raw *struct{}, req *mwTestReq) (map[string][]string, error) {
			return map[string][]string{name: scopes}, nil
		},
	)
}

// ── WithMiddleware: basic wiring ─────────────────────────────────────────────

func TestWithMiddleware_PopulatesHandleAndSpec(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	mw := requireScopesMW("bearerAuth", []string{"read"})

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
		rest.WithMiddleware(mw),
	).Register(b)
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
	b := rest.NewBuilder(testInfo)
	mw := middleware.Middleware{Name: "logging"} // no Security, no params

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/plain", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
	).Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.Descriptor.Security) != 0 {
		t.Errorf("want no Security entries for a non-security middleware, got %+v", h.Descriptor.Security)
	}
}

// ── Two ANDed security schemes from two attached middlewares (L4's scenario) ─

func TestWithMiddleware_TwoSchemesANDCombined(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	mwA := requireScopesMW("bearerAuth", nil)
	mwB := requireScopesMW("apiKey", nil)

	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/both", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	).Register(b)
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
	b := rest.NewBuilder(testInfo)
	mwA := requireScopesMW("oauth2", []string{"profile:read"})
	mwB := requireScopesMW("oauth2", []string{"profile:read", "profile:admin"})

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	).Register(b)

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
	b := rest.NewBuilder(testInfo)
	mw := requireScopesMW("oauth2", []string{"profile:read"})
	// Two DIFFERENT Middleware values, but IDENTICAL scheme+scopes.
	mwDup := requireScopesMW("oauth2", []string{"profile:read"})
	mwDup.Name = "different-name-same-declaration"

	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
		rest.WithMiddleware(mwDup),
	).Register(b)
	if err != nil {
		t.Fatalf("want identical redundant declarations to be allowed silently, got %v", err)
	}
}

// ── Drift-closing validation: manual Security with no attached middleware ───

func TestRegister_MissingSecurityMiddleware(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/secure", mwTestReqCodec, userCodec,
		rest.RouteMeta{Security: []route.SecurityRequirement{{"bearerAuth": nil}}},
		// No WithMiddleware attached at all — the original SecurityFunc bug.
	).Register(b)

	var missingErr rest.MissingSecurityMiddlewareError
	if !errors.As(err, &missingErr) {
		t.Fatalf("want MissingSecurityMiddlewareError, got %T: %v", err, err)
	}
	if missingErr.Scheme != "bearerAuth" {
		t.Errorf("unexpected scheme: %q", missingErr.Scheme)
	}
	if missingErr.LogValue().String() == "" {
		t.Error("want non-empty LogValue")
	}
}

func TestRegister_ManualSecurityWithAttachedMiddlewareOK(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	mw := requireScopesMW("bearerAuth", nil)
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/secure", mwTestReqCodec, userCodec,
		rest.RouteMeta{Security: []route.SecurityRequirement{{"bearerAuth": nil}}},
		rest.WithMiddleware(mw),
	).Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── RequestParams/ResponseParams contribution ────────────────────────────────

func TestWithMiddleware_RequestParamsContribution(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	mw := middleware.Middleware{
		Name: "require-api-key",
		RequestParams: []any{
			rest.HeaderParam{Name: "X-API-Key", Required: true},
		},
	}
	h, err := rest.NewRoute[mwTestReq, userResp]("GET", "/keyed", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
	).Register(b)
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
	b := rest.NewBuilder(testInfo)
	mwA := middleware.Middleware{
		Name:          "mw-a",
		RequestParams: []any{rest.HeaderParam{Name: "X-Trace", Required: true}},
	}
	mwB := middleware.Middleware{
		Name:          "mw-b",
		RequestParams: []any{rest.CookieParam{Name: "X-Trace", Required: true}}, // DIFFERENT kind, same name
	}
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/traced", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mwA),
		rest.WithMiddleware(mwB),
	).Register(b)

	var conflictErr rest.ConflictingParamContributionError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("want ConflictingParamContributionError, got %T: %v", err, err)
	}
	if conflictErr.ParamName != "X-Trace" {
		t.Errorf("unexpected param name: %q", conflictErr.ParamName)
	}
}

func TestWithMiddleware_ParamContributionShapeError(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	mw := middleware.Middleware{
		Name:          "bad-shape",
		RequestParams: []any{"not-a-param"},
	}
	_, err := rest.NewRoute[mwTestReq, userResp]("GET", "/bad", mwTestReqCodec, userCodec,
		rest.WithMiddleware(mw),
	).Register(b)

	var shapeErr rest.ParamContributionShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want ParamContributionShapeError, got %T: %v", err, err)
	}
}

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

func TestValidateRoute_CatchesMissingSecurityMiddleware(t *testing.T) {
	err := rest.ValidateRoute[mwTestReq, userResp](
		rest.RouteMeta{Security: []route.SecurityRequirement{{"bearerAuth": nil}}},
	)
	var missingErr rest.MissingSecurityMiddlewareError
	if !errors.As(err, &missingErr) {
		t.Fatalf("want MissingSecurityMiddlewareError, got %T: %v", err, err)
	}
}

// ── Route.Use / SSERoute.Use: chi-style chaining sugar ──────────────────────

func TestRouteUse_EquivalentToWithMiddleware(t *testing.T) {
	mw := requireScopesMW("bearerAuth", []string{"read"})

	viaUse, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(mw).Register(rest.NewBuilder(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	viaOpt, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
		rest.WithMiddleware(mw),
	).Register(rest.NewBuilder(testInfo))
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
	obsMw := middleware.Middleware{Name: "observability", Fn: nil}

	chained, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(mw1).Use(obsMw).Register(rest.NewBuilder(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	variadic, err := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	).Use(mw1, obsMw).Register(rest.NewBuilder(testInfo))
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

	handleA, err := branchA.Register(rest.NewBuilder(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	handleB, err := branchB.Register(rest.NewBuilder(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baseHandle, err := base.Register(rest.NewBuilder(testInfo))
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
	obsMw := middleware.Middleware{Name: "observability", Fn: nil}

	viaUse, err := rest.NewSSERoute[struct{}, userResp]("/stream", codex.Empty, userCodec,
		rest.RouteMeta{OperationID: "streamUsers"},
	).Use(obsMw).Register(rest.NewBuilder(testInfo))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(viaUse.Middlewares) != 1 || viaUse.Middlewares[0].Name != obsMw.Name {
		t.Fatalf("want Use to carry the attached middleware, got %+v", viaUse.Middlewares)
	}
}
