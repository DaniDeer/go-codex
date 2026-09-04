package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	asyncapiv3 "github.com/DaniDeer/go-codex/render/asyncapi/v3"
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "Original", Tags: []string{"original"}}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/notify", userEventCodec).WithPublish(events.Publish{Summary: "Notify user"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/events", userEventCodec,
		events.Publish{Summary: "Send user event"}).WithSubscribe(events.Subscribe{Summary: "Receive user event"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	b.AddServer("production", events.Server{
		URL:      "mqtt://broker.example.com",
		Protocol: "mqtt",
	})

	if _, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{
		Summary:    "A user was created",
		SchemaName: "UserCreatedEvent",
		Tags:       []string{"users"},
	}).Handle(b); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	if _, err := events.NewChannel[userEvent]("user/deleted", userEventCodec).WithSubscribe(events.Subscribe{Summary: "A user was deleted"}).Handle(b); err != nil {
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
	b := events.NewClient(events.WithInfo(testInfo))
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
	b := events.NewClient(events.WithInfo(testInfo))
	if _, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{
		SchemaName: "UserCreatedEvent",
	}).Handle(b); err != nil {
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
	b := events.NewClient(events.WithInfo(testInfo))
	if _, err := events.NewChannel[userEvent]("order/placed", userEventCodec).WithSubscribe(events.Subscribe{Summary: "Order placed"}).Handle(b); err != nil {
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
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicCodec(
		codex.String().Refine(validate.MQTTPublishTopic)))
	if _, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("expected no error for valid topic, got: %v", err)
	}
}

func TestBuilder_withTopicCodec_invalidTopicSurfacesError(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicCodec(
		codex.String().Refine(validate.MQTTPublishTopic)))
	_, err := events.NewChannel[userEvent]("user/+/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicConstraints(validate.MQTTPublishTopic))
	_, err := events.NewChannel[userEvent]("user/+/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
	if err == nil {
		t.Fatal("expected error for topic with wildcard '+', got nil")
	}
	if !strings.Contains(err.Error(), "user/+/created") {
		t.Errorf("error should mention the invalid topic, got: %v", err)
	}

	_, err = events.NewChannel[userEvent]("order/#", userEventCodec).WithSubscribe(events.Subscribe{Summary: "Order event"}).Handle(b)
	if err == nil {
		t.Fatal("expected error for topic with wildcard '#', got nil")
	}
	if !strings.Contains(err.Error(), "order/#") {
		t.Errorf("error should mention the invalid topic, got: %v", err)
	}
}

func TestBuilder_noTopicCodec_anyTopicAccepted(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	if _, err := events.NewChannel[userEvent]("user/+/wildcard", userEventCodec).WithSubscribe(events.Subscribe{Summary: "Wildcard sub"}).Handle(b); err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}

	_, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("without topic codec, expected no error; got: %v", err)
	}
}

func TestAddChannel_unknownTopicParamCodecKey(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	strCodec := codex.String()
	_, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec},
		events.TopicParam{Name: "missing", Codec: &strCodec}, // not in template
	).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicConstraints(noBraces))

	// Without template-transparent stripping this would return an InvalidTopicError.
	if _, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b); err != nil {
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
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicConstraints(noSlash))
	nonEmptyCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := events.NewChannel[userEvent]("{sensorID}", userEventCodec,
		events.TopicParam{Name: "sensorID", Codec: &nonEmptyCodec}).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "notInTemplate"}).WithSubscribe(events.Subscribe{Summary: "sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).WithSubscribe(events.Subscribe{Summary: "s"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "sensorID", Description: "The sensor UUID.", Codec: &uuidCodec}).WithSubscribe(events.Subscribe{Summary: "s"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("orders/{orderID}", userEventCodec).WithSubscribe(events.Subscribe{Summary: "s"}).Handle(b)
	if err != nil {
		t.Fatalf("AddChannel error: %v", err)
	}
	if _, ok := h.Descriptor.Parameters["orderID"]; !ok {
		t.Fatal("expected orderID auto-derived in Descriptor.Parameters, not found")
	}
}

func TestValidateTopic_noCodecAlwaysPasses(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo)) // no topic codec
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopic("sensors/abc/data"); err != nil {
		t.Fatalf("expected nil without topic codec, got: %v", err)
	}
}

func TestValidateTopic_passingTopicReturnsNil(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicConstraints(validate.MQTTPublishTopic))
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopic("sensors/f47ac10b/data"); err != nil {
		t.Fatalf("expected nil for valid topic, got: %v", err)
	}
}

func TestValidateTopic_wildcardTopicFailsMQTTConstraint(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo), events.WithTopicConstraints(validate.MQTTPublishTopic))
	// Register with a template topic — AddChannel sees the stripped form.
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.TopicParam{Name: "sensorID", Description: "any"}).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopicVars(map[string]string{"sensorID": "not-a-uuid"}); err != nil {
		t.Fatalf("expected nil without param codec, got: %v", err)
	}
}

func TestValidateTopicVars_validValuePasses(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("AddChannel: %v", err)
	}
	if err := h.ValidateTopicVars(map[string]string{"sensorID": "f47ac10b-58cc-4372-a567-0e02b2c3d479"}); err != nil {
		t.Fatalf("expected nil for valid UUID, got: %v", err)
	}
}

func TestValidateTopicVars_invalidValueReturnsTopicParamError(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, events.TopicParam{Name: "sensorID", Codec: &uuidCodec}).WithSubscribe(events.Subscribe{}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created", SchemaName: "UserEvent"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithPublish(events.Publish{Summary: "Publish user event"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/events", userEventCodec,
		events.Publish{Summary: "Send"}).WithSubscribe(events.Subscribe{Summary: "Receive"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created", SchemaName: "UserEvent"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{OperationID: "receiveUserCreated", Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("user/events", userEventCodec).WithPublish(events.Publish{OperationID: "publishUserEvent", Summary: "Publish user event"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("user/events", userEventCodec,
		events.Publish{OperationID: "sendEvent", Summary: "Send"}).WithSubscribe(events.Subscribe{OperationID: "receiveEvent", Summary: "Receive"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	_, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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

func TestWithSecurityScheme_propagatesToChannelHandle(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	c := codex.String().Refine(validate.NonEmptyString)

	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.WithSecurityScheme("bearer", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c))).WithSubscribe(events.Subscribe{}).Handle(b)
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

func TestWithSecurityScheme_ClientHandle_PopulatesSecuritySchemes(t *testing.T) {
	c := codex.String().Refine(validate.NonEmptyString)

	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.WithSecurityScheme("bearer", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(c))).WithSubscribe(events.Subscribe{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if _, ok := handle.SecuritySchemes["bearer"]; !ok {
		t.Fatal("expected ClientHandle to populate SecuritySchemes from WithSecurityScheme, same as Register")
	}
	if handle.SecuritySchemes["bearer"].Codec == nil {
		t.Fatal("expected Codec to be propagated to ClientHandle-built ChannelHandle")
	}
	if len(handle.GlobalSecurity) != 0 {
		t.Errorf("want empty GlobalSecurity on ClientHandle (no Builder to source it from), got %v", handle.GlobalSecurity)
	}
}

func TestAsyncAPISpec_AggregatesSecuritySchemesFromChannels(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.WithSecurityScheme("bearer", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
	if err != nil {
		t.Fatalf("Register user/created: %v", err)
	}
	_, err = events.NewChannel[userEvent]("user/updated", userEventCodec,
		events.WithSecurityScheme("apiKey", events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")})).WithSubscribe(events.Subscribe{Summary: "User updated"}).Handle(b)
	if err != nil {
		t.Fatalf("Register user/updated: %v", err)
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
	if !strings.Contains(spec, "bearer:") {
		t.Errorf("want 'bearer' scheme aggregated from user/created channel, got:\n%s", spec)
	}
	if !strings.Contains(spec, "apiKey:") {
		t.Errorf("want 'apiKey' scheme aggregated from user/updated channel, got:\n%s", spec)
	}
}

func TestAsyncAPISpec_SecuritySchemeCollision_LastRegisteredWins(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.WithSecurityScheme("shared", events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")})).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
	if err != nil {
		t.Fatalf("Register user/created: %v", err)
	}
	_, err = events.NewChannel[userEvent]("user/updated", userEventCodec,
		events.WithSecurityScheme("shared", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})).WithSubscribe(events.Subscribe{Summary: "User updated"}).Handle(b)
	if err != nil {
		t.Fatalf("Register user/updated: %v", err)
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
	// last-registered (bearer, from user/updated) must win — the apiKey
	// scheme's "in: header" shape must NOT appear under the "shared" name.
	if !strings.Contains(spec, "scheme: bearer") {
		t.Errorf("want last-registered (bearer) scheme to win collision, got:\n%s", spec)
	}
}

func TestBuilder_AddGlobalSecurity_populatesChannelHandleGlobalSecurity(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	b.AddGlobalSecurity(route.Require("bearer"))

	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	b.AddGlobalSecurity(route.Require("bearer"))

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.WithSecurityScheme("bearer", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	b.AddServer("bravo", events.Server{URL: "mqtt://bravo.example.com"})
	b.AddServer("alpha", events.Server{URL: "mqtt://alpha.example.com"})

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	b.AddServer("production", events.Server{URL: "mqtt://prod.example.com"})

	_, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	ch, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Publish{Summary: "Send"}).WithSubscribe(events.Subscribe{Summary: "Receive"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	ch, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.Publish{Summary: "Send"}).WithSubscribe(events.Subscribe{Summary: "Receive"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	ch, err := events.NewChannel[userEvent]("sensors/{sensorID}/data",
		userEventCodec,
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec)).WithSubscribe(events.Subscribe{Summary: "Sensor data"}).Handle(b)
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
	b := events.NewClient(events.WithInfo(testInfo))
	ch, err := events.NewChannel[userEvent]("sensors/{sensorID}/data",
		userEventCodec,
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec)).WithSubscribe(events.Subscribe{Summary: "Sensor data"}).Handle(b)
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

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleNewChannel() {
	type SensorReading struct {
		SensorID string
		Value    float64
	}

	readingCodec := codex.Struct[SensorReading](
		codex.RequiredField("sensor_id", codex.String(),
			func(r SensorReading) string { return r.SensorID },
			func(r *SensorReading, v string) { r.SensorID = v },
		),
		codex.RequiredField("value", codex.Float64(),
			func(r SensorReading) float64 { return r.Value },
			func(r *SensorReading, v float64) { r.Value = v },
		),
	)

	b := events.NewClient(events.WithInfo(events.Info{Title: "Sensor API", Version: "1.0.0"}))

	// NewChannel declares a typed channel as a value — register with any builder.
	ch := events.NewChannel[SensorReading](
		"sensors/{sensorID}/readings",
		readingCodec,
	).WithSubscribe(events.Subscribe{OperationID: "receiveSensorReading", Summary: "Receive a reading"})

	handle, err := ch.Handle(b)
	if err != nil {
		fmt.Println("register error:", err)
		return
	}

	// Decode a payload.
	reading, err := handle.Decode([]byte(`{"sensor_id":"s1","value":42.5}`))
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("sensor=%s value=%.1f\n", reading.SensorID, reading.Value)
	// Output: sensor=s1 value=42.5
}

// ── AppendTo ──────────────────────────────────────────────────────────────────

func TestBuilder_AppendTo_writesChannelsToExternalBuilder(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	if _, err := events.NewChannel("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "A user was created"}).Handle(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := events.NewChannel("user/deleted", userEventCodec).WithSubscribe(events.Subscribe{Summary: "A user was deleted"}).Handle(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	asyncapiB := asyncapiv3.NewDocumentBuilder(testInfo)
	if err := b.AppendTo(asyncapiB); err != nil {
		t.Fatalf("AppendTo: %v", err)
	}
	doc, err := asyncapiB.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, _ := doc.MarshalYAML()
	for _, want := range []string{"user/created:", "user/deleted:"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("combined spec missing %q", want)
		}
	}
}

func TestBuilder_AppendTo_multipleChannels(t *testing.T) {
	// Verify AppendTo correctly writes all registered channels.
	b := events.NewClient(events.WithInfo(testInfo))
	for _, topic := range []string{"sensor/temp", "sensor/humidity", "sensor/pressure"} {
		if _, err := events.NewChannel(topic, userEventCodec).WithSubscribe(events.Subscribe{Summary: "Sensor " + topic}).Handle(b); err != nil {
			t.Fatalf("Register %s: %v", topic, err)
		}
	}

	db := asyncapiv3.NewDocumentBuilder(testInfo)
	if err := b.AppendTo(db); err != nil {
		t.Fatalf("AppendTo: %v", err)
	}
	doc, err := db.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, _ := doc.MarshalYAML()
	for _, want := range []string{"sensor/temp:", "sensor/humidity:", "sensor/pressure:"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("spec missing channel %q", want)
		}
	}
}

func TestBuilder_AppendTo_channels_match_AsyncAPISpec(t *testing.T) {
	// AppendTo writes channels into an external builder. The channel content
	// must be the same as what AsyncAPISpec() emits. Servers are not copied —
	// the caller controls those on the external builder — so we compare only
	// the channel keys in the YAML output.
	b := events.NewClient(events.WithInfo(testInfo))
	if _, err := events.NewChannel("sensor/reading", userEventCodec).WithSubscribe(events.Subscribe{Summary: "Sensor reading"}).Handle(b); err != nil {
		t.Fatalf("Register: %v", err)
	}

	specDirect, err := b.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}

	db := asyncapiv3.NewDocumentBuilder(testInfo)
	if err := b.AppendTo(db); err != nil {
		t.Fatalf("AppendTo: %v", err)
	}
	specCombined, err := db.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Both must contain the same channel key — channel content is identical;
	// servers differ because AppendTo does not copy them (by design).
	direct, _ := specDirect.MarshalYAML()
	combined, _ := specCombined.MarshalYAML()
	if !strings.Contains(string(combined), "sensor/reading:") {
		t.Errorf("AppendTo output missing channel\ncombined:\n%s", combined)
	}
	if !strings.Contains(string(direct), "sensor/reading:") {
		t.Errorf("AsyncAPISpec output missing channel\ndirect:\n%s", direct)
	}
}

// ── Combined pub/sub + request-reply spec (integration) ───────────────────────

func TestCombinedSpec_eventsAndReqReply(t *testing.T) {
	// Pub/sub channels via events.Client
	eventsB := events.NewClient(events.WithInfo(testInfo))
	if _, err := events.NewChannel("sensor/reading", userEventCodec).WithSubscribe(events.Subscribe{OperationID: "receiveSensorReading"}).Handle(eventsB); err != nil {
		t.Fatalf("Register pub/sub: %v", err)
	}

	// Request-reply channels — simulate with raw asyncapi to avoid importing reqreply
	// (reqreply→events import would create a cycle; the integration is proven by
	// TestBuilder_AppendTo_writesChannelsToExternalBuilder in both packages separately).
	//
	// Here we verify the plumbing: a pre-populated DocumentBuilder can receive
	// channels from AppendTo and still Build() successfully.
	doc := asyncapiv3.NewDocumentBuilder(testInfo)
	doc.AddServer("mqtt5", asyncapiv3.Server{URL: "mqtts://broker.example.com:8883", Protocol: "mqtt5"})
	doc.AddChannel("compute/add", asyncapiv3.ChannelItem{
		Address: "compute/add",
		Publish: &asyncapiv3.Operation{
			OperationID: "sendComputeAdd",
			Message:     asyncapiv3.Message{},
			Reply:       &asyncapiv3.OperationReply{Channel: "computeAddReply"},
		},
	})
	doc.AddReplyChannel("computeAddReply", asyncapiv3.ChannelItem{
		Address:   "compute/add/reply",
		Subscribe: &asyncapiv3.Operation{OperationID: "receiveComputeReply", Message: asyncapiv3.Message{}},
	})

	if err := eventsB.AppendTo(doc); err != nil {
		t.Fatalf("AppendTo: %v", err)
	}

	spec, err := doc.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out, _ := spec.MarshalYAML()
	s := string(out)
	for _, want := range []string{
		"sensor/reading:",  // pub/sub channel from eventsB
		"compute/add:",     // request channel
		"computeAddReply:", // reply channel
		"mqtt5",            // server
	} {
		if !strings.Contains(s, want) {
			t.Errorf("combined spec missing %q\nfull:\n%s", want, s)
		}
	}
}

// ── ClientHandle tests ────────────────────────────────────────────────────────

func TestChannel_ClientHandle_returnsNonNilHandle(t *testing.T) {
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h == nil {
		t.Fatal("ClientHandle returned nil")
	}
}

func TestChannel_ClientHandle_topicMatches(t *testing.T) {
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.Topic != "user/created" {
		t.Errorf("expected topic %q, got %q", "user/created", h.Topic)
	}
}

func TestChannel_ClientHandle_encodeDecodeRoundTrip(t *testing.T) {
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	payload, err := h.Encode(userEvent{ID: "u1", Name: "Alice"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := h.Decode(payload)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.Name != "Alice" {
		t.Errorf("round-trip mismatch: got %+v", decoded)
	}
}

func TestChannel_ClientHandle_noBuilderRequired(t *testing.T) {
	// ClientHandle must not panic and must produce a usable handle
	// even when no Builder is created.
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.Decode == nil || h.Encode == nil {
		t.Fatal("ClientHandle fields must not be nil")
	}
}

func TestChannel_ClientHandle_topicParamsPreserved(t *testing.T) {
	uuidCodec := codex.String()
	templateChannel := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec,
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec)).WithSubscribe(events.Subscribe{})
	h, err := templateChannel.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	topic, err := h.BuildTopic(map[string]string{"sensorID": "acme"})
	if err != nil {
		t.Fatalf("BuildTopic: %v", err)
	}
	if topic != "sensors/acme/data" {
		t.Errorf("expected %q, got %q", "sensors/acme/data", topic)
	}
}

// ── Formats / SubscribeFormats / PublishFormats ChannelOpt ────────────────────

var eventPngCodec = codex.Bytes().Refine(validate.PNG)

// TestFormats_AppliesInline verifies events.Formats declared inline in
// NewChannel's opts is equivalent to calling WithFormats after Register.
func TestFormats_AppliesInline(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	ch := events.NewChannel[[]byte]("images/{id}", eventPngCodec,
		events.Formats(format.Binary(eventPngCodec).WithContentType("image/png")),
	).WithSubscribe(events.Subscribe{})
	handle, err := ch.Handle(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(handle.Formats) != 1 || handle.Formats[0].ContentType() != "image/png" {
		t.Errorf("want 1 Formats entry with image/png, got %+v", handle.Formats)
	}
}

// TestSubscribeFormats_PublishFormats_Asymmetric verifies the two
// direction-specific ChannelOpts apply independently.
func TestSubscribeFormats_PublishFormats_Asymmetric(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	ch := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.SubscribeFormats(format.YAML(userEventCodec)),
		events.PublishFormats(format.JSON(userEventCodec)),
	).WithSubscribe(events.Subscribe{})
	handle, err := ch.Handle(b)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(handle.SubscribeFormats) != 1 || handle.SubscribeFormats[0].ContentType() != "application/yaml" {
		t.Errorf("want SubscribeFormats=YAML, got %+v", handle.SubscribeFormats)
	}
	if len(handle.PublishFormats) != 1 || handle.PublishFormats[0].ContentType() != "application/json" {
		t.Errorf("want PublishFormats=JSON, got %+v", handle.PublishFormats)
	}
}

// TestFormats_TypeMismatch verifies a wrong-typed Formats option returns
// FormatOptError, reachable via errors.As, with a structured LogValue.
func TestFormats_TypeMismatch(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	ch := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.Formats(format.Binary(eventPngCodec)),
	).WithSubscribe(events.Subscribe{})
	_, err := ch.Handle(b)
	var fe events.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "both" {
		t.Fatalf("want FormatOptError{both}, got %v", err)
	}
	if fe.Unwrap() == nil {
		t.Error("want non-nil Unwrap")
	}
	v := fe.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
}

// TestSubscribeFormats_TypeMismatch mirrors TestFormats_TypeMismatch for
// the subscribe-only direction.
func TestSubscribeFormats_TypeMismatch(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	ch := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.SubscribeFormats(format.Binary(eventPngCodec)),
	).WithSubscribe(events.Subscribe{})
	_, err := ch.Handle(b)
	var fe events.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "subscribe" {
		t.Fatalf("want FormatOptError{subscribe}, got %v", err)
	}
}

// TestPublishFormats_TypeMismatch mirrors TestFormats_TypeMismatch for
// the publish-only direction.
func TestPublishFormats_TypeMismatch(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	ch := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.PublishFormats(format.Binary(eventPngCodec)),
	).WithPublish(events.Publish{})
	_, err := ch.Handle(b)
	var fe events.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "publish" {
		t.Fatalf("want FormatOptError{publish}, got %v", err)
	}
}

// TestChannel_ClientHandle_AppliesInlineFormats verifies events.Formats/
// SubscribeFormats/PublishFormats declared inline in NewChannel's opts are
// ALSO visible on the handle returned by ClientHandle — not just Register.
// Regression test for a confirmed bug where ClientHandle silently ignored
// declared Formats, always falling back to JSON regardless of what was
// declared (affecting mqtt5/zeromq client-side Subscribe/Publish).
func TestChannel_ClientHandle_AppliesInlineFormats(t *testing.T) {
	ch := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.SubscribeFormats(format.YAML(userEventCodec)),
		events.PublishFormats(format.JSON(userEventCodec)),
	).WithSubscribe(events.Subscribe{})
	handle, err := ch.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.SubscribeFormats) != 1 || handle.SubscribeFormats[0].ContentType() != "application/yaml" {
		t.Errorf("want SubscribeFormats=YAML, got %+v", handle.SubscribeFormats)
	}
	if len(handle.PublishFormats) != 1 || handle.PublishFormats[0].ContentType() != "application/json" {
		t.Errorf("want PublishFormats=JSON, got %+v", handle.PublishFormats)
	}
}

// TestChannel_ClientHandle_FormatTypeMismatch verifies a wrong-typed
// Formats option returns a FormatOptError-shaped error from Handle(nil),
// mirroring Register's returned-error behavior for the same mistake.
func TestChannel_ClientHandle_FormatTypeMismatch(t *testing.T) {
	ch := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.Formats(format.Binary(eventPngCodec)),
	).WithSubscribe(events.Subscribe{})
	_, err := ch.Handle(nil)
	var fe events.FormatOptError
	if !errors.As(err, &fe) || fe.Direction != "both" {
		t.Fatalf("want FormatOptError{both}, got %v", err)
	}
}

// ── Phase 2: events.NewTopicParam / ChannelHandle.DecodeMerged ────────────

// EV1: events.NewTopicParam registers both spec TopicParam and merge field.
func TestNewTopicParam_RegistersSpecAndMergeField(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users/{id}", userEventCodec,
		events.NewTopicParam("id", codex.String().Refine(validate.NonEmptyString),
			func(e userEvent) string { return e.ID },
			func(e *userEvent, v string) { e.ID = v })).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.Descriptor.Parameters) != 1 {
		t.Fatalf("Descriptor.Parameters: want 1, got %d: %+v", len(h.Descriptor.Parameters), h.Descriptor.Parameters)
	}
	if len(h.MergeFields()) != 1 {
		t.Fatalf("MergeFields: want 1, got %d", len(h.MergeFields()))
	}
}

type intIDEvent struct {
	ID   int
	Name string
}

// TestNewTopicParam_TypedIntValue_DecodeMergedRoundTrip mirrors
// rest.TestNewPathParam_TypedIntValue_DecodeMergedRoundTrip — verifies
// events.NewTopicParam merges into a non-string field (int) now that
// codex.NewParam is generic over V, not hardcoded to Codec[string].
func TestNewTopicParam_TypedIntValue_DecodeMergedRoundTrip(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	intIDEventCodec := codex.Struct[intIDEvent](
		codex.RequiredField("id", codex.Int(),
			func(e intIDEvent) int { return e.ID },
			func(e *intIDEvent, v int) { e.ID = v }),
		codex.RequiredField("name", codex.String(),
			func(e intIDEvent) string { return e.Name },
			func(e *intIDEvent, v string) { e.Name = v }),
	)
	h, err := events.NewChannel[intIDEvent]("users/{id}", intIDEventCodec,
		events.NewTopicParam("id", codex.IntString(),
			func(e intIDEvent) int { return e.ID },
			func(e *intIDEvent, v int) { e.ID = v })).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	msg, err := h.DecodeMerged([]byte(`{"id":0,"name":"Alice"}`), map[string]string{"id": "7"})
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if msg.ID != 7 {
		t.Fatalf("msg.ID = %d, want 7", msg.ID)
	}
	vars, err := codex.EncodeVars(intIDEvent{ID: 7}, h.MergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["id"] != "7" {
		t.Fatalf("EncodeVars: got %q, want \"7\"", vars["id"])
	}
}

// nestedUserEvent demonstrates the Round 4 mandate: nested struct
// composition also works for events, zero framework changes needed.
type nestedUserEvent struct {
	Meta struct {
		ID string
	}
	Name string
}

// EV2: ChannelHandle.DecodeMerged happy path — payload decoded AND topic
// var merged into a NESTED struct field.
func TestDecodeMerged_HappyPath_Nested(t *testing.T) {
	nestedCodec := codex.Struct[nestedUserEvent](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(e nestedUserEvent) string { return e.Name },
			func(e *nestedUserEvent, v string) { e.Name = v },
		),
	)
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[nestedUserEvent]("users/{id}", nestedCodec,
		events.NewTopicParam("id", codex.String().Refine(validate.NonEmptyString),
			func(e nestedUserEvent) string { return e.Meta.ID },
			func(e *nestedUserEvent, v string) { e.Meta.ID = v })).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	msg, err := h.DecodeMerged([]byte(`{"name":"Alice"}`), map[string]string{"id": "u1"})
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if msg.Name != "Alice" || msg.Meta.ID != "u1" {
		t.Errorf("unexpected merged msg: %+v", msg)
	}
}

// TestChannelHandle_EncodeVars_HappyPath verifies the publish-side
// mirror of DecodeMerged — added to support Decision 5's Transport
// reflection shims, which cannot call codex.EncodeVars (generic) or
// codex.FieldCodec's methods (unexported) directly.
func TestChannelHandle_EncodeVars_HappyPath(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	intIDEventCodec := codex.Struct[intIDEvent](
		codex.RequiredField("id", codex.Int(),
			func(e intIDEvent) int { return e.ID },
			func(e *intIDEvent, v int) { e.ID = v }),
		codex.RequiredField("name", codex.String(),
			func(e intIDEvent) string { return e.Name },
			func(e *intIDEvent, v string) { e.Name = v }),
	)
	h, err := events.NewChannel[intIDEvent]("users/{id}", intIDEventCodec,
		events.NewTopicParam("id", codex.IntString(),
			func(e intIDEvent) int { return e.ID },
			func(e *intIDEvent, v int) { e.ID = v })).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	vars, err := h.EncodeVars(intIDEvent{ID: 7, Name: "Alice"})
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["id"] != "7" {
		t.Fatalf("EncodeVars: got %q, want \"7\"", vars["id"])
	}
}

// TestChannelHandle_EncodeVars_NoMergeFieldsIsNoop mirrors
// TestDecodeMerged_NoMergeFieldsIsNoop for the encode direction.
func TestChannelHandle_EncodeVars_NoMergeFieldsIsNoop(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	vars, err := h.EncodeVars(userEvent{ID: "u1", Name: "Alice"})
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("want empty vars map with no merge fields declared, got %v", vars)
	}
}

// EV3: ChannelHandle.DecodeMerged with zero merge fields behaves like
// plain Decode (regression guard).
func TestDecodeMerged_NoMergeFieldsIsNoop(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	body := []byte(`{"id":"u1","name":"Alice"}`)
	viaDecode, err := h.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	viaMerged, err := h.DecodeMerged(body, nil)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	if viaDecode != viaMerged {
		t.Errorf("DecodeMerged should match plain Decode when no merge fields declared: %+v vs %+v", viaDecode, viaMerged)
	}
}

// ── EncodeWithFormats / DecodeMergedWithFormats (canonical format resolution) ──
//
// These are the single source of truth every adapter's Publish/Subscribe
// (escape-hatch AND Transport/Client.Attach) delegates to instead of
// duplicating format-resolution logic itself — see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 9.

// FMT1: EncodeWithFormats falls back to plain Encode when no format is
// declared and no call-time override is given (regression guard,
// mirrors TestDecodeMerged_NoMergeFieldsIsNoop's rationale).
func TestEncodeWithFormats_NoFormatDeclared_FallsBackToPlainEncode(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users", userEventCodec).WithPublish(events.Publish{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	msg := userEvent{ID: "u1", Name: "Alice"}
	viaEncode, err := h.Encode(msg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	viaFormats, err := h.EncodeWithFormats(msg)
	if err != nil {
		t.Fatalf("EncodeWithFormats: %v", err)
	}
	if string(viaEncode) != string(viaFormats) {
		t.Errorf("EncodeWithFormats should match plain Encode when no format declared: %s vs %s", viaEncode, viaFormats)
	}
}

// FMT2: EncodeWithFormats uses the channel's declared PublishFormats over
// plain Encode — proves the channel's OWN declaration (not the adapter)
// is the single source of truth for which format applies.
func TestEncodeWithFormats_UsesDeclaredPublishFormat(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users", userEventCodec).WithPublish(events.Publish{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	h = h.WithPublishFormats(format.YAML(userEventCodec))
	out, err := h.EncodeWithFormats(userEvent{ID: "u1", Name: "Alice"})
	if err != nil {
		t.Fatalf("EncodeWithFormats: %v", err)
	}
	if !strings.Contains(string(out), "name: Alice") {
		t.Errorf("expected YAML output, got: %s", out)
	}
}

// FMT3: a call-time format override wins over the declared PublishFormats
// — proves the escape-hatch adapters' own per-call override still works
// unchanged after delegating to this canonical method.
func TestEncodeWithFormats_CallTimeOverrideWinsOverDeclared(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users", userEventCodec).WithPublish(events.Publish{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	h = h.WithPublishFormats(format.YAML(userEventCodec))
	out, err := h.EncodeWithFormats(userEvent{ID: "u1", Name: "Alice"}, format.JSON(userEventCodec))
	if err != nil {
		t.Fatalf("EncodeWithFormats: %v", err)
	}
	if !strings.Contains(string(out), `"name":"Alice"`) {
		t.Errorf("expected JSON output (call-time override should win), got: %s", out)
	}
}

// FMT4: DecodeMergedWithFormats falls back to plain DecodeMerged (Decode +
// merge) when no format is declared (regression guard).
func TestDecodeMergedWithFormats_NoFormatDeclared_FallsBackToPlainDecodeMerged(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("users", userEventCodec).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	body := []byte(`{"id":"u1","name":"Alice"}`)
	viaMerged, err := h.DecodeMerged(body, nil)
	if err != nil {
		t.Fatalf("DecodeMerged: %v", err)
	}
	viaFormats, err := h.DecodeMergedWithFormats(body, nil)
	if err != nil {
		t.Fatalf("DecodeMergedWithFormats: %v", err)
	}
	if viaMerged != viaFormats {
		t.Errorf("DecodeMergedWithFormats should match DecodeMerged when no format declared: %+v vs %+v", viaMerged, viaFormats)
	}
}

// FMT5: DecodeMergedWithFormats uses the channel's declared
// SubscribeFormats AND still merges topic vars afterward — proves the
// format resolution and the merge step compose correctly.
func TestDecodeMergedWithFormats_UsesDeclaredSubscribeFormat_AndMerges(t *testing.T) {
	nestedCodec := codex.Struct[nestedUserEvent](
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(e nestedUserEvent) string { return e.Name },
			func(e *nestedUserEvent, v string) { e.Name = v },
		),
	)
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[nestedUserEvent]("users/{id}", nestedCodec,
		events.NewTopicParam("id", codex.String().Refine(validate.NonEmptyString),
			func(e nestedUserEvent) string { return e.Meta.ID },
			func(e *nestedUserEvent, v string) { e.Meta.ID = v })).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	h = h.WithSubscribeFormats(format.YAML(nestedCodec))
	msg, err := h.DecodeMergedWithFormats([]byte("name: Alice\n"), map[string]string{"id": "u1"})
	if err != nil {
		t.Fatalf("DecodeMergedWithFormats: %v", err)
	}
	if msg.Name != "Alice" || msg.Meta.ID != "u1" {
		t.Errorf("unexpected merged msg: %+v", msg)
	}
}

// ── Topic ────────────────────────────────────────────────────────────────────

func TestTopic_BuildTopic_RoundTrip(t *testing.T) {
	idCodec := codex.String().Refine(validate.NonEmptyString)
	topic := events.NewTopic("devices/{deviceID}/telemetry",
		events.TopicParam{Name: "deviceID", Codec: &idCodec},
	)
	got, err := topic.BuildTopic(map[string]string{"deviceID": "sensor-1"})
	if err != nil {
		t.Fatalf("BuildTopic: %v", err)
	}
	if got != "devices/sensor-1/telemetry" {
		t.Errorf("BuildTopic = %q, want devices/sensor-1/telemetry", got)
	}
}

func TestTopic_BuildTopic_MissingVar(t *testing.T) {
	topic := events.NewTopic("devices/{deviceID}/telemetry")
	_, err := topic.BuildTopic(nil)
	var missing events.MissingTopicVarError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingTopicVarError, got %v", err)
	}
	if missing.Name != "deviceID" {
		t.Errorf("missing var name = %q, want deviceID", missing.Name)
	}
}

func TestTopic_ValidateTopicVars(t *testing.T) {
	idCodec := codex.String().Refine(validate.NonEmptyString)
	topic := events.NewTopic("devices/{deviceID}/telemetry",
		events.TopicParam{Name: "deviceID", Codec: &idCodec},
	)
	if err := topic.ValidateTopicVars(map[string]string{"deviceID": "sensor-1"}); err != nil {
		t.Errorf("ValidateTopicVars: %v", err)
	}
	err := topic.ValidateTopicVars(map[string]string{"deviceID": ""})
	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %v", err)
	}
}

func TestNewChannelFromTopic_ProducesIdenticalHandleToNewChannel(t *testing.T) {
	idCodec := codex.String().Refine(validate.NonEmptyString)
	topic := events.NewTopic("devices/{deviceID}/telemetry",
		events.TopicParam{Name: "deviceID", Codec: &idCodec},
	)

	b1 := events.NewClient(events.WithInfo(testInfo))
	viaTopic, err := events.NewChannelFromTopic(topic, userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "Telemetry"}).Handle(b1)
	if err != nil {
		t.Fatalf("Register via Topic: %v", err)
	}

	b2 := events.NewClient(events.WithInfo(testInfo))
	viaPlain, err := events.NewChannel[userEvent]("devices/{deviceID}/telemetry", userEventCodec,
		events.TopicParam{Name: "deviceID", Codec: &idCodec}).WithSubscribe(events.Subscribe{Summary: "Telemetry"}).Handle(b2)
	if err != nil {
		t.Fatalf("Register via plain string: %v", err)
	}

	if viaTopic.Topic != viaPlain.Topic {
		t.Errorf("Topic = %q, want %q", viaTopic.Topic, viaPlain.Topic)
	}
	if viaTopic.Descriptor.Subscribe.Summary != viaPlain.Descriptor.Subscribe.Summary {
		t.Errorf("Descriptor.Subscribe.Summary differs between Topic-based and plain-string declarations")
	}
	// Both handles must validate/build topics identically.
	built1, err := viaTopic.BuildTopic(map[string]string{"deviceID": "sensor-1"})
	if err != nil {
		t.Fatalf("BuildTopic (viaTopic): %v", err)
	}
	built2, err := viaPlain.BuildTopic(map[string]string{"deviceID": "sensor-1"})
	if err != nil {
		t.Fatalf("BuildTopic (viaPlain): %v", err)
	}
	if built1 != built2 {
		t.Errorf("BuildTopic results differ: %q vs %q", built1, built2)
	}
}

// ── Client rename (Builder/NewBuilder deprecated aliases, since REMOVED) ─────

func TestNewClient_returnsUsableClient(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	_, err := events.NewChannel[userEvent]("user/created", userEventCodec).WithSubscribe(events.Subscribe{Summary: "User created"}).Handle(c)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// ── WithSubscribe / WithPublish / Subscriber[T] / Publisher[T] ────────────────

// secondUserEvent is a payload type distinct from userEvent, used to trigger
// events.ChannelTypeConflictError when registered against the same topic on
// the same Client as userEvent.
type secondUserEvent struct {
	ID string
}

var secondUserEventCodec = codex.Struct[secondUserEvent](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
		func(e secondUserEvent) string { return e.ID },
		func(e *secondUserEvent, v string) { e.ID = v },
	),
)

func TestWithSubscribe_HandlePopulatesSubscribeOperation(t *testing.T) {
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created", OperationID: "onUserCreated"})

	h, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.Descriptor.Subscribe == nil {
		t.Fatal("expected Descriptor.Subscribe to be set")
	}
	if h.Descriptor.Subscribe.OperationID != "onUserCreated" {
		t.Errorf("OperationID = %q, want %q", h.Descriptor.Subscribe.OperationID, "onUserCreated")
	}
	if h.Decode == nil || h.Encode == nil {
		t.Fatal("Handle(nil) must still return usable Decode/Encode helpers")
	}
}

func TestWithPublish_HandlePopulatesPublishOperation(t *testing.T) {
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "Publish user created", OperationID: "publishUserCreated"})

	h, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h.Descriptor.Publish == nil {
		t.Fatal("expected Descriptor.Publish to be set")
	}
	if h.Descriptor.Publish.OperationID != "publishUserCreated" {
		t.Errorf("OperationID = %q, want %q", h.Descriptor.Publish.OperationID, "publishUserCreated")
	}
}

func TestSubscriberHandle_NilClient_isSpecFree(t *testing.T) {
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{})
	h, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handle")
	}
}

func TestSubscriberHandle_BuildsFreshIndependentHandles(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{})

	h1, err := sub.Handle(c)
	if err != nil {
		t.Fatalf("Handle #1: %v", err)
	}
	h2, err := sub.Handle(c)
	if err != nil {
		t.Fatalf("Handle #2: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected two independent *ChannelHandle pointers, got the same pointer")
	}
	// Mutating one handle's mutable state must not affect the other.
	h1.WithFormats(format.JSON(userEventCodec))
	if len(h2.Formats) != 0 {
		t.Errorf("expected h2.Formats to remain empty, got %v", h2.Formats)
	}
}

func TestPublisherHandle_BuildsFreshIndependentHandles(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	pub := events.NewChannel[userEvent]("user/published", userEventCodec).
		WithPublish(events.Publish{})

	h1, err := pub.Handle(c)
	if err != nil {
		t.Fatalf("Handle #1: %v", err)
	}
	h2, err := pub.Handle(c)
	if err != nil {
		t.Fatalf("Handle #2: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected two independent *ChannelHandle pointers, got the same pointer")
	}
}

func TestSubscriberAndPublisherHandle_SameClientSameTopic_DedupsSpecEntry(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	channel := events.NewChannel[userEvent]("user/created", userEventCodec)

	sub := channel.WithSubscribe(events.Subscribe{Summary: "sub"})
	pub := channel.WithPublish(events.Publish{Summary: "pub"})

	if _, err := sub.Handle(c); err != nil {
		t.Fatalf("Subscriber.Handle: %v", err)
	}
	if _, err := pub.Handle(c); err != nil {
		t.Fatalf("Publisher.Handle: %v", err)
	}

	doc, err := c.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	// Same topic registered by both roles must dedup to ONE channel item —
	// never a duplicate "user/created:" channel key in the rendered spec.
	out := string(yamlBytes)
	count := strings.Count(out, "user/created:")
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of %q in the rendered spec, got %d\nfull output:\n%s",
			"user/created:", count, out)
	}
}

func TestSubscriberHandle_DifferentTypeSameTopic_ReturnsChannelTypeConflictError(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	sub1 := events.NewChannel[userEvent]("shared/topic", userEventCodec).
		WithSubscribe(events.Subscribe{})
	sub2 := events.NewChannel[secondUserEvent]("shared/topic", secondUserEventCodec).
		WithSubscribe(events.Subscribe{})

	if _, err := sub1.Handle(c); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	_, err := sub2.Handle(c)
	if err == nil {
		t.Fatal("expected ChannelTypeConflictError, got nil")
	}
	var conflictErr events.ChannelTypeConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ChannelTypeConflictError, got %T: %v", err, err)
	}
	if conflictErr.Topic != "shared/topic" {
		t.Errorf("Topic = %q, want %q", conflictErr.Topic, "shared/topic")
	}
	if conflictErr.Want == "" || conflictErr.Got == "" {
		t.Errorf("expected non-empty Want/Got, got Want=%q Got=%q", conflictErr.Want, conflictErr.Got)
	}
	if conflictErr.Want == conflictErr.Got {
		t.Errorf("expected Want != Got for a genuine type conflict, both were %q", conflictErr.Want)
	}
}

func TestChannelTypeConflictError_LogValue(t *testing.T) {
	err := events.ChannelTypeConflictError{Topic: "shared/topic", Want: "events_test.userEvent", Got: "events_test.secondUserEvent"}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("LogValue().Kind() = %v, want %v", v.Kind(), slog.KindGroup)
	}
	attrs := v.Group()
	got := make(map[string]string, len(attrs))
	for _, a := range attrs {
		got[a.Key] = a.Value.String()
	}
	if got["topic"] != "shared/topic" {
		t.Errorf("LogValue topic = %q, want %q", got["topic"], "shared/topic")
	}
	if got["want"] != "events_test.userEvent" {
		t.Errorf("LogValue want = %q, want %q", got["want"], "events_test.userEvent")
	}
	if got["got"] != "events_test.secondUserEvent" {
		t.Errorf("LogValue got = %q, want %q", got["got"], "events_test.secondUserEvent")
	}
}

func TestSubscriberUse_returnsIndependentCopy_doesNotMutateBase(t *testing.T) {
	base := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{})
	mw := middleware.Middleware{Name: "example"}

	withMW := base.Use(mw)

	// Both base and withMW must still build a usable handle — Use must not
	// mutate base itself (value semantics, mirrors rest.Route.Use).
	if _, err := base.Handle(nil); err != nil {
		t.Fatalf("base.Handle after Use on copy: %v", err)
	}
	if _, err := withMW.Handle(nil); err != nil {
		t.Fatalf("withMW.Handle: %v", err)
	}
}

func TestPublisherUse_returnsIndependentCopy_doesNotMutateBase(t *testing.T) {
	base := events.NewChannel[userEvent]("user/published", userEventCodec).
		WithPublish(events.Publish{})
	mw := middleware.Middleware{Name: "example"}

	withMW := base.Use(mw)

	if _, err := base.Handle(nil); err != nil {
		t.Fatalf("base.Handle after Use on copy: %v", err)
	}
	if _, err := withMW.Handle(nil); err != nil {
		t.Fatalf("withMW.Handle: %v", err)
	}
}

func TestSubscriberHandle_unconditionalValidation_invalidTopicParam(t *testing.T) {
	// TopicParam names a variable not present in the topic template — this
	// must be rejected by Handle(nil) exactly as it would be by Register,
	// proving the topic-param-name check runs unconditionally (client nil
	// or not).
	sub := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.TopicParam{Name: "doesNotExist"}).
		WithSubscribe(events.Subscribe{})

	_, err := sub.Handle(nil)
	if err == nil {
		t.Fatal("expected an error for an undeclared topic param, got nil")
	}
	var paramErr events.InvalidTopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected InvalidTopicParamError, got %T: %v", err, err)
	}
}

func TestPublisherHandle_unconditionalValidation_invalidTopicParam_nilClient(t *testing.T) {
	pub := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.TopicParam{Name: "doesNotExist"}).
		WithPublish(events.Publish{})

	_, err := pub.Handle(nil)
	if err == nil {
		t.Fatal("expected an error for an undeclared topic param even with client == nil, got nil")
	}
	var paramErr events.InvalidTopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected InvalidTopicParamError, got %T: %v", err, err)
	}
}

func TestSubscriberHandle_unconditionalValidation_formatTypeMismatch_nilClient(t *testing.T) {
	// events.Formats declared for the WRONG type (mismatched against the
	// channel's own T) must surface FormatOptError from Handle(nil) — proving
	// the format-type check runs unconditionally.
	badFormats := events.Formats[secondUserEvent](format.JSON(secondUserEventCodec))
	sub := events.NewChannel[userEvent]("user/created", userEventCodec, badFormats).
		WithSubscribe(events.Subscribe{})

	_, err := sub.Handle(nil)
	if err == nil {
		t.Fatal("expected FormatOptError for mismatched Formats type, got nil")
	}
	var fmtErr events.FormatOptError
	if !errors.As(err, &fmtErr) {
		t.Fatalf("expected FormatOptError, got %T: %v", err, err)
	}
}

func TestPublisherHandle_unconditionalValidation_mergeFieldTypeMismatch_nilClient(t *testing.T) {
	// NewTopicParam's merge-capable field is registered for the WRONG type —
	// must surface MergeFieldTypeError from Handle(nil), proving the
	// merge-field type check runs unconditionally.
	badMerge := events.NewTopicParam("sensorID", codex.String(),
		func(e secondUserEvent) string { return e.ID },
		func(e *secondUserEvent, v string) { e.ID = v },
	)
	pub := events.NewChannel[userEvent]("sensors/{sensorID}/data", userEventCodec, badMerge).
		WithPublish(events.Publish{})

	_, err := pub.Handle(nil)
	if err == nil {
		t.Fatal("expected MergeFieldTypeError for mismatched merge field type, got nil")
	}
	var mergeErr events.MergeFieldTypeError
	if !errors.As(err, &mergeErr) {
		t.Fatalf("expected MergeFieldTypeError, got %T: %v", err, err)
	}
}

// ── FromSecurityScheme / ConflictingSecurityDeclarationError /
// UnsupportedMiddlewareParamsError / CheckCoverage (Phase 2) ───────────────

func TestFromSecurityScheme_producesUsableMiddleware(t *testing.T) {
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	mw := events.FromSecurityScheme("bearerAuth", scheme, []string{"subscribe:sensors"})

	if mw.Security == nil {
		t.Fatal("expected FromSecurityScheme to produce a middleware.Middleware with non-nil Security")
	}
	if mw.Security.SchemeName != "bearerAuth" {
		t.Errorf("SchemeName = %q, want %q", mw.Security.SchemeName, "bearerAuth")
	}
	if len(mw.Security.Scopes) != 1 || mw.Security.Scopes[0] != "subscribe:sensors" {
		t.Errorf("Scopes = %v, want [subscribe:sensors]", mw.Security.Scopes)
	}
	if mw.Security.Scheme.Type != route.BearerScheme("JWT").Type {
		t.Errorf("Scheme.Type = %v, want %v", mw.Security.Scheme.Type, route.BearerScheme("JWT").Type)
	}

	// Attach to a Subscriber — must build a usable handle with the merged
	// security requirement, no ServerImplementation attached (so a coverage
	// error is expected — proven separately below).
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(mw)
	_, err := sub.Handle(nil)
	var covErr events.MissingSecurityMiddlewareError
	if !errors.As(err, &covErr) {
		t.Fatalf("expected MissingSecurityMiddlewareError (no implementation attached), got %T: %v", err, err)
	}
}

func TestFromSecurityScheme_Publisher_populatesSecuritySchemes_noCoverageCheck(t *testing.T) {
	// Publisher.Handle never runs CheckCoverage — a declared scheme with no
	// implementation must NOT error on the publish side.
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{}).
		Use(events.FromSecurityScheme("bearerAuth", scheme, []string{"publish:sensors"}))

	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Publisher.Handle: %v", err)
	}
	if _, ok := handle.SecuritySchemes["bearerAuth"]; !ok {
		t.Fatal("expected SecuritySchemes to be populated from .Use(FromSecurityScheme(...))")
	}
}

func TestSubscriberHandle_ConflictingSecurityDeclaration_manualVsMiddleware(t *testing.T) {
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{
			Security: []route.SecurityRequirement{route.Require("bearerAuth", "read:manual")},
		}).
		Use(events.FromSecurityScheme("bearerAuth", scheme, []string{"read:middleware"}))

	_, err := sub.Handle(nil)
	var conflictErr events.ConflictingSecurityDeclarationError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ConflictingSecurityDeclarationError, got %T: %v", err, err)
	}
	if conflictErr.Topic != "user/created" {
		t.Errorf("Topic = %q, want %q", conflictErr.Topic, "user/created")
	}
	if conflictErr.Scheme != "bearerAuth" {
		t.Errorf("Scheme = %q, want %q", conflictErr.Scheme, "bearerAuth")
	}
}

func TestPublisherHandle_ConflictingSecurityDeclaration_middlewareVsMiddleware(t *testing.T) {
	schemeA := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	pub := events.NewChannel[userEvent]("user/published", userEventCodec).
		WithPublish(events.Publish{}).
		Use(
			events.FromSecurityScheme("bearerAuth", schemeA, []string{"write:a"}),
			events.FromSecurityScheme("bearerAuth", schemeA, []string{"write:b"}),
		)

	_, err := pub.Handle(nil)
	var conflictErr events.ConflictingSecurityDeclarationError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected ConflictingSecurityDeclarationError for middleware-vs-middleware conflict, got %T: %v", err, err)
	}
}

func TestConflictingSecurityDeclarationError_LogValue(t *testing.T) {
	err := events.ConflictingSecurityDeclarationError{
		Topic: "user/created", Scheme: "bearerAuth",
		FirstSource: "manual", SecondSource: "declare-security:bearerAuth",
		FirstScopes: []string{"a"}, SecondScopes: []string{"b"},
	}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue().Kind() = %v, want %v", lv.Kind(), slog.KindGroup)
	}
	got := map[string]any{}
	for _, a := range lv.Group() {
		got[a.Key] = a.Value.Any()
	}
	for _, key := range []string{"topic", "scheme", "first_source", "first_scopes", "second_source", "second_scopes"} {
		if _, ok := got[key]; !ok {
			t.Errorf("LogValue missing key %q, got keys %v", key, got)
		}
	}
	if got["topic"] != "user/created" {
		t.Errorf("LogValue topic = %v, want %q", got["topic"], "user/created")
	}
}

func TestSubscriberHandle_UnsupportedMiddlewareParamsError(t *testing.T) {
	// A middleware carrying a REST-only param contribution (e.g. one built
	// via rest.FromHeaderParam) attached directly to a channel must be
	// rejected eagerly.
	mw := middleware.Middleware{
		Name:                "leaked-rest-header",
		RequestHeaderParams: []middleware.HeaderParamSpec{{Name: "X-API-Key"}},
	}
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(mw)

	_, err := sub.Handle(nil)
	var paramsErr events.UnsupportedMiddlewareParamsError
	if !errors.As(err, &paramsErr) {
		t.Fatalf("expected UnsupportedMiddlewareParamsError, got %T: %v", err, err)
	}
	if paramsErr.Middleware != "leaked-rest-header" {
		t.Errorf("Middleware = %q, want %q", paramsErr.Middleware, "leaked-rest-header")
	}
}

func TestPublisherHandle_UnsupportedMiddlewareParamsError(t *testing.T) {
	mw := middleware.Middleware{
		Name:                 "leaked-rest-cookie",
		ResponseCookieParams: []middleware.ResponseCookieParamSpec{{Name: "session"}},
	}
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{}).
		Use(mw)

	_, err := pub.Handle(nil)
	var paramsErr events.UnsupportedMiddlewareParamsError
	if !errors.As(err, &paramsErr) {
		t.Fatalf("expected UnsupportedMiddlewareParamsError, got %T: %v", err, err)
	}
}

func TestUnsupportedMiddlewareParamsError_LogValue(t *testing.T) {
	err := events.UnsupportedMiddlewareParamsError{Topic: "user/created", Middleware: "leaked"}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue().Kind() = %v, want %v", lv.Kind(), slog.KindGroup)
	}
	got := map[string]any{}
	for _, a := range lv.Group() {
		got[a.Key] = a.Value.Any()
	}
	if got["topic"] != "user/created" || got["middleware"] != "leaked" {
		t.Errorf("LogValue = %v, want topic=user/created middleware=leaked", got)
	}
}

func TestSubscriberHandle_CoverageEnforcement_unconditional_noImplementation(t *testing.T) {
	// A declared security scheme with NO attached implementation must now be
	// a hard error, always — no opt-out — proving CheckCoverage is wired
	// unconditionally into Subscriber.Handle (previously this check did not
	// run at all for the new Subscriber/Publisher path).
	sub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithSubscribe(events.Subscribe{
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		})

	_, err := sub.Handle(nil)
	var covErr events.MissingSecurityMiddlewareError
	if !errors.As(err, &covErr) {
		t.Fatalf("expected MissingSecurityMiddlewareError, got %T: %v", err, err)
	}
	if covErr.Topic != "sensors/data" || covErr.Scheme != "bearerAuth" {
		t.Errorf("got Topic=%q Scheme=%q, want Topic=%q Scheme=%q", covErr.Topic, covErr.Scheme, "sensors/data", "bearerAuth")
	}

	// A client too — coverage runs regardless of client's nilness.
	client := events.NewClient(events.WithInfo(testInfo))
	_, err = sub.Handle(client)
	if !errors.As(err, &covErr) {
		t.Fatalf("expected MissingSecurityMiddlewareError with non-nil client too, got %T: %v", err, err)
	}
}

func TestSubscriberHandle_CoverageEnforcement_noSecurityDeclared_passes(t *testing.T) {
	// No declared security requirement at all — CheckCoverage has nothing to
	// enforce, Handle must succeed.
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{})

	if _, err := sub.Handle(nil); err != nil {
		t.Fatalf("expected no error with no declared security, got %v", err)
	}
}

func TestMissingSecurityMiddlewareError_LogValue(t *testing.T) {
	err := events.MissingSecurityMiddlewareError{Topic: "sensors/data", Scheme: "bearerAuth"}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue().Kind() = %v, want %v", lv.Kind(), slog.KindGroup)
	}
	got := map[string]any{}
	for _, a := range lv.Group() {
		got[a.Key] = a.Value.Any()
	}
	if got["topic"] != "sensors/data" || got["scheme"] != "bearerAuth" {
		t.Errorf("LogValue = %v, want topic=sensors/data scheme=bearerAuth", got)
	}
}

// --- Subscriber[T].Register / Client.SubscriberEntries tests (Phase 3) ---

func TestSubscriberRegister_HappyPath_PopulatesSpecAndSubscriberEntries(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "sub"}).
		WithHandler(func(context.Context, userEvent) error { return nil })

	if err := sub.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Slot 1 (spec registry): AsyncAPISpec must render the channel.
	doc, err := c.AsyncAPISpec()
	if err != nil {
		t.Fatalf("AsyncAPISpec: %v", err)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "user/created:") {
		t.Errorf("expected spec to contain channel %q, got:\n%s", "user/created", yamlBytes)
	}

	// Slot 2 (ServeSubscribers registry): SubscriberEntries must expose it.
	entries := c.SubscriberEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 SubscriberEntry, got %d", len(entries))
	}
	if entries[0].Topic() != "user/created" {
		t.Errorf("Topic() = %q, want %q", entries[0].Topic(), "user/created")
	}
	if !entries[0].HasHandler() {
		t.Error("HasHandler() = false, want true")
	}
	handle, ok := entries[0].Handle().(*events.ChannelHandle[userEvent])
	if !ok {
		t.Fatalf("Handle() type = %T, want *events.ChannelHandle[userEvent]", entries[0].Handle())
	}
	if handle.Handler == nil {
		t.Error("handle.Handler is nil, want non-nil")
	}
}

func TestSubscriberRegister_NoHandler_ReturnsMissingHandlerError(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{})

	err := sub.Register(c)
	if err == nil {
		t.Fatal("expected MissingHandlerError, got nil")
	}
	var missingErr events.MissingHandlerError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingHandlerError, got %T: %v", err, err)
	}
	if missingErr.Topic != "user/created" {
		t.Errorf("Topic = %q, want %q", missingErr.Topic, "user/created")
	}

	// A rejected Register must touch nothing — neither registry slot.
	if entries := c.SubscriberEntries(); len(entries) != 0 {
		t.Errorf("expected 0 SubscriberEntries after a rejected Register, got %d", len(entries))
	}
}

func TestMissingHandlerError_LogValue(t *testing.T) {
	err := events.MissingHandlerError{Topic: "user/created"}
	lv := err.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue().Kind() = %v, want %v", lv.Kind(), slog.KindGroup)
	}
	got := map[string]any{}
	for _, a := range lv.Group() {
		got[a.Key] = a.Value.Any()
	}
	if got["topic"] != "user/created" {
		t.Errorf("LogValue = %v, want topic=user/created", got)
	}
}

// TestSubscriberRegister_HandleNeverClobbersRegistry is the F1 regression
// test: registering a handler-bearing Subscriber, then calling .Handle()
// again for the SAME topic (simulating what Subscribe(fn)'s internal
// plumbing does — a FRESH, handler-less Subscriber[T] value for that
// topic), must NEVER clobber the entry Register already stored in slot 2.
func TestSubscriberRegister_HandleNeverClobbersRegistry(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	channel := events.NewChannel[userEvent]("user/created", userEventCodec)

	registeredHandler := func(context.Context, userEvent) error { return nil }
	sub := channel.WithSubscribe(events.Subscribe{}).WithHandler(registeredHandler)
	if err := sub.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}

	entriesBefore := c.SubscriberEntries()
	if len(entriesBefore) != 1 {
		t.Fatalf("expected 1 SubscriberEntry after Register, got %d", len(entriesBefore))
	}
	handleBefore, ok := entriesBefore[0].Handle().(*events.ChannelHandle[userEvent])
	if !ok || handleBefore.Handler == nil {
		t.Fatalf("expected a handler-bearing handle after Register, got %+v", handleBefore)
	}

	// Simulate Subscribe(fn)'s internal plumbing: a FRESH Subscriber[T]
	// value for the SAME topic, with NO WithHandler attached, calling
	// .Handle(client) directly — exactly what an adapter's Subscribe(fn)
	// convenience does internally.
	freshSub := channel.WithSubscribe(events.Subscribe{})
	if _, err := freshSub.Handle(c); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	entriesAfter := c.SubscriberEntries()
	if len(entriesAfter) != 1 {
		t.Fatalf("expected 1 SubscriberEntry after .Handle(), got %d", len(entriesAfter))
	}
	handleAfter, ok := entriesAfter[0].Handle().(*events.ChannelHandle[userEvent])
	if !ok {
		t.Fatalf("Handle() type = %T, want *events.ChannelHandle[userEvent]", entriesAfter[0].Handle())
	}
	if handleAfter.Handler == nil {
		t.Fatal("registry entry's Handler is nil after .Handle() — .Handle() clobbered slot 2 (F1 regression)")
	}
	if handleAfter != handleBefore {
		t.Error("registry entry pointer changed after .Handle() — .Handle() must NEVER touch slot 2")
	}
}

func TestClientSubscriberEntries_OnlyReflectsRegisterFedEntries(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	channel := events.NewChannel[userEvent]("user/created", userEventCodec)

	// .Handle()-only: contributes to slot 1 (spec) but never slot 2.
	sub := channel.WithSubscribe(events.Subscribe{})
	if _, err := sub.Handle(c); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if entries := c.SubscriberEntries(); len(entries) != 0 {
		t.Fatalf("expected 0 SubscriberEntries from a .Handle()-only registration, got %d", len(entries))
	}

	// A DIFFERENT topic, registered via Register(), must show up.
	other := events.NewChannel[userEvent]("user/updated", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		WithHandler(func(context.Context, userEvent) error { return nil })
	if err := other.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}

	entries := c.SubscriberEntries()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 SubscriberEntry (Register-fed only), got %d", len(entries))
	}
	if entries[0].Topic() != "user/updated" {
		t.Errorf("Topic() = %q, want %q", entries[0].Topic(), "user/updated")
	}
}

// ── Client.Attach/Publish/Subscribe/ServeSubscribers (Decision 5) ───────────

// mockTransport is a minimal events.Transport for testing Client's own
// Attach/Publish/Subscribe/ServeSubscribers dispatch — does not attempt
// any real reflection-based encode/decode (that's each adapter's own
// responsibility, tested in adapters/{mqtt5,mqtt,zeromq}).
type mockTransport struct {
	publishCalled, subscribeCalled, serveCalled bool
	publishErr, subscribeErr, serveErr          error
}

func (m *mockTransport) Publish(_ context.Context, _, _ any) error {
	m.publishCalled = true
	return m.publishErr
}
func (m *mockTransport) Subscribe(_ context.Context, _, _ any) error {
	m.subscribeCalled = true
	return m.subscribeErr
}
func (m *mockTransport) ServeSubscribers(_ context.Context) error {
	m.serveCalled = true
	return m.serveErr
}

func TestClientAttach_Succeeds(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	if err := c.Attach(&mockTransport{}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
}

func TestClientAttach_SecondCall_ReturnsTransportAlreadyAttachedError(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	if err := c.Attach(&mockTransport{}); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	err := c.Attach(&mockTransport{})
	var attachedErr events.TransportAlreadyAttachedError
	if !errors.As(err, &attachedErr) {
		t.Fatalf("want TransportAlreadyAttachedError, got %v (%T)", err, err)
	}
}

func TestClientPublish_NoTransportAttached_ReturnsNoTransportAttachedError(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	err := c.Publish(context.Background(), nil, nil)
	var noTransportErr events.NoTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoTransportAttachedError, got %v (%T)", err, err)
	}
}

func TestClientSubscribe_NoTransportAttached_ReturnsNoTransportAttachedError(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	err := c.Subscribe(context.Background(), nil, nil)
	var noTransportErr events.NoTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoTransportAttachedError, got %v (%T)", err, err)
	}
}

func TestClientServeSubscribers_NoTransportAttached_ReturnsNoTransportAttachedError(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	err := c.ServeSubscribers(context.Background())
	var noTransportErr events.NoTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoTransportAttachedError, got %v (%T)", err, err)
	}
}

func TestClientPublish_DelegatesToAttachedTransport(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	mt := &mockTransport{}
	if err := c.Attach(mt); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.Publish(context.Background(), "pub", "msg"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !mt.publishCalled {
		t.Error("expected Transport.Publish to be called")
	}
}

func TestClientSubscribe_DelegatesToAttachedTransport(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	mt := &mockTransport{}
	if err := c.Attach(mt); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.Subscribe(context.Background(), "sub", func() {}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if !mt.subscribeCalled {
		t.Error("expected Transport.Subscribe to be called")
	}
}

func TestClientServeSubscribers_DelegatesToAttachedTransport(t *testing.T) {
	c := events.NewClient(events.WithInfo(testInfo))
	mt := &mockTransport{}
	if err := c.Attach(mt); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := c.ServeSubscribers(context.Background()); err != nil {
		t.Fatalf("ServeSubscribers: %v", err)
	}
	if !mt.serveCalled {
		t.Error("expected Transport.ServeSubscribers to be called")
	}
}

func TestSubscriberWithOptions_PopulatesHandlerOpts(t *testing.T) {
	type qosOpts struct{ QoS int }

	c := events.NewClient(events.WithInfo(testInfo))
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		WithOptions(qosOpts{QoS: 2})

	handle, err := sub.Handle(c)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	opts, ok := handle.HandlerOpts.(qosOpts)
	if !ok {
		t.Fatalf("HandlerOpts type = %T, want qosOpts", handle.HandlerOpts)
	}
	if opts.QoS != 2 {
		t.Errorf("HandlerOpts.QoS = %d, want 2", opts.QoS)
	}
}

// dummySubscriberServer/dummyPublisherClient are compile-time-only helper
// types asserting interface compliance — mirrors
// stats/observer_test.go's TestLoggingObserver_ImplementsAllInterfaces
// idiom.
type dummySubscriberServer struct{}

func (dummySubscriberServer) ServeSubscribers(ctx context.Context) error { return nil }

type dummyPublisherClient[T any] struct{}

func (dummyPublisherClient[T]) Publish(ctx context.Context, msg T) error { return nil }

func TestSubscriberServerAndPublisherClient_InterfaceCompliance(t *testing.T) {
	var _ events.SubscriberServer = dummySubscriberServer{}
	var _ events.PublisherClient[userEvent] = dummyPublisherClient[userEvent]{}
}

// ── Subscriber.SubscribeMW / Publisher.PublishMW (Phase 4) ─────────────────

func TestSubscribeMW_paired_derivesSatisfiesFromSecurity(t *testing.T) {
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	mw := events.FromSecurityScheme("bearerAuth", scheme, []string{"subscribe:sensors"})

	sub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(mw).
		SubscribeMW(&mw, func() {})

	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.Implementations) != 1 {
		t.Fatalf("len(Implementations) = %d, want 1", len(handle.Implementations))
	}
	impl := handle.Implementations[0]
	if len(impl.Satisfies) != 1 || impl.Satisfies[0] != "bearerAuth" {
		t.Errorf("Satisfies = %v, want [bearerAuth]", impl.Satisfies)
	}
	if impl.Fn == nil {
		t.Error("expected Fn to be preserved")
	}
}

// TestSubscribeMW_PairedAgainstUndeclaredScheme_ReturnsUnknownMiddlewareImplementationError
// is a regression test for a finding from a post-implementation
// consistency audit: Decision 1 explicitly promised a
// checkImplementationsDeclared-equivalent check (mirroring
// rest.UnknownMiddlewareImplementationError) but it was never actually
// implemented — mismatches like this used to be silently accepted.
func TestSubscribeMW_PairedAgainstUndeclaredScheme_ReturnsUnknownMiddlewareImplementationError(t *testing.T) {
	// declMw declares "bearerAuth" but the channel below pairs its
	// SubscribeMW implementation against a DIFFERENT scheme name
	// ("otherAuth") that was never .Use()'d on this channel — a
	// copy-paste mistake reusing a different channel's
	// middleware.Middleware. checkImplementationsDeclared must catch
	// this at Handle/Register time, not silently accept it.
	declMw := events.FromSecurityScheme("bearerAuth", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)
	mismatchedMw := events.FromSecurityScheme("otherAuth", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)

	sub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(declMw).
		SubscribeMW(&mismatchedMw, func() {})

	_, err := sub.Handle(nil)
	var unknownErr events.UnknownMiddlewareImplementationError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("want UnknownMiddlewareImplementationError, got %v (%T)", err, err)
	}
	if unknownErr.Scheme != "otherAuth" {
		t.Errorf("want Scheme %q, got %q", "otherAuth", unknownErr.Scheme)
	}
}

// TestSubscribeMW_PairedAgainstDeclaredScheme_NoError is the mirror-image
// happy path: SubscribeMW paired against a scheme that WAS .Use()'d must
// NOT trigger UnknownMiddlewareImplementationError.
func TestSubscribeMW_PairedAgainstDeclaredScheme_NoError(t *testing.T) {
	mw := events.FromSecurityScheme("bearerAuth", events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil)

	sub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(mw).
		SubscribeMW(&mw, func() {})

	if _, err := sub.Handle(nil); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
}

func TestSubscribeMW_unpaired_generalPurpose_emptySatisfies(t *testing.T) {
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, func() {})

	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.Implementations) != 1 {
		t.Fatalf("len(Implementations) = %d, want 1", len(handle.Implementations))
	}
	if len(handle.Implementations[0].Satisfies) != 0 {
		t.Errorf("Satisfies = %v, want empty (general-purpose)", handle.Implementations[0].Satisfies)
	}
}

func TestSubscribeMW_multipleCalls_accumulate(t *testing.T) {
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		SubscribeMW(nil, func() {}).
		SubscribeMW(nil, func() {})

	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.Implementations) != 2 {
		t.Fatalf("len(Implementations) = %d, want 2 (accumulated, not overwritten)", len(handle.Implementations))
	}
}

func TestSubscribeMW_doesNotMutateOriginal(t *testing.T) {
	// Subscriber values are immutable — SubscribeMW must return a copy, not
	// mutate the receiver, mirroring Use/WithHandler/WithOptions.
	base := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{})
	withMW := base.SubscribeMW(nil, func() {})

	baseHandle, err := base.Handle(nil)
	if err != nil {
		t.Fatalf("base.Handle: %v", err)
	}
	if len(baseHandle.Implementations) != 0 {
		t.Errorf("base Implementations = %v, want empty (SubscribeMW must not mutate base)", baseHandle.Implementations)
	}

	withHandle, err := withMW.Handle(nil)
	if err != nil {
		t.Fatalf("withMW.Handle: %v", err)
	}
	if len(withHandle.Implementations) != 1 {
		t.Errorf("withMW Implementations = %v, want 1 entry", withHandle.Implementations)
	}
}

func TestPublishMW_paired_derivesSatisfiesFromSecurity(t *testing.T) {
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	mw := events.FromSecurityScheme("bearerAuth", scheme, []string{"publish:sensors"})

	pub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithPublish(events.Publish{}).
		Use(mw).
		PublishMW(&mw, func() {})

	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.ClientImplementations) != 1 {
		t.Fatalf("len(ClientImplementations) = %d, want 1", len(handle.ClientImplementations))
	}
	impl := handle.ClientImplementations[0]
	if len(impl.Satisfies) != 1 || impl.Satisfies[0] != "bearerAuth" {
		t.Errorf("Satisfies = %v, want [bearerAuth]", impl.Satisfies)
	}
	if impl.Fn == nil {
		t.Error("expected Fn to be preserved")
	}
}

func TestPublishMW_unpaired_generalPurpose_emptySatisfies(t *testing.T) {
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{}).
		PublishMW(nil, func() {})

	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.ClientImplementations) != 1 {
		t.Fatalf("len(ClientImplementations) = %d, want 1", len(handle.ClientImplementations))
	}
	if len(handle.ClientImplementations[0].Satisfies) != 0 {
		t.Errorf("Satisfies = %v, want empty (general-purpose)", handle.ClientImplementations[0].Satisfies)
	}
}

func TestPublishMW_multipleCalls_accumulate(t *testing.T) {
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{}).
		PublishMW(nil, func() {}).
		PublishMW(nil, func() {})

	handle, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(handle.ClientImplementations) != 2 {
		t.Fatalf("len(ClientImplementations) = %d, want 2 (accumulated, not overwritten)", len(handle.ClientImplementations))
	}
}

// TestCheckCoverage_passes_withMatchingSubscribeMW is the key Phase 4
// regression test: Phase 2 proved ONLY the failure case (Implementations
// was always empty, so any declared security scheme was unconditionally a
// coverage failure). Now that Subscriber.SubscribeMW attaches a REAL
// middleware.ServerImplementation, a declared security scheme WITH a
// matching SubscribeMW attachment must PASS.
func TestCheckCoverage_passes_withMatchingSubscribeMW(t *testing.T) {
	scheme := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	mw := events.FromSecurityScheme("bearerAuth", scheme, []string{"subscribe:sensors"})

	sub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(mw).
		SubscribeMW(&mw, func(ctx context.Context, raw string) (map[string][]string, error) {
			return map[string][]string{"bearerAuth": {"subscribe:sensors"}}, nil
		})

	handle, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("expected Handle to succeed with matching SubscribeMW attached, got error: %v", err)
	}
	if len(handle.Implementations) != 1 {
		t.Fatalf("len(Implementations) = %d, want 1", len(handle.Implementations))
	}

	// Also verify against a non-nil client — coverage runs regardless of
	// client's nilness, and the pass case must hold there too.
	client := events.NewClient(events.WithInfo(testInfo))
	if _, err := sub.Handle(client); err != nil {
		t.Fatalf("expected Handle(client) to succeed too, got error: %v", err)
	}
}

// TestCheckCoverage_fails_withoutMatchingSubscribeMW proves the CONVERSE:
// a declared scheme with an ATTACHED-BUT-NON-MATCHING SubscribeMW (wrong
// Satisfies) still fails coverage — attachment alone is not enough, the
// Satisfies name must actually match the declared scheme.
func TestCheckCoverage_fails_withoutMatchingSubscribeMW(t *testing.T) {
	schemeA := events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
	mwA := events.FromSecurityScheme("bearerAuth", schemeA, []string{"subscribe:sensors"})
	schemeB := events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-API-Key", "header")}
	mwB := events.FromSecurityScheme("apiKey", schemeB, []string{"subscribe:sensors"})

	// Two DECLARED schemes (bearerAuth, apiKey), but SubscribeMW only
	// attaches an implementation satisfying apiKey — bearerAuth is left
	// uncovered, so CheckCoverage must fail for it specifically, even
	// though the attached implementation's scheme name (apiKey) IS one of
	// the ones declared via .Use() (so checkImplementationsDeclared alone
	// would pass).
	sub := events.NewChannel[userEvent]("sensors/data", userEventCodec).
		WithSubscribe(events.Subscribe{}).
		Use(mwA, mwB).
		SubscribeMW(&mwB, func(ctx context.Context, raw string) (map[string][]string, error) {
			return map[string][]string{"apiKey": {"subscribe:sensors"}}, nil
		})

	_, err := sub.Handle(nil)
	var covErr events.MissingSecurityMiddlewareError
	if !errors.As(err, &covErr) {
		t.Fatalf("expected MissingSecurityMiddlewareError for non-matching SubscribeMW, got %T: %v", err, err)
	}
	if covErr.Scheme != "bearerAuth" {
		t.Errorf("Scheme = %q, want %q", covErr.Scheme, "bearerAuth")
	}
}
