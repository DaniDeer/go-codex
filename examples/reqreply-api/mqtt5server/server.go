// Package mqtt5server assembles routes/+handlers/ onto an in-process mock
// MQTT 5 broker via mqtt5.AttachServer — the reqreply.Server/Client+Attach
// counterpart of examples/rest-api's chiserver/nethttpserver packages.
package mqtt5server

import (
	"context"
	"sync"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/handlers"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/route"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// Built bundles the assembled Server with the mock broker/router the
// caller's Client needs to Attach against (no real MQTT 5 broker required).
type Built struct {
	Server *reqreply.Server
	Broker mqtt5adapter.MQTTClient
	Router mqtt5adapter.MQTTRouter

	// GlobalHandle/SecuredHandle/HeaderParamHandle are the
	// *reqreply.RouteHandle values returned by Register — needed by
	// callers (e.g. mqtt5adapter.Call) that dispatch directly against an
	// already-registered route rather than through a *reqreply.Client.
	GlobalHandle       *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
	SecuredHandle      *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
	HeaderParamHandle  *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
	PropertyAxisHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
}

// LastReplyUserProperties returns the MQTT5 User Properties on the most
// recently server-published message (see [recordingBroker]) — used by
// demo_property_axis_middleware.go to visibly confirm the property
// axis's reply-side "Write-side wiring" fix actually reaches the wire.
func (b *Built) LastReplyUserProperties() pahomqtt5.UserProperties {
	return b.Broker.(*recordingBroker).LastReplyUserProperties()
}

// apiKeyUserProp/traceUserProp are Phase 1b's (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum)
// mqtt5-specific User Property declarations —
// bridged into a [reqreply.Route.Use]-attachable middleware via
// [mqtt5adapter.FromUserPropertyParam]/[mqtt5adapter.
// FromResponseUserPropertyParam]. mqtt5-specific, so declared here (not
// in routes/middleware.go, which stays adapter-agnostic).
var apiKeyUserProp = mqtt5adapter.UserPropertyParam{Name: "X-API-Key", Description: "API key for compute/header-param-add", Required: true}
var traceUserProp = mqtt5adapter.UserPropertyParam{Name: "X-Trace-Id", Description: "Trace correlation id on the reply"}

// Build registers routes.ComputeRoute, routes.SecuredComputeRoute, and
// routes.GlobalOnlyComputeRoute (with GlobalSecurity attached at the
// Server level, not per-route) against a fresh reqreply.Server, then
// mqtt5.AttachServer's it to an in-process mock broker.
//
// No per-route observer wiring is attached here — mqtt5's server-side
// general decorator wraps the raw, pre-decode *pahomqtt5.Publish
// handler, which has no ctx to inject an Observer into (see
// [reqreply.Observability]'s own doc comment for the full rationale).
// mqtt5.AttachServer's dispatch already resolves whichever Observer is
// present in the ctx passed to [reqreply.Server.Serve] (via
// [stats.ObserverFromContext]) for its own RecordRequest calls — main.go
// injects the shared Observer into that ctx ONCE via [stats.WithObserver]
// before Serve runs, which already covers every route below with ZERO
// additional per-route wiring.
func Build() (*Built, error) {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API (mqtt5)", Version: "1.0.0"})
	server.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
	server.AddGlobalSecurity(route.Require("bearerAuth"))

	if _, err := routes.ComputeRoute.
		WithHandler(handlers.Add).
		Register(server); err != nil {
		return nil, err
	}
	// SecuredComputeRoute/GlobalOnlyComputeRoute now declare+implement
	// security via .Use()+.HandleMW() (Phase 1, see
	// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum)
	// — REPLACES the OLD imperative
	// ServeOptions.SecurityFunc mechanism entirely (removed, breaking
	// change). A paired implementation is now REQUIRED for every route
	// with a non-empty effective security requirement — mqtt5.
	// AttachServer's CheckCoverage enforces this at Serve time, closing a
	// latent gap the old SecurityFunc-optional design silently allowed
	// (a route could declare a security scheme with NO enforcing
	// implementation attached anywhere and nothing would ever catch it).
	// The general-purpose observer HandleMW(nil, ...) attaches ALONGSIDE
	// the paired security HandleMW(&routes.BearerAuthMw, ...) below —
	// proving the two mechanisms compose freely on the same route.
	securedHandle, err := routes.SecuredComputeRoute.
		Use(routes.BearerAuthMw).
		HandleMW(&routes.BearerAuthMw, handlers.VerifyBearer).
		WithHandler(handlers.Add).
		Register(server)
	if err != nil {
		return nil, err
	}
	globalHandle, err := routes.GlobalOnlyComputeRoute.
		Use(routes.BearerAuthMw).
		HandleMW(&routes.BearerAuthMw, handlers.VerifyBearer).
		WithHandler(handlers.Add).
		Register(server)
	if err != nil {
		return nil, err
	}
	// HeaderParamComputeRoute demonstrates Phase 1b — NO .HandleMW()
	// pairing needed (unlike security schemes): RequestHeaderParams/
	// ResponseHeaderParams are validated automatically by
	// mqtt5adapter.AttachServer/AttachClient, not gated behind
	// [reqreply.CheckCoverage] (that check is security-scheme-specific).
	headerParamHandle, err := routes.HeaderParamComputeRoute.
		Use(mqtt5adapter.FromUserPropertyParam(apiKeyUserProp), mqtt5adapter.FromResponseUserPropertyParam(traceUserProp)).
		WithHandler(handlers.Add).
		Register(server)
	if err != nil {
		return nil, err
	}
	// PropertyAxisComputeRoute demonstrates the NEW property vocabulary
	// axis (docs/design/d-0003-codec-declared-middlewares.md's Addendum) —
	// routes.TenantPropertyMw (the DECLARATION) + handlers.ProcessTenant
	// (the IMPLEMENTATION) are both adapter-agnostic; THIS is the only
	// mqtt5-specific step — attaching them to a route and registering on
	// THIS server. handlers.ProcessTenant reads the merged
	// TenantIn.TenantID (from the request's "X-Tenant-Id" User Property,
	// carried as a real MQTT5 User Property here) and produces a
	// TenantAck merged into the reply's "X-Ack" User Property, ALONGSIDE
	// the route's own normal ComputeReq/ComputeResp handling (unaffected).
	// See zeromqserver/server.go for the SAME declaration+implementation
	// pair attached to a transport with NO property mechanism at all.
	propertyAxisHandle, err := reqreply.Transform(
		routes.PropertyAxisComputeRoute,
		routes.TenantPropertyMw,
		handlers.ProcessTenant,
	).
		WithHandler(handlers.Add).
		Register(server)
	if err != nil {
		return nil, err
	}

	rawBroker, router := newMockBroker()
	broker := &recordingBroker{MQTTClient: rawBroker}
	if err := mqtt5adapter.AttachServer(server, broker, router); err != nil {
		return nil, err
	}
	return &Built{
		Server:             server,
		Broker:             broker,
		Router:             router,
		GlobalHandle:       globalHandle,
		SecuredHandle:      securedHandle,
		HeaderParamHandle:  headerParamHandle,
		PropertyAxisHandle: propertyAxisHandle,
	}, nil
}

// recordingBroker wraps the mock broker's Publish to keep the LAST
// published message's User Properties observable — used by
// demo_property_axis_middleware.go to visibly confirm "Write-side
// wiring"'s Case 3 fix (the server's reply genuinely carries the
// Middleware-produced "X-Ack" User Property on the real wire, not just
// asserted internally by a unit test).
type recordingBroker struct {
	mqtt5adapter.MQTTClient
	mu   sync.Mutex
	last *pahomqtt5.Publish
}

func (b *recordingBroker) Publish(ctx context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	b.mu.Lock()
	b.last = p
	b.mu.Unlock()
	return b.MQTTClient.Publish(ctx, p)
}

// LastReplyUserProperties returns the User Properties on the most
// recently published message (server-side, including replies) — nil if
// none were set.
func (b *recordingBroker) LastReplyUserProperties() pahomqtt5.UserProperties {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last == nil || b.last.Properties == nil {
		return nil
	}
	return b.last.Properties.User
}

// ── in-process mock broker (self-contained — no real MQTT 5 broker needed) ──
//
// Mirrors examples/adapters-mqtt5's own mockRouter/mockBroker exactly (kept
// here, duplicated, rather than exported from that example — examples are
// standalone `package main` programs, not importable library packages).

type mockRouter struct {
	mu       sync.RWMutex
	handlers map[string]pahomqtt5.MessageHandler
}

func newMockRouter() *mockRouter {
	return &mockRouter{handlers: make(map[string]pahomqtt5.MessageHandler)}
}

func (r *mockRouter) RegisterHandler(topic string, h pahomqtt5.MessageHandler) {
	r.mu.Lock()
	r.handlers[topic] = h
	r.mu.Unlock()
}

func (r *mockRouter) UnregisterHandler(topic string) {
	r.mu.Lock()
	delete(r.handlers, topic)
	r.mu.Unlock()
}

func (r *mockRouter) dispatch(msg *pahomqtt5.Publish) {
	r.mu.RLock()
	var matched []pahomqtt5.MessageHandler
	for topic, h := range r.handlers {
		if topic == msg.Topic || topicMatches(topic, msg.Topic) {
			matched = append(matched, h)
		}
	}
	r.mu.RUnlock()
	for _, h := range matched {
		h(msg)
	}
}

// topicMatches supports exact match and MQTT-wildcard prefix match (a
// handler registered for "replies/<uuid>" matches a message published to
// that same literal topic).
func topicMatches(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	pParts := splitTopic(pattern)
	tParts := splitTopic(topic)
	return matchParts(pParts, tParts)
}

func splitTopic(topic string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(topic); i++ {
		if i == len(topic) || topic[i] == '/' {
			parts = append(parts, topic[start:i])
			start = i + 1
		}
	}
	return parts
}

func matchParts(pattern, topic []string) bool {
	if len(pattern) == 0 {
		return len(topic) == 0
	}
	if pattern[0] == "#" {
		return true
	}
	if len(topic) == 0 {
		return false
	}
	if pattern[0] == "+" || pattern[0] == topic[0] {
		return matchParts(pattern[1:], topic[1:])
	}
	return false
}

// mockBroker implements mqtt5adapter.MQTTClient and routes published
// messages to the shared mockRouter, simulating a broker round trip.
type mockBroker struct {
	router *mockRouter
}

func newMockBroker() (*mockBroker, *mockRouter) {
	router := newMockRouter()
	return &mockBroker{router: router}, router
}

func (b *mockBroker) Publish(_ context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	go b.router.dispatch(p)
	return &pahomqtt5.PublishResponse{}, nil
}

func (b *mockBroker) Subscribe(_ context.Context, _ *pahomqtt5.Subscribe) (*pahomqtt5.Suback, error) {
	return &pahomqtt5.Suback{}, nil
}

func (b *mockBroker) Unsubscribe(_ context.Context, _ *pahomqtt5.Unsubscribe) (*pahomqtt5.Unsuback, error) {
	return &pahomqtt5.Unsuback{}, nil
}
