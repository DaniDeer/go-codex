package zeromq

import (
	"context"

	"github.com/DaniDeer/go-codex/stats"
)

// Observability builds a general-purpose
// `func(next func(context.Context, T) error) func(context.Context, T) error`
// closure — the pub/sub analogue of [nethttp.Observability] — for
// declare-time, per-channel attachment via
// `sub.SubscribeMW(nil, zeromq.Observability[T](obs))` or
// `pub.PublishMW(nil, zeromq.Observability[T](obs))` (unpaired: mw is
// nil, so it runs unconditionally, wrapping the actual
// handler/transmit-step invocation — see
// docs/design/d-0002-pubsub-workflow-simplification.md's "General-purpose
// (non-spec) Fn shapes for SubscribeMW/PublishMW" subsection).
//
// Injects obs into ctx via [stats.WithObserver] (so a security-shaped Fn,
// or any downstream code, can resolve the SAME observer via
// [stats.ObserverFromContext]) and drains [stats.DiagnosticsFromContext]
// into [stats.Observer.RecordValidationError] after next returns. Does
// NOT itself call [stats.Observer.RecordSubscribe]/RecordPublish — those
// per-message lifecycle events are ALREADY recorded by
// [SubscribeWithHandle]/[Publish] via [SubscribeOptions.Observer]/
// [PublishOptions.Observer] (or the ctx-injection fallback via
// [stats.ObserverFromContext]) — this general-purpose hook is an
// ADDITIONAL, opt-in mechanism for declare-time attachment, not a
// replacement (see the roadmap subsection's "Observer specifically does
// NOT need to move off SubscribeOptions.Observer/PublishOptions.Observer
// entirely" note).
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
