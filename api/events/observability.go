package events

import (
	"context"

	"github.com/DaniDeer/go-codex/stats"
)

// ObserveErrorResponseFor is the observability-aware counterpart of
// [ChannelHandle.ErrorResponseFor]: it performs the SAME errors.As match,
// but ALSO reports the outcome to obs — stats.ErrorPatternObserver.
// RecordErrorPatternMatch on a match, RecordErrorPatternMiss on a miss
// (only when [ChannelHandle.HasErrorPatterns] is true), and stats.
// SpanTagger.TagSpan when both a match occurs AND obs implements
// SpanTagger — all type-asserted and derived INTERNALLY, so the adapter
// needs no knowledge of ErrorPatternObserver/SpanTagger/HasErrorPatterns
// at all. Returns the IDENTICAL 3-tuple ErrorResponseFor already
// returns, so an adapter's existing resp.Action-dispatch logic needs no
// changes beyond swapping which method it calls.
//
// This is the RECOMMENDED call site for every Category-A failure point
// (see docs/roadmap/error-handling-rest-events-reqreply.md's Topic 1/5)
// — adapters should call this instead of ErrorResponseFor directly
// whenever an Observer is in scope.
func (h *ChannelHandle[T]) ObserveErrorResponseFor(
	ctx context.Context, obs stats.Observer, err error,
) (resp ErrorChannelResponse, matched bool, applyErr error) {
	resp, matched, applyErr = h.ErrorResponseFor(err)
	location := h.Topic
	switch {
	case matched && applyErr == nil:
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMatch(location, resp.Topic, string(resp.Action))
		}
		if st, ok := obs.(stats.SpanTagger); ok {
			st.TagSpan(ctx, "error_pattern.code", resp.Topic)
		}
	case !matched && h.HasErrorPatterns():
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMiss(location)
		}
	}
	return resp, matched, applyErr
}

// Observability builds a general-purpose
// `func(next func(context.Context, T) error) func(context.Context, T) error`
// closure — the pub/sub analogue of [adapters/nethttp.Observability]
// (REST) and [api/reqreply.Observability] (reqreply) — for
// declare-time, per-channel attachment via
// `sub.SubscribeMW(nil, events.Observability[T](obs))` or
// `pub.PublishMW(nil, events.Observability[T](obs))` (unpaired: mw is
// nil, so it runs unconditionally, wrapping the actual
// handler/transmit-step invocation — see
// docs/design/d-0002-pubsub-workflow-simplification.md's "General-purpose
// (non-spec) Fn shapes for SubscribeMW/PublishMW" subsection).
//
// This is the SHARED, adapter-agnostic core moved out of
// `adapters/zeromq.Observability[T]` (confirmed to have ZERO
// zeromq-specific code) — `adapters/mqtt5.Observability[T]`/
// `adapters/mqtt.Observability[T]` delegate to this for their own
// shared part, then additionally call
// [stats.Observer.RecordSubscribe]/RecordPublish and drive a
// [stats.TraceObserver] span on top (genuinely adapter-specific
// behavior that cannot move here — see those adapters' own
// `Observability[T]` doc comments). `adapters/zeromq` has no such
// remainder, so it uses this function directly with no wrapper of its
// own.
//
// Injects obs into ctx via [stats.WithObserver] (so a security-shaped Fn,
// or any downstream code, can resolve the SAME observer via
// [stats.ObserverFromContext]) and drains [stats.DiagnosticsFromContext]
// into [stats.Observer.RecordValidationError] after next returns. Does
// NOT itself call [stats.Observer.RecordSubscribe]/RecordPublish — those
// per-message lifecycle events are recorded elsewhere (either directly by
// an adapter's subscribe/publish dispatch via its own
// `SubscribeOptions.Observer`/`PublishOptions.Observer`, or by an
// adapter-specific wrapper like `adapters/mqtt5.Observability[T]` that
// delegates to this function) — this general-purpose hook is an
// ADDITIONAL, opt-in mechanism for declare-time attachment, not a
// replacement.
func Observability[T any](obs stats.Observer) func(func(context.Context, T) error) func(context.Context, T) error {
	return func(next func(context.Context, T) error) func(context.Context, T) error {
		return func(ctx context.Context, msg T) error {
			ctx = stats.WithDiagnostics(ctx)
			ctx = stats.WithObserver(ctx, obs)
			err := next(ctx, msg)
			for _, d := range stats.DiagnosticsFromContext(ctx) {
				obs.RecordValidationError(d.Location, d.ConstraintName, d.Field)
			}
			return err
		}
	}
}
