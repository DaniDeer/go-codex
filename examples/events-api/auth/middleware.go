// Package auth is this example's SELF-CONTAINED auth module — codecs,
// middleware declarations, verifier/handler IMPLEMENTATIONS, and
// auth-flow demo channels (GrantedScopesChannel/Sub/Pub) all live
// together here, modeling how a real service would factor out a
// reusable auth library consumed by multiple services (mirrors
// examples/reqreply-api's/examples/rest-api's own auth/ packages — same
// reasoning, same per-file split). Plain BUSINESS channels that merely
// ATTACH this package's middleware (e.g. routes.SensorDataSub) stay in
// routes/, importing auth.X — a one-way dependency: auth/ MAY import
// routes/ (e.g. for routes.SensorReading, the shared message type),
// routes/ must NEVER import auth/ (would create a cycle).
package auth

import (
	"context"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── "apiKeyAuth" scheme ───────────────────────────────────────────────────
//
// Declared once — referenced via events.FromSecurityScheme + Subscriber/
// Publisher.Use on any channel that needs it. The Codec field is omitted
// (nil): none of the 3 pub/sub adapters can extract a credential purely
// from message metadata in a protocol-agnostic way (mqtt v3 has none at
// all; mqtt5's User Properties and zeromq's in-payload field are both
// adapter-specific extraction mechanisms), so codec-level FORMAT
// validation of the extracted credential happens per-adapter instead (see
// handler.go).
var APIKeyAuth = events.SecurityScheme{
	SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
}

// APIKeyAuthIn is NewAPIKeyAuthMW's credential vocabulary — decoded from
// whatever transport-specific channel each adapter-specific attachment
// site (mqttbroker/mqtt5broker/zeromqbroker) has available (mqtt5's User
// Property via WithSubscribeProperty, mqtt v3's CONNECT-time closure,
// zeromq leaving it zero) — see handler.go.
type APIKeyAuthIn struct{ Key string }

// APIKeyAuthOut carries the conventional GrantedScopes field, populated
// by every NewAPIKeyAuthMW-paired Fn on success (even though this scheme
// declares zero specific scopes — see NewAPIKeyAuthMW's own doc comment
// for why the map KEY's presence still matters at runtime).
type APIKeyAuthOut struct {
	GrantedScopes map[string][]string
}

// NewAPIKeyAuthMW builds the "apiKeyAuth" security middleware for
// routes.SensorDataSub/SecuredReadingsSub — via the channel-BOUND class
// ([events.BoundSecuritySubscribeMiddleware]), DELIBERATELY NOT the
// REUSABLE class ([events.SecurityMiddleware]+[events.Middleware.
// WithReceive]) a first migration pass of this demo used.
//
// Why: every adapter's runtime scope-enforcement path
// (middleware.CheckScopes, invoked unconditionally whenever a channel
// declares Subscribe.Security — see adapters/mqtt5/adapter.go,
// adapters/mqtt/adapter.go, adapters/zeromq/adapter.go) requires the
// scheme name to be present as a KEY in the merged `granted` map — even
// when, as here, zero specific scopes are required (route.Satisfied
// still checks presence before checking an empty want-scopes list is
// trivially satisfied). Only a HasOut-true dispatch handler can populate
// that key (via adapters/internal/scopesmerge.MergeHandlerGrants reading
// GrantedScopes off a handler's decoded Out) — and HasOut is ALWAYS true
// for [events.BoundSubscribeMiddleware] (its Fn ALWAYS returns
// `(Out, error)`), but ALWAYS false for the reusable class's
// WithReceive-bundled Fn (`func(ctx, In) error`, no Out return at all —
// events.Middleware's own doc comment confirms this structural
// asymmetry). A reusable-class Security attachment can therefore only
// ever be used for an UNPAIRED (no declared Subscribe.Security) general-
// purpose presence check — never to satisfy a DECLARED requirement like
// SensorDataSub's own `Security: []route.SecurityRequirement{route.
// Require("apiKeyAuth")}`, confirmed via an actual end-to-end run
// regression during this migration (see docs/design/
// d-0003-codec-declared-middlewares.md's Addendum 7).
//
// fn is supplied by the caller (MQTTSecurityImpl/MQTT5SecurityImpl/
// ZeromqSecurityImpl, handler.go) — none of them need the *msg parameter
// (no per-message credential in this demo's routes.SensorReading), they
// are attached via the bound mechanism purely to get a
// GrantedScopes-carrying Out, not because they need *T access.
func NewAPIKeyAuthMW(fn func(ctx context.Context, msg *routes.SensorReading, in APIKeyAuthIn) (APIKeyAuthOut, error)) events.BoundSubscribeMiddleware[routes.SensorReading, APIKeyAuthIn, APIKeyAuthOut] {
	return events.BoundSecuritySubscribeMiddleware[routes.SensorReading, APIKeyAuthIn, APIKeyAuthOut]("apiKeyAuth", APIKeyAuth, nil, fn)
}

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT CLASS of security attachment  ──
// ── than APIKeyAuthMW above. APIKeyAuthMW is the REUSABLE class         ──
// ── (events.SecurityMiddleware[In,Out].WithReceive(fn), attached via    ──
// ── plain .Use(mw)) paired with an adapter-specific Fn that has NO      ──
// ── access to the channel's own decoded *T at all. GrantedScopesSensorMw──
// ── below instead uses the channel-BOUND class — a REAL credential type ──
// ── (AuthIn) and a REAL GrantedScopes-carrying Out (AuthOut), its Fn    ──
// ── (VerifyAPIKeyGS, func(ctx, *T, In) (Out, error)) EMBEDDED AT        ──
// ── CONSTRUCTION via events.BoundSecuritySubscribeMiddleware and        ──
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
// CONSTRUCTION (docs/design/d-0003-codec-declared-middlewares.md's
// Addendum 7's events/BoundSubscribeMiddleware class). fn is supplied by
// the CALLER (see demo_granted_scopes_context_field.go, which passes
// VerifyAPIKeyGS, handler.go).
func NewGrantedScopesSensorMw(fn func(ctx context.Context, msg *routes.SensorReading, in AuthIn) (AuthOut, error)) events.BoundSubscribeMiddleware[routes.SensorReading, AuthIn, AuthOut] {
	return events.BoundSecuritySubscribeMiddleware[routes.SensorReading, AuthIn, AuthOut]("apiKeyGS",
		events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")}, []string{"read:sensors"},
		fn,
	).WithSubscribeProperty(events.NewPropertyParam("X-API-Key", codex.String(),
		func(in AuthIn) string { return in.Key },
		func(in *AuthIn, v string) { in.Key = v },
	)).SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Key })
}
