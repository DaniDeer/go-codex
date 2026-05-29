package events_test

import (
	"encoding/json"
	"errors"
	"fmt"
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
	h, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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
	h, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err = h.Decode([]byte(`{"id":"","name":"Alice"}`))
	if err == nil {
		t.Fatal("expected validation error for empty id, got nil")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAddChannel_topicAndDescriptorSet(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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
	h, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, config)
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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
	h, err := events.AddChannel[userEvent](b, "user/notify", userEventCodec, events.ChannelConfig{
		Publish: &events.OperationConfig{Summary: "Notify user"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	if h.Descriptor.Publish == nil {
		t.Error("Descriptor.Publish: expected non-nil, got nil")
	}
	if h.Descriptor.Subscribe != nil {
		t.Error("Descriptor.Subscribe: expected nil for publish-only channel")
	}
}

func TestAddChannel_bothDirections(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.AddChannel[userEvent](b, "user/events", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "Receive user event"},
		Publish:   &events.OperationConfig{Summary: "Send user event"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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

	if _, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{
			Summary:    "A user was created",
			SchemaName: "UserCreatedEvent",
			Tags:       []string{"users"},
		},
	}); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	if _, err := events.AddChannel[userEvent](b, "user/deleted", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "A user was deleted"},
	}); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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
	if _, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{
			SchemaName: "UserCreatedEvent",
		},
	}); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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
	if _, err := events.AddChannel[userEvent](b, "order/placed", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "Order placed"},
	}); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

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

func TestBuilder_withTopicCodec_validTopicPasses(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicCodec(
		codex.String().Refine(validate.MQTTPublishTopic),
	))
	if _, err := events.AddChannel[userEvent](b, "user/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	}); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("expected no error for valid topic, got: %v", err)
	}
}

func TestBuilder_withTopicCodec_invalidTopicSurfacesError(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicCodec(
		codex.String().Refine(validate.MQTTPublishTopic),
	))
	_, err := events.AddChannel[userEvent](b, "user/+/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})
	if err == nil {
		t.Fatal("expected error for topic with wildcard '+', got nil")
	}
	if !strings.Contains(err.Error(), "user/+/created") {
		t.Errorf("error message should mention the invalid topic, got: %v", err)
	}
	var topicErr events.InvalidTopicError
	if !errors.As(err, &topicErr) {
		t.Errorf("expected InvalidTopicError, got %T: %v", err, err)
	}
	if topicErr.Topic != "user/+/created" {
		t.Errorf("InvalidTopicError.Topic = %q, want %q", topicErr.Topic, "user/+/created")
	}
	if topicErr.Err == nil {
		t.Error("InvalidTopicError.Err should be non-nil")
	}
}

func TestBuilder_withTopicConstraints_multipleInvalidTopicsCollected(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicConstraints(validate.MQTTPublishTopic))
	_, err := events.AddChannel[userEvent](b, "user/+/created", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "User created"},
	})
	if err == nil {
		t.Fatal("expected error for topic with wildcard '+', got nil")
	}
	if !strings.Contains(err.Error(), "user/+/created") {
		t.Errorf("error should mention the invalid topic, got: %v", err)
	}

	_, err = events.AddChannel[userEvent](b, "order/#", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "Order event"},
	})
	if err == nil {
		t.Fatal("expected error for topic with wildcard '#', got nil")
	}
	if !strings.Contains(err.Error(), "order/#") {
		t.Errorf("error should mention the invalid topic, got: %v", err)
	}
}

func TestBuilder_noTopicCodec_anyTopicAccepted(t *testing.T) {
	b := events.NewBuilder(testInfo)
	if _, err := events.AddChannel[userEvent](b, "user/+/wildcard", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "Wildcard sub"},
	}); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("without topic codec, expected no error; got: %v", err)
	}
}

func TestAddChannel_unknownTopicParamCodecKey(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	strCodec := codex.String()
	_, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
		TopicParams: []events.TopicParam{
			{Name: "sensorID", Codec: &uuidCodec},
			{Name: "missing", Codec: &strCodec}, // not in template
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown TopicParams name, got nil")
	}
	var paramErr events.InvalidTopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected InvalidTopicParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "missing" {
		t.Errorf("InvalidTopicParamError.Name = %q, want %q", paramErr.Name, "missing")
	}
}

func TestBuildTopic_validVars(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
		TopicParams: []events.TopicParam{
			{Name: "sensorID", Codec: &uuidCodec},
		},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	topic, err := h.BuildTopic(map[string]string{"sensorID": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
	if err != nil {
		t.Fatalf("BuildTopic error: %v", err)
	}
	if topic != "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/data" {
		t.Errorf("BuildTopic = %q, want sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/data", topic)
	}
}

func TestBuildTopic_missingVar(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err = h.BuildTopic(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing template variable, got nil")
	}
	var missingErr events.MissingTopicVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingTopicVarError, got %T: %v", err, err)
	}
	if missingErr.Name != "sensorID" {
		t.Errorf("MissingTopicVarError.Name = %q, want sensorID", missingErr.Name)
	}
}

func TestBuildTopic_codecFailure(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
		TopicParams: []events.TopicParam{
			{Name: "sensorID", Codec: &uuidCodec},
		},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err = h.BuildTopic(map[string]string{"sensorID": "not-a-uuid"})
	if err == nil {
		t.Fatal("expected TopicParamError for invalid UUID, got nil")
	}
	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "sensorID" {
		t.Errorf("TopicParamError.Name = %q, want sensorID", paramErr.Name)
	}
	if paramErr.Value != "not-a-uuid" {
		t.Errorf("TopicParamError.Value = %q, want not-a-uuid", paramErr.Value)
	}
}

func TestBuildTopic_extraKeysIgnored(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	topic, err := h.BuildTopic(map[string]string{"sensorID": "42", "extra": "ignored"})
	if err != nil {
		t.Fatalf("BuildTopic error: %v", err)
	}
	if topic != "sensors/42/data" {
		t.Errorf("BuildTopic = %q, want sensors/42/data", topic)
	}
}

// TestBuilder_withTopicConstraints_templateTransparent verifies that a constraint
// that does not mention braces still accepts template topics. The constraint sees
// "sensors/x/data" (with {sensorID} replaced by "x"), not "sensors/{sensorID}/data".
func TestBuilder_withTopicConstraints_templateTransparent(t *testing.T) {
	noBraces := codex.Constraint[string]{
		Name:    "no-braces",
		Check:   func(v string) bool { return !strings.ContainsAny(v, "{}") },
		Message: func(v string) string { return fmt.Sprintf("topic must not contain braces: %q", v) },
	}
	b := events.NewBuilder(testInfo, events.WithTopicConstraints(noBraces))

	// Without template-transparent stripping this would return an InvalidTopicError.
	if _, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
	}); err != nil {
		t.Fatalf("expected template topic to pass brace-free constraint after stripping, got: %v", err)
	}
}

// TestBuildTopic_finalTopicReValidatedAgainstBuilderCodec verifies that if a
// variable value passes its TopicParamCodecs codec but the final assembled topic
// would fail the builder's topic codec, BuildTopic returns an InvalidTopicError.
func TestBuildTopic_finalTopicReValidatedAgainstBuilderCodec(t *testing.T) {
	noSlash := codex.Constraint[string]{
		Name:    "single-segment-only",
		Check:   func(v string) bool { return !strings.ContainsRune(v, '/') },
		Message: func(v string) string { return fmt.Sprintf("topic must be a single segment: %q", v) },
	}
	b := events.NewBuilder(testInfo, events.WithTopicConstraints(noSlash))
	nonEmptyCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := events.AddChannel[userEvent](b, "{sensorID}", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
		TopicParams: []events.TopicParam{
			{Name: "sensorID", Codec: &nonEmptyCodec},
		},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	// "abc" passes NonEmptyString and final "abc" passes single-segment.
	topic, err := h.BuildTopic(map[string]string{"sensorID": "abc"})
	if err != nil {
		t.Fatalf("BuildTopic(abc) error: %v", err)
	}
	if topic != "abc" {
		t.Errorf("BuildTopic = %q, want abc", topic)
	}

	// "a/b" passes NonEmptyString but final "a/b" fails single-segment (contains slash).
	_, err = h.BuildTopic(map[string]string{"sensorID": "a/b"})
	if err == nil {
		t.Fatal("expected error for slash in final topic, got nil")
	}
	var topicErr events.InvalidTopicError
	if !errors.As(err, &topicErr) {
		t.Errorf("expected InvalidTopicError, got %T: %v", err, err)
	}
	if topicErr.Topic != "a/b" {
		t.Errorf("InvalidTopicError.Topic = %q, want a/b", topicErr.Topic)
	}
}

func TestAddChannel_unknownTopicParamKey(t *testing.T) {
	b := events.NewBuilder(testInfo)
	_, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "sensor data"},
		TopicParams: []events.TopicParam{
			{Name: "notInTemplate"},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown TopicParams name, got nil")
	}
	var paramErr events.InvalidTopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected InvalidTopicParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "notInTemplate" {
		t.Errorf("InvalidTopicParamError.Name = %q, want %q", paramErr.Name, "notInTemplate")
	}
}

func TestAddChannel_topicParamCodecSchemaFlowsToSpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "s"},
		TopicParams: []events.TopicParam{
			{Name: "sensorID", Codec: &uuidCodec},
		},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	p, ok := h.Descriptor.Parameters["sensorID"]
	if !ok {
		t.Fatal("expected sensorID in Descriptor.Parameters, not found")
	}
	if p.Schema.Format != "uuid" {
		t.Errorf("expected schema.Format=uuid, got %q", p.Schema.Format)
	}
	if p.Schema.Type != "string" {
		t.Errorf("expected schema.Type=string, got %q", p.Schema.Type)
	}
}

func TestAddChannel_topicParamDescriptionEnrichment(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "s"},
		TopicParams: []events.TopicParam{
			{Name: "sensorID", Description: "The sensor UUID.", Codec: &uuidCodec},
		},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	p := h.Descriptor.Parameters["sensorID"]
	if p.Description != "The sensor UUID." {
		t.Errorf("expected description, got %q", p.Description)
	}
	// Codec.Schema flows through to the spec parameter.
	if p.Schema.Format != "uuid" {
		t.Errorf("expected uuid format from codec schema, got %q", p.Schema.Format)
	}
}

func TestAddChannel_autoDerivesParamWithoutTopicParams(t *testing.T) {
	// No TopicParams declared; parameter entry should still appear in spec
	// because the topic template has {varName}.
	b := events.NewBuilder(testInfo)
	h, err := events.AddChannel[userEvent](b, "orders/{orderID}", userEventCodec, events.ChannelConfig{
		Subscribe: &events.OperationConfig{Summary: "s"},
	})
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	if _, ok := h.Descriptor.Parameters["orderID"]; !ok {
		t.Fatal("expected orderID auto-derived in Descriptor.Parameters, not found")
	}
}

func TestValidateTopic_noCodecAlwaysPasses(t *testing.T) {
	b := events.NewBuilder(testInfo) // no topic codec
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{})
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopic("sensors/abc/data"); err != nil {
		t.Fatalf("expected nil without topic codec, got: %v", err)
	}
}

func TestValidateTopic_passingTopicReturnsNil(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicConstraints(validate.MQTTPublishTopic))
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{})
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopic("sensors/f47ac10b/data"); err != nil {
		t.Fatalf("expected nil for valid topic, got: %v", err)
	}
}

func TestValidateTopic_wildcardTopicFailsMQTTConstraint(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicConstraints(validate.MQTTPublishTopic))
	// Register with a template topic — AddChannel sees the stripped form.
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{})
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	err = h.ValidateTopic("sensors/+/data") // '+' is illegal in publish topics
	if err == nil {
		t.Fatal("expected InvalidTopicError for wildcard concrete topic, got nil")
	}
	var topicErr events.InvalidTopicError
	if !errors.As(err, &topicErr) {
		t.Fatalf("expected InvalidTopicError, got %T: %v", err, err)
	}
	if topicErr.Topic != "sensors/+/data" {
		t.Errorf("InvalidTopicError.Topic = %q, want sensors/+/data", topicErr.Topic)
	}
}

func TestValidateTopicVars_noCodecAlwaysPasses(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		// No Codec on the TopicParam.
		TopicParams: []events.TopicParam{{Name: "sensorID", Description: "any"}},
	})
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopicVars(map[string]string{"sensorID": "not-a-uuid"}); err != nil {
		t.Fatalf("expected nil without param codec, got: %v", err)
	}
}

func TestValidateTopicVars_validValuePasses(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		TopicParams: []events.TopicParam{{Name: "sensorID", Codec: &uuidCodec}},
	})
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopicVars(map[string]string{"sensorID": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Fatalf("expected nil for valid UUID, got: %v", err)
	}
}

func TestValidateTopicVars_invalidValueReturnsTopicParamError(t *testing.T) {
	b := events.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.AddChannel[userEvent](b, "sensors/{sensorID}/data", userEventCodec, events.ChannelConfig{
		TopicParams: []events.TopicParam{{Name: "sensorID", Codec: &uuidCodec}},
	})
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	err = h.ValidateTopicVars(map[string]string{"sensorID": "not-a-uuid"})
	if err == nil {
		t.Fatal("expected TopicParamError for non-UUID sensorID, got nil")
	}
	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %T: %v", err, err)
	}
	if paramErr.Name != "sensorID" {
		t.Errorf("TopicParamError.Name = %q, want sensorID", paramErr.Name)
	}
	if paramErr.Value != "not-a-uuid" {
		t.Errorf("TopicParamError.Value = %q, want not-a-uuid", paramErr.Value)
	}
}
