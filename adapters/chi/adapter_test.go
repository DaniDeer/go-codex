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
	h, _ := rest.AddRoute[createReq, userResp](b, "POST", "/users",
		createReqCodec, userRespCodec, rest.RouteConfig{OperationID: "createUser"})
	return h
}

func newGetHandle(path string) *rest.RouteHandle[getReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, _ := rest.AddRoute[getReq, userResp](b, "GET", path,
		getReqCodec, userRespCodec, rest.RouteConfig{OperationID: "getUser"})
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
	h, _ := rest.AddRoute[createReq, userResp](b, "POST", "/users",
		createReqCodec, userRespCodec, rest.RouteConfig{
			ResponseHeaderParams: []rest.ResponseHeaderParam{
				{Name: "Location", Required: true, Codec: &locationCodec},
			},
		})

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
	h, _ := rest.AddRoute[createReq, userResp](b, "POST", "/users",
		createReqCodec, userRespCodec, rest.RouteConfig{
			ResponseCookieParams: []rest.ResponseCookieParam{
				{Name: "session", Required: true, Codec: &sessionCodec},
			},
		})

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

func TestHandler_ContentNegotiation(t *testing.T) {
	jsonFmt := format.JSON[userResp](userRespCodec)
	yamlFmt := format.YAML[userResp](userRespCodec)
	b := rest.NewBuilder(testInfo)
	h, _ := rest.AddRoute[getReq, userResp](b, "GET", "/users",
		getReqCodec, userRespCodec, rest.RouteConfig{}, jsonFmt, yamlFmt)

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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{},
		streamFmt,
		format.JSON(userRespCodec),
	)
	if err != nil {
		t.Fatal(err)
	}
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, strictRespCodec,
		rest.RouteConfig{},
		streamFmt,
	)
	if err != nil {
		t.Fatal(err)
	}
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
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamEvents"})
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
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events2",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamEventsValidate"})
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
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events3",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamRegister"})
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
