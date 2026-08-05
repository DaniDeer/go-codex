package mqtt5_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	mqtt5 "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// connectBearerScheme requires "username:password" to be non-empty — used
// across all connect-level security tests below.
var connectBearerScheme = mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
	WithCodec(codex.String().Refine(validate.NonEmptyString))

func TestNewSecuredClient_ValidCredential_ReturnsWrapper(t *testing.T) {
	client := &mockClient{}
	secured, err := mqtt5.NewSecuredClient(client, connectBearerScheme, "svc-account", "s3cr3t")
	if err != nil {
		t.Fatalf("NewSecuredClient: %v", err)
	}
	if secured == nil {
		t.Fatal("want non-nil *SecuredClient")
	}
}

func TestNewSecuredClient_MalformedCredential_ReturnsError(t *testing.T) {
	client := &mockClient{}
	// Empty username AND password -> combined "username:password" is just
	// ":" which the NonEmptyString constraint still accepts (non-empty
	// string) -- use a constraint that actually distinguishes malformed
	// input: require a colon-separated non-empty password specifically via
	// a stricter constraint for this test.
	strictScheme := mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
		WithCodec(codex.String().Refine(codex.Constraint[string]{
			Name:    "must-not-be-bare-colon",
			Check:   func(v string) bool { return v != ":" },
			Message: func(v string) string { return "credential must not be empty username and password" },
		}))
	secured, err := mqtt5.NewSecuredClient(client, strictScheme, "", "")
	if err == nil {
		t.Fatal("want error for malformed (empty username+password) credential")
	}
	if secured != nil {
		t.Fatal("want nil *SecuredClient on failure")
	}
	var credErr mqtt5.ConnectSecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("want ConnectSecurityCredentialError, got %v", err)
	}
	if credErr.Scheme.Type != route.SecuritySchemeHTTP {
		t.Errorf("want Scheme.Type=SecuritySchemeHTTP, got %v", credErr.Scheme.Type)
	}
}

func TestNewSecuredClient_NilCodec_NoOp(t *testing.T) {
	client := &mockClient{}
	noCodecScheme := mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}
	secured, err := mqtt5.NewSecuredClient(client, noCodecScheme, "", "")
	if err != nil {
		t.Fatalf("want no error when scheme.Codec is nil, got %v", err)
	}
	if secured == nil {
		t.Fatal("want non-nil *SecuredClient")
	}
}

func TestNewSecuredClient_MalformedCredential_ClientNeverUsed(t *testing.T) {
	client := &mockClient{}
	emptyOnlyScheme := mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
		WithCodec(codex.String().Refine(codex.Constraint[string]{
			Name:    "always-fails",
			Check:   func(string) bool { return false },
			Message: func(string) string { return "always fails" },
		}))
	_, err := mqtt5.NewSecuredClient(client, emptyOnlyScheme, "user", "pass")
	if err == nil {
		t.Fatal("want error")
	}
	if len(client.published) != 0 || len(client.subscribed) != 0 {
		t.Error("want underlying client never used on validation failure")
	}
}

func TestSecuredClient_SatisfiesMQTTClient(t *testing.T) {
	var _ mqtt5.MQTTClient = (*mqtt5.SecuredClient)(nil)
}

func TestSecuredClient_TransparentDelegation(t *testing.T) {
	client := &mockClient{}
	secured, err := mqtt5.NewSecuredClient(client, connectBearerScheme, "svc-account", "s3cr3t")
	if err != nil {
		t.Fatalf("NewSecuredClient: %v", err)
	}

	// Publish through the wrapper — behaves identically to the raw client.
	err = mqtt5.Publish(context.Background(), secured, newChannelHandle(), 1, false,
		sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}, nil,
		mqtt5.PublishOptions{})
	if err != nil {
		t.Fatalf("Publish via SecuredClient: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message via underlying client, got %d", len(client.published))
	}

	// Subscribe through the wrapper — behaves identically to the raw client.
	router := newMockRouter()
	var received sensorReading
	if err := mqtt5.Subscribe(context.Background(), secured, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		mqtt5.SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe via SecuredClient: %v", err)
	}
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})
	if received.Value != 22.5 {
		t.Fatalf("unexpected value via SecuredClient subscribe: %v", received.Value)
	}
}

func TestNewSecuredClient_Observer_RecordsSecurityRejection(t *testing.T) {
	client := &mockClient{}
	obs := &testObserver{}
	failScheme := mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
		WithCodec(codex.String().Refine(codex.Constraint[string]{
			Name:    "always-fails",
			Check:   func(string) bool { return false },
			Message: func(string) string { return "always fails" },
		}))
	_, err := mqtt5.NewSecuredClient(client, failScheme, "user", "pass", mqtt5.WithObserver(obs))
	if err == nil {
		t.Fatal("want error")
	}
	if len(obs.secRejections) != 1 {
		t.Errorf("want 1 security rejection recorded, got %d", len(obs.secRejections))
	}
	if obs.secRejections[0] != "connect" {
		t.Errorf("want rejection location=connect, got %q", obs.secRejections[0])
	}
}

func TestNewSecuredClient_NilObserver_NoPanic(t *testing.T) {
	client := &mockClient{}
	failScheme := mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
		WithCodec(codex.String().Refine(codex.Constraint[string]{
			Name:    "always-fails",
			Check:   func(string) bool { return false },
			Message: func(string) string { return "always fails" },
		}))
	_, err := mqtt5.NewSecuredClient(client, failScheme, "user", "pass") // no WithObserver
	if err == nil {
		t.Fatal("want error")
	}
}

func TestConnectSecurityCredentialError_LogValue(t *testing.T) {
	err := mqtt5.ConnectSecurityCredentialError{
		Scheme: connectBearerScheme,
		Err:    errors.New("empty credential"),
	}
	v := err.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want slog.KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	var hasSchemeType, hasErr bool
	for _, a := range attrs {
		switch a.Key {
		case "scheme_type":
			hasSchemeType = true
		case "err":
			hasErr = true
		}
	}
	if !hasSchemeType || !hasErr {
		t.Errorf("want scheme_type and err keys in LogValue, got %v", attrs)
	}
}

func TestConnectSecurityCredentialError_ErrorsAs(t *testing.T) {
	inner := errors.New("empty credential")
	wrapped := mqtt5.ConnectSecurityCredentialError{Scheme: connectBearerScheme, Err: inner}
	var credErr mqtt5.ConnectSecurityCredentialError
	if !errors.As(wrapped, &credErr) {
		t.Fatal("errors.As must find ConnectSecurityCredentialError")
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("errors.Is must find inner error via Unwrap")
	}
}
