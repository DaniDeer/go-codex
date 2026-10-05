package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── docs/design/d-0003-codec-declared-middlewares.md's Addendum 7: BoundSubscribeMiddleware[T,
// In, Out]/BoundPublishMiddleware[T, In, Out] are the explicit, compile-
// time-distinct channel-BOUND class for events — attached via
// Subscriber.SubscribeBoundMW/Publisher.PublishBoundMW. These tests mirror
// api/rest's handlemw_bound_test.go scenarios, which previously had NO
// events-side equivalent at all (a confirmed test-coverage gap found
// during a dedicated Phase B critical-review round: the boundReqWitness
// discriminator — the mechanism's own core safety guarantee — had ZERO
// regression coverage).

// otherEventT is a SECOND payload type, deliberately different from
// userEvent, used by the mismatch tests below. No dedicated codec is
// declared for it — every test constructing a
// BoundSubscribeMiddleware[otherEventT, ...]/BoundPublishMiddleware[otherEventT, ...]
// value below does so purely to exercise the boundReqWitness mismatch
// path, never a real Handle()/dispatch round-trip that would need one.
type otherEventT struct{ Foo string }

// TestSubscribeBoundMW_TMismatch_ReturnsTypedError confirms a
// BoundSubscribeMiddleware[otherEventT, ...] (built for the WRONG T)
// attached to a Subscriber[userEvent] is rejected via
// BoundMiddlewareReqMismatchError at Handle time — never a panic, never
// silently dropped — exercising the boundReqWitness discriminator
// directly (ported proactively from REST's own Phase A Finding Q
// specifically to make this detection possible).
func TestSubscribeBoundMW_TMismatch_ReturnsTypedError(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("mismatch-policy"),
		func(ctx context.Context, msg *otherEventT, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"}).
		SubscribeBoundMW(bm)

	_, err := sub.Handle(nil)
	var mismatchErr events.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	if mismatchErr.Name != "mismatch-policy" {
		t.Errorf("want Name %q, got %q", "mismatch-policy", mismatchErr.Name)
	}
	// LogValue must not panic.
	_ = mismatchErr.LogValue()
}

// TestPublishBoundMW_TMismatch_ReturnsTypedError mirrors
// TestSubscribeBoundMW_TMismatch_ReturnsTypedError for the publish
// (SENDING) role.
func TestPublishBoundMW_TMismatch_ReturnsTypedError(t *testing.T) {
	bm := events.NewBoundPublishMiddleware(newTestDeclaration("mismatch-policy-pub"),
		func(ctx context.Context, msg otherEventT) (mdTestOut, error) {
			return mdTestOut{Value: "v"}, nil
		})

	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"}).
		PublishBoundMW(bm)

	_, err := pub.Handle(nil)
	var mismatchErr events.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	if mismatchErr.Name != "mismatch-policy-pub" {
		t.Errorf("want Name %q, got %q", "mismatch-policy-pub", mismatchErr.Name)
	}
	_ = mismatchErr.LogValue()
}

// TestSubscribeBoundMW_PlainMiddleware_ReturnsTypedError confirms passing
// a PLAIN Middleware[In,Out] (never a BoundSubscribeMiddleware at all,
// not merely the wrong T) to SubscribeBoundMW is rejected via
// BoundMiddlewareReqMismatchError — never a panic, never a silent no-op.
func TestSubscribeBoundMW_PlainMiddleware_ReturnsTypedError(t *testing.T) {
	plainMw := events.SecurityMiddleware[mdTestIn, mdTestOut]("bearerAuth",
		events.SecurityScheme{}, nil).
		WithReceive(func(ctx context.Context, in mdTestIn) error { return nil })

	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"}).
		SubscribeBoundMW(plainMw)

	_, err := sub.Handle(nil)
	var mismatchErr events.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	// LogValue must not panic even though Got is the wrong CLASS (not
	// just the wrong T).
	_ = mismatchErr.LogValue()
}

// TestSubscribeBoundMW_NilBm_ReturnsTypedError confirms
// SubscribeBoundMW(nil) returns a typed error (not a panic) at Handle
// time, AND that the returned error's LogValue() does not panic on a nil
// Got — mirrors REST's identical regression guard for the confirmed
// reflect.TypeOf(nil).String() crash class.
func TestSubscribeBoundMW_NilBm_ReturnsTypedError(t *testing.T) {
	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"}).
		SubscribeBoundMW(nil)

	_, err := sub.Handle(nil)
	var mismatchErr events.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	// Must not panic.
	_ = mismatchErr.LogValue()
}

// TestPublishBoundMW_NilBm_ReturnsTypedError mirrors
// TestSubscribeBoundMW_NilBm_ReturnsTypedError for the publish role.
func TestPublishBoundMW_NilBm_ReturnsTypedError(t *testing.T) {
	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"}).
		PublishBoundMW(nil)

	_, err := pub.Handle(nil)
	var mismatchErr events.BoundMiddlewareReqMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want BoundMiddlewareReqMismatchError, got %v", err)
	}
	_ = mismatchErr.LogValue()
}

// TestBoundSubscribeMiddleware_SharedAcrossMultipleChannels_SameT
// confirms a capability the roadmap doc explicitly claims is preserved:
// ONE BoundSubscribeMiddleware[T,...] value (built once) attaches cleanly
// to MULTIPLE DIFFERENT channels sharing that SAME T — the bound class's
// T-parameterization was never meant to force a fresh construction per
// CHANNEL, only per distinct T TYPE.
func TestBoundSubscribeMiddleware_SharedAcrossMultipleChannels_SameT(t *testing.T) {
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("shared-policy"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
			return mdTestOut{Value: in.Key}, nil
		})

	subA := events.NewChannel[userEvent]("user/created-a", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "A"}).
		SubscribeBoundMW(bm)
	subB := events.NewChannel[userEvent]("user/created-b", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "B"}).
		SubscribeBoundMW(bm)

	hA, errA := subA.Handle(nil)
	if errA != nil {
		t.Fatalf("channel A Handle: %v", errA)
	}
	hB, errB := subB.Handle(nil)
	if errB != nil {
		t.Fatalf("channel B Handle: %v", errB)
	}
	if len(hA.MiddlewareHandlers) != 1 || len(hB.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler on each channel, got %d / %d", len(hA.MiddlewareHandlers), len(hB.MiddlewareHandlers))
	}
}

// TestSubscribeBoundMW_StackedWithUse_BothDispatch confirms a reusable
// Middleware (attached via .Use()) and a BoundSubscribeMiddleware
// (attached via SubscribeBoundMW) can be STACKED on ONE Subscriber,
// composing rather than conflicting — declaration order is dispatch
// order.
func TestSubscribeBoundMW_StackedWithUse_BothDispatch(t *testing.T) {
	var order []string

	// propOptIn/propOptInCodec (transform_test.go, same package) has NO
	// required fields — avoids mdTestInCodec's own NonEmptyString
	// constraint tripping on a zero-value In when no merge fields are
	// declared (irrelevant to what this test actually checks: dispatch
	// ORDER, not decoded content).
	genericDecl := middleware.NewDeclaration("generic-policy", propOptInCodec, mdTestOutCodec)
	specificDecl := middleware.NewDeclaration("specific-policy", propOptInCodec, mdTestOutCodec)

	reusable := events.NewMiddleware(genericDecl).
		WithReceive(func(ctx context.Context, in propOptIn) error {
			order = append(order, "generic")
			return nil
		})
	bound := events.NewBoundSubscribeMiddleware(specificDecl,
		func(ctx context.Context, msg *userEvent, in propOptIn) (mdTestOut, error) {
			order = append(order, "specific")
			// mdTestOutCodec requires a non-empty Value — ValidateOut
			// enforces this even though this test never inspects the
			// returned Out itself.
			return mdTestOut{Value: "unused"}, nil
		})

	sub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "User created"}).
		Use(reusable).
		SubscribeBoundMW(bound)

	h, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.MiddlewareHandlers) != 2 {
		t.Fatalf("want 2 MiddlewareHandlers (stacked), got %d", len(h.MiddlewareHandlers))
	}

	msg := userEvent{ID: "1", Name: "Alice"}
	_, err = events.DispatchSubscribeMiddlewareHandlers(context.Background(), &msg, h.MiddlewareHandlers, nil, nil)
	if err != nil {
		t.Fatalf("DispatchSubscribeMiddlewareHandlers: %v", err)
	}
	if len(order) != 2 || order[0] != "generic" || order[1] != "specific" {
		t.Errorf("want dispatch order [generic, specific], got %v", order)
	}
}

// TestPublishMW_PlainMiddleware_ReturnsTypedError is PublishMW's
// misattachment-rejection test — the publish-side parity test
// TestSubscriberHandle_CodecBackedMiddlewarePassedToSubscribeMW_Rejected
// (middleware_declaration_test.go) already covers for the subscribe
// side, confirmed missing here until now.
func TestPublishMW_PlainMiddleware_ReturnsTypedError(t *testing.T) {
	mw := events.NewMiddleware(newTestDeclaration("dual-policy-pub")).
		WithSend(func(ctx context.Context) (mdTestOut, error) { return mdTestOut{}, nil })

	pub := events.NewChannel[userEvent]("user/created", userEventCodec).
		WithPublish(events.Publish{Summary: "User created"}).
		PublishMW(mw, func(ctx context.Context, msg userEvent) (mdTestOut, error) {
			return mdTestOut{}, nil
		})

	_, err := pub.Handle(nil)
	var misErr events.MiddlewareMisattachedError
	if !errors.As(err, &misErr) {
		t.Fatalf("want MiddlewareMisattachedError, got %v", err)
	}
	if misErr.Name != "dual-policy-pub" {
		t.Errorf("want Name %q, got %q", "dual-policy-pub", misErr.Name)
	}
}

// TestSubscribeBoundMWPlusPublishBoundMW_SameScheme_IndependentValues_NoConflict
// confirms [BoundSubscribeMiddleware.applyBoundSubscriber]'s own doc
// comment claim: a Security-carrying BoundSubscribeMiddleware (subscribe)
// and a BoundPublishMiddleware (publish) for the SAME scheme name are
// attached to INDEPENDENT [Subscriber]/[Publisher] values to begin with
// (unlike REST, where HandleBoundMW+ClientBoundMW on ONE shared route
// value conflicts via DuplicateMiddlewareNameError) — this was asserted
// in godoc but never regression-tested until now.
func TestSubscribeBoundMWPlusPublishBoundMW_SameScheme_IndependentValues_NoConflict(t *testing.T) {
	bmSub := events.BoundSecuritySubscribeMiddleware[userEvent, mdTestIn, mdTestOut]("sharedScheme",
		events.SecurityScheme{}, nil,
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) { return mdTestOut{}, nil })
	bmPub := events.BoundSecurityPublishMiddleware[userEvent, mdTestIn, mdTestOut]("sharedScheme",
		events.SecurityScheme{}, nil,
		func(ctx context.Context, msg userEvent) (mdTestOut, error) { return mdTestOut{Value: "v"}, nil })

	channel := events.NewChannel[userEvent]("user/created", userEventCodec)
	sub := channel.WithSubscribe(events.Subscribe{Summary: "sub"}).SubscribeBoundMW(bmSub)
	pub := channel.WithPublish(events.Publish{Summary: "pub"}).PublishBoundMW(bmPub)

	hSub, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Subscriber Handle: %v", err)
	}
	if len(hSub.MiddlewareHandlers) != 1 {
		t.Fatalf("want 1 MiddlewareHandler, got %d", len(hSub.MiddlewareHandlers))
	}
	hPub, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Publisher Handle: %v", err)
	}
	if len(hPub.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want 1 ClientMiddlewareHandler, got %d", len(hPub.ClientMiddlewareHandlers))
	}
}
