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
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type tdIn struct{ Key string }

var tdInCodec = codex.Struct[tdIn](
	codex.RequiredField("key", codex.String().Refine(validate.NonEmptyString),
		func(in tdIn) string { return in.Key },
		func(in *tdIn, v string) { in.Key = v },
	),
)

type tdOut struct{ Value string }

var tdOutCodec = codex.Struct[tdOut](
	codex.RequiredField("value", codex.String().Refine(validate.NonEmptyString),
		func(out tdOut) string { return out.Value },
		func(out *tdOut, v string) { out.Value = v },
	),
)

func newTDDeclaration(name string) middleware.Declaration[tdIn, tdOut] {
	return middleware.NewDeclaration(name, tdInCodec, tdOutCodec)
}

// ── Transform: happy path, enrichment, response-header composition ──────

func TestTransform_HappyPath_EnrichesReqAndSetsResponseHeader(t *testing.T) {
	var receivedReqName string
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Applied", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		req.Name = req.Name + "-enriched" // enrichment visible to the handler
		return tdOut{Value: "applied:" + in.Key}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		receivedReqName = req.Name
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", "secret123")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if receivedReqName != "Alice-enriched" {
		t.Errorf("want handler to see enriched req, got %q", receivedReqName)
	}
	if got := rec.Header().Get("X-Policy-Applied"); got != "applied:secret123" {
		t.Errorf("want X-Policy-Applied %q, got %q", "applied:secret123", got)
	}
}

// ── Transform: In-decode failure short-circuits with 400 ────────────────

func TestTransform_InDecodeFailure_Returns400(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	handlerCalled := false
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: in.Key}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		handlerCalled = true
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	// X-Api-Key deliberately omitted — required header missing.
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if handlerCalled {
		t.Error("want handler NOT called when middleware In-decode fails")
	}
}

// ── Transform: fn error falls back to rest.MiddlewareError (no ErrorPattern) ──

func TestTransform_FnError_FallsBackToMiddlewareError(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	handlerCalled := false
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{}, errBadAPIKey
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		handlerCalled = true
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", "bad-key")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (rest.MiddlewareError default status), got %d: %s", rec.Code, rec.Body.String())
	}
	if handlerCalled {
		t.Error("want handler NOT called when middleware fn returns an error")
	}
}

type badAPIKeyError struct{}

func (badAPIKeyError) Error() string { return "bad api key" }

var errBadAPIKey = badAPIKeyError{}

// ── D2: a middleware fn error matching a declared ErrorPattern produces
// the PATTERN's structured response, not the generic MiddlewareError fallback ──

type invalidAPIKeyError struct{ Reason string }

func (e invalidAPIKeyError) Error() string { return "invalid api key: " + e.Reason }

var invalidAPIKeyCodec = codex.Struct[invalidAPIKeyError](
	codex.RequiredField("reason", codex.String(),
		func(e invalidAPIKeyError) string { return e.Reason },
		func(e *invalidAPIKeyError, v string) { e.Reason = v },
	),
)

func TestTransform_FnError_MatchingErrorPattern_UsesPatternResponse(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.ErrorPattern[invalidAPIKeyError, invalidAPIKeyError](422, invalidAPIKeyCodec),
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{}, invalidAPIKeyError{Reason: "too short"}
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", "short")
	h.ServeHTTP(rec, r)

	if rec.Code != 422 {
		t.Fatalf("want 422 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too short") {
		t.Errorf("want ErrorPattern-encoded body containing %q, got %s", "too short", rec.Body.String())
	}
}

// ── D5: stats.ReportErrors called with "middleware:in"/"middleware:fn" ──

type spyValidationObserver struct {
	stats.NoopObserver
	locations []string
}

func (s *spyValidationObserver) RecordValidationError(location, constraintName, field string) {
	s.locations = append(s.locations, location)
}

func TestTransform_InDecodeFailure_ReportsMiddlewareInLocation(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: in.Key}, nil
	})
	spy := &spyValidationObserver{}
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	// adapters/chi has no exported Observability(obs) wrapper (unlike
	// adapters/nethttp) — decorate ctx with stats.WithDiagnostics/
	// stats.WithObserver directly and drain manually after ServeHTTP
	// returns, to exercise the SAME rest.DiagnosticObserver{Ctx: ctx}
	// call this package's runMiddlewareHandlersReflect already makes.
	ctx := stats.WithDiagnostics(context.Background())
	ctx = stats.WithObserver(ctx, spy)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	// Header PRESENT (satisfies the route's own layered required-header
	// check, which only validates codex.String()'s plain presence/shape)
	// but EMPTY — fails ONLY the middleware's OWN, stricter InCodec
	// (NonEmptyString) at DecodeIn time, isolating the "middleware:in"
	// location from the generic route-level "header" location.
	r.Header.Set("X-Api-Key", "")
	h.ServeHTTP(rec, r)
	for _, d := range stats.DiagnosticsFromContext(ctx) {
		spy.RecordValidationError(d.Location, d.ConstraintName, d.Field)
	}

	found := false
	for _, loc := range spy.locations {
		if loc == "middleware:in" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:in", spy.locations)
	}
}

// ── D6(c): two middlewares both writing the SAME *Req field — attachment-
// order, last-applied-wins, not flagged as a conflict ──

// tdEmpty is an In/Out shape with NO required fields — used where a test
// needs a Transform-attached middleware whose fn ignores In/Out entirely
// (only enrichment matters), so DecodeIn's InCodec.Validate never fails on
// a zero value.
type tdEmpty struct{}

var tdEmptyCodec = codex.Struct[tdEmpty]()

func newTDEmptyDeclaration(name string) middleware.Declaration[tdEmpty, tdEmpty] {
	return middleware.NewDeclaration(name, tdEmptyCodec, tdEmptyCodec)
}

func TestTransform_TwoMiddlewaresEnrichSameField_LastAttachedWins(t *testing.T) {
	mwFirst := rest.NewMiddleware(newTDEmptyDeclaration("first-policy"))
	mwSecond := rest.NewMiddleware(newTDEmptyDeclaration("second-policy"))

	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, mwFirst, func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
		req.Name = "first"
		return tdEmpty{}, nil
	})
	route = rest.Transform(route, mwSecond, func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
		req.Name = "second"
		return tdEmpty{}, nil
	})

	var receivedName string
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		receivedName = req.Name
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
	if receivedName != "second" {
		t.Errorf("want last-attached middleware's write to win (%q), got %q", "second", receivedName)
	}
}

// ── Route-agnostic .Use(mw) dispatch: reused across two different routes ──

func TestUse_AgnosticMiddleware_DispatchesOnBothRoutes(t *testing.T) {
	callCount := 0
	mw := rest.NewMiddleware(newTDDeclaration("agnostic-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithReceive(func(ctx context.Context, in tdIn) (tdOut, error) {
			callCount++
			return tdOut{Value: in.Key}, nil
		})

	routeA := rest.NewRoute[createReq, userResp]("POST", "/a", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "a"},
	).Use(mw).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	routeB := rest.NewRoute[getReq, userResp]("GET", "/b", getReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "b"},
	).Use(mw).WithHandler(func(_ context.Context, req getReq) (userResp, error) {
		return userResp{ID: "2", Name: "B"}, nil
	})

	hA := mustServeOne(t, routeA)
	hB := mustServeOne(t, routeB)

	recA := httptest.NewRecorder()
	rA := httptest.NewRequest(http.MethodPost, "/a", strings.NewReader(`{"name":"Alice"}`))
	rA.Header.Set("Content-Type", "application/json")
	rA.Header.Set("X-Api-Key", "key-a")
	hA.ServeHTTP(recA, rA)
	if recA.Code != http.StatusCreated {
		t.Fatalf("route A: want 201, got %d: %s", recA.Code, recA.Body.String())
	}

	recB := httptest.NewRecorder()
	rB := httptest.NewRequest(http.MethodGet, "/b", nil)
	rB.Header.Set("X-Api-Key", "key-b")
	hB.ServeHTTP(recB, rB)
	if recB.Code != http.StatusOK {
		t.Fatalf("route B: want 200, got %d: %s", recB.Code, recB.Body.String())
	}

	if callCount != 2 {
		t.Errorf("want mw's bundled receiveFn called once per route (2 total), got %d", callCount)
	}
}
