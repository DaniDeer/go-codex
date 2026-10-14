package restdispatch_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

type sseConformanceEvent struct{ EchoedID string }

var sseConformanceEventCodec = codex.Struct[sseConformanceEvent](
	codex.RequiredField("echoed_id", codex.String(),
		func(e sseConformanceEvent) string { return e.EchoedID },
		func(e *sseConformanceEvent, v string) { e.EchoedID = v },
	),
)

// TestDispatchSSERoute_ConsistentAcrossPathValueMechanisms is the SSE
// family's own conformance test, mirroring
// TestDispatchRoute_ConsistentAcrossPathValueMechanisms exactly (same
// fixture shape, same two pathValues stand-ins, same reasoning for why
// restdispatch builds its own ResponseWriter/pathValues implementations
// rather than importing adapters/chi) — see that test's own doc comment
// for the full rationale. This one exercises
// [restdispatch.DispatchSSERoute] instead of [restdispatch.DispatchRoute].
func TestDispatchSSERoute_ConsistentAcrossPathValueMechanisms(t *testing.T) {
	route := rest.NewSSERoute[conformanceReq, sseConformanceEvent]("/sse-conformance/{id}",
		conformanceReqCodec, sseConformanceEventCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.UUID),
			func(r conformanceReq) string { return r.ID },
			func(r *conformanceReq, v string) { r.ID = v },
		),
	).WithHandler(func(_ context.Context, req conformanceReq, send func(sseConformanceEvent) error) error {
		return send(sseConformanceEvent{EchoedID: req.ID})
	})

	b := rest.NewServer(rest.Info{Title: "Test", Version: "1.0.0"})
	handle, err := route.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	// Confirmed during the SSE family consolidation round:
	// rest.SSERouteHandle's field names/types match rest.RouteHandle's
	// exactly, so the SAME ValidateRouteConstruction works unchanged.
	cr, err := restdispatch.ValidateRouteConstruction("sse-conformance-test", handle)
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
			responseHeaders := make(http.Header)
			pendingCookies := make([]rest.PendingCookie, 0)
			restdispatch.DispatchSSERoute(ctx, cr, sw, r, responseHeaders, &pendingCookies, pathValues, writeCookie, errFn, false)
		})
	}

	mux := http.NewServeMux()
	mux.Handle("GET /sse-conformance/{id}", newHandler(pathValuesStdlib))
	stdlibSrv := httptest.NewServer(mux)
	defer stdlibSrv.Close()

	manualSrv := httptest.NewServer(newHandler(pathValuesManualTemplate))
	defer manualSrv.Close()

	doRequest := func(t *testing.T, baseURL, id string) (status int, dataLines []string) {
		t.Helper()
		resp, err := http.Get(baseURL + "/sse-conformance/" + id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				dataLines = append(dataLines, line)
			}
		}
		return resp.StatusCode, dataLines
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
			stdlibStatus, stdlibData := doRequest(t, stdlibSrv.URL, tc.id)
			manualStatus, manualData := doRequest(t, manualSrv.URL, tc.id)

			if stdlibStatus != manualStatus {
				t.Errorf("status mismatch for %s: stdlib-PathValue=%d manual-template=%d", tc.name, stdlibStatus, manualStatus)
			}
			if strings.Join(stdlibData, "\n") != strings.Join(manualData, "\n") {
				t.Errorf("SSE data mismatch for %s:\n  stdlib-PathValue=%v\n  manual-template=%v", tc.name, stdlibData, manualData)
			}
		})
	}
}
