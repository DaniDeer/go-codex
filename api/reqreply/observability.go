package reqreply

import (
	"context"

	"github.com/DaniDeer/go-codex/stats"
)

// Observability builds a general-purpose
// `func(next func(context.Context, Req) (Resp, error)) func(context.Context,
// Req) (Resp, error)` closure — the reqreply analogue of
// [adapters/nethttp.Observability] (REST) and
// [adapters/zeromq.Observability] (pub/sub) — for declare-time, per-route
// attachment via `route.HandleMW(nil, reqreply.Observability[Req,
// Resp](obs))` (server) or `route.ClientMW(nil,
// reqreply.Observability[Req, Resp](obs))` (client). Unlike its REST and
// pub/sub siblings, this ONE function covers every reqreply attachment
// point that has a ctx to work with — zeromq's server-side AND
// client-side general decorators, plus mqtt5's client-side one, all
// share this EXACT generic shape (confirmed via each adapter's own
// reqreply transport Fn-shape validation), so it lives here in the core
// `api/reqreply` package instead of being duplicated per adapter.
//
// mqtt5's SERVER side has no equivalent attachment point: its
// server-side general decorator wraps the raw, pre-decode
// `*pahomqtt5.Publish` handler, which has no `ctx` parameter to inject
// obs into. mqtt5's `Server.Serve(ctx, ...)` already resolves whichever
// Observer is present in the ctx it was called with (via
// [stats.ObserverFromContext]) for its own dispatch-time
// [stats.Observer.RecordRequest] calls — injecting obs once into that
// ctx (via [stats.WithObserver], e.g. before calling Serve) already
// covers the server side with zero additional wiring, so no
// mqtt5-specific server helper is shipped.
//
// Injects obs into ctx via [stats.WithObserver] (so a paired security Fn,
// or any downstream code, can resolve the SAME observer via
// [stats.ObserverFromContext]) and drains [stats.DiagnosticsFromContext]
// into [stats.Observer.RecordValidationError] after next returns.
// Deliberately does NOT itself call [stats.Observer.RecordRequest] or
// start a [stats.TraceObserver] span — every reqreply adapter transport
// (zeromq's and mqtt5's Serve/Call dispatch) ALREADY calls RecordRequest
// and drives TraceObserver spans unconditionally on every code path, so
// doing so again here would double-count/duplicate those events. This
// mirrors [adapters/zeromq.Observability]'s (pub/sub) identical
// non-duplication rationale ("Does NOT itself call
// RecordSubscribe/RecordPublish").
func Observability[Req, Resp any](obs stats.Observer) func(func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(next func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
		return func(ctx context.Context, req Req) (Resp, error) {
			ctx = stats.WithDiagnostics(ctx)
			ctx = stats.WithObserver(ctx, obs)
			resp, err := next(ctx, req)
			for _, d := range stats.DiagnosticsFromContext(ctx) {
				obs.RecordValidationError(d.Location, d.ConstraintName, d.Field)
			}
			return resp, err
		}
	}
}
