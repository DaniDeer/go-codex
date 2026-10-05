package rest_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type mdTestIn struct {
	Key string
}

var mdTestInCodec = codex.Struct[mdTestIn](
	codex.RequiredField("key", codex.String().Refine(validate.NonEmptyString),
		func(in mdTestIn) string { return in.Key },
		func(in *mdTestIn, v string) { in.Key = v },
	),
)

type mdTestOut struct {
	Value string
}

var mdTestOutCodec = codex.Struct[mdTestOut](
	codex.RequiredField("value", codex.String().Refine(validate.NonEmptyString),
		func(out mdTestOut) string { return out.Value },
		func(out *mdTestOut, v string) { out.Value = v },
	),
)

func newTestDeclaration() middleware.Declaration[mdTestIn, mdTestOut] {
	return middleware.NewDeclaration("test-policy", mdTestInCodec, mdTestOutCodec)
}

// ── rest.Middleware[In,Out] construction ─────────────────────────────────

func TestNewMiddleware_BuildsExpectedShape(t *testing.T) {
	mw := rest.NewMiddleware(newTestDeclaration())
	if mw.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", mw.Name)
	}
	var _ middleware.RouteMiddleware = mw // compiles: RouteMiddlewareMarker is exported
	// End-to-end .Use(mw) dispatch is exercised by TestUse_AcceptsCodecBackedMiddleware.
}

func TestMiddleware_WithRequestHeader_PopulatesParam(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	)
	route = route.HandleBoundMW(bm)
	h, err := route.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.HeaderParams {
		if p.Name == "X-API-Key" {
			found = true
		}
	}
	if !found {
		t.Errorf("want X-API-Key header param layered into spec, got %+v", h.Descriptor.HeaderParams)
	}
}

func TestMiddleware_WithResponseCookie_PopulatesParam(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: "sess-123"}, nil
		}).
		WithResponseCookie(rest.NewRequiredResponseCookieParam("session", codex.String(),
			func(out mdTestOut) string { return out.Value },
			func(out *mdTestOut, v string) { out.Value = v },
		))
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/profile", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile"},
	)
	route = route.HandleBoundMW(bm)
	h, err := route.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	// Response cookies have no first-class OpenAPI representation — they
	// are emitted as a single generic "Set-Cookie" string header (same
	// convention as [rest.ResponseCookieParam] itself), so a layered
	// cookie surfaces as a "Set-Cookie" header entry, not one named
	// "session".
	found := false
	for _, p := range h.Descriptor.Responses[0].Headers {
		if p.Name == "Set-Cookie" {
			found = true
		}
	}
	if !found {
		t.Errorf("want Set-Cookie response header (from layered response cookie) in spec, got %+v", h.Descriptor.Responses[0].Headers)
	}
}

// TestMiddleware_WithResponseCookie_WithAttributes_ReachesMiddlewareHandler
// exercises the declarative cookie-attributes mechanism's Middleware[In,Out]
// integration: WithAttributes declared on a middleware's response cookie
// reaches the built MiddlewareHandler.EncodeOutCookieAttrs.
func TestMiddleware_WithResponseCookie_WithAttributes_ReachesMiddlewareHandler(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: "sess-123"}, nil
		}).
		WithResponseCookie(rest.NewRequiredResponseCookieParam("session", codex.String(),
			func(out mdTestOut) string { return out.Value },
			func(out *mdTestOut, v string) { out.Value = v },
		).WithAttributes(func(out mdTestOut) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 3600, Insecure: true}
		}))
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/profile2", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getProfile2"},
	)
	route = route.HandleBoundMW(bm)
	h, err := route.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Fatalf("want exactly one MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
	mh := h.MiddlewareHandlers[0]
	if mh.EncodeOutCookieAttrs == nil {
		t.Fatal("want non-nil EncodeOutCookieAttrs")
	}
	attrs, err := mh.EncodeOutCookieAttrs(mdTestOut{Value: "sess-123"})
	if err != nil {
		t.Fatalf("EncodeOutCookieAttrs: %v", err)
	}
	got, ok := attrs["session"]
	if !ok {
		t.Fatalf("want attrs for %q, got %+v", "session", attrs)
	}
	if got.MaxAge != 3600 || !got.Insecure {
		t.Errorf("want MaxAge=3600 Insecure=true, got %+v", got)
	}
}

// ── D6(b)/D7: uniqueness + ambiguous-attachment checks ──────────────────

func TestRegister_DuplicateMiddlewareNameRejected(t *testing.T) {
	fn := func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	}
	bmA := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(), fn).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))
	bmB := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(), fn) // SAME Declaration.Name "test-policy"

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/dup", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "dup"},
	)
	route = route.HandleBoundMW(bmA)
	route = route.HandleBoundMW(bmB)

	err := route.Register(rest.NewServer(testInfo))
	var dupErr rest.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
	if dupErr.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", dupErr.Name)
	}
}

func TestRegister_CodecBackedMiddlewarePassedToHandleMW_Rejected(t *testing.T) {
	// docs/design/d-0003-codec-declared-middlewares.md's Addendum 7: a codec-backed Middleware[In,Out]
	// (reusable-ONLY, regardless of whether it's also bundled via
	// WithReceive) can no longer be attached via HandleMW at all — only
	// .Use() (reusable) or HandleBoundMW (bound, a DIFFERENT type) work.
	mw := rest.NewMiddleware(newTestDeclaration()).
		WithReceive(func(ctx context.Context, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	route := rest.NewRoute[mwTestReq, userResp]("GET", "/ambiguous", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "ambiguous"},
	)
	route = route.HandleMW(mw, func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	})

	err := route.Register(rest.NewServer(testInfo))
	var misErr rest.MiddlewareMisattachedError
	if !errors.As(err, &misErr) {
		t.Fatalf("want MiddlewareMisattachedError, got %v", err)
	}
	if misErr.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", misErr.Name)
	}
}

func TestRegister_SingleAttachmentStyleSucceeds(t *testing.T) {
	// A BoundMiddleware used ONLY via HandleBoundMW (bound, never bundled
	// via .Use()) must register successfully — D7's ambiguity is now
	// structurally impossible (see checkMiddlewareNameUniquenessAndAttachment).
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})
	route := rest.NewRoute[mwTestReq, userResp]("GET", "/single", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "single"},
	)
	route = route.HandleBoundMW(bm)
	if err := route.Register(rest.NewServer(testInfo)); err != nil {
		t.Fatalf("want successful Register, got %v", err)
	}
}

// ── HandleBoundMW/ClientBoundMW (SSE): spec layering + D6(b)/D7 apply to SSE too ──

func TestSSERoute_HandleBoundMW_LayersHeaderIntoSpec(t *testing.T) {
	bm := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: "applied:" + in.Key}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Applied", codex.String(),
			func(out mdTestOut) string { return out.Value },
			func(out *mdTestOut, v string) { out.Value = v },
		))
	sseRoute := rest.NewSSERoute[mwTestReq, sseEvent]("/stream",
		mwTestReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "stream"},
	)
	sseRoute = sseRoute.HandleBoundMW(bm)
	h, err := sseRoute.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.HeaderParams {
		if p.Name == "X-API-Key" {
			found = true
		}
	}
	if !found {
		t.Errorf("want X-API-Key header param layered into spec, got %+v", h.Descriptor.HeaderParams)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Errorf("want exactly one MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
	// NOTE: mw's own response header merge field does NOT populate
	// h.ResponseHeaderMergeFields() (that accessor is for the SSE ROUTE's
	// OWN Event-derived response merge fields) — mw's response merge
	// fields are encoded via its OWN MiddlewareHandler.EncodeOut at
	// runtime instead (exercised by the server dispatch tests in
	// adapters/nethttp and adapters/chi). Only the plain spec metadata
	// (the param NAME/description/required/codec) is layered into
	// Descriptor.Responses[0].Headers, asserted below.
	responseHeaderFound := false
	for _, p := range h.Descriptor.Responses[0].Headers {
		if p.Name == "X-Policy-Applied" {
			responseHeaderFound = true
		}
	}
	if !responseHeaderFound {
		t.Errorf("want X-Policy-Applied layered into Descriptor.Responses[0].Headers, got %+v", h.Descriptor.Responses[0].Headers)
	}
}

func TestSSERoute_ClientBoundMW_PopulatesClientHandle(t *testing.T) {
	bm := rest.NewBoundClientMiddleware[mwTestReq](newTestDeclaration(),
		func(ctx context.Context, req mwTestReq) (mdTestIn, error) {
			return mdTestIn{Key: "secret"}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		))
	sseRoute := rest.NewSSERoute[mwTestReq, sseEvent]("/stream2",
		mwTestReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "stream2"},
	)
	sseRoute = sseRoute.ClientBoundMW(bm)
	h := sseRoute.ClientHandle()
	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Errorf("want exactly one ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}
}

func TestSSERoute_Register_DuplicateMiddlewareNameRejected(t *testing.T) {
	fn := func(ctx context.Context, req *mwTestReq, in mdTestIn) (mdTestOut, error) {
		return mdTestOut{Value: in.Key}, nil
	}
	bmA := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(), fn)
	bmB := rest.NewBoundMiddleware[mwTestReq](newTestDeclaration(), fn) // SAME Declaration.Name
	sseRoute := rest.NewSSERoute[mwTestReq, sseEvent]("/stream3",
		mwTestReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "stream3"},
	)
	sseRoute = sseRoute.HandleBoundMW(bmA)
	sseRoute = sseRoute.HandleBoundMW(bmB)

	_, err := sseRoute.RegisterHandle(rest.NewServer(testInfo))
	var dupErr rest.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
}

// mdAgnosticReq2 is a SECOND, DIFFERENT Req type — used to prove a
// route-agnostic Middleware value is reusable verbatim across routes with
// different Req types (the actual point of route-agnostic .Use()).
type mdAgnosticReq2 struct{ Other string }

var mdAgnosticReq2Codec = codex.Struct[mdAgnosticReq2](
	codex.RequiredField("other", codex.String(),
		func(r mdAgnosticReq2) string { return r.Other },
		func(r *mdAgnosticReq2, v string) { r.Other = v },
	),
)

func TestUse_AcceptsCodecBackedMiddleware(t *testing.T) {
	// A single bundled Middleware value, attached via plain .Use() to TWO
	// routes with DIFFERENT Req types — confirms route-AGNOSTIC reuse.
	mw := rest.NewMiddleware(newTestDeclaration()).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-API-Key", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		)).
		WithReceive(func(ctx context.Context, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	routeA := rest.NewRoute[mwTestReq, userResp]("GET", "/agnostic-a", mwTestReqCodec, userCodec,
		rest.RouteMeta{OperationID: "agnosticA"},
	).Use(mw)
	routeB := rest.NewRoute[mdAgnosticReq2, userResp]("GET", "/agnostic-b", mdAgnosticReq2Codec, userCodec,
		rest.RouteMeta{OperationID: "agnosticB"},
	).Use(mw)

	hA, err := routeA.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("route A RegisterHandle: %v", err)
	}
	hB, err := routeB.RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("route B RegisterHandle: %v", err)
	}

	foundA := false
	for _, p := range hA.Descriptor.HeaderParams {
		if p.Name == "X-API-Key" {
			foundA = true
		}
	}
	if !foundA {
		t.Errorf("route A: want X-API-Key header param layered into spec, got %+v", hA.Descriptor.HeaderParams)
	}
	if len(hA.MiddlewareHandlers) != 1 || !hA.MiddlewareHandlers[0].Agnostic {
		t.Errorf("route A: want exactly one Agnostic MiddlewareHandler, got %+v", hA.MiddlewareHandlers)
	}

	foundB := false
	for _, p := range hB.Descriptor.HeaderParams {
		if p.Name == "X-API-Key" {
			foundB = true
		}
	}
	if !foundB {
		t.Errorf("route B: want X-API-Key header param layered into spec, got %+v", hB.Descriptor.HeaderParams)
	}
	if len(hB.MiddlewareHandlers) != 1 || !hB.MiddlewareHandlers[0].Agnostic {
		t.Errorf("route B: want exactly one Agnostic MiddlewareHandler, got %+v", hB.MiddlewareHandlers)
	}
}

// ── structured errors: construction + Error()/Unwrap()/LogValue() ───────

func TestMiddlewareInputError(t *testing.T) {
	inner := errors.New("boom")
	err := rest.MiddlewareInputError{Name: "api-key-policy", Err: inner}
	if !errors.Is(err, inner) {
		t.Errorf("want errors.Is to match the wrapped error")
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestMiddlewareError(t *testing.T) {
	inner := errors.New("invalid api key")
	err := rest.MiddlewareError{Name: "api-key-policy", Err: inner}
	if !errors.Is(err, inner) {
		t.Errorf("want errors.Is to match the wrapped error")
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

// TestMiddlewareOutputError verifies the OUTPUT-side counterpart of
// TestMiddlewareInputError above — added for symmetry (see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2).
func TestMiddlewareOutputError(t *testing.T) {
	inner := errors.New("boom")
	err := rest.MiddlewareOutputError{Name: "api-key-policy", Err: inner}
	if !errors.Is(err, inner) {
		t.Errorf("want errors.Is to match the wrapped error")
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
	var target rest.MiddlewareOutputError
	if !errors.As(err, &target) {
		t.Fatal("want errors.As to match MiddlewareOutputError")
	}
	if target.Name != "api-key-policy" {
		t.Errorf("want Name %q, got %q", "api-key-policy", target.Name)
	}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	seen := map[string]bool{}
	for _, a := range v.Group() {
		seen[a.Key] = true
	}
	for _, key := range []string{"name", "err"} {
		if !seen[key] {
			t.Errorf("want LogValue group to include key %q, got %v", key, v.Group())
		}
	}
}

func TestDuplicateMiddlewareNameError(t *testing.T) {
	err := rest.DuplicateMiddlewareNameError{Route: "GET /profile", Name: "api-key-policy"}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestBoundMiddlewareReqMismatchError(t *testing.T) {
	err := rest.BoundMiddlewareReqMismatchError{Route: "GET /profile", Got: 42, Name: "api-key-policy"}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestMiddlewareMisattachedError(t *testing.T) {
	err := rest.MiddlewareMisattachedError{Route: "GET /profile", Name: "api-key-policy"}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}
