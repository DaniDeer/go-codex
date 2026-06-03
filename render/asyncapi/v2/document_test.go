package v2_test

import (
	"encoding/json"
	"strings"
	"testing"

	v2 "github.com/DaniDeer/go-codex/render/asyncapi/v2"
	"github.com/DaniDeer/go-codex/schema"
)

func TestDocumentBuilder_emptyBuildProducesMinimalDocument(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).Build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "asyncapi:") {
		t.Error("missing asyncapi field in output")
	}
	if !strings.Contains(out, "User Events") {
		t.Error("missing title in output")
	}
}

func TestDocumentBuilder_addChannel_populatesChannels(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("user/created", v2.ChannelItem{
			Description: "User creation events.",
			Subscribe: &v2.Operation{
				Summary: "User created",
				Message: v2.Message{
					Schema:     userSchema,
					SchemaName: "User",
				},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "user/created") {
		t.Error("missing channel name in output")
	}
	if !strings.Contains(out, "subscribe:") {
		t.Error("missing subscribe operation in output")
	}
	if !strings.Contains(out, "User created") {
		t.Error("missing summary in output")
	}
}

func TestDocumentBuilder_messageWithSchemaName_emitsRef(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("order/placed", v2.ChannelItem{
			Publish: &v2.Operation{
				Summary: "Place an order",
				Message: v2.Message{
					Schema:     userSchema,
					SchemaName: "Order",
				},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "$ref: '#/components/schemas/Order'") {
		t.Errorf("missing $ref in payload:\n%s", out)
	}
	if !strings.Contains(out, "components:") {
		t.Error("missing components section in output")
	}
}

func TestDocumentBuilder_messageWithoutSchemaName_inlinesSchema(t *testing.T) {
	inlineSchema := schema.Schema{Type: "object", Properties: []schema.Property{{Name: "x", Schema: schema.Schema{Type: "integer"}}}}
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("item/updated", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Message: v2.Message{
					Schema: inlineSchema,
				},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if strings.Contains(out, "$ref") {
		t.Error("unexpected $ref — payload should be inlined")
	}
	if !strings.Contains(out, "type: object") {
		t.Errorf("missing inlined schema in output:\n%s", out)
	}
}

func TestDocumentBuilder_subscribeOnly(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("user/deleted", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Summary: "User deleted",
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "subscribe:") {
		t.Error("missing subscribe in output")
	}
	if strings.Contains(out, "publish:") {
		t.Error("unexpected publish in subscribe-only channel")
	}
}

func TestDocumentBuilder_publishOnly(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("cmd/send-email", v2.ChannelItem{
			Publish: &v2.Operation{
				Summary: "Send email command",
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if strings.Contains(out, "subscribe:") {
		t.Error("unexpected subscribe in publish-only channel")
	}
	if !strings.Contains(out, "publish:") {
		t.Error("missing publish in output")
	}
}

func TestDocumentBuilder_subscribeAndPublish_onSameChannel(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("chat/message", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Summary: "Receive chat message",
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
			Publish: &v2.Operation{
				Summary: "Send chat message",
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "subscribe:") || !strings.Contains(out, "publish:") {
		t.Errorf("missing subscribe or publish on bidirectional channel:\n%s", out)
	}
}

func TestDocumentBuilder_channelWithNoOps_returnsError(t *testing.T) {
	_, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("empty/channel", v2.ChannelItem{
			Description: "No ops defined.",
		}).
		Build()
	if err == nil {
		t.Error("expected error for channel with no operations, got nil")
	}
}

func TestDocumentBuilder_explicitSchemaWinsOverChannel(t *testing.T) {
	channelSchema := schema.Schema{Type: "string"}
	explicitSchema := schema.Schema{Type: "object", Title: "Explicit"}
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddSchema("MyEvent", explicitSchema).
		AddChannel("my/event", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Message: v2.Message{
					Schema:     channelSchema,
					SchemaName: "MyEvent",
				},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "Explicit") {
		t.Errorf("explicit schema title not found in output:\n%s", out)
	}
}

func TestDocumentBuilder_addServer_includesInOutput(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddServer("production", v2.Server{
			URL:         "amqp://broker.example.com",
			Protocol:    "amqp",
			Description: "Production broker",
		}).
		AddChannel("ping", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "amqp://broker.example.com") {
		t.Error("missing server URL in output")
	}
	if !strings.Contains(out, "amqp") {
		t.Error("missing server protocol in output")
	}
}

func TestDocument_marshalJSON_validJSON(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("ping", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if !json.Valid(b) {
		t.Errorf("MarshalJSON produced invalid JSON: %s", b)
	}
}

func TestDocumentBuilder_operationTags_inOutput(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("user/created", v2.ChannelItem{
			Subscribe: &v2.Operation{
				Tags:    []string{"user", "events"},
				Message: v2.Message{Schema: schema.Schema{Type: "object"}},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}

	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "user") || !strings.Contains(out, "events") {
		t.Errorf("missing operation tags in output:\n%s", out)
	}
}

func TestDocumentBuilder_channelParameters_emittedForTemplateChannel(t *testing.T) {
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("sensors/{sensorID}/data", v2.ChannelItem{
			Parameters: map[string]v2.Parameter{
				"sensorID": {
					Description: "The sensor UUID.",
					Schema:      schema.Schema{Type: "string", Format: "uuid"},
				},
			},
			Subscribe: &v2.Operation{
				Summary: "Receive sensor data",
				Message: v2.Message{Schema: userSchema},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "parameters:") {
		t.Errorf("expected 'parameters:' in output:\n%s", out)
	}
	if !strings.Contains(out, "sensorID:") {
		t.Errorf("expected 'sensorID:' in parameters:\n%s", out)
	}
	if !strings.Contains(out, "The sensor UUID.") {
		t.Errorf("expected description in parameters:\n%s", out)
	}
	if !strings.Contains(out, "format: uuid") {
		t.Errorf("expected 'format: uuid' schema in parameters:\n%s", out)
	}
}

func TestDocumentBuilder_channelParameters_defaultTypeString(t *testing.T) {
	// A parameter with zero-value Schema should default to {type: string}.
	doc, err := v2.NewDocumentBuilder(testInfo).
		AddChannel("events/{eventID}", v2.ChannelItem{
			Parameters: map[string]v2.Parameter{
				"eventID": {},
			},
			Subscribe: &v2.Operation{
				Summary: "Some event",
				Message: v2.Message{Schema: userSchema},
			},
		}).
		Build()
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	b, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML error: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "type: string") {
		t.Errorf("expected default 'type: string' for zero-value Schema:\n%s", out)
	}
}
