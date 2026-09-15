package events_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── PropertyParam/MergedPropertyParam[T] construction ────────────────────

func TestNewPropertyParam_RequiredByDefault(t *testing.T) {
	p := events.NewPropertyParam("tenantID", codex.String(),
		func(s string) string { return s },
		func(s *string, v string) { *s = v })
	if !p.Required {
		t.Error("want Required true")
	}
}

func TestNewOptionalPropertyParam_NotRequired(t *testing.T) {
	p := events.NewOptionalPropertyParam("tenantID", codex.String(),
		func(s string) string { return s },
		func(s *string, v string) { *s = v })
	if p.Required {
		t.Error("want Required false")
	}
}

func TestPropertyParam_WithCodec(t *testing.T) {
	c := codex.String()
	p := events.PropertyParam{Param: codex.Param{Name: "x"}}.WithCodec(c)
	if p.Codec == nil {
		t.Fatal("want Codec set")
	}
}

func TestMergedPropertyParam_WithDescription(t *testing.T) {
	p := events.NewPropertyParam("tenantID", codex.String(),
		func(s string) string { return s },
		func(s *string, v string) { *s = v }).WithDescription("tenant id")
	if p.Description != "tenant id" {
		t.Errorf("want Description %q, got %q", "tenant id", p.Description)
	}
}

// ── Round 14: nested sub-struct field access ──────────────────────────────

func TestNewPropertyParam_NestedSubStructField_GetSetReachesIntoSubstruct(t *testing.T) {
	type meta struct{ TenantID string }
	type in struct{ Meta meta }

	p := events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
		func(i in) string { return i.Meta.TenantID },
		func(i *in, v string) { i.Meta.TenantID = v })

	var target in
	if err := codex.DecodeVars(&target, map[string]string{"tenantID": "acme"}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if target.Meta.TenantID != "acme" {
		t.Errorf("want nested field populated, got %+v", target)
	}

	vars, err := codex.EncodeVars(target, p.Field)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["tenantID"] != "acme" {
		t.Errorf("want encoded var %q, got %v", "acme", vars)
	}
}

func TestNewOptionalPropertyParam_AbsentLeavesZeroValue(t *testing.T) {
	type in struct{ TenantID string }
	p := events.NewOptionalPropertyParam("tenantID", codex.String(),
		func(i in) string { return i.TenantID },
		func(i *in, v string) { i.TenantID = v })

	var target in
	if err := codex.DecodeVars(&target, map[string]string{}, p.Field); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if target.TenantID != "" {
		t.Errorf("want zero value, got %q", target.TenantID)
	}
}

// ── Round 42: MergedPropertyParam[T] direct (Middleware-free) channel
// attachment — the symmetry-bug fix. Mirrors
// TestNewTopicParam_RegistersSpecAndMergeField/
// TestNewTopicParam_TypedIntValue_DecodeMergedRoundTrip's exact shape,
// proving MergedPropertyParam now behaves like MergedTopicParam when
// attached directly to NewChannel — no Middleware wrapper needed.

// TestNewPropertyParam_DirectAttachment_RegistersSpecAndMergeField proves
// attaching a MergedPropertyParam directly to NewChannel registers BOTH the
// spec metadata AND the merge field (previously only the former).
func TestNewPropertyParam_DirectAttachment_RegistersSpecAndMergeField(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(e userEvent) string { return e.Name },
			func(e *userEvent, v string) { e.Name = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.Descriptor.Subscribe.Message.Headers.Properties) != 1 {
		t.Fatalf("Descriptor.Subscribe.Message.Headers.Properties: want 1, got %d", len(h.Descriptor.Subscribe.Message.Headers.Properties))
	}
	if len(h.PropertyMergeFields()) != 1 {
		t.Fatalf("PropertyMergeFields: want 1, got %d", len(h.PropertyMergeFields()))
	}
}

// TestNewPropertyParam_DirectAttachment_MergesWithoutMiddleware proves the
// merge actually happens end-to-end via codex.DecodeVars/EncodeVars using
// the handle's own PropertyMergeFields() — no Middleware[In,Out] involved.
func TestNewPropertyParam_DirectAttachment_MergesWithoutMiddleware(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(e userEvent) string { return e.Name },
			func(e *userEvent, v string) { e.Name = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	msg, err := h.Decode([]byte(`{"id":"u1","name":"placeholder"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := codex.DecodeVars(&msg, map[string]string{"tenantID": "acme"}, h.PropertyMergeFields()...); err != nil {
		t.Fatalf("DecodeVars: %v", err)
	}
	if msg.Name != "acme" {
		t.Fatalf("want merged property value %q, got %q", "acme", msg.Name)
	}

	vars, err := codex.EncodeVars(msg, h.PropertyMergeFields()...)
	if err != nil {
		t.Fatalf("EncodeVars: %v", err)
	}
	if vars["tenantID"] != "acme" {
		t.Fatalf("EncodeVars: got %q, want %q", vars["tenantID"], "acme")
	}
}

// TestChannelHandle_MergePropertyVars_EncodePropertyVars proves the
// convenience wrapper pair added for symmetry with
// reqreply.RouteHandle.MergePropertyVars/EncodePropertyVars (and with
// ChannelHandle.MergeFields's own EncodeVars/DecodeMerged pair) — a
// caller can merge/derive property values via ONE method call, without
// reaching into PropertyMergeFields()/codex.DecodeVars/EncodeVars
// manually.
func TestChannelHandle_MergePropertyVars_EncodePropertyVars(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(e userEvent) string { return e.Name },
			func(e *userEvent, v string) { e.Name = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	msg, err := h.Decode([]byte(`{"id":"u1","name":"placeholder"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := h.MergePropertyVars(&msg, map[string]string{"tenantID": "acme"}); err != nil {
		t.Fatalf("MergePropertyVars: %v", err)
	}
	if msg.Name != "acme" {
		t.Fatalf("want merged property value %q, got %q", "acme", msg.Name)
	}

	vars, err := h.EncodePropertyVars(msg)
	if err != nil {
		t.Fatalf("EncodePropertyVars: %v", err)
	}
	if vars["tenantID"] != "acme" {
		t.Fatalf("EncodePropertyVars: got %q, want %q", vars["tenantID"], "acme")
	}
}

// TestChannelHandle_MergePropertyVars_EncodePropertyVars_NoFields_NoOp
// proves both methods are safe no-ops (nil error, nil map) when the
// channel declares no property merge fields — mirrors MergeFields()'s own
// empty-slice behavior for EncodeVars/DecodeMerged.
func TestChannelHandle_MergePropertyVars_EncodePropertyVars_NoFields_NoOp(t *testing.T) {
	b := events.NewClient(events.WithInfo(testInfo))
	h, err := events.NewChannel[userEvent]("user/plain", userEventCodec).
		WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	msg := userEvent{ID: "u1", Name: "Alice"}
	if err := h.MergePropertyVars(&msg, map[string]string{"tenantID": "acme"}); err != nil {
		t.Fatalf("MergePropertyVars: want no-op nil error, got %v", err)
	}
	if msg.Name != "Alice" {
		t.Fatalf("want msg unchanged, got %+v", msg)
	}

	vars, err := h.EncodePropertyVars(msg)
	if err != nil {
		t.Fatalf("EncodePropertyVars: want nil error, got %v", err)
	}
	if vars != nil {
		t.Fatalf("EncodePropertyVars: want nil map, got %v", vars)
	}
}
