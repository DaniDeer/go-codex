package resttest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Scenario 1: basic decode/encode route ──────────────────────────────────

// EchoReq/EchoResp are EchoRoute's request/response types — deliberately
// minimal so every Assert failure is attributable to the dispatch
// mechanism itself, not an incidental codec detail.
type EchoReq struct{ Message string }
type EchoResp struct {
	Message string
	Handled bool
}

var echoReqCodec = codex.Struct[EchoReq](
	codex.RequiredField("message", codex.String(),
		func(r EchoReq) string { return r.Message },
		func(r *EchoReq, v string) { r.Message = v },
	),
)

var echoRespCodec = codex.Struct[EchoResp](
	codex.RequiredField("message", codex.String(),
		func(r EchoResp) string { return r.Message },
		func(r *EchoResp, v string) { r.Message = v },
	),
	codex.RequiredField("handled", codex.Bool(),
		func(r EchoResp) bool { return r.Handled },
		func(r *EchoResp, v bool) { r.Handled = v },
	),
)

// EchoRoute is the canonical "basic route" every conforming REST adapter
// must correctly serve: decode a JSON body, call the handler, encode the
// JSON response. Returns a fresh route (with its own handler attached) on
// every call — build one http.Handler per call via your own adapter's
// ServeOne/serveOne, do not share a single built route across tests.
func EchoRoute() rest.Route[EchoReq, EchoResp] {
	return rest.NewRoute[EchoReq, EchoResp]("POST", "/resttest/echo",
		echoReqCodec, echoRespCodec, rest.RouteMeta{OperationID: "resttestEcho"},
	).WithHandler(func(_ context.Context, req EchoReq) (EchoResp, error) {
		return EchoResp{Message: req.Message, Handled: true}, nil
	})
}

// AssertEchoRoute proves h (built from [EchoRoute]) correctly decodes a
// valid body and rejects a malformed one.
func AssertEchoRoute(t *testing.T, h http.Handler) {
	t.Helper()

	t.Run("valid_body_decodes_and_echoes", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/resttest/echo", strings.NewReader(`{"message":"hi"}`))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (api/rest's default POST status); body=%s", rec.Code, rec.Body.String())
		}
		var got EchoResp
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got.Message != "hi" || !got.Handled {
			t.Errorf("unexpected response: %+v, want {Message:hi Handled:true}", got)
		}
	})

	t.Run("malformed_body_rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/resttest/echo", strings.NewReader(`not valid json`))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 for malformed body; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// ── Scenario 2: path-param merge-field route ────────────────────────────────

// PathParamReq/PathParamResp are PathParamRoute's request/response types.
type PathParamReq struct{ ID string }
type PathParamResp struct{ ID string }

var pathParamReqCodec = codex.Struct[PathParamReq]()

var pathParamRespCodec = codex.Struct[PathParamResp](
	codex.RequiredField("id", codex.String(),
		func(r PathParamResp) string { return r.ID },
		func(r *PathParamResp, v string) { r.ID = v },
	),
)

var pathParamUUIDCodec = codex.String().Refine(validate.UUID)

// PathParamRoute is the canonical "path-param merge field" route: a
// conforming adapter must decode/validate the `{id}` path segment as a
// UUID AND merge it directly into the handler's decoded request value —
// the handler itself must never need to re-extract it manually.
func PathParamRoute() rest.Route[PathParamReq, PathParamResp] {
	return rest.NewRoute[PathParamReq, PathParamResp]("GET", "/resttest/items/{id}",
		pathParamReqCodec, pathParamRespCodec, rest.RouteMeta{OperationID: "resttestPathParam"},
		rest.NewPathParam("id", pathParamUUIDCodec,
			func(r PathParamReq) string { return r.ID },
			func(r *PathParamReq, v string) { r.ID = v },
		),
	).WithHandler(func(_ context.Context, req PathParamReq) (PathParamResp, error) {
		// req.ID is already merged — no manual path-value extraction.
		return PathParamResp(req), nil
	})
}

// AssertPathParamRoute proves h (built from [PathParamRoute]) rejects an
// invalid path-param value (fails the declared UUID codec) and correctly
// merges a valid one into the decoded request before the handler runs.
func AssertPathParamRoute(t *testing.T, h http.Handler) {
	t.Helper()

	t.Run("invalid_uuid_rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/items/not-a-uuid", nil)
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400 for invalid path param; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid_uuid_merged_into_request", func(t *testing.T) {
		const id = "550e8400-e29b-41d4-a716-446655440000"
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/items/"+id, nil)
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var got PathParamResp
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got.ID != id {
			t.Errorf("want handler to receive the merged path param %q, got %q", id, got.ID)
		}
	})
}

// ── Scenario 3: general-purpose security-gate route ─────────────────────────

// SecurityReq/SecurityResp are SecurityRoute's request/response types.
type SecurityReq struct{}
type SecurityResp struct{ OK bool }

var securityReqCodec = codex.Struct[SecurityReq]()

var securityRespCodec = codex.Struct[SecurityResp](
	codex.RequiredField("ok", codex.Bool(),
		func(r SecurityResp) bool { return r.OK },
		func(r *SecurityResp, v bool) { r.OK = v },
	),
)

// SecurityAPIKeyHeader is the header [SecurityRoute]'s gate reads — a
// conforming adapter must run the gate BEFORE the handler and reject
// a missing/wrong key without ever invoking the handler.
const SecurityAPIKeyHeader = "X-Resttest-Api-Key"

// SecurityAPIKeyValue is the one fixed, non-sensitive value
// [SecurityRoute]'s gate accepts — a test fixture, not a real credential.
// gosec's G101 (hardcoded-credentials) is excluded for this file in
// gosec.config.json, matching this repo's established convention for
// fixture files whose string constants merely look credential-shaped
// (see examples/reqreply-api's own G101 exclusions).
const SecurityAPIKeyValue = "resttest-fixture-value"

// SecurityRoute is the canonical "security gate" route: a general-purpose
// (unpaired) HandleMW Fn — the same shape every REST adapter's own
// "RunsWithoutRouteSecurity" test already exercises independently (see
// adapters/nethttp's and adapters/chi's TestHandler_RequireAPIKey_
// RunsWithoutRouteSecurity, and Phase 3 case 5's cross-adapter test) —
// checked before the handler runs, rejecting with 401 on a missing/wrong
// key and never invoking the handler.
func SecurityRoute() rest.Route[SecurityReq, SecurityResp] {
	apiKeyFn := func(_ context.Context, r *http.Request, _ *SecurityReq) (map[string][]string, error) {
		if r.Header.Get(SecurityAPIKeyHeader) != SecurityAPIKeyValue {
			return nil, errors.New("invalid or missing API key")
		}
		return nil, nil
	}
	return rest.NewRoute[SecurityReq, SecurityResp]("GET", "/resttest/secure",
		securityReqCodec, securityRespCodec, rest.RouteMeta{OperationID: "resttestSecure"},
	).WithHandler(func(_ context.Context, _ SecurityReq) (SecurityResp, error) {
		return SecurityResp{OK: true}, nil
	}).HandleMW(nil, apiKeyFn)
}

// AssertSecurityRoute proves h (built from [SecurityRoute]) rejects a
// request missing (or presenting the wrong) API key with 401 WITHOUT
// running the handler, and accepts a request with the correct key.
func AssertSecurityRoute(t *testing.T, h http.Handler) {
	t.Helper()

	t.Run("missing_key_rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/secure", nil)
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 for missing API key; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("wrong_key_rejected", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/secure", nil)
		r.Header.Set(SecurityAPIKeyHeader, "wrong-value")
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 for wrong API key; body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("valid_key_accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/secure", nil)
		r.Header.Set(SecurityAPIKeyHeader, SecurityAPIKeyValue)
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 for valid API key; body=%s", rec.Code, rec.Body.String())
		}
		var got SecurityResp
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if !got.OK {
			t.Errorf("want handler to run and return OK:true, got %+v", got)
		}
	})
}
