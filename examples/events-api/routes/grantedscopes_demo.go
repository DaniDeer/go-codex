package routes

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than APIKeyAuthMW above. APIKeyAuthMW uses the LEGACY shape:        ──
// ── events.SecurityMiddleware[struct{}, struct{}] paired with an        ──
// ── adapter-specific implementation Fn (handlers.MQTT5SecurityImpl,     ──
// ── reading a raw *paho.Publish's User Properties directly).            ──
// ── GrantedScopesSensorMw below instead uses the GENERALIZED form — a   ──
// ── REAL credential type (AuthIn) and a REAL GrantedScopes-carrying Out ──
// ── (AuthOut) — dispatched through SubscribeMW's NEW additive 2-return  ──
// ── bound shape (func(ctx, *T, In) (Out, error)), the flagship Rollout  ──
// ── Phase B/pre-Phase-C capability no example in this repo had          ──
// ── exercised until now.

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

// GrantedScopesSensorMw declares the "apiKeyGS" scheme, requiring
// "read:sensors" — built via the GENERALIZED events.SecurityMiddleware[
// In, Out] (not [struct{},struct{}] like APIKeyAuthMW above).
var GrantedScopesSensorMw = events.SecurityMiddleware[AuthIn, AuthOut]("apiKeyGS",
	events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")}, []string{"read:sensors"},
).WithSubscribeProperty(events.NewPropertyParam("X-API-Key", codex.String(),
	func(in AuthIn) string { return in.Key },
	func(in *AuthIn, v string) { in.Key = v },
)).SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Key })

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
// Subscribe.Security — deliberately NOT via .Use(GrantedScopesSensorMw).
// See the "Known gap" callout in docs/features/security.md's events
// section... actually events does NOT have this gap (different
// spec-bundling architecture, confirmed via docs/design/
// d-0007-declarative-middleware-layering.md's own Learnings) — kept here
// as .Use()+SubscribeMW anyway for parity with
// adapters/mqtt5/grantedscopes_test.go's own precedent, which uses
// exactly this combination safely.
var GrantedScopesSub = GrantedScopesChannel.WithSubscribe(events.Subscribe{
	Summary:  "GrantedScopes + ContextField demo subscribe",
	Security: []route.SecurityRequirement{route.Require("apiKeyGS", "read:sensors")},
}).Use(GrantedScopesSensorMw)

// GrantedScopesPub is the matching PLAIN publish declaration (no
// credential-supplying PublishMW needed for this demo — the mock broker
// publish call sets the User Property directly via PublishOptions).
var GrantedScopesPub = GrantedScopesChannel.WithPublish(events.Publish{Summary: "GrantedScopes + ContextField demo publish"})
