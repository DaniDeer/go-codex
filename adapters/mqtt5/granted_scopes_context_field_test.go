package mqtt5

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file is the automated-test mirror of
// examples/events-api/demo_granted_scopes_context_field.go — Phase 5's
// own "immediate, low-effort action item" (docs/roadmap/
// adapter-integration-architecture.md): the Client.Attach +
// ServeSubscribers + SubscribeBoundMW + GrantedScopes + ContextField
// combination is the EXACT entry point d-0007's regression was about,
// and was previously exercised only by a runnable example, never an
// automated test. Mirrors the example's scenario exactly (correct scope
// granted vs. known key with the WRONG scope granted), using local
// fixtures instead of the examples/events-api/auth+routes packages, so
// this package gains no new dependency on examples/.

type gsAuthIn struct{ Key string }

type gsAuthOut struct {
	GrantedScopes map[string][]string
}

// gsAPIKeyScopes mirrors the demo's mock credential store exactly: one
// key has the required "read:sensors" scope, the other deliberately
// lacks it (has "write:sensors" instead) to prove rejection.
var gsAPIKeyScopes = map[string][]string{
	"sensor-key-readonly":  {"read:sensors"},
	"sensor-key-writeonly": {"write:sensors"},
}

func verifyGSAPIKey(_ context.Context, _ *sensorReading, in gsAuthIn) (gsAuthOut, error) {
	scopes, ok := gsAPIKeyScopes[in.Key]
	if !ok {
		return gsAuthOut{}, fmt.Errorf("unknown or expired API key %q", in.Key)
	}
	return gsAuthOut{GrantedScopes: map[string][]string{"apiKeyGS": scopes}}, nil
}

// gsUserIDField is the events.ContextField[string] the authenticated API
// key is published through (via SetContextFieldFromIn), consumed by the
// real subscribe handler with ZERO manual re-decoding — mirrors the
// demo's GrantedScopesUserIDField exactly.
var gsUserIDField = events.NewContextField(codex.String())

func newGrantedScopesSensorMw(fn func(context.Context, *sensorReading, gsAuthIn) (gsAuthOut, error)) events.BoundSubscribeMiddleware[sensorReading, gsAuthIn, gsAuthOut] {
	return events.BoundSecuritySubscribeMiddleware[sensorReading, gsAuthIn, gsAuthOut]("apiKeyGS",
		events.APIKeyScheme("X-API-Key", "header"), []string{"read:sensors"},
		fn,
	).WithSubscribeProperty(events.NewPropertyParam("X-API-Key", codex.String(),
		func(in gsAuthIn) string { return in.Key },
		func(in *gsAuthIn, v string) { in.Key = v },
	)).SetContextFieldFromIn(gsUserIDField, func(in gsAuthIn) any { return in.Key })
}

// TestAttach_ServeSubscribers_GrantedScopesContextField_CorrectScope_Handled
// proves the full Client.Attach + ServeSubscribers + SubscribeBoundMW +
// GrantedScopes + ContextField pipeline: a correctly-scoped API key is
// accepted (events.CheckScopes passes against the declared
// "read:sensors" requirement), the handler runs, and it can read the
// authenticated key purely via gsUserIDField.Get(ctx) — zero manual
// re-decoding.
func TestAttach_ServeSubscribers_GrantedScopesContextField_CorrectScope_Handled(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch := events.NewChannel[sensorReading]("sensor/data-gs", sensorCodec)
	var rejected SubscribeError
	var gotKey string
	var gotOK bool
	var handlerCalled bool
	sub := ch.WithSubscribe(events.Subscribe{
		Security: []events.SecurityRequirement{events.Require("apiKeyGS", "read:sensors")},
	}).SubscribeBoundMW(newGrantedScopesSensorMw(verifyGSAPIKey)).
		WithHandler(func(ctx context.Context, _ sensorReading) error {
			handlerCalled = true
			gotKey, gotOK = gsUserIDField.Get(ctx)
			return nil
		}).
		WithOptions(SubscribeOptions{OnError: func(e SubscribeError) { rejected = e }})
	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- evtClient.ServeSubscribers(ctx) }()
	router.waitHandler("sensor/data-gs")

	pub := ch.WithPublish(events.Publish{}).WithOptions(PublishOptions[sensorReading]{
		UserProperties: []UserProperty{{Key: "X-API-Key", Value: "sensor-key-readonly"}},
	})
	if err := evtClient.Publish(ctx, pub, sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 19.5}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// The mock client/router pair is NOT a loopback broker: Publish only
	// records the outgoing message on the mock client, it does not
	// automatically deliver it back to subscribers. Simulate broker
	// delivery explicitly, mirroring this package's established
	// mockRouter.dispatch(topic, client.lastPublished()) convention.
	router.dispatch("sensor/data-gs", client.lastPublished())
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	if rejected.Err != nil {
		t.Fatalf("want no rejection, got: %v", rejected.Err)
	}
	if !handlerCalled {
		t.Fatal("want handler to be called")
	}
	if !gotOK || gotKey != "sensor-key-readonly" {
		t.Errorf("want ContextField to carry the authenticated key, got ok=%v key=%q", gotOK, gotKey)
	}
}

// TestAttach_ServeSubscribers_GrantedScopesContextField_WrongScope_Rejected
// proves the same pipeline REJECTS a known key whose granted scopes do
// NOT satisfy the declared requirement — the handler must never run.
func TestAttach_ServeSubscribers_GrantedScopesContextField_WrongScope_Rejected(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	evtClient := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := evtClient.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ch := events.NewChannel[sensorReading]("sensor/data-gs", sensorCodec)
	var rejected SubscribeError
	var handlerCalled bool
	sub := ch.WithSubscribe(events.Subscribe{
		Security: []events.SecurityRequirement{events.Require("apiKeyGS", "read:sensors")},
	}).SubscribeBoundMW(newGrantedScopesSensorMw(verifyGSAPIKey)).
		WithHandler(func(_ context.Context, _ sensorReading) error {
			handlerCalled = true
			return nil
		}).
		WithOptions(SubscribeOptions{OnError: func(e SubscribeError) { rejected = e }})
	if err := sub.Register(evtClient); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- evtClient.ServeSubscribers(ctx) }()
	router.waitHandler("sensor/data-gs")

	pub := ch.WithPublish(events.Publish{}).WithOptions(PublishOptions[sensorReading]{
		UserProperties: []UserProperty{{Key: "X-API-Key", Value: "sensor-key-writeonly"}},
	})
	if err := evtClient.Publish(ctx, pub, sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 19.5}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// The mock client/router pair is NOT a loopback broker: Publish only
	// records the outgoing message on the mock client, it does not
	// automatically deliver it back to subscribers. Simulate broker
	// delivery explicitly, mirroring this package's established
	// mockRouter.dispatch(topic, client.lastPublished()) convention.
	router.dispatch("sensor/data-gs", client.lastPublished())
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	if handlerCalled {
		t.Error("handler must NOT be called when the granted scope doesn't satisfy the declared requirement")
	}
	if rejected.Err == nil {
		t.Fatal("want a rejection via OnError, got nil")
	}
	if rejected.Kind != KindSecurity {
		t.Errorf("want KindSecurity, got %v", rejected.Kind)
	}
}
