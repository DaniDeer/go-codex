package events_test

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase B,
// Phase 3: Middleware.SetContextFieldFromIn/SetContextFieldFromOut —
// events' mirror of adapters/nethttp/contextfield_dispatch_test.go's
// dispatch-order tests. Confirmed design-closure Decision 1 (no
// ContextField/ContextFieldSetter implementation changes needed, only new
// dispatch call sites) applies identically here — these tests exercise
// that claim directly via DispatchSubscribeMiddlewareHandlers/
// DispatchPublishMiddlewareHandlers.

var cfEventsUserIDField = middleware.NewContextField(codex.String())

// TestSetContextFieldFromIn_DispatchOrder_BeforeFn proves the field is
// published DURING DecodeIn — before the attached middleware's own Fn
// runs (events' Subscribe side has no separate "handler" stage distinct
// from Fn, unlike REST — the Fn itself IS the subscribe business logic).
func TestSetContextFieldFromIn_DispatchOrder_BeforeFn(t *testing.T) {
	var fnSawUserID string
	bm := events.NewBoundSubscribeMiddleware(newTestDeclaration("userid-policy"),
		func(ctx context.Context, msg *userEvent, in mdTestIn) (mdTestOut, error) {
			fnSawUserID, _ = cfEventsUserIDField.Get(ctx)
			// mdTestOutCodec requires a non-empty Value — this Fn's
			// return value is never consulted by this test (ValidateOut
			// just needs to pass), unlike TestSetContextFieldFromOut_*
			// below, where the OTHER test's Out value IS the thing under
			// test.
			return mdTestOut{Value: "unused"}, nil
		}).
		WithSubscribeTopic(events.NewTopicParam("userID", codex.String(),
			func(in mdTestIn) string { return in.Key },
			func(in *mdTestIn, v string) { in.Key = v },
		)).
		SetContextFieldFromIn(cfEventsUserIDField, func(in mdTestIn) any { return in.Key })

	sub := events.NewChannel[userEvent]("user/{userID}/created", userEventCodec).
		WithSubscribe(events.Subscribe{Summary: "sub"}).
		SubscribeBoundMW(bm)
	h, err := sub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx := middleware.EnsureContextFields(context.Background())
	msg := userEvent{}
	if _, err := events.DispatchSubscribeMiddlewareHandlers(ctx, &msg, h.MiddlewareHandlers, map[string]string{"userID": "acme-corp"}, nil); err != nil {
		t.Fatalf("DispatchSubscribeMiddlewareHandlers: %v", err)
	}
	if fnSawUserID != "acme-corp" {
		t.Errorf("want Fn to see userID %q, got %q", "acme-corp", fnSawUserID)
	}
	got, _ := cfEventsUserIDField.Get(ctx)
	if got != "acme-corp" {
		t.Errorf("want ctx to carry userID %q after dispatch, got %q", "acme-corp", got)
	}
}

// TestSetContextFieldFromOut_DispatchOrder_AfterFnReturns proves the Out
// side publishes from the middleware's OWN returned Out value, available
// AFTER Fn returns (Publish side — the only side with an Out to source a
// value from).
func TestSetContextFieldFromOut_DispatchOrder_AfterFnReturns(t *testing.T) {
	versionField := middleware.NewContextField(codex.String())
	bm := events.NewBoundPublishMiddleware(newTestDeclaration("version-policy"),
		func(ctx context.Context, msg userEvent) (mdTestOut, error) {
			return mdTestOut{Value: "v2"}, nil
		}).
		WithPublishTopic(events.NewTopicParam("version", codex.String(),
			func(out mdTestOut) string { return out.Value },
			func(out *mdTestOut, v string) { out.Value = v },
		)).
		SetContextFieldFromOut(versionField, func(out mdTestOut) any { return out.Value })

	pub := events.NewChannel[userEvent]("user/{version}/created", userEventCodec).
		WithPublish(events.Publish{Summary: "pub"}).
		PublishBoundMW(bm)
	h, err := pub.Handle(nil)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx := middleware.EnsureContextFields(context.Background())
	_, _, err = events.DispatchPublishMiddlewareHandlers(ctx, userEvent{}, h.ClientMiddlewareHandlers)
	if err != nil {
		t.Fatalf("DispatchPublishMiddlewareHandlers: %v", err)
	}
	got, ok := versionField.Get(ctx)
	if !ok || got != "v2" {
		t.Errorf("want (%q, true) after dispatch, got (%q, %v)", "v2", got, ok)
	}
}
