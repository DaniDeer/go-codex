package events_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
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

// ── BoundSubscribeMiddleware: happy path, enrichment ─────────────────────

func TestBoundSubscribeMiddleware_HappyPath_EnrichesMsg(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("region-policy"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
			msg.Name = msg.Name + "-enriched"
			return mdTestOut{}, nil
		})
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bm)
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Errorf("want exactly one MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
}

// ── BoundPublishMiddleware: registers a ClientMiddlewareHandler ──────────

func TestBoundPublishMiddleware_PopulatesClientMiddlewareHandlers(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTestDeclaration("region-policy"),
		func(ctx context.Context, msg userEvent) (mdTestOut, error) {
			return mdTestOut{Value: "region-1"}, nil
		})
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"})
	publisher = publisher.PublishBoundMW(bm)
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
	fn := func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) { return mdTestOut{}, nil }
	bmA := events.NewBoundSubscribeMiddleware(newTestDeclaration("dup-policy"), fn)
	bmB := events.NewBoundSubscribeMiddleware(newTestDeclaration("dup-policy"), fn) // SAME name
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bmA)
	subscriber = subscriber.SubscribeBoundMW(bmB)

	_, err := subscriber.Handle(nil)
	var dupErr events.DuplicateMiddlewareNameError
	if !errors.As(err, &dupErr) {
		t.Fatalf("want DuplicateMiddlewareNameError, got %v", err)
	}
	if dupErr.Name != "dup-policy" {
		t.Errorf("want Name %q, got %q", "dup-policy", dupErr.Name)
	}
}

// ── A codec-backed Middleware passed to SubscribeMW is rejected (the
// legacy raw-adapter-Fn-pairing escape hatch is permanently closed) ─────

func TestSubscriberHandle_CodecBackedMiddlewarePassedToSubscribeMW_Rejected(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("dual-policy")).
		WithReceive(func(ctx context.Context, in mdTestIn) error { return nil })
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeMW(mw, func(ctx context.Context, msg *userEvent, in mdTestIn) error {
		return nil
	})

	_, err := subscriber.Handle(nil)
	var misErr events.MiddlewareMisattachedError
	if !errors.As(err, &misErr) {
		t.Fatalf("want MiddlewareMisattachedError, got %v", err)
	}
	if misErr.Name != "dual-policy" {
		t.Errorf("want Name %q, got %q", "dual-policy", misErr.Name)
	}
}

func TestSubscriberHandle_SingleAttachmentStyleSucceeds(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("single-policy"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) { return mdTestOut{}, nil })
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bm)
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

// TestMiddlewareOutputError verifies the OUTPUT-side counterpart of
// TestMiddlewareInputError above — added for symmetry (see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum 2).
func TestMiddlewareOutputError(t *testing.T) {
	inner := errors.New("boom")
	err := events.MiddlewareOutputError{Name: "region-policy", Err: inner}
	if !errors.Is(err, inner) {
		t.Errorf("want errors.Is to match the wrapped error")
	}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
	var target events.MiddlewareOutputError
	if !errors.As(err, &target) {
		t.Fatal("want errors.As to match MiddlewareOutputError")
	}
	if target.Name != "region-policy" {
		t.Errorf("want Name %q, got %q", "region-policy", target.Name)
	}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	seen := map[string]bool{}
	for _, a := range v.Group() {
		seen[a.Key] = true
	}
	for _, key := range []string{"name", "err"} {
		if !seen[key] {
			t.Errorf("want LogValue group to include key %q, got %v", key, v.Group())
		}
	}
}

func TestDuplicateMiddlewareNameError(t *testing.T) {
	err := events.DuplicateMiddlewareNameError{Topic: "user/created", Name: "dup-policy"}
	if err.Error() == "" {
		t.Error("want non-empty Error() message")
	}
}

func TestMiddlewareMisattachedError(t *testing.T) {
	err := events.MiddlewareMisattachedError{Topic: "user/created", Name: "dual-policy"}
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

func boundFn() func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
	return func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) { return mdTestOut{}, nil }
}

func TestChannel_Register_ConflictingParamContributionError(t *testing.T) {
	bmA := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-a"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(true))
	// Force a genuine codec mismatch: policy-b declares tenantID with a
	// DIFFERENT codec schema (plain String, no NonEmptyString refinement).
	bmB := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-b"), boundFn()).
		WithSubscribeProperty(events.NewPropertyParam("tenantID", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v }))

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bmA)
	subscriber = subscriber.SubscribeBoundMW(bmB)

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
	bmA := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-a"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(true))
	bmB := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-b"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(false))

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bmA)
	subscriber = subscriber.SubscribeBoundMW(bmB)

	_, err := subscriber.Handle(nil)
	var confErr events.ConflictingParamContributionError
	if !errors.As(err, &confErr) {
		t.Fatalf("want ConflictingParamContributionError, got %v", err)
	}
}

func TestChannel_Register_AgreeingParamContributions_DedupeWithoutError(t *testing.T) {
	bmA := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-a"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(true))
	bmB := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-b"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(true))

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bmA)
	subscriber = subscriber.SubscribeBoundMW(bmB)

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
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-a"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(true))

	subscriber := events.NewChannel[userEvent](
		"user/{tenantID}/created", userEventCodec,
		events.NewTopicParam("tenantID", codex.String(),
			func(e userEvent) string { return e.ID },
			func(e *userEvent, v string) { e.ID = v }),
	).WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bm)

	if _, err := subscriber.Handle(nil); err != nil {
		t.Fatalf("want no conflict between independent topic/property namespaces, got %v", err)
	}
}

func TestChannel_Register_OptionalProperty_NotInSchemaRequiredList(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-a"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(false))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bm)

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
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("policy-a"), boundFn()).
		WithSubscribeProperty(tenantPropertyParam(true))
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bm)

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

func TestSubscriber_MultipleBoundAttachments_DispatchInRegistrationOrder(t *testing.T) {
	bmFirst := events.NewBoundSubscribeMiddleware(newTestDeclaration("first-policy"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
			msg.Name = "first"
			return mdTestOut{}, nil
		})
	bmSecond := events.NewBoundSubscribeMiddleware(newTestDeclaration("second-policy"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
			msg.Name = "second"
			return mdTestOut{}, nil
		})

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bmFirst)
	subscriber = subscriber.SubscribeBoundMW(bmSecond)

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

func TestBoundSubscribeMiddleware_D6c_TwoMiddlewaresEnrichSameMsgField_LastAppliedWins(t *testing.T) {
	// propOptInCodec (no required fields) avoids mdTestInCodec's own
	// NonEmptyString constraint tripping on a zero-value In — only
	// attachment-order/last-write matters for this test.
	decl := middleware.NewDeclaration("first-policy", propOptInCodec, mdTestOutCodec)
	decl2 := middleware.NewDeclaration("second-policy", propOptInCodec, mdTestOutCodec)
	bmFirst := events.NewBoundSubscribeMiddleware(decl, func(ctx context.Context, msg *userEvent, in propOptIn) (mdTestOut, error) {
		msg.Name = "first"
		return mdTestOut{}, nil
	})
	bmSecond := events.NewBoundSubscribeMiddleware(decl2, func(ctx context.Context, msg *userEvent, in propOptIn) (mdTestOut, error) {
		msg.Name = "second"
		return mdTestOut{}, nil
	})

	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"})
	subscriber = subscriber.SubscribeBoundMW(bmFirst)
	subscriber = subscriber.SubscribeBoundMW(bmSecond)
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	msg := userEvent{ID: "1", Name: "Alice"}
	for _, mh := range h.MiddlewareHandlers {
		in, err := mh.DecodeIn(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("DecodeIn: %v", err)
		}
		fn := mh.Fn.(func(context.Context, *userEvent, propOptIn) (mdTestOut, error))
		if _, err := fn(context.Background(), &msg, in.(propOptIn)); err != nil {
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

// ── SubscribeBoundMW/PublishBoundMW (bound-middleware-split) ────────────

// TestSubscribeBoundMW_DispatchesAsNonAgnosticHandler proves
// SubscribeBoundMW reaches the channel-bound dispatch path (one
// MiddlewareHandler, NOT Agnostic).
func TestSubscribeBoundMW_DispatchesAsNonAgnosticHandler(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("region-policy-mw"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
			msg.Name = msg.Name + "-enriched"
			return mdTestOut{}, nil
		})
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"}).
		SubscribeBoundMW(bm)
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 1 {
		t.Fatalf("want exactly one MiddlewareHandler, got %d", len(h.MiddlewareHandlers))
	}
	if h.MiddlewareHandlers[0].Agnostic {
		t.Error("want a BOUND handler (Agnostic=false), got Agnostic=true")
	}
}

// TestPublishBoundMW_DispatchesAsNonAgnosticHandler mirrors the
// Subscribe-side test, for the publish (SENDING) role.
func TestPublishBoundMW_DispatchesAsNonAgnosticHandler(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTestDeclaration("region-policy-mw2"),
		func(ctx context.Context, msg userEvent) (mdTestOut, error) {
			return mdTestOut{Value: "region-1"}, nil
		})
	publisher := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"}).
		PublishBoundMW(bm)
	h, err := publisher.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want exactly one ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}
	if h.ClientMiddlewareHandlers[0].Agnostic {
		t.Error("want a BOUND handler (Agnostic=false), got Agnostic=true")
	}
}

// TestSubscribeMW_LegacyCredentialShape_StillDispatchesViaLegacyPath
// confirms SubscribeMW's rejection check (mw.(eventsMiddlewareContributor))
// correctly falls through to the UNCHANGED legacy path for a bare
// [middleware.Middleware] value, which does NOT implement
// eventsMiddlewareContributor — only a codec-backed [Middleware][In, Out]
// does.
func TestSubscribeMW_LegacyCredentialShape_StillDispatchesViaLegacyPath(t *testing.T) {
	legacyFn := func(ctx context.Context, msg *userEvent, reqs []route.SecurityRequirement) error { return nil }
	subscriber := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"}).
		SubscribeMW(middleware.Middleware{Name: "legacy"}, legacyFn)
	h, err := subscriber.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 0 {
		t.Errorf("want ZERO MiddlewareHandlers (legacy path uses Implementations), got %d", len(h.MiddlewareHandlers))
	}
	if len(h.Implementations) != 1 {
		t.Errorf("want exactly one legacy Implementation, got %d", len(h.Implementations))
	}
}

// TestBoundSecurityMiddleware_RealInOutType_DoesNotPanicOnDispatch is a
// REGRESSION GUARD mirroring rest.TestSecurityMiddleware_RealInType_
// DoesNotPanicOnDispatch exactly — confirms events.BoundSecuritySubscribeMiddleware/
// BoundSecurityPublishMiddleware's generalization applies the InCodec/
// OutCodec zero-value-codec fix FROM THE START (Phase A's own
// carried-forward learning), rather than rediscovering the panic via a
// real migration the way REST's did. Exercises the FULL dispatch path
// (DecodeIn on subscribe, EncodeOut on publish) for a REAL
// (non-struct{}) In/Out pair — must NOT panic.
func TestBoundSecurityMiddleware_RealInOutType_DoesNotPanicOnDispatch(t *testing.T) {
	bmSub := events.BoundSecuritySubscribeMiddleware[userEvent, mdTestIn, mdTestOut]("apiKeyAuth",
		events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-Api-Key", "header")}, nil,
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) { return mdTestOut{}, nil },
	)

	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "sub"}).
		SubscribeBoundMW(bmSub)
	hSub, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Subscriber Handle: %v", err)
	}
	if len(hSub.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler, got %d", len(hSub.MiddlewareHandlers))
	}
	// DecodeIn internally calls InCodec.Validate — must NOT panic for a
	// real (non-struct{}) In type.
	if _, err := hSub.MiddlewareHandlers[0].DecodeIn(context.Background(), nil, nil); err != nil {
		t.Fatalf("DecodeIn: %v", err)
	}

	bmPub := events.BoundSecurityPublishMiddleware[userEvent, mdTestIn, mdTestOut]("apiKeyAuth",
		events.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-Api-Key", "header")}, nil,
		func(ctx context.Context, msg userEvent) (mdTestOut, error) {
			return mdTestOut{Value: "v1"}, nil
		},
	)
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "pub"}).
		PublishBoundMW(bmPub)
	hPub, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Publisher Handle: %v", err)
	}
	if len(hPub.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want 1 ClientMiddlewareHandler, got %d", len(hPub.ClientMiddlewareHandlers))
	}
	// EncodeOut internally calls OutCodec.Validate — must NOT panic for a
	// real (non-struct{}) Out type.
	if _, _, err := hPub.ClientMiddlewareHandlers[0].EncodeOut(context.Background(), mdTestOut{Value: "v1"}); err != nil {
		t.Fatalf("EncodeOut: %v", err)
	}
}
