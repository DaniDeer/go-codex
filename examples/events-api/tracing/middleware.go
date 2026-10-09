// Package tracing is this example's SELF-CONTAINED, generic
// request-correlation/trace-ID module — a plain, T-agnostic
// reusable-class `events.Middleware` bundled together with its own
// In/Out vocabulary and codecs, modeling how a real service would
// factor out a reusable cross-cutting concern into its own library,
// mirroring this project's `auth/`/`observer/` packages' identical
// "declaration + implementation together" precedent. The pub/sub
// analogue of examples/rest-api's `requestid/` package. Kept SEPARATE
// from `observer/` because it is a request-correlation/tracing concern,
// not a stats.Observer/timing concern.
package tracing

import (
	"context"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// In/Out are [ReusablePresenceMw]'s vocabulary — a generic, NON-Security
// middleware (no scheme, no GrantedScopes) that merges an optional
// "X-Demo-Trace-Id" User Property and simply logs it.
type In struct{ TraceID string }
type Out struct{}

var (
	inCodec = codex.Struct[In](
		codex.OptionalField("traceID", codex.String(),
			func(r In) string { return r.TraceID },
			func(r *In, v string) { r.TraceID = v },
		),
	)
	outCodec = codex.Struct[Out]()
)

// ReusablePresenceMw is Class 1 (reusable) standalone: a plain
// [events.Middleware], attached via .Use() (see
// demo_bound_middleware_split.go), that reads the optional
// "X-Demo-Trace-Id" User Property and logs it — generic across ANY
// channel's T, since receiveFn never touches T. This SAME value could be
// .Use()'d on any subscriber without per-channel wrapping — unlike the
// bound class, which needs one instantiation per T.
var ReusablePresenceMw = events.NewMiddleware(
	events.NewDeclaration[In, Out]("logTraceID", inCodec, outCodec),
).
	WithSubscribeProperty(events.NewPropertyParam("X-Demo-Trace-Id", codex.String(),
		func(in In) string { return in.TraceID },
		func(in *In, v string) { in.TraceID = v },
	)).
	WithReceive(func(_ context.Context, in In) error {
		if in.TraceID != "" {
			println("  [ReusablePresenceMw] X-Demo-Trace-Id =", in.TraceID)
		}
		return nil
	})
