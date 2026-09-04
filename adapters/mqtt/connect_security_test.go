package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// connectBearerScheme requires "username:password" to be non-empty — used
// across all connect-level security tests below.
var connectBearerScheme = ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
	WithCodec(codex.String().Refine(validate.NonEmptyString))

func alwaysFailsScheme() ConnectSecurityScheme {
	return ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
		WithCodec(codex.String().Refine(codex.Constraint[string]{
			Name:    "always-fails",
			Check:   func(string) bool { return false },
			Message: func(string) string { return "always fails" },
		}))
}

func TestNewSecuredClient_ValidCredential_ReturnsWrapper(t *testing.T) {
	client := &mockClient{}
	secured, err := NewSecuredClient(client, connectBearerScheme, "svc-account", "s3cr3t")
	if err != nil {
		t.Fatalf("NewSecuredClient: %v", err)
	}
	if secured == nil {
		t.Fatal("want non-nil *SecuredClient")
	}
}

func TestNewSecuredClient_MalformedCredential_ReturnsError(t *testing.T) {
	client := &mockClient{}
	secured, err := NewSecuredClient(client, alwaysFailsScheme(), "user", "pass")
	if err == nil {
		t.Fatal("want error for malformed credential")
	}
	if secured != nil {
		t.Fatal("want nil *SecuredClient on failure")
	}
	var credErr ConnectSecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("want ConnectSecurityCredentialError, got %v", err)
	}
	if credErr.Scheme.Type != route.SecuritySchemeHTTP {
		t.Errorf("want Scheme.Type=SecuritySchemeHTTP, got %v", credErr.Scheme.Type)
	}
}

func TestNewSecuredClient_NilCodec_NoOp(t *testing.T) {
	client := &mockClient{}
	noCodecScheme := ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}
	secured, err := NewSecuredClient(client, noCodecScheme, "", "")
	if err != nil {
		t.Fatalf("want no error when scheme.Codec is nil, got %v", err)
	}
	if secured == nil {
		t.Fatal("want non-nil *SecuredClient")
	}
}

func TestNewSecuredClient_MalformedCredential_ClientNeverUsed(t *testing.T) {
	client := &mockClient{}
	_, err := NewSecuredClient(client, alwaysFailsScheme(), "user", "pass")
	if err == nil {
		t.Fatal("want error")
	}
	if client.publishedTopicSnapshot() != "" || client.subscribedTopicSnapshot() != "" {
		t.Error("want underlying client never used on validation failure")
	}
}

func TestSecuredClient_SatisfiesMQTTClient(t *testing.T) {
	var _ pahomqtt.Client = (*SecuredClient)(nil)
}

func TestSecuredClient_TransparentDelegation(t *testing.T) {
	client := &mockClient{token: newCompletedToken(nil)}
	secured, err := NewSecuredClient(client, connectBearerScheme, "svc-account", "s3cr3t")
	if err != nil {
		t.Fatalf("NewSecuredClient: %v", err)
	}

	// Publish through the wrapper — behaves identically to the raw client.
	err = publish(context.Background(), secured, newHandle(), 1, false,
		userEvent{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Email: "alice@example.com"}, nil,
		PublishOptions[userEvent]{})
	if err != nil {
		t.Fatalf("Publish via SecuredClient: %v", err)
	}
	if client.publishedTopicSnapshot() != "user/created" {
		t.Fatalf("want publish via underlying client to topic user/created, got %q", client.publishedTopicSnapshot())
	}

	// Subscribe through the wrapper — behaves identically to the raw
	// client. SubscribeHandler builds the pahomqtt.MessageHandler;
	// the caller (here, through the SecuredClient wrapper) registers it
	// with the broker via the client's own Subscribe method.
	handler := subscribeHandler(context.Background(), newHandle(),
		func(_ context.Context, _ userEvent) error { return nil },
		SubscribeOptions{})
	secured.Subscribe("user/created", 1, handler)
	if client.subscribedTopicSnapshot() != "user/created" {
		t.Fatalf("want subscribe via underlying client to topic user/created, got %q", client.subscribedTopicSnapshot())
	}
}

func TestNewSecuredClient_Observer_RecordsSecurityRejection(t *testing.T) {
	client := &mockClient{}
	obs := &mockSecurityObserver{}
	_, err := NewSecuredClient(client, alwaysFailsScheme(), "user", "pass", WithObserver(obs))
	if err == nil {
		t.Fatal("want error")
	}
	if obs.location != "connect" {
		t.Errorf("want rejection location=connect, got %q", obs.location)
	}
	if obs.scheme != string(route.SecuritySchemeHTTP) {
		t.Errorf("want rejection scheme=%q, got %q", route.SecuritySchemeHTTP, obs.scheme)
	}
}

func TestNewSecuredClient_NilObserver_NoPanic(t *testing.T) {
	client := &mockClient{}
	_, err := NewSecuredClient(client, alwaysFailsScheme(), "user", "pass") // no WithObserver
	if err == nil {
		t.Fatal("want error")
	}
}

func TestConnectSecurityCredentialError_LogValue(t *testing.T) {
	err := ConnectSecurityCredentialError{
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
	wrapped := ConnectSecurityCredentialError{Scheme: connectBearerScheme, Err: inner}
	var credErr ConnectSecurityCredentialError
	if !errors.As(wrapped, &credErr) {
		t.Fatal("errors.As must find ConnectSecurityCredentialError")
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("errors.Is must find inner error via Unwrap")
	}
}
