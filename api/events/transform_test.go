package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

// propOptIn is an In shape with NO required fields — used where a test
// needs [events.NewOptionalPropertyParam]'s absent-is-not-an-error
// behavior to be observable without the OUTER InCodec.Validate call also
// failing on a zero value (mirrors transform_dispatch_test.go's tdEmpty
// pattern, one level up).
type propOptIn struct{ TenantID string }

var propOptInCodec = codex.Struct[propOptIn]()

// ── property vocabulary axis: WithSubscribeProperty/WithPublishProperty ──

func TestMiddleware_WithSubscribeProperty_MergesIn(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("tenant-policy")).
		WithSubscribeProperty(events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v }))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		msg.Name = in.Key
		return nil
	})
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Fatalf("want exactly one MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}

	// Decoded from its OWN, SEPARATE property-value map — topicVars is nil
	// here, confirming the two axes never combine.
	in, err := h.MiddlewareHandlers[0].DecodeIn(nil, map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}
	got, ok := in.(mdTestIn)
	if !ok || got.Key != "acme" {
		t.Errorf("want decoded In{Key: %q}, got %#v", "acme", in)
	}
}

func TestMiddleware_WithPublishProperty_EncodesOutIntoMessage(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("tenant-out-policy")).
		WithPublishProperty(events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(out mdTestOut) string { return out.Value },
			func(out *mdTestOut, v string) { out.Value = v }))
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg userEvent) (mdTestOut, error) {
		return mdTestOut{Value: "acme"}, nil
	})
	h, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want exactly one ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}

	topicVars, propertyVars, err := h.ClientMiddlewareHandlers[0].EncodeOut(mdTestOut{Value: "acme"})
	if err != nil {
		t.Fatalf("EncodeOut: %v", err)
	}
	if len(topicVars) != 0 {
		t.Errorf("want no topic vars (only WithPublishProperty declared), got %v", topicVars)
	}
	if propertyVars["tenantID"] != "acme" {
		t.Errorf("want propertyVars[tenantID] = %q, got %v", "acme", propertyVars)
	}
}

func TestMiddleware_WithSubscribeProperty_RequiredButAdapterSuppliesNoPropertyMap(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("tenant-policy")).
		WithSubscribeProperty(events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v }))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil })
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Simulates an adapter with no property mechanism (e.g. zeromq) — a
	// REQUIRED property fails naturally with the SAME MiddlewareInputError
	// a missing topic var would, no special-casing needed.
	_, err = h.MiddlewareHandlers[0].DecodeIn(nil, nil)
	var mie events.MiddlewareInputError
	if !errors.As(err, &mie) {
		t.Fatalf("want MiddlewareInputError, got %v", err)
	}
}

func TestMiddleware_WithOptionalSubscribeProperty_AbsentLeavesZeroValueNoError(t *testing.T) {
	decl := middleware.NewDeclaration("tenant-optional-policy", propOptInCodec, mdTestOutCodec)
	mw := events.NewMiddleware(decl).
		WithSubscribeProperty(events.NewOptionalPropertyParam("tenantID", codex.String(),
			func(in propOptIn) string { return in.TenantID },
			func(in *propOptIn, v string) { in.TenantID = v }))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in propOptIn) error { return nil })
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	in, err := h.MiddlewareHandlers[0].DecodeIn(nil, nil)
	if err != nil {
		t.Fatalf("want no error for an absent OPTIONAL property, got %v", err)
	}
	got, ok := in.(propOptIn)
	if !ok || got.TenantID != "" {
		t.Errorf("want zero-value In, got %#v", in)
	}
}

// ── Round 14: property merge composes with a non-JSON (Gob) format ───────

func TestMiddleware_PropertyMergeComposesWithGobRequestFormat(t *testing.T) {
	type payload struct{ Data string }
	payloadCodec := codex.Struct[payload](
		codex.RequiredField("data", codex.String(),
			func(p payload) string { return p.Data },
			func(p *payload, v string) { p.Data = v },
		),
	)
	gobFmt := format.Gob(payloadCodec)

	mw := events.NewMiddleware(newTestDeclaration("tenant-gob-policy")).
		WithSubscribeProperty(events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v }))

	subscriber := events.NewChannel[payload]("uploads/data", payloadCodec, events.Formats(gobFmt)).
		WithSubscribe(events.Subscribe{Summary: "Upload received"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *payload, in mdTestIn) error { return nil })
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.Formats) != 1 {
		t.Fatalf("want channel Formats set (Gob), got %d", len(h.Formats))
	}

	// Payload format (Gob) and property merge remain fully orthogonal —
	// neither affects the other.
	in, err := h.MiddlewareHandlers[0].DecodeIn(nil, map[string]string{"tenantID": "acme"})
	if err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}
	got, ok := in.(mdTestIn)
	if !ok || got.Key != "acme" {
		t.Errorf("want decoded In{Key: %q}, got %#v", "acme", in)
	}
}
