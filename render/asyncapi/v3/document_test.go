package v3_test

import (
	"encoding/json"
	"strings"
	"testing"

	v3 "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/schema"
)

var testInfo = v3.Info{Title: "Test API", Version: "1.0.0"}

func buildDoc(t *testing.T, b *v3.DocumentBuilder) v3.Document {
	t.Helper()
	doc, err := b.Build()
	if err != nil {
		t.Fatalf("Build() error: %v", err)
	}
	return doc
}

func mustYAML(t *testing.T, doc v3.Document) string {
	t.Helper()
	out, err := doc.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML() error: %v", err)
	}
	return string(out)
}

func TestV3DocumentBuilder_minimalDocument(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ping", v3.ChannelItem{
		Subscribe: &v3.Operation{Summary: "ping"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "asyncapi: 3.0.0") {
		t.Errorf("want asyncapi: 3.0.0 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Test API") {
		t.Errorf("want title in output, got:\n%s", out)
	}
	if !strings.Contains(out, "1.0.0") {
		t.Errorf("want version in output, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_serverRendered(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddServer("production", v3.Server{
		URL:      "broker.example.com:1883",
		Protocol: "mqtt",
	})
	b.AddChannel("ping", v3.ChannelItem{Subscribe: &v3.Operation{Summary: "ping"}})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "host:") {
		t.Errorf("want host: key, got:\n%s", out)
	}
	if !strings.Contains(out, "protocol:") {
		t.Errorf("want protocol: key, got:\n%s", out)
	}
	if strings.Contains(out, "url:") {
		t.Errorf("must NOT contain url: key (AsyncAPI 3.0 uses host:), got:\n%s", out)
	}
	if !strings.Contains(out, "broker.example.com:1883") {
		t.Errorf("want host value, got:\n%s", out)
	}
	if !strings.Contains(out, "mqtt") {
		t.Errorf("want protocol value, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_serverWithSecurity(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddServer("production", v3.Server{
		URL:      "broker.example.com:1883",
		Protocol: "mqtt",
		Security: []route.SecurityRequirement{route.Require("bearerAuth")},
	})
	b.AddChannel("ping", v3.ChannelItem{Subscribe: &v3.Operation{Summary: "ping"}})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "security:") {
		t.Errorf("want security: in server output, got:\n%s", out)
	}
	if !strings.Contains(out, "bearerAuth") {
		t.Errorf("want bearerAuth in security, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_channelsAndOperationsSeparated(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Summary: "User created"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "channels:") {
		t.Errorf("want top-level channels: key, got:\n%s", out)
	}
	if !strings.Contains(out, "operations:") {
		t.Errorf("want top-level operations: key, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_channelAddressField(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Address:   "user/created",
		Subscribe: &v3.Operation{Summary: "User created"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "address: user/created") {
		t.Errorf("want address: user/created, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_channelAddressDefaultsToKey(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Address:   "",
		Subscribe: &v3.Operation{Summary: "User created"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "address: userCreated") {
		t.Errorf("want address to default to channel key 'userCreated', got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationActionReceive(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Summary: "User created"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "action: receive") {
		t.Errorf("want action: receive for Subscribe op, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationActionSend(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Publish: &v3.Operation{Summary: "User created"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "action: send") {
		t.Errorf("want action: send for Publish op, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationChannelRef(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Summary: "User created"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "#/channels/userCreated") {
		t.Errorf("want channel.$ref '#/channels/userCreated', got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationIDDerived(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "receiveUserCreated") {
		t.Errorf("want auto-derived operationID 'receiveUserCreated', got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationIDExplicit(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{OperationID: "onUserCreated"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "onUserCreated") {
		t.Errorf("want explicit operationID 'onUserCreated', got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationSecurity(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "security:") {
		t.Errorf("want security: in operation output, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationSecurityNil(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Security: nil},
	})
	out := mustYAML(t, buildDoc(t, b))

	if strings.Contains(out, "security:") {
		t.Errorf("want NO security: key when Security is nil, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_operationSecurityEmpty(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Security: []route.SecurityRequirement{}},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "security: []") {
		t.Errorf("want security: [] for empty slice, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_schemaInComponents(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddSchema("UserEvent", schema.Schema{Type: "object"})
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Summary: "ping"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "components:") {
		t.Errorf("want components: key, got:\n%s", out)
	}
	if !strings.Contains(out, "schemas:") {
		t.Errorf("want schemas: key in components, got:\n%s", out)
	}
	if !strings.Contains(out, "UserEvent") {
		t.Errorf("want UserEvent in schemas, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_securitySchemeInComponents(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", route.BearerScheme("JWT"))
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{Summary: "ping"},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "securitySchemes:") {
		t.Errorf("want securitySchemes: key in components, got:\n%s", out)
	}
	if !strings.Contains(out, "bearerAuth") {
		t.Errorf("want bearerAuth in securitySchemes, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_buildErrorOnEmptyChannel(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("bad", v3.ChannelItem{})
	_, err := b.Build()
	if err == nil {
		t.Fatal("want error for channel with no subscribe or publish, got nil")
	}
}

func TestV3DocumentBuilder_marshalJSON(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ping", v3.ChannelItem{Subscribe: &v3.Operation{Summary: "ping"}})
	doc := buildDoc(t, b)

	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("JSON invalid: %v", err)
	}
	if m["asyncapi"] != "3.0.0" {
		t.Errorf("want asyncapi=3.0.0, got %v", m["asyncapi"])
	}
}

func TestV3DocumentBuilder_marshalYAML(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ping", v3.ChannelItem{Subscribe: &v3.Operation{Summary: "ping"}})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "asyncapi: 3.0.0") {
		t.Errorf("want asyncapi: 3.0.0 in YAML, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_messageInChannel(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Message: v3.Message{
				Name:       "UserCreatedMessage",
				SchemaName: "UserCreatedEvent",
				Schema:     schema.Schema{Type: "object"},
			},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "messages:") {
		t.Errorf("want messages: key in channel, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_schemaAutoCollectedFromMessage(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("userCreated", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Message: v3.Message{
				SchemaName: "UserCreatedEvent",
				Schema:     schema.Schema{Type: "object"},
			},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "UserCreatedEvent") {
		t.Errorf("want UserCreatedEvent auto-collected into components/schemas, got:\n%s", out)
	}
	if !strings.Contains(out, "schemas:") {
		t.Errorf("want schemas: in components, got:\n%s", out)
	}
}

func TestV3DocumentBuilder_channelItem_titleSummaryTags(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("user.created", v3.ChannelItem{
		Address:     "user/created",
		Title:       "User Created Channel",
		Summary:     "Fires when a user signs up",
		Description: "Extended description.",
		Tags:        []string{"users", "events"},
		Subscribe: &v3.Operation{
			Summary: "Receive user created events",
			Message: v3.Message{Schema: schema.Schema{Type: "object"}},
		},
	})
	out := mustYAML(t, buildDoc(t, b))
	for _, want := range []string{"title: User Created Channel", "summary: Fires when a user signs up", "tags:", "name: users"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in output:\n%s", want, out)
		}
	}
}

func TestOperationReply_EmittedInSendOp(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	reqSchema := schema.Schema{Type: "object"}
	respSchema := schema.Schema{Type: "object"}
	b.AddChannel("computeRequest", v3.ChannelItem{
		Address: "/compute",
		Publish: &v3.Operation{
			OperationID: "sendComputeRequest",
			Summary:     "Send a compute request.",
			Message:     v3.Message{Schema: reqSchema},
			Reply:       &v3.OperationReply{Channel: "computeReply"},
		},
	})
	b.AddReplyChannel("computeReply", v3.ChannelItem{
		Address: "/compute/reply",
		Subscribe: &v3.Operation{
			OperationID: "receiveComputeReply",
			Summary:     "Receive the compute reply.",
			Message:     v3.Message{Schema: respSchema},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "reply:") {
		t.Errorf("want 'reply:' in YAML output:\n%s", out)
	}
	if !strings.Contains(out, "$ref: '#/channels/computeReply'") {
		t.Errorf("want reply channel $ref in output:\n%s", out)
	}
	if !strings.Contains(out, "sendComputeRequest") {
		t.Errorf("want sendComputeRequest operation in output:\n%s", out)
	}
	if !strings.Contains(out, "receiveComputeReply") {
		t.Errorf("want receiveComputeReply operation in output:\n%s", out)
	}
}

func TestOperationReply_BothChannelsInSpec(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("computeRequest", v3.ChannelItem{
		Address: "/compute",
		Publish: &v3.Operation{
			Message: v3.Message{Schema: schema.Schema{Type: "object"}},
			Reply:   &v3.OperationReply{Channel: "computeReply"},
		},
	})
	b.AddReplyChannel("computeReply", v3.ChannelItem{
		Address: "/compute/reply",
		Subscribe: &v3.Operation{
			Message: v3.Message{Schema: schema.Schema{Type: "object"}},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "computeRequest:") {
		t.Errorf("want computeRequest channel:\n%s", out)
	}
	if !strings.Contains(out, "computeReply:") {
		t.Errorf("want computeReply channel:\n%s", out)
	}
}

func TestOperationReply_NilReply_NoReplyBlock(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("events", v3.ChannelItem{
		Subscribe: &v3.Operation{
			OperationID: "receiveEvent",
			Message:     v3.Message{Schema: schema.Schema{Type: "object"}},
		},
	})
	out := mustYAML(t, buildDoc(t, b))
	if strings.Contains(out, "reply:") {
		t.Errorf("must not emit 'reply:' when Reply is nil:\n%s", out)
	}
}

func TestAddReplyChannel_ValidatesWithoutSubscribeOrPublish(t *testing.T) {
	// Reply-only channel with no subscribe/publish should not cause Build to fail.
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("computeRequest", v3.ChannelItem{
		Publish: &v3.Operation{
			Message: v3.Message{Schema: schema.Schema{Type: "object"}},
			Reply:   &v3.OperationReply{Channel: "computeReply"},
		},
	})
	// Reply channel has no Subscribe/Publish — would fail normal AddChannel validation.
	b.AddReplyChannel("computeReply", v3.ChannelItem{Address: "/compute/reply"})
	if _, err := b.Build(); err != nil {
		t.Fatalf("Build() must not error for reply channel with no operations: %v", err)
	}
}

func TestAddChannel_StillValidatesNonReplyChannel(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("noOps", v3.ChannelItem{Address: "/no-ops"}) // no subscribe/publish
	if _, err := b.Build(); err == nil {
		t.Fatal("Build() must error when non-reply channel has no subscribe or publish operation")
	}
}

func TestOperationReply_JSONMarshal(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("req", v3.ChannelItem{
		Publish: &v3.Operation{
			Message: v3.Message{Schema: schema.Schema{Type: "object"}},
			Reply:   &v3.OperationReply{Channel: "rep"},
		},
	})
	b.AddReplyChannel("rep", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Message: v3.Message{Schema: schema.Schema{Type: "object"}},
		},
	})
	doc := buildDoc(t, b)
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("JSON parse error: %v", err)
	}
	ops, _ := m["operations"].(map[string]any)
	found := false
	for _, v := range ops {
		op, _ := v.(map[string]any)
		if _, ok := op["reply"]; ok {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'reply' key in at least one operation in JSON output")
	}
}
