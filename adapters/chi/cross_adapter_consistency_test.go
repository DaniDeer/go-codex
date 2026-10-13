package chi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
)

// TestMiddlewareChain_ConsistentAcrossNethttpAndChi is Phase 3 (case 5) of
// docs/roadmap/adapter-integration-architecture.md's roadmap: "A
// middleware chain whose behavior remains consistent across compatible
// adapters." The Phase 1 audit found adapters/chi independently
// duplicates adapters/nethttp's entire dispatch/middleware logic
// line-for-line, with no import relationship between the two packages —
// this test proves that duplication has NOT (yet) caused observable
// behavioral drift: the EXACT SAME rest.Route declaration, with the EXACT
// SAME general-purpose middleware Fn attached via .HandleMW, produces
// identical accept/reject outcomes whether served via nethttp.ServeOne or
// chi's own (unexported, package-internal) serveOne.
//
// Deliberately lives in package chi (not an external _test package) so it
// can reach chi's own unexported serveOne directly, while importing
// adapters/nethttp normally (ServeOne is exported there) — the asymmetry
// itself (chi's serveOne isn't exported, unlike nethttp.ServeOne) is a
// confirmed Phase 1 finding, not something this test silently works
// around without comment.
func TestMiddlewareChain_ConsistentAcrossNethttpAndChi(t *testing.T) {
	// apiKeyFn is a general-purpose, non-Security HandleMW Fn — it
	// operates directly on *http.Request, the one wire type both
	// adapters are confirmed (Phase 2's REST family design) to receive
	// identically, mirroring the existing
	// TestHandler_RequireAPIKey_RunsWithoutRouteSecurity pattern both
	// packages already carry independently.
	verifyCallCount := 0
	apiKeyFn := func(_ context.Context, r *http.Request, _ *createReq) (map[string][]string, error) {
		verifyCallCount++
		if r.Header.Get("X-API-Key") != "secret" {
			return nil, errors.New("invalid api key")
		}
		return nil, nil
	}

	// ONE shared declaration — not redeclared per adapter.
	route := newCreateRoute().WithHandler(func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}).HandleMW(nil, apiKeyFn)

	chiHandler, err := serveOne(route)
	if err != nil {
		t.Fatalf("chi serveOne: %v", err)
	}
	nethttpHandler, err := nethttp.ServeOne(route)
	if err != nil {
		t.Fatalf("nethttp.ServeOne: %v", err)
	}

	chiSrv := httptest.NewServer(chiHandler)
	defer chiSrv.Close()
	nethttpSrv := httptest.NewServer(nethttpHandler)
	defer nethttpSrv.Close()

	doRequest := func(t *testing.T, baseURL, apiKey string) (status int, body string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, baseURL+"/users", strings.NewReader(`{"name":"Alice"}`))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("X-API-Key", apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		return resp.StatusCode, string(buf[:n])
	}

	cases := []struct {
		name   string
		apiKey string
	}{
		{"missing_api_key", ""},
		{"wrong_api_key", "wrong"},
		{"valid_api_key", "secret"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chiStatus, chiBody := doRequest(t, chiSrv.URL, tc.apiKey)
			nethttpStatus, nethttpBody := doRequest(t, nethttpSrv.URL, tc.apiKey)

			if chiStatus != nethttpStatus {
				t.Errorf("status mismatch for %s: chi=%d nethttp=%d", tc.name, chiStatus, nethttpStatus)
			}
			// Body content need not be byte-identical (error message
			// wrapping may legitimately differ in detail), but the
			// SUCCESS/FAILURE shape must agree: both carry a JSON "id"
			// field on success, or neither does.
			chiOK := strings.Contains(chiBody, `"id"`)
			nethttpOK := strings.Contains(nethttpBody, `"id"`)
			if chiOK != nethttpOK {
				t.Errorf("success/failure shape mismatch for %s: chi body=%q nethttp body=%q", tc.name, chiBody, nethttpBody)
			}
		})
	}

	// The general-purpose Fn must run unconditionally on EVERY request,
	// on BOTH adapters — 3 cases x 2 adapters = 6 calls.
	if verifyCallCount != 6 {
		t.Errorf("want apiKeyFn called 6 times (3 cases x 2 adapters), got %d", verifyCallCount)
	}
}
