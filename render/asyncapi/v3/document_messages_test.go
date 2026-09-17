package v3_test

import (
	"encoding/json"
	"strings"
	"testing"

	v3 "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/schema"
)

// jsonMap marshals doc to JSON and unmarshals it into a generic map, for
// tests that need to inspect structure (e.g. confirm a key is ABSENT)
// rather than just substring-match the rendered text.
func jsonMap(t *testing.T, doc v3.Document) (map[string]any, error) {
	t.Helper()
	raw, err := doc.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// This file tests Topic 3's AsyncAPI multi-message migration (see
// docs/roadmap/error-handling-rest-events-reqreply.md): Operation.Messages
// lists MULTIPLE named message variants for one channel/operation, the
// direct analogue of OpenAPI's per-status responses object.

func TestOperation_Messages_MultipleNamedVariants(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("computeAddReply", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Messages: []v3.Message{
				{Name: "Success", SchemaName: "ComputeResp", Schema: schema.Schema{Type: "object"}},
				{Name: "ErrorConflict", SchemaName: "ConflictPayload", Schema: schema.Schema{Type: "object"}},
			},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "Success:") {
		t.Errorf("want Success message key in spec:\n%s", out)
	}
	if !strings.Contains(out, "ErrorConflict:") {
		t.Errorf("want ErrorConflict message key in spec:\n%s", out)
	}
	if !strings.Contains(out, "ComputeResp") {
		t.Errorf("want ComputeResp schema registered in components:\n%s", out)
	}
	if !strings.Contains(out, "ConflictPayload") {
		t.Errorf("want ConflictPayload schema registered in components:\n%s", out)
	}
}

func TestOperation_Messages_TakesPriorityOverMessage(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ch", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Message: v3.Message{Name: "ShouldBeIgnored", SchemaName: "IgnoredSchema", Schema: schema.Schema{Type: "string"}},
			Messages: []v3.Message{
				{Name: "Winner", SchemaName: "WinnerSchema", Schema: schema.Schema{Type: "object"}},
			},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "Winner:") {
		t.Errorf("want Messages to take priority over Message:\n%s", out)
	}
	if strings.Contains(out, "ShouldBeIgnored") {
		t.Errorf("want Message ignored when Messages is set:\n%s", out)
	}
}

func TestOperation_Messages_FallsBackToSchemaNameThenGeneratedKey(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ch", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Messages: []v3.Message{
				{SchemaName: "NamedBySchema", Schema: schema.Schema{Type: "object"}},
				{Schema: schema.Schema{Type: "string"}}, // no Name, no SchemaName
			},
		},
	})
	out := mustYAML(t, buildDoc(t, b))

	if !strings.Contains(out, "NamedBySchema:") {
		t.Errorf("want message key falling back to SchemaName:\n%s", out)
	}
	if !strings.Contains(out, "message2:") {
		t.Errorf("want message key falling back to a generated \"message<N>\" key:\n%s", out)
	}
}

func TestOperation_Messages_DuplicateKey_ReturnsError(t *testing.T) {
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ch", v3.ChannelItem{
		Subscribe: &v3.Operation{
			Messages: []v3.Message{
				{Name: "Dup", Schema: schema.Schema{Type: "object"}},
				{Name: "Dup", Schema: schema.Schema{Type: "string"}},
			},
		},
	})
	doc := buildDoc(t, b)
	_, err := doc.MarshalYAML()
	if err == nil {
		t.Fatal("want a DuplicateMessageKeyError, got nil")
	}
	var dupErr v3.DuplicateMessageKeyError
	if !asDuplicateMessageKeyError(err, &dupErr) {
		t.Fatalf("want errors.As-recoverable DuplicateMessageKeyError, got: %v", err)
	}
	if dupErr.Key != "Dup" {
		t.Errorf("want Key=\"Dup\", got %q", dupErr.Key)
	}
}

// asDuplicateMessageKeyError avoids importing "errors" solely for one
// errors.As call in this file — kept local and trivial.
func asDuplicateMessageKeyError(err error, target *v3.DuplicateMessageKeyError) bool {
	de, ok := err.(v3.DuplicateMessageKeyError)
	if ok {
		*target = de
	}
	return ok
}

func TestOperation_Messages_OperationLevelArrayLeftEmpty(t *testing.T) {
	// Per AsyncAPI 3.0 spec, an operation with no explicit "messages"
	// array applies ALL of its channel's declared messages — so the
	// renderer intentionally does NOT populate an operation-level
	// messages array; this test guards against a future regression that
	// adds one unnecessarily.
	b := v3.NewDocumentBuilder(testInfo)
	b.AddChannel("ch", v3.ChannelItem{
		Subscribe: &v3.Operation{
			OperationID: "receiveCh",
			Messages: []v3.Message{
				{Name: "A", Schema: schema.Schema{Type: "object"}},
				{Name: "B", Schema: schema.Schema{Type: "object"}},
			},
		},
	})
	m, err := jsonMap(t, buildDoc(t, b))
	if err != nil {
		t.Fatalf("jsonMap: %v", err)
	}
	ops, _ := m["operations"].(map[string]any)
	op, _ := ops["receiveCh"].(map[string]any)
	if _, has := op["messages"]; has {
		t.Errorf("want NO operation-level messages array, got: %v", op["messages"])
	}
}
