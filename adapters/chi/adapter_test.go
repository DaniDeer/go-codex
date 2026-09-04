package chi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test types ---

// scopesImpl reproduces the removed middleware.Scopes/nethttp.Scopes
// constructors' exact behavior — kept test-local since HandleMW now
// builds this shape internally (see
// docs/design/middleware-workflow-simplification.md's "Decision:
// HandleMW/ClientMW unification"); these tests exercise the OLD, still-
// present Handler/Register directly (not HandleMW), so they
// still need a raw middleware.ServerImplementation value to pass.
func scopesImpl[Req any](schemeName string, extract func(context.Context, *http.Request, *Req) (map[string][]string, error)) middleware.ServerImplementation {
	return middleware.ServerImplementation{
		Name:      "implement-scopes:" + schemeName,
		Satisfies: []string{schemeName},
		Fn:        extract,
	}
}

// apiKeyImpl reproduces the removed nethttp.APIKey constructor's exact
// behavior — same rationale as [scopesImpl].
func apiKeyImpl[Req any](headerName string, verify func(ctx context.Context, key string) error) middleware.ServerImplementation {
	return middleware.ServerImplementation{
		Name: "implement-api-key:" + headerName,
		Fn: func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error) {
			return nil, verify(ctx, r.Header.Get(headerName))
		},
	}
}

type createReq struct{ Name string }
type userResp struct{ ID, Name string }
type getReq struct{}
type handlerConflictError struct{ msg string }

func (e handlerConflictError) Error() string { return e.msg }

var createReqCodec = codex.Struct[createReq](
	codex.Field[createReq, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(r createReq) string { return r.Name },
		Set:      func(r *createReq, v string) { r.Name = v },
		Required: true,
	},
)
var userRespCodec = codex.Struct[userResp](
	codex.Field[userResp, string]{
		Name:  "id",
		Codec: codex.String(),
		Get:   func(u userResp) string { return u.ID },
		Set:   func(u *userResp, v string) { u.ID = v },
	},
	codex.Field[userResp, string]{
		Name:  "name",
		Codec: codex.String(),
		Get:   func(u userResp) string { return u.Name },
		Set:   func(u *userResp, v string) { u.Name = v },
	},
)
var getReqCodec = codex.Struct[getReq]()
var testInfo = rest.Info{Title: "Test API", Version: "1.0.0"}

// --- helpers ---

func newCreateRoute() rest.Route[createReq, userResp] {
	return rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"})
}

func newGetRoute(path string) rest.Route[getReq, userResp] {
	return rest.NewRoute[getReq, userResp]("GET", path,
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "getUser"})
}

func decodeJSON(t *testing.T, body io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// mustServeOne is a test helper wrapping [ServeOne], failing the
// test immediately on error (every test route here is expected to be
// valid — a ServeOne error indicates a genuine test bug).
func mustServeOne[Req, Resp any](t *testing.T, route rest.Route[Req, Resp]) http.Handler {
	t.Helper()
	h, err := serveOne(route)
	if err != nil {
		t.Fatalf("ServeOne: %v", err)
	}
	return h
}

// mustServe is [mustServeOne]'s builder-based sibling — used when a test
// needs builder-level state (e.g. [rest.Server.AddGlobalSecurity]) that
// ServeOne's internal scratch Builder cannot expose.
func mustServe[Req, Resp any](t *testing.T, route rest.Route[Req, Resp], b *rest.Server) gochi.Router {
	t.Helper()
	if err := route.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := gochi.NewRouter()
	if err := serve(r, b); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return r
}

// mustServeSSE is [mustServe]'s SSE sibling.
func mustServeSSE[Req, Event any](t *testing.T, route rest.SSERoute[Req, Event], b *rest.Server) gochi.Router {
	t.Helper()
	if err := route.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := gochi.NewRouter()
	if err := serveSSE(r, b); err != nil {
		t.Fatalf("ServeSSE: %v", err)
	}
	return r
}

// --- tests ---

func TestHandler_Post_Success(t *testing.T) {
	route := newCreateRoute().WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	var got userResp
	decodeJSON(t, resp.Body, &got)
	if got.Name != "Alice" {
		t.Fatalf("want Name=Alice, got %q", got.Name)
	}
}

func TestHandler_Post_InvalidBody(t *testing.T) {
	route := newCreateRoute().WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":""}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestHandler_PathParam_Chi(t *testing.T) {
	route := newGetRoute("/users/{id}").WithHandler(func(ctx context.Context, _ getReq) (userResp, error) {
		r, _ := RequestFromContext(ctx)
		id := gochi.URLParam(r, "id")
		return userResp{ID: id, Name: "Alice"}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/users/42") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got userResp
	decodeJSON(t, resp.Body, &got)
	if got.ID != "42" {
		t.Fatalf("want ID=42, got %q", got.ID)
	}
}

// P5: chi's Handler for a route WITH merge fields — the handler function
// receives an already-merged, validated req; no manual chi.URLParam needed.
func TestHandler_MergeFields_AutomaticMerge_Chi(t *testing.T) {
	type getUserReq struct{ ID string }
	getUserReqCodec := codex.Struct[getUserReq]()

	var gotID string
	route := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
	).WithHandler(func(_ context.Context, r getUserReq) (userResp, error) {
		gotID = r.ID // no chi.URLParam() call needed — already merged
		return userResp{ID: r.ID, Name: "Alice"}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/users/42") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if gotID != "42" {
		t.Errorf("handler did not receive merged ID: got %q", gotID)
	}
}

// P6: chi's Handler for a route WITHOUT merge fields — byte-for-byte
// identical behavior to before this feature (regression guard).
func TestHandler_NoMergeFields_UnchangedBehavior_Chi(t *testing.T) {
	baseRoute := newGetRoute("/users/{id}")
	if got := len(baseRoute.ClientHandle().MergeFields()); got != 0 {
		t.Fatalf("expected no merge fields for a plain route, got %d", got)
	}
	route := baseRoute.WithHandler(func(ctx context.Context, _ getReq) (userResp, error) {
		r, _ := RequestFromContext(ctx)
		id := gochi.URLParam(r, "id")
		return userResp{ID: id, Name: "Alice"}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/users/42") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestServe_WiresOntoRouter(t *testing.T) {
	route := newGetRoute("/users/{id}").WithHandler(func(ctx context.Context, _ getReq) (userResp, error) {
		rr, _ := RequestFromContext(ctx)
		id := gochi.URLParam(rr, "id")
		return userResp{ID: id, Name: "Bob"}, nil
	})
	b := rest.NewServer(testInfo)
	if err := route.Register(b); err != nil {
		t.Fatal(err)
	}
	r := gochi.NewRouter()
	if err := serve(r, b); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/users/99") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got userResp
	decodeJSON(t, resp.Body, &got)
	if got.ID != "99" {
		t.Fatalf("want ID=99, got %q", got.ID)
	}
}

func TestHandler_ResponseHeaders(t *testing.T) {
	locationCodec := codex.String().Refine(validate.MinLen(1))
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ResponseHeaderParam{Name: "Location", Required: true, Codec: &locationCodec},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		header := make(http.Header)
		header.Set("Location", "/users/1")
		WithResponseHeaders(ctx, header)
		return userResp{ID: "1", Name: req.Name}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/users/1" {
		t.Fatalf("want Location=/users/1, got %q", loc)
	}
}

func TestHandler_ResponseCookies(t *testing.T) {
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ResponseCookieParam{Name: "session", Required: true, Codec: &sessionCodec},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		WithResponseCookies(ctx, PendingCookie{
			Name: "session", Value: "abcdefgh",
			Opts: CookieOptions{MaxAge: 3600, Insecure: true},
		})
		return userResp{ID: "1", Name: req.Name}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	found := false
	for _, c := range resp.Header["Set-Cookie"] {
		if strings.Contains(c, "session=abcdefgh") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Set-Cookie header with session=abcdefgh not found, got: %v", resp.Header["Set-Cookie"])
	}
}

func TestHandler_ErrorStatusRouteMapping_Chi(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ErrorStatus[handlerConflictError](http.StatusConflict),
	).WithHandler(func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, handlerConflictError{msg: "conflict"}
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
}

// userRespWithMeta carries response header/cookie merge fields alongside
// the JSON body fields (Round 3 — response merge fields).
type userRespWithMeta struct {
	ID        string
	Name      string
	RequestID string
	Session   string
}

func (u userRespWithMeta) Error() string { return "error user response " + u.ID }

var userRespWithMetaBodyCodec = codex.Struct[userRespWithMeta](
	codex.RequiredField("id", codex.String(),
		func(u userRespWithMeta) string { return u.ID },
		func(u *userRespWithMeta, v string) { u.ID = v },
	),
	codex.RequiredField("name", codex.String(),
		func(u userRespWithMeta) string { return u.Name },
		func(u *userRespWithMeta, v string) { u.Name = v },
	),
)

// R6 (chi): Handler route WITH response header/cookie merge fields — server
// sets the header/cookie automatically from the handler's returned Resp, no
// WithResponseHeaders/WithResponseCookies call needed.
func TestHandler_ResponseMergeFields_AutoAppliesFromResp_Chi(t *testing.T) {
	route := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).WithHandler(func(_ context.Context, req createReq) (userRespWithMeta, error) {
		return userRespWithMeta{ID: "1", Name: req.Name, RequestID: "req-999", Session: "sess-xyz"}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Request-Id"); got != "req-999" {
		t.Errorf("want X-Request-Id=req-999, got %q", got)
	}
	found := false
	for _, c := range resp.Header["Set-Cookie"] {
		if strings.Contains(c, "session=sess-xyz") {
			found = true
		}
	}
	if !found {
		t.Errorf("Set-Cookie session=sess-xyz not found, got: %v", resp.Header["Set-Cookie"])
	}
}

func TestHandler_ErrorPattern_DirectWithResponseHeaderCookieParity_Chi(t *testing.T) {
	route := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
		rest.ErrorPattern[userRespWithMeta, userRespWithMeta](http.StatusConflict, userRespWithMetaBodyCodec),
	).WithHandler(func(_ context.Context, req createReq) (userRespWithMeta, error) {
		return userRespWithMeta{}, userRespWithMeta{
			ID: "e1", Name: req.Name, RequestID: "req-error-1", Session: "sess-error-1",
		}
	})

	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Request-Id"); got != "req-error-1" {
		t.Fatalf("want X-Request-Id=req-error-1, got %q", got)
	}
	found := false
	for _, c := range resp.Header["Set-Cookie"] {
		if strings.Contains(c, "session=sess-error-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Set-Cookie session=sess-error-1 not found, got: %v", resp.Header["Set-Cookie"])
	}
	var bodyMap map[string]string
	decodeJSON(t, resp.Body, &bodyMap)
	if bodyMap["id"] != "e1" || bodyMap["name"] != "Alice" {
		t.Fatalf("unexpected body: %v", bodyMap)
	}
}

func TestHandler_ErrorPattern_MappedPayload_Chi(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[handlerConflictError, userResp](http.StatusUnprocessableEntity, userRespCodec,
			func(e handlerConflictError) (userResp, error) {
				return userResp{ID: "mapped", Name: e.msg}, nil
			}),
	).WithHandler(func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, handlerConflictError{msg: "mapped-message"}
	})

	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", resp.StatusCode)
	}
	var got userResp
	decodeJSON(t, resp.Body, &got)
	if got.ID != "mapped" || got.Name != "mapped-message" {
		t.Fatalf("unexpected mapped response: %+v", got)
	}
}

// Phase 2 (chi): rest.ErrorPattern.WithAction(rest.ErrorHandle) skips the
// automatic typed body write and falls through to Options.ErrorHandler
// instead — the resolved status still applies.
func TestHandler_ErrorPattern_WithActionHandle_FallsThroughToErrorHandler_Chi(t *testing.T) {
	var gotErrorHandlerStatus int
	var gotErrorHandlerErr error
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[handlerConflictError, userResp](http.StatusConflict, userRespCodec,
			func(e handlerConflictError) (userResp, error) {
				return userResp{ID: "mapped", Name: e.msg}, nil
			}).WithAction(rest.ErrorHandle),
	).WithHandler(func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, handlerConflictError{msg: "handled-not-responded"}
	}).WithOptions(Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			gotErrorHandlerStatus = status
			gotErrorHandlerErr = err
			w.WriteHeader(status)
		},
	})

	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotErrorHandlerStatus != http.StatusConflict {
		t.Fatalf("want ErrorHandler called with 409, got %d", gotErrorHandlerStatus)
	}
	if gotErrorHandlerErr == nil {
		t.Fatal("want ErrorHandler called with the original error")
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want response status 409, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), `"mapped"`) {
		t.Fatalf("want NO typed body auto-written for handle action, got %s", raw)
	}
}

// R7 (chi): Handler route WITHOUT response merge fields behaves
// byte-for-byte identically to today — regression guard.
func TestHandler_ResponseMergeFields_NoneDeclaredIsUnaffected_Chi(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	srv := httptest.NewServer(mustServeOne(t, route))
	defer srv.Close()

	body := strings.NewReader(`{"name":"Alice"}`)
	resp, err := http.Post(srv.URL+"/users", "application/json", body) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
}

func TestHandler_ContentNegotiation(t *testing.T) {
	jsonFmt := format.JSON[userResp](userRespCodec)
	yamlFmt := format.YAML[userResp](userRespCodec)
	route := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec, rest.Formats(jsonFmt, yamlFmt),
	).WithHandler(func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "1", Name: "Alice"}, nil
	})
	httpHandler := mustServeOne(t, route)
	handler := httpHandler.ServeHTTP

	t.Run("json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users", nil)
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("want application/json, got %q", ct)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users", nil)
		req.Header.Set("Accept", "application/yaml")
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/yaml" {
			t.Fatalf("want application/yaml, got %q", ct)
		}
	})

	t.Run("not acceptable", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users", nil)
		req.Header.Set("Accept", "text/html")
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusNotAcceptable {
			t.Fatalf("want 406, got %d", rr.Code)
		}
	})
}

func TestContentNegotiation_streamedFormat_writesDirectly(t *testing.T) {
	streamFmt := format.NewStreamed(userRespCodec,
		func(v userResp, w io.Writer) error {
			_, err := fmt.Fprintf(w, "id=%s name=%s", v.ID, v.Name)
			return err
		},
		func([]byte) (userResp, error) { return userResp{}, errors.New("not decodable") },
		"text/plain",
	)
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.Formats(streamFmt, format.JSON(userRespCodec)),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "42", Name: req.Name}, nil
	})
	handler := mustServeOne(t, route).ServeHTTP

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Bob"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/plain")
	handler(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("want Content-Type text/plain, got %q", ct)
	}
	if body := rec.Body.String(); body != "id=42 name=Bob" {
		t.Errorf("want body 'id=42 name=Bob', got %q", body)
	}
}

func TestContentNegotiation_streamedFormat_validationErrorBefore200(t *testing.T) {
	// Codec with NonEmpty validation on response name field
	strictRespCodec := codex.Struct[userResp](
		codex.Field[userResp, string]{
			Name:  "id",
			Codec: codex.String(),
			Get:   func(u userResp) string { return u.ID },
			Set:   func(u *userResp, v string) { u.ID = v },
		},
		codex.Field[userResp, string]{
			Name:  "name",
			Codec: codex.String().Refine(validate.NonEmptyString),
			Get:   func(u userResp) string { return u.Name },
			Set:   func(u *userResp, v string) { u.Name = v },
		},
	)
	streamFmt := format.NewStreamed(strictRespCodec,
		func(v userResp, w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s", v.ID)
			return err
		},
		func([]byte) (userResp, error) { return userResp{}, nil },
		"text/plain",
	)
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, strictRespCodec,
		rest.Formats(streamFmt),
	).WithHandler(func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{ID: "1", Name: ""}, nil // Name="" fails NonEmptyString
	})
	handler := mustServeOne(t, route).ServeHTTP

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/plain")
	handler(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on validation failure, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Request format negotiation tests ---

func TestRequestFormats_JSONBodyAccepted(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec)),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	r := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRequestFormats_YAMLBodyAccepted(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec)),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	r := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("name: Bob\n"))
	req.Header.Set("Content-Type", "application/yaml")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got userResp
	decodeJSON(t, rec.Body, &got)
	if got.Name != "Bob" {
		t.Errorf("want Name=Bob, got %q", got.Name)
	}
}

// TestRequestFormats_YAMLBodyWithQueryMergeField exercises Serve's reflect
// dispatch's "format-negotiated body decode ALSO applies merge-capable
// params" path (RequestFormats + a merge field together) — the ApplyMergeFields
// reflect call added specifically for routes that decode via a non-default
// format instead of plain DecodeMerged (see docs/design/middleware-workflow-simplification.md).
func TestRequestFormats_YAMLBodyWithQueryMergeField(t *testing.T) {
	type mergeReq struct {
		Name   string
		Source string
	}
	reqCodec := codex.Struct[mergeReq](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(r mergeReq) string { return r.Name },
			func(r *mergeReq, v string) { r.Name = v },
		),
	)
	route := rest.NewRoute[mergeReq, userResp]("POST", "/users", reqCodec, userRespCodec,
		rest.RequestFormats(format.JSON(reqCodec), format.YAML(reqCodec)),
		rest.NewRequiredQueryParam("source", codex.String(),
			func(r mergeReq) string { return r.Source },
			func(r *mergeReq, v string) { r.Source = v }),
	).WithHandler(func(_ context.Context, req mergeReq) (userResp, error) {
		return userResp{ID: req.Source, Name: req.Name}, nil
	})
	r := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users?source=import", strings.NewReader("name: Bob\n"))
	req.Header.Set("Content-Type", "application/yaml")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got userResp
	decodeJSON(t, rec.Body, &got)
	if got.Name != "Bob" {
		t.Errorf("want Name=Bob (decoded from YAML body), got %q", got.Name)
	}
	if got.ID != "import" {
		t.Errorf("want ID=import (merged from query param via ApplyMergeFields), got %q", got.ID)
	}
}

func TestRequestFormats_WrongContentType_returns415(t *testing.T) {
	var capturedErr error
	route := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec)),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).WithOptions(Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
			capturedErr = e
			w.WriteHeader(status)
		},
	})
	r := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`<name>Alice</name>`))
	req.Header.Set("Content-Type", "application/xml")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("want 415, got %d", rec.Code)
	}
	var ctErr rest.UnsupportedMediaTypeError
	if !errors.As(capturedErr, &ctErr) {
		t.Fatalf("want UnsupportedMediaTypeError, got %T: %v", capturedErr, capturedErr)
	}
	if ctErr.Got != "application/xml" {
		t.Errorf("want Got=application/xml, got %q", ctErr.Got)
	}
	if len(ctErr.Supported) != 2 {
		t.Errorf("want 2 supported types, got %v", ctErr.Supported)
	}
}

// --- SSE tests ---

type sseEvent struct{ Message string }

var sseEventCodec = codex.Struct[sseEvent](
	codex.Field[sseEvent, string]{
		Name:     "message",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(e sseEvent) string { return e.Message },
		Set:      func(e *sseEvent, v string) { e.Message = v },
	},
)

// decodeSSEFirstFrame reassembles the FIRST SSE event's raw data bytes
// from a raw response body — the spec-correct counterpart to
// writeSSEData: strips the "data: " prefix from EVERY consecutive
// "data:" line up to the blank-line terminator and rejoins them with a
// single "\n". Mirrors adapters/nethttp's identical test helper (no
// import relationship between the two packages, so intentionally
// duplicated).
func decodeSSEFirstFrame(t *testing.T, body []byte) []byte {
	t.Helper()
	var lines [][]byte
	for _, line := range strings.Split(string(body), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, []byte(data))
	}
	if len(lines) == 0 {
		t.Fatalf("missing SSE data prefix in %q", string(body))
	}
	out := lines[0]
	for _, l := range lines[1:] {
		out = append(out, '\n')
		out = append(out, l...)
	}
	return out
}

// TestSSEHandler_MultiLineDataFraming confirms the server writes a
// multi-line event value (e.g. YAML block style) with EACH line getting
// its own "data: " prefix, per the WHATWG SSE spec — mirrors
// adapters/nethttp's identical test.
func TestSSEHandler_MultiLineDataFraming(t *testing.T) {
	route := rest.NewSSERoute[struct{}, userResp]("/stream-yaml",
		codex.Empty, userRespCodec, rest.RouteMeta{OperationID: "streamYAML"},
	).WithHandler(func(_ context.Context, _ struct{}, send func(userResp) error) error {
		return send(userResp{ID: "1", Name: "Alice"})
	})
	b := rest.NewServer(testInfo)
	handle, err := route.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	handle.WithFormats(format.YAML(userRespCodec))
	r := gochi.NewRouter()
	if err := serveSSE(r, b); err != nil {
		t.Fatalf("ServeSSE: %v", err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream-yaml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\n") {
		t.Fatalf("test setup: want YAML output to contain a newline, got %q", body)
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Errorf("want every line prefixed with %q, got line %q in body %q", "data: ", line, body)
		}
	}
	frame := decodeSSEFirstFrame(t, rec.Body.Bytes())
	got, err := format.YAML(userRespCodec).Unmarshal(frame)
	if err != nil {
		t.Fatalf("unmarshal reassembled YAML frame: %v", err)
	}
	if got.ID != "1" || got.Name != "Alice" {
		t.Fatalf("unexpected decoded event: %+v", got)
	}
}

func TestSSEHandler_streamEvents(t *testing.T) {
	route := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"}).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		for _, msg := range []string{"hello", "world"} {
			if err := send(sseEvent{Message: msg}); err != nil {
				return err
			}
		}
		return nil
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want text/event-stream, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"hello"`) || !strings.Contains(body, `"world"`) {
		t.Errorf("expected both events in body, got: %s", body)
	}
	if !strings.Contains(body, "data:") {
		t.Errorf("expected SSE data: prefix, got: %s", body)
	}
}

func TestSSEHandler_validationRejectsEvent(t *testing.T) {
	var sendErr error
	route := rest.NewSSERoute[createReq, sseEvent]("/events2",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEventsValidate"}).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		sendErr = send(sseEvent{Message: ""})
		return nil
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events2", nil)
	handler.ServeHTTP(rec, r)

	if sendErr == nil {
		t.Fatal("expected send to return validation error for empty message")
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("expected empty body on validation rejection, got: %s", body)
	}
}

func TestSSEHandler_ServeSSE_wiresOntoRouter(t *testing.T) {
	route := rest.NewSSERoute[createReq, sseEvent]("/events3",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamRegister"}).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "chi-registered"})
	})
	r := mustServeSSE(t, route, rest.NewServer(testInfo))

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events3")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want text/event-stream, got %q", ct)
	}
	if !strings.Contains(string(body), "chi-registered") {
		t.Errorf("expected 'chi-registered' in body, got: %s", body)
	}
}

// --- Security tests ---

type mockSecurityObserver struct {
	stats.NoopObserver
	location string
	scheme   string
}

func (o *mockSecurityObserver) RecordSecurityRejection(location, scheme string) {
	o.location = location
	o.scheme = scheme
}

func newSecuredRoute(mw middleware.Middleware) rest.Route[createReq, userResp] {
	return rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.WithMiddleware(mw),
	)
}

func TestHandler_SecurityFunc_calledForSecuredRoute(t *testing.T) {
	secFuncCalled := false
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, r *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "test-bearer-token" {
				return nil, errors.New("unauthorized")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	)
	route := newSecuredRoute(declMw).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(&declMw, implMw.Fn)
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	h.ServeHTTP(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for secured route")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_rejectsRequest(t *testing.T) {
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, errors.New("unauthorized")
		},
	)
	route := newSecuredRoute(declMw).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called when security rejects")
		return userResp{}, nil
	}).HandleMW(&declMw, implMw.Fn)
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

// TestHandler_SecurityFunc_UnpairedImplRejectedAtServeOne mirrors
// adapters/nethttp's identically-purposed test — see its own doc comment
// for the full rationale (attaching a Satisfies-bearing implementation
// WITHOUT a matching .Use() declaration is now REJECTED at Register/
// ServeOne time via UnknownMiddlewareImplementationError, not silently
// skipped at runtime).
func TestHandler_SecurityFunc_UnpairedImplRejectedAtServeOne(t *testing.T) {
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			t.Fatal("Fn must never be called — ServeOne should reject before wiring")
			return nil, nil
		},
	)
	unrelatedMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	route := newCreateRoute().WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(&unrelatedMw, implMw.Fn) // "bearerAuth" was never .Use()'d on this route

	_, err := serveOne(route)
	var unknownErr rest.UnknownMiddlewareImplementationError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("want UnknownMiddlewareImplementationError, got %v (%T)", err, err)
	}
}

// TestServeOne_MissingSecurityCoverage_RejectedAtServeTime mirrors
// adapters/nethttp's identically-purposed test — see its own doc comment
// for the full rationale (a route that declares a security scheme via
// .Use() but never attaches ANY implementation for it must be rejected at
// ServeOne/Serve time via rest.MissingSecurityMiddlewareError, a regression
// test for buildRouteHandler losing this check when Register/RegisterSSE
// were deleted).
func TestServeOne_MissingSecurityCoverage_RejectedAtServeTime(t *testing.T) {
	secMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	route := newCreateRoute().
		Use(secMw).
		WithHandler(func(_ context.Context, req createReq) (userResp, error) {
			return userResp{ID: "1", Name: req.Name}, nil
		})

	_, err := serveOne(route)
	var missingErr rest.MissingSecurityMiddlewareError
	if !errors.As(err, &missingErr) {
		t.Fatalf("want MissingSecurityMiddlewareError, got %v (%T)", err, err)
	}
}

// TestServeSSE_MissingSecurityCoverage_RejectedAtServeTime mirrors
// adapters/nethttp's identically-purposed test — see its own doc comment
// for the full rationale.
func TestServeSSE_MissingSecurityCoverage_RejectedAtServeTime(t *testing.T) {
	secMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	sseRoute := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"},
	).Use(secMw).WithHandler(func(_ context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hi"})
	})

	b := rest.NewServer(testInfo)
	if err := sseRoute.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := gochi.NewRouter()
	err := serveSSE(r, b)
	var missingErr rest.MissingSecurityMiddlewareError
	if !errors.As(err, &missingErr) {
		t.Fatalf("want MissingSecurityMiddlewareError, got %v (%T)", err, err)
	}
}

// TestHandler_RequireAPIKey_RunsWithoutRouteSecurity mirrors
// adapters/nethttp's identically-named test — chi reuses
// nethttp.APIKey directly, and the same G2 gating bug (fixed
// alongside this test) was present in chi's own runSecurityMiddleware.
func TestHandler_RequireAPIKey_RunsWithoutRouteSecurity(t *testing.T) {
	verifyCalled := false
	implMw := apiKeyImpl[createReq]("X-API-Key", func(_ context.Context, key string) error {
		verifyCalled = true
		if key != "secret" {
			return errors.New("invalid api key")
		}
		return nil
	})
	route := newCreateRoute().WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(nil, implMw.Fn) // implMw's Satisfies is empty — no .Use() pairing needed
	h := mustServeOne(t, route)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if !verifyCalled {
		t.Fatal("want RequireAPIKey's verify called even though the route declares no Security")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for missing API key, got %d", rec.Code)
	}

	verifyCalled = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", "secret")
	h.ServeHTTP(rec, r)

	if !verifyCalled {
		t.Fatal("want RequireAPIKey's verify called")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201 for valid API key, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_codecValidationFailure(t *testing.T) {
	jwtCodec := codex.String().Refine(validate.JWT)
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, &jwtCodec)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			t.Fatal("Fn must not be called when credential fails codec format validation")
			return nil, nil
		},
	)
	r := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.WithMiddleware(declMw),
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called when credential fails codec")
		return userResp{}, nil
	}).HandleMW(&declMw, implMw.Fn)
	h := mustServeOne(t, r)

	rec := httptest.NewRecorder()
	// No Authorization header — extracted credential will be empty → invalid JWT
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestHandler_SecurityObserver_calledOnRejection(t *testing.T) {
	obs := &mockSecurityObserver{}
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(ctx context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			// Fn-driven rejection recording is now the Fn author's own
			// responsibility (Class A moved into Fn) — the adapter no
			// longer calls RecordSecurityRejection automatically for
			// authorization (as opposed to credential-format) failures.
			if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection("/users", "bearerAuth")
			}
			return nil, errors.New("unauthorized")
		},
	)
	route := newSecuredRoute(declMw).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}).HandleMW(&declMw, implMw.Fn)
	h := mustServeOne(t, route)

	withObsMiddleware := func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), obs))
		h.ServeHTTP(w, r)
	}

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	withObsMiddleware(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if obs.location != "/users" {
		t.Errorf("want location=/users, got %q", obs.location)
	}
	if obs.scheme != "bearerAuth" {
		t.Errorf("want scheme=bearerAuth, got %q", obs.scheme)
	}
}

func newGlobalSecuredChiRoute() (rest.Route[createReq, userResp], *rest.Server) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	// No per-route Security — inherits global.
	r := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.WithMiddleware(rest.FromSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)),
	)
	return r, b
}

func TestChiHandler_GlobalSecurity_enforcedWhenNoPerRouteSecurity(t *testing.T) {
	r, b := newGlobalSecuredChiRoute()
	secFuncCalled := false
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, req *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			if req.Header.Get("Authorization") != "test-bearer-token" {
				return nil, errors.New("unauthorized")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	)
	r = r.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(&declMw, implMw.Fn)
	router := mustServe(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "test-bearer-token")
	router.ServeHTTP(rec, req)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for route inheriting global security")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestChiHandler_GlobalSecurity_rejectsWhenNoToken(t *testing.T) {
	r, b := newGlobalSecuredChiRoute()
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, errors.New("missing token")
		},
	)
	r = r.WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}).HandleMW(&declMw, implMw.Fn)
	router := mustServe(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 from global security, got %d", rec.Code)
	}
}

func TestChiHandler_GlobalSecurity_notCalledWhenExplicitlyEmpty(t *testing.T) {
	// NOTE: mirrors adapters/nethttp's identically-named test — see its
	// own doc comment for the full rationale (attaching a Satisfies-
	// bearing impl with no matching .Use() is now REJECTED outright, not
	// silently skipped; this test declares NO scheme and attaches NO
	// implementation at all — its actual assertion, that explicit empty
	// Security wins over inherited global security, needs neither).
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	r := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{
			OperationID: "createUser",
			Security:    []route.SecurityRequirement{},
		},
	).WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	router := mustServe(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201 (no auth required — explicit empty Security), got %d", rec.Code)
	}
}

// --- SSE security + param validation tests ---

func newGlobalSecuredSSERoute() (rest.SSERoute[createReq, sseEvent], *rest.Server) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	r := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamSecured"},
		rest.WithMiddleware(rest.FromSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)),
	)
	return r, b
}

func TestChiSSEHandler_GlobalSecurity_enforced(t *testing.T) {
	r, b := newGlobalSecuredSSERoute()
	secFuncCalled := false
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, req *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			if req.Header.Get("Authorization") != "test-bearer-token" {
				return nil, errors.New("unauthorized")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	)
	r = r.WithHandler(func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}).HandleMW(&declMw, implMw.Fn)
	router := mustServeSSE(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Authorization", "test-bearer-token")
	router.ServeHTTP(rec, req)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for SSE route inheriting global security")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestChiSSEHandler_GlobalSecurity_rejectsWhenNoToken(t *testing.T) {
	r, b := newGlobalSecuredSSERoute()
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	implMw := scopesImpl[createReq]("bearerAuth",
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, errors.New("missing token")
		},
	)
	r = r.WithHandler(func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}).HandleMW(&declMw, implMw.Fn)
	router := mustServeSSE(t, r, b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 from SSE global security, got %d", rec.Code)
	}
}

func TestChiSSEHandler_QueryParam_rejectsInvalid(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	r := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).WithHandler(func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream?id=not-a-uuid", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE query param, got %d", rec.Code)
	}
}

func TestChiSSEHandler_QueryParam_allowsValid(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	r := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).WithHandler(func(_ context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "ok"})
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream?id=f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200 for valid SSE query param, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChiSSEHandler_CookieParam_rejectsInvalid(t *testing.T) {
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	r := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.CookieParam{Name: "session", Codec: &tokenCodec},
	).WithHandler(func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: ""})
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE cookie param, got %d", rec.Code)
	}
}

func TestChiSSEHandler_HeaderParam_rejectsInvalid(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	r := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.HeaderParam{Name: "X-Request-Id", Codec: &uuidCodec},
	).WithHandler(func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("X-Request-Id", "not-a-uuid")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE header param, got %d", rec.Code)
	}
}

func TestSSEHandler_ResponseHeaderParam_appearsOnFirstSend(t *testing.T) {
	traceCodec := codex.String().Refine(validate.NonEmptyString)
	r := rest.NewSSERoute[createReq, sseEvent]("/stream-rh",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &traceCodec},
	).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		extra := make(http.Header)
		extra.Set("X-Trace-Id", "trace-abc-123")
		WithResponseHeaders(ctx, extra)
		return send(sseEvent{Message: "hello"})
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/stream-rh") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if v := resp.Header.Get("X-Trace-Id"); v != "trace-abc-123" {
		t.Errorf("want X-Trace-Id=trace-abc-123, got %q", v)
	}
}

func TestSSEHandler_ResponseHeaderParam_codecViolation_abortsStream(t *testing.T) {
	traceCodec := codex.String().Refine(validate.NonEmptyString)
	sendCalled := false
	r := rest.NewSSERoute[createReq, sseEvent]("/stream-rh2",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &traceCodec},
	).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		extra := make(http.Header)
		extra.Set("X-Trace-Id", "") // empty — codec rejects it
		WithResponseHeaders(ctx, extra)
		err := send(sseEvent{Message: "should not appear"})
		if err != nil {
			sendCalled = true
			return err
		}
		return nil
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream-rh2", nil)
	router.ServeHTTP(rec, req)

	if !sendCalled {
		t.Error("want send to return error for invalid response header codec, but send was not called")
	}
	if strings.Contains(rec.Body.String(), "should not appear") {
		t.Errorf("want no event data written when response header codec fails, got: %s", rec.Body.String())
	}
}

func TestHandler_PathParam_codecValidated(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	r := rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).WithHandler(func(_ context.Context, r getReq) (userResp, error) {
		return userResp{ID: "ok"}, nil
	})
	router := mustServeOne(t, r) // ServeOne's returned handler is already a fully-routed router

	// Invalid UUID → 400.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid path param, got %d", rec.Code)
	}

	// Valid UUID → 200.
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/users/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("want 200 for valid path param, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestSSEHandler_PathParam_codecValidated(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	r := rest.NewSSERoute[getReq, sseEvent]("/stream/{id}",
		getReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).WithHandler(func(ctx context.Context, _ getReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hi"})
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	// Invalid UUID → 400.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid path param, got %d", rec.Code)
	}

	// Valid UUID → 200.
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/stream/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("want 200 for valid path param, got %d", rec2.Code)
	}
}

func TestSSEHandler_EventMerge_FromConnectionVars(t *testing.T) {
	type mergedEvent struct {
		ID     string
		Tenant string
		Trace  string
		SID    string
	}
	evtCodec := codex.Struct[mergedEvent](
		codex.OptionalField("id", codex.String(), func(e mergedEvent) string { return e.ID }, func(e *mergedEvent, v string) { e.ID = v }),
		codex.OptionalField("tenant", codex.String(), func(e mergedEvent) string { return e.Tenant }, func(e *mergedEvent, v string) { e.Tenant = v }),
		codex.OptionalField("trace", codex.String(), func(e mergedEvent) string { return e.Trace }, func(e *mergedEvent, v string) { e.Trace = v }),
		codex.OptionalField("sid", codex.String(), func(e mergedEvent) string { return e.SID }, func(e *mergedEvent, v string) { e.SID = v }),
	)
	uuidCodec := codex.String().Refine(validate.UUID)
	nonEmpty := codex.String().Refine(validate.NonEmptyString)
	r := rest.NewSSERoute[getReq, mergedEvent]("/stream/{id}",
		getReqCodec, evtCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
		rest.QueryParam{Name: "tenant", Required: true, Codec: &nonEmpty},
		rest.HeaderParam{Name: "X-Trace", Required: true, Codec: &nonEmpty},
		rest.CookieParam{Name: "sid", Required: true, Codec: &nonEmpty},
		rest.NewRequiredSSEEventParam("id", codex.String(), func(e mergedEvent) string { return e.ID }, func(e *mergedEvent, v string) { e.ID = v }),
		rest.NewRequiredSSEEventParam("tenant", codex.String(), func(e mergedEvent) string { return e.Tenant }, func(e *mergedEvent, v string) { e.Tenant = v }),
		rest.NewRequiredSSEEventParam("X-Trace", codex.String(), func(e mergedEvent) string { return e.Trace }, func(e *mergedEvent, v string) { e.Trace = v }),
		rest.NewRequiredSSEEventParam("sid", codex.String(), func(e mergedEvent) string { return e.SID }, func(e *mergedEvent, v string) { e.SID = v }),
	).WithHandler(func(_ context.Context, _ getReq, send func(mergedEvent) error) error {
		return send(mergedEvent{ID: "wrong", Tenant: "wrong", Trace: "wrong", SID: "wrong"})
	})
	router := mustServeSSE(t, r, rest.NewServer(testInfo))

	req := httptest.NewRequest(http.MethodGet, "/stream/550e8400-e29b-41d4-a716-446655440000?tenant=acme", nil)
	req.Header.Set("X-Trace", "trace-1")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"550e8400-e29b-41d4-a716-446655440000"`) ||
		!strings.Contains(body, `"tenant":"acme"`) ||
		!strings.Contains(body, `"trace":"trace-1"`) ||
		!strings.Contains(body, `"sid":"sid-1"`) {
		t.Fatalf("expected merged event payload, got: %s", body)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

// Example demonstrates serveOne — an internal helper now that [AttachRouter]
// is the sole public server-side workflow (see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 6); named
// Example() (not ExampleServeOne, which vet would reject for referring to
// an unexported identifier) so it still runs as a documented runnable
// snippet.
func Example() {
	type CreateReq struct{ Name string }
	type Item struct{ ID, Name string }

	reqCodec := codex.Struct[CreateReq](
		codex.RequiredField("name", codex.String(),
			func(r CreateReq) string { return r.Name },
			func(r *CreateReq, v string) { r.Name = v },
		),
	)
	itemCodec := codex.Struct[Item](
		codex.RequiredField("id", codex.String(),
			func(i Item) string { return i.ID },
			func(i *Item, v string) { i.ID = v },
		),
		codex.RequiredField("name", codex.String(),
			func(i Item) string { return i.Name },
			func(i *Item, v string) { i.Name = v },
		),
	)

	route := rest.NewRoute[CreateReq, Item]("POST", "/items",
		reqCodec, itemCodec,
		rest.RouteMeta{OperationID: "createItem", RespStatus: "201"},
	).WithHandler(func(_ context.Context, req CreateReq) (Item, error) {
		return Item{ID: "1", Name: req.Name}, nil
	})

	r, err := serveOne(route)
	if err != nil {
		fmt.Println("register error:", err)
		return
	}

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/items", "application/json",
		strings.NewReader(`{"name":"Widget"}`))
	if err != nil {
		fmt.Println("post error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println(resp.StatusCode)
	// Output:
	// 201
}
