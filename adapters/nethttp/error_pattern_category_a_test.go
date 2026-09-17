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
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// This file tests Topic 1's Category A full enumeration fix (see
// docs/design/d-0005-error-handling.md): every failure
// point on an already-dispatched request is now ErrorPattern-eligible,
// not just the handler/middleware-Fn rows shipped previously. Each row
// gets a matched pair (declared ErrorPattern fires, typed response) and,
// where not already covered by an existing regression test elsewhere,
// an unmatched pair (no declared pattern, today's fixed shape unchanged).
//
// Query/cookie/header param validation are deliberately NOT re-tested
// here — they dispatch through the IDENTICAL tryRespondErrorPattern call
// path as path param validation (verified via source review, not a
// bespoke per-kind branch), so path param's pair is representative.

// ── Row 1: request body decode ────────────────────────────────────────────

func TestErrorPattern_MatchesCodexErrorType(t *testing.T) {
	// Proves Category B's "zero new codex awareness needed" claim
	// end-to-end: a declared rest.ErrorPattern[codex.ValidationErrors, Body]
	// matches a REAL body-decode failure, with no special-casing anywhere.
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/body-decode-matched",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[codex.ValidationErrors, codexErrBody](http.StatusUnprocessableEntity, codexErrBodyCodec,
			func(e codex.ValidationErrors) (codexErrBody, error) {
				return codexErrBody{Count: len(e)}, nil
			}),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/body-decode-matched", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestErrorPattern_BodyDecode_NoPattern_FallsBackUnchanged(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/body-decode-unmatched",
		createReqCodec, userRespCodec,
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/body-decode-unmatched", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (unchanged fixed shape), got %d: %s", rec.Code, rec.Body.String())
	}
}

type codexErrBody struct{ Count int }

var codexErrBodyCodec = codex.Struct[codexErrBody](
	codex.RequiredField("count", codex.Int(),
		func(b codexErrBody) int { return b.Count },
		func(b *codexErrBody, v int) { b.Count = v },
	),
)

// ── Row 2 (representative of 2-5): path param validation ─────────────────

func TestErrorPattern_PathParam_Matched_RespondsTyped(t *testing.T) {
	type getReq struct{ ID string }
	getReqCodec := codex.Struct[getReq]()
	route := rest.NewRoute[getReq, userResp]("GET", "/errors/path-matched/{id}",
		getReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.UUID),
			func(r getReq) string { return r.ID },
			func(r *getReq, v string) { r.ID = v }),
		rest.ErrorPattern[rest.PathParamError, codexErrBody](http.StatusUnprocessableEntity, codexErrBodyCodec,
			func(e rest.PathParamError) (codexErrBody, error) { return codexErrBody{Count: 1}, nil }),
	).WithHandler(func(_ context.Context, r getReq) (userResp, error) {
		return userResp{ID: r.ID}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/errors/path-matched/not-a-uuid", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestErrorPattern_PathParam_NoPattern_FallsBackUnchanged(t *testing.T) {
	type getReq struct{ ID string }
	getReqCodec := codex.Struct[getReq]()
	route := rest.NewRoute[getReq, userResp]("GET", "/errors/path-unmatched/{id}",
		getReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.UUID),
			func(r getReq) string { return r.ID },
			func(r *getReq, v string) { r.ID = v }),
	).WithHandler(func(_ context.Context, r getReq) (userResp, error) {
		return userResp{ID: r.ID}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/errors/path-unmatched/not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (unchanged), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Security middleware Fn error ──────────────────────────────────────────

type secRejectedError struct{ Reason string }

func (e secRejectedError) Error() string { return "security rejected: " + e.Reason }

var secRejectedCodec = codex.Struct[secRejectedError](
	codex.RequiredField("reason", codex.String(),
		func(e secRejectedError) string { return e.Reason },
		func(e *secRejectedError, v string) { e.Reason = v },
	),
)

func newSecMiddlewareRoute(pattern ...rest.RouteOpt) (rest.Route[createReq, userResp], *rest.Server) {
	b := rest.NewServer(testInfo)
	opts := append([]rest.RouteOpt{
		rest.RouteMeta{OperationID: "createUserSecured"},
		rest.WithMiddleware(rest.FromSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)),
	}, pattern...)
	r := rest.NewRoute[createReq, userResp]("POST", "/errors/security-mw", createReqCodec, userRespCodec, opts...)
	return r, b
}

func TestErrorPattern_SecurityMiddlewareFn_Matched_RespondsTyped(t *testing.T) {
	r, b := newSecMiddlewareRoute(
		rest.ErrorPattern[secRejectedError, secRejectedError](http.StatusForbidden, secRejectedCodec),
	)
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, secRejectedError{Reason: "bad token"}
		},
	)
	r = r.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(&declMw, implMw.Fn)
	mux := mustServe(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/errors/security-mw", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestErrorPattern_SecurityMiddlewareFn_NoPattern_FallsBackUnchanged(t *testing.T) {
	r, b := newSecMiddlewareRoute()
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, secRejectedError{Reason: "bad token"}
		},
	)
	r = r.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(&declMw, implMw.Fn)
	mux := mustServe(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/errors/security-mw", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 (unchanged SecurityError fallback), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Middleware DecodeIn failure ────────────────────────────────────────────

// newDecodeInFailingMiddleware declares NO request header/cookie/query
// param source for tdIn.Key at all — so buildDecodeIn falls through to
// mw.InCodec.Validate(in) on the ZERO-VALUE in (Key==""), which fails
// tdInCodec's own required-non-empty constraint. This isolates a GENUINE
// middleware DecodeIn failure (wrapped in rest.MiddlewareInputError) from
// the route-level ValidateHeaders step entirely — declaring
// .WithRequestHeader here would instead register a route-level header
// param contribution, whose OWN validation runs BEFORE middleware
// dispatch and would intercept a missing-header case first.
func newDecodeInFailingMiddleware() rest.Middleware[tdIn, tdOut] {
	return rest.NewMiddleware(newTDDeclaration("api-key-policy"))
}

func TestErrorPattern_MiddlewareDecodeIn_Matched_RespondsTyped(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/mw-decode-in-matched",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.ErrorPattern[rest.MiddlewareInputError, codexErrBody](http.StatusUnprocessableEntity, codexErrBodyCodec,
			func(e rest.MiddlewareInputError) (codexErrBody, error) { return codexErrBody{Count: 1}, nil }),
	)
	route = rest.Transform(route, newDecodeInFailingMiddleware(), func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: "ok"}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/mw-decode-in-matched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestErrorPattern_MiddlewareDecodeIn_NoPattern_FallsBackUnchanged(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/mw-decode-in-unmatched",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, newDecodeInFailingMiddleware(), func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: "ok"}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/mw-decode-in-unmatched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (unchanged), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Row 9: response header/cookie merge-field encode failure ─────────────

func TestErrorPattern_ResponseMergeFieldEncode_Matched_RespondsTyped(t *testing.T) {
	// Uses an OPTIONAL response header (not required) so that
	// writeErrorPatternResponseReflect's own unconditional
	// ValidateResponseHeaders check (which runs for ANY response it
	// writes, matched or not) doesn't itself fail — a REQUIRED header
	// whose own merge-field encode is what failed can never be
	// satisfied by definition, which would be a self-defeating test
	// scenario, not a bug.
	route := rest.NewRoute[createReq, userRespWithMeta]("POST", "/errors/resp-merge-matched",
		createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOptionalResponseHeaderParam("X-Session", codex.String().Refine(validate.UUID),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
		rest.ErrorPattern[codex.ValidationErrors, codexErrBody](http.StatusUnprocessableEntity, codexErrBodyCodec,
			func(e codex.ValidationErrors) (codexErrBody, error) { return codexErrBody{Count: len(e)}, nil }),
	).WithHandler(func(_ context.Context, req createReq) (userRespWithMeta, error) {
		return userRespWithMeta{ID: "1", Name: req.Name, Session: "not-a-uuid"}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/resp-merge-matched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
}

// Row 9's unmatched/regression-guard case is already covered by the
// pre-existing TestHandler_ResponseMergeFields_CodecViolationReturns500.

// ── Row 10: middleware EncodeOut failure ──────────────────────────────────

func newEncodeOutTestMiddleware() rest.Middleware[tdIn, tdOut] {
	// WithRequestHeader ensures DecodeIn succeeds (tdIn.Key is populated
	// from a real request header) so the deliberate failure below is
	// ISOLATED to EncodeOut, not accidentally a DecodeIn failure.
	// WithResponseHeader uses OPTIONAL (not required) — see the
	// merge-field encode test above for why: a REQUIRED response header
	// whose own encode is what failed can never be satisfied, which
	// would make writeErrorPatternResponseReflect's own unconditional
	// ValidateResponseHeaders check fail — a self-defeating scenario,
	// not a bug.
	return rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String().Refine(validate.NonEmptyString),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewOptionalResponseHeaderParam("X-Policy-Applied", codex.String().Refine(validate.UUID),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
}

func TestErrorPattern_MiddlewareEncodeOut_Matched_RespondsTyped(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/mw-encode-out-matched",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.ErrorPattern[rest.MiddlewareOutputError, codexErrBody](http.StatusUnprocessableEntity, codexErrBodyCodec,
			func(e rest.MiddlewareOutputError) (codexErrBody, error) { return codexErrBody{Count: 1}, nil }),
	)
	route = rest.Transform(route, newEncodeOutTestMiddleware(), func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: "not-a-uuid"}, nil // deliberately fails the UUID refine on EncodeOut
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/mw-encode-out-matched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", "some-key")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestErrorPattern_MiddlewareEncodeOut_NoPattern_FallsBackUnchanged(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/mw-encode-out-unmatched",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	route = rest.Transform(route, newEncodeOutTestMiddleware(), func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: "not-a-uuid"}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/mw-encode-out-unmatched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", "some-key")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 (unchanged), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Row 11: response body encode (Marshal) failure ────────────────────────

type strictResp struct{ Value string }

var strictRespCodec = codex.Struct[strictResp](
	codex.RequiredField("value", codex.String().Refine(validate.UUID),
		func(r strictResp) string { return r.Value },
		func(r *strictResp, v string) { r.Value = v },
	),
)

func TestErrorPattern_ResponseBodyEncode_Matched_RespondsTyped(t *testing.T) {
	route := rest.NewRoute[createReq, strictResp]("POST", "/errors/resp-body-encode-matched",
		createReqCodec, strictRespCodec,
		rest.ErrorPattern[codex.ValidationErrors, codexErrBody](http.StatusUnprocessableEntity, codexErrBodyCodec,
			func(e codex.ValidationErrors) (codexErrBody, error) { return codexErrBody{Count: len(e)}, nil }),
	).WithHandler(func(_ context.Context, req createReq) (strictResp, error) {
		return strictResp{Value: "not-a-uuid"}, nil // fails Marshal-time Refine
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/resp-body-encode-matched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 (ErrorPattern status), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestErrorPattern_ResponseBodyEncode_NoPattern_FallsBackUnchanged(t *testing.T) {
	route := rest.NewRoute[createReq, strictResp]("POST", "/errors/resp-body-encode-unmatched",
		createReqCodec, strictRespCodec,
	).WithHandler(func(_ context.Context, req createReq) (strictResp, error) {
		return strictResp{Value: "not-a-uuid"}, nil
	})
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/errors/resp-body-encode-unmatched", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 (unchanged), got %d: %s", rec.Code, rec.Body.String())
	}
}
