// Package mqtt5broker assembles routes/+handlers/ onto an in-process mock
// MQTT 5 broker via mqtt5.Attach — the events analogue of
// examples/reqreply-api's mqtt5server package. Demonstrates: User
// Properties + ContentType, Connect-level security, error-path
// ergonomics (events.ErrorChannel).
package mqtt5broker

import (
	"context"
	"sync"
	"time"

	mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// Built bundles the assembled events.Client with the mock broker/router
// the caller's demo needs to simulate incoming/outgoing messages (no real
// MQTT 5 broker required).
type Built struct {
	Client *events.Client
	Broker mqtt5adapter.MQTTClient
	Router *MockRouter
}

// Build attaches routes.SensorDataSub (secured, via
// handlers.MQTT5SecurityImpl) and routes.ReadingsWithErrorsPub (error-path
// ergonomics demo) onto a fresh in-process mock broker.
func Build() (*Built, error) {
	router := NewMockRouter()
	broker := &MockBroker{router: router}

	client := events.NewClient(events.WithInfo(events.Info{
		Title:       "Sensor Network Events (MQTT5)",
		Version:     "1.0.0",
		Description: "Channels for the sensor data ingestion service over MQTT 5.",
	}))
	client.AddServer("production", events.Server{
		URL:         "mqtt5.example.com",
		Protocol:    "mqtt5",
		Description: "Production MQTT 5 broker",
	})

	if err := mqtt5adapter.Attach(client, broker, router); err != nil {
		return nil, err
	}

	sub := routes.SensorDataSub.
		Use(routes.APIKeyAuthMW).
		SubscribeMW(&routes.APIKeyAuthMW, handlers.MQTT5SecurityImpl).
		WithHandler(handlers.PrintReading("mqtt5"))
	if err := sub.Register(client); err != nil {
		return nil, err
	}

	plainSub := routes.PlainReadingsSub.WithHandler(handlers.PrintReading("mqtt5-plain"))
	if err := plainSub.Register(client); err != nil {
		return nil, err
	}

	if _, err := routes.ReadingsWithErrorsPub.Handle(client); err != nil {
		return nil, err
	}

	return &Built{Client: client, Broker: broker, Router: router}, nil
}

// ── in-process mock broker/router ────────────────────────────────────────────
//
// Mirrors the retired examples/adapters-mqtt5's own mock infrastructure —
// pure test/demo scaffolding, not novel business logic.

// MockRouter implements [mqtt5adapter.MQTTRouter] and dispatches published
// messages to topic-matched handlers.
type MockRouter struct {
	mu       sync.RWMutex
	handlers map[string]pahomqtt5.MessageHandler
}

func NewMockRouter() *MockRouter {
	return &MockRouter{handlers: make(map[string]pahomqtt5.MessageHandler)}
}

func (r *MockRouter) RegisterHandler(topic string, h pahomqtt5.MessageHandler) {
	r.mu.Lock()
	r.handlers[topic] = h
	r.mu.Unlock()
}

func (r *MockRouter) UnregisterHandler(topic string) {
	r.mu.Lock()
	delete(r.handlers, topic)
	r.mu.Unlock()
}

// WaitHandler blocks until the handler for topic is registered or 1 second
// passes — Client.Attach's subscription registration runs asynchronously.
func (r *MockRouter) WaitHandler(topic string) {
	for i := 0; i < 200; i++ {
		r.mu.RLock()
		_, ok := r.handlers[topic]
		r.mu.RUnlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Dispatch finds all registered handlers whose topic matches msg.Topic and
// calls them synchronously (caller controls goroutine scheduling).
func (r *MockRouter) Dispatch(msg *pahomqtt5.Publish) {
	r.mu.RLock()
	var matched []pahomqtt5.MessageHandler
	for topic, h := range r.handlers {
		if topic == msg.Topic || TopicMatches(topic, msg.Topic) {
			matched = append(matched, h)
		}
	}
	r.mu.RUnlock()
	for _, h := range matched {
		h(msg)
	}
}

// TopicMatches checks if a registered pattern (MQTT wildcard or template
// topic) matches a concrete topic string. Supports MQTT "+"/"#" wildcards
// and "{varName}" template placeholders (treated as "+").
func TopicMatches(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	return mqttTopicMatch(normaliseTopic(pattern), topic)
}

func normaliseTopic(pattern string) string {
	result := make([]byte, 0, len(pattern))
	i := 0
	for i < len(pattern) {
		if pattern[i] == '{' {
			j := i + 1
			for j < len(pattern) && pattern[j] != '}' {
				j++
			}
			result = append(result, '+')
			i = j + 1
		} else {
			result = append(result, pattern[i])
			i++
		}
	}
	return string(result)
}

func mqttTopicMatch(pattern, topic string) bool {
	return matchParts(splitTopic(pattern), splitTopic(topic))
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

// MockBroker implements [mqtt5adapter.MQTTClient] and routes published
// messages to the shared MockRouter, simulating a broker round-trip.
type MockBroker struct {
	mu        sync.Mutex
	router    *MockRouter
	Published []*pahomqtt5.Publish
}

// NewMockBroker returns a MockBroker wired to router — use when a demo
// needs its OWN broker/router pair independent of Build's full,
// security-attached route set (e.g. a plain, unsecured channel demo).
func NewMockBroker(router *MockRouter) *MockBroker {
	return &MockBroker{router: router}
}

func (b *MockBroker) Publish(_ context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	b.mu.Lock()
	b.Published = append(b.Published, p)
	b.mu.Unlock()
	go b.router.Dispatch(p)
	return &pahomqtt5.PublishResponse{}, nil
}

func (b *MockBroker) Subscribe(_ context.Context, _ *pahomqtt5.Subscribe) (*pahomqtt5.Suback, error) {
	return &pahomqtt5.Suback{}, nil
}

func (b *MockBroker) Unsubscribe(_ context.Context, _ *pahomqtt5.Unsubscribe) (*pahomqtt5.Unsuback, error) {
	return &pahomqtt5.Unsuback{}, nil
}

// PublishedSnapshot returns a copy of every message published so far.
func (b *MockBroker) PublishedSnapshot() []*pahomqtt5.Publish {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*pahomqtt5.Publish, len(b.Published))
	copy(out, b.Published)
	return out
}
