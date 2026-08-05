package mqtt5

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds CONNECTION-LEVEL security — a codec-validated CONNECT-time
// (username/password) credential check, distinct from the MESSAGE-LEVEL
// security in adapter.go/reqreply.go (events.WithSecurityScheme/
// reqreply.WithSecurityScheme + SubscribeOptions.SecurityFunc/
// PublishOptions.CredentialFunc/ServeOptions.SecurityFunc/CallOptions.CredentialFunc).
// See docs/features/security.md's "Connection-level vs message-level
// security" section for when to use which.
//
// Unlike message-level security (which re-validates a credential on EVERY
// message via MQTT 5 User Properties — MQTT5-only, since MQTT 3.1.1 has no
// per-message metadata channel), connection-level security validates ONCE,
// synchronously, at construction — appropriate for the common "one
// connection = one identity" case. go-codex NEVER calls Connect() itself;
// the caller connects their own client first, THEN wraps it with
// NewSecuredClient before passing it to Subscribe/Publish/Serve/Call.

// ConnectSecurityScheme combines [route.SecurityScheme] spec metadata with a
// runtime Codec for connect-level (CONNECT username/password) credential
// validation — the connection-level analogue of [events.SecurityScheme]/
// [reqreply.SecurityScheme], validated ONCE at wrap time ([NewSecuredClient]),
// not once per message.
//
// Use [ConnectSecurityScheme.WithCodec] to set the Codec field inline
// without a temporary variable, same idiom as events.SecurityScheme/
// reqreply.SecurityScheme.
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

// SecuredClient wraps an already-connected MQTTClient. Every MQTTClient
// method is promoted automatically via struct embedding — a *SecuredClient
// behaves IDENTICALLY to the wrapped client in every respect except that
// its connect-time credential has already been validated by the time it
// exists. Construct one via [NewSecuredClient].
type SecuredClient struct {
	MQTTClient
}

// Compile-time assertion that *SecuredClient satisfies MQTTClient — every
// method is promoted via struct embedding, so this should always hold.
var _ MQTTClient = (*SecuredClient)(nil)

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
// wire fields (unlike HTTP's single Authorization header, which is why
// the message-level model validates one plain string) — but Codec.Validate
// takes exactly one string. NewSecuredClient bridges this the same way
// examples/go-edge-models/docker/registry's internal.BasicAuthCodec
// already does for HTTP Basic auth: combine both into one string before
// validating. This "username:password" string is a VALIDATION-TIME
// representation ONLY — it is never transmitted; the raw username/password
// you pass here are exactly what your OWN prior client.Connect(...) call
// already sent to the broker, unchanged.
//
// On success: returns a *SecuredClient, a drop-in replacement for the raw
// client at every Subscribe/Publish/Serve/Call call site — every existing
// call site keeps working exactly as it does with a raw MQTTClient, since
// *SecuredClient satisfies MQTTClient transparently via struct embedding.
//
// On failure: returns (nil, ConnectSecurityCredentialError) — client is
// NEVER touched or used; the caller should treat this as fatal (do not
// attempt Subscribe/Publish with the unwrapped raw client either — the
// whole point is that a malformed credential should never reach the
// broker in the first place).
//
// A nil scheme.Codec is a no-op (matches the "nil Codec means no format
// validation" contract used everywhere else in the security model) —
// NewSecuredClient always succeeds in that case.
//
// go-codex still never calls Connect() itself — connect first, THEN wrap.
// Recommendation: also declare the SAME requirement via [events.Server.Security]/
// [reqreply.Server.Security] when building an AsyncAPI spec for this
// connection, purely for documentation parity between the spec output and
// this runtime check — Server.Security itself has no code-level link to
// ConnectSecurityScheme/NewSecuredClient.
//
//	client := paho.NewClient(...)
//	if _, err := client.Connect(ctx, &paho.Connect{
//	    Username: "svc-account", Password: []byte(token),
//	}); err != nil { /* handle */ }
//
//	secured, err := mqtt5.NewSecuredClient(client, bearerAuth, "svc-account", token)
//	if err != nil { /* malformed credential — client is never used */ }
//
//	mqtt5.Subscribe(ctx, secured, router, handle, fn, opts) // works exactly as before
func NewSecuredClient(client MQTTClient, scheme ConnectSecurityScheme, username, password string, opts ...SecuredClientOption) (*SecuredClient, error) {
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
	return &SecuredClient{MQTTClient: client}, nil
}

// ConnectSecurityCredentialError is returned by [NewSecuredClient] when the
// combined username/password fails scheme.Codec's validation. The
// underlying client is NEVER touched when this error is returned.
//
// Use [errors.As] to extract the scheme and underlying constraint error:
//
//	var credErr mqtt5.ConnectSecurityCredentialError
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
