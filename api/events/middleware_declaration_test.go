package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

// ── fixtures ─────────────────────────────────────────────────────────────

type mdTestIn struct{ Key string }

var mdTestInCodec = codex.Struct[mdTestIn](
	codex.RequiredField("key", codex.String().Refine(validate.NonEmptyString),
		func(in mdTestIn) string { return in.Key },
		func(in *mdTestIn, v string) { in.Key = v },
	),
)

type mdTestOut struct{ Value string }

var mdTestOutCodec = codex.Struct[mdTestOut](
	codex.RequiredField("value", codex.String().Refine(validate.NonEmptyString),
		func(out mdTestOut) string { return out.Value },
		func(out *mdTestOut, v string) { out.Value = v },
	),
)

func newTestDeclaration(name string) middleware.Declaration[mdTestIn, mdTestOut] {
	return middleware.NewDeclaration(name, mdTestInCodec, mdTestOutCodec)
}

// ── events.Middleware[In,Out] construction ───────────────────────────────

func TestNewMiddleware_BuildsExpectedShape(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("test-policy"))
	if mw.Name != "test-policy" {
		t.Errorf("want Name %q, got %q", "test-policy", mw.Name)
	}
	var _ middleware.RouteMiddleware = mw // compiles: RouteMiddlewareMarker is exported
}

// ── Transform: happy path, enrichment ────────────────────────────────────

func TestTransform_HappyPath_EnrichesMsg(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("region-policy"))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		msg.Name = msg.Name + "-enriched"
		return nil
	})
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Errorf("want exactly one MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
}

// ── ClientTransform: registers a ClientMiddlewareHandler ─────────────────

func TestClientTransform_PopulatesClientMiddlewareHandlers(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("region-policy"))
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"})
	publisher = events.ClientTransform(publisher, mw, func(ctx context.Context, msg userEvent) (mdTestOut, error) {
		return mdTestOut{Value: "region-1"}, nil
	})
	h, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Errorf("want exactly one ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}
}

// ── D6(b): duplicate middleware name on ONE channel is rejected ─────────

func TestSubscriberHandle_DuplicateMiddlewareNameRejected(t *testing.T) {
	mwA := events.NewMiddleware(newTestDeclaration("dup-policy"))
	mwB := events.NewMiddleware(newTestDeclaration("dup-policy")) // SAME name
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber = events.Transform(subscriber, mwA, fn)
	subscriber = events.Transform(subscriber, mwB, fn)

	_, err := subscriber.Handle(nil)
	var dupErr events.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
	if dupErr.Name != "dup-policy" {
		t.Errorf("want Name %q, got %q", "dup-policy", dupErr.Name)
	}
}

// ── D7: combining bundled + bound attachment on ONE value is rejected ──

func TestSubscriberHandle_AmbiguousDualAttachmentRejected(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("dual-policy")).
		WithReceive(func(ctx context.Context, in mdTestIn) error { return nil })
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		return nil
	})

	_, err := subscriber.Handle(nil)
	var ambErr events.AmbiguousMiddlewareAttachmentError
	if !errors.As(err, &ambErr) {
		t.Fatalf("want AmbiguousMiddlewareAttachmentError, got %v", err)
	}
	if ambErr.Name != "dual-policy" {
		t.Errorf("want Name %q, got %q", "dual-policy", ambErr.Name)
	}
}

func TestSubscriberHandle_SingleAttachmentStyleSucceeds(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("single-policy"))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		return nil
	})
	if _, err := subscriber.Handle(nil); err != nil {
		t.Fatalf("want successful Handle, got %v", err)
	}
}

// ── Channel-agnostic .Use(mw) reuse across two different channels ──────

func TestUse_AgnosticMiddleware_DispatchesOnBothChannels(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("agnostic-policy")).
		WithReceive(func(ctx context.Context, in mdTestIn) error { return nil })

	subA := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "A"}).Use(mw)
	hA, err := subA.Handle(nil)
	if err != nil {
		t.Fatalf("channel A Handle: %v", err)
	}
	if len(hA.MiddlewareHandlers) != 1 || !hA.MiddlewareHandlers[0].Agnostic {
		t.Errorf("channel A: want exactly one Agnostic MiddlewareHandler, got %+v", hA.MiddlewareHandlers)
	}

	type otherEvent struct{ Foo string }
	otherCodec := codex.Struct[otherEvent](
		codex.RequiredField("foo", codex.String(),
			func(e otherEvent) string { return e.Foo },
			func(e *otherEvent, v string) { e.Foo = v },
		),
	)
	subB := events.NewChannel[otherEvent]("other/created", otherCodec).
		WithSubscribe(events.Subscribe{Summary: "B"}).Use(mw)
	hB, err := subB.Handle(nil)
	if err != nil {
		t.Fatalf("channel B Handle: %v", err)
	}
	if len(hB.MiddlewareHandlers) != 1 || !hB.MiddlewareHandlers[0].Agnostic {
		t.Errorf("channel B: want exactly one Agnostic MiddlewareHandler, got %+v", hB.MiddlewareHandlers)
	}
}

// ── structured errors: construction + Error()/Unwrap()/LogValue() ───────

func TestMiddlewareInputError(t *testing.T) {
	inner := errors.New("boom")
	err := events.MiddlewareInputError{Name: "region-policy", Err: inner}
	if !errors.Is(err, inner) {
		t.Errorf("want errors.Is to match the wrapped error")
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestMiddlewareError(t *testing.T) {
	inner := errors.New("invalid region")
	err := events.MiddlewareError{Name: "region-policy", Err: inner}
	if !errors.Is(err, inner) {
		t.Errorf("want errors.Is to match the wrapped error")
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestDuplicateMiddlewareNameError(t *testing.T) {
	err := events.DuplicateMiddlewareNameError{Topic: "user/created", Name: "dup-policy"}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestAmbiguousMiddlewareAttachmentError(t *testing.T) {
	err := events.AmbiguousMiddlewareAttachmentError{Name: "dual-policy"}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestConflictingParamContributionError(t *testing.T) {
	err := events.ConflictingParamContributionError{
		Topic: "user/created", ParamName: "tenantID",
		FirstSource: "policy-a", SecondSource: "policy-b",
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

// ── property axis conflict detection (Phase 0, Round 9/11/15) ───────────

func tenantPropertyParam(required bool) events.MergedPropertyParam[mdTestIn] {
	if required {
		return events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v })
	}
	return events.NewOptionalPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
		func(in mdTestIn) string { return in.Key },
		func(in *mdTestIn, v string) { in.Key = v })
}

func TestChannel_Register_ConflictingParamContributionError(t *testing.T) {
	mwA := events.NewMiddleware(newTestDeclaration("policy-a")).
		WithSubscribeProperty(tenantPropertyParam(true))
	// Force a genuine codec mismatch: policy-b declares tenantID with a
	// DIFFERENT codec schema (plain String, no NonEmptyString refinement).
	mwB := events.NewMiddleware(newTestDeclaration("policy-b")).
		WithSubscribeProperty(events.NewPropertyParam("tenantID", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v }))

	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mwA, fn)
	subscriber = events.Transform(subscriber, mwB, fn)

	_, err := subscriber.Handle(nil)
	var confErr events.ConflictingParamContributionError
	if !errors.As(err, &confErr) {
		t.Fatalf("want ConflictingParamContributionError, got %v", err)
	}
	if confErr.ParamName != "tenantID" {
		t.Errorf("want ParamName %q, got %q", "tenantID", confErr.ParamName)
	}
}

func TestChannel_Register_RequiredVsOptionalPropertyMismatchError(t *testing.T) {
	mwA := events.NewMiddleware(newTestDeclaration("policy-a")).
		WithSubscribeProperty(tenantPropertyParam(true))
	mwB := events.NewMiddleware(newTestDeclaration("policy-b")).
		WithSubscribeProperty(tenantPropertyParam(false))

	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mwA, fn)
	subscriber = events.Transform(subscriber, mwB, fn)

	_, err := subscriber.Handle(nil)
	var confErr events.ConflictingParamContributionError
	if !errors.As(err, &confErr) {
		t.Fatalf("want ConflictingParamContributionError, got %v", err)
	}
}

func TestChannel_Register_AgreeingParamContributions_DedupeWithoutError(t *testing.T) {
	mwA := events.NewMiddleware(newTestDeclaration("policy-a")).
		WithSubscribeProperty(tenantPropertyParam(true))
	mwB := events.NewMiddleware(newTestDeclaration("policy-b")).
		WithSubscribeProperty(tenantPropertyParam(true))

	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mwA, fn)
	subscriber = events.Transform(subscriber, mwB, fn)

	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("want no conflict for agreeing declarations, got %v", err)
	}
	if h.Descriptor.Subscribe.Message.Headers.Properties == nil {
		t.Fatal("want a rendered headers schema")
	}
	count := 0
	for _, p := range h.Descriptor.Subscribe.Message.Headers.Properties {
		if p.Name == "tenantID" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want tenantID deduped to ONE spec entry, got %d", count)
	}
}

func TestChannel_Register_TopicAndPropertySameName_NoConflict(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("policy-a")).
		WithSubscribeProperty(tenantPropertyParam(true))

	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber := events.NewChannel[userEvent](
		"user/{tenantID}/created", userEventCodec,
		events.NewTopicParam("tenantID", codex.String(),
			func(e userEvent) string { return e.ID },
			func(e *userEvent, v string) { e.ID = v }),
	).WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, fn)

	if _, err := subscriber.Handle(nil); err != nil {
		t.Fatalf("want no conflict between independent topic/property namespaces, got %v", err)
	}
}

func TestChannel_Register_OptionalProperty_NotInSchemaRequiredList(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("policy-a")).
		WithSubscribeProperty(tenantPropertyParam(false))
	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, fn)

	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	for _, name := range h.Descriptor.Subscribe.Message.Headers.Required {
		if name == "tenantID" {
			t.Fatalf("want optional property NOT in Required list, got %v", h.Descriptor.Subscribe.Message.Headers.Required)
		}
	}
}

func TestChannel_Register_RequiredProperty_InSchemaRequiredList(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("policy-a")).
		WithSubscribeProperty(tenantPropertyParam(true))
	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) error { return nil }
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mw, fn)

	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	found := false
	for _, name := range h.Descriptor.Subscribe.Message.Headers.Required {
		if name == "tenantID" {
			found = true
		}
	}
	if !found {
		t.Errorf("want required property IN Required list, got %v", h.Descriptor.Subscribe.Message.Headers.Required)
	}
}

// ── Round 12: multiple attachments accumulate, in registration order ────

func TestSubscriber_MultipleTransformAttachments_DispatchInRegistrationOrder(t *testing.T) {
	mwFirst := events.NewMiddleware(newTestDeclaration("first-policy"))
	mwSecond := events.NewMiddleware(newTestDeclaration("second-policy"))

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mwFirst, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		msg.Name = "first"
		return nil
	})
	subscriber = events.Transform(subscriber, mwSecond, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		msg.Name = "second"
		return nil
	})

	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 2 {
		t.Fatalf("want 2 accumulated MiddlewareHandlers, got %d", len(h.MiddlewareHandlers))
	}
	if h.MiddlewareHandlers[0].Name != "first-policy" || h.MiddlewareHandlers[1].Name != "second-policy" {
		t.Errorf("want registration order [first-policy, second-policy], got [%s, %s]",
			h.MiddlewareHandlers[0].Name, h.MiddlewareHandlers[1].Name)
	}
}

// ── D6(c): two middlewares enriching the SAME *T field — last-applied wins ──

func TestTransform_D6c_TwoMiddlewaresEnrichSameMsgField_LastAppliedWins(t *testing.T) {
	// propOptInCodec (no required fields) avoids mdTestInCodec's own
	// NonEmptyString constraint tripping on a zero-value In — only
	// attachment-order/last-write matters for this test.
	decl := middleware.NewDeclaration("first-policy", propOptInCodec, mdTestOutCodec)
	decl2 := middleware.NewDeclaration("second-policy", propOptInCodec, mdTestOutCodec)
	mwFirst := events.NewMiddleware(decl)
	mwSecond := events.NewMiddleware(decl2)

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = events.Transform(subscriber, mwFirst, func(ctx context.Context, msg *userEvent, in propOptIn) error {
		msg.Name = "first"
		return nil
	})
	subscriber = events.Transform(subscriber, mwSecond, func(ctx context.Context, msg *userEvent, in propOptIn) error {
		msg.Name = "second"
		return nil
	})
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	msg := userEvent{ID: "1", Name: "Alice"}
	for _, mh := range h.MiddlewareHandlers {
		in, err := mh.DecodeIn(nil, nil)
		if err != nil {
			t.Fatalf("DecodeIn: %v", err)
		}
		fn := mh.Fn.(func(context.Context, *userEvent, propOptIn) error)
		if err := fn(context.Background(), &msg, in.(propOptIn)); err != nil {
			t.Fatalf("fn: %v", err)
		}
	}
	if msg.Name != "second" {
		t.Errorf("want last-attached middleware's write to win (%q), got %q", "second", msg.Name)
	}
}

// ── Round 13: reuse patterns ──────────────────────────────────────────────

func TestSubscriber_Use_SameMiddlewareValue_ReusedAcrossDifferentTTypes(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("reusable-policy")).
		WithReceive(func(ctx context.Context, in mdTestIn) error { return nil })

	subA := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "A"}).Use(mw)
	hA, err := subA.Handle(nil)
	if err != nil {
		t.Fatalf("channel A Handle: %v", err)
	}
	if len(hA.MiddlewareHandlers) != 1 {
		t.Fatalf("channel A: want 1 MiddlewareHandler, got %d", len(hA.MiddlewareHandlers))
	}

	type otherEvent struct{ Foo string }
	otherCodec := codex.Struct[otherEvent](
		codex.RequiredField("foo", codex.String(),
			func(e otherEvent) string { return e.Foo },
			func(e *otherEvent, v string) { e.Foo = v },
		),
	)
	subB := events.NewChannel[otherEvent]("other/created", otherCodec).
		WithSubscribe(events.Subscribe{Summary: "B"}).Use(mw)
	hB, err := subB.Handle(nil)
	if err != nil {
		t.Fatalf("channel B Handle: %v", err)
	}
	if len(hB.MiddlewareHandlers) != 1 {
		t.Errorf("channel B: want 1 MiddlewareHandler (SAME mw value, DIFFERENT T), got %d", len(hB.MiddlewareHandlers))
	}
}

func TestMiddleware_Use_SameValue_ReusedAcrossSubscriberAndPublisher(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("dual-role-policy")).
		WithReceive(func(ctx context.Context, in mdTestIn) error { return nil }).
		WithSend(func(ctx context.Context) (mdTestOut, error) { return mdTestOut{Value: "v"}, nil })

	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "sub"}).Use(mw)
	hSub, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Subscriber Handle: %v", err)
	}
	if len(hSub.MiddlewareHandlers) != 1 || !hSub.MiddlewareHandlers[0].Agnostic {
		t.Errorf("want 1 agnostic MiddlewareHandler (receiveFn half), got %+v", hSub.MiddlewareHandlers)
	}
	if len(hSub.ClientMiddlewareHandlers) != 0 {
		t.Errorf("want ZERO ClientMiddlewareHandlers on the Subscriber side, got %d", len(hSub.ClientMiddlewareHandlers))
	}

	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "pub"}).Use(mw)
	hPub, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Publisher Handle: %v", err)
	}
	if len(hPub.ClientMiddlewareHandlers) != 1 || !hPub.ClientMiddlewareHandlers[0].Agnostic {
		t.Errorf("want 1 agnostic ClientMiddlewareHandler (sendFn half), got %+v", hPub.ClientMiddlewareHandlers)
	}
	if len(hPub.MiddlewareHandlers) != 0 {
		t.Errorf("want ZERO MiddlewareHandlers on the Publisher side, got %d", len(hPub.MiddlewareHandlers))
	}
}
