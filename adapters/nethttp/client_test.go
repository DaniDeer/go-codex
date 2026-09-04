package nethttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- helpers ---

// newClientCreateRoute returns a POST /users route handle via Register (shared contract path).
func newClientCreateRoute() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewServer(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).RegisterHandle(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newClientGetRoute returns a GET /users/{id} route handle via ClientHandle
// (client-only path) with a PLAIN (non-merge-capable) PathParam — used
// SPECIFICALLY to exercise the new design's "no manual-vars fallback"
// tradeoff: a route with a path template var but NO declared merge field
// fails with rest.MissingPathVarError at Call time, since CallWithHandle
// only ever derives vars from merge fields.
func newClientGetRoute() *rest.RouteHandle[getReq, userResp] {
	return rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
	).ClientHandle()
}

// getByIDReq carries a path merge field — used by tests needing an ACTUAL
// path value to flow through (getReq is an empty struct and can't).
type getByIDReq struct{ ID string }

var getByIDReqCodec = codex.Struct[getByIDReq]()

// newClientGetByIDRoute returns a GET /users/{id} route handle with a
// path MERGE field (rest.NewPathParam) — CallWithHandle derives the path
// value directly from req.ID.
func newClientGetByIDRoute() *rest.RouteHandle[getByIDReq, userResp] {
	return rest.NewRoute[getByIDReq, userResp]("GET", "/users/{id}",
		getByIDReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getByIDReq) string { return r.ID },
			func(r *getByIDReq, v string) { r.ID = v }),
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
	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"},
		CallOptions{Observer: obs})

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
	handle := newClientGetByIDRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "abc", "name": "Bob"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getByIDReq{ID: "abc"},
		CallOptions{})

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
	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"},
		CallOptions{Observer: obs})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var statusErr UnexpectedStatusError
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

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if got := statusErr.Header.Get("WWW-Authenticate"); got != `Bearer realm="https://auth.example.com/token"` {
		t.Errorf("Header.Get(WWW-Authenticate) = %q, unexpected", got)
	}
}

// --- path parameter validation ---

// TestCall_PathParamValidation_EmptyID exercises a merge-field-declared
// path param's constraint failure — this now surfaces as
// codex.ValidationError (from codex.EncodeVars, run at merge-DERIVE time,
// BEFORE the vars map is even built), not rest.PathParamError (which
// only ever fires from BuildPath's re-validation, unreachable here since
// EncodeVars already rejects the value first). This is an inherent
// consequence of the new "no manual-vars fallback" design: an invalid
// merge-field value is caught EARLIER than the old raw-vars flow could
// catch it.
func TestCall_PathParamValidation_EmptyID(t *testing.T) {
	handle := newClientGetByIDRoute() // id param requires NonEmptyString

	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getByIDReq{ID: ""},
		CallOptions{})

	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var valErr codex.ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected codex.ValidationError, got %T: %v", err, err)
	}
	if valErr.Field != "id" {
		t.Errorf("field = %q, want 'id'", valErr.Field)
	}
}

// TestCall_PathParamValidation_MissingVar exercises the new design's "no
// manual-vars fallback" tradeoff directly: newClientGetRoute's path
// template declares {id}, but with NO merge field — CallWithHandle
// derives an EMPTY vars map (nothing to derive from), so BuildPath fails
// with rest.MissingPathVarError exactly as if the caller had passed nil
// vars under the old API.
func TestCall_PathParamValidation_MissingVar(t *testing.T) {
	handle := newClientGetRoute()

	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{},
		CallOptions{})

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
	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{},
		CallOptions{
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

	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{},
		CallOptions{
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

	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{},
		CallOptions{
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
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	credCalled := false
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientMW(&declMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	}).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "test-bearer-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{})

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
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	credErr := errors.New("token expired")
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientMW(&declMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		return nil, credErr
	}).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	_, callErr := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, CallOptions{})

	if !errors.Is(callErr, credErr) {
		t.Fatalf("expected credential error, got %v", callErr)
	}
}

// TestCall_WrongShapeMiddleware_ReturnsMiddlewareShapeError locks in that
// Call fails LOUDLY (a typed middleware.MiddlewareShapeError, before any
// network activity) for a mismatched-shape Fn, rather than silently
// ignoring it the way mergeCredentialHeaders' own type assertion would —
// mirrors Handler/Register's eager validateMiddlewareShapes on the server
// side.
func TestCall_WrongShapeMiddleware_ReturnsMiddlewareShapeError(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).
		ClientMW(nil, func(http.Handler) http.Handler { return nil }). // server-side shape, wrong for Call
		ClientHandle()

	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, CallOptions{})

	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want MiddlewareShapeError, got %v", err)
	}
	if shapeErr.Name != "fulfill:general#0" {
		t.Errorf("want Name %q, got %q", "fulfill:general#0", shapeErr.Name)
	}
}

// TestCall_TwoCredentialMiddlewares_DifferingHeaderValuesConflict locks in
// "L9" in docs/roadmap/declarative-middleware.md: two attached
// credential-providing middlewares that return DIFFERENT values for the
// SAME header key must fail with a typed ConflictingCredentialHeaderError —
// the client never silently picks one over the other.
func TestCall_TwoCredentialMiddlewares_DifferingHeaderValuesConflict(t *testing.T) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	// Two ClientMW calls for the SAME scheme on the SAME route — an
	// unusual but valid pattern; Name is index-suffixed
	// ("fulfill:bearerAuth#0"/"#1") so the conflict detection below can
	// still tell them apart as distinct sources.
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientMW(&declMw, func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "Bearer token-a")
		return h, nil
	}).ClientMW(&declMw, func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "Bearer token-b")
		return h, nil
	}).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	_, callErr := CallWithHandle(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, CallOptions{})

	var conflictErr ConflictingCredentialHeaderError
	if !errors.As(callErr, &conflictErr) {
		t.Fatalf("want ConflictingCredentialHeaderError, got %v", callErr)
	}
	if conflictErr.Header != "Authorization" {
		t.Errorf("want Header %q, got %q", "Authorization", conflictErr.Header)
	}
	if conflictErr.FirstSource != "fulfill:bearerAuth#0" || conflictErr.SecondSource != "fulfill:bearerAuth#1" {
		t.Errorf("want sources fulfill:bearerAuth#0/#1, got %q/%q", conflictErr.FirstSource, conflictErr.SecondSource)
	}
}

// TestCall_TwoCredentialMiddlewares_IdenticalHeaderValuesMergeSilently
// confirms the "identical values are allowed silently" half of the same
// rule — only DIFFERING values for the same key conflict.
func TestCall_TwoCredentialMiddlewares_IdenticalHeaderValuesMergeSilently(t *testing.T) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	credFn := func(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	}
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientMW(&declMw, credFn).ClientMW(&declMw, credFn).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "test-bearer-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "me" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCall_OnCredentialRejected_FiresOn401(t *testing.T) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientMW(&declMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	}).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rejectedCalls := 0
	_, err = CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{},
		CallOptions{
			OnCredentialRejected: func() { rejectedCalls++ },
		})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected UnexpectedStatusError{StatusCode:401}, got %v", err)
	}
	if rejectedCalls != 1 {
		t.Errorf("want OnCredentialRejected called exactly once, got %d", rejectedCalls)
	}
}

func TestCall_OnCredentialRejected_NotCalledWhenCredentialFuncNil(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rejectedCalls := 0
	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{},
		CallOptions{
			// No credential-providing ClientMW configured.
			OnCredentialRejected: func() { rejectedCalls++ },
		})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected UnexpectedStatusError{StatusCode:401}, got %v", err)
	}
	if rejectedCalls != 0 {
		t.Errorf("want OnCredentialRejected never called without a credential-providing ClientMW, got %d calls", rejectedCalls)
	}
}

func TestCall_OnCredentialRejected_NotCalledOnNon401Status(t *testing.T) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientMW(&declMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	}).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rejectedCalls := 0
	_, err = CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{},
		CallOptions{
			OnCredentialRejected: func() { rejectedCalls++ },
		})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected UnexpectedStatusError{StatusCode:500}, got %v", err)
	}
	if rejectedCalls != 0 {
		t.Errorf("want OnCredentialRejected not called on non-401 status, got %d calls", rejectedCalls)
	}
}

// --- Observer RecordRequest on validation failure ---

func TestCall_Observer_RecordRequest_OnValidationFailure(t *testing.T) {
	handle := newClientGetRoute()
	obs := &testObserver{}

	// Missing path var → validation error before any request is sent.
	CallWithHandle(context.Background(), http.DefaultClient, "http://localhost", //nolint:errcheck
		handle, getReq{},
		CallOptions{Observer: obs})

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

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Widget"},
		CallOptions{})

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

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{},
		CallOptions{QueryParams: map[string]string{"limit": "10"}})

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
	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{},
		CallOptions{ExtraHeaders: extra})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Transport error types: Error() string and Unwrap() ---

func TestRequestBuildError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("invalid url")
	e := RequestBuildError{Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted RequestBuildError
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
	e := RequestError{Method: "GET", Path: "/users/{id}", Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted RequestError
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
	e := ResponseBodyError{Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted ResponseBodyError
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

// R8: Call decodes response headers/cookies into Resp when response
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

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getUserActivityReq{ID: "u1"}, CallOptions{})
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

// R9: CallWithHandle happy path — a route with path+query merge
// fields on Req derives BOTH the path var and the query param from ONE
// call, with no cross-role leakage (mirrors
// TestClientEncode_RoleAwareMergeFields_NoLeakage in api/rest, extended
// here through the full client stack).
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

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getUserActivityReq{ID: "u1", Filter: "logins"}, CallOptions{})
	if err != nil {
		t.Fatalf("CallWithHandle: %v", err)
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
// CallWithHandle derives from req for the same key.
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

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getUserActivityReq{ID: "u1", Filter: "logins"},
		CallOptions{QueryParams: map[string]string{"filter": "overridden"}})
	if err != nil {
		t.Fatalf("CallWithHandle: %v", err)
	}
	if gotQuery != "overridden" {
		t.Errorf("query filter: want explicit override %q, got %q", "overridden", gotQuery)
	}
}

// R11: CallWithHandle with zero merge fields declared behaves like
// call(ctx, caller, route, req, opts) — regression guard.
func TestCallHandle_NoMergeFieldsMatchesCall(t *testing.T) {
	handle := newClientCreateRoute()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"1","name":"Alice"}`))
	}))
	defer srv.Close()

	r := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"})
	caller := newCaller(srv.Client(), srv.URL)
	viaCall, err := call(context.Background(), caller, r, createReq{Name: "Alice"}, CallOptions{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	viaCallWithHandle, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})
	if err != nil {
		t.Fatalf("CallWithHandle: %v", err)
	}
	if viaCall != viaCallWithHandle {
		t.Errorf("CallWithHandle should match Call when no merge fields declared: %+v vs %+v", viaCall, viaCallWithHandle)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

// Example shows path-param validation happening BEFORE any HTTP call —
// the route declares a path merge field, so the merge-field's own codec
// constraint is checked at derive time, and an invalid value surfaces as
// a [codex.ValidationError] without ever reaching the network. This
// derivation is internal plumbing shared by [Attach] (the sole public
// client-side workflow — see
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 6) and by
// [ports]' handle-based binding adapters.
func Example() {
	type Item struct{ ID, Name string }
	type getItemReq struct{ ID string }
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
	getRoute := rest.NewRoute[getItemReq, Item]("GET", "/items/{id}",
		codex.Struct[getItemReq](), itemCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.NonEmptyString),
			func(r getItemReq) string { return r.ID },
			func(r *getItemReq, v string) { r.ID = v }),
	)

	// Validate path params before any HTTP call — a merge-field's own
	// codec constraint is checked at derive time, so an invalid value
	// surfaces as a codex.ValidationError.
	caller := newCaller(http.DefaultClient, "https://api.example.com")
	_, err := call(context.Background(), caller, getRoute, getItemReq{ID: ""}, CallOptions{})
	if err != nil {
		var valErr codex.ValidationError
		if errors.As(err, &valErr) {
			fmt.Printf("field %q rejected: %v\n", valErr.Field, valErr.Err)
		}
	}
	// Output: field "id" rejected: constraint failed (non-empty): expected non-empty string
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
	b := rest.NewServer(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/client-call",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[clientErrPayload, clientErrPayload](http.StatusConflict, clientErrPayloadCodec),
	).RegisterHandle(b)
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

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var epr ErrorPatternResponse
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

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var epr ErrorPatternResponse
	if errors.As(err, &epr) {
		t.Fatalf("expected fallback to UnexpectedStatusError, got ErrorPatternResponse: %+v", epr)
	}
	var statusErr UnexpectedStatusError
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

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", statusErr.StatusCode)
	}
}

// CDP8: ErrorPatternResponse.LogValue shape.
func TestErrorPatternResponse_LogValue(t *testing.T) {
	epr := ErrorPatternResponse{
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

// --- Client-side credential FORMAT validation (symmetric with server) ---

// newSecuredClientHandle returns a GET /me RouteHandle via ClientHandle
// (no Builder) with a "bearerAuth" scheme declared via
// middleware.SecurityScheme (attached via .Use(), which also populates
// RouteMeta.Security) — the credential Codec requires a non-empty
// string. credFn is attached via .ClientMW, paired against the SAME
// declared scheme.
func newSecuredClientHandle(credFn func(context.Context, []route.SecurityRequirement) (http.Header, error)) *rest.RouteHandle[getReq, userResp] {
	credCodec := codex.String().Refine(validate.NonEmptyString)
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, &credCodec)
	return rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).Use(declMw).ClientMW(&declMw, credFn).ClientHandle()
}

func TestCall_CredentialFunc_ValidFormat_Passes(t *testing.T) {
	handle := newSecuredClientHandle(func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "me" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCall_CredentialFunc_MalformedFormat_ReturnsSecurityCredentialError(t *testing.T) {
	called := false
	handle := newSecuredClientHandle(func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "Bearer ") // strips to an empty credential -> fails NonEmptyString
		return h, nil
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{})

	var credErr rest.SecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("want rest.SecurityCredentialError, got %T: %v", err, err)
	}
	if credErr.Scheme != "bearerAuth" {
		t.Errorf("Scheme = %q, want %q", credErr.Scheme, "bearerAuth")
	}
	if called {
		t.Error("server must NOT have been called — rejection must happen before any network call")
	}
}

func TestCall_CredentialFunc_MalformedFormat_RecordsSecurityRejection(t *testing.T) {
	handle := newSecuredClientHandle(func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		h := make(http.Header)
		h.Set("Authorization", "Bearer ")
		return h, nil
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	obs := &mockSecurityObserver{}
	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{},
		CallOptions{
			Observer: obs,
		})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if obs.location != "/me" {
		t.Errorf("location = %q, want /me", obs.location)
	}
	if obs.scheme != "bearerAuth" {
		t.Errorf("scheme = %q, want bearerAuth", obs.scheme)
	}
}

func TestCall_NoSecurityScheme_NoValidation(t *testing.T) {
	// Security is declared but NO WithSecurityScheme entry exists for it —
	// SecuritySchemes is empty, so the new client-side codec check is a
	// no-op (matches pre-feature behavior): the request is sent, and the
	// SERVER is the one that rejects it.
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
		rest.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth")}},
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{}) // no credential-providing ClientMW

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("want UnexpectedStatusError from the SERVER, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", statusErr.StatusCode)
	}
}

func TestCall_NoCredentialFunc_SecuredRoute_StillNotAnError(t *testing.T) {
	// A secured route with a declared scheme+Codec, but the credential
	// Codec would ACCEPT an empty string here (no Refine constraint) — so
	// nil CredentialFunc must still not be a client-side error by itself;
	// the request goes out with no Authorization, and it's up to the
	// server to accept or reject it.
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).Use(declMw).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{}) // no credential-providing ClientMW
	if err != nil {
		t.Fatalf("no credential-providing ClientMW on a secured route must not itself be an error: %v", err)
	}
}

// TestCall_CredentialFunc_ReturnsNilHeader_SkipsValidation guards against a
// real bug: a CredentialFunc that deliberately returns (nil, nil) to signal
// "this call needs no credential" (e.g. an auth flow that first probes
// whether the specific server instance requires auth at all) must NOT be
// rejected by the client-side codec check just because the resulting
// (absent) Authorization header extracts as an empty string — that would
// wrongly treat "no credential needed" the same as "malformed credential".
func TestCall_CredentialFunc_ReturnsNilHeader_SkipsValidation(t *testing.T) {
	// Codec requires non-empty string; credFn deliberately returns (nil, nil).
	handle := newSecuredClientHandle(func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		return nil, nil // deliberately "no credential needed"
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{})
	if err != nil {
		t.Fatalf("CredentialFunc returning (nil, nil) must not be rejected by the codec check: %v", err)
	}
}

// ── CallOptions.RequestFormats / ResponseFormats per-call override ────────

// TestCall_ResponseFormats_OverridesRouteDeclaredFormat verifies
// CallOptions.ResponseFormats wins over the route's declared handle.Formats
// for THIS call only.
func TestCall_ResponseFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
		rest.Formats(format.JSON(userRespCodec)),
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write([]byte("id: u1\nname: Alice\n")) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{
			ResponseFormats: []format.Format[userResp]{format.YAML(userRespCodec)},
		})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.ID != "u1" || resp.Name != "Alice" {
		t.Errorf("want decoded via YAML override, got %+v", resp)
	}
}

// TestCall_ResponseFormats_RouteDeclaredStillAppliesWithoutOverride
// verifies the route-declared handle.Formats still applies when no
// per-call override is given — the override must be additive, not a
// regression of the normal declared-format path.
func TestCall_ResponseFormats_RouteDeclaredStillAppliesWithoutOverride(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
		rest.Formats(format.YAML(userRespCodec)),
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Write([]byte("id: u2\nname: Bob\n")) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, CallOptions{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.ID != "u2" || resp.Name != "Bob" {
		t.Errorf("want decoded via route-declared YAML, got %+v", resp)
	}
}

// TestCall_RequestFormats_OverridesRouteDeclaredFormat verifies
// CallOptions.RequestFormats wins over the route's declared
// handle.RequestFormats for THIS call only.
func TestCall_RequestFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	handle := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RequestFormats(format.JSON(createReqCodec)),
	).ClientHandle()

	var gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "u1", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{
			RequestFormats: []format.Format[createReq]{format.YAML(createReqCodec)},
		})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotContentType != "application/yaml" {
		t.Errorf("want Content-Type application/yaml (override), got %q", gotContentType)
	}
	if !strings.Contains(string(gotBody), "name: Alice") {
		t.Errorf("want YAML-encoded body, got %q", gotBody)
	}
}

// TestCall_ResponseFormats_TypeMismatch_ReturnsCallFormatOptError verifies
// a wrong-typed CallOptions.ResponseFormats returns CallFormatOptError,
// errors.As-reachable, with a structured LogValue.
func TestCall_ResponseFormats_TypeMismatch_ReturnsCallFormatOptError(t *testing.T) {
	handle := newClientCreateRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "u1", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := CallWithHandle(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, CallOptions{
			// Wrong type: []format.Format[getReq] instead of []format.Format[userResp].
			ResponseFormats: []format.Format[getReq]{format.JSON(getReqCodec)},
		})
	var fe CallFormatOptError
	if !errors.As(err, &fe) || fe.Direction != "response" {
		t.Fatalf("want CallFormatOptError{response}, got %v", err)
	}
	if fe.Unwrap() == nil {
		t.Error("want non-nil Unwrap")
	}
	v := fe.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"direction", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}

// TestCall_RequestFormats_TypeMismatch_ReturnsCallFormatOptError mirrors
// TestCall_ResponseFormats_TypeMismatch_ReturnsCallFormatOptError for the
// request direction.
func TestCall_RequestFormats_TypeMismatch_ReturnsCallFormatOptError(t *testing.T) {
	handle := newClientCreateRoute()

	_, err := CallWithHandle(context.Background(), http.DefaultClient, "http://unused.invalid",
		handle, createReq{Name: "Alice"}, CallOptions{
			// Wrong type: []format.Format[userResp] instead of []format.Format[createReq].
			RequestFormats: []format.Format[userResp]{format.JSON(userRespCodec)},
		})
	var fe CallFormatOptError
	if !errors.As(err, &fe) || fe.Direction != "request" {
		t.Fatalf("want CallFormatOptError{request}, got %v", err)
	}
	if fe.Unwrap() == nil {
		t.Error("want non-nil Unwrap")
	}
	v := fe.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"direction", "err"} {
		if !keys[want] {
			t.Errorf("missing LogValue key %q", want)
		}
	}
}
