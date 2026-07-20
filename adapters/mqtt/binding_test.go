package mqtt_test

import (
	"context"
	"testing"
	"time"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

func TestSubscribeAdapter_ValidPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newHandle()
	client := &mockClient{}

	p, err := ports.NewSourcePort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptermqtt.SubscribeAdapter(client, handle, 0, format.JSON(userEventCodec), adaptermqtt.SubscribeAdapterOptions{}))
	s := p.Stream(ctx)

	deadline := time.Now().Add(200 * time.Millisecond)
	for client.subscribedHandlerSnapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	handler := client.subscribedHandlerSnapshot()
	if handler == nil {
		t.Fatal("timeout waiting for Subscribe to register a handler")
	}
	handler(client, &mockMessage{payload: []byte(validPayload)})
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
}

// TestSubscribeAdapter_AutoDerivesWildcardFilter verifies that when
// TopicFilter is empty and the handle's topic has {var} placeholders, the
// adapter subscribes using the derived MQTT wildcard filter
// ("users/{userID}/events" -> "users/+/events") instead of the raw,
// brace-containing topic string.
func TestSubscribeAdapter_AutoDerivesWildcardFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle := newTemplateHandle() // topic: "users/{userID}/events"
	client := &mockClient{}

	p, err := ports.NewSourcePort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptermqtt.SubscribeAdapter(client, handle, 0, format.JSON(userEventCodec), adaptermqtt.SubscribeAdapterOptions{}))
	p.Stream(ctx)

	deadline := time.Now().Add(200 * time.Millisecond)
	for client.subscribedTopicSnapshot() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := client.subscribedTopicSnapshot(); got != "users/+/events" {
		t.Errorf("want derived wildcard filter %q, got %q", "users/+/events", got)
	}
}

// ── PublishAdapter ────────────────────────────────────────────────────────────

func TestPublishAdapter_PublishesEachItem(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newHandle()

	ch := make(chan userEvent, 1)
	ch <- userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	close(ch)

	p, err := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptermqtt.PublishAdapter(client, handle, format.JSON(userEventCodec), adaptermqtt.MQTTDrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	topics := client.publishedTopicsSnapshot()
	if len(topics) != 1 || topics[0] != "user/created" {
		t.Errorf("want 1 publish to user/created, got %v", topics)
	}
}

// G1-3 (mqtt v3): PublishAdapter derives topic vars PER-ITEM from each
// item's own merge fields when opts.Vars is nil — two items with different
// user IDs must publish to two different concrete topics.
func TestPublishAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newMergeHandle()

	ch := make(chan userEvent, 2)
	ch <- userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	ch <- userEvent{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "bob@example.com"}
	close(ch)

	p, err := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// opts.Vars left nil -> per-item derivation via PublishHandle.
	p.Bind(ctx, adaptermqtt.PublishAdapter(client, handle, format.JSON(userEventCodec), adaptermqtt.MQTTDrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	topics := client.publishedTopicsSnapshot()
	if len(topics) != 2 {
		t.Fatalf("want 2 published, got %d", len(topics))
	}
	if topics[0] != "users/f47ac10b-58cc-4372-a567-0e02b2c3d479/events" ||
		topics[1] != "users/550e8400-e29b-41d4-a716-446655440000/events" {
		t.Errorf("want per-item resolved topics, got %v", topics)
	}
}

// Explicit (non-nil) MQTTDrainPublishOptions.Vars still wins — regression guard.
func TestPublishAdapter_ExplicitVarsStillWins(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newMergeHandle()

	ch := make(chan userEvent, 2)
	ch <- userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	ch <- userEvent{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "bob@example.com"}
	close(ch)

	p, err := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptermqtt.PublishAdapter(client, handle, format.JSON(userEventCodec),
		adaptermqtt.MQTTDrainPublishOptions{Vars: map[string]string{"userID": "static-user"}}))
	p.Feed(ctx, gstream.From(ctx, ch))

	for _, topic := range client.publishedTopicsSnapshot() {
		if topic != "users/static-user/events" {
			t.Errorf("want static topic for every item, got %q", topic)
		}
	}
}
