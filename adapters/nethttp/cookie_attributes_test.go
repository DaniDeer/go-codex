package nethttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// TestHandler_ResponseCookieMerge_WithAttributes_SetsMaxAgeAndInsecure
// exercises the declarative cookie-attributes mechanism end-to-end: a
// route's own merge-derived response cookie declares WithAttributes, and
// the ACTUAL Set-Cookie header reflects Max-Age + omits Secure (Insecure).
func TestHandler_ResponseCookieMerge_WithAttributes_SetsMaxAgeAndInsecure(t *testing.T) {
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.NewRequiredResponseCookieParam("session", sessionCodec,
			func(u userResp) string { return "sess-" + u.ID },
			func(u *userResp, v string) {},
		).WithAttributes(func(u userResp) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 3600, Insecure: true}
		}),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "abcdefgh", Name: req.Name}, nil
	})
	handler := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "Max-Age=3600") {
		t.Errorf("want Set-Cookie to contain Max-Age=3600, got %q", setCookie)
	}
	if strings.Contains(setCookie, "Secure") {
		t.Errorf("want Set-Cookie to OMIT Secure (Insecure declared), got %q", setCookie)
	}
}

// TestHandler_ResponseCookieMerge_NoAttributes_UnchangedBehavior confirms a
// merge-derived cookie with NO declared attributes behaves exactly as
// before this mechanism existed (zero-value Opts).
func TestHandler_ResponseCookieMerge_NoAttributes_UnchangedBehavior(t *testing.T) {
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	route := rest.NewRoute[createReq, userResp]("POST", "/users2", createReqCodec, userRespCodec,
		rest.NewRequiredResponseCookieParam("session", sessionCodec,
			func(u userResp) string { return "sess-" + u.ID },
			func(u *userResp, v string) {},
		),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "abcdefgh", Name: req.Name}, nil
	})
	handler := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users2", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if strings.Contains(setCookie, "Max-Age") {
		t.Errorf("want no Max-Age (no attributes declared), got %q", setCookie)
	}
	if !strings.Contains(setCookie, "Secure") {
		t.Errorf("want Secure by default (adapter's own default), got %q", setCookie)
	}
}

// TestSSEHandler_ResponseCookieMerge_WithAttributes exercises the SSE
// mirror of the plain-Route mechanism end-to-end — a route's own
// merge-derived response cookie declares WithAttributes, and the ACTUAL
// Set-Cookie header (committed at first event) reflects Max-Age.
func TestSSEHandler_ResponseCookieMerge_WithAttributes(t *testing.T) {
	sessionCodec := codex.String().Refine(validate.MinLen(4))
	route := rest.NewSSERoute[createReq, sseEvent]("/events5", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents5"},
		rest.NewRequiredResponseCookieParam("session", sessionCodec,
			func(e sseEvent) string { return "sess1" },
			func(e *sseEvent, v string) {},
		).WithAttributes(func(e sseEvent) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 1800, Insecure: true}
		}),
	).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events5", nil)
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "Max-Age=1800") {
		t.Errorf("want Set-Cookie to contain Max-Age=1800, got %q", setCookie)
	}
	if strings.Contains(setCookie, "Secure") {
		t.Errorf("want Set-Cookie to OMIT Secure (Insecure declared), got %q", setCookie)
	}
}

// TestTransform_ResponseCookieWithAttributes_ReachesSetCookieHeader
// exercises a Middleware[In,Out]-declared response cookie's attributes,
// attached via Transform, reaching the actual Set-Cookie header.
func TestTransform_ResponseCookieWithAttributes_ReachesSetCookieHeader(t *testing.T) {
	mw := rest.NewMiddleware(newTDEmptyDeclaration("cookie-attrs-policy")).
		WithResponseCookie(rest.NewRequiredResponseCookieParam("policy-session", codex.String(),
			func(out tdEmpty) string { return "policy-value" },
			func(out *tdEmpty, v string) {},
		).WithAttributes(func(out tdEmpty) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 900, Insecure: true}
		}))

	route := rest.NewRoute[createReq, userResp]("POST", "/users3", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser3"},
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
		return tdEmpty{}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	handler := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users3", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Api-Key", "irrelevant")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "Max-Age=900") {
		t.Errorf("want Set-Cookie to contain Max-Age=900, got %q", setCookie)
	}
}

// TestCookieAttributes_Precedence_LastAttachedWins exercises the
// registration-order, last-applied-wins precedence rule (mirrors the
// existing header/cookie VALUE composition rule) when a route's own
// response cookie AND a Transform-attached middleware's cookie both
// declare attributes for the SAME cookie name.
func TestCookieAttributes_Precedence_LastAttachedWins(t *testing.T) {
	mw := rest.NewMiddleware(newTDEmptyDeclaration("precedence-policy")).
		WithResponseCookie(rest.NewRequiredResponseCookieParam("session", codex.String(),
			func(out tdEmpty) string { return "mw-value" },
			func(out *tdEmpty, v string) {},
		).WithAttributes(func(out tdEmpty) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 111}
		}))

	route := rest.NewRoute[createReq, userResp]("POST", "/users4", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser4"},
		rest.NewRequiredResponseCookieParam("session", codex.String(),
			func(u userResp) string { return "route-value" },
			func(u *userResp, v string) {},
		).WithAttributes(func(u userResp) rest.CookieAttributes {
			return rest.CookieAttributes{MaxAge: 222}
		}),
	)
	route = rest.Transform(route, mw, func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
		return tdEmpty{}, nil
	})
	route = route.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	handler := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users4", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	// Both the route's own AND the middleware's "session" cookie are
	// emitted as SEPARATE Set-Cookie response headers (this codebase does
	// not deduplicate same-named PendingCookie entries at dispatch time —
	// pre-existing behavior, unrelated to cookie attributes). A
	// standards-conformant HTTP client applies the LAST one received for
	// a given cookie name to its jar — mirrored here by checking the
	// LAST "session" entry in the response's parsed Set-Cookie headers,
	// confirming middleware (composed AFTER the route's own merge
	// fields) wins.
	cookies := rec.Result().Cookies()
	var lastSession *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			c := c
			lastSession = c
		}
	}
	if lastSession == nil {
		t.Fatalf("want a session cookie, got %+v", cookies)
	}
	if lastSession.MaxAge != 111 {
		t.Errorf("want last-attached (middleware) attrs MaxAge=111 to win, got MaxAge=%d", lastSession.MaxAge)
	}
}
