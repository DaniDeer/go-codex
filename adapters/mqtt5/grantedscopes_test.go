package mqtt5

import (
	"context"
	"testing"
	"time"

	pahomqtt5 "github.com/eclipse/paho.golang/paho"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/route"
)

// docs/design/d-0007-declarative-middleware-layering.md's "Prerequisite for
// Phase 2 (api/events)": regression tests proving the NEW additive
// Subscribe shape's GrantedScopes are ACTUALLY merged into CheckScopes —
// not just "doesn't panic" — and that the ORIGINAL 1-return shape stays
// completely unaffected.

type gsIn struct{ Token string }

type gsOut struct{ GrantedScopes map[string][]string }

var gsBearerScheme = events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}

// TestSubscribeMW_GrantedScopes_MergedIntoCheckScopes proves the core fix
// end-to-end: a bound Subscribe Security middleware using the NEW
// 2-return shape, granting a REAL scope, lets a scope-requiring channel
// dispatch succeed — and a middleware granting the WRONG scope (or none)
// gets rejected by CheckScopes, not silently ignored.
func TestSubscribeMW_GrantedScopes_MergedIntoCheckScopes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		grant      map[string][]string
		wantCalled bool
	}{
		{"correct scope granted", map[string][]string{"bearer": {"read:sensors"}}, true},
		{"wrong scope granted", map[string][]string{"bearer": {"write:other"}}, false},
		{"no scopes granted", map[string][]string{"bearer": {}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &mockClient{}
			router := newMockRouter()
			handlerCalled := false

			bm := events.BoundSecuritySubscribeMiddleware[sensorReading, gsIn, gsOut]("bearer", gsBearerScheme, []string{"read:sensors"},
				func(_ context.Context, _ *sensorReading, _ gsIn) (gsOut, error) {
					return gsOut{GrantedScopes: tc.grant}, nil
				})
			b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
			handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
				WithSubscribe(events.Subscribe{
					Summary:  "test",
					Security: []route.SecurityRequirement{route.Require("bearer", "read:sensors")},
				}).
				SubscribeBoundMW(bm).
				Handle(b)
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := subscribeWithHandle(ctx, client, router, handle, func(_ context.Context, _ sensorReading) error {
				handlerCalled = true
				return nil
			}, SubscribeOptions{}); err != nil {
				t.Fatalf("Subscribe setup failed: %v", err)
			}
			router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

			if handlerCalled != tc.wantCalled {
				t.Errorf("handlerCalled = %v, want %v (grant %v)", handlerCalled, tc.wantCalled, tc.grant)
			}
		})
	}
}

// TestSubscribeMW_LegacyOneReturnShape_StillDispatchesUnchanged confirms
// a general-purpose (non-Security, no Out needed) bound Subscribe
// middleware using the EXISTING 1-return shape is COMPLETELY unaffected
// by the new 2-return shape's addition.
func TestSubscribeMW_LegacyOneReturnShape_StillDispatchesUnchanged(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	mwCalled := false
	handlerCalled := false

	bm := events.NewBoundSubscribeMiddleware(newMqttMdDeclaration("general-purpose"),
		func(ctx context.Context, msg *sensorReading, in mqttMdIn) (mqttMdOut, error) {
			mwCalled = true
			return mqttMdOut{}, nil
		})
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeBoundMW(bm).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, func(_ context.Context, _ sensorReading) error {
		handlerCalled = true
		return nil
	}, SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	if !mwCalled {
		t.Error("want the 1-return-shape middleware Fn called")
	}
	if !handlerCalled {
		t.Error("want the channel handler called after middleware dispatch succeeds")
	}
}

// TestPublishMW_Unaffected_NoMergeWiringAdded confirms Publish dispatch
// gets NO new merge call — GrantedScopes enforcement is RECEIVING-side
// (Subscribe) only, symmetric with REST's ClientMW never doing this
// either (docs/design/d-0007-declarative-middleware-layering.md's "Prerequisite
// for Phase 2 (api/events)", corrected during implementation). A Publish
// bound middleware with a GrantedScopes-carrying Out still works exactly
// as before (Out's fields merge into topic/property vars via its OWN
// merge-field declarations, if any — GrantedScopes itself is simply
// never read on this side, since it is not a merge field).
func TestPublishMW_Unaffected_NoMergeWiringAdded(t *testing.T) {
	client := &mockClient{}
	fnCalled := false

	bm := events.BoundSecurityPublishMiddleware[sensorReading, gsIn, gsOut]("bearer", gsBearerScheme, nil,
		func(_ context.Context, _ sensorReading) (gsOut, error) {
			fnCalled = true
			return gsOut{GrantedScopes: map[string][]string{"bearer": {"read:sensors"}}}, nil
		})
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"}).
		PublishBoundMW(bm).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if err := publish(context.Background(), client, handle, sensorReading{SensorID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Value: 1}, nil, true, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !fnCalled {
		t.Error("want the Publish middleware Fn called (unaffected by the Subscribe-side patch)")
	}
}
