package mqtt5

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// Round 42: MergedPropertyParam[T] direct (Middleware-free) channel
// attachment, exercised end-to-end through mqtt5's real dispatch — the
// symmetry-bug fix's adapter-level proof. Mirrors
// TestSubscribe_Observer_ReportsMiddlewareInAndFnLocations/
// TestPublish_WithPublishProperty_WritesOutgoingUserProperty_SeparateFromTopicVars's
// shape, but with NO events.Middleware/Transform involved — only a plain
// events.NewPropertyParam attached directly to NewChannel.

type tenantReading struct {
	SensorID string
	TenantID string
}

var tenantReadingCodec = codex.Struct[tenantReading](
	codex.RequiredField("sensor_id", codex.String(),
		func(r tenantReading) string { return r.SensorID },
		func(r *tenantReading, v string) { r.SensorID = v }),
	codex.OptionalField("tenant_id", codex.String(),
		func(r tenantReading) string { return r.TenantID },
		func(r *tenantReading, v string) { r.TenantID = v }),
)

// TestSubscribe_DirectPropertyAttachment_MergesRealUserProperty proves a
// MergedPropertyParam attached DIRECTLY to NewChannel (no Middleware) now
// merges a real incoming MQTT5 User Property into the decoded value —
// previously silently dropped.
func TestSubscribe_DirectPropertyAttachment_MergesRealUserProperty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	sub := events.NewChannel[tenantReading]("sensors/readings", tenantReadingCodec,
		events.NewPropertyParam("tenantID", codex.String(),
			func(r tenantReading) string { return r.TenantID },
			func(r *tenantReading, v string) { r.TenantID = v }),
	).WithSubscribe(events.Subscribe{Summary: "test"})
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := sub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var got tenantReading
	client := &mockClient{}
	router := newMockRouter()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, msg tenantReading) error { got = msg; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(`{"sensor_id":"s1"}`),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "tenantID", Value: "acme"}},
		},
	})

	if got.TenantID != "acme" {
		t.Errorf("want TenantID merged to %q, got %q", "acme", got.TenantID)
	}
}

// TestPublish_DirectPropertyAttachment_WritesRealUserProperty proves a
// MergedPropertyParam attached DIRECTLY to NewChannel derives its value
// FROM the outgoing message and writes it as a real MQTT5 User Property —
// no Middleware/ClientTransform needed.
func TestPublish_DirectPropertyAttachment_WritesRealUserProperty(t *testing.T) {
	pub := events.NewChannel[tenantReading]("sensors/readings", tenantReadingCodec,
		events.NewPropertyParam("tenantID", codex.String(),
			func(r tenantReading) string { return r.TenantID },
			func(r *tenantReading, v string) { r.TenantID = v }),
	).WithPublish(events.Publish{})
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{}
	msg := tenantReading{SensorID: "s1", TenantID: "acme"}
	if err := publishHandle(context.Background(), client, handle, 1, false, msg, PublishOptions[tenantReading]{}); err != nil {
		t.Fatalf("publishHandle: %v", err)
	}

	published := client.lastPublished()
	if !strings.Contains(published.Topic, "sensors/readings") {
		t.Errorf("unexpected topic: %q", published.Topic)
	}
	var gotTenant string
	if published.Properties != nil {
		for _, p := range published.Properties.User {
			if p.Key == "tenantID" {
				gotTenant = p.Value
			}
		}
	}
	if gotTenant != "acme" {
		t.Errorf("want tenantID User Property %q, got %q", "acme", gotTenant)
	}
}
