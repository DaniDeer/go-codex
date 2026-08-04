package nethttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- helpers ---

// newClientCreateRoute returns a POST /users route handle via Register (shared contract path).
func newClientCreateRoute() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newClientGetRoute returns a GET /users/{id} route handle via ClientHandle (client-only path).
func newClientGetRoute() *rest.RouteHandle[getReq, userResp] {
	return rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
	).ClientHandle()
}

// testObserver records RecordRequest calls for assertion.
type testObserver struct {
	stats.NoopObserver
	method   string
	path     string
	status   int
	duration time.Duration
	called   bool
	valErrs  []struct{ location, constraint, field string }
}

func (o *testObserver) RecordRequest(method, path string, statusCode int, duration time.Duration) {
	o.method = method
	o.path = path
	o.status = statusCode
	o.duration = duration
	o.called = true
}

func (o *testObserver) RecordValidationError(location, constraint, field string) {
	o.valErrs = append(o.valErrs, struct{ location, constraint, field string }{location, constraint, field})
}

// --- happy path ---

func TestCall_POST_HappyPath(t *testing.T) {
	handle := newClientCreateRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/users" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": body["name"].(string)}) //nolint:errcheck
	}))
	defer srv.Close()

	obs := &testObserver{}
	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil,
		nethttp.CallOptions{Observer: obs})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Alice" || resp.ID != "1" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if !obs.called {
		t.Error("observer RecordRequest not called")
	}
	if obs.method != "POST" {
		t.Errorf("observer method = %q, want POST", obs.method)
	}
	if obs.status != http.StatusOK {
		t.Errorf("observer status = %d, want 200", obs.status)
	}
}

func TestCall_GET_HappyPath(t *testing.T) {
	handle := newClientGetRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "abc", "name": "Bob"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, map[string]string{"id": "abc"},
		nethttp.CallOptions{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "abc" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// --- non-2xx response ---

func TestCall_UnexpectedStatus(t *testing.T) {
	handle := newClientCreateRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	obs := &testObserver{}
	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil,
		nethttp.CallOptions{Observer: obs})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", statusErr.StatusCode)
	}
	if statusErr.Method != "POST" {
		t.Errorf("method = %q, want POST", statusErr.Method)
	}
	if statusErr.Path != "/users" {
		t.Errorf("path = %q, want /users", statusErr.Path)
	}
	if !strings.Contains(string(statusErr.Body), "not found") {
		t.Errorf("body = %q, want to contain 'not found'", statusErr.Body)
	}
	if obs.status != http.StatusNotFound {
		t.Errorf("observer status = %d, want 404", obs.status)
	}
}

// TestCall_UnexpectedStatus_HeaderPopulated verifies UnexpectedStatusError
// exposes the raw response header set on a non-2xx response — the
// declarative escape hatch for challenge-response flows (e.g.
// WWW-Authenticate on a 401) that need a response header Call's normal
// success-path header merge (rest.NewRequiredResponseHeaderParam) cannot
// reach, since that only applies to 2xx responses.
func TestCall_UnexpectedStatus_HeaderPopulated(t *testing.T) {
	handle := newClientCreateRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://auth.example.com/token"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil, nethttp.CallOptions{})

	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if got := statusErr.Header.Get("WWW-Authenticate"); got != `Bearer realm="https://auth.example.com/token"` {
		t.Errorf("Header.Get(WWW-Authenticate) = %q, unexpected", got)
	}
}

// --- path parameter validation ---

func TestCall_PathParamValidation_EmptyID(t *testing.T) {
	handle := newClientGetRoute() // id param requires NonEmptyString

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, map[string]string{"id": ""},
		nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected path param error, got nil")
	}
	var pathErr rest.PathParamError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected rest.PathParamError, got %T: %v", err, err)
	}
	if pathErr.Name != "id" {
		t.Errorf("param name = %q, want 'id'", pathErr.Name)
	}
}

func TestCall_PathParamValidation_MissingVar(t *testing.T) {
	handle := newClientGetRoute()

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil, // nil vars — {id} missing
		nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected missing path var error, got nil")
	}
	var missingErr rest.MissingPathVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected rest.MissingPathVarError, got %T: %v", err, err)
	}
}

// --- query parameter validation ---

func TestCall_QueryParamValidation(t *testing.T) {
	qpCodec := codex.String().Refine(validate.NonEmptyString)
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
		rest.QueryParam{Name: "filter", Required: true}.WithCodec(qpCodec),
	).ClientHandle()

	obs := &testObserver{}
	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			QueryParams: map[string]string{"filter": ""},
			Observer:    obs,
		})

	if err == nil {
		t.Fatal("expected query param error, got nil")
	}
	var qpErr rest.QueryParamError
	if !errors.As(err, &qpErr) {
		t.Fatalf("expected rest.QueryParamError, got %T: %v", err, err)
	}
	if qpErr.Name != "filter" {
		t.Errorf("param name = %q, want 'filter'", qpErr.Name)
	}
	if len(obs.valErrs) == 0 {
		t.Error("expected observer to receive validation error")
	}
}

// --- cookie parameter validation ---

func TestCall_CookieParamValidation(t *testing.T) {
	cookieCodec := codex.String().Refine(validate.NonEmptyString)
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
		rest.CookieParam{Name: "session", Required: true}.WithCodec(cookieCodec),
	).ClientHandle()

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			CookieParams: map[string]string{"session": ""},
		})

	if err == nil {
		t.Fatal("expected cookie param error, got nil")
	}
	var cpErr rest.CookieParamError
	if !errors.As(err, &cpErr) {
		t.Fatalf("expected rest.CookieParamError, got %T: %v", err, err)
	}
	if cpErr.Name != "session" {
		t.Errorf("param name = %q, want 'session'", cpErr.Name)
	}
}

// --- header parameter validation ---

func TestCall_HeaderParamValidation(t *testing.T) {
	headerCodec := codex.String().Refine(validate.NonEmptyString)
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
		rest.HeaderParam{Name: "X-Tenant-ID", Required: true}.WithCodec(headerCodec),
	).ClientHandle()

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			HeaderParams: map[string]string{"X-Tenant-ID": ""},
		})

	if err == nil {
		t.Fatal("expected header param error, got nil")
	}
	var hErr rest.HeaderParamError
	if !errors.As(err, &hErr) {
		t.Fatalf("expected rest.HeaderParamError, got %T: %v", err, err)
	}
	if hErr.Name != "X-Tenant-ID" {
		t.Errorf("param name = %q, want 'X-Tenant-ID'", hErr.Name)
	}
}

// --- CredentialFunc ---

func TestCall_CredentialFunc_Invoked(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	credCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, nil,
		nethttp.CallOptions{
			CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
				credCalled = true
				h := make(http.Header)
				h.Set("Authorization", "Bearer test-token")
				return h, nil
			},
		})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !credCalled {
		t.Error("CredentialFunc was not called")
	}
	if resp.ID != "me" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCall_CredentialFunc_Error(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	credErr := errors.New("token expired")
	_, callErr := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
				return nil, credErr
			},
		})

	if !errors.Is(callErr, credErr) {
		t.Fatalf("expected credential error, got %v", callErr)
	}
}

// --- Observer RecordRequest on validation failure ---

func TestCall_Observer_RecordRequest_OnValidationFailure(t *testing.T) {
	handle := newClientGetRoute()
	obs := &testObserver{}

	// Missing path var → validation error before any request is sent.
	nethttp.Call(context.Background(), http.DefaultClient, "http://localhost", //nolint:errcheck
		handle, getReq{}, nil,
		nethttp.CallOptions{Observer: obs})

	if !obs.called {
		t.Error("observer RecordRequest not called on validation failure")
	}
	if obs.status != 0 {
		t.Errorf("observer status = %d, want 0 (no request sent)", obs.status)
	}
}

// --- ClientHandle (client-only, no builder) ---

func TestCall_ClientHandle_NoBuilder(t *testing.T) {
	handle := rest.NewRoute[createReq, userResp]("POST", "/items",
		createReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		name, _ := body["name"].(string)
		json.NewEncoder(w).Encode(map[string]string{"id": "42", "name": name}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Widget"}, nil,
		nethttp.CallOptions{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Widget" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// --- query params appended to URL ---

func TestCall_QueryParams_AppendedToURL(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("limit")
		if q != "10" {
			t.Errorf("query param limit = %q, want '10'", q)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "x"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, nil,
		nethttp.CallOptions{QueryParams: map[string]string{"limit": "10"}})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ExtraHeaders sent ---

func TestCall_ExtraHeaders_Sent(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") != "req-123" {
			t.Errorf("X-Request-ID header missing or wrong")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me"}) //nolint:errcheck
	}))
	defer srv.Close()

	extra := make(http.Header)
	extra.Set("X-Request-ID", "req-123")
	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, nil,
		nethttp.CallOptions{ExtraHeaders: extra})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Transport error types: Error() string and Unwrap() ---

func TestRequestBuildError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("invalid url")
	e := nethttp.RequestBuildError{Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted nethttp.RequestBuildError
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.As(wrapped, &extracted) {
		t.Error("errors.As must extract RequestBuildError through wrapping")
	}
	if extracted.Err != cause {
		t.Errorf("extracted.Err = %v, want %v", extracted.Err, cause)
	}
}

func TestRequestError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("connection refused")
	e := nethttp.RequestError{Method: "GET", Path: "/users/{id}", Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted nethttp.RequestError
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.As(wrapped, &extracted) {
		t.Error("errors.As must extract RequestError through wrapping")
	}
	if extracted.Method != "GET" || extracted.Path != "/users/{id}" {
		t.Errorf("extracted = {%s %s}, want {GET /users/{id}}", extracted.Method, extracted.Path)
	}
}

func TestResponseBodyError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("unexpected EOF")
	e := nethttp.ResponseBodyError{Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted nethttp.ResponseBodyError
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.As(wrapped, &extracted) {
		t.Error("errors.As must extract ResponseBodyError through wrapping")
	}
	if extracted.Err != cause {
		t.Errorf("extracted.Err = %v, want %v", extracted.Err, cause)
	}
}

// --- Response merge fields + CallHandle (Round 3) ---

// getUserActivityReq declares BOTH a path merge field (id) and a query
// merge field (filter) on Req — used by the CallHandle role-leakage tests.
type getUserActivityReq struct {
	ID     string
	Filter string
}

// newClientActivityRoute returns a GET /users/{id}/activity route with a
// path merge field, a query merge field, a required response header merge
// field, and an optional response cookie merge field — exercising every
// role in a single route for the R8-R11 test matrix.
func newClientActivityRoute() *rest.RouteHandle[getUserActivityReq, userRespWithMeta] {
	return rest.NewRoute[getUserActivityReq, userRespWithMeta]("GET", "/users/{id}/activity",
		codex.Struct[getUserActivityReq](), userRespWithMetaBodyCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getUserActivityReq) string { return r.ID },
			func(r *getUserActivityReq, v string) { r.ID = v }),
		rest.NewOptionalQueryParam("filter", codex.String(),
			func(r getUserActivityReq) string { return r.Filter },
			func(r *getUserActivityReq, v string) { r.Filter = v }),
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).ClientHandle()
}

// R8: nethttp.Call decodes response headers/cookies into Resp when response
// merge fields are registered — round-trip: server sets header/cookie from
// a fixed value, client reads it back into the SAME struct fields.
func TestCall_ResponseMergeFields_DecodesIntoResp(t *testing.T) {
	handle := newClientActivityRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-777")
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "sess-777"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Alice"}`))
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getUserActivityReq{}, map[string]string{"id": "u1"}, nethttp.CallOptions{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.RequestID != "req-777" {
		t.Errorf("RequestID: want %q, got %q", "req-777", resp.RequestID)
	}
	if resp.Session != "sess-777" {
		t.Errorf("Session: want %q, got %q", "sess-777", resp.Session)
	}
}

// R9: nethttp.CallHandle happy path — a route with path+query merge fields
// on Req derives BOTH the path var and the query param from ONE call,
// with no cross-role leakage (mirrors TestClientEncode_RoleAwareMergeFields_NoLeakage
// in api/rest, extended here through the full client stack).
func TestCallHandle_HappyPath_NoLeakage(t *testing.T) {
	handle := newClientActivityRoute()
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("filter")
		if r.URL.Query().Get("id") != "" {
			t.Errorf("path value leaked into query string: %q", r.URL.Query().Get("id"))
		}
		w.Header().Set("X-Request-Id", "req-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Alice"}`))
	}))
	defer srv.Close()

	resp, err := nethttp.CallHandle(context.Background(), srv.Client(), srv.URL,
		handle, getUserActivityReq{ID: "u1", Filter: "logins"}, nethttp.CallOptions{})
	if err != nil {
		t.Fatalf("CallHandle: %v", err)
	}
	if gotPath != "/users/u1/activity" {
		t.Errorf("path: want %q, got %q", "/users/u1/activity", gotPath)
	}
	if gotQuery != "logins" {
		t.Errorf("query filter: want %q, got %q", "logins", gotQuery)
	}
	if resp.RequestID != "req-1" {
		t.Errorf("RequestID: want %q, got %q", "req-1", resp.RequestID)
	}
}

// R10: explicit opts.QueryParams/etc. take precedence over the value
// CallHandle derives from req for the same key.
func TestCallHandle_ExplicitOptsOverridePrecedence(t *testing.T) {
	handle := newClientActivityRoute()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("filter")
		w.Header().Set("X-Request-Id", "req-2")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"u1","name":"Alice"}`))
	}))
	defer srv.Close()

	_, err := nethttp.CallHandle(context.Background(), srv.Client(), srv.URL,
		handle, getUserActivityReq{ID: "u1", Filter: "logins"},
		nethttp.CallOptions{QueryParams: map[string]string{"filter": "overridden"}})
	if err != nil {
		t.Fatalf("CallHandle: %v", err)
	}
	if gotQuery != "overridden" {
		t.Errorf("query filter: want explicit override %q, got %q", "overridden", gotQuery)
	}
}

// R11: CallHandle with zero merge fields declared behaves like
// Call(ctx, client, baseURL, handle, req, nil, opts) — regression guard.
func TestCallHandle_NoMergeFieldsMatchesCall(t *testing.T) {
	handle := newClientCreateRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"1","name":"Alice"}`))
	}))
	defer srv.Close()

	viaCall, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil, nethttp.CallOptions{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	viaCallHandle, err := nethttp.CallHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nethttp.CallOptions{})
	if err != nil {
		t.Fatalf("CallHandle: %v", err)
	}
	if viaCall != viaCallHandle {
		t.Errorf("CallHandle should match plain Call when no merge fields declared: %+v vs %+v", viaCall, viaCallHandle)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleCall() {
	// Define the route — use ClientHandle() when no OpenAPI spec is needed.
	type Item struct{ ID, Name string }
	itemCodec := codex.Struct[Item](
		codex.OptionalField("id", codex.String(),
			func(i Item) string { return i.ID },
			func(i *Item, v string) { i.ID = v },
		),
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(i Item) string { return i.Name },
			func(i *Item, v string) { i.Name = v },
		),
	)
	getRoute := rest.NewRoute[getReq, Item]("GET", "/items/{id}",
		codex.Struct[getReq](), itemCodec,
		rest.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
	).ClientHandle()

	// Validate path params before any HTTP call.
	_, err := nethttp.Call(context.Background(), http.DefaultClient, "https://api.example.com",
		getRoute, getReq{}, map[string]string{"id": ""},
		nethttp.CallOptions{})
	if err != nil {
		var pathErr rest.PathParamError
		if errors.As(err, &pathErr) {
			fmt.Printf("param %q rejected: %v\n", pathErr.Name, pathErr.Err)
		}
	}
	// Output: param "id" rejected: constraint failed (non-empty): expected non-empty string
}

// --- client-side error decode parity (ErrorPatternResponse) ---

type clientErrPayload struct {
	Code string `json:"code"`
}

func (e clientErrPayload) Error() string { return "client error " + e.Code }

var clientErrPayloadCodec = codex.Struct[clientErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e clientErrPayload) string { return e.Code },
		func(e *clientErrPayload, v string) { e.Code = v },
	),
)

// newClientErrorPatternRoute returns a route declaring an ErrorPattern for
// status 409, whose payload type is clientErrPayload.
func newClientErrorPatternRoute() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-call",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[clientErrPayload, clientErrPayload](http.StatusConflict, clientErrPayloadCodec),
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// CDP1: Call returns ErrorPatternResponse when the declared pattern matches.
func TestCall_ErrorPatternResponse_MatchedPattern(t *testing.T) {
	handle := newClientErrorPatternRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil, nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var epr nethttp.ErrorPatternResponse
	if !errors.As(err, &epr) {
		t.Fatalf("expected ErrorPatternResponse, got %T: %v", err, err)
	}
	if epr.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", epr.StatusCode)
	}
	payload, ok := epr.Value.(clientErrPayload)
	if !ok {
		t.Fatalf("Value type = %T, want clientErrPayload", epr.Value)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

// CDP5 (integration side): a matching status whose body fails to decode
// against the declared codec falls back unchanged to UnexpectedStatusError.
func TestCall_ErrorPatternResponse_DecodeFailureFallsBackToUnexpectedStatus(t *testing.T) {
	handle := newClientErrorPatternRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		// "code" is required non-empty; this violates the declared codec.
		_, _ = w.Write([]byte(`{"code":""}`))
	}))
	defer srv.Close()

	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil, nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var epr nethttp.ErrorPatternResponse
	if errors.As(err, &epr) {
		t.Fatalf("expected fallback to UnexpectedStatusError, got ErrorPatternResponse: %+v", epr)
	}
	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", statusErr.StatusCode)
	}
}

// Unmatched status (no declared pattern) still falls back to
// UnexpectedStatusError unchanged — regression guard for non-opted-in callers.
func TestCall_ErrorPatternResponse_NoMatch_FallsBackToUnexpectedStatus(t *testing.T) {
	handle := newClientErrorPatternRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil, nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", statusErr.StatusCode)
	}
}

// CDP8: ErrorPatternResponse.LogValue shape.
func TestErrorPatternResponse_LogValue(t *testing.T) {
	epr := nethttp.ErrorPatternResponse{
		StatusCode: http.StatusConflict,
		Value:      clientErrPayload{Code: "conflict"},
		Body:       []byte(`{"code":"conflict"}`),
	}
	v := epr.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("LogValue kind = %v, want Group", v.Kind())
	}
	attrs := v.Group()
	found := map[string]bool{}
	for _, a := range attrs {
		found[a.Key] = true
	}
	if !found["status"] || !found["value"] {
		t.Fatalf("LogValue attrs = %+v, want status and value keys", attrs)
	}
}
