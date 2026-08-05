# Security Scheme Parity — `api/events`, `api/reqreply`, `api/mcp`

> **Status:** Design draft — awaiting confirmation on Phase 4 (MCP) before implementation.
> [← Back to Roadmap](index.md)
>
> See also: [Security & Authentication](../features/security.md) (`api/rest`'s shipped symmetric security model)

## Motivation

`api/rest` already ships a fully symmetric security model (see
[Security & Authentication](../features/security.md)): a route
declares its security scheme ONCE (`rest.WithSecurityScheme`), and BOTH the
server (`Route.Register` → `adapters/nethttp.Handler`) and the client
(`Route.ClientHandle` → `adapters/nethttp.Call`) enforce/validate the SAME
credential format from that one declaration. This doc investigates what
"the same parity" would concretely mean for `api/events`, `api/reqreply`,
and `api/mcp` — and finds each layer starts from a very different place, so
this is NOT one uniform migration; it's three separate efforts of very
different size, phased accordingly.

## Current state per layer (investigated this round)

| Layer | Scheme declaration | Server-side enforcement | Client-side credential mechanism |
|---|---|---|---|
| `api/events` | `events.SecurityScheme{route.SecurityScheme + Codec}` + `Builder.AddSecurityScheme(name, scheme)` — **builder-level only**, mirrors REST's OLD (pre-Round-92) design | `adapters/mqtt`/`mqtt5`'s `SubscribeOptions.SecurityFunc` — subscribe-side only, already validates format via `ChannelHandle.SecuritySchemes` before calling `SecurityFunc`, same shape as REST's `Handler` | **NONE.** `adapters/mqtt`/`mqtt5`/`zeromq`'s `PublishOptions` has no `CredentialFunc`-equivalent field at all — publishing a message has no per-call credential injection point today |
| `api/reqreply` | **NONE.** No `SecurityScheme` type, no `Security` field on `RouteMeta`, nothing | **NONE.** `mqtt5.reqreply.ServeOptions` has no `SecurityFunc` | **NONE.** `mqtt5.reqreply.CallOptions` has no `CredentialFunc` |
| `api/mcp` | **NONE**, and explicitly by design — see Gotcha in `review-go-codex/SKILL.md`: *"`api/mcp` has no security methods. No `AddSecurityScheme`, `AddGlobalSecurity`, or `SecurityFunc` — MCP security is handled separately and not part of `api/mcp`."* | **NONE** | **N/A** — go-codex has no "MCP client" concept; `adapters/mcpgo` only SERVES tools, it never calls one |

**Key implication:** only `api/events` has anything to genuinely "migrate."
`api/reqreply` needs a brand-new feature (routes have never had a security
concept). `api/mcp` reversing its documented "security is out of scope"
decision is a policy question, not an implementation gap — flagged
explicitly in Phase 4 below, requiring sign-off before any code is written
for it.

## Scope decisions (what's in Phase 1–4, deliberately phased by size/risk)

| Phase | Layer | What ships | Size |
|---|---|---|---|
| 1 | `api/events` | `events.WithSecurityScheme(name, scheme) ChannelOpt` — channel-level declaration, consumed by BOTH `Channel.Register` and (new) `Channel.ClientHandle`-equivalent path; remove `Builder.AddSecurityScheme` (breaking, matches REST's precedent) | Small — pure declaration-site migration, no new runtime mechanism |
| 2 | `api/events` + `adapters/mqtt`/`mqtt5`/`zeromq` | Add a genuine publish-side `CredentialFunc`-equivalent (new `PublishOptions.CredentialFunc` per adapter) + a symmetric client-side format check in each `Publish`/`PublishHandle`, mirroring `nethttp.Call`'s Round-92 check | Medium-large — net-new capability across 3 adapters, no existing mechanism to build on |
| 3 | `api/reqreply` + `adapters/mqtt5/reqreply` | Add `Security []route.SecurityRequirement` to `reqreply.RouteMeta`, a `reqreply.SecurityScheme` type + `WithSecurityScheme`, `CredentialFunc` on `mqtt5.reqreply.CallOptions` (client `Call`), `SecurityFunc` on `ServeOptions` (server) — essentially replaying REST's whole security feature end-to-end for reqreply | Large — full net-new feature, no existing scaffolding at all |
| 4 | `api/mcp` + `adapters/mcpgo` | `apimcp.SecurityScheme` + `WithSecurityScheme`, tool-level `Security`, `SecurityFunc` hook in `adapters/mcpgo.ToolHandler` | **BLOCKED on explicit user sign-off** — reverses a deliberate, documented design decision; not a routine parity gap |

## Toolchain / dependency decisions

No new external dependencies for any phase — this stays within existing
`route.SecurityScheme`/`codex.Codec[string]` primitives, exactly like REST's
implementation. Phase 2's per-adapter `CredentialFunc` additions each stay
within that adapter's existing client dependency (Paho MQTT client for
`mqtt`/`mqtt5`, ZeroMQ socket for `zeromq`) — no protocol-level changes,
purely an additive options field plus a pre-send validation step, exactly
mirroring `nethttp.Call`'s Round-92/93 implementation.

## API surface (sketch — exact signatures to firm up per phase during implementation)

### Phase 1 — `api/events`

```go
// api/events/builder.go

// WithSecurityScheme declares scheme's spec metadata and optional Codec for
// THIS channel. Mirrors rest.WithSecurityScheme exactly — the ONLY way to
// declare a security scheme after this ships; Builder.AddSecurityScheme is
// removed (breaking change, matching api/rest's precedent).
func WithSecurityScheme(name string, scheme SecurityScheme) ChannelOpt

// channelBuilder gains: securitySchemes map[string]SecurityScheme
// Channel.Register(b) and Channel.ClientHandle() (if one is added — see
// open question below) both populate ChannelHandle.SecuritySchemes from
// this declaration.
```

**Open question for Phase 1:** does `api/events.Channel` even have a
`ClientHandle()`-equivalent today (a way to get a `*ChannelHandle` without a
`Builder`, for `Publish`/`Subscribe` calls that don't need an AsyncAPI
spec)? Investigate during implementation — if it exists, mirror REST's
dual population (`Register` + `ClientHandle`); if not, Phase 1 only needs
to fix `Register`'s builder-level dependency, since there's no
builder-free path to also fix.

### Phase 2 — publish-side `CredentialFunc` (all three pub/sub adapters)

```go
// adapters/mqtt/adapter.go (mirror in mqtt5, zeromq)

type PublishOptions struct {
    Observer stats.Observer

    // CredentialFunc, when non-nil, is called for channels declaring
    // non-nil Security, mirroring nethttp.CallOptions.CredentialFunc
    // exactly. Must return a value to inject into the outgoing message —
    // TBD during implementation whether this is an MQTT 5 User Property,
    // a payload envelope field, or something else, since MQTT has no
    // header concept per-message the way HTTP does. This is the crux of
    // why Phase 2 is "medium-large," not "small": the injection point
    // itself needs designing, unlike REST where Authorization is an
    // obvious, standard HTTP header.
    CredentialFunc func(ctx context.Context, reqs []route.SecurityRequirement) (map[string]string, error)
}
```

**Open question for Phase 2:** MQTT 3.1.1 (`adapters/mqtt`) has NO
per-message metadata channel at all (no User Properties — that's MQTT 5
only) — so `mqtt.PublishOptions.CredentialFunc` would have nowhere to
inject a credential except the PAYLOAD itself (mixing transport concerns
into the application payload) or a reserved topic segment. `adapters/mqtt5`
CAN use MQTT 5 User Properties (already used for
`UserPropertyParams`/`SecurityFunc` on the subscribe side). `adapters/zeromq`
has no headers at all (raw frames only) — same problem as MQTT 3.1.1. This
needs a per-transport design decision, possibly concluding that Phase 2 is
MQTT-5-only for now (skip `mqtt`/`zeromq` until a concrete need + design
appears) — **flag as an open design decision, not a blocker for planning,
but a real scoping question before implementation starts.**

### Phase 3 — `api/reqreply`

```go
// api/reqreply/route.go

type SecurityScheme struct {
    route.SecurityScheme
    Codec *codex.Codec[string]
}
func (s SecurityScheme) WithCodec(c codex.Codec[string]) SecurityScheme

func WithSecurityScheme(name string, scheme SecurityScheme) RouteOpt

// RouteMeta gains:
//   Security []route.SecurityRequirement

// RouteHandle gains:
//   SecuritySchemes map[string]SecurityScheme
//   GlobalSecurity  []route.SecurityRequirement   (if reqreply gains AddGlobalSecurity too)
```

```go
// adapters/mqtt5/reqreply.go

type CallOptions struct {
    // ... existing fields ...
    CredentialFunc func(ctx context.Context, reqs []route.SecurityRequirement) (map[string]string, error)
}

type ServeOptions struct {
    // ... existing fields ...
    SecurityFunc func(ctx context.Context, msg *pahomqtt5.Publish, reqs []route.SecurityRequirement) error
}
```

Same MQTT-5-User-Property injection question as Phase 2 applies here too
(reqreply is mqtt5-only today, so this is simpler — no `mqtt`/`zeromq`
variant to design around).

### Phase 4 — `api/mcp` (BLOCKED on sign-off)

Sketch only, not to be implemented without explicit confirmation:

```go
// api/mcp/builder.go
type SecurityScheme struct {
    route.SecurityScheme
    Codec *codex.Codec[string]
}
func WithSecurityScheme(name string, scheme SecurityScheme) ToolOpt

// ToolMeta gains: Security []route.SecurityRequirement
```

```go
// adapters/mcpgo/adapter.go
type Options struct {
    // ... existing fields ...
    SecurityFunc func(ctx context.Context, args map[string]any, reqs []route.SecurityRequirement) error
}
```

**Why this needs sign-off, not just a plan:** the existing Gotcha
documents this as INTENTIONAL — MCP's security model is handled by the
HOST application (e.g., the MCP client's OAuth flow, or transport-level
auth like a Bearer token on the SSE/stdio transport itself), not by
per-tool credential validation inside go-codex. Adding this reverses that
stance and needs the maintainer to explicitly decide it's now wanted,
not just infer it from "parity with REST."

## Structured errors (all implement `slog.LogValuer`)

Each phase reuses the EXACT SAME error shape REST already has —
`SecurityCredentialError{Scheme, Err}` / `SecurityError{Err}` — but as a
NEW type per package (`events.SecurityCredentialError`, `events.SecurityError`,
`reqreply.SecurityCredentialError`, `reqreply.SecurityError`, and — if Phase
4 proceeds — `apimcp.SecurityCredentialError`/`apimcp.SecurityError`),
matching the existing pattern where each API layer keeps its own parallel
error vocabulary (same rationale as `RouteMeta`/`ChannelMeta` staying
separate types per layer, not shared).

## Observer integration

Reuses `stats.SecurityObserver.RecordSecurityRejection(location, scheme string)`
unchanged for all phases — already generic enough (`location` becomes the
topic template for events/reqreply, the tool name for mcp).

## Unit test plan (per phase, mirrors `api/rest`'s shipped pattern (see docs/features/security.md))

| Phase | Tests |
|---|---|
| 1 | `TestWithSecurityScheme_events_Register_PopulatesSecuritySchemes`, `TestWithSecurityScheme_events_ClientHandle_PopulatesSecuritySchemes` (if applicable), `TestAsyncAPISpec_AggregatesSecuritySchemesFromChannels` |
| 2 | Per adapter: `TestPublish_CredentialFunc_ValidFormat_Passes`, `TestPublish_CredentialFunc_MalformedFormat_ReturnsSecurityCredentialError`, `TestPublish_CredentialFunc_ReturnsNilHeader_SkipsValidation` (the EXACT regression class Round 93 found for REST — must be covered from day one here) |
| 3 | Mirrors REST's full test matrix: `Call`/`Serve` happy path, malformed credential, nil-CredentialFunc-not-an-error, `SecurityFunc` rejection |
| 4 | Only if Phase 4 proceeds: tool-call equivalent of the above |

## Files to create / modify (per phase, high-level)

| Phase | Files |
|---|---|
| 1 | `api/events/builder.go`, `api/events/builder_test.go`, migrate existing `AddSecurityScheme` call sites (`adapters/mqtt/adapter_test.go` — 2 sites found this session, `examples/adapters-mqtt-security`, `examples/api-events`, `examples/event-driven`) |
| 2 | `adapters/mqtt/adapter.go`+`_test.go`, `adapters/mqtt5/adapter.go`+`_test.go`, `adapters/zeromq/adapter.go`+`_test.go` (or MQTT-5-only, per the open design decision) |
| 3 | `api/reqreply/route.go`+`_test.go`, `adapters/mqtt5/reqreply.go`+`_test.go` |
| 4 | `api/mcp/builder.go`+`_test.go`, `adapters/mcpgo/adapter.go`+`_test.go` — ONLY after sign-off |

## Out of scope

- Any transport OTHER than MQTT/MQTT5/ZeroMQ/MCP getting a security
  mechanism it doesn't have today (e.g., `adapters/sql`, `adapters/redis`,
  `adapters/file`) — none of those are request/reply or pub/sub protocols
  with a credential-per-call concept in the first place.
- Retry/refresh semantics for any of these — that's
  [credential-caching.md](credential-caching.md)'s scope, orthogonal to
  this doc.

## Open design decisions (to resolve before/during implementation)

- **Does `api/events.Channel` have a `ClientHandle()`-equivalent today?**
  Investigate first — determines whether Phase 1 needs to fix one code
  path (`Register`) or two.
- **Where does a publish-side credential get injected for MQTT 3.1.1 and
  ZeroMQ**, which have no per-message header/property concept? Likely
  conclusion: Phase 2 ships MQTT-5-only (User Properties) initially, with
  `mqtt`/`zeromq` left as a documented gap until a concrete transport-level
  design exists.
- **Phase 4 sign-off** — must be explicitly granted before any `api/mcp`
  code is written; treat as BLOCKED, not merely "last in the queue."
