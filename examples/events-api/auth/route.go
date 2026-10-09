package auth

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
)

// GrantedScopesChannel is a dedicated channel (not routes.SensorDataChannel)
// so this demo's Attach-time topology stays independent of the shared
// mqtt5broker/mqttbroker/zeromqbroker fixtures every other demo reuses.
var GrantedScopesChannel = events.NewChannel[routes.SensorReading](
	"sensor/data-gs",
	routes.SensorReadingCodec,
	events.ChannelMeta{Description: "GrantedScopes + ContextField demo channel."},
)

// GrantedScopesSub declares the "read:sensors" requirement directly on
// Subscribe.Security — attachment of the actual "apiKeyGS" middleware
// happens at the demo call site via
// .SubscribeBoundMW(auth.NewGrantedScopesSensorMw(auth.VerifyAPIKeyGS))
// (see demo_granted_scopes_context_field.go), NOT via .Use(): a
// [events.BoundSubscribeMiddleware] value deliberately does NOT satisfy
// .Use()'s accepted [events.RouteMiddleware] interface — only
// [Subscriber.SubscribeBoundMW] accepts it (docs/design/d-0003-codec-
// declared-middlewares.md's Addendum 7, events/BoundSubscribeMiddleware class).
var GrantedScopesSub = GrantedScopesChannel.WithSubscribe(events.Subscribe{
	Summary:  "GrantedScopes + ContextField demo subscribe",
	Security: []events.SecurityRequirement{events.Require("apiKeyGS", "read:sensors")},
})

// GrantedScopesPub is the matching PLAIN publish declaration (no
// credential-supplying PublishMW needed for this demo — the mock broker
// publish call sets the User Property directly via PublishOptions).
var GrantedScopesPub = GrantedScopesChannel.WithPublish(events.Publish{Summary: "GrantedScopes + ContextField demo publish"})
