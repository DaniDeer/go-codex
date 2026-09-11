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

	// GlobalHandle/SecuredHandle are the *reqreply.RouteHandle values
	// returned by Register — needed by callers (e.g. mqtt5adapter.Call)
	// that dispatch directly against an already-registered route rather
	// than through a *reqreply.Client.
	GlobalHandle  *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
	SecuredHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
}

// Build registers routes.ComputeRoute, routes.SecuredComputeRoute, and
// routes.GlobalOnlyComputeRoute (with GlobalSecurity attached at the
// Server level, not per-route) against a fresh reqreply.Server, then
// mqtt5.AttachServer's it to an in-process mock broker.
func Build() (*Built, error) {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API (mqtt5)", Version: "1.0.0"})
	server.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
	server.AddGlobalSecurity(route.Require("bearerAuth"))

	if _, err := routes.ComputeRoute.WithHandler(handlers.Add).Register(server); err != nil {
		return nil, err
	}
	securedHandle, err := routes.SecuredComputeRoute.WithHandler(handlers.Add).Register(server)
	if err != nil {
		return nil, err
	}
	globalHandle, err := routes.GlobalOnlyComputeRoute.WithHandler(handlers.Add).Register(server)
	if err != nil {
		return nil, err
	}

	broker, router := newMockBroker()
	if err := mqtt5adapter.AttachServer(server, broker, router); err != nil {
		return nil, err
	}
	return &Built{
		Server:        server,
		Broker:        broker,
		Router:        router,
		GlobalHandle:  globalHandle,
		SecuredHandle: securedHandle,
	}, nil
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
