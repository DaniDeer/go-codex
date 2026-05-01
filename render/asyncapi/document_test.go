package asyncapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/render/asyncapi"
	"github.com/DaniDeer/go-codex/schema"
)

func TestDocumentBuilder_emptyBuildProducesMinimalDocument(t *testing.T) {
	doc, err := asyncapi.NewDocumentBuilder(testInfo).Build()
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("user/created", asyncapi.ChannelItem{
			Description: "User creation events.",
			Subscribe: &asyncapi.Operation{
				Summary: "User created",
				Message: asyncapi.Message{
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("order/placed", asyncapi.ChannelItem{
			Publish: &asyncapi.Operation{
				Summary: "Place an order",
				Message: asyncapi.Message{
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
	inlineSchema := schema.Schema{Type: "object", Properties: map[string]schema.Schema{"x": {Type: "integer"}}}
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("item/updated", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Message: asyncapi.Message{
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("user/deleted", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Summary: "User deleted",
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("cmd/send-email", asyncapi.ChannelItem{
			Publish: &asyncapi.Operation{
				Summary: "Send email command",
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("chat/message", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Summary: "Receive chat message",
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
			},
			Publish: &asyncapi.Operation{
				Summary: "Send chat message",
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
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
	_, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("empty/channel", asyncapi.ChannelItem{
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddSchema("MyEvent", explicitSchema).
		AddChannel("my/event", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Message: asyncapi.Message{
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddServer("production", asyncapi.Server{
			URL:         "amqp://broker.example.com",
			Protocol:    "amqp",
			Description: "Production broker",
		}).
		AddChannel("ping", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("ping", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
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
	doc, err := asyncapi.NewDocumentBuilder(testInfo).
		AddChannel("user/created", asyncapi.ChannelItem{
			Subscribe: &asyncapi.Operation{
				Tags:    []string{"user", "events"},
				Message: asyncapi.Message{Schema: schema.Schema{Type: "object"}},
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
