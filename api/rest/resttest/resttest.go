package resttest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
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

// ── Scenario 4: ordered middleware chain with short-circuit ────────────────

// MiddlewareChainReq/MiddlewareChainResp are MiddlewareChainRoute's
// request/response types.
type MiddlewareChainReq struct{}
type MiddlewareChainResp struct{ OK bool }

var middlewareChainReqCodec = codex.Struct[MiddlewareChainReq]()

var middlewareChainRespCodec = codex.Struct[MiddlewareChainResp](
	codex.RequiredField("ok", codex.Bool(),
		func(r MiddlewareChainResp) bool { return r.OK },
		func(r *MiddlewareChainResp, v bool) { r.OK = v },
	),
)

// MiddlewareChainGateHeader is the header [MiddlewareChainRoute]'s FIRST
// middleware Fn requires to be present (any value) before letting the
// chain proceed.
const MiddlewareChainGateHeader = "X-Resttest-Chain-Gate"

// NewMiddlewareChainRoute declares the canonical "ordered middleware
// chain" route: TWO general-purpose (unpaired) HandleMW Fns attached in
// order, plus the handler. order records each participant's name, in the
// order it actually ran, into *order (reset by the caller between
// sub-tests) — a conforming adapter must run both Fns BEFORE the handler,
// in ATTACHMENT order, and must NEVER invoke the second Fn or the handler
// when the first Fn fails.
func NewMiddlewareChainRoute(order *[]string) rest.Route[MiddlewareChainReq, MiddlewareChainResp] {
	first := func(_ context.Context, r *http.Request, _ *MiddlewareChainReq) (map[string][]string, error) {
		*order = append(*order, "first")
		if r.Header.Get(MiddlewareChainGateHeader) == "" {
			return nil, errors.New("missing chain gate header")
		}
		return nil, nil
	}
	second := func(_ context.Context, _ *http.Request, _ *MiddlewareChainReq) (map[string][]string, error) {
		*order = append(*order, "second")
		return nil, nil
	}
	return rest.NewRoute[MiddlewareChainReq, MiddlewareChainResp]("GET", "/resttest/chain",
		middlewareChainReqCodec, middlewareChainRespCodec, rest.RouteMeta{OperationID: "resttestChain"},
	).WithHandler(func(_ context.Context, _ MiddlewareChainReq) (MiddlewareChainResp, error) {
		*order = append(*order, "handler")
		return MiddlewareChainResp{OK: true}, nil
	}).HandleMW(nil, first).HandleMW(nil, second)
}

// AssertMiddlewareChainRoute proves h (built from a route declared via
// [NewMiddlewareChainRoute] with a FRESH *order for each call) runs both
// Fns and the handler in attachment order when the gate header is
// present, and stops at the first Fn — never reaching the second Fn or
// the handler — when it is absent.
func AssertMiddlewareChainRoute(t *testing.T, newHandler func(order *[]string) http.Handler) {
	t.Helper()

	t.Run("gate_present_runs_full_chain_in_order", func(t *testing.T) {
		var order []string
		h := newHandler(&order)
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/chain", nil)
		r.Header.Set(MiddlewareChainGateHeader, "present")
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		want := []string{"first", "second", "handler"}
		if !slices.Equal(order, want) {
			t.Errorf("execution order = %v, want %v", order, want)
		}
	})

	t.Run("gate_absent_short_circuits_before_second_and_handler", func(t *testing.T) {
		var order []string
		h := newHandler(&order)
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/resttest/chain", nil)
		h.ServeHTTP(rec, r)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401 when the gate header is missing; body=%s", rec.Code, rec.Body.String())
		}
		want := []string{"first"}
		if !slices.Equal(order, want) {
			t.Errorf("execution order = %v, want %v (second Fn and handler must never run)", order, want)
		}
	})
}

// ── Scenario 5: declared ErrorPattern mapping ───────────────────────────────

// ErrorMappingReq/ErrorMappingResp are ErrorMappingRoute's request/
// response types. ErrorMappingResp doubles as the ErrorPattern's mapped
// body shape.
type ErrorMappingReq struct{}
type ErrorMappingResp struct{ Reason string }

var errorMappingReqCodec = codex.Struct[ErrorMappingReq]()

var errorMappingRespCodec = codex.Struct[ErrorMappingResp](
	codex.RequiredField("reason", codex.String(),
		func(r ErrorMappingResp) string { return r.Reason },
		func(r *ErrorMappingResp, v string) { r.Reason = v },
	),
)

// ErrorMappingSentinelError is the error type [ErrorMappingRoute]'s
// handler always returns and its declared [rest.ErrorPattern] matches —
// a conforming adapter must map it to [ErrorMappingStatus] + a
// [ErrorMappingResp] body, not a generic 500.
type ErrorMappingSentinelError struct{ Reason string }

func (e ErrorMappingSentinelError) Error() string { return "resttest sentinel: " + e.Reason }

// ErrorMappingStatus is the status [ErrorMappingRoute]'s declared
// [rest.ErrorPattern] maps [ErrorMappingSentinelError] to.
const ErrorMappingStatus = http.StatusUnprocessableEntity

// ErrorMappingReason is the fixed reason the handler's sentinel error
// (and therefore the mapped response body) always carries.
const ErrorMappingReason = "resttest-mapped-reason"

// ErrorMappingRoute is the canonical "declared ErrorPattern" route: a
// conforming adapter must consult the route's declared [rest.ErrorPattern]
// when the handler returns a matching error type, and produce the
// pattern's declared status + mapped body — not treat it as an
// undifferentiated handler failure (generic 500).
func ErrorMappingRoute() rest.Route[ErrorMappingReq, ErrorMappingResp] {
	return rest.NewRoute[ErrorMappingReq, ErrorMappingResp]("GET", "/resttest/error-mapping",
		errorMappingReqCodec, errorMappingRespCodec, rest.RouteMeta{OperationID: "resttestErrorMapping"},
		rest.ErrorPattern[ErrorMappingSentinelError, ErrorMappingResp](ErrorMappingStatus, errorMappingRespCodec,
			func(e ErrorMappingSentinelError) (ErrorMappingResp, error) {
				return ErrorMappingResp(e), nil
			}),
	).WithHandler(func(_ context.Context, _ ErrorMappingReq) (ErrorMappingResp, error) {
		return ErrorMappingResp{}, ErrorMappingSentinelError{Reason: ErrorMappingReason}
	})
}

// AssertErrorMappingRoute proves h (built from [ErrorMappingRoute])
// returns the declared ErrorPattern's mapped status and body instead of
// a generic failure status.
func AssertErrorMappingRoute(t *testing.T, h http.Handler) {
	t.Helper()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/resttest/error-mapping", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != ErrorMappingStatus {
		t.Fatalf("status = %d, want %d (the declared ErrorPattern's status); body=%s", rec.Code, ErrorMappingStatus, rec.Body.String())
	}
	var got ErrorMappingResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Reason != ErrorMappingReason {
		t.Errorf("mapped body Reason = %q, want %q", got.Reason, ErrorMappingReason)
	}
}

// ── Scenario 6: capability-requirement rejection at construction time ──────

// CapabilityRejectionReq/CapabilityRejectionResp are
// CapabilityRejectionRoute's request/response types — never actually
// served, since the route is expected to fail BEFORE a handler could run.
type CapabilityRejectionReq struct{}
type CapabilityRejectionResp struct{}

var capabilityRejectionReqCodec = codex.Struct[CapabilityRejectionReq]()
var capabilityRejectionRespCodec = codex.Struct[CapabilityRejectionResp]()

// CapabilityRejectionRoute is the canonical "unsatisfiable capability
// requirement" route: it declares [rest.RequireQoS], a requirement every
// current plain-HTTP REST adapter (nethttp, chi) declares zero
// capabilities to satisfy — a conforming adapter's own
// ServeOne/serveOne-equivalent construction function MUST reject this
// route with a non-nil error at construction time, before any HTTP
// traffic is possible. This also exercises "lifecycle" in Phase 5's
// sense: a malformed/unsatisfiable route must fail fast at construction,
// not silently serve traffic it cannot correctly handle.
func CapabilityRejectionRoute() rest.Route[CapabilityRejectionReq, CapabilityRejectionResp] {
	return rest.NewRoute[CapabilityRejectionReq, CapabilityRejectionResp]("GET", "/resttest/capability-rejection",
		capabilityRejectionReqCodec, capabilityRejectionRespCodec, rest.RouteMeta{OperationID: "resttestCapabilityRejection"},
		rest.RequireQoS(rest.AtLeastOnce),
	).WithHandler(func(_ context.Context, _ CapabilityRejectionReq) (CapabilityRejectionResp, error) {
		return CapabilityRejectionResp{}, nil
	})
}

// AssertCapabilityRejected proves err (the error returned by the
// adapter's own ServeOne/serveOne when given [CapabilityRejectionRoute])
// is non-nil and, when it exposes a stable public capability-coverage
// error type, names the correct missing capability.
func AssertCapabilityRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want a non-nil construction-time error for an unsatisfiable capability requirement, got nil")
	}
	var cce *rest.CapabilityCoverageError
	if errors.As(err, &cce) {
		if len(cce.Missing) != 1 || cce.Missing[0] != "QoS" {
			t.Errorf("want Missing=[QoS], got %v", cce.Missing)
		}
		return
	}
	t.Errorf("want a *rest.CapabilityCoverageError, got %T: %v", err, err)
}

// ── Scenario 7: Observer hook fires with the expected data ──────────────────

// ObservabilityReq/ObservabilityResp are ObservabilityRoute's request/
// response types.
type ObservabilityReq struct{}
type ObservabilityResp struct{}

var observabilityReqCodec = codex.Struct[ObservabilityReq]()
var observabilityRespCodec = codex.Struct[ObservabilityResp]()

// SpyObserver is a minimal [stats.Observer] spy recording every
// RecordRequest call it receives — exported so adapter test files can
// construct one, wrap it with their own general-purpose observability
// middleware (e.g. `nethttp.Observability(spy)`, which `adapters/chi`
// deliberately reuses as-is rather than duplicating — see that
// function's own doc comment), and pass the SAME spy to
// [AssertObservabilityRoute] for inspection afterward.
//
// Observability is NOT automatically consulted via
// [stats.ObserverFromContext] by every dispatch path — it is an
// EXPLICIT, adapter-provided general-purpose middleware a route opts
// into via `.HandleMW(nil, observabilityMW)`, confirmed directly from
// adapters/nethttp's own Observability doc comment. [ObservabilityRoute]
// therefore takes the observability-wrapping middleware as a parameter
// instead of assuming a context-injection-only mechanism exists.
type SpyObserver struct {
	stats.NoopObserver
	Calls []SpyObserverCall
}

// SpyObserverCall is one recorded [stats.Observer.RecordRequest] call.
type SpyObserverCall struct {
	Method string
	Path   string
	Status int
}

// RecordRequest implements [stats.Observer].
func (s *SpyObserver) RecordRequest(method, path string, statusCode int, _ time.Duration) {
	s.Calls = append(s.Calls, SpyObserverCall{Method: method, Path: path, Status: statusCode})
}

// ObservabilityRoute is the canonical "observed request" route — a plain
// handler attached to the caller-supplied observability middleware (a
// general-purpose `func(http.Handler) http.Handler`, e.g.
// `nethttp.Observability(spy)`) via `.HandleMW(nil, observabilityMW)`,
// the same way a real caller would opt a route into observability.
func ObservabilityRoute(observabilityMW func(http.Handler) http.Handler) rest.Route[ObservabilityReq, ObservabilityResp] {
	return rest.NewRoute[ObservabilityReq, ObservabilityResp]("GET", "/resttest/observability",
		observabilityReqCodec, observabilityRespCodec, rest.RouteMeta{OperationID: "resttestObservability"},
	).WithHandler(func(_ context.Context, _ ObservabilityReq) (ObservabilityResp, error) {
		return ObservabilityResp{}, nil
	}).HandleMW(nil, observabilityMW)
}

// AssertObservabilityRoute proves h (built from [ObservabilityRoute]
// wrapping spy via the adapter's own observability middleware) reports
// exactly one RecordRequest call with the expected method/path/status.
func AssertObservabilityRoute(t *testing.T, h http.Handler, spy *SpyObserver) {
	t.Helper()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/resttest/observability", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(spy.Calls) != 1 {
		t.Fatalf("want exactly 1 RecordRequest call, got %d: %+v", len(spy.Calls), spy.Calls)
	}
	got := spy.Calls[0]
	if got.Method != http.MethodGet || got.Path != "/resttest/observability" || got.Status != http.StatusOK {
		t.Errorf("RecordRequest call = %+v, want {Method:GET Path:/resttest/observability Status:200}", got)
	}
}
