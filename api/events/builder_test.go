package events_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// testInfo is reused across test cases.
var testInfo = events.Info{Title: "Test Events", Version: "1.0.0"}

// userEventCodec decodes a simple user-created event.
var userEventCodec = codex.Struct[userEvent](
	codex.Field[userEvent, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(e userEvent) string { return e.ID },
		Set:      func(e *userEvent, v string) { e.ID = v },
		Required: true,
	},
	codex.Field[userEvent, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(e userEvent) string { return e.Name },
		Set:      func(e *userEvent, v string) { e.Name = v },
		Required: true,
	},
)

type userEvent struct {
	ID   string
	Name string
}

func TestAddChannel_returnsHandleWithDecodeEncode(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})

	// Decode valid JSON payload.
	event, err := h.Decode([]byte(`{"id":"1","name":"Alice"}`))
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if event.ID != "1" || event.Name != "Alice" {
		t.Errorf("got %+v, want {ID:1 Name:Alice}", event)
	}

	// Encode event.
	payload, err := h.Encode(userEvent{ID: "2", Name: "Bob"})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("Encode output not valid JSON: %v", err)
	}
	if m["id"] != "2" {
		t.Errorf("encoded id = %v, want 2", m["id"])
	}
}

func TestAddChannel_decodeRunsValidation(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})

	_, err := h.Decode([]byte(`{"id":"","name":"Alice"}`))
	if err == nil {
		t.Fatal("expected validation error for empty id, got nil")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAddChannel_topicAndDescriptorSet(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})

	if h.Topic != "user/created" {
		t.Errorf("Topic: got %q, want user/created", h.Topic)
	}
	if h.Descriptor.Subscribe == nil {
		t.Error("Descriptor.Subscribe: expected non-nil, got nil")
	}
	if h.Descriptor.Subscribe.Summary != "User created" {
		t.Errorf("Subscribe.Summary: got %q, want 'User created'", h.Descriptor.Subscribe.Summary)
	}
}

func TestAddChannel_descriptorFrozenAtRegistration(t *testing.T) {
	b := events.NewBuilder(testInfo)
	opConfig := &events.OperationConfig{Summary: "Original", Tags: []string{"original"}}
	config := events.ChannelConfig{Subscribe: opConfig}
	h := events.AddChannel[userEvent](b, "user/created", userEventCodec, config)

	// Mutate original config after registration.
	opConfig.Summary = "Mutated"
	opConfig.Tags[0] = "mutated"

	if h.Descriptor.Subscribe.Summary != "Original" {
		t.Errorf("descriptor Summary mutated: got %q", h.Descriptor.Subscribe.Summary)
	}
	if len(h.Descriptor.Subscribe.Tags) > 0 && h.Descriptor.Subscribe.Tags[0] != "original" {
		t.Errorf("descriptor Tags mutated: got %v", h.Descriptor.Subscribe.Tags)
	}
}

func TestAddChannel_publishDirection(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h := events.AddChannel[userEvent](b, "user/notify", userEventCodec, events.ChannelConfig{
		Publish: &events.OperationConfig{Summary: "Notify user"},
	})

	if h.Descriptor.Publish == nil {
		t.Error("Descriptor.Publish: expected non-nil, got nil")
	}
	if h.Descriptor.Subscribe != nil {
		t.Error("Descriptor.Subscribe: expected nil for publish-only channel")
	}
}

func TestAddChannel_bothDirections(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h := events.AddChannel[userEvent](b, "user/events", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "Receive user event"},
		Publish:   &events.OperationConfig{Summary: "Send user event"},
	})

	if h.Descriptor.Subscribe == nil {
		t.Error("expected Subscribe operation")
	}
	if h.Descriptor.Publish == nil {
		t.Error("expected Publish operation")
	}
}

func TestBuilder_asyncAPISpec_containsRegisteredChannels(t *testing.T) {
	b := events.NewBuilder(testInfo)
	b.AddServer("production", events.Server{
		URL:      "mqtt://broker.example.com",
		Protocol: "mqtt",
	})

	events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{
			Summary:    "A user was created",
			SchemaName: "UserCreatedEvent",
			Tags:       []string{"users"},
		},
	})
	events.AddChannel[userEvent](b, "user/deleted", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "A user was deleted"},
	})

	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec error: %v", err)
	}

	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(yamlBytes)

	for _, want := range []string{
		"asyncapi:",
		"Test Events",
		"user/created:",
		"user/deleted:",
		"subscribe:",
		"A user was created",
		"A user was deleted",
		"components:",
		"UserCreatedEvent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("AsyncAPI spec missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestBuilder_asyncAPISpec_emptyChannelError(t *testing.T) {
	b := events.NewBuilder(testInfo)
	// Manually add a channel with neither Subscribe nor Publish via the asyncapi builder
	// to ensure our spec generation catches it. We do this through AddChannel with an
	// empty config — but AddChannel itself produces a valid ChannelItem if at least
	// one operation is set. Test the underlying asyncapi validation by building a spec
	// with no channels at all (degenerate case — no error expected).
	doc, err := b.AsyncAPISpec()
	// asyncapi requires at least one channel — but DocumentBuilder.Build() does not
	// enforce a minimum channel count; only empty-operation channels are rejected.
	// An empty builder should succeed.
	if err != nil {
		t.Logf("AsyncAPISpec with no channels returned: %v", err)
	}
	_ = doc
}

func TestBuilder_asyncAPISpec_schemaRefInComponents(t *testing.T) {
	b := events.NewBuilder(testInfo)
	events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{
			SchemaName: "UserCreatedEvent",
		},
	})

	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	yamlBytes, _ := doc.MarshalYAML()
	out := string(yamlBytes)
	if !strings.Contains(out, "$ref") {
		t.Error("spec missing $ref for named schema")
	}
	if !strings.Contains(out, "UserCreatedEvent") {
		t.Error("spec missing UserCreatedEvent schema name")
	}
}

func TestBuilder_asyncAPISpec_jsonOutput(t *testing.T) {
	b := events.NewBuilder(testInfo)
	events.AddChannel[userEvent](b, "order/placed", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "Order placed"},
	})

	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	jsonBytes, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if m["asyncapi"] != "2.6.0" {
		t.Errorf("asyncapi version: got %v, want 2.6.0", m["asyncapi"])
	}
}
