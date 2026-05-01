// Package adapters-mqtt demonstrates wiring the api/events builder to Paho MQTT
// using the adapters/mqtt adapter.
//
// This example shows the wiring pattern — no live broker is required to run it.
// In a real application, replace the mockClient with a live paho mqtt.Client.
//
// 1. Define codecs and build channels with api/events (transport-agnostic).
// 2. Wrap each ChannelHandle with adapters/mqtt.SubscribeHandler or Publish.
// 3. Pass the MessageHandler to client.Subscribe, or call Publish directly.
// 4. Generate the AsyncAPI 2.6 spec from the same builder.
//
// Run with: go run ./examples/adapters-mqtt
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- Domain types ---

// UserCreatedEvent is received when a new user registers.
type UserCreatedEvent struct {
	ID    string
	Email string
}

// NotificationCommand is sent to trigger a notification delivery.
type NotificationCommand struct {
	Recipient string
	Subject   string
	Body      string
}

// --- Codecs ---

var userCreatedCodec = codex.Struct[UserCreatedEvent](
	codex.Field[UserCreatedEvent, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID).WithDescription("User UUID."),
		Get:      func(e UserCreatedEvent) string { return e.ID },
		Set:      func(e *UserCreatedEvent, v string) { e.ID = v },
		Required: true,
	},
	codex.Field[UserCreatedEvent, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email).WithDescription("User email."),
		Get:      func(e UserCreatedEvent) string { return e.Email },
		Set:      func(e *UserCreatedEvent, v string) { e.Email = v },
		Required: true,
	},
)

var notificationCommandCodec = codex.Struct[NotificationCommand](
	codex.Field[NotificationCommand, string]{
		Name:     "recipient",
		Codec:    codex.String().Refine(validate.Email).WithDescription("Recipient email."),
		Get:      func(c NotificationCommand) string { return c.Recipient },
		Set:      func(c *NotificationCommand, v string) { c.Recipient = v },
		Required: true,
	},
	codex.Field[NotificationCommand, string]{
		Name:     "subject",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Subject line."),
		Get:      func(c NotificationCommand) string { return c.Subject },
		Set:      func(c *NotificationCommand, v string) { c.Subject = v },
		Required: true,
	},
	codex.Field[NotificationCommand, string]{
		Name:     "body",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Message body."),
		Get:      func(c NotificationCommand) string { return c.Body },
		Set:      func(c *NotificationCommand, v string) { c.Body = v },
		Required: true,
	},
)

// --- mockClient for demo (replace with a real paho client in production) ---

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

type mockMessage struct{ payload []byte }

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 0 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return "user/created" }
func (m *mockMessage) MessageID() uint16 { return 0 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

type mockClient struct {
	mu       sync.Mutex
	handlers map[string]pahomqtt.MessageHandler
}

func newMockClient() *mockClient {
	return &mockClient{handlers: make(map[string]pahomqtt.MessageHandler)}
}

func (c *mockClient) Subscribe(topic string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.handlers[topic] = handler
	c.mu.Unlock()
	return newMockToken()
}

func (c *mockClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	fmt.Printf("[broker] Published to %s: %s\n", topic, payload.([]byte))
	return newMockToken()
}

func (c *mockClient) deliver(topic string, payload []byte) {
	c.mu.Lock()
	h := c.handlers[topic]
	c.mu.Unlock()
	if h != nil {
		h(c, &mockMessage{payload: payload})
	}
}

// Stub methods to satisfy pahomqtt.Client interface.
func (c *mockClient) IsConnected() bool       { return true }
func (c *mockClient) IsConnectionOpen() bool  { return true }
func (c *mockClient) Connect() pahomqtt.Token { return newMockToken() }
func (c *mockClient) Disconnect(_ uint)       {}
func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newMockToken()
}
func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newMockToken() }
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

func main() {
	ctx := context.Background()

	// Step 1: build the event API (transport-agnostic).
	b := events.NewBuilder(events.Info{
		Title:       "Notification Service",
		Version:     "1.0.0",
		Description: "Subscribe to user events, publish notification commands.",
	})
	b.AddServer("production", events.Server{
		URL:      "mqtt://broker.example.com:1883",
		Protocol: "mqtt",
	})

	userCreated := events.AddChannel[UserCreatedEvent](b, "user/created", userCreatedCodec,
		events.ChannelConfig{
			Description: "User registration events consumed by the notification service.",
			Subscribe: &events.OperationConfig{
				Summary:    "Receive user created event",
				SchemaName: "UserCreatedEvent",
			},
		})

	notifSend := events.AddChannel[NotificationCommand](b, "notification/send", notificationCommandCodec,
		events.ChannelConfig{
			Description: "Notification commands produced by this service.",
			Publish: &events.OperationConfig{
				Summary:    "Send notification command",
				SchemaName: "NotificationCommand",
			},
		})

	// Step 2: wire to Paho MQTT via the adapter.
	client := newMockClient()

	// Subscribe: wrap the channel handle into a Paho MessageHandler.
	client.Subscribe(userCreated.Topic, 1,
		adaptermqtt.SubscribeHandler(ctx, userCreated,
			func(ctx context.Context, e UserCreatedEvent) error {
				fmt.Printf("[handler] UserCreated: id=%s email=%s\n", e.ID, e.Email)

				// Publish a notification command in response.
				cmd := NotificationCommand{
					Recipient: e.Email,
					Subject:   "Welcome!",
					Body:      "Your account is ready.",
				}
				return adaptermqtt.Publish(ctx, client, notifSend, 1, false, cmd)
			},
			func(err error) { fmt.Fprintf(os.Stderr, "[error] %v\n", err) },
		),
	)

	// Step 3: simulate the broker delivering a message.
	fmt.Println("=== Simulating incoming user/created message ===")
	fmt.Println()
	client.deliver("user/created",
		[]byte(`{"id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","email":"alice@example.com"}`))
	fmt.Println()

	// Step 4: generate AsyncAPI 2.6 spec from the same builder.
	fmt.Println("=== AsyncAPI 2.6 spec ===")
	fmt.Println()
	doc, err := b.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yaml, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yaml))
}
