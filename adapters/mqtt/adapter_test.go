package mqtt_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test type and codec ---

type userEvent struct {
	ID    string
	Email string
}

var userEventCodec = codex.Struct[userEvent](
	codex.Field[userEvent, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID),
		Get:      func(e userEvent) string { return e.ID },
		Set:      func(e *userEvent, v string) { e.ID = v },
		Required: true,
	},
	codex.Field[userEvent, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email),
		Get:      func(e userEvent) string { return e.Email },
		Set:      func(e *userEvent, v string) { e.Email = v },
		Required: true,
	},
)

func newHandle() *events.ChannelHandle[userEvent] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	return events.AddChannel[userEvent](b, "user/created", userEventCodec,
		events.ChannelConfig{
			Subscribe: &events.OperationConfig{Summary: "User created"},
		})
}

// --- mock implementations ---

// mockMessage implements pahomqtt.Message.
type mockMessage struct{ payload []byte }

func (m *mockMessage) Duplicate() bool   { return false }
func (m *mockMessage) Qos() byte         { return 0 }
func (m *mockMessage) Retained() bool    { return false }
func (m *mockMessage) Topic() string     { return "user/created" }
func (m *mockMessage) MessageID() uint16 { return 0 }
func (m *mockMessage) Payload() []byte   { return m.payload }
func (m *mockMessage) Ack()              {}

// completedToken is a Token that is already done.
type completedToken struct {
	err  error
	done chan struct{}
}

func newCompletedToken(err error) *completedToken {
	t := &completedToken{err: err, done: make(chan struct{})}
	close(t.done)
	return t
}

func (t *completedToken) Wait() bool                       { return true }
func (t *completedToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *completedToken) Done() <-chan struct{}            { return t.done }
func (t *completedToken) Error() error                     { return t.err }

// pendingToken is a Token that never completes (used for ctx-cancel test).
type pendingToken struct{ done chan struct{} }

func newPendingToken() *pendingToken { return &pendingToken{done: make(chan struct{})} }

func (t *pendingToken) Wait() bool                       { <-t.done; return true }
func (t *pendingToken) WaitTimeout(_ time.Duration) bool { return false }
func (t *pendingToken) Done() <-chan struct{}            { return t.done }
func (t *pendingToken) Error() error                     { return nil }

// mockClient implements pahomqtt.Client (only Publish is exercised in tests).
type mockClient struct {
	publishedTopic   string
	publishedPayload []byte
	token            pahomqtt.Token
}

func (c *mockClient) IsConnected() bool       { return true }
func (c *mockClient) IsConnectionOpen() bool  { return true }
func (c *mockClient) Connect() pahomqtt.Token { return newCompletedToken(nil) }
func (c *mockClient) Disconnect(_ uint)       {}
func (c *mockClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	c.publishedTopic = topic
	if b, ok := payload.([]byte); ok {
		c.publishedPayload = b
	}
	return c.token
}
func (c *mockClient) Subscribe(_ string, _ byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newCompletedToken(nil)
}
func (c *mockClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newCompletedToken(nil)
}
func (c *mockClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newCompletedToken(nil) }
func (c *mockClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}

// --- tests ---

const validPayload = `{"id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","email":"alice@example.com"}`

func TestSubscribeHandler_ValidPayload(t *testing.T) {
	handle := newHandle()
	var received userEvent

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error {
			received = e
			return nil
		}, nil)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if received.Email != "alice@example.com" {
		t.Fatalf("want alice@example.com, got %q", received.Email)
	}
}

func TestSubscribeHandler_DecodeError(t *testing.T) {
	handle := newHandle()
	var gotErr adaptermqtt.SubscribeError

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error {
			t.Fatal("fn must not be called on decode error")
			return nil
		},
		func(e adaptermqtt.SubscribeError) { gotErr = e },
	)

	handler(nil, &mockMessage{payload: []byte(`{"id":"bad-uuid","email":"not-email"}`)})

	if gotErr.Err == nil {
		t.Fatal("want error, got nil")
	}
	if gotErr.Kind != adaptermqtt.KindDecode {
		t.Fatalf("want KindDecode, got %v", gotErr.Kind)
	}
	if gotErr.Topic != "user/created" {
		t.Fatalf("want topic user/created, got %q", gotErr.Topic)
	}
}

func TestSubscribeHandler_FnError(t *testing.T) {
	handle := newHandle()
	var gotErr adaptermqtt.SubscribeError
	fnErr := errors.New("downstream failure")

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error { return fnErr },
		func(e adaptermqtt.SubscribeError) { gotErr = e },
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if !errors.Is(gotErr.Err, fnErr) {
		t.Fatalf("want fnErr in gotErr.Err, got %v", gotErr.Err)
	}
	if gotErr.Kind != adaptermqtt.KindHandler {
		t.Fatalf("want KindHandler, got %v", gotErr.Kind)
	}
}

func TestSubscribeHandler_NilOnErrNoPanic(t *testing.T) {
	handle := newHandle()
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error { return errors.New("boom") },
		nil,
	)
	// Must not panic.
	handler(nil, &mockMessage{payload: []byte(validPayload)})
}

func TestPublish_Success(t *testing.T) {
	handle := newHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event)
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if client.publishedTopic != "user/created" {
		t.Fatalf("want topic user/created, got %q", client.publishedTopic)
	}
	if !strings.Contains(string(client.publishedPayload), "alice@example.com") {
		t.Fatalf("want email in payload, got %s", client.publishedPayload)
	}
}

func TestPublish_BrokerError(t *testing.T) {
	handle := newHandle()
	brokerErr := errors.New("broker ack failed")
	client := &mockClient{token: newCompletedToken(brokerErr)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event)
	if !errors.Is(err, brokerErr) {
		t.Fatalf("want brokerErr, got %v", err)
	}
}

func TestPublish_ContextCancelled(t *testing.T) {
	handle := newHandle()
	client := &mockClient{token: newPendingToken()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(ctx, client, handle, 1, false, event)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
