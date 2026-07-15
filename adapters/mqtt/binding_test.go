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
	for client.subscribedHandler == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if client.subscribedHandler == nil {
		t.Fatal("timeout waiting for Subscribe to register a handler")
	}
	client.subscribedHandler(client, &mockMessage{payload: []byte(validPayload)})
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
	for client.subscribedTopic == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if client.subscribedTopic != "users/+/events" {
		t.Errorf("want derived wildcard filter %q, got %q", "users/+/events", client.subscribedTopic)
	}
}
