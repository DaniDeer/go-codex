package mqtt

import (
	"fmt"
	"log/slog"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds CONNECTION-LEVEL security — a codec-validated CONNECT-time
// (username/password) credential check, distinct from the MESSAGE-LEVEL
// security in adapter.go (events.WithSecurityScheme +
// SubscribeOptions.SecurityFunc; MQTT 3.1.1 has no PublishOptions.CredentialFunc
// equivalent, since the protocol carries no per-message metadata at all).
// See docs/features/security.md's "Connection-level vs message-level
// security" section for when to use which.
//
// Connect-time username/password IS protocol-symmetric between MQTT 3.1.1
// and MQTT5 (unlike per-message User Properties, MQTT5-only) — this file
// mirrors adapters/mqtt5/connect_security.go exactly, wrapping
// pahomqtt.Client instead of mqtt5.MQTTClient. go-codex NEVER calls
// Connect() itself; the caller connects their own client first, THEN wraps
// it with NewSecuredClient before passing it to Subscribe/Publish.

// ConnectSecurityScheme combines [route.SecurityScheme] spec metadata with a
// runtime Codec for connect-level (CONNECT username/password) credential
// validation — the connection-level analogue of [events.SecurityScheme],
// validated ONCE at wrap time ([NewSecuredClient]), not once per message.
//
// Use [ConnectSecurityScheme.WithCodec] to set the Codec field inline
// without a temporary variable, same idiom as events.SecurityScheme.
type ConnectSecurityScheme struct {
	route.SecurityScheme
	// Codec, when non-nil, validates the combined "username:password"
	// string (see NewSecuredClient). Nil means no format validation;
	// NewSecuredClient always succeeds in that case.
	Codec *codex.Codec[string]
}

// WithCodec returns a copy of s with Codec set to c.
func (s ConnectSecurityScheme) WithCodec(c codex.Codec[string]) ConnectSecurityScheme {
	s.Codec = &c
	return s
}

// SecuredClient wraps an already-connected [pahomqtt.Client]. Every
// pahomqtt.Client method is promoted automatically via struct embedding — a
// *SecuredClient behaves IDENTICALLY to the wrapped client in every respect
// except that its connect-time credential has already been validated by the
// time it exists. Construct one via [NewSecuredClient].
type SecuredClient struct {
	pahomqtt.Client
}

// Compile-time assertion that *SecuredClient satisfies pahomqtt.Client —
// every method is promoted via struct embedding, so this should always hold.
var _ pahomqtt.Client = (*SecuredClient)(nil)

// SecuredClientOption configures [NewSecuredClient].
type SecuredClientOption func(*securedClientOptions)

type securedClientOptions struct {
	observer stats.Observer
}

func resolveSecuredClientOptions(opts []SecuredClientOption) securedClientOptions {
	o := securedClientOptions{observer: stats.NoopObserver{}}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithObserver sets the Observer [NewSecuredClient] reports
// [stats.SecurityObserver.RecordSecurityRejection] to on validation
// failure. Defaults to [stats.NoopObserver] when not supplied.
func WithObserver(obs stats.Observer) SecuredClientOption {
	return func(o *securedClientOptions) { o.observer = obs }
}

// NewSecuredClient combines username+password into a single
// "username:password" string and validates that ONCE, synchronously,
// against scheme.Codec — before client is ever used.
//
// The MQTT CONNECT packet carries username and password as two SEPARATE
// wire fields (unlike HTTP's single Authorization header) — but
// Codec.Validate takes exactly one string. NewSecuredClient bridges this
// the same way examples/go-edge-models/docker/registry's
// internal.BasicAuthCodec already does for HTTP Basic auth: combine both
// into one string before validating. This "username:password" string is a
// VALIDATION-TIME representation ONLY — it is never transmitted; the raw
// username/password you pass here are exactly what your OWN prior
// client.Connect() call already sent to the broker (via
// [pahomqtt.ClientOptions.SetUsername]/[pahomqtt.ClientOptions.SetPassword]),
// unchanged.
//
// On success: returns a *SecuredClient, a drop-in replacement for the raw
// client at every Subscribe/Publish call site — every existing call site
// keeps working exactly as it does with a raw pahomqtt.Client, since
// *SecuredClient satisfies pahomqtt.Client transparently via struct
// embedding.
//
// On failure: returns (nil, ConnectSecurityCredentialError) — client is
// NEVER touched or used; the caller should treat this as fatal.
//
// A nil scheme.Codec is a no-op (matches the "nil Codec means no format
// validation" contract used everywhere else in the security model) —
// NewSecuredClient always succeeds in that case.
//
// Connect first, THEN wrap — whether the connection was established via
// [Connect] or by constructing a raw [pahomqtt.Client] directly.
// Recommendation: also declare the SAME requirement via
// [events.Server.Security] when building an AsyncAPI spec for this
// connection, purely for documentation parity between the spec output and
// this runtime check — Server.Security itself has no code-level link to
// ConnectSecurityScheme/NewSecuredClient.
//
//	client, err := mqtt.Connect(ctx, "tcp://broker:1883", mqtt.ConnectOptions{
//	    ClientID: "svc-account", Username: "svc-account", Password: token,
//	})
//	if err != nil { /* handle */ }
//
//	secured, err := mqtt.NewSecuredClient(client, bearerAuth, "svc-account", token)
//	if err != nil { /* malformed credential — client is never used */ }
//
//	transport := mqtt.NewPublishTransport[T](secured, 1, false, mqtt.PublishOptions[T]{})
//	err = events.PublishHandle(ctx, pub, transport, msg) // works exactly as before
func NewSecuredClient(client pahomqtt.Client, scheme ConnectSecurityScheme, username, password string, opts ...SecuredClientOption) (*SecuredClient, error) {
	o := resolveSecuredClientOptions(opts)
	if scheme.Codec != nil {
		combined := username + ":" + password
		if err := scheme.Codec.Validate(combined); err != nil {
			if secObs, ok := o.observer.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection("connect", string(scheme.Type))
			}
			return nil, ConnectSecurityCredentialError{Scheme: scheme, Err: err}
		}
	}
	return &SecuredClient{Client: client}, nil
}

// ConnectSecurityCredentialError is returned by [NewSecuredClient] when the
// combined username/password fails scheme.Codec's validation. The
// underlying client is NEVER touched when this error is returned.
//
// Use [errors.As] to extract the scheme and underlying constraint error:
//
//	var credErr mqtt.ConnectSecurityCredentialError
//	if errors.As(err, &credErr) {
//	    log.Printf("connect credential invalid: %v", credErr.Err)
//	}
type ConnectSecurityCredentialError struct {
	Scheme ConnectSecurityScheme // the scheme that rejected the credential
	Err    error                 // the underlying constraint error
}

func (e ConnectSecurityCredentialError) Error() string {
	return fmt.Sprintf("connect security scheme %q: invalid credential: %s", e.Scheme.Type, e.Err)
}

// Unwrap allows errors.As and errors.Is to traverse the underlying constraint error.
func (e ConnectSecurityCredentialError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e ConnectSecurityCredentialError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("scheme_type", string(e.Scheme.Type)),
		slog.Any("err", e.Err),
	)
}
