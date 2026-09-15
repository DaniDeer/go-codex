// Package mqttbroker assembles routes/+handlers/ onto an in-process mock
// MQTT v3 client via mqtt.Attach — the events analogue of
// examples/reqreply-api's mqtt5server package, for mqtt v3 (Paho).
// Demonstrates: Client.Attach preferred workflow, the handle-based escape
// hatch (OnError, wildcard subscription, multi-format), the domain-
// boundary pipeline (MeasurementEvent → TimeSeriesRecord → AlertEvent),
// and SubscribeMW-based security via handlers.MQTTSecurityImpl.
package mqttbroker

import (
	"context"
	"strings"
	"sync"
	"time"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/examples/events-api/handlers"
	"github.com/DaniDeer/go-codex/examples/events-api/routes"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// Built bundles the assembled events.Client with the mock client the
// caller's demo needs to simulate incoming/outgoing messages (no real
// MQTT v3 broker required).
type Built struct {
	Client     *events.Client
	MQTTClient *MockClient
}

// Build attaches routes.SensorDataSub (secured, via
// handlers.MQTTSecurityImpl closed over a credential), routes.
// MeasurementSub+MeasurementAlertPub (domain-boundary pipeline), and
// routes.WildcardSub (wildcard subscription) onto a fresh in-process mock
// client. credential is the API key this "connection" authenticates with
// (see handlers.MQTTSecurityImpl's own closure-pattern doc comment).
func Build(credential string, store *handlers.TimeSeriesStore, threshold float64) (*Built, error) {
	client := NewMockClient()

	eventsClient := events.NewClient(events.WithInfo(events.Info{
		Title:       "Sensor Network Events (MQTT v3)",
		Version:     "1.0.0",
		Description: "Channels for the sensor data ingestion service over MQTT 3.1.1.",
	}))
	eventsClient.AddServer("production", events.Server{
		URL:         "mqtt.example.com",
		Protocol:    "mqtt",
		Description: "Production MQTT broker",
	})

	if err := adaptermqtt.Attach(eventsClient, client); err != nil {
		return nil, err
	}

	sub := routes.SensorDataSub.
		Use(routes.APIKeyAuthMW).
		SubscribeMW(&routes.APIKeyAuthMW, handlers.MQTTSecurityImpl(credential)).
		WithHandler(handlers.PrintReading("mqtt"))
	if err := sub.Register(eventsClient); err != nil {
		return nil, err
	}

	plainSub := routes.PlainReadingsSub.WithHandler(handlers.PrintReading("mqtt-plain"))
	if err := plainSub.Register(eventsClient); err != nil {
		return nil, err
	}

	measurementSub := routes.MeasurementSub.WithHandler(
		handlers.MeasurementHandler(store, threshold, func(ctx context.Context, a routes.AlertEvent) error {
			return events.PublishHandle(ctx, routes.MeasurementAlertPub, adaptermqtt.NewPublishTransport[routes.AlertEvent](client, 1, false, adaptermqtt.PublishOptions[routes.AlertEvent]{}), a)
		}),
	)
	if err := measurementSub.Register(eventsClient); err != nil {
		return nil, err
	}

	wildcardSub := routes.WildcardSub.WithHandler(handlers.PrintReading("wildcard"))
	if err := wildcardSub.Register(eventsClient); err != nil {
		return nil, err
	}

	return &Built{Client: eventsClient, MQTTClient: client}, nil
}

// ── in-process mock client ────────────────────────────────────────────────────
//
// Mirrors the retired examples/adapters-mqtt's own mock infrastructure —
// pure test/demo scaffolding, not novel business logic.

type mockToken struct{ done chan struct{} }

func newMockToken() *mockToken {
	t := &mockToken{done: make(chan struct{})}
	close(t.done)
	return t
}

func (t *mockToken) Wait() bool                       { return true }
func (t *mockToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *mockToken) Done() <-chan struct{}            { return t.done }
func (t *mockToken) Error() error                     { return nil }

// MockMessage implements [pahomqtt.Message] for testing without a real broker.
type MockMessage struct {
	TopicName string
	Bytes     []byte
}

func (m *MockMessage) Duplicate() bool   { return false }
func (m *MockMessage) Qos() byte         { return 0 }
func (m *MockMessage) Retained() bool    { return false }
func (m *MockMessage) Topic() string     { return m.TopicName }
func (m *MockMessage) MessageID() uint16 { return 0 }
func (m *MockMessage) Payload() []byte   { return m.Bytes }
func (m *MockMessage) Ack()              {}

// MockClient implements [pahomqtt.Client] without a live broker.
// AutoDispatch, when true, makes Publish immediately fan out to every
// matching registered handler — used by Client.Attach-based demos, where
// Publish must actually reach Subscribe's handler with no manual step.
type MockClient struct {
	mu           sync.Mutex
	handlers     map[string]pahomqtt.MessageHandler
	AutoDispatch bool
	Published    []struct {
		Topic   string
		Payload []byte
	}
}

func NewMockClient() *MockClient {
	return &MockClient{handlers: make(map[string]pahomqtt.MessageHandler), AutoDispatch: true}
}

func (c *MockClient) Subscribe(topic string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.handlers[topic] = handler
	c.mu.Unlock()
	return newMockToken()
}

func (c *MockClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	b := payload.([]byte)
	c.mu.Lock()
	c.Published = append(c.Published, struct {
		Topic   string
		Payload []byte
	}{Topic: topic, Payload: b})
	c.mu.Unlock()
	if c.AutoDispatch {
		c.Deliver(topic, b)
	}
	return newMockToken()
}

// Deliver simulates an incoming broker message on topic, dispatching to
// every registered handler whose subscription pattern matches.
func (c *MockClient) Deliver(topic string, payload []byte) {
	c.mu.Lock()
	var matched []pahomqtt.MessageHandler
	for sub, h := range c.handlers {
		if topicMatchesSub(sub, topic) {
			matched = append(matched, h)
		}
	}
	c.mu.Unlock()
	for _, h := range matched {
		h(c, &MockMessage{TopicName: topic, Bytes: payload})
	}
}

// topicMatchesSub reports whether a concrete MQTT topic matches a
// subscription pattern — handles MQTT single-level (+) and multi-level
// (#) wildcards.
func topicMatchesSub(sub, topic string) bool {
	subParts := strings.Split(sub, "/")
	topicParts := strings.Split(topic, "/")
	for i, seg := range subParts {
		if seg == "#" {
			return true
		}
		if i >= len(topicParts) {
			return false
		}
		if seg != "+" && seg != topicParts[i] {
			return false
		}
	}
	return len(subParts) == len(topicParts)
}

// WaitForSubscription polls until filter has been registered by
// [MockClient.Subscribe], or timeout elapses.
func (c *MockClient) WaitForSubscription(filter string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		_, ok := c.handlers[filter]
		c.mu.Unlock()
		if ok {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func (c *MockClient) IsConnected() bool       { return true }
func (c *MockClient) IsConnectionOpen() bool  { return true }
func (c *MockClient) Connect() pahomqtt.Token { return newMockToken() }
func (c *MockClient) Disconnect(_ uint)       {}
func (c *MockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newMockToken()
}
func (c *MockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newMockToken() }
func (c *MockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *MockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}
