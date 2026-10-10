package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/middleware"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests docs/design/d-0006-protocol-native-capabilities.md's
// Phase 4e: [events.Client.Subscribe]/[events.Client.Publish]'s
// reflection shim (transport.go) now runs the FULL [subscribeHandler][T]/
// [publish][T] pipeline (property-merge, User-Property-param validation,
// codec-based AND declarative SubscribeMW/PublishMW security, codec-
// backed Middleware dispatch (via .Use()/SubscribeBoundMW/PublishBoundMW),
// general-purpose wrapping,
// per-call format overrides) — not just Capabilities (Phase 4c). Each
// test exercises ONE pipeline step via the real Client.Attach +
// Client.Subscribe/Publish path (never the lower [subscribeWithHandle]/
// [publish] escape hatch), mirroring adapter_test.go's equivalent
// per-step tests but through the NEW public surface.

func attachedClientSubscribe(t *testing.T, client *mockClient, router *mockRouter) *events.Client {
	t.Helper()
	c := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	if err := c.Attach(NewTransport(TransportOptions{Client: client, Router: router})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return c
}

// ── Subscribe-side ──────────────────────────────────────────────────────────

// TestClientSubscribe_PropertyMerge_MergesRealUserProperty proves a
// MergedPropertyParam attached DIRECTLY to NewChannel merges a real
// incoming MQTT5 User Property into the decoded value through
// Client.Subscribe — previously silently skipped by this shim.
func TestClientSubscribe_PropertyMerge_MergesRealUserProperty(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	sub := events.NewChannel[tenantReading]("sensors/readings", tenantReadingCodec,
		events.NewPropertyParam("tenantID", codex.String(),
			func(r tenantReading) string { return r.TenantID },
			func(r *tenantReading, v string) { r.TenantID = v }),
	).WithSubscribe(events.Subscribe{Summary: "test"})

	var got tenantReading
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, msg tenantReading) error {
			got = msg
			close(done)
			return nil
		})
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(`{"sensor_id":"s1"}`),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "tenantID", Value: "acme"}},
		},
	})
	<-done
	if got.TenantID != "acme" {
		t.Errorf("want TenantID merged to %q, got %q", "acme", got.TenantID)
	}
}

// TestClientSubscribe_UserPropertyParam_MissingRequired_FiresOnError proves
// a declared, Required [UserPropertyParam] is now validated through
// Client.Subscribe — previously never consulted by this shim.
func TestClientSubscribe_UserPropertyParam_MissingRequired_FiresOnError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var gotErr SubscribeError
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		WithOptions(SubscribeOptions{
			UserPropertyParams: []UserPropertyParam{{Name: "TenantID", Required: true}},
			OnError:            func(e SubscribeError) { gotErr = e },
		})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil })
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:      "sensors/readings",
		Payload:    []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{},
	})
	deadline := time.Now().Add(time.Second)
	for gotErr.Kind == 0 && gotErr.Err == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if gotErr.Kind != KindSecurity {
		t.Fatalf("want KindSecurity, got %v (err=%v)", gotErr.Kind, gotErr.Err)
	}
	var missingErr MissingUserPropertyError
	if !errors.As(gotErr.Err, &missingErr) {
		t.Fatalf("want MissingUserPropertyError, got %v (%T)", gotErr.Err, gotErr.Err)
	}
}

// TestClientSubscribe_BuiltinSecurityCredential_Reject proves the
// codec-based credential check now runs through Client.Subscribe.
func TestClientSubscribe_BuiltinSecurityCredential_Reject(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var gotErr SubscribeError
	declMw := events.SecurityMiddleware[struct{}, struct{}]("bearer", securedBearerScheme, nil)
	noopImpl := func(_ context.Context, _ *sensorReading, _ subSecIn) (subSecOut, error) {
		return subSecOut{GrantedScopes: map[string][]string{"bearer": {}}}, nil
	}
	mw := events.BoundSecuritySubscribeMiddleware[sensorReading, subSecIn, subSecOut](
		"bearer", securedBearerScheme, nil, noopImpl,
	)
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		Use(declMw).
		SubscribeBoundMW(mw).
		WithOptions(SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil })
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:      "sensors/readings",
		Payload:    []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{}, // no Authorization property
	})
	deadline := time.Now().Add(time.Second)
	for gotErr.Kind == 0 && gotErr.Err == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if gotErr.Kind != KindSecurity {
		t.Fatalf("want KindSecurity, got %v (err=%v)", gotErr.Kind, gotErr.Err)
	}
	var credErr events.SecurityCredentialError
	if !errors.As(gotErr.Err, &credErr) {
		t.Fatalf("want events.SecurityCredentialError, got %v (%T)", gotErr.Err, gotErr.Err)
	}
}

// TestClientSubscribe_SubscribeMW_SecurityImpl_RunsAfterBuiltinCheck proves
// a declarative SubscribeMW-paired security implementation now runs
// through Client.Subscribe, AFTER the built-in codec-based check passes.
func TestClientSubscribe_SubscribeMW_SecurityImpl_RunsAfterBuiltinCheck(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	implCalled := make(chan struct{}, 1)
	fnCalled := make(chan struct{}, 1)
	declMw := events.SecurityMiddleware[struct{}, struct{}]("bearer", securedBearerScheme, nil)
	impl := func(_ context.Context, _ *sensorReading, _ subSecIn) (subSecOut, error) {
		implCalled <- struct{}{}
		return subSecOut{GrantedScopes: map[string][]string{"bearer": {}}}, nil
	}
	mw := events.BoundSecuritySubscribeMiddleware[sensorReading, subSecIn, subSecOut](
		"bearer", securedBearerScheme, nil, impl,
	)
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		Use(declMw).
		SubscribeBoundMW(mw)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { fnCalled <- struct{}{}; return nil })
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "Authorization", Value: "x"}},
		},
	})
	select {
	case <-implCalled:
	case <-time.After(time.Second):
		t.Fatal("want SubscribeMW-paired implementation called")
	}
	select {
	case <-fnCalled:
	case <-time.After(time.Second):
		t.Fatal("want fn called after implementation passes")
	}
}

// TestClientSubscribe_GeneralPurposeMW_WrapsHandler proves a
// general-purpose SubscribeMW-attached Fn wraps the handler through
// Client.Subscribe.
func TestClientSubscribe_GeneralPurposeMW_WrapsHandler(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var order []string
	wrap := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			order = append(order, "wrap-before")
			err := next(ctx, r)
			order = append(order, "wrap-after")
			return err
		}
	}
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, wrap)

	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			order = append(order, "handler")
			close(done)
			return nil
		})
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})
	<-done
	want := []string{"wrap-before", "handler", "wrap-after"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

// mqttFullPipelineIn/Out are minimal codec-backed middleware In/Out
// shapes for the codec-Middleware dispatch tests below.
type mqttFullPipelineIn struct{ Marker string }

var mqttFullPipelineInCodec = codex.Struct[mqttFullPipelineIn]()

type mqttFullPipelineOut struct{ Marker string }

var mqttFullPipelineOutCodec = codex.Struct[mqttFullPipelineOut]()

// TestClientSubscribe_MiddlewareDispatch_RunsBeforeHandler proves a
// codec-backed Middleware attached via .Use()/SubscribeBoundMW now dispatches
// through Client.Subscribe, before the handler runs.
func TestClientSubscribe_MiddlewareDispatch_RunsBeforeHandler(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var order []string
	decl := middleware.NewDeclaration("full-pipeline-policy", mqttFullPipelineInCodec, mqttFullPipelineOutCodec)
	bm := events.NewBoundSubscribeMiddleware(decl, func(_ context.Context, _ *sensorReading, _ mqttFullPipelineIn) (mqttFullPipelineOut, error) {
		order = append(order, "middleware")
		return mqttFullPipelineOut{}, nil
	})
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})
	sub = sub.SubscribeBoundMW(bm)

	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			order = append(order, "handler")
			close(done)
			return nil
		})
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})
	<-done
	if len(order) != 2 || order[0] != "middleware" || order[1] != "handler" {
		t.Errorf("want dispatch order [middleware, handler], got %v", order)
	}
}

// TestClientSubscribe_FormatOverride proves a per-call
// [events.ClientSubscribeOptions.Formats] override is now honored.
func TestClientSubscribe_FormatOverride(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	callCount := 0
	jsonFmt := format.JSON(sensorCodec)
	overrideFmt := format.NewTyped(sensorCodec,
		func(r sensorReading) ([]byte, error) { return jsonFmt.Marshal(r) },
		func(b []byte) (sensorReading, error) {
			callCount++
			return jsonFmt.Unmarshal(b)
		},
		"application/x-custom",
	)

	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})

	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error {
			close(done)
			return nil
		}, events.ClientSubscribeOptions{Formats: []format.Format[sensorReading]{overrideFmt}})
	}()
	router.waitHandler("sensors/readings")
	payload, _ := jsonFmt.Marshal(sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1})
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: payload})
	<-done
	if callCount != 1 {
		t.Errorf("want override format's Unmarshal called once, got %d", callCount)
	}
}

// TestClientSubscribe_ErrorChannel_MatchedRespond_SkipsOnError is a
// regression test for a REAL bug found while converting
// examples/events-api/demo_error_pattern.go onto this pipeline: a
// matched, published [events.ErrorRespond] MUST skip opts.OnError
// entirely (mirrors adapter.go's tryPublishErrorChannel's exact
// "handled=true means return immediately" contract) — this dispatch
// pipeline's shared dispatchFailure closure initially called OnError
// UNCONDITIONALLY after every branch, a real behavioral divergence from
// the escape hatch this shim is supposed to match exactly.
func TestClientSubscribe_ErrorChannel_MatchedRespond_SkipsOnError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	onErrorCalled := false
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[codex.ValidationErrors, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e codex.ValidationErrors) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"}).
		WithOptions(SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(_ context.Context, _ sensorReading) error { return nil })
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{}`), // missing required fields -> codex.ValidationErrors
	})
	// Poll for the error-channel publish rather than a fixed sleep —
	// avoids a flaky race against the async dispatch above.
	deadline := time.Now().Add(time.Second)
	var matched bool
	for time.Now().Before(deadline) {
		if p := client.lastPublished(); p != nil && p.Topic == "sensors/readings/errors" {
			matched = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !matched {
		t.Fatal("want a publish to the declared error-output topic")
	}
	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches and publishes (handled=true)")
	}
}

// TestClientSubscribe_MalformedImplementationFn_ReturnsShapeErrorEagerly
// proves a malformed SubscribeMW Fn fails LOUDLY and IMMEDIATELY (before
// any message is dispatched), never silently at message time.
// TestClientSubscribe_MessageAndUserPropertiesFromContext_Retrievable is
// a regression test for a REAL, previously-untracked gap found while
// refactoring examples/events-api's escape-hatch demos: the ctx passed
// to a Client.Subscribe handler never carried the raw
// *pahomqtt5.Publish/its User Properties the way the escape hatch
// (subscribeWithHandle/makeSubscribeMessageHandler) always has —
// [MessageFromContext]/[UserPropertiesFromContext] silently returned
// (nil, false) through Client.Subscribe. Fixed by injecting the SAME 2
// context.WithValue calls adapter.go's handler already does, at the
// SAME pre-decode placement.
func TestClientSubscribe_MessageAndUserPropertiesFromContext_Retrievable(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var gotMsg *pahomqtt5.Publish
	var gotMsgOK bool
	var gotProps pahomqtt5.UserProperties
	var gotPropsOK bool
	done := make(chan struct{})
	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.Subscribe(ctx, sub, func(hCtx context.Context, _ sensorReading) error {
			gotMsg, gotMsgOK = MessageFromContext(hCtx)
			gotProps, gotPropsOK = UserPropertiesFromContext(hCtx)
			close(done)
			return nil
		})
	}()
	router.waitHandler("sensors/readings")
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "TenantID", Value: "acme"}},
		},
	})
	<-done

	if !gotMsgOK || gotMsg == nil {
		t.Fatal("want MessageFromContext to retrieve the raw *pahomqtt5.Publish through Client.Subscribe")
	}
	if gotMsg.Topic != "sensors/readings" {
		t.Errorf("want retrieved message Topic = %q, got %q", "sensors/readings", gotMsg.Topic)
	}
	if !gotPropsOK {
		t.Fatal("want UserPropertiesFromContext to retrieve User Properties through Client.Subscribe")
	}
	if got := gotProps.Get("TenantID"); got != "acme" {
		t.Errorf("want TenantID=%q, got %q", "acme", got)
	}
}

func TestClientSubscribe_MalformedImplementationFn_ReturnsShapeErrorEagerly(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func() {}) // wrong shape entirely

	err := c.Subscribe(context.Background(), sub, func(_ context.Context, _ sensorReading) error { return nil })
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v (%T)", err, err)
	}
}

// ── Publish-side ─────────────────────────────────────────────────────────────

// TestClientPublish_PropertyMerge_WritesRealUserProperty proves a
// MergedPropertyParam attached DIRECTLY to NewChannel derives its value
// FROM the outgoing message through Client.Publish.
func TestClientPublish_PropertyMerge_WritesRealUserProperty(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	pub := events.NewChannel[tenantReading]("sensors/readings", tenantReadingCodec,
		events.NewPropertyParam("tenantID", codex.String(),
			func(r tenantReading) string { return r.TenantID },
			func(r *tenantReading, v string) { r.TenantID = v }),
	).WithPublish(events.Publish{})

	msg := tenantReading{SensorID: "s1", TenantID: "acme"}
	if err := c.Publish(context.Background(), pub, msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	published := client.lastPublished()
	if published == nil || published.Properties == nil {
		t.Fatal("expected a published message with properties")
	}
	var gotTenant string
	for _, p := range published.Properties.User {
		if p.Key == "tenantID" {
			gotTenant = p.Value
		}
	}
	if gotTenant != "acme" {
		t.Errorf("want tenantID User Property %q, got %q", "acme", gotTenant)
	}
}

// TestClientPublish_ClientImplementations_CredentialMerge_ValidFormat_Passes
// proves a PublishMW-paired security implementation now runs through
// Client.Publish.
func TestClientPublish_ClientImplementations_CredentialMerge_ValidFormat_Passes(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	declMw := events.SecurityMiddleware[struct{}, struct{}]("bearer", securedBearerScheme, nil)
	mw := pubSecBoundMw(func(context.Context, sensorReading) (pubSecOut, error) {
		return pubSecOut{GrantedScopes: map[string][]string{"bearer": {}}, Authorization: "x"}, nil
	})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		Use(declMw).
		PublishBoundMW(mw)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	published := client.lastPublished()
	if published == nil || published.Properties == nil {
		t.Fatal("expected a published message with properties")
	}
	if got := published.Properties.User.Get("Authorization"); got != "x" {
		t.Errorf("want Authorization=%q, got %q", "x", got)
	}
}

// TestClientPublish_ClientImplementations_MalformedCredential_ReturnsSecurityCredentialError
// proves the credential FORMAT is still validated after a
// ClientImplementations Fn runs, through Client.Publish.
func TestClientPublish_ClientImplementations_MalformedCredential_ReturnsSecurityCredentialError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	declMw := events.SecurityMiddleware[struct{}, struct{}]("bearer", securedBearerScheme, nil)
	mw := pubSecBoundMw(func(context.Context, sensorReading) (pubSecOut, error) {
		return pubSecOut{GrantedScopes: map[string][]string{"bearer": {}}, Authorization: ""}, nil // empty -> fails non-empty-string codec
	})
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []events.SecurityRequirement{events.Require("bearer")}}).
		Use(declMw).
		PublishBoundMW(mw)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading)
	var credErr events.SecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("want events.SecurityCredentialError, got %v (%T)", err, err)
	}
	if len(client.published) != 0 {
		t.Error("want no message actually published when credential format is malformed")
	}
}

// TestClientPublish_GeneralPurposeMW_WrapsEncodeAndTransmit proves a
// general-purpose PublishMW-attached Fn wraps the "encode and transmit"
// step through Client.Publish.
func TestClientPublish_GeneralPurposeMW_WrapsEncodeAndTransmit(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	var order []string
	wrap := func(next func(context.Context, sensorReading) error) func(context.Context, sensorReading) error {
		return func(ctx context.Context, r sensorReading) error {
			order = append(order, "wrap-before")
			err := next(ctx, r)
			order = append(order, "wrap-after")
			return err
		}
	}
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, wrap)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(order) != 2 || order[0] != "wrap-before" || order[1] != "wrap-after" {
		t.Errorf("order = %v, want [wrap-before, wrap-after] (transmit runs BETWEEN)", order)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message, got %d", len(client.published))
	}
}

// TestClientPublish_MiddlewareDispatch_ContributesPropertyVar proves a
// codec-backed Middleware attached via .Use()/PublishBoundMW now
// dispatches through Client.Publish, contributing a real MQTT5 User
// Property.
func TestClientPublish_MiddlewareDispatch_ContributesPropertyVar(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	decl := middleware.NewDeclaration("full-pipeline-out-policy", mqttFullPipelineInCodec, mqttFullPipelineOutCodec)
	bm := events.NewBoundPublishMiddleware(decl, func(_ context.Context, _ sensorReading) (mqttFullPipelineOut, error) {
		return mqttFullPipelineOut{Marker: "from-middleware"}, nil
	}).
		WithPublishProperty(events.NewPropertyParam("mw-tenant", codex.String(),
			func(o mqttFullPipelineOut) string { return o.Marker },
			func(o *mqttFullPipelineOut, v string) { o.Marker = v }))
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	pub = pub.PublishBoundMW(bm)

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	if err := c.Publish(context.Background(), pub, reading); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	published := client.lastPublished()
	if published == nil || published.Properties == nil {
		t.Fatal("expected a published message with properties")
	}
	var got string
	for _, p := range published.Properties.User {
		if p.Key == "mw-tenant" {
			got = p.Value
		}
	}
	if got != "from-middleware" {
		t.Errorf("want mw-tenant=%q, got %q", "from-middleware", got)
	}
}

// TestClientPublish_FormatOverride proves a per-call
// [events.ClientPublishOptions.Formats] override is now honored.
func TestClientPublish_FormatOverride(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	callCount := 0
	jsonFmt := format.JSON(sensorCodec)
	overrideFmt := format.NewTyped(sensorCodec,
		func(r sensorReading) ([]byte, error) {
			callCount++
			return jsonFmt.Marshal(r)
		},
		func(b []byte) (sensorReading, error) { return jsonFmt.Unmarshal(b) },
		"application/x-custom",
	)

	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"})
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading,
		events.ClientPublishOptions{Formats: []format.Format[sensorReading]{overrideFmt}})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if callCount != 1 {
		t.Errorf("want override format's Marshal called once, got %d", callCount)
	}
}

// TestClientPublish_MalformedImplementationFn_ReturnsShapeErrorEagerly
// proves a malformed PublishMW Fn fails LOUDLY and IMMEDIATELY.
func TestClientPublish_MalformedImplementationFn_ReturnsShapeErrorEagerly(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	c := attachedClientSubscribe(t, client, router)

	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test"}).
		PublishMW(nil, func() {}) // wrong shape entirely

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	err := c.Publish(context.Background(), pub, reading)
	var shapeErr middleware.MiddlewareShapeError
	if !errors.As(err, &shapeErr) {
		t.Fatalf("want middleware.MiddlewareShapeError, got %v (%T)", err, err)
	}
	if len(client.published) != 0 {
		t.Error("want no message published when shape validation fails eagerly")
	}
}
