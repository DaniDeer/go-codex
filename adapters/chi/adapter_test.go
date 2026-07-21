package chi_test

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

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test types ---

type createReq struct{ Name string }
type userResp struct{ ID, Name string }
type getReq struct{}

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

func newCreateHandle() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).Register(b)
	return h
}

func newGetHandle(path string) *rest.RouteHandle[getReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[getReq, userResp]("GET", path,
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "getUser"}).Register(b)
	return h
}

func decodeJSON(t *testing.T, body io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// --- tests ---

func TestHandler_Post_Success(t *testing.T) {
	h := newCreateHandle()
	srv := httptest.NewServer(chiadapter.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{}))
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
	h := newCreateHandle()
	srv := httptest.NewServer(chiadapter.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, chiadapter.Options{}))
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
	h := newGetHandle("/users/{id}")
	handler := chiadapter.Handler(h, func(ctx context.Context, _ getReq) (userResp, error) {
		r, _ := chiadapter.RequestFromContext(ctx)
		id := gochi.URLParam(r, "id")
		return userResp{ID: id, Name: "Alice"}, nil
	}, chiadapter.Options{})

	r := gochi.NewRouter()
	r.Get("/users/{id}", handler)

	srv := httptest.NewServer(r)
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

	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var gotID string
	handler := chiadapter.Handler(handle, func(_ context.Context, r getUserReq) (userResp, error) {
		gotID = r.ID // no chi.URLParam() call needed — already merged
		return userResp{ID: r.ID, Name: "Alice"}, nil
	}, chiadapter.Options{})

	router := gochi.NewRouter()
	router.Get("/users/{id}", handler)
	srv := httptest.NewServer(router)
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
	h := newGetHandle("/users/{id}")
	if len(h.MergeFields()) != 0 {
		t.Fatalf("expected no merge fields for a plain route, got %d", len(h.MergeFields()))
	}
	handler := chiadapter.Handler(h, func(ctx context.Context, _ getReq) (userResp, error) {
		r, _ := chiadapter.RequestFromContext(ctx)
		id := gochi.URLParam(r, "id")
		return userResp{ID: id, Name: "Alice"}, nil
	}, chiadapter.Options{})

	router := gochi.NewRouter()
	router.Get("/users/{id}", handler)
	srv := httptest.NewServer(router)
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

func TestRegister_WiresOntoRouter(t *testing.T) {
	h := newGetHandle("/users/{id}")
	r := gochi.NewRouter()
	chiadapter.Register(r, h, func(ctx context.Context, _ getReq) (userResp, error) {
		rr, _ := chiadapter.RequestFromContext(ctx)
		id := gochi.URLParam(rr, "id")
		return userResp{ID: id, Name: "Bob"}, nil
	}, chiadapter.Options{})

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
	b := rest.NewBuilder(testInfo)
	locationCodec := codex.String().Refine(validate.MinLen(1))
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ResponseHeaderParam{Name: "Location", Required: true, Codec: &locationCodec},
	).Register(b)

	srv := httptest.NewServer(chiadapter.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		header := make(http.Header)
		header.Set("Location", "/users/1")
		chiadapter.WithResponseHeaders(ctx, header)
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{}))
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
	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ResponseCookieParam{Name: "session", Required: true, Codec: &sessionCodec},
	).Register(b)

	srv := httptest.NewServer(chiadapter.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		chiadapter.WithResponseCookies(ctx, chiadapter.PendingCookie{
			Name: "session", Value: "abcdefgh",
			Opts: chiadapter.CookieOptions{MaxAge: 3600, Insecure: true},
		})
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{}))
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

// userRespWithMeta carries response header/cookie merge fields alongside
// the JSON body fields (Round 3 — response merge fields).
type userRespWithMeta struct {
	ID        string
	Name      string
	RequestID string
	Session   string
}

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
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(chiadapter.Handler(h, func(_ context.Context, req createReq) (userRespWithMeta, error) {
		return userRespWithMeta{ID: "1", Name: req.Name, RequestID: "req-999", Session: "sess-xyz"}, nil
	}, chiadapter.Options{}))
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

// R7 (chi): Handler route WITHOUT response merge fields behaves
// byte-for-byte identically to today — regression guard.
func TestHandler_ResponseMergeFields_NoneDeclaredIsUnaffected_Chi(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(chiadapter.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{}))
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
	b := rest.NewBuilder(testInfo)
	h, _ := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec).Register(b)
	h.WithFormats(jsonFmt, yamlFmt)

	handler := chiadapter.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "1", Name: "Alice"}, nil
	}, chiadapter.Options{})

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
	b := rest.NewBuilder(testInfo)
	streamFmt := format.NewStreamed(userRespCodec,
		func(v userResp, w io.Writer) error {
			_, err := fmt.Fprintf(w, "id=%s name=%s", v.ID, v.Name)
			return err
		},
		func([]byte) (userResp, error) { return userResp{}, errors.New("not decodable") },
		"text/plain",
	)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(streamFmt, format.JSON(userRespCodec))
	handler := chiadapter.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "42", Name: req.Name}, nil
	}, chiadapter.Options{})

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
	b := rest.NewBuilder(testInfo)
	streamFmt := format.NewStreamed(strictRespCodec,
		func(v userResp, w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s", v.ID)
			return err
		},
		func([]byte) (userResp, error) { return userResp{}, nil },
		"text/plain",
	)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, strictRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(streamFmt)
	handler := chiadapter.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{ID: "1", Name: ""}, nil // Name="" fails NonEmptyString
	}, chiadapter.Options{})

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
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))

	r := gochi.NewRouter()
	chiadapter.Register(r, h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRequestFormats_YAMLBodyAccepted(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))

	r := gochi.NewRouter()
	chiadapter.Register(r, h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{})

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

func TestRequestFormats_WrongContentType_returns415(t *testing.T) {
	var capturedErr error
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))

	r := gochi.NewRouter()
	chiadapter.Register(r, h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
			capturedErr = e
			w.WriteHeader(status)
		},
	})

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

func TestSSEHandler_streamEvents(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	handler := chiadapter.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		for _, msg := range []string{"hello", "world"} {
			if err := send(sseEvent{Message: msg}); err != nil {
				return err
			}
		}
		return nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	handler(rec, r)

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
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events2",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEventsValidate"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	var sendErr error
	handler := chiadapter.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		sendErr = send(sseEvent{Message: ""})
		return nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events2", nil)
	handler(rec, r)

	if sendErr == nil {
		t.Fatal("expected send to return validation error for empty message")
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("expected empty body on validation rejection, got: %s", body)
	}
}

func TestSSEHandler_RegisterSSE_wiresOntoRouter(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events3",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamRegister"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	r := gochi.NewRouter()
	chiadapter.RegisterSSE(r, handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "chi-registered"})
	}, chiadapter.Options{})

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

func newSecuredRoute() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	})
	return rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{
			OperationID: "createUser",
			Security:    []route.SecurityRequirement{route.Require("bearerAuth")},
		},
	).Register(b)
}

func TestHandler_SecurityFunc_calledForSecuredRoute(t *testing.T) {
	handle, err := newSecuredRoute()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, r *http.Request, _ []route.SecurityRequirement) error {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "Bearer valid-token" {
				return errors.New("unauthorized")
			}
			return nil
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer valid-token")
	h(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for secured route")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_rejectsRequest(t *testing.T) {
	handle, err := newSecuredRoute()
	if err != nil {
		t.Fatal(err)
	}
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called when security rejects")
		return userResp{}, nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, _ *http.Request, _ []route.SecurityRequirement) error {
			return errors.New("unauthorized")
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer valid-token")
	h(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_notCalledForUnsecuredRoute(t *testing.T) {
	handle := newCreateHandle()
	secFuncCalled := false
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, _ *http.Request, _ []route.SecurityRequirement) error {
			secFuncCalled = true
			return nil
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h(rec, r)

	if secFuncCalled {
		t.Error("want SecurityFunc NOT called for unsecured route")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_codecValidationFailure(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	jwtCodec := codex.String().Refine(validate.JWT)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
		Codec:          &jwtCodec,
	})
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{
			OperationID: "createUser",
			Security:    []route.SecurityRequirement{route.Require("bearerAuth")},
		},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called when credential fails codec")
		return userResp{}, nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	// No Authorization header — extracted credential will be empty → invalid JWT
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestHandler_SecurityObserver_calledOnRejection(t *testing.T) {
	handle, err := newSecuredRoute()
	if err != nil {
		t.Fatal(err)
	}
	obs := &mockSecurityObserver{}
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, chiadapter.Options{
		Observer: obs,
		SecurityFunc: func(_ context.Context, _ *http.Request, _ []route.SecurityRequirement) error {
			return errors.New("unauthorized")
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer valid-token")
	h(rec, r)

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

func newGlobalSecuredChiRoute() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	return rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).Register(b)
}

func TestChiHandler_GlobalSecurity_enforcedWhenNoPerRouteSecurity(t *testing.T) {
	handle, err := newGlobalSecuredChiRoute()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, r *http.Request, _ []route.SecurityRequirement) error {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "Bearer valid-token" {
				return errors.New("unauthorized")
			}
			return nil
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer valid-token")
	h.ServeHTTP(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for route inheriting global security")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestChiHandler_GlobalSecurity_rejectsWhenNoToken(t *testing.T) {
	handle, err := newGlobalSecuredChiRoute()
	if err != nil {
		t.Fatal(err)
	}
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, _ *http.Request, _ []route.SecurityRequirement) error {
			return errors.New("missing token")
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 from global security, got %d", rec.Code)
	}
}

func TestChiHandler_GlobalSecurity_notCalledWhenExplicitlyEmpty(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{
			OperationID: "createUser",
			Security:    []route.SecurityRequirement{},
		},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	h := chiadapter.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, _ *http.Request, _ []route.SecurityRequirement) error {
			secFuncCalled = true
			return nil
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if secFuncCalled {
		t.Error("want SecurityFunc NOT called for route with explicit empty Security")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

// --- SSE security + param validation tests ---

func newGlobalSecuredSSERoute() (*rest.SSERouteHandle[createReq, sseEvent], error) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	return rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamSecured"},
	).Register(b)
}

func TestChiSSEHandler_GlobalSecurity_enforced(t *testing.T) {
	handle, err := newGlobalSecuredSSERoute()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	h := chiadapter.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, r *http.Request, _ []route.SecurityRequirement) error {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "Bearer valid-token" {
				return errors.New("unauthorized")
			}
			return nil
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("Authorization", "Bearer valid-token")
	h.ServeHTTP(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for SSE route inheriting global security")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestChiSSEHandler_GlobalSecurity_rejectsWhenNoToken(t *testing.T) {
	handle, err := newGlobalSecuredSSERoute()
	if err != nil {
		t.Fatal(err)
	}
	h := chiadapter.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, chiadapter.Options{
		SecurityFunc: func(_ context.Context, _ *http.Request, _ []route.SecurityRequirement) error {
			return errors.New("missing token")
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 from SSE global security, got %d", rec.Code)
	}
}

func TestChiSSEHandler_QueryParam_rejectsInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := chiadapter.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream?id=not-a-uuid", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE query param, got %d", rec.Code)
	}
}

func TestChiSSEHandler_QueryParam_allowsValid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := chiadapter.SSEHandler(handle, func(_ context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "ok"})
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream?id=f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200 for valid SSE query param, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestChiSSEHandler_CookieParam_rejectsInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.CookieParam{Name: "session", Codec: &tokenCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := chiadapter.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: ""})
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE cookie param, got %d", rec.Code)
	}
}

func TestChiSSEHandler_HeaderParam_rejectsInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.HeaderParam{Name: "X-Request-Id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := chiadapter.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("X-Request-Id", "not-a-uuid")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE header param, got %d", rec.Code)
	}
}

func TestSSEHandler_ResponseHeaderParam_appearsOnFirstSend(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	traceCodec := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream-rh",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &traceCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	router := gochi.NewRouter()
	chiadapter.RegisterSSE(router, handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		extra := make(http.Header)
		extra.Set("X-Trace-Id", "trace-abc-123")
		chiadapter.WithResponseHeaders(ctx, extra)
		return send(sseEvent{Message: "hello"})
	}, chiadapter.Options{})

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
	b := rest.NewBuilder(testInfo)
	traceCodec := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream-rh2",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &traceCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	sendCalled := false
	h := chiadapter.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		extra := make(http.Header)
		extra.Set("X-Trace-Id", "") // empty — codec rejects it
		chiadapter.WithResponseHeaders(ctx, extra)
		err := send(sseEvent{Message: "should not appear"})
		if err != nil {
			sendCalled = true
			return err
		}
		return nil
	}, chiadapter.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream-rh2", nil)
	h.ServeHTTP(rec, r)

	if !sendCalled {
		t.Error("want send to return error for invalid response header codec, but send was not called")
	}
	if strings.Contains(rec.Body.String(), "should not appear") {
		t.Errorf("want no event data written when response header codec fails, got: %s", rec.Body.String())
	}
}

func TestHandler_PathParam_codecValidated(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := chiadapter.Handler(handle, func(_ context.Context, r getReq) (userResp, error) {
		return userResp{ID: "ok"}, nil
	}, chiadapter.Options{})
	router := gochi.NewRouter()
	router.Get("/users/{id}", h)

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
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[getReq, sseEvent]("/stream/{id}",
		getReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := chiadapter.SSEHandler(handle, func(ctx context.Context, _ getReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hi"})
	}, chiadapter.Options{})
	router := gochi.NewRouter()
	router.Get("/stream/{id}", h)

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
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	nonEmpty := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[getReq, mergedEvent]("/stream/{id}",
		getReqCodec, evtCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
		rest.QueryParam{Name: "tenant", Required: true, Codec: &nonEmpty},
		rest.HeaderParam{Name: "X-Trace", Required: true, Codec: &nonEmpty},
		rest.CookieParam{Name: "sid", Required: true, Codec: &nonEmpty},
		rest.NewRequiredSSEEventParam("id", codex.String(), func(e mergedEvent) string { return e.ID }, func(e *mergedEvent, v string) { e.ID = v }),
		rest.NewRequiredSSEEventParam("tenant", codex.String(), func(e mergedEvent) string { return e.Tenant }, func(e *mergedEvent, v string) { e.Tenant = v }),
		rest.NewRequiredSSEEventParam("X-Trace", codex.String(), func(e mergedEvent) string { return e.Trace }, func(e *mergedEvent, v string) { e.Trace = v }),
		rest.NewRequiredSSEEventParam("sid", codex.String(), func(e mergedEvent) string { return e.SID }, func(e *mergedEvent, v string) { e.SID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	r := gochi.NewRouter()
	r.Get("/stream/{id}", chiadapter.SSEHandler(handle, func(_ context.Context, _ getReq, send func(mergedEvent) error) error {
		return send(mergedEvent{ID: "wrong", Tenant: "wrong", Trace: "wrong", SID: "wrong"})
	}, chiadapter.Options{}))

	req := httptest.NewRequest(http.MethodGet, "/stream/550e8400-e29b-41d4-a716-446655440000?tenant=acme", nil)
	req.Header.Set("X-Trace", "trace-1")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
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

func ExampleRegister() {
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

	b := rest.NewBuilder(rest.Info{Title: "Example API", Version: "1.0.0"})
	handle, err := rest.NewRoute[CreateReq, Item]("POST", "/items",
		reqCodec, itemCodec,
		rest.RouteMeta{OperationID: "createItem", RespStatus: "201"},
	).Register(b)
	if err != nil {
		fmt.Println("register error:", err)
		return
	}

	r := gochi.NewRouter()
	chiadapter.Register(r, handle, func(_ context.Context, req CreateReq) (Item, error) {
		return Item{ID: "1", Name: req.Name}, nil
	}, chiadapter.Options{})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/items", "application/json",
		strings.NewReader(`{"name":"Widget"}`))
	defer resp.Body.Close()
	fmt.Println(resp.StatusCode)
	// Output:
	// 201
}
