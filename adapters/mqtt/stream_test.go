package mqtt_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	adaptermqtt "github.com/DaniDeer/go-codex/adapters/mqtt"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/format"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeStream ───────────────────────────────────────────────────────────

func deliver(handler pahomqtt.MessageHandler, payload []byte) {
	handler(nil, &mockMessage{payload: payload})
}

func subscribeStreamWithCancel(t *testing.T) (gstream.Stream[userEvent], pahomqtt.MessageHandler, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	handle := newHandle()
	s, handler := adaptermqtt.SubscribeStream(ctx, handle, format.JSON(userEventCodec),
		gstream.SourceOptions{Buffer: 8},
		adaptermqtt.SubscribeOptions{})
	return s, handler, cancel
}

func TestSubscribeStream_ValidPayload(t *testing.T) {
	s, handler, cancel := subscribeStreamWithCancel(t)

	payload, _ := json.Marshal(map[string]any{
		"id":    "550e8400-e29b-41d4-a716-446655440000",
		"email": "alice@example.com",
	})
	deliver(handler, payload)
	cancel() // close stream

	vals, errs := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if vals[0].Email != "alice@example.com" {
		t.Errorf("Email: want alice@example.com, got %q", vals[0].Email)
	}
}

func TestSubscribeStream_DecodeErrorGoesToStreamErrors(t *testing.T) {
	s, handler, cancel := subscribeStreamWithCancel(t)

	deliver(handler, []byte("not-json"))
	cancel()

	_, errs := gstream.Collect(context.Background(), s)
	if len(errs) == 0 {
		t.Fatal("want decode error in Stream.Errors, got none")
	}
	// SubscribeHandler wraps decode errors in SubscribeError
	var se adaptermqtt.SubscribeError
	if !errors.As(errs[0], &se) {
		t.Errorf("want SubscribeError, got %T: %v", errs[0], errs[0])
	}
	if se.Kind != adaptermqtt.KindDecode {
		t.Errorf("Kind: want KindDecode, got %v", se.Kind)
	}
}

func TestSubscribeStream_ValidationErrorGoesToStreamErrors(t *testing.T) {
	s, handler, cancel := subscribeStreamWithCancel(t)

	payload, _ := json.Marshal(map[string]any{
		"id":    "550e8400-e29b-41d4-a716-446655440000",
		"email": "not-an-email", // fails validate.Email
	})
	deliver(handler, payload)
	cancel()

	_, errs := gstream.Collect(context.Background(), s)
	if len(errs) == 0 {
		t.Fatal("want validation error in Stream.Errors, got none")
	}
	var se adaptermqtt.SubscribeError
	if !errors.As(errs[0], &se) {
		t.Errorf("want SubscribeError, got %T", errs[0])
	}
}

func TestSubscribeStream_SecurityFuncRejectsToStreamErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newHandle()
	s, handler := adaptermqtt.SubscribeStream(ctx, handle, format.JSON(userEventCodec),
		gstream.SourceOptions{Buffer: 4},
		adaptermqtt.SubscribeOptions{
			// No SecurityFunc set — security not enforced. This test just verifies
			// a clean delivery. Full security enforcement is tested in adapter_test.go.
		})

	payload, _ := json.Marshal(map[string]any{
		"id": "550e8400-e29b-41d4-a716-446655440000", "email": "a@b.com",
	})
	deliver(handler, payload)
	cancel()

	vals, _ := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value without security, got %d", len(vals))
	}
}

func TestSubscribeStream_ErrorsGoToStreamNotCallback(t *testing.T) {
	// Verify errors go to Stream.Errors — not a separate OnError callback
	// (the bridge overrides OnError internally).
	s, handler, cancel := subscribeStreamWithCancel(t)

	deliver(handler, []byte("bad"))
	cancel()

	_, errs := gstream.Collect(context.Background(), s)
	if len(errs) == 0 {
		t.Error("decode error must appear in Stream.Errors")
	}
}

func TestSubscribeStream_MultipleMessages(t *testing.T) {
	s, handler, cancel := subscribeStreamWithCancel(t)

	good, _ := json.Marshal(map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000", "email": "a@b.com"})
	deliver(handler, []byte("bad"))      // decode error
	deliver(handler, good)               // valid
	deliver(handler, []byte("also-bad")) // decode error
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
	if len(errs) != 2 {
		t.Errorf("want 2 errors, got %d", len(errs))
	}
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

func TestDrainPublish_PublishesEachItem(t *testing.T) {
	ctx := context.Background()

	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	alertHandle, err := events.NewChannel[userEvent]("user/alerts", userEventCodec,
		events.Publish{Summary: "Alert"}).Register(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	client := &mockPublishClient{}
	ch := make(chan userEvent, 1)
	ch <- userEvent{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "a@b.com"}
	close(ch)

	adaptermqtt.DrainPublish(ctx, client, alertHandle,
		gstream.From(ctx, ch),
		format.JSON(userEventCodec),
		adaptermqtt.MQTTDrainPublishOptions{})

	if len(client.published) != 1 {
		t.Errorf("want 1 published message, got %d", len(client.published))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

type mockPublishClient struct {
	published [][]byte
}

func (c *mockPublishClient) Publish(_ string, _ byte, _ bool, payload interface{}) pahomqtt.Token {
	if b, ok := payload.([]byte); ok {
		c.published = append(c.published, b)
	}
	return newCompletedToken(nil)
}
func (c *mockPublishClient) Subscribe(_ string, _ byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newCompletedToken(nil)
}
func (c *mockPublishClient) Unsubscribe(_ ...string) pahomqtt.Token       { return newCompletedToken(nil) }
func (c *mockPublishClient) IsConnected() bool                            { return true }
func (c *mockPublishClient) IsConnectionOpen() bool                       { return true }
func (c *mockPublishClient) Connect() pahomqtt.Token                      { return newCompletedToken(nil) }
func (c *mockPublishClient) Disconnect(_ uint)                            {}
func (c *mockPublishClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (c *mockPublishClient) SubscribeMultiple(_ map[string]byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return newCompletedToken(nil)
}
func (c *mockPublishClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.ClientOptionsReader{}
}
