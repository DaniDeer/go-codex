package routes

import (
	"context"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── Bound-middleware-split demo (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) ──
//
// A direct, side-by-side contrast of the two classes the split
// introduced, mirroring examples/rest-api's own
// demo_bound_middleware_split.go:
//
//  1. REUSABLE ALONE (Class 1) — [ReusablePresenceMw] below: an
//     [events.Middleware] attached via plain .Use() on
//     [ReusableAloneSub], which declares NO Subscribe.Security — a
//     reusable-class Security attachment can NEVER satisfy a DECLARED
//     requirement (see routes.go's [NewAPIKeyAuthMW] doc comment for the
//     full reasoning, confirmed via an actual regression during this
//     session's own migration), so this is the only way to demonstrate
//     Class 1 standalone here: an UNPAIRED, general-purpose presence
//     check.
//  2. BOUND ALONE (Class 2) — ALREADY fully demonstrated: [NewAPIKeyAuthMW]
//     is reused, unchanged, across BOTH [SensorDataSub] AND
//     [SecuredReadingsSub] (2 different Subscriber declarations sharing
//     the SAME T=SensorReading) — not re-demonstrated here to avoid
//     duplicating that existing, already-tested coverage.
//  3. STACKED (both together, ONE subscriber) — [StackedDemoSub] below:
//     .Use(ReusablePresenceMw) (a generic, cross-cutting concern) run
//     FIRST, then .SubscribeBoundMW(NewAPIKeyAuthMW(...)) (the
//     channel-specific "apiKeyAuth" security check, reusing the SAME
//     bound helper [SensorDataSub]/[SecuredReadingsSub] already use) run
//     SECOND — proving a reusable and a bound attachment compose on one
//     subscriber, in declaration order.

// PresenceIn/PresenceOut are [ReusablePresenceMw]'s vocabulary — a
// generic, NON-Security middleware (no scheme, no GrantedScopes) that
// merges an optional "X-Demo-Trace-Id" User Property and simply logs it.
type PresenceIn struct{ TraceID string }
type PresenceOut struct{}

var (
	presenceInCodec = codex.Struct[PresenceIn](
		codex.OptionalField("traceID", codex.String(),
			func(r PresenceIn) string { return r.TraceID },
			func(r *PresenceIn, v string) { r.TraceID = v },
		),
	)
	presenceOutCodec = codex.Struct[PresenceOut]()
)

// ReusablePresenceMw is Class 1 (reusable) standalone: a plain
// [events.Middleware], attached via .Use() (see
// demo_bound_middleware_split.go), that reads the optional
// "X-Demo-Trace-Id" User Property and logs it —
// generic across ANY channel's T, since receiveFn never touches T. This
// SAME value could be .Use()'d on any subscriber without per-channel
// wrapping — unlike the bound class, which needs one instantiation per T.
var ReusablePresenceMw = events.NewMiddleware(
	middleware.NewDeclaration[PresenceIn, PresenceOut]("logTraceID", presenceInCodec, presenceOutCodec),
).
	WithSubscribeProperty(events.NewPropertyParam("X-Demo-Trace-Id", codex.String(),
		func(in PresenceIn) string { return in.TraceID },
		func(in *PresenceIn, v string) { in.TraceID = v },
	)).
	WithReceive(func(_ context.Context, in PresenceIn) error {
		if in.TraceID != "" {
			println("  [ReusablePresenceMw] X-Demo-Trace-Id =", in.TraceID)
		}
		return nil
	})

// ReusableAloneChannel/ReusableAloneSub — a dedicated channel with NO
// declared Subscribe.Security, so [ReusablePresenceMw] can attach via
// .Use() ALONE (see demo_bound_middleware_split.go) and demonstrate
// Class 1 in total isolation from the bound class.
var ReusableAloneChannel = events.NewChannel[SensorReading](
	"sensor/data-reusable-alone",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Reusable middleware class, attached alone (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)."},
)

var ReusableAloneSub = ReusableAloneChannel.WithSubscribe(events.Subscribe{
	Summary: "Reusable middleware class, attached alone (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)",
})

// ReusableAlonePub is the matching PLAIN publish declaration for
// [ReusableAloneChannel] — no credential-supplying PublishMW needed.
var ReusableAlonePub = ReusableAloneChannel.WithPublish(events.Publish{
	Summary: "Reusable middleware class, attached alone (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)",
})

// StackedDemoChannel/StackedDemoSub — a dedicated channel so this demo's
// topology stays independent of SensorDataChannel's own shared fixture.
// Declares "apiKeyAuth" security directly on Subscribe.Security (same
// scheme NewAPIKeyAuthMW already declares against — a DECLARED
// requirement, satisfiable ONLY by the bound class) — attachment of BOTH
// [ReusablePresenceMw] (.Use()) and [NewAPIKeyAuthMW] (.SubscribeBoundMW())
// happens at demo_bound_middleware_split.go's call site (a
// self-contained client, mirroring demoGrantedScopesContextField's own
// precedent — NOT mqtt5broker.Build(), which attaches neither).
var StackedDemoChannel = events.NewChannel[SensorReading](
	"sensor/data-stacked",
	SensorReadingCodec,
	events.ChannelMeta{Description: "Reusable + bound middleware STACKED demo channel."},
)

var StackedDemoSub = StackedDemoChannel.WithSubscribe(events.Subscribe{
	Summary:  "Reusable + bound middleware STACKED on one subscriber (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)",
	Security: []route.SecurityRequirement{route.Require("apiKeyAuth")},
})

// StackedDemoPub is the matching PLAIN publish declaration for
// [StackedDemoChannel] — the mock broker publish call sets the
// "X-API-Key" User Property directly (mirrors [SensorDataPub]'s own
// pattern), no credential-supplying PublishMW needed.
var StackedDemoPub = StackedDemoChannel.WithPublish(events.Publish{
	Summary: "Reusable + bound middleware STACKED on one subscriber (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)",
})
