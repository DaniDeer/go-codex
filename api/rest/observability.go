package rest

import (
	"context"
	"errors"
	"strconv"

	"github.com/DaniDeer/go-codex/stats"
)

// DiagnosticObserver adapts ctx-based [stats.RecordDiagnostic] to the
// [stats.ValidationObserver] interface, so [stats.ReportErrors]'s
// existing per-field error-walking logic can be reused UNCHANGED — the
// SAME data, just ferried out via ctx instead of a direct Observer call
// (decode-time validation events have no other home once
// [adapters/nethttp.Options.Observer]/[adapters/chi.Options.Observer]
// is resolved per-request, not at construction time).
//
// Moved here (from `adapters/nethttp`'s own now-deleted copy) because it
// and its 7 sibling `Report*Errors` functions were confirmed
// byte-for-byte IDENTICAL between `adapters/nethttp` and `adapters/chi`
// — both delete their own copies entirely and call these exports
// directly, mirroring [api/reqreply.Observability]'s own "one shared
// implementation, zero per-adapter indirection" precedent (unlike
// [adapters/mqtt5.Observability]'s thin-wrapper-delegates-to-core shape,
// which exists ONLY because mqtt5/mqtt genuinely have adapter-specific
// behavior on top — REST's 8 symbols have none).
type DiagnosticObserver struct{ Ctx context.Context }

// RecordValidationError implements [stats.ValidationObserver].
func (d DiagnosticObserver) RecordValidationError(location, constraintName, field string) {
	stats.RecordDiagnostic(d.Ctx, stats.Diagnostic{Location: location, ConstraintName: constraintName, Field: field})
}

// ReportBodyErrors extracts per-field validation errors from a body
// decode error and ferries them out via [stats.RecordDiagnostic] with
// location "body".
func ReportBodyErrors(ctx context.Context, err error) {
	stats.ReportErrors(DiagnosticObserver{ctx}, "body", err)
}

// ReportQueryErrors extracts the failing query parameter from a
// [QueryParamError] and ferries it out via [stats.RecordDiagnostic] with
// location "query".
func ReportQueryErrors(ctx context.Context, err error) {
	var qe QueryParamError
	if !errors.As(err, &qe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "query", ConstraintName: stats.ConstraintName(qe.Err), Field: qe.Name})
}

// ReportCookieErrors extracts the failing cookie parameter from a
// [CookieParamError] and ferries it out via [stats.RecordDiagnostic] with
// location "cookie".
func ReportCookieErrors(ctx context.Context, err error) {
	var ce CookieParamError
	if !errors.As(err, &ce) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "cookie", ConstraintName: stats.ConstraintName(ce.Err), Field: ce.Name})
}

// ReportHeaderErrors extracts the failing header from a
// [HeaderParamError] and ferries it out via [stats.RecordDiagnostic] with
// location "header".
func ReportHeaderErrors(ctx context.Context, err error) {
	var he HeaderParamError
	if !errors.As(err, &he) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "header", ConstraintName: stats.ConstraintName(he.Err), Field: he.Name})
}

// ReportResponseHeaderErrors extracts the failing response header from a
// [ResponseHeaderParamError] and ferries it out via
// [stats.RecordDiagnostic] with location "response_header".
func ReportResponseHeaderErrors(ctx context.Context, err error) {
	var rhe ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response_header", ConstraintName: stats.ConstraintName(rhe.Err), Field: rhe.Name})
}

// ReportResponseCookieErrors extracts the failing response cookie from a
// [ResponseCookieParamError] and ferries it out via
// [stats.RecordDiagnostic] with location "response_cookie".
func ReportResponseCookieErrors(ctx context.Context, err error) {
	var rce ResponseCookieParamError
	if !errors.As(err, &rce) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response_cookie", ConstraintName: stats.ConstraintName(rce.Err), Field: rce.Name})
}

// ReportPathErrors extracts the failing path variable from a
// [PathParamError] and ferries it out via [stats.RecordDiagnostic] with
// location "path". Also unpacks [MissingPathVarError] (constraint
// "required") and [InvalidPathParamError] (constraint
// "declared-var-not-in-template") — an extension over the pre-move
// implementation, mirroring O1's exact fix pattern
// (`stats.ReportErrors`'s own walker) applied to this Diagnostics-ferry
// mechanism specifically. Confirmed reachable: a client-side
// [adapters/nethttp.Call]/[adapters/chi]-equivalent caller forgetting a
// required path var previously produced a [MissingPathVarError] with
// ZERO [stats.Diagnostic] reported (though the correct error was still
// returned) — this closes that gap.
func ReportPathErrors(ctx context.Context, err error) {
	var pe PathParamError
	if errors.As(err, &pe) {
		stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "path", ConstraintName: stats.ConstraintName(pe.Err), Field: pe.Name})
		return
	}
	var mpe MissingPathVarError
	if errors.As(err, &mpe) {
		stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "path", ConstraintName: "required", Field: mpe.Name})
		return
	}
	var ipe InvalidPathParamError
	if errors.As(err, &ipe) {
		stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "path", ConstraintName: "declared-var-not-in-template", Field: ipe.Name})
	}
}

// ObserveErrorResponseFor is the observability-aware counterpart of
// [RouteHandle.ErrorResponseFor]: it performs the SAME errors.As match,
// but ALSO reports the outcome to obs — stats.ErrorPatternObserver.
// RecordErrorPatternMatch on a match, RecordErrorPatternMiss on a miss
// (only when [RouteHandle.HasErrorPatterns] is true), and stats.
// SpanTagger.TagSpan when both a match occurs AND obs implements
// SpanTagger — all type-asserted and derived INTERNALLY, so the caller
// needs no knowledge of ErrorPatternObserver/SpanTagger/HasErrorPatterns
// at all. Returns the IDENTICAL 3-tuple ErrorResponseFor already
// returns, so an adapter's existing resp.Action-dispatch logic (write
// body, run ErrorHandle's callback, etc.) needs no changes beyond
// swapping which method it calls.
//
// This is the RECOMMENDED call site for every Category-A failure point
// (see docs/roadmap/error-handling-rest-events-reqreply.md's Topic 1) —
// adapters should call this instead of ErrorResponseFor directly
// whenever an Observer is in scope.
func (h *RouteHandle[Req, Resp]) ObserveErrorResponseFor(
	ctx context.Context, obs stats.Observer, err error,
) (resp ErrorPatternResponse, matched bool, applyErr error) {
	resp, matched, applyErr = h.ErrorResponseFor(err)
	location := h.Descriptor.Path
	switch {
	case matched && applyErr == nil:
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMatch(location, strconv.Itoa(resp.Status), string(resp.Action))
		}
		if st, ok := obs.(stats.SpanTagger); ok {
			st.TagSpan(ctx, "error_pattern.code", strconv.Itoa(resp.Status))
		}
	case !matched && h.HasErrorPatterns():
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMiss(location)
		}
	}
	return resp, matched, applyErr
}
