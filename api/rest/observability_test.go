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
