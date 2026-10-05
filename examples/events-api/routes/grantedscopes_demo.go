package routes

import (
	"context"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT CLASS of security attachment  ──
// ── than APIKeyAuthMW above. APIKeyAuthMW is the REUSABLE class         ──
// ── (events.SecurityMiddleware[In,Out].WithReceive(fn), attached via    ──
// ── plain .Use(mw)) paired with an adapter-specific Fn that has NO      ──
// ── access to the channel's own decoded *T at all. GrantedScopesSensorMw──
// ── below instead uses the channel-BOUND class — a REAL credential type ──
// ── (AuthIn) and a REAL GrantedScopes-carrying Out (AuthOut), its Fn    ──
// ── (handlers.VerifyAPIKeyGS, func(ctx, *T, In) (Out, error)) EMBEDDED  ──
// ── AT CONSTRUCTION via events.BoundSecuritySubscribeMiddleware and     ──
// ── attached via the dedicated Subscriber.SubscribeBoundMW method —     ──
// ── see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's events/                ──
// ── BoundSubscribeMiddleware section for the full class split.

// AuthIn is GrantedScopesSensorMw's credential vocabulary — decoded from
// the "X-API-Key" User Property via the required property merge field
// below (WithSubscribeProperty).
type AuthIn struct{ Key string }

// AuthOut carries the conventional GrantedScopes map[string][]string
// field, merged into the SAME middleware.CheckScopes call the legacy
// SubscribeMW path above already uses.
type AuthOut struct {
	GrantedScopes map[string][]string
}

// GrantedScopesUserIDField is a middleware.ContextField[string] — the
// authenticated API key published by GrantedScopesSensorMw's paired
// SubscribeMW Fn and consumed by the real subscribe handler via Get(ctx),
// with ZERO manual re-decoding (docs/design/
// d-0007-declarative-middleware-layering.md's Phase 3). Subscribe has no
// reply/Out direction of its own, so only SetContextFieldFromIn is
// meaningful here — the asymmetry events' own design doc documents.
var GrantedScopesUserIDField = middleware.NewContextField(codex.String())

// NewGrantedScopesSensorMw builds the channel-BOUND "apiKeyGS" security
// middleware for GrantedScopesSub — via
// [events.BoundSecuritySubscribeMiddleware], whose Fn is EMBEDDED AT
// CONSTRUCTION (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's events/
// BoundSubscribeMiddleware class). fn is supplied by the CALLER (see
// demo_granted_scopes_context_field.go, which passes
// handlers.VerifyAPIKeyGS) rather than being a package-level value
// embedded directly here: handlers.VerifyAPIKeyGS lives in the handlers
// package, which itself imports routes (for routes.SensorReading/
// routes.AuthIn/routes.AuthOut) — embedding it directly in THIS package
// would create an import cycle. The declarative shape (scheme, scopes,
// the "X-API-Key" property merge field, the ContextField wiring) still
// lives entirely in routes, same as before.
func NewGrantedScopesSensorMw(fn func(ctx context.Context, msg *SensorReading, in AuthIn) (AuthOut, error)) events.BoundSubscribeMiddleware[SensorReading, AuthIn, AuthOut] {
	return events.BoundSecuritySubscribeMiddleware[SensorReading, AuthIn, AuthOut]("apiKeyGS",
		events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")}, []string{"read:sensors"},
		fn,
	).WithSubscribeProperty(events.NewPropertyParam("X-API-Key", codex.String(),
		func(in AuthIn) string { return in.Key },
		func(in *AuthIn, v string) { in.Key = v },
	)).SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Key })
}

// GrantedScopesChannel is a dedicated channel (not SensorDataChannel
// above) so this demo's Attach-time topology stays independent of the
// shared mqtt5broker/mqttbroker/zeromqbroker fixtures every other demo
// reuses.
var GrantedScopesChannel = events.NewChannel[SensorReading](
	"sensor/data-gs",
	SensorReadingCodec,
	events.ChannelMeta{Description: "GrantedScopes + ContextField demo channel."},
)

// GrantedScopesSub declares the "read:sensors" requirement directly on
// Subscribe.Security — attachment of the actual "apiKeyGS" middleware
// happens at the demo call site via
// .SubscribeBoundMW(routes.NewGrantedScopesSensorMw(handlers.VerifyAPIKeyGS))
// (see demo_granted_scopes_context_field.go), NOT via .Use(): a
// [events.BoundSubscribeMiddleware] value deliberately does NOT satisfy
// .Use()'s accepted [middleware.RouteMiddleware] interface — only
// [Subscriber.SubscribeBoundMW] accepts it (docs/design/d-0003-codec-
// declared-middlewares.md's Addendum 7, events/BoundSubscribeMiddleware class).
var GrantedScopesSub = GrantedScopesChannel.WithSubscribe(events.Subscribe{
	Summary:  "GrantedScopes + ContextField demo subscribe",
	Security: []route.SecurityRequirement{route.Require("apiKeyGS", "read:sensors")},
})

// GrantedScopesPub is the matching PLAIN publish declaration (no
// credential-supplying PublishMW needed for this demo — the mock broker
// publish call sets the User Property directly via PublishOptions).
var GrantedScopesPub = GrantedScopesChannel.WithPublish(events.Publish{Summary: "GrantedScopes + ContextField demo publish"})
