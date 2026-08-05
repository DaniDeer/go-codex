# Connect-Level Credential Codec — `adapters/mqtt` + `adapters/mqtt5`

> **Status:** Design draft — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [Security & Authentication](../features/security.md) (`api/rest`'s shipped symmetric security model; the "Connection-level vs message-level security" section explains where this feature fits)

## Motivation

MQTT connections (both 3.1.1 and 5.0) are long-lived, stateful TCP
sessions: the caller connects ONCE (`client.Connect(...)`, entirely
outside go-codex — `MQTTClient`/`pahomqtt.Client` deliberately expose no
connection-lifecycle methods to the adapters), then reuses that SAME
client across many `Subscribe`/`Publish`/`Serve`/`Call` invocations for
the program's lifetime.

Today, go-codex's security model operates entirely at the MESSAGE level
(`events.WithSecurityScheme`/`reqreply.WithSecurityScheme` +
`SecurityFunc`/`CredentialFunc`, shipped in Round 99) — appropriate when
ONE connection carries traffic for MULTIPLE logical identities (e.g. a
gateway relaying many devices over one broker connection). But the
message-level mechanism is MQTT5-only, since it depends on MQTT 5 User
Properties — MQTT 3.1.1 has no per-message metadata channel at all.

CONNECTION-level security (broker ACLs tied to CONNECT-time
username/password/TLS cert) already works today with ZERO go-codex
involvement, configured entirely in the caller's own connect code. But
there is no codec-validated, go-codex-native way to check that a
connect-time credential is well-formed BEFORE it's used — unlike the
message-level model, which validates format via a `Codec[string]` before
a message is ever sent or accepted.

**Key insight that makes this protocol-symmetric** (unlike the
message-level model): both `github.com/eclipse/paho.mqtt.golang`
(`ClientOptions.SetUsername`/`SetPassword`, MQTT 3.1.1) and
`github.com/eclipse/paho.golang/paho` (`Connect.Username`/`Password`,
MQTT 5.0) carry username/password on the CONNECT packet. A connect-level
credential codec therefore works IDENTICALLY for both adapters — closing
the asymmetry gap found while reviewing Round 99's message-level parity
work, but from a completely different (and better-fitting) angle than
trying to force per-message parity onto MQTT 3.1.1.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `ConnectSecurityScheme` type (`route.SecurityScheme` + `Codec *codex.Codec[string]`) — the connect-level analogue of `events.SecurityScheme`/`reqreply.SecurityScheme` | go-codex ever calling `Connect()` itself — the caller ALWAYS connects their own client first; this feature validates a credential the caller already obtained, nothing more |
| `SecuredClient` wrapper type (both `adapters/mqtt` and `adapters/mqtt5`) + `NewSecuredClient(client, scheme, credential) (*SecuredClient, error)` constructor — validates ONCE, synchronously, at construction | A global/memoized per-client-instance validation mechanism (**Design 1**, rejected — see below) |
| Both `adapters/mqtt` (3.1.1) and `adapters/mqtt5` — protocol-symmetric, unlike the message-level feature | `Server.Security` (existing AsyncAPI spec-only field) integration — left untouched for now; this feature has no spec representation (see Open design decisions) |
| A new structured error for a malformed connect-level credential | Any specific auth PROTOCOL (OAuth2 flows, JWT parsing, TLS cert validation) — same "bring your own protocol, we validate the resulting string's FORMAT" boundary as the message-level model |

### Rejected alternative — Design 1: global per-client-pointer memoization

An earlier sketch considered a package-level cache (e.g. `sync.Map`) keyed
by the `MQTTClient` interface value's identity, with validation triggered
lazily on the FIRST `Subscribe`/`Publish`/`Serve`/`Call` call against a
given client — achieving "zero code at the call site" the same way
`WithSecurityScheme` does (since `Subscribe`/etc. are go-codex-owned entry
points that could transparently check the cache).

**Rejected** in favor of the explicit wrapper (Design 2, below) because:

- It introduces hidden global mutable state keyed by pointer identity — a
  real code smell requiring careful concurrent-access design (a "first
  call" race between concurrent `Subscribe`+`Publish` on the same fresh
  client) and unresolved cache-eviction questions (does an entry ever get
  removed? What bounds the cache's size for a process that creates many
  short-lived clients?).
- It requires a NEW options field on ALL FOUR entry points
  (`SubscribeOptions`/`PublishOptions`/`ServeOptions`/`CallOptions`) in
  BOTH adapters (8 structs total) just to carry the credential-fetch
  mechanism through to wherever the lazy check happens.
- The explicit wrapper achieves the SAME practical ergonomics — "declare
  the scheme once, wrap the client once, every call site afterward is
  unchanged" — with none of the above complexity, at the cost of one
  explicit line of code (`NewSecuredClient(...)`) the caller writes right
  after their own `Connect()` call. This is directly in line with
  `docker/registry`'s own proven pattern in this codebase
  (`newAuthCredentialFunc`'s `sync.Once`-memoized closure, constructed once
  per top-level call) — this feature is the SAME idea, just constructed
  once per CONNECTION instead of once per top-level call.

## Chosen design — Design 2: explicit, eagerly-validated wrapper

Since `mqtt5.MQTTClient` and `pahomqtt.Client` (mqtt 3.1.1) are BOTH
interfaces, `SecuredClient` uses Go struct embedding to get every method
promoted automatically — zero manual per-method delegation — and
validation happens ONCE, SYNCHRONOUSLY, at construction (no `sync.Once`,
no lazy first-call race, no memoization/eviction concerns at all: the
whole mechanism is a single function call with a single success/failure
outcome, evaluated exactly once).

```go
// adapters/mqtt5/connect_security.go (new file)

// ConnectSecurityScheme combines route.SecurityScheme spec metadata with a
// runtime Codec for connect-level (CONNECT username/password) credential
// validation — the connection-level analogue of events.SecurityScheme /
// reqreply.SecurityScheme, validated ONCE at wrap time (construction), not
// once per message.
//
// Use ConnectSecurityScheme.WithCodec to set the Codec field inline without
// a temporary variable, same idiom as events.SecurityScheme/reqreply.SecurityScheme.
type ConnectSecurityScheme struct {
    route.SecurityScheme
    Codec *codex.Codec[string]
}

func (s ConnectSecurityScheme) WithCodec(c codex.Codec[string]) ConnectSecurityScheme {
    s.Codec = &c
    return s
}

// SecuredClient wraps an already-connected MQTTClient. Every MQTTClient
// method is promoted automatically via struct embedding — a *SecuredClient
// behaves IDENTICALLY to the wrapped client in every respect except that
// its credential has already been validated by the time it exists.
type SecuredClient struct {
    MQTTClient
}

// NewSecuredClient validates credential against scheme.Codec ONCE,
// synchronously. On success, returns a *SecuredClient ready to pass to
// Subscribe/Publish/Serve/Call UNCHANGED — every existing call site keeps
// working exactly as it does with a raw MQTTClient, since *SecuredClient
// satisfies MQTTClient transparently.
//
// On failure, returns (nil, ConnectSecurityCredentialError) — client is
// NEVER touched or used; the caller should treat this as fatal (do not
// attempt Subscribe/Publish with the unwrapped raw client either — the
// whole point is that a malformed credential should never reach the
// broker in the first place).
//
// A nil scheme.Codec is a no-op (matches the "nil Codec means no format
// validation" contract used everywhere else in the security model) —
// NewSecuredClient always succeeds in that case.
//
// go-codex still never calls Connect() itself — connect first, THEN wrap:
//
//	client := paho.NewClient(...)
//	if _, err := client.Connect(ctx, &paho.Connect{
//	    Username: "svc-account", Password: []byte(token),
//	}); err != nil { /* handle */ }
//
//	secured, err := mqtt5.NewSecuredClient(client, bearerAuth, "Bearer "+token)
//	if err != nil { /* malformed credential — client is never used */ }
//
//	mqtt5.Subscribe(ctx, secured, router, handle, fn, opts) // works exactly as before
func NewSecuredClient(client MQTTClient, scheme ConnectSecurityScheme, credential string) (*SecuredClient, error) {
    if scheme.Codec != nil {
        if err := scheme.Codec.Validate(credential); err != nil {
            return nil, ConnectSecurityCredentialError{Scheme: scheme, Err: err}
        }
    }
    return &SecuredClient{MQTTClient: client}, nil
}
```

An identical `mqtt.SecuredClient`/`mqtt.NewSecuredClient` ships for MQTT
3.1.1, wrapping `pahomqtt.Client` — the exact same shape, since the
CONNECT-time credential concept (unlike message-level User Properties) is
protocol-symmetric.

**"Declare once, use everywhere" is satisfied**: declare
`ConnectSecurityScheme` once as a shared package-level value (same idiom
as `events.SecurityScheme`/`reqreply.SecurityScheme`/`rest.SecurityScheme`),
wrap the client once (right after `Connect()`), and every
`Subscribe`/`Publish`/`Serve`/`Call` call site afterward is COMPLETELY
UNCHANGED — no special-casing, no new parameters, no per-call
validation overhead, since `*SecuredClient` satisfies `MQTTClient`
transparently via embedding.

## Structured errors (implements `slog.LogValuer`)

```go
// ConnectSecurityCredentialError is returned by NewSecuredClient when
// credential fails scheme.Codec's validation. The underlying client is
// NEVER touched when this error is returned.
//
// Use errors.As to extract the scheme and underlying constraint error:
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

func (e ConnectSecurityCredentialError) Unwrap() error { return e.Err }

func (e ConnectSecurityCredentialError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("scheme_type", string(e.Scheme.Type)),
        slog.Any("err", e.Err),
    )
}
```

Note the error embeds the WHOLE `ConnectSecurityScheme` value (its
`Type`/`Scheme`/`Name` fields render directly in `LogValue()`) rather than
a bare string name — there is no name-registry to look up a name FROM,
unlike `SecurityCredentialError.Scheme` (populated from
`WithSecurityScheme`'s map key). This avoids inventing a redundant naming
concept just for error identification.

## Observer integration

`NewSecuredClient` accepts an optional `stats.Observer` so
`RecordSecurityRejection` can still fire on failure, following the
existing `SecurityObserver` extension (no new observer interface needed):

```go
func NewSecuredClient(
    client MQTTClient,
    scheme ConnectSecurityScheme,
    credential string,
    opts ...SecuredClientOption,
) (*SecuredClient, error)

// SecuredClientOption configures NewSecuredClient.
type SecuredClientOption func(*securedClientOptions)

// WithObserver sets the Observer NewSecuredClient reports
// RecordSecurityRejection to on validation failure.
func WithObserver(obs stats.Observer) SecuredClientOption
```

`RecordSecurityRejection(location, scheme string)` is called with
`location = "connect"` (a fixed string — there is no topic/route yet at
construction time, unlike the message-level calls which have a concrete
topic) and `scheme = string(scheme.Type)`.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestNewSecuredClient_ValidCredential_ReturnsWrapper` | Well-formed credential → non-nil `*SecuredClient`, nil error |
| `TestNewSecuredClient_MalformedCredential_ReturnsError` | Malformed credential → nil wrapper, `ConnectSecurityCredentialError` with correct `Scheme`/`Err` |
| `TestNewSecuredClient_NilCodec_NoOp` | `scheme.Codec == nil` → always succeeds, matching the "nil Codec = no validation" contract elsewhere |
| `TestNewSecuredClient_MalformedCredential_ClientNeverUsed` | On failure, the underlying client's methods are never invoked (mock client asserts zero calls) |
| `TestSecuredClient_SatisfiesMQTTClient` | Compile-time assertion: `var _ MQTTClient = (*SecuredClient)(nil)` |
| `TestSecuredClient_TransparentDelegation` | `Subscribe`/`Publish` through a `*SecuredClient` behave IDENTICALLY to the raw client — re-run a representative slice of existing `Subscribe`/`Publish` tests wrapped in a `SecuredClient` to prove transparency |
| `TestNewSecuredClient_Observer_RecordsSecurityRejection` | `WithObserver` + malformed credential → `RecordSecurityRejection("connect", scheme.Type)` called exactly once |
| `TestNewSecuredClient_NilObserver_NoPanic` | No `WithObserver` option → no panic on failure |
| `TestConnectSecurityCredentialError_LogValue` | `LogValue()` returns `slog.KindGroup` with `scheme_type`/`err` keys |
| `TestConnectSecurityCredentialError_ErrorsAs` | `errors.As` reaches the underlying constraint error via `Unwrap()` |

Mirrored 1:1 in `adapters/mqtt` for MQTT 3.1.1.

## Files to create

| File | Responsibility |
|---|---|
| `adapters/mqtt5/connect_security.go` | `ConnectSecurityScheme`, `SecuredClient`, `NewSecuredClient`, `SecuredClientOption`/`WithObserver`, `ConnectSecurityCredentialError` |
| `adapters/mqtt5/connect_security_test.go` | Full unit test matrix above |
| `adapters/mqtt/connect_security.go` | Identical shape, wrapping `pahomqtt.Client` |
| `adapters/mqtt/connect_security_test.go` | Full unit test matrix above |

## Example update

Extend `examples/adapters-mqtt5` (Demo 3, which already demonstrates
message-level `WithSecurityScheme`/`CredentialFunc`/`SecurityFunc`) with a
short connect-level section showing `NewSecuredClient` wrapping the mock
broker client, contrasting it directly against the message-level demo
already there.

## Out of scope

- Design 1 (global per-client memoization) — rejected, see above.
- `Server.Security` (existing AsyncAPI spec-only field) integration — no
  code-level linkage planned; this feature has no spec/AsyncAPI
  representation of its own (see open design decisions).
- Any specific auth protocol implementation (OAuth2, JWT, mTLS cert
  validation) — same "bring your own protocol, validate the resulting
  string's FORMAT only" boundary as the message-level model.
- Async/refreshable connect-level credentials (e.g. re-validating on a
  timer) — `SecuredClient` validates ONCE at construction; if a caller
  needs to rotate the connect-level credential, they reconnect and
  construct a NEW `SecuredClient` — orthogonal to
  [credential-caching.md](credential-caching.md)'s scope (which caches a
  message-level `CredentialFunc`'s return value across calls, not a
  connection's own credential).

## Open design decisions (to resolve before/during implementation)

- **Credential shape**: a single opaque `credential string` (as sketched
  above — simplest, also covers MQTT5 enhanced/SASL auth methods beyond
  plain username/password) vs. separate `username, password string`
  parameters (matches the literal CONNECT packet fields more closely, but
  doesn't generalize past basic auth). Lean toward the opaque string for
  symmetry with the message-level model's `Codec[string]` validation, but
  confirm before implementation.
- **Does `Server.Security` need any cross-reference at all?** It remains
  pure AsyncAPI documentation today, disconnected from any runtime
  mechanism. This feature could optionally recommend (in godoc only, not
  code) declaring the same requirement via `Server.Security` for spec
  documentation purposes, purely as a convention — needs a decision on
  whether that's worth mentioning or just leaving fully separate.
- **Naming**: `SecuredClient`/`NewSecuredClient` vs. alternatives (e.g.
  `AuthenticatedClient`, `ValidatedClient`) — confirm the chosen name reads
  well alongside `MQTTClient`/`MQTTRouter` in both adapters' godoc.
