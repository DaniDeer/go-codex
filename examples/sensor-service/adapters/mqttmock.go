// Package adapters holds the sensor service's infrastructure edge: the mock
// MQTT client used for the demo, the SQL-backed ReadingStore, and the HTTP
// handler factories. This is the ONLY example package that touches concrete
// clients — domain and pipeline never import it.
package adapters

import (
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// ── Mock MQTT client ──────────────────────────────────────────────────────────

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

type mockMessage struct {
	topic   string
	payload []byte
}

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 0 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return m.topic }
func (m *mockMessage) MessageID() uint16 { return 0 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

// MockMQTTClient is an in-process pahomqtt.Client that routes published
// messages straight to registered subscription handlers — no broker needed.
type MockMQTTClient struct {
	mu       sync.Mutex
	handlers map[string]pahomqtt.MessageHandler
}

// NewMockMQTTClient returns a ready-to-use in-process MQTT client mock.
func NewMockMQTTClient() *MockMQTTClient {
	return &MockMQTTClient{handlers: make(map[string]pahomqtt.MessageHandler)}
}

// Publish implements pahomqtt.Client (no-op success).
func (c *MockMQTTClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return newMockToken()
}

// Subscribe implements pahomqtt.Client, registering h for topic.
func (c *MockMQTTClient) Subscribe(topic string, _ byte, h pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[topic] = h
	return newMockToken()
}

// Unsubscribe implements pahomqtt.Client (no-op success).
func (c *MockMQTTClient) Unsubscribe(_ ...string) pahomqtt.Token { return newMockToken() }

// Deliver simulates a sensor publishing a message on topic.
// It finds the first registered subscription filter that matches the topic,
// supporting the MQTT '+' single-level wildcard.
func (c *MockMQTTClient) Deliver(topic string, payload []byte) {
	c.mu.Lock()
	var h pahomqtt.MessageHandler
	for filter, handler := range c.handlers {
		if mqttMatches(filter, topic) {
			h = handler
			break
		}
	}
	c.mu.Unlock()
	if h != nil {
		h(c, &mockMessage{topic: topic, payload: payload})
	}
}

// mqttMatches reports whether subscription filter matches concrete topic.
// Supports '+' (single-level wildcard) and '#' (multi-level wildcard).
func mqttMatches(filter, topic string) bool {
	if filter == topic {
		return true
	}
	fs := splitTopic(filter)
	ts := splitTopic(topic)
	for i, f := range fs {
		if f == "#" {
			return true
		}
		if i >= len(ts) {
			return false
		}
		if f != "+" && f != ts[i] {
			return false
		}
	}
	return len(fs) == len(ts)
}

func splitTopic(t string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(t); i++ {
		if i == len(t) || t[i] == '/' {
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	return parts
}

// IsConnected implements pahomqtt.Client.
func (c *MockMQTTClient) IsConnected() bool { return true }

// IsConnectionOpen implements pahomqtt.Client.
func (c *MockMQTTClient) IsConnectionOpen() bool { return true }

// Connect implements pahomqtt.Client (no-op success).
func (c *MockMQTTClient) Connect() pahomqtt.Token { return newMockToken() }

// Disconnect implements pahomqtt.Client (no-op).
func (c *MockMQTTClient) Disconnect(_ uint) {}

// AddRoute implements pahomqtt.Client (no-op).
func (c *MockMQTTClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}

// SubscribeMultiple implements pahomqtt.Client (no-op success).
func (c *MockMQTTClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newMockToken()
}

// OptionsReader implements pahomqtt.Client.
func (c *MockMQTTClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}
