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
	h, err := events.AddChannel[userEvent](b, "user/created", userEventCodec,
		events.ChannelConfig{
			Subscribe: &events.OperationConfig{Summary: "User created"},
		})
	if err != nil {
		panic(err)
	}
	return h
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
		}, adaptermqtt.SubscribeOptions{})

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
		adaptermqtt.SubscribeOptions{OnError: func(e adaptermqtt.SubscribeError) { gotErr = e }},
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
		adaptermqtt.SubscribeOptions{OnError: func(e adaptermqtt.SubscribeError) { gotErr = e }},
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
		adaptermqtt.SubscribeOptions{},
	)
	// Must not panic.
	handler(nil, &mockMessage{payload: []byte(validPayload)})
}

func TestPublish_Success(t *testing.T) {
	handle := newHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil, adaptermqtt.PublishOptions{})
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
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil, adaptermqtt.PublishOptions{})
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
	err := adaptermqtt.Publish(ctx, client, handle, 1, false, event, nil, adaptermqtt.PublishOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func newTemplateHandle() *events.ChannelHandle[userEvent] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "users/{userID}/events", userEventCodec,
		events.ChannelConfig{
			Publish: &events.OperationConfig{Summary: "User event"},
			TopicParams: []events.TopicParam{
				{Name: "userID", Codec: &uuidCodec},
			},
		})
	if err != nil {
		panic(err)
	}
	return h
}

func TestPublish_TemplateVars(t *testing.T) {
	handle := newTemplateHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event,
		map[string]string{"userID": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}, adaptermqtt.PublishOptions{})
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if client.publishedTopic != "users/f47ac10b-58cc-4372-a567-0e02b2c3d479/events" {
		t.Fatalf("want concrete topic, got %q", client.publishedTopic)
	}
}

func TestPublish_TemplateVars_InvalidUUID(t *testing.T) {
	handle := newTemplateHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event,
		map[string]string{"userID": "not-a-uuid"}, adaptermqtt.PublishOptions{})
	if err == nil {
		t.Fatal("want error for invalid UUID, got nil")
	}
	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "userID" {
		t.Errorf("TopicParamError.Name = %q, want userID", paramErr.Name)
	}
}

func TestMessageFromContext_InsideHandler(t *testing.T) {
	handle := newHandle()
	msg := &mockMessage{payload: []byte(validPayload)}
	var gotMsg pahomqtt.Message
	var gotOK bool

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(ctx context.Context, _ userEvent) error {
			gotMsg, gotOK = adaptermqtt.MessageFromContext(ctx)
			return nil
		}, adaptermqtt.SubscribeOptions{})

	handler(nil, msg)

	if !gotOK {
		t.Fatal("MessageFromContext: want ok=true inside handler, got false")
	}
	if gotMsg != msg {
		t.Fatal("MessageFromContext: returned message is not the original message")
	}
}

func TestMessageFromContext_OutsideHandler(t *testing.T) {
	msg, ok := adaptermqtt.MessageFromContext(context.Background())
	if ok {
		t.Fatalf("MessageFromContext: want ok=false on plain context, got true with %v", msg)
	}
}

// ── Observer tests ─────────────────────────────────────────────────────────────

type mqttSpyObserver struct {
	messages  []mqttSpyMessage
	valErrors []mqttSpyValError
}

type mqttSpyMessage struct {
	topic   string
	success bool
	dir     string // "subscribe" or "publish"
}

type mqttSpyValError struct {
	location       string
	constraintName string
	field          string
}

func (s *mqttSpyObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {}

func (s *mqttSpyObserver) RecordSubscribe(topic string, success bool, _ time.Duration) {
	s.messages = append(s.messages, mqttSpyMessage{topic: topic, success: success, dir: "subscribe"})
}

func (s *mqttSpyObserver) RecordPublish(topic string, success bool, _ time.Duration) {
	s.messages = append(s.messages, mqttSpyMessage{topic: topic, success: success, dir: "publish"})
}

func (s *mqttSpyObserver) RecordValidationError(location, constraintName, field string) {
	s.valErrors = append(s.valErrors, mqttSpyValError{location: location, constraintName: constraintName, field: field})
}

func TestObserver_RecordSubscribe_success(t *testing.T) {
	handle := newHandle()
	obs := &mqttSpyObserver{}

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{Observer: obs},
	)
	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if len(obs.messages) != 1 {
		t.Fatalf("want 1 RecordSubscribe call, got %d", len(obs.messages))
	}
	if !obs.messages[0].success {
		t.Error("want success=true on successful message, got false")
	}
	if obs.messages[0].dir != "subscribe" {
		t.Errorf("want dir=subscribe, got %q", obs.messages[0].dir)
	}
	if obs.messages[0].topic != "user/created" {
		t.Errorf("want topic user/created, got %q", obs.messages[0].topic)
	}
}

func TestObserver_RecordSubscribe_decodeError(t *testing.T) {
	handle := newHandle()
	obs := &mqttSpyObserver{}

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{Observer: obs},
	)
	handler(nil, &mockMessage{payload: []byte(`{"id":"bad","email":"not-email"}`)})

	if len(obs.messages) != 1 {
		t.Fatalf("want 1 RecordSubscribe call, got %d", len(obs.messages))
	}
	if obs.messages[0].success {
		t.Error("want success=false on decode error, got true")
	}
	if obs.messages[0].dir != "subscribe" {
		t.Errorf("want dir=subscribe, got %q", obs.messages[0].dir)
	}
}

func TestObserver_RecordValidationError_payload(t *testing.T) {
	handle := newHandle()
	obs := &mqttSpyObserver{}

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{Observer: obs},
	)
	handler(nil, &mockMessage{payload: []byte(`{"id":"bad-uuid","email":"bad-email"}`)})

	if len(obs.valErrors) == 0 {
		t.Fatal("want at least one RecordValidationError call, got none")
	}
	for _, ve := range obs.valErrors {
		if ve.location != "payload" {
			t.Errorf("want location 'payload', got %q", ve.location)
		}
	}
}

func TestObserver_RecordPublish_success(t *testing.T) {
	handle := newHandle()
	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil,
		adaptermqtt.PublishOptions{Observer: obs})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(obs.messages) != 1 {
		t.Fatalf("want 1 RecordPublish call, got %d", len(obs.messages))
	}
	if !obs.messages[0].success {
		t.Error("want success=true on successful publish, got false")
	}
	if obs.messages[0].dir != "publish" {
		t.Errorf("want dir=publish, got %q", obs.messages[0].dir)
	}
	if obs.messages[0].topic != "user/created" {
		t.Errorf("want topic user/created, got %q", obs.messages[0].topic)
	}
}

func TestObserver_RecordPublish_brokerError(t *testing.T) {
	handle := newHandle()
	obs := &mqttSpyObserver{}
	brokerErr := errors.New("broker unavailable")
	client := &mockClient{token: newCompletedToken(brokerErr)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil,
		adaptermqtt.PublishOptions{Observer: obs})
	if !errors.Is(err, brokerErr) {
		t.Fatalf("want brokerErr, got %v", err)
	}
	if len(obs.messages) != 1 {
		t.Fatalf("want 1 RecordPublish call, got %d", len(obs.messages))
	}
	if obs.messages[0].success {
		t.Error("want success=false on broker error, got true")
	}
	if obs.messages[0].dir != "publish" {
		t.Errorf("want dir=publish, got %q", obs.messages[0].dir)
	}
}
