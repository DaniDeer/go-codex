package restdispatch_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// testStatusResponseWriter is a minimal, test-local stand-in for
// adapters/nethttp's/adapters/chi's own `statusResponseWriter` —
// restdispatch cannot import either adapter package (that would invert
// the Phase 4 dependency direction: the adapters import restdispatch,
// never the reverse), so this conformance test builds its own
// [restdispatch.ResponseWriter] implementation instead, exactly as a
// THIRD, hypothetical net/http-family adapter would.
type testStatusResponseWriter struct {
	http.ResponseWriter
	code int
}

func (w *testStatusResponseWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *testStatusResponseWriter) StatusCode() int { return w.code }

func (w *testStatusResponseWriter) WriteErrorResponse(headers map[string][]string, cookies []rest.PendingCookie, status int, body []byte) error {
	for k, vals := range headers {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	for _, pc := range cookies {
		http.SetCookie(w, &http.Cookie{Name: pc.Name, Value: pc.Value})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := w.Write(body)
	return err
}

var _ restdispatch.ResponseWriter = (*testStatusResponseWriter)(nil)

// pathValuesStdlib mirrors adapters/nethttp's own `pathValues` function
// exactly: the stdlib net/http (*http.Request).PathValue mechanism
// (requires registering via a real http.ServeMux pattern).
func pathValuesStdlib(r *http.Request, names []string) map[string]string {
	m := make(map[string]string, len(names))
	for _, name := range names {
		m[name] = r.PathValue(name)
	}
	return m
}

// pathValuesManualTemplate is a standalone, from-scratch path-template
// matcher — representationally equivalent to what any OTHER router
// (chi's gochi.URLParam included) does internally, without this package
// introducing a dependency on one. Matches r.URL.Path against the fixed
// "/conformance/{id}" template this test's route declares.
func pathValuesManualTemplate(r *http.Request, names []string) map[string]string {
	m := make(map[string]string, len(names))
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// "/conformance/{id}" -> ["conformance", "{id}"]; segments[1] is the
	// "id" value for this test's fixed route shape.
	if len(segments) == 2 {
		for _, name := range names {
			if name == "id" {
				m[name] = segments[1]
			}
		}
	}
	return m
}

type conformanceReq struct{ ID string }
type conformanceResp struct{ EchoedID string }

var conformanceReqCodec = codex.Struct[conformanceReq]()
var conformanceRespCodec = codex.Struct[conformanceResp](
	codex.RequiredField("echoed_id", codex.String(),
		func(r conformanceResp) string { return r.EchoedID },
		func(r *conformanceResp, v string) { r.EchoedID = v },
	),
)

// TestDispatchRoute_ConsistentAcrossPathValueMechanisms is Phase 4 (REST
// family step 8)'s cross-adapter conformance test: one
// [restdispatch.ConstructedRoute] (built ONCE via
// [restdispatch.ValidateRouteConstruction]) is dispatched TWICE via
// [restdispatch.DispatchRoute] — once per [pathValues] mechanism — and
// both must produce byte-identical outcomes. This directly exercises the
// ONE genuine per-adapter seam Phase 2's design identified
// (docs/roadmap/adapter-integration-architecture.md's "REST family
// dispatch-core consolidation design"): everything else in the shared
// core is proven identical by construction (one function, one call);
// only pathValues is adapter-supplied, so this is the one seam future
// per-router divergence could hide in.
func TestDispatchRoute_ConsistentAcrossPathValueMechanisms(t *testing.T) {
	route := rest.NewRoute[conformanceReq, conformanceResp]("GET", "/conformance/{id}",
		conformanceReqCodec, conformanceRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.UUID),
			func(r conformanceReq) string { return r.ID },
			func(r *conformanceReq, v string) { r.ID = v },
		),
	).WithHandler(func(_ context.Context, req conformanceReq) (conformanceResp, error) {
		return conformanceResp{EchoedID: req.ID}, nil
	})

	b := rest.NewServer(rest.Info{Title: "Test", Version: "1.0.0"})
	handle, err := route.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	cr, err := restdispatch.ValidateRouteConstruction("conformance-test", handle)
	if err != nil {
		t.Fatalf("ValidateRouteConstruction: %v", err)
	}

	writeCookie := func(w http.ResponseWriter, name, value string, attrs rest.CookieAttributes) error {
		http.SetCookie(w, &http.Cookie{Name: name, Value: value})
		return nil
	}
	errFn := restdispatch.DefaultErrorHandler

	newHandler := func(pathValues func(*http.Request, []string) map[string]string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &testStatusResponseWriter{ResponseWriter: w, code: http.StatusOK}
			ctx := r.Context()
			respHeaders := make(http.Header)
			pendingCookies := make([]rest.PendingCookie, 0)
			restdispatch.DispatchRoute(ctx, cr, sw, r, respHeaders, &pendingCookies, pathValues, writeCookie, errFn, restdispatch.MaxRequestBodyBytes, "application/json", false)
		})
	}

	// pathValuesStdlib needs a real http.ServeMux pattern to populate
	// r.PathValue — mirrors adapters/nethttp's own wiring exactly.
	mux := http.NewServeMux()
	mux.Handle("GET /conformance/{id}", newHandler(pathValuesStdlib))
	stdlibSrv := httptest.NewServer(mux)
	defer stdlibSrv.Close()

	// pathValuesManualTemplate needs no special router wiring — it parses
	// the URL itself — so a plain handler suffices.
	manualSrv := httptest.NewServer(newHandler(pathValuesManualTemplate))
	defer manualSrv.Close()

	doRequest := func(t *testing.T, baseURL, id string) (status int, body string) {
		t.Helper()
		resp, err := http.Get(baseURL + "/conformance/" + id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		return resp.StatusCode, string(b)
	}

	cases := []struct {
		name string
		id   string
	}{
		{"valid_uuid", "550e8400-e29b-41d4-a716-446655440000"},
		{"invalid_uuid", "not-a-uuid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdlibStatus, stdlibBody := doRequest(t, stdlibSrv.URL, tc.id)
			manualStatus, manualBody := doRequest(t, manualSrv.URL, tc.id)

			if stdlibStatus != manualStatus {
				t.Errorf("status mismatch for %s: stdlib-PathValue=%d manual-template=%d", tc.name, stdlibStatus, manualStatus)
			}
			if stdlibBody != manualBody {
				t.Errorf("body mismatch for %s:\n  stdlib-PathValue=%q\n  manual-template=%q", tc.name, stdlibBody, manualBody)
			}
		})
	}
}
