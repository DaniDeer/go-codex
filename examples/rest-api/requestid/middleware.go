// Package requestid is this example's SELF-CONTAINED, generic
// request-correlation-ID module — a plain, Req-agnostic reusable-class
// `rest.Middleware` bundled together with its own In/Out vocabulary and
// codecs, modeling how a real service would factor out a reusable
// cross-cutting logging concern into its own library, mirroring this
// project's `auth/`/`observer/` packages' identical "declaration +
// implementation together" precedent. Kept SEPARATE from `observer/`
// because it is a request-correlation/tracing concern, not a
// stats.Observer/timing concern.
package requestid

import (
	"context"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// In/Out are [ReusableRequestIDMw]'s vocabulary — a generic, NON-Security
// middleware (no scheme, no GrantedScopes) that merges an optional
// "X-Demo-Request-Id" header and simply logs it. Out is empty: this
// middleware's only purpose is the side effect (logging), not producing
// a value any downstream code reads.
type In struct{ RequestID string }
type Out struct{}

var (
	inCodec = codex.Struct[In](
		codex.OptionalField("requestID", codex.String(),
			func(r In) string { return r.RequestID },
			func(r *In, v string) { r.RequestID = v },
		),
	)
	outCodec = codex.Struct[Out]()
)

// ReusableRequestIDMw is Class 1 (reusable) standalone: a plain
// [rest.Middleware], attached via .Use() (see chiserver/server.go), that
// reads the optional "X-Demo-Request-Id" header and logs it — generic
// across ANY route's Req type, since receiveFn never touches Req. This
// SAME value could be .Use()'d on every route in this example without
// any per-route wrapping (unlike the bound class, which needs one
// instantiation per Req type) — the headline "reusable" property.
var ReusableRequestIDMw = rest.NewMiddleware(
	middleware.NewDeclaration[In, Out]("logRequestID", inCodec, outCodec),
).
	WithRequestHeader(rest.NewOptionalHeaderParam("X-Demo-Request-Id", codex.String(),
		func(in In) string { return in.RequestID },
		func(in *In, v string) { in.RequestID = v },
	)).
	WithReceive(func(_ context.Context, in In) (Out, error) {
		if in.RequestID != "" {
			println("  [ReusableRequestIDMw] X-Demo-Request-Id =", in.RequestID)
		}
		return Out{}, nil
	})
