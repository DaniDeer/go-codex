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
