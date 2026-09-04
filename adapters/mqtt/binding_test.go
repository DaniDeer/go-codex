package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── SubscribeAdapter ──────────────────────────────────────────────────────────

func TestSubscribeAdapter_ValidPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newHandle()
	client := &mockClient{}

	p, err := ports.NewSourcePort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, SubscribeAdapter(client, handle, 0, format.JSON(userEventCodec), SubscribeAdapterOptions{}))
	s := p.Stream(ctx)

	deadline := time.Now().Add(200 * time.Millisecond)
	for client.subscribedHandlerSnapshot() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	handler := client.subscribedHandlerSnapshot()
	if handler == nil {
		t.Fatal("timeout waiting for Subscribe to register a handler")
	}
	handler(client, &mockMessage{payload: []byte(validPayload)})
	cancel()

	vals, errs := gstream.Collect(context.Background(), s)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 1 {
		t.Errorf("want 1 value, got %d", len(vals))
	}
}

// TestSubscribeAdapter_AutoDerivesWildcardFilter verifies that when
// TopicFilter is empty and the handle's topic has {var} placeholders, the
// adapter subscribes using the derived MQTT wildcard filter
// ("users/{userID}/events" -> "users/+/events") instead of the raw,
// brace-containing topic string.
func TestSubscribeAdapter_AutoDerivesWildcardFilter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handle := newTemplateHandle() // topic: "users/{userID}/events"
	client := &mockClient{}

	p, err := ports.NewSourcePort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, SubscribeAdapter(client, handle, 0, format.JSON(userEventCodec), SubscribeAdapterOptions{}))
	p.Stream(ctx)

	deadline := time.Now().Add(200 * time.Millisecond)
	for client.subscribedTopicSnapshot() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := client.subscribedTopicSnapshot(); got != "users/+/events" {
		t.Errorf("want derived wildcard filter %q, got %q", "users/+/events", got)
	}
}

// ── PublishAdapter ────────────────────────────────────────────────────────────

func TestPublishAdapter_PublishesEachItem(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newHandle()

	ch := make(chan userEvent, 1)
	ch <- userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	close(ch)

	p, err := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec), MQTTDrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	topics := client.publishedTopicsSnapshot()
	if len(topics) != 1 || topics[0] != "user/created" {
		t.Errorf("want 1 publish to user/created, got %v", topics)
	}
}

// R1B-adoption-1: an upstream stream error matching a declared
// events.ErrorChannel pattern publishes the typed error payload to the
// declared error topic instead of calling OnError.
func TestPublishAdapter_ErrorChannelMatch_PublishesToDeclaredTopic(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.ErrorChannel[userValidationErr, userErrPayload](
			"user/created/errors", userErrPayloadCodec,
			func(e userValidationErr) (userErrPayload, error) {
				return userErrPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).WithPublish(events.Publish{Summary: "User created"}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan userEvent)
	errCh <- userValidationErr{msg: "out of range"}
	close(errCh)
	close(valCh)
	src := gstream.Stream[userEvent]{Values: valCh, Errors: errCh}

	var gotOnError error
	p, perr := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if perr != nil {
		t.Fatalf("construct port: %v", perr)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec),
		MQTTDrainPublishOptions{OnError: func(e error) { gotOnError = e }}))
	p.Feed(ctx, src)

	if gotOnError != nil {
		t.Errorf("want OnError NOT called on matched respond action, got %v", gotOnError)
	}
	topics := client.publishedTopicsSnapshot()
	if len(topics) != 1 || topics[0] != "user/created/errors" {
		t.Fatalf("want 1 publish to user/created/errors, got %v", topics)
	}
	var payload map[string]string
	if err := json.Unmarshal(client.publishedPayloadSnapshot(), &payload); err != nil {
		t.Fatalf("decode published payload: %v", err)
	}
	if payload["code"] != "validation" || payload["message"] != "out of range" {
		t.Errorf("unexpected published payload: %+v", payload)
	}
}

// R1B-adoption-2: an upstream stream error NOT matching any declared
// ErrorChannel pattern falls through to OnError unchanged.
func TestPublishAdapter_ErrorChannelNoMatch_FallsBackToOnError(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.ErrorChannel[userValidationErr, userErrPayload](
			"user/created/errors", userErrPayloadCodec,
			func(e userValidationErr) (userErrPayload, error) {
				return userErrPayload{Code: "validation", Message: e.msg}, nil
			},
		),
	).WithPublish(events.Publish{Summary: "User created"}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan userEvent)
	errCh <- fmt.Errorf("unrelated upstream error")
	close(errCh)
	close(valCh)
	src := gstream.Stream[userEvent]{Values: valCh, Errors: errCh}

	var gotOnError error
	p, perr := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if perr != nil {
		t.Fatalf("construct port: %v", perr)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec),
		MQTTDrainPublishOptions{OnError: func(e error) { gotOnError = e }}))
	p.Feed(ctx, src)

	if gotOnError == nil {
		t.Fatal("want unmatched error forwarded to OnError, got nil")
	}
	if topics := client.publishedTopicsSnapshot(); len(topics) != 0 {
		t.Errorf("want no publish for unmatched error, got %v", topics)
	}
}

// R1B-adoption-3: ErrorChannel declared with WithAction(events.ErrorHandle)
// does NOT auto-publish — OnError still runs (one-action-only semantics).
func TestPublishAdapter_ErrorChannelHandleAction_NoAutoPublish(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[userEvent]("user/created", userEventCodec,
		events.ErrorChannel[userValidationErr, userErrPayload](
			"user/created/errors", userErrPayloadCodec,
			func(e userValidationErr) (userErrPayload, error) {
				return userErrPayload{Code: "validation", Message: e.msg}, nil
			},
		).WithAction(events.ErrorHandle),
	).WithPublish(events.Publish{Summary: "User created"}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	errCh := make(chan error, 1)
	valCh := make(chan userEvent)
	errCh <- userValidationErr{msg: "x"}
	close(errCh)
	close(valCh)
	src := gstream.Stream[userEvent]{Values: valCh, Errors: errCh}

	var gotOnError error
	p, perr := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if perr != nil {
		t.Fatalf("construct port: %v", perr)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec),
		MQTTDrainPublishOptions{OnError: func(e error) { gotOnError = e }}))
	p.Feed(ctx, src)

	if gotOnError == nil {
		t.Fatal("want OnError called for handle action, got nil")
	}
	if topics := client.publishedTopicsSnapshot(); len(topics) != 0 {
		t.Errorf("want no auto-publish for handle action, got %v", topics)
	}
}

type userValidationErr struct{ msg string }

func (e userValidationErr) Error() string { return "user validation: " + e.msg }

type userErrPayload struct {
	Code    string
	Message string
}

func (e userErrPayload) Error() string { return "user error " + e.Code }

var userErrPayloadCodec = codex.Struct[userErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e userErrPayload) string { return e.Code },
		func(e *userErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e userErrPayload) string { return e.Message },
		func(e *userErrPayload, v string) { e.Message = v },
	),
)

// G1-3 (mqtt v3): PublishAdapter derives topic vars PER-ITEM from each
// item's own merge fields when opts.Vars is nil — two items with different
// user IDs must publish to two different concrete topics.
func TestPublishAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newMergeHandle()

	ch := make(chan userEvent, 2)
	ch <- userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	ch <- userEvent{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "bob@example.com"}
	close(ch)

	p, err := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// opts.Vars left nil -> per-item derivation via PublishHandle.
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec), MQTTDrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	topics := client.publishedTopicsSnapshot()
	if len(topics) != 2 {
		t.Fatalf("want 2 published, got %d", len(topics))
	}
	if topics[0] != "users/f47ac10b-58cc-4372-a567-0e02b2c3d479/events" ||
		topics[1] != "users/550e8400-e29b-41d4-a716-446655440000/events" {
		t.Errorf("want per-item resolved topics, got %v", topics)
	}
}

// Explicit (non-nil) MQTTDrainPublishOptions.Vars still wins — regression guard.
func TestPublishAdapter_ExplicitVarsStillWins(t *testing.T) {
	ctx := context.Background()
	client := &mockClient{token: newCompletedToken(nil)}
	handle := newMergeHandle()

	ch := make(chan userEvent, 2)
	ch <- userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}
	ch <- userEvent{ID: "550e8400-e29b-41d4-a716-446655440000", Email: "bob@example.com"}
	close(ch)

	p, err := ports.NewSinkPort[userEvent]("test", userEventCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, PublishAdapter(client, handle, format.JSON(userEventCodec),
		MQTTDrainPublishOptions{Vars: map[string]string{"userID": "static-user"}}))
	p.Feed(ctx, gstream.From(ctx, ch))

	for _, topic := range client.publishedTopicsSnapshot() {
		if topic != "users/static-user/events" {
			t.Errorf("want static topic for every item, got %q", topic)
		}
	}
}
