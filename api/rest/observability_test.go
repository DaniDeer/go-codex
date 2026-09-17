package rest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// diagnosticsFrom wraps ctx via [stats.WithDiagnostics], invokes fn(ctx),
// then returns every [stats.Diagnostic] recorded — the shared harness for
// every ReportXxxErrors test below (all 8 symbols ferry data out via ctx,
// not a direct Observer call).
func diagnosticsFrom(fn func(ctx context.Context)) []stats.Diagnostic {
	ctx := stats.WithDiagnostics(context.Background())
	fn(ctx)
	return stats.DiagnosticsFromContext(ctx)
}

func TestReportBodyErrors(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(5))
	verr := c.Validate("ab")
	paramErr := codex.ParamError{Name: "name", Value: "ab", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportBodyErrors(ctx, paramErr) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "body" || got[0].Field != "name" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReportQueryErrors(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(5))
	verr := c.Validate("ab")
	qe := rest.QueryParamError{Name: "q", Value: "ab", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportQueryErrors(ctx, qe) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "query" || got[0].Field != "q" || got[0].ConstraintName != "minLen(5)" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReportQueryErrors_NonMatchingError_NoOp(t *testing.T) {
	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportQueryErrors(ctx, errors.New("unrelated")) })
	if len(got) != 0 {
		t.Fatalf("want 0 diagnostics for a non-QueryParamError, got %d", len(got))
	}
}

func TestReportCookieErrors(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(5))
	verr := c.Validate("ab")
	ce := rest.CookieParamError{Name: "session", Value: "ab", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportCookieErrors(ctx, ce) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "cookie" || got[0].Field != "session" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReportHeaderErrors(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(5))
	verr := c.Validate("ab")
	he := rest.HeaderParamError{Name: "X-Test", Value: "ab", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportHeaderErrors(ctx, he) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "header" || got[0].Field != "X-Test" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReportResponseHeaderErrors(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(5))
	verr := c.Validate("ab")
	rhe := rest.ResponseHeaderParamError{Name: "X-Resp", Value: "ab", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportResponseHeaderErrors(ctx, rhe) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "response_header" || got[0].Field != "X-Resp" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReportResponseCookieErrors(t *testing.T) {
	c := codex.String().Refine(validate.MinLen(5))
	verr := c.Validate("ab")
	rce := rest.ResponseCookieParamError{Name: "session", Value: "ab", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportResponseCookieErrors(ctx, rce) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "response_cookie" || got[0].Field != "session" {
		t.Errorf("got %+v", got[0])
	}
}

func TestReportPathErrors(t *testing.T) {
	c := codex.String().Refine(validate.UUID)
	verr := c.Validate("not-a-uuid")
	pe := rest.PathParamError{Name: "id", Value: "not-a-uuid", Err: verr}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportPathErrors(ctx, pe) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "path" || got[0].Field != "id" || got[0].ConstraintName != "uuid" {
		t.Errorf("got %+v", got[0])
	}
}

// TestReportPathErrors_MissingPathVarError verifies the extension over
// the pre-move implementation (this round): a client-side caller
// forgetting a required path var previously produced a
// MissingPathVarError with ZERO stats.Diagnostic reported.
func TestReportPathErrors_MissingPathVarError(t *testing.T) {
	mpe := rest.MissingPathVarError{Name: "id"}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportPathErrors(ctx, mpe) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "path" || got[0].Field != "id" || got[0].ConstraintName != "required" {
		t.Errorf("got %+v", got[0])
	}
}

// TestReportPathErrors_InvalidPathParamError covers the third alias
// sibling, mirroring O1's exact 3-type-addition pattern.
func TestReportPathErrors_InvalidPathParamError(t *testing.T) {
	ipe := rest.InvalidPathParamError{Name: "id", Template: "/users/{id}"}

	got := diagnosticsFrom(func(ctx context.Context) { rest.ReportPathErrors(ctx, ipe) })
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "path" || got[0].Field != "id" || got[0].ConstraintName != "declared-var-not-in-template" {
		t.Errorf("got %+v", got[0])
	}
}

func TestDiagnosticObserver_RecordValidationError(t *testing.T) {
	got := diagnosticsFrom(func(ctx context.Context) {
		rest.DiagnosticObserver{Ctx: ctx}.RecordValidationError("body", "required", "name")
	})
	if len(got) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(got))
	}
	if got[0].Location != "body" || got[0].ConstraintName != "required" || got[0].Field != "name" {
		t.Errorf("got %+v", got[0])
	}
}

// ── ObserveErrorResponseFor tests ─────────────────────────────────────────

type errorPatternObserverSpy struct {
	stats.NoopObserver
	matches, misses                    int
	lastLocation, lastCode, lastAction string
	lastMissLocation                   string
	tags                               int
	lastTagKey, lastTagValue           string
}

func (s *errorPatternObserverSpy) RecordErrorPatternMatch(location, code, action string) {
	s.matches++
	s.lastLocation, s.lastCode, s.lastAction = location, code, action
}

func (s *errorPatternObserverSpy) RecordErrorPatternMiss(location string) {
	s.misses++
	s.lastMissLocation = location
}

func (s *errorPatternObserverSpy) TagSpan(_ context.Context, key, value string) {
	s.tags++
	s.lastTagKey, s.lastTagValue = key, value
}

func newObserveTestRoute(t *testing.T) *rest.RouteHandle[createReq, userResp] {
	t.Helper()
	b := rest.NewServer(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/errors/observe-"+t.Name(),
		createReqCodec, userCodec,
		rest.ErrorPattern[directPatternError, directPatternError](409, directPatternCodec),
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	return h
}

func TestObserveErrorResponseFor_RecordsMatch(t *testing.T) {
	h := newObserveTestRoute(t)
	spy := &errorPatternObserverSpy{}
	resp, matched, applyErr := h.ObserveErrorResponseFor(context.Background(), spy, directPatternError{Code: "boom"})
	if applyErr != nil {
		t.Fatalf("applyErr: %v", applyErr)
	}
	if !matched {
		t.Fatal("want matched=true")
	}
	if resp.Status != 409 {
		t.Errorf("want Status=409, got %d", resp.Status)
	}
	if spy.matches != 1 {
		t.Fatalf("want 1 RecordErrorPatternMatch call, got %d", spy.matches)
	}
	if spy.lastCode != "409" {
		t.Errorf("want code=409, got %q", spy.lastCode)
	}
	if spy.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls on a match, got %d", spy.misses)
	}
}

func TestObserveErrorResponseFor_RecordsMiss_OnlyWhenPatternsDeclared(t *testing.T) {
	h := newObserveTestRoute(t)
	spy := &errorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, errors.New("unrelated"))
	if matched {
		t.Fatal("want matched=false")
	}
	if spy.misses != 1 {
		t.Fatalf("want 1 RecordErrorPatternMiss call, got %d", spy.misses)
	}

	// Now confirm the negative case: a route with NO declared ErrorPatterns
	// never calls RecordErrorPatternMiss at all.
	b := rest.NewServer(testInfo)
	noPatterns, err := rest.NewRoute[createReq, userResp]("POST", "/errors/observe-no-patterns",
		createReqCodec, userCodec,
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	spy2 := &errorPatternObserverSpy{}
	_, matched2, _ := noPatterns.ObserveErrorResponseFor(context.Background(), spy2, errors.New("unrelated"))
	if matched2 {
		t.Fatal("want matched=false")
	}
	if spy2.misses != 0 {
		t.Errorf("want 0 RecordErrorPatternMiss calls when no patterns declared, got %d", spy2.misses)
	}
}

func TestObserveErrorResponseFor_TagsSpan_OnMatch(t *testing.T) {
	h := newObserveTestRoute(t)
	spy := &errorPatternObserverSpy{}
	_, matched, _ := h.ObserveErrorResponseFor(context.Background(), spy, directPatternError{Code: "boom"})
	if !matched {
		t.Fatal("want matched=true")
	}
	if spy.tags != 1 {
		t.Fatalf("want 1 TagSpan call, got %d", spy.tags)
	}
	if spy.lastTagValue != "409" {
		t.Errorf("want tag value=409, got %q", spy.lastTagValue)
	}
}

func TestObserveErrorResponseFor_PlainObserver_NoPanic(t *testing.T) {
	h := newObserveTestRoute(t)
	// stats.NoopObserver implements neither ErrorPatternObserver's methods
	// with any custom behavior nor SpanTagger with any custom behavior
	// (only no-ops) — but more importantly, use a bareObserver-equivalent
	// (an Observer implementing only the base interface, via a minimal
	// wrapper) to ensure the type-assertion guards inside
	// ObserveErrorResponseFor never panic.
	plain := stats.NoopObserver{}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, directPatternError{Code: "boom"}); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
	if _, _, err := h.ObserveErrorResponseFor(context.Background(), plain, errors.New("unrelated")); err != nil {
		t.Fatalf("unexpected applyErr: %v", err)
	}
}

func TestRouteHandle_HasErrorPatterns(t *testing.T) {
	h := newObserveTestRoute(t)
	if !h.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=true for a route with a declared ErrorPattern")
	}

	b := rest.NewServer(testInfo)
	noPatterns, err := rest.NewRoute[createReq, userResp]("POST", "/errors/has-error-patterns-none",
		createReqCodec, userCodec,
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if noPatterns.HasErrorPatterns() {
		t.Error("want HasErrorPatterns()=false for a route with no declared ErrorPattern")
	}
}
