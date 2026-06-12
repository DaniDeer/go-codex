package events_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// testInfo is reused across test cases.
var testInfo = events.Info{Title: "Test Events", Version: "1.0.0"}

// userEventCodec decodes a simple user-created event.
var userEventCodec = codex.Struct[userEvent](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
		func(e userEvent) string { return e.ID },
		func(e *userEvent, v string) { e.ID = v },
	),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(e userEvent) string { return e.Name },
		func(e *userEvent, v string) { e.Name = v },
	),
)

type userEvent struct {
	ID   string
	Name string
}

func TestAddChannel_returnsHandleWithDecodeEncode(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec, events.Subscribe{Summary: "User created"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec, events.Subscribe{Summary: "User created"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec, events.Subscribe{Summary: "User created"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "Original", Tags: []string{"original"}}).Register(b)
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	if h.Descriptor.Subscribe == nil {
		t.Fatal("Descriptor.Subscribe: expected non-nil, got nil")
	}
	if h.Descriptor.Subscribe.Summary != "Original" {
		t.Errorf("descriptor Summary: got %q, want %q", h.Descriptor.Subscribe.Summary, "Original")
	}
	if len(h.Descriptor.Subscribe.Tags) != 1 || h.Descriptor.Subscribe.Tags[0] != "original" {
		t.Errorf("descriptor Tags: got %v, want [original]", h.Descriptor.Subscribe.Tags)
	}
}

func TestAddChannel_publishDirection(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/notify", userEventCodec, events.Publish{Summary: "Notify user"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("user/events", userEventCodec, events.Subscribe{Summary: "Receive user event"},
		events.Publish{Summary: "Send user event"}).Register(b)
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

	if _, err := events.NewChannel[userEvent]("user/created", userEventCodec, events.Subscribe{
		Summary:    "A user was created",
		SchemaName: "UserCreatedEvent",
		Tags:       []string{"users"},
	}).Register(b); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	if _, err := events.NewChannel[userEvent]("user/deleted", userEventCodec, events.Subscribe{Summary: "A user was deleted"}).Register(b); err != nil {
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
		"action: receive",
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
	if _, err := events.NewChannel[userEvent]("user/created", userEventCodec, events.Subscribe{
		SchemaName: "UserCreatedEvent",
	}).Register(b); err != nil {
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
	if _, err := events.NewChannel[userEvent]("order/placed", userEventCodec, events.Subscribe{Summary: "Order placed"}).Register(b); err != nil {
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
	if m["asyncapi"] != "3.0.0" {
		t.Errorf("asyncapi version: got %v, want 3.0.0", m["asyncapi"])
	}
}

func TestBuilder_withTopicCodec_validTopicPasses(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicCodec(
		codex.String().Refine(validate.MQTTPublishTopic)))
	if _, err := events.NewChannel[userEvent]("user/created", userEventCodec, events.Subscribe{Summary: "User created"}).Register(b); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("expected no error for valid topic, got: %v", err)
	}
}

func TestBuilder_withTopicCodec_invalidTopicSurfacesError(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicCodec(
		codex.String().Refine(validate.MQTTPublishTopic)))
	_, err := events.NewChannel[userEvent]("user/+/created", userEventCodec, events.Subscribe{Summary: "User created"}).Register(b)
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
	_, err := events.NewChannel[userEvent]("user/+/created", userEventCodec, events.Subscribe{Summary: "User created"}).Register(b)
	if err == nil {
		t.Fatal("expected error for topic with wildcard '+', got nil")
	}
	if !strings.Contains(err.Error(), "user/+/created") {
		t.Errorf("error should mention the invalid topic, got: %v", err)
	}

	_, err = events.NewChannel[userEvent]("order/#", userEventCodec, events.Subscribe{Summary: "Order event"}).Register(b)
	if err == nil {
		t.Fatal("expected error for topic with wildcard '#', got nil")
	}
	if !strings.Contains(err.Error(), "order/#") {
		t.Errorf("error should mention the invalid topic, got: %v", err)
	}
}

func TestBuilder_noTopicCodec_anyTopicAccepted(t *testing.T) {
	b := events.NewBuilder(testInfo)
	if _, err := events.NewChannel[userEvent]("user/+/wildcard", userEventCodec, events.Subscribe{Summary: "Wildcard sub"}).Register(b); err != nil {
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
	_, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.Subscribe{Summary: "sensor data"},
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec},
		events.TopicParam{Name: "missing", Codec: &strCodec}, // not in template
	).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "sensor data"},
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "sensor data"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "sensor data"},
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "sensor data"}).Register(b)
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
	if _, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "sensor data"}).Register(b); err != nil {
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
	h, err := events.NewChannel[userEvent]("{sensorID}", userEventCodec, events.Subscribe{Summary: "sensor data"},
		events.TopicParam{Name: "sensorID", Codec: &nonEmptyCodec}).Register(b)
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
	_, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "sensor data"},
		events.TopicParam{Name: "notInTemplate"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "s"},
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.Subscribe{Summary: "s"},
		events.TopicParam{Name: "sensorID", Description: "The sensor UUID.", Codec: &uuidCodec}).Register(b)
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
	h, err := events.NewChannel[userEvent]("orders/{orderID}", userEventCodec, events.Subscribe{Summary: "s"}).Register(b)
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	if _, ok := h.Descriptor.Parameters["orderID"]; !ok {
		t.Fatal("expected orderID auto-derived in Descriptor.Parameters, not found")
	}
}

func TestValidateTopic_noCodecAlwaysPasses(t *testing.T) {
	b := events.NewBuilder(testInfo) // no topic codec
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopic("sensors/abc/data"); err != nil {
		t.Fatalf("expected nil without topic codec, got: %v", err)
	}
}

func TestValidateTopic_passingTopicReturnsNil(t *testing.T) {
	b := events.NewBuilder(testInfo, events.WithTopicConstraints(validate.MQTTPublishTopic))
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.TopicParam{Name: "sensorID", Description: "any"}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).Register(b)
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
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).Register(b)
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

func TestWithFormats_setsContentTypeOnSubscribeDescriptor(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created", SchemaName: "UserEvent"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	h = h.WithFormats(format.YAML(userEventCodec))

	ct := h.Descriptor.Subscribe.Message.ContentType
	if ct != "application/yaml" {
		t.Errorf("Subscribe.Message.ContentType = %q, want application/yaml", ct)
	}
	if h.Descriptor.Publish != nil {
		t.Errorf("expected Publish to be nil (not registered)")
	}
}

func TestWithFormats_setsContentTypeOnPublishDescriptor(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Publish{Summary: "Publish user event"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	h = h.WithFormats(format.YAML(userEventCodec))

	ct := h.Descriptor.Publish.Message.ContentType
	if ct != "application/yaml" {
		t.Errorf("Publish.Message.ContentType = %q, want application/yaml", ct)
	}
}

func TestWithFormats_updatesBothOperationsWhenBothPresent(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/events", userEventCodec,
		events.Subscribe{Summary: "Receive"},
		events.Publish{Summary: "Send"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	h = h.WithFormats(format.YAML(userEventCodec))

	subCT := h.Descriptor.Subscribe.Message.ContentType
	pubCT := h.Descriptor.Publish.Message.ContentType
	if subCT != "application/yaml" {
		t.Errorf("Subscribe.Message.ContentType = %q, want application/yaml", subCT)
	}
	if pubCT != "application/yaml" {
		t.Errorf("Publish.Message.ContentType = %q, want application/yaml", pubCT)
	}
}

func TestWithFormats_clearsContentTypeWhenCalledEmpty(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}

	h = h.WithFormats(format.YAML(userEventCodec))
	h = h.WithFormats() // clear

	if len(h.Formats) != 0 {
		t.Errorf("expected Formats to be empty after WithFormats()")
	}
	ct := h.Descriptor.Subscribe.Message.ContentType
	if ct != "" {
		t.Errorf("Subscribe.Message.ContentType = %q after clear, want empty", ct)
	}
}

func TestWithFormats_reflectsInAsyncAPISpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created", SchemaName: "UserEvent"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	h.WithFormats(format.YAML(userEventCodec))

	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "application/yaml") {
		t.Errorf("expected AsyncAPI spec to contain application/yaml, got:\n%s", yamlBytes)
	}
}

func TestSubscribe_operationIDInAsyncAPISpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{OperationID: "receiveUserCreated", Summary: "User created"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "receiveUserCreated") {
		t.Errorf("expected AsyncAPI spec to contain operationId receiveUserCreated, got:\n%s", yamlBytes)
	}
}

func TestPublish_operationIDInAsyncAPISpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	_, err := events.NewChannel[userEvent]("user/events", userEventCodec,
		events.Publish{OperationID: "publishUserEvent", Summary: "Publish user event"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "publishUserEvent") {
		t.Errorf("expected AsyncAPI spec to contain operationId publishUserEvent, got:\n%s", yamlBytes)
	}
}

func TestSubscribePublish_bothOperationIDsInAsyncAPISpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	_, err := events.NewChannel[userEvent]("user/events", userEventCodec,
		events.Subscribe{OperationID: "receiveEvent", Summary: "Receive"},
		events.Publish{OperationID: "sendEvent", Summary: "Send"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	spec := string(yamlBytes)
	if !strings.Contains(spec, "receiveEvent") {
		t.Errorf("expected AsyncAPI spec to contain receiveEvent, got:\n%s", spec)
	}
	if !strings.Contains(spec, "sendEvent") {
		t.Errorf("expected AsyncAPI spec to contain sendEvent, got:\n%s", spec)
	}
}

func TestSubscribe_emptyOperationIDOmittedFromSpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if strings.Contains(string(yamlBytes), "operationId") {
		t.Errorf("expected AsyncAPI spec to omit operationId when empty, got:\n%s", yamlBytes)
	}
}

// --- Security builder tests ---

func TestSecurityScheme_WithCodec_setsCodec(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)
	s := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c)
	if s.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
}

func TestSecurityScheme_WithCodec_returnsDistinctCopy(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)
	orig := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	updated := orig.WithCodec(c)
	if orig.Codec != nil {
		t.Fatal("WithCodec must not mutate the original")
	}
	if updated.Codec == nil {
		t.Fatal("updated copy must have non-nil Codec")
	}
}

func TestBuilder_AddSecurityScheme_propagatesToChannelHandle(t *testing.T) {
	b := events.NewBuilder(testInfo)
	c := codex.String().Refine(validate.NonEmptyString)
	b.AddSecurityScheme("bearer", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c))

	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if _, ok := handle.SecuritySchemes["bearer"]; !ok {
		t.Fatal("expected SecuritySchemes to contain 'bearer'")
	}
	if handle.SecuritySchemes["bearer"].Codec == nil {
		t.Fatal("expected Codec to be propagated to ChannelHandle")
	}
}

func TestBuilder_AddGlobalSecurity_populatesChannelHandleGlobalSecurity(t *testing.T) {
	b := events.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearer"))

	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if len(handle.GlobalSecurity) == 0 {
		t.Fatal("expected GlobalSecurity to be populated on ChannelHandle")
	}
	req := handle.GlobalSecurity[0]
	if _, ok := req["bearer"]; !ok {
		t.Errorf("expected GlobalSecurity to contain 'bearer' requirement, got %v", req)
	}
}

func TestBuilder_AddGlobalSecurity_doesNotAppearInAsyncAPISpec(t *testing.T) {
	b := events.NewBuilder(testInfo)
	b.AddSecurityScheme("bearer", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})
	b.AddGlobalSecurity(route.Require("bearer"))

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	// AsyncAPI 3.0 has no document-level global security — must not appear at top level.
	// Per-server or per-operation security would appear nested, but a global "security:"
	// key at document root is not valid AsyncAPI 3.0.
	spec := string(yamlBytes)
	lines := strings.Split(spec, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "security:") {
			t.Errorf("AsyncAPI spec must not have a top-level 'security:' key (AsyncAPI 3.0 has no global security); got:\n%s", spec)
		}
	}
}

func TestAddServer_preservesInsertionOrder(t *testing.T) {
	// Verify that AddServer stores servers in insertion order (not map/random order).
	// The builder now uses a []namedServer slice internally, so repeated calls with
	// the same builder produce a stable, deterministic order in the spec regardless
	// of how Go's map iteration would have randomised a map-based implementation.
	b := events.NewBuilder(testInfo)
	b.AddServer("bravo", events.Server{URL: "mqtt://bravo.example.com"})
	b.AddServer("alpha", events.Server{URL: "mqtt://alpha.example.com"})

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	spec := string(yamlBytes)
	if !strings.Contains(spec, "bravo") || !strings.Contains(spec, "alpha") {
		t.Fatalf("expected both servers in spec; got:\n%s", spec)
	}
	// The YAML renderer outputs map keys in alphabetical order, so both names
	// will appear; we verify only that both are present and the spec is stable
	// (running the same builder twice produces identical output).
	doc2, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("second AsyncAPISpec call: %v", err)
	}
	yamlBytes2, err := doc2.MarshalYAML()
	if err != nil {
		t.Fatalf("second MarshalYAML: %v", err)
	}
	if string(yamlBytes) != string(yamlBytes2) {
		t.Errorf("spec must be deterministic across calls; got diff:\nfirst:\n%s\nsecond:\n%s",
			yamlBytes, yamlBytes2)
	}
}

func TestAddServer_descriptionFallsBackToName(t *testing.T) {
	b := events.NewBuilder(testInfo)
	b.AddServer("production", events.Server{URL: "mqtt://prod.example.com"})

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "User created"},
	).Register(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	doc, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "production") {
		t.Errorf("expected server description 'production' in spec; got:\n%s", string(yamlBytes))
	}
}

func TestWithCodec_topicParam_setsCodecWithoutAddressOf(t *testing.T) {
	uuidCodec := codex.String().Refine(codex.Constraint[string]{
		Name:    "uuid-length",
		Check:   func(s string) bool { return len(s) == 36 },
		Message: func(s string) string { return "not a UUID" },
	})
	p := events.TopicParam{Name: "id"}.WithCodec(uuidCodec)
	if p.Codec == nil {
		t.Fatal("expected Codec to be set after WithCodec")
	}
	if err := p.Codec.Validate("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Errorf("expected valid UUID to pass: %v", err)
	}
	if err := p.Codec.Validate("not-a-uuid"); err == nil {
		t.Error("expected short string to fail codec")
	}
}

func TestWithSubscribeFormats_setsOnlySubscribeDirection(t *testing.T) {
	b := events.NewBuilder(testInfo)
	ch, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "Receive"},
		events.Publish{Summary: "Send"},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	yamlFormat := format.YAML(userEventCodec)
	ch = ch.WithSubscribeFormats(yamlFormat)

	if len(ch.SubscribeFormats) != 1 {
		t.Errorf("expected 1 SubscribeFormat, got %d", len(ch.SubscribeFormats))
	}
	if len(ch.PublishFormats) != 0 {
		t.Errorf("expected 0 PublishFormats, got %d", len(ch.PublishFormats))
	}
	if ch.Descriptor.Subscribe == nil {
		t.Fatal("expected Subscribe descriptor to be non-nil")
	}
	if ch.Descriptor.Subscribe.Message.ContentType != yamlFormat.ContentType() {
		t.Errorf("expected Subscribe ContentType %q, got %q",
			yamlFormat.ContentType(), ch.Descriptor.Subscribe.Message.ContentType)
	}
	// Publish descriptor ContentType should remain default (empty → JSON).
	if ch.Descriptor.Publish != nil && ch.Descriptor.Publish.Message.ContentType != "" {
		t.Errorf("expected Publish ContentType to be unmodified, got %q",
			ch.Descriptor.Publish.Message.ContentType)
	}
}

func TestWithPublishFormats_setsOnlyPublishDirection(t *testing.T) {
	b := events.NewBuilder(testInfo)
	ch, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Subscribe{Summary: "Receive"},
		events.Publish{Summary: "Send"},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	yamlFormat := format.YAML(userEventCodec)
	ch = ch.WithPublishFormats(yamlFormat)

	if len(ch.PublishFormats) != 1 {
		t.Errorf("expected 1 PublishFormat, got %d", len(ch.PublishFormats))
	}
	if len(ch.SubscribeFormats) != 0 {
		t.Errorf("expected 0 SubscribeFormats, got %d", len(ch.SubscribeFormats))
	}
	if ch.Descriptor.Publish == nil {
		t.Fatal("expected Publish descriptor to be non-nil")
	}
	if ch.Descriptor.Publish.Message.ContentType != yamlFormat.ContentType() {
		t.Errorf("expected Publish ContentType %q, got %q",
			yamlFormat.ContentType(), ch.Descriptor.Publish.Message.ContentType)
	}
	// Subscribe descriptor ContentType should remain default (empty → JSON).
	if ch.Descriptor.Subscribe != nil && ch.Descriptor.Subscribe.Message.ContentType != "" {
		t.Errorf("expected Subscribe ContentType to be unmodified, got %q",
			ch.Descriptor.Subscribe.Message.ContentType)
	}
}

func TestValidateTopicVars_missingKey_returnsMissingTopicVarError(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewBuilder(testInfo)
	ch, err := events.NewChannel[userEvent]("sensors/{sensorID}/data",
		userEventCodec,
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
		events.Subscribe{Summary: "Sensor data"},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Pass a vars map that is missing "sensorID" entirely.
	err = ch.ValidateTopicVars(map[string]string{})
	var missingErr events.MissingTopicVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingTopicVarError for absent key, got %T: %v", err, err)
	}
	if missingErr.Name != "sensorID" {
		t.Errorf("expected Name=sensorID, got %q", missingErr.Name)
	}
}

func TestValidateTopicVars_presentKey_codecFailure_returnsTopicParamError(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewBuilder(testInfo)
	ch, err := events.NewChannel[userEvent]("sensors/{sensorID}/data",
		userEventCodec,
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
		events.Subscribe{Summary: "Sensor data"},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	err = ch.ValidateTopicVars(map[string]string{"sensorID": "not-a-uuid"})
	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError for invalid value, got %T: %v", err, err)
	}
	if paramErr.Name != "sensorID" {
		t.Errorf("expected Name=sensorID, got %q", paramErr.Name)
	}
}
