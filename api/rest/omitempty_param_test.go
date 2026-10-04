package rest_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ── docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A:
// the full omit-empty constructor family (one per existing Required/
// Optional pair, matching the EXISTING symmetry every other merge-field
// location already has) — NewOmitEmptyQueryParam/Cookie/Header (request
// side) and NewOmitEmptyResponseHeaderParam/ResponseCookieParam (response
// side). All built on [codex.EncodeMergeVars], never [codex.EncodeVars].

// ── request side: query ─────────────────────────────────────────────────

func TestNewOmitEmptyQueryParam_OmitsWhenEmpty(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyQueryParam("filter", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeQueryVars(createReq{Name: ""})
	if err != nil {
		t.Fatalf("EncodeQueryVars: %v", err)
	}
	if _, ok := vars["filter"]; ok {
		t.Errorf("want filter omitted, got %q", vars["filter"])
	}
}

func TestNewOmitEmptyQueryParam_IncludesWhenPresent(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyQueryParam("filter", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeQueryVars(createReq{Name: "alice"})
	if err != nil {
		t.Fatalf("EncodeQueryVars: %v", err)
	}
	if vars["filter"] != "alice" {
		t.Errorf("want filter %q, got %q", "alice", vars["filter"])
	}
}

func TestNewOmitEmptyQueryParam_SpecRendersNotRequired(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyQueryParam("filter", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.QueryParams {
		if p.Name == "filter" {
			found = true
			if p.Required {
				t.Errorf("want Required false, got true")
			}
		}
	}
	if !found {
		t.Errorf("want filter query param layered into spec, got %+v", h.Descriptor.QueryParams)
	}
}

// ── request side: cookie ────────────────────────────────────────────────

func TestNewOmitEmptyCookieParam_OmitsWhenEmpty(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyCookieParam("session", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeCookieVars(createReq{Name: ""})
	if err != nil {
		t.Fatalf("EncodeCookieVars: %v", err)
	}
	if _, ok := vars["session"]; ok {
		t.Errorf("want session omitted, got %q", vars["session"])
	}
}

func TestNewOmitEmptyCookieParam_IncludesWhenPresent(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyCookieParam("session", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeCookieVars(createReq{Name: "sess-123"})
	if err != nil {
		t.Fatalf("EncodeCookieVars: %v", err)
	}
	if vars["session"] != "sess-123" {
		t.Errorf("want session %q, got %q", "sess-123", vars["session"])
	}
}

func TestNewOmitEmptyCookieParam_SpecRendersNotRequired(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyCookieParam("session", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.CookieParams {
		if p.Name == "session" {
			found = true
			if p.Required {
				t.Errorf("want Required false, got true")
			}
		}
	}
	if !found {
		t.Errorf("want session cookie param layered into spec, got %+v", h.Descriptor.CookieParams)
	}
}

// ── request side: header ────────────────────────────────────────────────

func TestNewOmitEmptyHeaderParam_OmitsWhenEmpty(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyHeaderParam("X-Api-Key", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeHeaderVars(createReq{Name: ""})
	if err != nil {
		t.Fatalf("EncodeHeaderVars: %v", err)
	}
	if _, ok := vars["X-Api-Key"]; ok {
		t.Errorf("want X-Api-Key omitted, got %q", vars["X-Api-Key"])
	}
}

func TestNewOmitEmptyHeaderParam_IncludesWhenPresent(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyHeaderParam("X-Api-Key", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeHeaderVars(createReq{Name: "secret"})
	if err != nil {
		t.Fatalf("EncodeHeaderVars: %v", err)
	}
	if vars["X-Api-Key"] != "secret" {
		t.Errorf("want X-Api-Key %q, got %q", "secret", vars["X-Api-Key"])
	}
}

func TestNewOmitEmptyHeaderParam_SpecRendersNotRequired(t *testing.T) {
	h, err := rest.NewRoute[createReq, userResp]("GET", "/users", createReqCodec, userCodec,
		rest.NewOmitEmptyHeaderParam("X-Api-Key", codex.String(),
			func(r createReq) string { return r.Name },
			func(r *createReq, v string) { r.Name = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	found := false
	for _, p := range h.Descriptor.HeaderParams {
		if p.Name == "X-Api-Key" {
			found = true
			if p.Required {
				t.Errorf("want Required false, got true")
			}
		}
	}
	if !found {
		t.Errorf("want X-Api-Key header param layered into spec, got %+v", h.Descriptor.HeaderParams)
	}
}

// ── response side: header ───────────────────────────────────────────────

func TestNewOmitEmptyResponseHeaderParam_OmitsWhenEmpty(t *testing.T) {
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOmitEmptyResponseHeaderParam("X-Request-Id", codex.String(),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	headers, _, err := h.EncodeResponseMergeFields(userRespWithMeta{RequestID: ""})
	if err != nil {
		t.Fatalf("EncodeResponseMergeFields: %v", err)
	}
	if _, ok := headers["X-Request-Id"]; ok {
		t.Errorf("want X-Request-Id omitted, got %q", headers["X-Request-Id"])
	}
}

func TestNewOmitEmptyResponseHeaderParam_IncludesWhenPresent(t *testing.T) {
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOmitEmptyResponseHeaderParam("X-Request-Id", codex.String(),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	headers, _, err := h.EncodeResponseMergeFields(userRespWithMeta{RequestID: "req-1"})
	if err != nil {
		t.Fatalf("EncodeResponseMergeFields: %v", err)
	}
	if headers["X-Request-Id"] != "req-1" {
		t.Errorf("want X-Request-Id %q, got %q", "req-1", headers["X-Request-Id"])
	}
}

func TestNewOmitEmptyResponseHeaderParam_SpecRendersNotRequired(t *testing.T) {
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOmitEmptyResponseHeaderParam("X-Request-Id", codex.String(),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if len(h.Descriptor.Responses) == 0 {
		t.Fatalf("Descriptor.Responses: want non-empty, got %+v", h.Descriptor.Responses)
	}
	found := false
	for _, hdr := range h.Descriptor.Responses[0].Headers {
		if hdr.Name == "X-Request-Id" {
			found = true
			if hdr.Required {
				t.Errorf("want Required false, got true")
			}
		}
	}
	if !found {
		t.Errorf("want X-Request-Id response header layered into spec, got %+v", h.Descriptor.Responses[0].Headers)
	}
}

// ── response side: cookie ───────────────────────────────────────────────

func TestNewOmitEmptyResponseCookieParam_OmitsWhenEmpty(t *testing.T) {
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOmitEmptyResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	_, cookies, err := h.EncodeResponseMergeFields(userRespWithMeta{Session: ""})
	if err != nil {
		t.Fatalf("EncodeResponseMergeFields: %v", err)
	}
	if _, ok := cookies["session"]; ok {
		t.Errorf("want session omitted, got %q", cookies["session"])
	}
}

func TestNewOmitEmptyResponseCookieParam_IncludesWhenPresent(t *testing.T) {
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOmitEmptyResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	_, cookies, err := h.EncodeResponseMergeFields(userRespWithMeta{Session: "sess-9"})
	if err != nil {
		t.Fatalf("EncodeResponseMergeFields: %v", err)
	}
	if cookies["session"] != "sess-9" {
		t.Errorf("want session %q, got %q", "sess-9", cookies["session"])
	}
}

func TestNewOmitEmptyResponseCookieParam_SpecRendersNotRequired(t *testing.T) {
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewOmitEmptyResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if len(h.ResponseCookieMergeFields()) != 1 {
		t.Fatalf("ResponseCookieMergeFields: want 1, got %d", len(h.ResponseCookieMergeFields()))
	}
}

// TestEncodeMergeVars_RegressionGuard_EncodeVarsStillWritesSparseUnconditionally
// confirms (api/rest layer) that nothing in this family altered
// [codex.EncodeVars]'s OWN "always write every field" contract — the path
// var encode direction (RouteHandle.EncodeVars) must remain untouched,
// never switched to EncodeMergeVars (path segments must never be silently
// omitted — see codex.EncodeMergeVars's own doc comment).
func TestEncodeMergeVars_RegressionGuard_PathVarsStillUseEncodeVars(t *testing.T) {
	type pathReq struct{ ID string }
	pathReqCodec := codex.Struct[pathReq]()
	h, err := rest.NewRoute[pathReq, userResp]("GET", "/users/{id}", pathReqCodec, userCodec,
		rest.NewPathParam("id", codex.String(),
			func(r pathReq) string { return r.ID },
			func(r *pathReq, v string) { r.ID = v }),
	).RegisterHandle(rest.NewServer(testInfo))
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	vars, err := h.EncodeVars(pathReq{ID: ""})
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if v, ok := vars["id"]; !ok || v != "" {
		t.Errorf("want id present and empty (path vars never omitted), got ok=%v val=%q", ok, v)
	}
}
