package mcp

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/stats"
)

// HasErrorPatterns reports whether at least one [ErrorPattern] is
// declared on h — mirrors [rest.RouteHandle.HasErrorPatterns]/
// [events.ChannelHandle.HasErrorPatterns]/[reqreply.RouteHandle.HasErrorPatterns]
// exactly. Used by [ToolHandle.ObserveErrorResponseFor] to gate
// stats.ErrorPatternObserver.RecordErrorPatternMiss — a tool with NO
// declared patterns has no "miss" to report at all.
func (h *ToolHandle[In, Out]) HasErrorPatterns() bool {
	return len(h.errorPatternRules) > 0
}

// ObserveErrorResponseFor is the observability-aware counterpart of
// [ToolHandle.ErrorResponseFor]: it performs the SAME errors.As match,
// but ALSO reports the outcome to obs — stats.ErrorPatternObserver.
// RecordErrorPatternMatch on a match, RecordErrorPatternMiss on a miss
// (only when [ToolHandle.HasErrorPatterns] is true), and stats.
// SpanTagger.TagSpan when both a match occurs AND obs implements
// SpanTagger — all type-asserted and derived INTERNALLY, so the adapter
// needs no knowledge of ErrorPatternObserver/SpanTagger/HasErrorPatterns
// at all. Returns the IDENTICAL 3-tuple ErrorResponseFor already
// returns, so an adapter's existing dispatch logic needs no changes
// beyond swapping which method it calls.
//
// Unlike REST/events, MCP's [ErrorPatternResponse] has no status/topic
// concept to use as the observer's "code" parameter — the matched
// pattern's OWN declared payload type (fmt.Sprintf("%T", resp.Value))
// is used instead, giving a stable, per-pattern distinguishing
// identifier with no new field needed on ErrorPatternResponse. action is
// always "" (MCP tool results have no ErrorAction concept — mirrors
// reqreply's identical choice, for the identical reason).
//
// This closes docs/design/d-0005-error-handling.md's Topic 5 cross-
// reference ("the identical mechanism in adapters/mcpgo... has the same
// gap... a future MCP-focused round would likely want the same fix
// using the same mechanism designed here") — found during this skill's
// mandatory Phase B re-review of the api/mcp ErrorPattern round, not
// during that round's own Phase A pass.
//
// This is the RECOMMENDED call site for every Category-A failure point
// — adapters should call this instead of ErrorResponseFor directly
// whenever an Observer is in scope.
func (h *ToolHandle[In, Out]) ObserveErrorResponseFor(
	ctx context.Context, obs stats.Observer, err error,
) (resp ErrorPatternResponse, matched bool, applyErr error) {
	resp, matched, applyErr = h.ErrorResponseFor(err)
	location := h.Name
	switch {
	case matched && applyErr == nil:
		code := fmt.Sprintf("%T", resp.Value)
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMatch(location, code, "")
		}
		if st, ok := obs.(stats.SpanTagger); ok {
			st.TagSpan(ctx, "error_pattern.code", code)
		}
	case !matched && h.HasErrorPatterns():
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMiss(location)
		}
	}
	return resp, matched, applyErr
}
