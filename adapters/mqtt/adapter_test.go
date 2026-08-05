package mqtt_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test type and codec ---

type userEvent struct {
	ID    string
	Email string
}

var userEventCodec = codex.Struct[userEvent](
	codex.RequiredField("id", codex.String().Refine(validate.UUID),
		func(e userEvent) string { return e.ID },
		func(e *userEvent, v string) { e.ID = v },
	),
	codex.RequiredField("email", codex.String().Refine(validate.Email),
		func(e userEvent) string { return e.Email },
		func(e *userEvent, v string) { e.Email = v },
	),
)

func newHandle() *events.ChannelHandle[userEvent] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"}).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// --- mock implementations ---

// mockMessage implements pahomqtt.Message.
type mockMessage struct {
	topic   string
	payload []byte
}

func (m *mockMessage) Duplicate() bool { return false }
func (m *mockMessage) Qos() byte       { return 0 }
func (m *mockMessage) Retained() bool  { return false }
func (m *mockMessage) Topic() string {
	if m.topic != "" {
		return m.topic
	}
	return "user/created"
}
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
//
// mu guards every mutable field below — SubscribeAdapter's Activate
// goroutine writes subscribedTopic/subscribedHandler concurrently with the
// test goroutine's polling reads (see subscribedTopicSnapshot/
// subscribedHandlerSnapshot), mirroring adapters/mqtt5's mockClient pattern.
// This was a pre-existing, unrelated data race, found via `go test -race`
// and fixed in a prior round.
type mockClient struct {
	mu                sync.Mutex
	publishedTopic    string
	publishedPayload  []byte
	subscribedTopic   string
	subscribedHandler pahomqtt.MessageHandler
	token             pahomqtt.Token

	// publishedTopics/publishedPayloads accumulate EVERY Publish call (unlike
	// publishedTopic/publishedPayload above, which only ever hold the LAST
	// call) — used by multi-item per-item-vars-derivation tests (G1).
	publishedTopics   []string
	publishedPayloads [][]byte
}

func (c *mockClient) IsConnected() bool       { return true }
func (c *mockClient) IsConnectionOpen() bool  { return true }
func (c *mockClient) Connect() pahomqtt.Token { return newCompletedToken(nil) }
func (c *mockClient) Disconnect(_ uint)       {}
func (c *mockClient) Publish(topic string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	c.mu.Lock()
	c.publishedTopic = topic
	c.publishedTopics = append(c.publishedTopics, topic)
	if b, ok := payload.([]byte); ok {
		c.publishedPayload = b
		c.publishedPayloads = append(c.publishedPayloads, b)
	}
	c.mu.Unlock()
	return c.token
}
func (c *mockClient) Subscribe(topic string, _ byte, handler pahomqtt.MessageHandler) pahomqtt.Token {
	c.mu.Lock()
	c.subscribedTopic = topic
	c.subscribedHandler = handler
	c.mu.Unlock()
	return newCompletedToken(nil)
}

// subscribedTopicSnapshot returns the last subscribed topic under c.mu —
// use this (not the bare field) from a goroutine that races with Subscribe.
func (c *mockClient) subscribedTopicSnapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subscribedTopic
}

// subscribedHandlerSnapshot returns the last registered handler under c.mu —
// use this (not the bare field) from a goroutine that races with Subscribe.
func (c *mockClient) subscribedHandlerSnapshot() pahomqtt.MessageHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subscribedHandler
}

// publishedTopicSnapshot returns the last published topic under c.mu — use
// this (not the bare field) from a goroutine that races with Publish.
func (c *mockClient) publishedTopicSnapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishedTopic
}

// publishedPayloadSnapshot returns the last published payload under c.mu —
// use this (not the bare field) from a goroutine that races with Publish.
func (c *mockClient) publishedPayloadSnapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.publishedPayload
}

// publishedTopicsSnapshot returns a copy of every published topic under
// c.mu — use this (not the bare field) from a goroutine that races with
// Publish.
func (c *mockClient) publishedTopicsSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.publishedTopics))
	copy(out, c.publishedTopics)
	return out
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
	if client.publishedTopicSnapshot() != "user/created" {
		t.Fatalf("want topic user/created, got %q", client.publishedTopicSnapshot())
	}
	if !strings.Contains(string(client.publishedPayloadSnapshot()), "alice@example.com") {
		t.Fatalf("want email in payload, got %s", client.publishedPayloadSnapshot())
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
	h, err := events.NewChannel[userEvent]("users/{userID}/events", userEventCodec,
		events.Publish{Summary: "User event"},
		events.TopicParam{Name: "userID", Codec: &uuidCodec}).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newMergeHandle returns a channel whose userID topic var is merge-capable
// (events.NewTopicParam) — mirrors mqtt5/zeromq's newMergeChannelHandle,
// used for G3 (SubscribeHandler auto-merge, PublishHandle) tests.
func newMergeHandle() *events.ChannelHandle[userEvent] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("users/{userID}/events", userEventCodec,
		events.NewTopicParam("userID", uuidCodec,
			func(e userEvent) string { return e.ID },
			func(e *userEvent, v string) { e.ID = v }),
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// G3-1: SubscribeHandler auto-merges topic vars into the decoded value when
// the channel declares merge fields — no manual TopicVarsFromMessage call
// needed in the handler function. Mirrors mqtt5's equivalent EV5 test.
func TestSubscribeHandler_MergeFields_AutoMergesTopicVars(t *testing.T) {
	handle := newMergeHandle()
	var received userEvent

	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error {
			received = e
			return nil
		}, adaptermqtt.SubscribeOptions{})

	// Payload JSON deliberately carries a DIFFERENT id — merge must
	// OVERWRITE it with the value extracted from the concrete topic.
	handler(nil, &mockMessage{
		topic:   "users/f47ac10b-58cc-4372-a567-0e02b2c3d479/events",
		payload: []byte(`{"id":"00000000-0000-0000-0000-000000000000","email":"alice@example.com"}`),
	})

	if received.ID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("ID: want merged from topic, got %q", received.ID)
	}
	if received.Email != "alice@example.com" {
		t.Errorf("Email: want alice@example.com, got %q", received.Email)
	}
}

// G3-1 (regression guard): a channel WITHOUT merge fields behaves
// identically to before — SubscribeHandler does not attempt any
// topic-var merge.
func TestSubscribeHandler_NoMergeFields_NoTopicVarMergeAttempted(t *testing.T) {
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

// G3-2: mqtt.PublishHandle single-call convenience — one struct in, no
// manual vars map, derives the topic from msg's own merge fields.
func TestPublishHandle_DerivesTopicFromMsg(t *testing.T) {
	handle := newMergeHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.PublishHandle(context.Background(), client, handle, 1, false, event, adaptermqtt.PublishOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantTopic := "users/f47ac10b-58cc-4372-a567-0e02b2c3d479/events"
	if client.publishedTopicSnapshot() != wantTopic {
		t.Errorf("topic: want %q, got %q", wantTopic, client.publishedTopicSnapshot())
	}
}

// PublishHandle with no merge fields declared behaves identically to a bare
// Publish(..., nil, ...) call (regression guard).
func TestPublishHandle_NoMergeFields_MatchesPlainPublish(t *testing.T) {
	handle := newHandle()
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.PublishHandle(context.Background(), client, handle, 1, false, event, adaptermqtt.PublishOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.publishedTopicSnapshot() != "user/created" {
		t.Errorf("topic: want static, got %q", client.publishedTopicSnapshot())
	}
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
	if client.publishedTopicSnapshot() != "users/f47ac10b-58cc-4372-a567-0e02b2c3d479/events" {
		t.Fatalf("want concrete topic, got %q", client.publishedTopicSnapshot())
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

func TestObserver_RecordPublish_buildTopicError(t *testing.T) {
	handle := newTemplateHandle()
	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event,
		map[string]string{"userID": "not-a-uuid"}, adaptermqtt.PublishOptions{Observer: obs})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if len(obs.messages) != 1 {
		t.Fatalf("want 1 RecordPublish call, got %d", len(obs.messages))
	}
	if obs.messages[0].success {
		t.Error("want success=false on BuildTopic error, got true")
	}
	if obs.messages[0].dir != "publish" {
		t.Errorf("want dir=publish, got %q", obs.messages[0].dir)
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

func TestObserver_RecordValidationError_topicParam_publish(t *testing.T) {
	handle := newTemplateHandle()
	obs := &mqttSpyObserver{}
	client := &mockClient{token: newCompletedToken(nil)}

	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event,
		map[string]string{"userID": "not-a-uuid"}, adaptermqtt.PublishOptions{Observer: obs})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if len(obs.valErrors) != 1 {
		t.Fatalf("want 1 RecordValidationError call, got %d", len(obs.valErrors))
	}
	ve := obs.valErrors[0]
	if ve.location != "topic_var" {
		t.Errorf("want location=%q, got %q", "topic_var", ve.location)
	}
	if ve.field != "userID" {
		t.Errorf("want field=%q, got %q", "userID", ve.field)
	}
	if ve.constraintName != "uuid" {
		t.Errorf("want constraintName=%q, got %q", "uuid", ve.constraintName)
	}
}

func TestObserver_RecordValidationError_topicParam_subscribe(t *testing.T) {
	handle := newTemplateHandle()
	obs := &mqttSpyObserver{}

	// Handler simulates what TopicVarsFromMessage returns when the topic param fails.
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error {
			return events.TopicParamError{
				Name:  "userID",
				Value: "not-a-uuid",
				Err:   errors.New("constraint failed (uuid): invalid UUID"),
			}
		},
		adaptermqtt.SubscribeOptions{Observer: obs},
	)
	handler(nil, &mockMessage{
		topic:   "users/not-a-uuid/events",
		payload: []byte(validPayload),
	})
	if len(obs.valErrors) != 1 {
		t.Fatalf("want 1 RecordValidationError call, got %d", len(obs.valErrors))
	}
	ve := obs.valErrors[0]
	if ve.location != "topic_var" {
		t.Errorf("want location=%q, got %q", "topic_var", ve.location)
	}
	if ve.field != "userID" {
		t.Errorf("want field=%q, got %q", "userID", ve.field)
	}
}

func TestObserver_RecordValidationError_topicMismatch_subscribe(t *testing.T) {
	handle := newTemplateHandle()
	obs := &mqttSpyObserver{}

	// Handler simulates what TopicVarsFromMessage returns on structural mismatch.
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error {
			return adaptermqtt.TopicMismatchError{
				Template: "users/{userID}/events",
				Topic:    "wrong/topic",
			}
		},
		adaptermqtt.SubscribeOptions{Observer: obs},
	)
	handler(nil, &mockMessage{
		topic:   "wrong/topic",
		payload: []byte(validPayload),
	})
	if len(obs.valErrors) != 1 {
		t.Fatalf("want 1 RecordValidationError call, got %d", len(obs.valErrors))
	}
	ve := obs.valErrors[0]
	if ve.location != "topic" {
		t.Errorf("want location=%q, got %q", "topic", ve.location)
	}
	if ve.constraintName != "topic-mismatch" {
		t.Errorf("want constraintName=%q, got %q", "topic-mismatch", ve.constraintName)
	}
}

// --- Multi-format tests ---

func TestSubscribeHandler_YAMLFormat(t *testing.T) {
	handle := newHandle()
	var received userEvent
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error {
			received = e
			return nil
		},
		adaptermqtt.SubscribeOptions{},
		format.YAML(userEventCodec),
	)

	// Well-formed YAML payload matching codec constraints.
	yamlPayload := "id: \"f47ac10b-58cc-4372-a567-0e02b2c3d479\"\nemail: alice@example.com\n"
	msg := &mockMessage{topic: "user/created", payload: []byte(yamlPayload)}
	handler(nil, msg)

	if received.ID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("want ID f47ac10b-…, got %q", received.ID)
	}
	if received.Email != "alice@example.com" {
		t.Errorf("want Email alice@example.com, got %q", received.Email)
	}
}

func TestSubscribeHandler_YAMLFormat_DecodeError(t *testing.T) {
	handle := newHandle()
	var subErr adaptermqtt.SubscribeError
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, _ userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{
			OnError: func(e adaptermqtt.SubscribeError) { subErr = e },
		},
		format.YAML(userEventCodec),
	)

	// Invalid YAML — codec validation will fail on id (not a UUID).
	msg := &mockMessage{topic: "user/created", payload: []byte("id: bad\nemail: alice@example.com\n")}
	handler(nil, msg)

	if subErr.Kind != adaptermqtt.KindDecode {
		t.Errorf("want KindDecode, got %v", subErr.Kind)
	}
}

func TestPublish_YAMLFormat(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newHandle()

	event := userEvent{
		ID:    "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Email: "alice@example.com",
	}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil,
		adaptermqtt.PublishOptions{},
		format.YAML(userEventCodec),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(client.publishedPayloadSnapshot()), "alice@example.com") {
		t.Errorf("want YAML payload with alice@example.com, got %q", string(client.publishedPayloadSnapshot()))
	}
	// YAML payload should not be JSON.
	if strings.HasPrefix(string(client.publishedPayloadSnapshot()), "{") {
		t.Errorf("want YAML payload, got JSON-like: %q", string(client.publishedPayloadSnapshot()))
	}
}

func TestPublish_YAMLFormat_EncodeError(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newHandle()

	// Empty email fails the Email constraint.
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: ""}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil,
		adaptermqtt.PublishOptions{},
		format.YAML(userEventCodec),
	)
	if err == nil {
		t.Fatal("expected encode error, got nil")
	}
}

func TestPublish_EncodeError_returnsPublishEncodeError(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newHandle()

	// Empty email fails the Email constraint on default JSON encode.
	event := userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: ""}
	err := adaptermqtt.Publish(context.Background(), client, handle, 1, false, event, nil,
		adaptermqtt.PublishOptions{},
	)
	if err == nil {
		t.Fatal("expected PublishEncodeError, got nil")
	}
	var encErr adaptermqtt.PublishEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("want errors.As(PublishEncodeError), got %T: %v", err, err)
	}
	if encErr.Topic == "" {
		t.Error("want non-empty Topic in PublishEncodeError")
	}
	if encErr.Err == nil {
		t.Error("want non-nil Err in PublishEncodeError")
	}
}

func TestPublishEncodeError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("constraint failed")
	e := adaptermqtt.PublishEncodeError{Topic: "sensors/01", Err: inner}

	if e.Error() != "mqtt encode sensors/01: constraint failed" {
		t.Errorf("unexpected Error() string: %s", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should traverse Unwrap to inner error")
	}
}

// --- Security tests ---

type mockSecurityObserver struct {
	stats.NoopObserver
	location string
	scheme   string
}

func (o *mockSecurityObserver) RecordSecurityRejection(location, scheme string) {
	o.location = location
	o.scheme = scheme
}

func newSecuredHandle() (*events.ChannelHandle[userEvent], error) {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	return events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{
			Summary:  "User created",
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		},
	).Register(b)
}

func TestSubscribeHandler_SecurityFunc_calledForSecuredChannel(t *testing.T) {
	handle, err := newSecuredHandle()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	handlerCalled := false
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error {
			handlerCalled = true
			return nil
		},
		adaptermqtt.SubscribeOptions{
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				secFuncCalled = true
				return nil
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if !secFuncCalled {
		t.Error("want SecurityFunc called for secured channel")
	}
	if !handlerCalled {
		t.Error("want handler called when SecurityFunc returns nil")
	}
}

func TestSubscribeHandler_SecurityFunc_rejectsMessage(t *testing.T) {
	handle, err := newSecuredHandle()
	if err != nil {
		t.Fatal(err)
	}
	var subErr adaptermqtt.SubscribeError
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error {
			t.Fatal("handler must not be called when SecurityFunc rejects")
			return nil
		},
		adaptermqtt.SubscribeOptions{
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				return errors.New("unauthorized")
			},
			OnError: func(e adaptermqtt.SubscribeError) {
				subErr = e
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if subErr.Kind != adaptermqtt.KindSecurity {
		t.Errorf("want KindSecurity, got %v", subErr.Kind)
	}
}

func TestSubscribeHandler_SecurityFunc_notCalledForUnsecuredChannel(t *testing.T) {
	handle := newHandle()
	secFuncCalled := false
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				secFuncCalled = true
				return nil
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if secFuncCalled {
		t.Error("want SecurityFunc NOT called for unsecured channel")
	}
}

func TestSubscribeHandler_SecurityObserver_calledOnRejection(t *testing.T) {
	handle, err := newSecuredHandle()
	if err != nil {
		t.Fatal(err)
	}
	obs := &mockSecurityObserver{}
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{
			Observer: obs,
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				return errors.New("unauthorized")
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if obs.location != "user/created" {
		t.Errorf("want location=user/created, got %q", obs.location)
	}
	if obs.scheme != "bearerAuth" {
		t.Errorf("want scheme=bearerAuth, got %q", obs.scheme)
	}
}

func newGlobalSecuredMQTTHandle() (*events.ChannelHandle[userEvent], error) {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	// No per-operation Security — inherits global.
	return events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"},
		events.WithSecurityScheme("bearerAuth", events.SecurityScheme{
			SecurityScheme: route.BearerScheme("JWT"),
		}),
	).Register(b)
}

func TestSubscribeHandler_GlobalSecurity_enforcedWhenNoPerChannelSecurity(t *testing.T) {
	handle, err := newGlobalSecuredMQTTHandle()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				secFuncCalled = true
				return nil
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if !secFuncCalled {
		t.Error("want SecurityFunc called for channel inheriting global security")
	}
}

func TestSubscribeHandler_GlobalSecurity_rejectsMessage(t *testing.T) {
	handle, err := newGlobalSecuredMQTTHandle()
	if err != nil {
		t.Fatal(err)
	}
	var gotErr adaptermqtt.SubscribeError
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{
			OnError: func(e adaptermqtt.SubscribeError) { gotErr = e },
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				return errors.New("missing api key")
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if gotErr.Kind != adaptermqtt.KindSecurity {
		t.Errorf("want KindSecurity, got %v", gotErr.Kind)
	}
}

func TestSubscribeHandler_GlobalSecurity_notCalledWhenExplicitlyEmpty(t *testing.T) {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	// Explicitly empty Security = no auth on this channel.
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{
			Summary:  "User created",
			Security: []route.SecurityRequirement{},
		},
		events.WithSecurityScheme("bearerAuth", events.SecurityScheme{
			SecurityScheme: route.BearerScheme("JWT"),
		}),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	handler := adaptermqtt.SubscribeHandler(context.Background(), handle,
		func(_ context.Context, e userEvent) error { return nil },
		adaptermqtt.SubscribeOptions{
			SecurityFunc: func(_ context.Context, _ pahomqtt.Message, _ []route.SecurityRequirement) error {
				secFuncCalled = true
				return nil
			},
		},
	)

	handler(nil, &mockMessage{payload: []byte(validPayload)})

	if secFuncCalled {
		t.Error("want SecurityFunc NOT called for channel with explicit empty Security")
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExamplePublish() {
	type Alert struct{ SensorID, Message string }

	alertCodec := codex.Struct[Alert](
		codex.RequiredField("sensor_id", codex.String(),
			func(a Alert) string { return a.SensorID },
			func(a *Alert, v string) { a.SensorID = v },
		),
		codex.RequiredField("message", codex.String(),
			func(a Alert) string { return a.Message },
			func(a *Alert, v string) { a.Message = v },
		),
	)

	b := events.NewBuilder(events.Info{Title: "Alert Service", Version: "1.0.0"})
	alertChannel, _ := events.NewChannel[Alert]("alerts/{sensorID}", alertCodec,
		events.Publish{OperationID: "publishAlert", Summary: "Publish a threshold alert"},
		events.TopicParam{Name: "sensorID"}.WithCodec(
			codex.String().Refine(validate.NonEmptyString),
		),
	).Register(b)

	// Mock client records the published topic and payload.
	client := &mockClient{token: newCompletedToken(nil)}

	err := adaptermqtt.Publish(context.Background(), client, alertChannel, 1, false,
		Alert{SensorID: "s1", Message: "threshold exceeded"},
		map[string]string{"sensorID": "sensor-01"},
		adaptermqtt.PublishOptions{},
	)
	fmt.Println(err)
	fmt.Println(client.publishedTopicSnapshot())
	// Output:
	// <nil>
	// alerts/sensor-01
}
