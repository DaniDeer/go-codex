# Declarative Middleware as Partial Route/Channel Definitions — and Security Credentials as the Proving Case

> **Status:** Design draft — all open design decisions resolved (see
> "Open design decisions" below); not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

This doc has two layers, by explicit user direction:

1. **The general principle** — across `api/rest`, `api/events`, AND
   `api/reqreply`, a `Middleware[In,Out]` value is not a bolt-on
   decorator; it is **a partial route/channel definition**. It declares
   its OWN merge fields (mapping `In`/`Out` struct fields to wire
   locations — header/cookie/query for REST, topic/property for
   events/reqreply) using the IDENTICAL constructors
   (`NewRequiredHeaderParam`/`NewRequiredTopicParam`/etc.) a route's or
   channel's own Req/Resp already uses, and its own handler Fn
   (`WithReceive`/`WithSend`). **Stacking N such partial definitions on
   top of the route/channel's own declaration aggregates them into ONE
   final, complete declaration.** This is not a metaphor or aspiration —
   confirmed via code, it is EXACTLY what `applyParamDeclarations`
   (`api/rest/middleware.go`, mirrored in `api/events`/`api/reqreply`)
   already does today: it builds independent per-kind contribution maps,
   merging the route's/channel's OWN declared params with EVERY ATTACHED
   MIDDLEWARE's contributed params into one spec, with conflict detection
   (`ConflictingSecurityDeclarationError`/`DuplicateMiddlewareNameError`)
   across layers — exactly the behavior a genuine "stacked partial
   declarations aggregate into one final declaration" model requires.
2. **The proving case** — Security credentials are the one place this
   principle is NOT followed today, across ALL THREE packages. A
   Security-flagged `Middleware[struct{},struct{}]` is dispatched through
   an entirely separate, adapter-shaped Fn contract
   (`http.Header`/`*http.Request` for REST; `[]UserProperty`/`*T` for
   events+reqreply's MQTT adapters) instead of using its OWN
   `WithReceive`/`WithSend` + merge-field vocabulary like every other
   middleware already does. This doc proposes fixing Security
   SPECIFICALLY, as the concrete, motivating demonstration that the
   general principle holds uniformly — not a security-only fix bolted on
   sideways.

## Confirmed via code: the general mechanism already exists, symmetrically, in all three packages

| Package | Request/Subscribe-side (decode) | Response/Publish-side (encode) | Merge-field vocabulary |
|---|---|---|---|
| `api/rest` | `buildDecodeIn` (`transform.go`) — decodes incoming header/cookie/query into `In` before `.WithReceive` | `buildEncodeIn` — encodes `In` into OUTGOING header/cookie/query via `.WithSend` (client); `buildEncodeOut`/`buildDecodeOut` handle the Resp/Out direction | `WithRequestHeader`/`WithRequestCookie`/`WithRequestQuery` (In); `WithResponseHeader`/`WithResponseCookie` (Out) |
| `api/events` | `buildDecodeIn` (`transform.go`) — decodes incoming topic/property vars into `In` before `.WithReceive(fn func(ctx, In) error)` (Subscribe) | `buildEncodeOut` — encodes `Out` into outgoing topic/property vars via `.WithSend(fn func(ctx) (Out, error))` (Publish) | `WithSubscribeTopic`/`WithSubscribeProperty` (In); `WithPublishTopic`/`WithPublishProperty` (Out) |
| `api/reqreply` | `buildDecodeIn`/`buildDecodeOut` (`transform.go`) | `buildEncodeIn`/`buildEncodeOut` — FULLY symmetric, mirroring REST's duplex (request/response) shape exactly | `WithRequestTopic`/`WithRequestProperty` (In); `WithResponseTopic`/`WithResponseProperty` (Out) |

Each package's directionality matches its OWN communication pattern
(REST/reqreply are duplex request+response; events' Subscribe/Publish
are each one-directional) — but the UNDERLYING model is identical in
all three: a merge-field-declared `In`/`Out`, encoded/decoded generically
by the dispatch layer, with a handler Fn in between. Security, in ALL
THREE packages, bypasses this entirely today.

## Architectural consequence: a SHARED, explicit layering mechanism — not just a documented convention

User direction: this layering must be **reflected in the architecture
and the mechanism the code introduces**, not merely documented as a
shared mental model while three independent implementations continue to
coincidentally look alike. Checked the actual duplication this implies
fixing — it is real and already self-acknowledged in the code:
`api/reqreply/transform.go`'s own doc comment on `buildDecodeIn` says,
verbatim, **"mirrors rest's identical function, adapted to reqreply's
two axes (topic, property) instead of REST's three (header, cookie,
query)"** — the SAME structural logic (iterate N wire-location axes,
`codex.DecodeVars`/`EncodeVars` against each axis's declared fields,
validate the whole value via `InCodec`/`OutCodec`, wrap failures in
`MiddlewareInputError`/`MiddlewareOutputError`) is hand-duplicated THREE
times today (`api/rest/transform.go`, `api/events/transform.go`,
`api/reqreply/transform.go`), differing only in axis COUNT (2 or 3) and
axis NAMES (header/cookie/query vs. topic/property).

**Proposed: extract this into ONE shared, generic mechanism in the
`middleware` package**, so "a layer is a partial declaration with
declared wire axes + a handler, aggregated with its siblings" becomes an
actual shared TYPE/FUNCTION every package's dispatch calls into — not
three independently-maintained look-alikes kept in sync by convention
and code-review vigilance alone.

```go
// middleware — NEW, shared across api/rest/api/events/api/reqreply.

// Axis describes ONE wire-location axis (e.g. "header", "topic") a
// Declaration[In,Out] value can merge a struct field against — the
// axis-generic form of what REST's reqHeaderParams/reqCookieParams/
// reqQueryParams (and events/reqreply's topic/property equivalents)
// already are per-package today, just not yet a SHARED type.
type Axis[T any] struct {
    Name   string // diagnostic only — "header", "cookie", "query", "topic", "property"
    Fields []codex.FieldCodec[T]
}

// DecodeLayer decodes a T from N axes' wire-value maps (each axis's OWN
// map — e.g. headerVars, cookieVars, queryVars — passed positionally,
// matched to axes by index), validates the assembled T via codec, and
// wraps any failure in a caller-supplied error constructor — the ONE
// shared implementation [rest.buildDecodeIn]/[events.buildDecodeIn]/
// [reqreply.buildDecodeIn] all become thin callers of.
func DecodeLayer[T any](
    name string,
    codec codex.Codec[T],
    newInputErr func(name string, err error) error,
    axesAndVars ...AxisVars[T],
) (T, error)

// EncodeLayer is DecodeLayer's encode-side mirror — validates T first,
// then derives each axis's wire-value map via codex.EncodeVars.
func EncodeLayer[T any](
    name string,
    codec codex.Codec[T],
    newOutputErr func(name string, err error) error,
    value T,
    axes ...Axis[T],
) ([]map[string]string, error) // one map per axis, same order as axes
```

Each package's OWN `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/
`buildDecodeOut` becomes a THIN wrapper: supply ITS OWN axis definitions
(REST: header/cookie/query; events/reqreply: topic/property) and ITS OWN
error constructor (`rest.MiddlewareInputError`/`events.
MiddlewareInputError`/`reqreply.MiddlewareInputError` — these stay
PACKAGE-SPECIFIC, since `errors.As` callers need to distinguish which
package's middleware failed), then delegate the actual decode/encode/
validate/wrap logic to the shared `middleware.DecodeLayer`/`EncodeLayer`
— eliminating the 3-way hand-duplication, not just documenting it away.

**This is a pure internal refactor with ZERO behavior change** — fully
verifiable by running EVERY existing test in all 3 packages' `transform_test.go`-equivalents unchanged before/after; the shared
mechanism must produce byte-identical results to today's 3 separate
implementations for every currently-passing test case. This is Phase 1,
and a hard PREREQUISITE for Phase 2 (Security's generalization) — once
`DecodeLayer`/`EncodeLayer` exist and all 3 packages delegate to them,
extending Security to use the SAME mechanism becomes a natural
consequence of the architecture, not a 4th hand-written variant to keep
in sync.

### Phase 1 sub-item: thread `codex`'s existing sparse-field capability through `EncodeVars`

Checked (via a "think outside the box" question about reusing
`codex.PartialField`/`PartialStruct`'s layering — the mechanism
`examples/go-edge-models/iotedge`'s manifest patches use) whether a
general codex-level layering primitive could ALSO serve this doc's
merge-field axes. Conclusion: `PartialStruct`/`PartialField` itself
solves a DIFFERENT, body-shaped problem (patch semantics within ONE JSON
object — confirmed a deliberately separate, parallel interface to
`FieldCodec[T]` per its own doc comment) and is not directly reusable
here. But the research surfaced a smaller, genuinely reusable, ALREADY
BUILT primitive:

- `codex/omitempty.go`'s `sparseFieldCodec[T]` is an ADDITIVE, OPTIONAL
  companion capability any `FieldCodec[T]` may implement (NOT a parallel
  type system) — `Struct`'s own `Encode` loop already checks for it
  (`codex/object.go:150`: `if sf, ok := f.(sparseFieldCodec[T]); ok {
  ...encodeSparse... }`), falling back to the normal `encode` otherwise.
  `OmitEmptyField`/`OmitEmptyFieldFunc`/`OmitDefaultField`/`MaybeField`
  all implement it today.
- **Confirmed gap**: `codex.EncodeVars` — the function underlying EVERY
  merge-field axis in all 3 packages (and this doc's own proposed
  `middleware.EncodeLayer`) — calls `f.encode(v)` unconditionally, with
  NO equivalent check (`codex/varfields.go`). No merge-field axis
  anywhere (header/cookie/query/topic/property) can express "omit this
  wire value entirely if unset" today — e.g. an optional
  `X-Idempotency-Key` header that should simply not be sent when unset.

**Folded into Phase 1's scope**: extend `codex.EncodeVars` to also check
for `sparseFieldCodec[T]`, mirroring `Struct`'s existing pattern exactly
— zero new codec concept, reusing `OmitEmptyField`/`MaybeField` as-is.
Once `middleware.EncodeLayer` is built on top of the now-extended
`EncodeVars` (Phase 1's own main item), every merge-field axis in all 3
packages inherits "omit if unset" support for free, including Security's
own credential merge fields (Phase 2) — e.g. an optional credential
sub-field that should be omitted from the wire entirely when the
`.WithSend` Fn didn't set it.

### Phase 1 sub-item: fail-fast vs. accumulate-all errors across stacked layers

Checked (via a question about modeling middleware as a `forge`-style
pipeline with an Either(Result,Error)-shaped error channel) whether
go-codex's existing control flow already matches this shape. Confirmed
it does, just via idiomatic Go `(T, error)` rather than an explicit
Either wrapper:

- `forge.Compose` chains functions sequentially, short-circuiting on the
  first error (`mid, err := f1.Apply(a); if err != nil { return
  ...err }`) — a classic railway/Either-shaped composition, already
  shipped.
- `api/rest.DispatchMiddlewareHandlers` (`api/rest/transform_dispatch.go`)
  already does exactly what "partial results flow into the resulting
  output" describes for middleware: it iterates N stacked layers
  sequentially, collecting EACH layer's own Out value independently into
  `outs[i]` (later merged into the response) — but SHORT-CIRCUITS the
  instant ANY layer's `DecodeIn` or Fn errors.

**The one genuine, previously-undecided asymmetry this surfaced**:
layers ACROSS a stack are fail-fast (stop at the first failing layer),
while fields WITHIN one layer's own axis decode already ACCUMULATE every
field's error into one `ValidationErrors` (via `codex.DecodeVars`/
`EncodeVars`, never stopping early). Literally introducing an
`Either[Result,Error]` wrapper type is NOT the recommended fix (go-codex
already prefers typed errors + `errors.As`, per its own established
"Six mandatory requirements" convention — a formal Either/Result
algebraic type would be a foundational, un-idiomatic departure for
little real benefit, since `(T, error)` already IS go-codex's Either).
**Folded into Phase 1's scope as an open design decision** (below):
should `middleware.DecodeLayer`'s dispatch across STACKED layers also
accumulate every layer's error (extending `ValidationErrors`' existing
precedent upward from "fields within an axis" to "layers within a
stack"), or is today's fail-fast-across-layers behavior deliberate and
should stay? Not pre-decided — a real behavior change either way,
needing its own confirmation before Phase 1 ships.

Note: literally reusing `forge.Function[In,Out]` as the middleware step
type itself was explicitly considered and rejected — it carries a
SHA-256 contract hash and governance metadata (author/approver/approval
date) built for KPI-computation governance, irrelevant ceremony for an
ordinary middleware layer. The two mechanisms share PHILOSOPHY (named,
typed, composed, short-circuiting steps), not code worth merging.

## The pivot, concretely — all three packages

**Today (REST):**
```go
var BearerAuthDeclaration = rest.SecurityMiddleware("bearerAuth", route.BearerScheme("JWT"), nil)
route.ClientMW(&BearerAuthDeclaration, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
    h := make(http.Header)
    h.Set("Authorization", "Bearer "+token) // hand-built, adapter-aware
    return h, nil
})
```

**Proposed (REST) — Security IS just a stacked partial declaration:**
```go
type BearerCredential struct{ Token string }

var BearerAuthDeclaration = rest.SecurityMiddleware[BearerCredential, struct{}](
    "bearerAuth", route.BearerScheme("JWT"), nil,
).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", bearerHeaderCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
))

route.Use(BearerAuthDeclaration.WithSend(func(ctx context.Context) (BearerCredential, error) {
    return BearerCredential{Token: token}, nil
}))
```
`BearerAuthDeclaration` contributes an `Authorization` header merge field
+ a credential-producing Fn — **exactly** the way any other
`.Use(someOtherMw)` middleware contributes an `X-Trace-Id` header merge
field + its own Fn today. No special case.

**Today (events/reqreply, mqtt5):**
```go
var BearerAuthDeclaration = events.SecurityMiddleware("bearerAuth", route.BearerScheme("JWT"), nil)
channel.PublishMW(&BearerAuthDeclaration, func(ctx context.Context, msg *T, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error) {
    return []mqtt5.UserProperty{{Key: "Authorization", Value: "Bearer " + token}}, nil
})
```

**Proposed (events/reqreply) — the SAME pivot, mqtt5's User Property carrier instead of HTTP headers:**
```go
type BearerCredential struct{ Token string }

var BearerAuthDeclaration = events.SecurityMiddleware[struct{}, BearerCredential](
    "bearerAuth", route.BearerScheme("JWT"), nil,
).WithPublishProperty(events.NewRequiredPropertyParam("Authorization", bearerPropertyCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
))

channel.Use(BearerAuthDeclaration.WithSend(func(ctx context.Context) (BearerCredential, error) {
    return BearerCredential{Token: token}, nil
}))
```
(`api/reqreply` mirrors this identically via `WithRequestProperty`/
`.WithSend(fn func(ctx) (In, error))`, matching its REST-like duplex
shape rather than events' publish-only shape.)

## Cross-protocol credential composition — a confirmed, already-supported capability

A natural question this design raises: can a middleware declared for an
`api/events` (or future `api/mcp`/`ports`) boundary use a COMPLETELY
DIFFERENT protocol internally to authenticate — e.g. an MQTT channel's
Security Fn performing an OAuth2 client-credentials flow over REST/HTTP
before publishing? **Yes, confirmed — and not a new capability this doc
introduces, but a direct, structural consequence of a design choice
already in place today.**

**Already-shipped proof, not a hypothesis**:
`examples/go-edge-models/app/registry/auth.go`'s `authenticate(ctx,
httpClient, registryHost, repository, creds, obs)` ALREADY makes a real
REST/HTTP call (`rest.CallWithTransport(ctx, pingTransport, pingHandle,
struct{}{}, ...)` against a `PingRoute`) INSIDE a function called from
`newAuthCredentialFunc` — the Security credential Fn attached to
`GetTagsRoute`'s REST route via `.ClientMW(...)`. This is a
same-protocol (REST authenticating REST) instance of the SAME principle
that generalizes to cross-protocol use directly.

**Why this generalizes with ZERO new go-codex mechanism needed**: the
Security Fn — under BOTH the current mechanism AND this doc's own
redesign — is, and always has been, an ORDINARY GO CLOSURE
(`func(ctx context.Context) (Cred, error)` under `.WithSend`). go-codex's
middleware mechanism ONLY governs the DECLARATIVE SURFACE (scheme name,
scopes, and which wire location the resulting credential maps to via
merge fields) — it places ZERO constraints on the Fn's OWN internal
implementation. The Fn body can call any Go code, including
`rest.CallWithTransport` against a completely unrelated REST OAuth2
token endpoint, even though the CHANNEL being secured is MQTT/ZeroMQ.
This isn't a special case or an escape hatch — it is the direct,
structural consequence of the Fn being "just a closure," true in every
version of this mechanism.

**Illustrative sketch** (events channel authenticating via a REST OAuth2
token endpoint — NOT shipped code, a pattern sketch):

```go
// A REST route for an OAuth2 client-credentials token endpoint —
// completely unrelated to the events channel this will secure.
var tokenTransport = nethttp.NewClientTransport(nethttp.ClientTransportOptions{
    HTTPClient: httpClient, BaseURL: oauthProviderBaseURL,
})

// The events channel's credential Fn — an ordinary closure embedding a
// REST call, mirroring auth.go's authenticate() pattern exactly,
// including its sync.Once-based token caching (avoiding re-authenticating
// on every publish).
var once sync.Once
var cachedToken string
var tokenErr error

credFn := func(ctx context.Context) (BearerCredential, error) {
    once.Do(func() {
        resp, err := rest.CallWithTransport(ctx, tokenTransport, tokenHandle,
            TokenReq{GrantType: "client_credentials", ClientID: id, ClientSecret: secret})
        if err != nil {
            tokenErr = err
            return
        }
        cachedToken = resp.(TokenResp).AccessToken
    })
    if tokenErr != nil {
        return BearerCredential{}, tokenErr
    }
    return BearerCredential{Token: cachedToken}, nil
}

channel.Use(events.SecurityMiddleware[BearerCredential, struct{}](
    "oauth2", route.OAuth2Scheme(flows), scopes,
).WithPublishProperty(events.NewRequiredPropertyParam("Authorization", bearerPropertyCodec,
    func(c BearerCredential) string { return c.Token },
    func(c *BearerCredential, v string) { c.Token = v },
)).WithSend(credFn))
```

**MCP/ports — confirmed the same principle applies**:
`docs/roadmap/mcp-ports-declarative-middleware.md` already designs
`ports.RequireScopes[T]` to reuse the ALREADY-SHARED
`middleware.SecurityScheme`/`CheckScopes` directly — the SAME
Fn-is-a-plain-closure principle applies there: a `ports.File`
middleware's credential Fn could embed a REST/HTTP OAuth2 call before
permitting file access, for the identical structural reason. No special
plumbing is needed in that roadmap doc's design either, for the same
reason documented here.

## `api/events` middleware deep-dive: Observer + connection-level auth + message-level (cross-protocol) auth + context propagation

The user asked for all of this tied together specifically for
`api/events`: an Observer middleware, a pub/sub client's authentication
(both broker-native connection-level AND protocol-independent
message-level), and the context-propagation mechanism from the Phase 3
section above. Confirmed via code — these are not 2 or 3 competing
options, they are 3 DIFFERENT LAYERS of "secured, observed channel," and
(with one confirmed gap, below) they compose.

### Three layers, confirmed via code — and a real asymmetry between them

| Layer | Mechanism | Spec-rendered? | Protocol-independent? |
|---|---|---|---|
| **Observer** | `events.Observability[T]` — a general-purpose `func(next) func(ctx,T) error` closure, attached unpaired via `sub.SubscribeMW(nil, events.Observability[T](obs))`/`pub.PublishMW(nil, ...)`. No `In`/`Out` at all — confirmed, matches this doc's own earlier "Observer pattern ... has no IN/OUT structs, a special case" framing. | No — purely a runtime hook. | Yes — identical shape regardless of adapter. |
| **Connection-level auth** | `mqtt5.Connect(ctx, brokerURL, ConnectOptions{Username, Password})` — performs the REAL broker CONNECT handshake (confirmed via `adapters/mqtt5/connect.go`); `mqtt5.ConnectSecurityScheme`/`NewSecuredClient` is a SEPARATE, OPTIONAL format-only pre-check layered on top of an already-connected client, not the auth mechanism itself. | **Partially, and NOT linked to the runtime check** — see the confirmed gap below (closed by Phase 4). | **No — inherently protocol-native by definition** (CONNECT-time credentials are tied to the wire protocol's own handshake; MQTT's CONNECT packet fields have no cross-protocol equivalent to embed an unrelated OAuth2 call INTO). |
| **Message-level auth** | `Middleware[In,Out]` + `SecurityDeclaration()` + a credential `Fn` — the subject of this doc's Phase 2. | Yes, fully — one declaration drives both the AsyncAPI `security` list AND the runtime Fn. | **Yes, confirmed** — the Fn is an ordinary Go closure; see "Cross-protocol credential composition" above (the `auth.go` proof). |

**Correcting this doc's own earlier framing**: the user is right that
connection-level auth IS a legitimate middleware/capability concern under
this project's own design guardrails ("a capability is declared in the
API layer; an adapter satisfies it at attach") — it was wrong to treat it
as purely out-of-scope/adapter-only. Confirmed via code exactly where it
currently falls short of that guardrail:

### Phase 4: closing the connection-level auth gap — a real design, for `api/events` AND `api/reqreply`

Message-level Security (`Middleware[In,Out]` + `SecurityDeclaration()`)
already unifies spec and runtime behind ONE declaration — attach the
value once, both the AsyncAPI `security` requirement AND the runtime Fn
dispatch come from the SAME source. Connection-level auth did NOT have
this property — confirmed via code, and sharper than first thought:

- The SPEC side is `asyncapi/v3.Server.Security []route.SecurityRequirement`
  — a real, rendered field on the AsyncAPI Server object (confirmed:
  `render/asyncapi/v3/document.go`'s `Server` struct), populated via
  `events.Client.AddServer(name, events.Server{..., Security: [...]})` /
  `reqreply.Builder.AddServer(name, reqreply.ServerEntry{...})` at the API
  layer.
- The RUNTIME side is `mqtt5.Connect` (the function that actually dials
  and performs the CONNECT handshake — see "Reframed explicitly as a
  D-0006-pattern capability" below) and/or `mqtt5.ConnectSecurityScheme`/
  `NewSecuredClient` (a separate, optional, format-only pre-check layered
  on an already-connected client) — entirely separate code, living in
  the adapter package, invoked by the caller before passing the result
  to Subscribe/Publish/Serve/Call.
- **Sharper root cause, confirmed via code**: `events.Client.AsyncAPISpec()`'s
  own comment states *"there is no builder-level registry"* for security
  schemes — `components/securitySchemes` is populated ONLY by
  aggregating every CHANNEL's own `WithSecurityScheme` declarations
  (`reqreply.Builder.AsyncAPISpec()` has the IDENTICAL limitation, same
  wording, aggregating from ROUTES instead). **A connection-only scheme —
  referenced solely via `Server.Security`, used by NO individual
  channel/route — cannot be spec-registered at all today.** This is
  worse than "unlinked" — it's "spec-unrepresentable without an
  unrelated channel/route declaring the same scheme as a workaround."

**Confirmed this applies equally to `api/reqreply`, not just
`api/events`** (the motivating observation for this round): an
mqtt5-backed reqreply `Call`/`Serve` requires the identical
connect-then-authenticate sequence as events' Subscribe/Publish.
Confirmed via code: `adapters/mqtt5/reqreply_transport.go`'s `Call`/`Serve`
take a plain `MQTTClient` parameter — the EXACT SAME interface
`adapters/mqtt5`'s Subscribe/Publish already take — meaning the plain
`MQTTClient` `mqtt5.Connect` returns is ALREADY a drop-in for reqreply
too, with **zero adapter code change needed** (and so is
`*mqtt5.SecuredClient`, the OPTIONAL secondary pre-check wrapper, since
it promotes every `MQTTClient` method transparently via struct
embedding). Confirmed via `adapters/zeromq`: no connection-level
security construct exists there at all — this remains an mqtt5-only
concern, consistent with CONNECT-time credentials being inherently
protocol-native (D-0006's own conclusion, cross-referenced above).

#### The fix — one new, small, additive method per package; zero adapter changes

```go
// package events
//
// AddConnectSecurityScheme registers name/scheme directly into
// components/securitySchemes, independent of any channel's own
// WithSecurityScheme declaration — for a scheme used ONLY via a
// Server's connection-level Security list (see AddServer), never
// referenced by any individual channel's own Subscribe/Publish
// requirements. Collision policy matches every other registration on
// Client: last-registered-wins (no error returned), consistent with
// [Client.AddSchema]/[Client.AddServer].
//
// Reuse the SAME scheme's NAME, unchanged, when later supplying real
// credentials via the adapter's own attach-time mechanism — e.g.
// mqtt5.Connect(ctx, brokerURL, mqtt5.ConnectOptions{Username, Password})
// — closing the spec/runtime link via one reused declared value, not a
// new shared mechanism. See "Reframed explicitly as a D-0006-pattern
// capability" below for why this split (decoupled declare + sealed
// attach-time supply, no handler stage in between) is the right shape.
func (c *Client) AddConnectSecurityScheme(name string, scheme route.SecurityScheme) *Client
```

```go
// package reqreply — byte-identical shape and doc comment, same
// collision policy as [Builder.AddSchema]/[Builder.AddServer].
func (b *Builder) AddConnectSecurityScheme(name string, scheme route.SecurityScheme) *Builder
```

Both merge into the SAME aggregation each package's `AsyncAPISpec()`
already performs for channel/route-registered schemes — registered
BEFORE the channel/route loop runs, so a channel/route re-registering
the identical name still wins on collision (unchanged last-registered-
wins policy, now simply has a 3rd contributor instead of 2).

**No new error type** — mirrors `AddSchema`/`AddServer`'s existing
no-error-return, silently-overwrite-on-collision convention; introducing
one here would be inconsistent with every sibling registration method on
the same type.

#### Reframed explicitly as a D-0006-pattern capability — declare/attach split, no handler stage

Re-reading [D-0006](../design/d-0006-protocol-native-capabilities.md) §3's
own, already-RESOLVED 4-stage lifecycle model — (1) **declare**
(route/channel/port, adapter-agnostic), (2) **capability-declare**
(spec-contributing, still adapter-agnostic), (3) **handler-attach**
(business logic), (4) **adapter-attach** (concrete adapter supplied,
checked against stage 2) — and its chosen "Candidate 3" design (a
DECOUPLED, spec-only stage-2 sibling value + the ALREADY-PROVEN, sealed,
compile-time-checked stage-4 `Attach`-time supply, linked only by an
OPTIONAL, opt-in drift-check, never a compiler guarantee) — connection-
level auth is confirmed to fit this EXACT pattern, one granularity level
up from QoS/User Properties (which operate at the per-channel-operation
level via `SubscribeOptions`/`PublishOptions.Capabilities`):

- **Stage 2 (declare, adapter-agnostic)** = `AddConnectSecurityScheme`
  (unchanged from above) — declared near "new builder/new broker"
  construction, exactly where a connection-level concern belongs
  conceptually, with NO link yet to any concrete adapter.
- **Stage 4 (adapter-attach)** = `mqtt5.Connect`'s `ConnectOptions{
  Username, Password, ...}` — confirmed ALREADY EXISTS
  (`adapters/mqtt5/connect.go`) and ALREADY IS exactly "hand over
  credentials as attach-time options for the adapter." `ConnectOptions`
  is sealed to package `mqtt5` by ordinary Go function-signature
  scoping — there is no shared `[]Capability`-style slot here for a
  wrong adapter's value to be mistakenly accepted into, so (unlike
  QoS/Retained, which share ONE `SubscribeOptions.Capabilities []Capability`
  slot across potentially many capability kinds) a sealed marker-method
  interface would add ceremony with no corresponding safety gain. The
  safety D-0006's `Capability` interface buys via `isMQTT5Capability()`
  is achieved here for free, simply because `mqtt5.Connect` is a
  concrete, non-generic function living in one package.
- **There is NO stage 3 (handler-attach) for this capability — confirmed,
  not a gap.** This directly answers "how would the handler of a
  connection-auth middleware work before any adapter is attached": it
  wouldn't, because there is nothing for it to do. `Capability.Apply`
  already establishes the precedent that some capabilities have NO
  separate Fn/handler stage — the adapter-level action itself (`Apply`
  for QoS/Retained; `Connect` here) **is** the realization. A
  `Middleware[In,Out]`-shaped "handler" cannot exist for connection-level
  auth, for the same reason stated in the prior round (no per-message
  `In`/`Out`, no cross-protocol angle) — now given a principled citation
  instead of an ad-hoc observation: stages 3 and 4 simply collapse into
  one for any capability whose entire realization IS an adapter-level
  action with nothing upstream of it to enrich or validate.

#### Worked example — the full 2-stage flow, in order, both packages

```go
// STAGE 2 — declare, near client/builder construction. Adapter-agnostic;
// no concrete adapter exists yet at this point.
var brokerAuth = route.SecurityScheme{Type: route.SecuritySchemeHTTP, Scheme: "basic"}
eventsClient.AddConnectSecurityScheme("brokerAuth", brokerAuth)
reqreplyBuilder.AddConnectSecurityScheme("brokerAuth", brokerAuth)

eventsClient.AddServer("mqtt5", events.Server{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})
reqreplyBuilder.AddServer("mqtt5", reqreply.ServerEntry{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})

// ... later, at ATTACH time — STAGE 4, entirely adapter-owned. This is
// where real credentials are handed to the adapter, and where a real
// broker-rejection error surfaces, BEFORE Client.Attach is ever reached.
client, router, err := mqtt5.Connect(ctx, "broker:8883", mqtt5.ConnectOptions{
    ClientID: "svc-1", Username: username, Password: password,
})
if err != nil {
    var connErr mqtt5.ConnectError
    if errors.As(err, &connErr) {
        // handle dial failure vs. broker-rejected credentials here
    }
    return err
}
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})

// Client.Attach itself stays entirely protocol-agnostic — it never sees
// Username/Password, only the already-connected Transport value.
if err := eventsClient.Attach(transport); err != nil { /* handle */ }
```

**Optional, opt-in drift-check** (NOT designed further here, flagged by
analogy only): mirroring `CheckCapabilityCoverage`'s own precedented,
non-mandatory shape, a future helper COULD confirm the scheme NAME
declared via `AddConnectSecurityScheme` ("brokerAuth" above) matches the
name a caller intends when supplying `mqtt5.Connect`'s credentials — but
this is explicitly an OPT-IN safety net a caller could still forget to
call, not a compiler-enforced link, exactly the same honest trade-off
D-0006 itself accepts for `Capability`/`CheckCapabilityCoverage`. The
reused-Go-value convention shown above (the SAME `"brokerAuth"` string
literal at both call sites) is the PRIMARY safety mechanism today; the
drift-check would only catch a caller who let the two literals drift
apart, a secondary concern.

#### Two confirmed, real gaps in `mqtt5.Connect`'s error propagation — found via code, not assumed

1. **CONNACK reason code is silently discarded today.** Confirmed via
   `paho.golang`'s own `Client.Connect`: on an auth rejection (CONNACK
   reason code ≥ 0x80, e.g. `0x86` "Bad username or password", `0x87`
   "Not authorized"), paho returns BOTH a non-nil `*paho.Connack`
   (carrying `.ReasonCode`/`.Properties.ReasonString`) AND a generic
   `fmt.Errorf("failed to connect to server: %s", reason)`.
   `mqtt5.Connect`'s current body discards the `*Connack` return entirely
   (`if _, err := client.Connect(...); err != nil`) — the STRUCTURED
   reason is lost, only a generic string survives inside `ConnectError.Err`.
   **Proposed fix**: extend `ConnectError` with optional `ReasonCode byte`/
   `ReasonString string` fields, populated from the `*Connack` paho
   already returns (today discarded) whenever `Op == "connect"` and a
   non-nil `Connack` was received — zero-value for a "dial"-stage
   failure, where no CONNACK was ever received. `errors.As` callers can
   then branch on `ReasonCode` directly, without parsing the generic
   error string — this is the concrete mechanism that makes "propagating
   back connection errors" actionable, not just visible.
2. **No Observer integration.** `ConnectOptions` has no `Observer` field
   at all — unlike `NewSecuredClient`'s existing `WithObserver`/
   `stats.SecurityObserver.RecordSecurityRejection` pattern. **Proposed
   fix**: add an optional `Observer` field to `ConnectOptions`; on an
   auth rejection (reason code in the "not authorized"/"bad username or
   password" range), call `stats.SecurityObserver.RecordSecurityRejection(
   "connect", ...)` — mirroring `NewSecuredClient`'s already-shipped
   pattern, satisfying this project's own observer-integration
   requirement for any new/extended mechanism.

**Confirmed reqreply parity, unchanged**: `mqtt5.Connect` is adapter-level
(package `mqtt5`, not events- or reqreply-specific) — already directly
reusable by `api/reqreply` with zero code change, since it returns a
plain `MQTTClient`/`MQTTRouter` pair, consumed identically by either
package's own `Attach`/`NewTransport` constructor (confirmed via the
worked example above, which attaches to both).

**Housekeeping note (not a new design question)**: `connect_security.go`'s
doc comment "go-codex NEVER calls Connect() itself" is misleading at the
PACKAGE level — true only for `NewSecuredClient`'s own mechanism (which
wraps an ALREADY-connected client for format-only pre-validation), false
for its sibling `mqtt5.Connect` (which performs the real handshake). Flag
for correction when Phase 4 is actually implemented.


### Combining Phase 2 (message-level OAuth2/scopes) and Phase 4 (connection-level auth) — a full worked AsyncAPI rendering, and what the broker does NOT know about

A natural follow-up question: declaring BOTH a connection-auth scheme
(Phase 4) AND a message-layer OAuth2-with-scopes authorization middleware
(Phase 2) on the SAME channel/server — does this render correctly into
AsyncAPI, is it even a sound design, and what exactly is being
compensated for, given plain MQTT brokers have no native
scopes/authorization concept at all? **Confirmed via code: yes, yes, and
precisely this — the broker enforces CONNECT-time credentials only; ALL
scope/authorization enforcement happens in go-codex's own process, never
touching the broker.**

#### Worked example — one of each, same channel

```go
// Phase 4 — connection-level: the broker's OWN CONNECT-time credential.
// A plain MQTT broker understands THIS (username/password), nothing more.
var brokerAuth = route.SecurityScheme{Type: route.SecuritySchemeHTTP, Scheme: "basic"}
eventsClient.AddConnectSecurityScheme("brokerAuth", brokerAuth)
eventsClient.AddServer("mqtt5", events.Server{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})
client, router, _ := mqtt5.Connect(ctx, "broker:8883", mqtt5.ConnectOptions{
    Username: username, Password: password,
})
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})
eventsClient.Attach(transport)

// Phase 2 — message-level: an OAuth2 scheme the BROKER has never heard
// of. Declared via the SAME route.OAuth2Scheme(...) constructor REST/
// reqreply already use — nothing events-specific about the scheme type.
var oauth2 = route.OAuth2Scheme(route.OAuthFlows{
    ClientCredentials: &route.OAuthFlow{
        TokenURL: "https://auth.example.com/token",
        Scopes:   map[string]string{"subscribe:sensors": "read sensor data"},
    },
})
bearerAuth := events.NewMiddleware(middleware.Declaration[OAuthCred, struct{}]{
    Name: "oauth2",
    Security: &middleware.SecurityDeclaration{Scheme: oauth2},
}).WithReceive(validateTokenAndCheckScopes)

sub := sensorChannel.WithSubscribe(events.Subscribe{
    Security: []route.SecurityRequirement{route.Require("oauth2", "subscribe:sensors")},
})
sub.Use(bearerAuth)
```

#### Resulting AsyncAPI document (sketch) — both schemes coexist, zero collision

```yaml
components:
  securitySchemes:
    brokerAuth: { type: http, scheme: basic }
    oauth2:
      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: https://auth.example.com/token
          scopes: { "subscribe:sensors": "read sensor data" }
servers:
  mqtt5:
    url: mqtts://broker:8883
    protocol: mqtt5
    security: [{ brokerAuth: [] }]          # connection-level, Phase 4
channels:
  sensors/{sensorID}/data:
    subscribe:
      security: [{ oauth2: ["subscribe:sensors"] }]   # message-level, Phase 2
```

Confirmed via code: `components/securitySchemes` is just a `name →
scheme` map (`buildSecuritySchemes`) — `brokerAuth` and `oauth2` are two
unrelated entries, registered by two unrelated call sites
(`AddConnectSecurityScheme` vs. the channel's own aggregated
`WithSecurityScheme`/`SecurityDeclaration`), with zero shared code path.
`Server.Security` (rendered by `document.go`'s server-building loop) and
`channels.<x>.subscribe.security` (rendered by the SAME file's
operation-building loop) are two INDEPENDENT nesting points of the same
document — standard AsyncAPI v3 modeling: connection-level and
operation-level security are separate, both-apply constraints, not an
either/or choice a renderer needs to reconcile.

#### Who enforces what, and where — the actual compensation

| | Phase 4 (`brokerAuth`) | Phase 2 (`oauth2` + scopes) |
|---|---|---|
| What the SPEC says | A client must authenticate to connect to this server | A client must hold a token granting `subscribe:sensors` to use this operation |
| Who ENFORCES it | **The MQTT broker itself** — via its own ACL/auth config, checking the CONNECT packet's username/password. go-codex's `NewSecuredClient` only pre-validates the FORMAT before sending; the broker makes the real accept/reject decision. | **go-codex's own dispatch, in the subscribing/publishing process** — `Middleware[In,Out]`'s Fn validates the token, `CheckScopes` compares granted vs. required scopes. The broker is not involved at all — it has already delivered (or would deliver) the message regardless of scope outcome; go-codex's own handler simply never runs the business logic if `CheckScopes` fails. |
| Does the broker understand this constraint? | Yes — this is exactly what MQTT's CONNECT packet models. | **No — confirmed, by protocol design.** MQTT (v3 and v5) has no scope/authorization concept at the wire level at all. A "basic" MQTT broker that merely implements the protocol spec literally cannot represent, let alone enforce, "`subscribe:sensors` scope required" — it only ever sees "this client connected with these credentials, then subscribed to this topic string." |

**This is the precise compensation the user described**: Phase 2 exists
*because* the broker can't do this — go-codex's `Middleware[In,Out]` +
`CheckScopes` mechanism is an APPLICATION-layer authorization layer
bolted on top of a protocol that has none, enforced entirely in the
subscribing/publishing process, never delegated to (or verifiable by)
the broker. The AsyncAPI document's `channels.*.subscribe.security`
entry is therefore **descriptive of go-codex's own enforcement**, not a
broker capability being documented — an important distinction for a
spec reader: `Server.Security` describes a BROKER-ENFORCED guarantee;
`Operation.Security` (for an MQTT-backed channel) describes an
APPLICATION-ENFORCED one. Both are equally real, equally worth
documenting, and AsyncAPI already has the vocabulary for both — but a
reader auditing "is this actually secure" needs to know WHICH enforcement
point applies to which entry, since only one of them is the broker's
problem.

**No new mechanism needed to combine them** — declaring one of each is
sufficient; Phase 2 and Phase 4 were already confirmed structurally
independent (prior rounds), and this round confirms their SPEC OUTPUTS
compose cleanly too, with no renderer-level conflict.


### Correcting the context-propagation tie-in: `SetContextFieldFromIn` on subscribe has NOTHING to do with publish

A separate point of confusion in this round's earlier draft: describing
`SetContextFieldFromIn` (née `PublishFieldIn` — see the renamed "Open
design decisions for Phase 3" item above) as somehow involving the
publish side on a SUBSCRIBE channel. **This was wrong, and the
`PublishField*` naming itself was the direct cause** — renamed in this
same round to `SetContextFieldFromIn`/`SetContextFieldFromOut` precisely
because the old name's "Publish" prefix reads, in an `api/events`
context, as "the publish side of this channel" — it never meant that.

To be fully explicit, for the SUBSCRIBE path specifically:

- A Security `Middleware[In,Out]` attached to a channel's SUBSCRIBE side
  decodes `In` from the incoming message's topic/property vars (e.g. a
  bearer token) — confirmed, `WithReceive`'s shape is `func(ctx, In)
  error`, no `Out` at all (see the signature table earlier in this doc).
- `SetContextFieldFromIn(field, get func(In) any)` dispatches
  IMMEDIATELY after `DecodeIn` succeeds, BEFORE the Fn even runs — it
  reads a value OUT OF `In` (e.g. a `UserID` the credential's OWN field
  codec already derived from the raw token, ordinary
  `codex.Struct`/`Refine` composition, ADDRESSED in the Phase 3 section
  above) and writes it into the `ContextField`.
- The subscribe HANDLER then reads it back via `field.Get(ctx)` — fully
  typed, available by the time the handler runs.
- **The PUBLISH side of this (or any other) channel is not involved at
  any point in this flow.** `SetContextFieldFromOut` is the SEPARATE
  method for the produced-value case (events' PUBLISH side, where
  `WithSend` DOES produce an `Out`) — it simply does not apply to
  subscribe at all, by the structural asymmetry already documented in
  the Phase 3 section (events' `WithReceive` has no `Out` to source a
  value from).

### Worked, corrected sketch — all 3 layers on one subscribe channel, zero publish-side involvement

```go
// Layer 1 — connection-level (broker) auth: connect once, before any
// Subscribe call, using the SAME scheme name registered via
// AddConnectSecurityScheme (see Phase 4 above for the full declare/
// attach-time design) — closing what was, in an earlier round of this
// doc, an unlinked, by-hand-only convention.
client, router, err := mqtt5.Connect(ctx, brokerURL, mqtt5.ConnectOptions{
    Username: username, Password: password,
})
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: client, Router: router})

// Layer 2 — Observer: attached unpaired, wraps every dispatch.
sub := sensorChannel.WithSubscribe(events.Subscribe{})
sub.SubscribeMW(nil, events.Observability[SensorReading](obs))

// Layer 3 — message-level, protocol-independent auth (Phase 2 shape):
// the credential Fn embeds an OAuth2 token validation over REST,
// independent of this channel's own mqtt5 transport (see "Cross-protocol
// credential composition" above) — and its In carries a codec-derived
// UserID alongside the raw token.
userIDField := middleware.NewContextField[string]("events.userID", userIDCodec)

bearerAuth := events.NewMiddleware(middleware.Declaration[BearerCred, struct{}]{
    Name: "bearerAuth", Security: &middleware.SecurityDeclaration{...},
}).WithReceive(func(ctx context.Context, in BearerCred) error {
    // in.UserID already derived at the CODEC level from in.Token —
    // ordinary Refine composition, no new mechanism.
    return nil // credential already validated by DecodeIn; nothing left to do
}).SetContextFieldFromIn(userIDField, func(in BearerCred) any { return in.UserID })

sub.Use(bearerAuth)

// The subscribe HANDLER — reads the derived UserID, fully typed, with
// ZERO manual *T mutation and ZERO involvement of this channel's
// (or any channel's) publish side.
events.SubscribeHandle(ctx, sub, transport, func(ctx context.Context, msg SensorReading) error {
    userID, _ := userIDField.Get(ctx)
    return handle(userID, msg)
})
```

### Remaining small open item: recommended Observer/Security dispatch ordering

Confirmed via code: `SubscribeMW`'s dispatch order is registration order,
caller-controlled — go-codex does not enforce Observer-before-Security or
vice versa. **Recommendation (documentation guidance only, not a new
mechanism)**: register the Observer FIRST (as in the sketch above) so a
Security rejection is still observed/logged — mirrors the equivalent
recommendation already given for REST in
`docs/features/security.md`. No code change proposed; a doc note only,
added when Phase 2/3 land.


## Confirmed via code: scope-checking is ALREADY separable, zero change needed there — in all three packages

`runSecurityMiddleware`-equivalents already treat scope-checking as a
GENERIC post-Fn step, independent of credential shape and independent of
package: every attached security Fn returns a `granted
map[string][]string`, merged across all attached Fns, THEN
`middleware.CheckScopes(secReqs, granted)` runs ONCE. Confirmed identical
in `adapters/nethttp/adapter.go` (REST), `adapters/mqtt5/{caller.go,
adapter.go,reqreply_transport.go}` (events + reqreply). This pivot only
needs to change HOW the Fn receives its credential input / produces its
wire output — the granted-scopes return value and `CheckScopes` call are
UNCHANGED everywhere.

## Relationship to the Observer pattern and ErrorPattern — confirmed orthogonal/compatible, neither threatened by this doc

User direction: explicitly check this doc's "middleware is a partial
route/channel definition" model against the Observer pattern and the
ErrorPattern mechanism, since Observer in particular is "a non-spec
adding middleware, that has no IN/OUT structs and is a special case."
Confirmed via code — both are genuinely different in kind from the
`Middleware[In,Out]` layering this doc is about, and neither needs any
change as a result of Phase 1 or Phase 2.

### Observer — confirmed fully orthogonal, not a layer at all

Traced `DispatchMiddlewareHandlers` (`api/rest/transform_dispatch.go`,
the exact function Phase 1 wraps `DecodeLayer`/`EncodeLayer` inside of):
Observer integration is `stats.ReportErrors(DiagnosticObserver{Ctx: ctx},
"middleware:in"/"middleware:fn", err)` — resolved PURELY from `ctx` at
dispatch time (`stats.ObserverFromContext`-style resolution), with:

- **No `Middleware[In,Out]` involvement at all** — Observer is not
  attached via `.Use(mw)`/`Transform`/`ClientTransform`; it is ambient,
  threaded through `ctx` the same way for EVERY route/channel, with or
  without any middleware attached.
- **Zero spec contribution** — Observer never appears in the OpenAPI/
  AsyncAPI spec, has no `Name`, no `Security` field, no merge-field
  declarations of its own. It genuinely has no `In`/`Out` because it
  isn't decoding or encoding anything — it's an OBSERVATION of what
  already happened, not a declared data transformation.
- **Confirms the user's framing exactly**: Observer is correctly
  understood as a special case OUTSIDE this doc's layering model, not an
  example this doc's principle should be generalized to cover. This doc
  does not propose (and should not be read as implying) any change to
  how Observer works — Phase 1's `DecodeLayer`/`EncodeLayer` extraction
  is a pure internal refactor of the axis decode/encode logic INSIDE
  `DispatchMiddlewareHandlers`; the `stats.ReportErrors` calls that wrap
  it are untouched, at the same call sites, unaffected by where the
  decode/encode logic itself lives.

### ErrorPattern — confirmed a genuine, but different-in-kind, interaction point; Phase 2 must preserve an already-shipped fix

Unlike Observer, `ErrorPattern` is NOT fully orthogonal — it genuinely
interacts with middleware dispatch outcomes, but it is declared on the
ROUTE/channel itself (via `route.ErrorPattern[E,B](...)`), never on
`Middleware[In,Out]` — confirmed via grep: zero `ErrorPattern`-related
fields or methods exist on `Middleware[In,Out]` anywhere. The
interaction is at the ERROR-CLASSIFICATION level, confirmed via
`middlewareDispatchError`'s own fields (`api/rest/transform_dispatch.go`):

- A middleware's `DecodeIn` failure (a merge-field/axis decode or
  `InCodec.Validate` failure) is classified `isFnError: false` — NOT
  ErrorPattern-eligible, treated as a plain param-validation-style
  failure (no business error exists yet to match against a pattern).
- A middleware's own Fn failure (the user's business logic) IS
  classified `isFnError: true` — ErrorPattern-eligible, falling back to
  `MiddlewareError` when no pattern matches.

**Security's CURRENT error handling already received this exact fix** —
confirmed via `adapters/nethttp/adapter.go`'s own code comment:
"Security middleware Fn error IS ErrorPattern-eligible now (session-review
finding H1...) — previously bypassed ErrorResponseFor entirely, always
producing SecurityError." This is an ALREADY-SHIPPED behavior, not a new
design question — Phase 2 MUST preserve it: once Security's credential
Fn dispatches through the SAME `DispatchMiddlewareHandlers` mechanism
every other middleware uses, its Fn error naturally gets the SAME
`isFnError: true`/ErrorPattern-eligible classification for free, with NO
special-casing needed (confirming, yet again, that routing Security
through the general mechanism is strictly as good as or better than its
current bespoke path, never a regression).

**One asymmetry confirmed consistent, not a regression**: today,
`ValidateSecurityCredentials`'s failure (the `SecurityScheme.Codec`
path already resolved as redundant/to-be-deprecated — see "Open design
decisions" item 6) bypasses ErrorPattern entirely (`errFn(sw, r,
http.StatusUnauthorized, credErr)`, called directly, no pattern
consultation) — DIFFERENT from the Fn-error path's ErrorPattern
eligibility. Under Phase 2, credential validation moves to
`mw.InCodec.Validate(in)`, which runs INSIDE `DecodeIn`/`DecodeLayer` —
classified `isFnError: false`, i.e. also NOT ErrorPattern-eligible. **This
is the SAME classification as today, reached via a different code path**
— confirmed consistent, not a behavior change to flag as a risk.

## Phasing

**Phase 4** (connection-level auth, `api/events` + `api/reqreply`) is independent of Phases 1–3 — a small, additive builder-level method (`AddConnectSecurityScheme`) with no dependency on `DecodeLayer`/`EncodeLayer`/`SetContextFieldFromIn`/`Out`. Can ship before, after, or alongside Phases 1–3.

This doc now has FOUR concrete implementation phases:

- **Phase 1 — shared mechanism (prerequisite, zero behavior change).**
  Extract `middleware.DecodeLayer`/`EncodeLayer` (or equivalently-named
  equivalents, see Open design decisions); migrate all 3 packages'
  `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` onto
  it, representative-sample-then-full-sweep, fully verified by every
  EXISTING test continuing to pass unchanged (this refactor must be
  invisible to any current caller).
- **Phase 2 — Security as the proving consumer.** Generalize
  `SecurityMiddleware[In,Out]` in all 3 packages and route its dispatch
  through the NOW-SHARED Phase 1 mechanism — by construction uniform
  across packages, not a 4th hand-written variant.
- **Phase 3 — propagate middleware-derived values to the handler/caller
  (new scope, not a Phase 1/2 dependency).** Extend `middleware.
  ContextField[V]` with a declarative `SetContextFieldFromIn`/`SetContextFieldFromOut`
  link on `Middleware[In,Out]`, so a middleware's decoded `In` (or
  produced `Out`) becomes automatically retrievable by the route/channel
  handler — see "Propagating middleware-derived values to the handler/
  caller" below for the full design. Independently useful, genuinely
  separate from Phase 1/2's own scope (which only extract/generalize the
  EXISTING wire-encoding mechanism, never touch handler-visibility).
- **Phase 4 — connection-level auth, `api/events` + `api/reqreply`**
  (new scope, independent of Phases 1–3). Add
  `Client.AddConnectSecurityScheme`/`Builder.AddConnectSecurityScheme`
  (declare-time, spec-only, D-0006-pattern stage 2) and extend
  `mqtt5.Connect`'s error/Observer fidelity (`ConnectError.ReasonCode`/
  `ReasonString`, `ConnectOptions.Observer` — D-0006-pattern stage 4,
  already-sealed by package boundary). See "api/events middleware
  deep-dive" above for the full design.

Phase 1 is independently valuable (removes a confirmed, self-acknowledged
3-way duplication) even if Phase 2 were deferred — but Phase 2 is what
actually PROVES the shared mechanism is correctly general, not
REST-shaped-with-mqtt5-bolted-on. Phase 3 is independent of BOTH —
useful for ANY middleware (Security or otherwise) in ANY package, not
specific to Security's own generalization. **Phase 4 is independent of
Phase 3 too, confirmed, not just unstated**: Phase 4's `mqtt5.Connect`
runs at Attach-time/Transport-construction, strictly BEFORE any
`Middleware[In,Out]` dispatch exists for a given channel/route — there is
no per-message `In`/`Out` at that point for Phase 3's `ContextField`
mechanism to hook into, so the two phases never interact, by
construction, not merely by omission.

## Prerequisite for Phase 2 (REST only): `Client.Call`/`Client.Consume` must dispatch `ClientMiddlewareHandlers`

**Found during a pre-implementation review — a real, previously-missed
blocking dependency, not a hypothetical risk.** Traced
`adapters/nethttp/clienttransport.go`'s `Call` AND `Consume` (the
functions behind `rest.Client.Call`/`Client.Consume`, the MAIN documented
entry points this whole doc is about) — confirmed via direct grep: ZERO
occurrences of `ClientMiddlewareHandlers` anywhere in this file. This is
not a new discovery of a bug — it's an ALREADY-DOCUMENTED, previously
ACCEPTED limitation, confirmed via `adapters/nethttp/client.go`'s own
code comment (right after the deleted `CallWithHandle`'s doc comment):

> "One pre-existing, KNOWN, unchanged limitation carries over unchanged:
> neither this deleted function's replacement NOR `rest.Client.Call`
> dispatch declared `rest.Route.ClientTransform`/bundled `.Use()`
> codec-backed middleware today (confirmed via code —
> `clientTransport.Call` never called
> dispatchClientMiddlewareIn/Out)... this is a known, accepted gap, not
> a regression affecting any migrated caller."

**Why this was safe to accept before, but is NOT safe to leave as-is for
this doc's Phase 2**: at the time (D-0006 Phase 5a), no real caller used
`ClientTransform`/bundled `.Use()` agnostic middleware through
`Client.Call`, so the gap was harmless. **Phase 2 changes that
directly**: it proposes migrating Security's credential Fn from today's
`ClientImplementations`-based dispatch (which `Call`/`Consume` DO
correctly read today, via `resolveClientSecurity`/`mergeCredentialHeaders`)
onto the `.Use(mw.WithSend(...))` agnostic mechanism — which populates
`ClientMiddlewareHandlers`, a COMPLETELY DIFFERENT field `Call`/`Consume`
never read. **Left unfixed, Phase 2's own worked code example in this doc
(`route.Use(BearerAuthDeclaration.WithSend(...))`) would silently stop
dispatching the credential Fn when called via `client.Call(...)` — a real
functional regression for Security specifically**, not a hypothetical
risk, and not acceptable to ship.

**Confirmed REST/nethttp-specific, NOT shared across packages** — events
and reqreply have NO equivalent gap:

- `adapters/mqtt5/adapter.go`'s publish dispatch ALREADY calls
  `events.DispatchPublishMiddlewareHandlers(ctx, msg,
  handle.ClientMiddlewareHandlers)` (line 925).
- `adapters/mqtt5/reqreply_transport.go` ALREADY calls
  `reqreply.DispatchClientMiddlewareIn`/`DispatchClientMiddlewareOut`
  (lines 966/1230).
- `adapters/chi` has no client at all — not applicable.

**Required fix, scoped precisely**: add `ClientMiddlewareHandlers`
dispatch to `adapters/nethttp/clienttransport.go`'s `Call` AND `Consume`
— mirroring the EXACT pattern `adapters/nethttp/binding.go`/`client.go`
already use (`dispatchClientMiddlewareIn`/`dispatchClientMiddlewareOut`,
confirmed existing, tested functions — nothing new to invent, just a
new call site in 2 more places). This is a BLOCKING PREREQUISITE of
Phase 2 specifically (not Phase 1 — Phase 1 never touches dispatch call
sites, only the axis decode/encode logic inside functions already being
called) — tracked here as its own concrete migration step, not folded
silently into Phase 2's description.

## SSE (`api/rest`) — partially covered already; the gap above closes the rest

SSE was not explicitly considered in earlier drafts of this doc. Traced
the actual dispatch mechanism directly:

- **Server-side (SSE's receive direction)**: confirmed
  `adapters/nethttp/serve_sse.go` ALREADY calls the EXACT SAME
  `rest.DispatchMiddlewareHandlers` function Route's own server dispatch
  uses (line 220) — meaning Phase 1's `DecodeLayer`/`EncodeLayer`
  extraction (which only touches the axis decode/encode logic INSIDE
  this already-shared function) automatically, silently benefits SSE's
  server-side dispatch too, with ZERO additional Phase 1 work.
- **Client-side (`Consume`)**: shares the EXACT gap described above —
  `clienttransport.go` hosts both `Call` and `Consume` in the same file,
  and the "zero `ClientMiddlewareHandlers` dispatch" finding covers both
  equally. The SAME fix (adding dispatch to `clienttransport.go`) closes
  BOTH gaps in one pass.
- `SSERoute.Use()`/`.HandleMW()`/`.ClientMW()` are confirmed to exist and
  mirror `Route`'s own signatures exactly (`api/rest/middleware.go`) —
  SSE already has full attachment-point parity with `Route`; only the
  CLIENT-side dispatch (shared with the gap above) was missing, not the
  declaration surface.

## Phase 3: propagating middleware-derived values to the route/channel handler/caller

Raised directly: since middleware uses the SAME codec/merge-field
mechanism a route's own Req/Resp does, and the library's own "one
struct, one call" paradigm composes everything into one struct, a
middleware's derived values should be AVAILABLE to the handler — not
just silently merged onto the wire. Confirmed, via code, this is only
PARTIALLY true today, and the gap splits into two genuinely distinct
aspects.

### Two aspects, confirmed distinct

1. **Spec-adding structs (headers/cookies/queries/topics/properties)** —
   a middleware's `Out` CAN already declare these via the EXISTING
   merge-field methods (`WithResponseHeader`/`WithPublishProperty`/etc.),
   and they DO already appear in the OpenAPI/AsyncAPI spec. The gap here
   is RETRIEVAL: once encoded onto the wire, is the Out value ALSO
   retrievable by the handler/caller as a typed value, not just sent?
2. **Derived, non-wire values** (e.g. a `UserID` parsed out of a JWT —
   not itself a new wire location, just something the HANDLER needs
   access to) — these have NO natural spec representation at all (they
   don't correspond to any single wire field); the gap here is
   PROPAGATION: does the handler get this value at all, typed, without
   manual `*Req`/`*T` mutation?

### What already works, confirmed (manual, opt-in, not automatic)

`Transform` (receive-side, all 3 packages) gives the middleware Fn
POINTER access to the route/channel's own `Req`/`T` — confirmed
identical signatures: REST `func(ctx, req *Req, in In) (Out, error)`,
events `func(ctx, msg *T, in In) error`, reqreply `func(ctx, req *Req,
in In) (Out, error)`. A Fn CAN manually copy a derived value from `In`
into `*Req`/`*T`, making it visible to the handler — but this is a
manual, per-field mutation the Fn author must choose to do, not
automatic. **Confirmed TODAY's Security Fn signature already has this
access too** — REST server-side: `func(ctx, *http.Request, *Req)
(map[string][]string, error)`; events subscribe-side: `func(ctx, *T,
[]route.SecurityRequirement) ([]UserProperty, error)` — meaning
Phase 2's CURRENTLY-SKETCHED examples (using the AGNOSTIC
`.Use(mw.WithSend(...))` style, which has NO Req/T access at all) would
actually REGRESS this existing capability unless Phase 3 (below) closes
the gap a different way.

### How nethttp/chi solve this — confirmed standard, no compile-time safety, no spec concept

Both use plain `context.WithValue(ctx, key, value)` — a middleware
writes an untyped `any`, a handler reads it back via a manual type
assertion, zero compile-time safety (a wrong assertion is caught only at
runtime). Neither library has ANY concept of spec rendering (they are
raw HTTP libraries with no OpenAPI/AsyncAPI awareness). There is no
prior art to directly adopt — go-codex's own codec-driven approach
already goes further than the ecosystem baseline.

### go-codex already has a close, codec-typed answer: `middleware.ContextField[V]`

Confirmed, already shipped (`middleware/context_field.go`):
`ContextField[V]{key, codec}`, declared once at package level, shared by
every producer (`Set`) and consumer (`Get`) — Go generics give
compile-time type safety for THIS field (the SAME value is used on both
sides). `Set(ctx, raw any) error` takes a RAW, undecoded value and runs
it through the field's OWN `codec.Decode` — the field's codec does the
ACTUAL parsing/validation (e.g. turning a raw JWT string into a
structured `UserID`), cleanly separating "extract the raw wire value"
from "derive/validate the typed result." Implemented via a shared
mutable box pre-allocated once per request/call
(`EnsureContextFields`), so values set by an earlier middleware are
visible to a later middleware or the handler. **Confirmed via its own
doc comment it deliberately does NOT feed spec rendering** — framed as
"use this INSTEAD OF adding security-specific fields to every route's
own Req type" — matching Aspect 2 (derived, non-wire values) precisely.

**Confirmed scope gaps, via code**: `EnsureContextFields` is called ONLY
by `adapters/nethttp`/`adapters/chi`'s SERVER-side dispatch (`serve.go`,
`serve_sse.go`, `adapter.go`) — ZERO calls anywhere in `adapters/mqtt5`/
`zeromq`/`mqtt` or `api/reqreply`'s adapters, and ZERO calls on REST's
OWN CLIENT side (`clienttransport.go`). `ContextField` is
REST-SERVER-ONLY today, and entirely MANUALLY invoked — nothing links it
declaratively to a `Middleware[In,Out]`'s own `In`/`Out` fields.

### Proposed design — extend `ContextField`, don't replace it; link it declaratively

Confirmed all 6 `WithReceive`/`WithSend` signatures precisely before
designing this, since `events` turned out to be genuinely asymmetric
relative to REST/reqreply:

| Package | Receive direction | Send direction |
|---|---|---|
| `api/rest` | `func(ctx, In) (Out, error)` | `func(ctx) (In, error)` |
| `api/reqreply` | `func(ctx, In) (Out, error)` | `func(ctx) (In, error)` |
| `api/events` | `func(ctx, In) error` — **no Out at all** | `func(ctx) (Out, error)` — produces Out, not In |

REST and reqreply are fully symmetric with each other. **events is
asymmetric on BOTH axes** — its Subscribe (`WithReceive`) produces
NOTHING but an error (no Out to extract a field from at all), and its
Publish (`WithSend`) produces `Out` (not `In` — analogous in ROLE to
REST/reqreply's `WithSend` producing `In`, just named oppositely by
that package's own convention). **A single `SetContextField(field, get
func(Out) any)` method would silently be unusable for events'
Subscribe** — there being no `Out` there at all — so the design splits
into two methods instead of one, reflecting this honestly:

```go
// middleware — two new declarative methods on Middleware[In,Out].

// SetContextFieldFromIn declares that, after DecodeIn succeeds (BEFORE the Fn
// runs), dispatch automatically calls field.Set(ctx, get(in)) — no
// manual Fn-body code needed. Works UNIVERSALLY: every package, every
// attachment style (bound Transform, agnostic WithReceive/WithSend, AND
// events' Subscribe, which has no Out at all) — In always exists
// post-decode, regardless of direction or package. The derivation
// itself (e.g. JWT parsing into a structured UserID) happens at the
// credential field's OWN codec level (ordinary codex.Struct/Refine
// composition, nothing new) — so by the time SetContextFieldFromIn runs, the
// derived value is ALREADY part of In.
func (m Middleware[In, Out]) SetContextFieldFromIn(field middleware.ContextField[V], get func(In) any) Middleware[In, Out]

// SetContextFieldFromOut is SetContextFieldFromIn's sibling for the PRODUCED value —
// Out for REST/reqreply's receive direction AND events' send
// direction; In for REST/reqreply's send direction. NOT usable for
// events' Subscribe (WithReceive) — there is no Out parameter to
// reference there; Go's own type system means the method simply
// doesn't type-check against that shape, not a runtime restriction.
func (m Middleware[In, Out]) SetContextFieldFromOut(field middleware.ContextField[V], get func(Out) any) Middleware[In, Out]
```

Dispatch calls every declared link automatically — `SetContextFieldFromIn`
right after `DecodeIn` succeeds (before the Fn runs), `SetContextFieldFromOut`
right after the Fn produces its value (alongside the EXISTING
`EncodeOut`/`EncodeIn` calls) — mirroring exactly how wire merge fields
are already dispatched automatically today, just targeting
`ContextField.Set` instead of a wire location. **Does NOT feed spec
rendering** — by design, matching `ContextField`'s own existing
framing; a caller wanting the SAME value ALSO wire-rendered declares a
SEPARATE, ordinary merge field on the same struct — the two are
complementary, not exclusive.

### Package-by-package verdict

- **`api/rest`**: BOTH methods fully supported, both directions.
  Confirmed prerequisites: extend `EnsureContextFields` to the CLIENT
  side (`clienttransport.go`, currently server-only); new dispatch call
  sites alongside the existing `EncodeOut`/`EncodeIn` calls.
- **`api/reqreply`**: IDENTICAL shape to REST (fully symmetric, per the
  table above) — same 2 prerequisites, scoped to
  `adapters/mqtt5/reqreply_transport.go`/`adapters/zeromq/
  reqreply_transport.go`. PLUS a confirmed, NEW prerequisite:
  `ContextField`/`EnsureContextFields` have NEVER been wired into
  reqreply's dispatch at all (confirmed zero existing usage) — this is
  GREENFIELD integration for this package, not an extension.
- **`api/events`**: `SetContextFieldFromOut` works on PUBLISH (send) only —
  analogous to REST/reqreply's send-side `In`. `SetContextFieldFromIn` is the
  ONLY option — and the thing that actually closes the user's original
  UserID-in-handler use case — on SUBSCRIBE (receive), PROVIDED the
  derivation happens at the credential field's OWN codec level
  (confirmed a real, if more constrained, requirement specific to
  events — REST/reqreply could ALSO derive post-Fn via `Out`; events
  cannot). Same greenfield `ContextField` integration prerequisite as
  reqreply (confirmed zero existing usage in `adapters/mqtt5`/`zeromq`/
  `mqtt`'s events dispatch either).

**This design also fully retires the Phase 2 Transform-vs-agnostic
regression concern** raised earlier — once `SetContextFieldFromIn` exists,
Security's Fn no longer needs `*Req`/`*T` access to expose a derived
value to the handler, so the agnostic `.Use(mw.WithSend(...))` style
(Phase 2's original worked examples) is fine again, PROVIDED Phase 3
ships — if Phase 3 is deferred, Phase 2 should note this as an interim
trade-off (see new Open design decision below).

### Open design decisions for Phase 3 (NOT yet resolved — genuinely new, unlike Phase 1/2's)

- **Sequencing: does Phase 2 need to ship Transform-based Security (not
  just the agnostic style) if Phase 3 is deferred or ships later?** If
  Phase 3 is NOT implemented alongside Phase 2, Security's agnostic-style
  migration would regress today's `*Req`/`*T` enrichment capability (see
  "What already works" above) until Phase 3 lands — needs an explicit
  sequencing decision: ship Phase 3 together with Phase 2, OR have Phase
  2 attach Security via `Transform`/`ClientTransform` as an interim
  measure, OR accept the temporary regression with a documented
  migration note. Not pre-decided.
- **Naming — RESOLVED: `SetContextFieldFromIn`/`SetContextFieldFromOut`,
  not `PublishFieldIn`/`PublishFieldOut`.** The original sketch used a
  `PublishField*` prefix, chosen to read as "publish this value into the
  ContextField" — but this COLLIDES with `api/events`' own
  Publish/Subscribe vocabulary: on events' SUBSCRIBE path specifically, a
  name containing "Publish" wrongly suggests the mechanism is tied to the
  PUBLISH side, when it is not — `SetContextFieldFromIn` works on
  SUBSCRIBE precisely BECAUSE it is sourced from `In` (always available,
  regardless of direction), with ZERO relationship to publish. Renamed to
  name the mechanism by its SOURCE (`In`/`Out`), never by a verb that
  could be misread as a pub/sub role — closes a real, user-caught naming
  defect (see "api/events middleware deep-dive" section's own call-out).
  Other alternatives considered and still rejected: a single method with
  an enum/flag distinguishing direction (would compile-check against the
  wrong direction for events, defeating the whole point of catching the
  events-Subscribe case at compile time); a single method overloaded via
  Go's type system (not possible — Go has no function overloading).
- **Does `ContextField`'s existing shared-mutable-box implementation
  need any change to support reqreply/events' dispatch models**, or does
  `EnsureContextFields` just need a new call site in each adapter,
  reusing the box as-is? Needs a close read of reqreply's/events'
  context-propagation chain during implementation — not yet spiked.
- **Should `SetContextFieldFromIn`'s derivation-at-the-codec-level requirement
  (for events' Subscribe specifically) be documented as a design
  constraint up front, or should events eventually gain its OWN
  post-Fn "Out-equivalent" return channel** (a `WithReceive(fn func(ctx,
  In) (SomeNewType, error))` signature change) to close this asymmetry
  properly instead of working around it? The latter is a BREAKING change
  to events' `WithReceive` signature, explicitly NOT proposed here — but
  flagged as the more symmetric long-term alternative, worth a future,
  separate evaluation if the codec-level-derivation constraint proves
  too limiting in practice.

## Relationship to sibling "declarative middleware" roadmap docs

Two sibling roadmap docs explore closely related "does boundary X have a
declarative middleware mechanism" territory — cross-referenced here for
discoverability, mirroring this session's established convention (e.g.
`dynamic-port-rebinding.md` ↔ `mcp-ports-declarative-middleware.md`):

- [WebSocket — should it gain a general-purpose declarative middleware
  mechanism?](websocket-declarative-middleware.md) — confirmed OUT OF
  SCOPE here: WebSocket is built on `ports.DuplexPort`/`SocketPattern`,
  not `api/events.Channel`, so there is no `Middleware[In,Out]`
  attachment point to generalize in the first place (confirmed via that
  doc's own research — zero `.HandleMW`/`.ClientMW`/`.Use(` matches
  anywhere under `adapters/websocket`). This doc's `DecodeLayer`/
  `EncodeLayer` mechanism is specific to `api/rest`/`api/events`/
  `api/reqreply`'s shared `Middleware[In,Out]` shape, which WebSocket
  does not have.
- [MCP and Ports Declarative Middleware](mcp-ports-declarative-middleware.md)
  — confirmed OUT OF SCOPE here: MCP/`ports` use a DIFFERENT,
  single-phase attachment model with no spec/two-phase declare-dispatch
  split (see this doc's own "Out of scope" section) — the two doc's
  designs are independent, not competing or overlapping.

## Relationship to D-0006 (protocol-native capabilities) — two distinct, intentionally-separate mechanisms, preserved unchanged

A natural question this redesign raises: does generalizing `Middleware`
dispatch via a shared `DecodeLayer`/`EncodeLayer` mechanism interact with,
weaken, or risk being conflated with
[D-0006's](../design/d-0006-protocol-native-capabilities.md) sealed,
per-adapter `Capability` interface (`mqtt5.QoS`/`mqtt5.Retained`, etc.)?
**Confirmed via code: no — these are two distinct mechanisms, already
cleanly separated today, and this doc's Phase 1–3 redesign preserves that
separation unchanged.** Worth stating explicitly, since both this doc's
prose and D-0006's own prose use the word "capability" loosely and
non-exclusively — a reader skimming either doc could otherwise wrongly
assume they're the same thing or that one subsumes the other.

### Side by side

| | D-0006's sealed `Capability` | This doc's `Middleware[In,Out]` |
|---|---|---|
| Where declared | Adapter-owned (`mqtt5.QoS`, `zeromq.Conflate`, ...) | Core, protocol-agnostic (`middleware.Declaration[In,Out]`) |
| Sealing mechanism | Unexported marker method per adapter (`isMQTT5Capability()`) — Go-compiler-enforced, zero cross-adapter mixing possible | Not sealed — `Middleware[In,Out]` is a plain generic value, usable with any adapter that dispatches `Middleware`'s shared mechanism |
| Supplied at | DECLARE time, via `SubscribeOptions.Capabilities`/`PublishOptions.Capabilities` | DECLARE time too, but via `.Use(mw)`/`Transform`/`ClientTransform` on a channel/route — a different declare-time surface |
| Dispatch mechanism | Direct method call — `Capability.Apply(wire *WireAttributes) (bool, error)`, NO reflection | Reflection-based — `reflect.ValueOf(h.Fn).Call(...)`, needed because `Fn`'s exact signature varies by attachment style (bound vs. agnostic, `In`/`Out` generic) |
| Coverage/requirement check | `api/events.CapabilityRequirement` (Tier 3 — Explicit) + `CheckCapabilityCoverage`, adapter-agnostic declare-time hook resolved against whichever adapter's sealed value is actually supplied | `rest.CheckCoverage`-equivalent (Security-specific, pre-existing, unchanged by this doc) |
| Spec rendering | Generic `"x-capabilities"` AsyncAPI vendor extension (one entry per `CapabilityRequirement`, adapter-agnostic by design) | The route/channel's own existing merge-field-driven spec rendering (headers/cookies/queries/topic-vars/properties) |

### Why Security stays on this doc's mechanism, not D-0006's sealed `Capability`

D-0006 §5.5 already settled this, and this doc's redesign is consistent
with that resolution rather than reopening it: Security is the ONE
surveyed case that clears BOTH of D-0006's bars for folding into the
sealed-`Capability`-at-Attach-time mechanism — **uniform shape** (scheme +
scopes + credential, the SAME declaration shape in all 3 packages) AND
**uniform-enough support** (every adapter can enforce or at least document
a security requirement) — unlike QoS/User Properties/AMQP addressing,
each of which fails at least one bar (D-0006 §5.1/§5.2's worked
analysis) and so correctly stays adapter-owned and sealed. Because
Security clears both bars, it belongs in the protocol-agnostic
`middleware`/`api/*` core — exactly where `Middleware[In,Out]` already
lives — with zero adapter import required at declare time. This doc's
Phase 2 (generalizing `SecurityMiddleware`'s signature) is a refinement
of HOW Security's existing core-layer mechanism dispatches, not a
proposal to move Security onto D-0006's sealed mechanism or vice versa.

### Explicit non-goal

Phase 1–3 of this doc do NOT touch, generalize, fold into, or otherwise
alter D-0006's sealed `Capability` mechanism. Confirmed: this doc's
"Files to create/modify" table contains zero entries under
`adapters/*/capability.go` or `api/events/capability*.go` — every file
D-0006's mechanism owns is untouched by this redesign. QoS, Retained, and
any future sealed capability keep their own Attach-time,
non-reflection, `Apply(wire)`-based path, completely unchanged by
anything in this doc.

### One confirmed-compatible future point of convergence — not proposed or scheduled here

`adapters/mqtt5/capability.go`'s own comment already notes that
`UserPropertyParam` (today a separate, pre-existing, non-`Capability`
mechanism) could, in a FUTURE round, be folded into a sealed
`UserProperty[In]` Capability type that EMBEDS
`middleware.Declaration[In, struct{}]` for its merge-field vocabulary —
exactly the shape D-0006 §2.1 originally sketched. If that future round
happens, Phase 1's shared `DecodeLayer`/`EncodeLayer` mechanism would be
a natural fit for that Capability's INNER codec/merge-field dispatch —
while the OUTER sealed `isMQTT5Capability()`/`Apply` contract remains
exactly as sealed and adapter-owned as it is today. This is a
confirmed-COMPATIBLE future shape, not a dependency, proposal, or
scheduled phase of this doc — noted here only so a future reader doesn't
need to re-derive that these two mechanisms CAN layer cleanly, should
that future round ever happen.


## Scope decisions

| In scope | Out of scope |
|---|---|
| **Phase 1**: a shared `middleware.DecodeLayer`/`EncodeLayer` mechanism, with all 3 packages' existing `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` migrated onto it as thin wrappers | Changing `route.SecurityScheme`'s own shape — unchanged, in all 3 packages |
| **Phase 2**: generalizing `SecurityMiddleware`'s signature in ALL THREE packages — `rest.SecurityMiddleware[In, Out any](...)`, `events.SecurityMiddleware[In, Out any](...)`, `reqreply.SecurityMiddleware[In, Out any](...)` — away from the hardcoded `struct{}, struct{}`, dispatched through Phase 1's shared mechanism | Inventing any new merge-field/codec type — explicitly rejected; the EXISTING per-package merge-field constructors are reused verbatim |
| Preserving each package's OWN error types (`rest.MiddlewareInputError` vs `events.MiddlewareInputError` vs `reqreply.MiddlewareInputError`) — Phase 1's shared mechanism takes an error CONSTRUCTOR callback, it does not unify the error TYPES themselves (`errors.As` callers must still distinguish which package failed) | Unifying `MiddlewareInputError`/`MiddlewareOutputError` into one cross-package type — explicitly rejected, would break existing `errors.As` call sites for no benefit |
| Preserving the granted-scopes return value + `CheckScopes` call exactly as today, in all 3 packages (confirmed separable) | Changing `CheckScopes`'s own logic or signature |
| `adapters/nethttp`/`chi` (HTTP: header/cookie/query) AND `adapters/mqtt5`/`zeromq`/`mqtt` (user-property) — Phase 2 only, once Phase 1's shared mechanism exists to dispatch Security through | A brand-new wire-location kind beyond header/cookie/query/property |
| A breaking replacement of today's fixed-shape `ClientImplementation.Fn`/`ServerImplementation.Fn` signatures for Security specifically, in all 3 packages (Phase 2) | Changing the fixed-shape Fn contract for NON-Security general-purpose middleware — `WithReceive`/`WithSend`'s own existing contract is unchanged; Security adopts it, it doesn't change it |
| N/A (pure cross-reference, no code change) | Any change to `adapters/*/capability.go`'s sealed `Capability` interface or `api/events/capability*.go`'s declare/Attach-time coverage-check mechanism (D-0006) — confirmed fully independent, zero files touched |
| **Phase 4**: `events.Client.AddConnectSecurityScheme`/`reqreply.Builder.AddConnectSecurityScheme` (standalone connection-level security-scheme spec registration, both packages) PLUS `mqtt5.Connect`'s error/Observer fidelity (`ConnectError.ReasonCode`/`ReasonString`, `ConnectOptions.Observer`) | A new shared/core-layer connection-security type unifying spec+runtime — explicitly rejected; `route.SecurityScheme` (already shared) plus a documented reuse convention is sufficient, no new type needed. Also out of scope: a sealed, marker-method `Capability`-style interface for connection auth — explicitly rejected, no corresponding safety gain (see "Reframed explicitly as a D-0006-pattern capability") |

## Current state (confirmed via code, for contrast — all three packages)

- **REST client-side** (`adapters/nethttp/clienttransport.go`'s
  `mergeCredentialHeaders`): `ClientImplementation.Fn` type-asserted to
  the FIXED shape `func(context.Context, []route.SecurityRequirement)
  (http.Header, error)` — bypasses `ClientMiddlewareHandler`/`EncodeIn`
  entirely.
- **REST server-side** (`adapters/nethttp/adapter.go`'s
  `runSecurityMiddleware`): `ServerImplementation.Fn` type-asserted to
  `func(context.Context, *http.Request, *Req) (map[string][]string,
  error)` — same bypass, raw `*http.Request` handed directly to the Fn.
- **events/reqreply publish-side** (`adapters/mqtt5/transport_dispatch.go`,
  `adapters/mqtt5/reqreply_transport.go`): `func(context.Context, *T,
  []route.SecurityRequirement) ([]UserProperty, error)` — also bypasses
  the agnostic `EncodeOut`/`WithSend` dispatch; ALSO note the Fn receives
  `*T` (the outgoing payload pointer) for in-payload credential
  embedding, a capability this redesign must explicitly decide to keep
  or drop (see Open design decisions).
- **Post-Fn credential validation** (`api/rest/security_dispatch.go`'s
  `ValidateSecurityCredentials`/`extractCredential`; mirrored per package):
  re-extracts the credential, strips the scheme's own prefix, validates
  against `SecurityScheme.Codec *codex.Codec[string]` — SEPARATE from the
  Fn's own return. Under the pivot, this role is naturally absorbed by
  `mw.InCodec.Validate(in)` (already running generically for ANY agnostic
  middleware, in all 3 packages) — `SecurityScheme.Codec` likely becomes
  redundant, see Open design decisions.

## API surface

```go
// api/rest
func SecurityMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string) Middleware[In, Out]

// api/events
func SecurityMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string) Middleware[In, Out]

// api/reqreply
func SecurityMiddleware[In, Out any](schemeName string, scheme SecurityScheme, scopes []string) Middleware[In, Out]
```

No NEW types are introduced beyond this signature change in each
package — `.WithSend`/`.WithReceive`/the merge-field methods are ALL
pre-existing `Middleware[In,Out]` methods, reused verbatim, per package.

**Granted-scopes convention (RESOLVED — Option 3, "conventional field"):**
`Out` keeps the EXACT SAME uniform signature every other agnostic
middleware uses (`func(ctx, In) (Out, error)` — no 3-tuple return, no
Security-specific Fn arity). A Security `Out` type is simply EXPECTED to
carry a field named `GrantedScopes map[string][]string`:

```go
type BearerAuthOut struct {
    GrantedScopes map[string][]string
    // ... any OTHER genuine response merge fields a caller wants, e.g.:
    // RefreshedSession string `header:"Set-Cookie"` (illustrative — actual
    // declaration via .WithResponseHeader/.WithResponseCookie as usual)
}
```

The adapter's dispatch reads `GrantedScopes` via
`elem.FieldByName("GrantedScopes")` — the SAME reflection technique
ALREADY used pervasively in this codebase (e.g.
`clienttransport.go`'s `elem.FieldByName("Descriptor")`) — not a new
technique, just applied to a new, conventionalized field name. `Out`
remains free to carry additional genuine response data on OTHER fields,
declared via the EXISTING `WithResponseHeader`/`WithResponseCookie`
methods, unaffected by the `GrantedScopes` convention.

```go
// adapters/nethttp (chi mirrors) + adapters/mqtt5 (zeromq/mqtt mirror) —
// Security's client/server dispatch changes from a type-asserted FIXED
// Fn shape to invoking the mw's OWN ClientMiddlewareHandler/
// MiddlewareHandler (EncodeIn/DecodeIn/EncodeOut/DecodeOut +
// WithSend/WithReceive's Fn) — the SAME dispatch already built for
// non-Security agnostic middleware in each package.
//
// Adapter-side plumbing (RESOLVED): adapters/mqtt5/adapter.go's publish
// dispatch ALREADY calls BOTH the old ClientImplementations-based
// Security path (runPublishSecurityImpls, line 997) AND the new
// ClientMiddlewareHandlers-based agnostic path (events.
// DispatchPublishMiddlewareHandlers, line 925) in the SAME function
// today. Phase 2 simply STOPS populating/reading ClientImplementations
// for Security-shaped attachments specifically — the dispatch call
// already exists and already runs. wrapPublishGeneral's SEPARATE
// general-wrap-shaped ClientImplementations usage is UNRELATED and
// explicitly untouched (out of scope). Identical dual-path structure
// confirmed in adapters/mqtt5/reqreply_transport.go.
```

## Structured errors (all implement `slog.LogValuer`)

No NEW error type needed in any package for the credential-encoding
axis — a merge field's codec rejection already surfaces as the EXISTING
`MiddlewareInputError`/`MiddlewareOutputError` (REST),
`events.MiddlewareInputError`/`MiddlewareOutputError`,
`reqreply.MiddlewareInputError`/`MiddlewareOutputError` — used by every
other agnostic middleware today; Security's credential merge fields
reuse the SAME error types per package, not new ones.
**`SecurityScheme.Codec` redundancy (RESOLVED):** confirmed via code —
`rest.SecurityScheme.Codec *codex.Codec[string]` has EXACTLY ONE
consumer, `ValidateSecurityCredentials` (`api/rest/security_dispatch.go`),
which re-validates the credential AFTER the Fn's return is already
merged into the wire request — a post-hoc re-check of content
`mw.InCodec.Validate(in)` ALREADY validates, earlier and more directly,
once a scheme migrates to the new mechanism. Confirmed zero
OpenAPI/AsyncAPI spec-rendering dependency on `Codec` (`render/openapi`/
`render/asyncapi` consume only the base `route.SecurityScheme`, which has
no `Codec` field). Plan: deprecate `SecurityScheme.Codec` once Phase 2's
full sweep deletes the old dispatch path that was its only caller;
`SecurityCredentialError` retires alongside it.

## Observer integration

No new observer hooks in any package — reuses `stats.SecurityObserver
.RecordSecurityRejection` exactly as today; a rejected credential merge
field already flows through the SAME `stats.ReportErrors`/
`"middleware:in"` location string every other agnostic middleware's
input decode failure already uses, in all 3 packages. See "Relationship
to the Observer pattern and ErrorPattern" above for the full confirmation
that Observer is fully orthogonal to this doc's layering model (ambient,
ctx-resolved, zero spec/`In`/`Out` involvement) and that Security's
Fn-error ErrorPattern-eligibility (already shipped) is preserved, not
regressed, by Phase 2.

## Unit test plan (sketch — per package, mirrored)

**Phase 1 (shared mechanism — zero behavior change, verified not assumed):**

| Test | Verifies |
|---|---|
| `TestDecodeLayer_MultiAxis_MatchesExistingRestBehavior` | `middleware.DecodeLayer` given REST's 3 axes (header/cookie/query) produces IDENTICAL results to today's `rest.buildDecodeIn` for every existing REST middleware test case |
| `TestDecodeLayer_MultiAxis_MatchesExistingEventsBehavior` | Same, for events' 2 axes (topic/property) |
| `TestDecodeLayer_MultiAxis_MatchesExistingReqreplyBehavior` | Same, for reqreply's 2 axes |
| `TestEncodeLayer_MatchesExistingBehavior` (×3 packages) | `middleware.EncodeLayer`'s encode-side mirror, same cross-check |
| **Full regression**: every EXISTING `*_test.go` test in `api/rest/transform_test.go`/`api/events/transform_test.go`/`api/reqreply/transform_test.go`-equivalents must continue passing UNCHANGED after the Phase 1 migration — this is the actual proof of "zero behavior change," not a design-review claim (per this repo's own established "verify by migration, not by review" lesson) |

**Phase 2 (Security as the proving consumer):**

| Test | Verifies |
|---|---|
| `TestSecurityMiddleware_WithSend_MergeField_EncodesCredential` (×3 packages) | A `.WithSend` Fn returning `Cred{Token: "x"}` + a merge-field declaration results in the correctly-formatted wire value — reusing each package's NOW-SHARED `DecodeLayer`/`EncodeLayer`, not a new mechanism |
| `TestSecurityMiddleware_WithReceive_DecodesCredentialBeforeFn` (×3) | An incoming message's declared field is decoded into `Cred` BEFORE the `.WithReceive` Fn runs |
| `TestSecurityMiddleware_GrantedScopes_ReadViaReflectedField` (×3) | The RESOLVED `GrantedScopes` convention: dispatch reads `Out.GrantedScopes` via `elem.FieldByName("GrantedScopes")` and feeds it into an UNCHANGED `middleware.CheckScopes` call |
| `TestSecurityMiddleware_Out_CarriesAdditionalResponseFields` (×3) | A Security `Out` type with BOTH `GrantedScopes` AND a genuine response merge field (e.g. a declared response header) — confirms the convention doesn't foreclose real response data |
| `TestSecurityMiddleware_CredentialCodecRejects_MiddlewareInputError` (×3) | A credential field's codec validation failure surfaces as the EXISTING, package-specific `MiddlewareInputError`, not a new or unified type |
| `TestSecurityMiddleware_RawRouteVsRouteHandle_Unaffected` (REST only) | This redesign does not reintroduce or interact with the separate `GlobalSecurity` dual-mode dispatch gap (D-0001 Addendum 6) — independent concerns |
| `TestSecurityMiddleware_FnError_StillErrorPatternEligible` (×3 packages) | Confirms Security's already-shipped ErrorPattern-eligibility for Fn errors (session-review finding H1) is PRESERVED once dispatched through the shared mechanism — a declared `ErrorPattern` still matches a Security Fn's business error after migration |
| `TestSecurityMiddleware_CredentialDecodeFailure_NotErrorPatternEligible` (×3 packages) | Confirms a credential merge-field/`InCodec` decode failure stays classified as `isFnError: false` (NOT ErrorPattern-eligible) — consistent with today's `ValidateSecurityCredentials` bypass behavior, reached via a different code path |
| `TestDispatchMiddlewareHandlers_FailFast_StopsAtFirstFailure` (×3, Phase 1) | Confirms the RESOLVED fail-fast decision is preserved by the Phase 1 migration — a 2nd/3rd stacked layer's Fn must NOT run after an earlier layer already failed |
| `TestSecurityMiddleware_InPayloadMutation_NotSupported` (mqtt5 only) | Confirms the RESOLVED "drop" decision — migrating a Security middleware onto the new mechanism has no way to mutate the outgoing payload; documents the limitation via a compile-shape/doc-level test, not a runtime capability test |

**Phase 3 (middleware-derived value propagation):**

| Test | Verifies |
|---|---|
| `TestSetContextFieldFromIn_HandlerRetrievesDerivedValue` (×3 packages, incl. events Subscribe) | A `SetContextFieldFromIn`-linked `ContextField` is retrievable, fully typed, inside the route/channel handler — the user's original "UserID in the handler" use case, proven end-to-end |
| `TestSetContextFieldFromOut_CallerRetrievesDerivedValue` (REST + reqreply; events Publish) | Same, for the produced-value direction |
| `TestSetContextFieldFromIn_EventsSubscribe_NoOutNeeded` (events only) | Confirms `SetContextFieldFromIn` works on events' Subscribe despite its `WithReceive` having no `Out` at all — the asymmetry-aware part of the design |
| `TestSetContextFieldFromOut_EventsSubscribe_DoesNotCompile` (events only, compile-time check) | Confirms (via a `//go:build` compile-fail test or similar) that `SetContextFieldFromOut` genuinely cannot be declared against events' `WithReceive` shape — Go's own type system catches it, not a runtime guard |
| `TestContextField_SharedAcrossStackedLayers` (×3) | A value `Set` by an EARLIER middleware layer is retrievable by a LATER layer or the handler — the "propagate between middleware handlers and the route/channel handler" requirement |

**Phase 4 (connection-level auth spec registration):**

| Test | Verifies |
|---|---|
| `TestClient_AddConnectSecurityScheme_StandaloneRegistration` (events) | A scheme registered via `AddConnectSecurityScheme`, referenced by ZERO channels, still appears in `components/securitySchemes` |
| `TestBuilder_AddConnectSecurityScheme_StandaloneRegistration` (reqreply) | Same, for `reqreply.Builder` |
| `TestClient_AddConnectSecurityScheme_LastRegisteredWins` (events + reqreply) | A channel/route re-registering the SAME scheme name still wins on collision — unchanged aggregation precedence, now with a 3rd contributor |
| `TestServer_Security_ReferencesConnectOnlyScheme_NoDanglingRef` (events + reqreply) | `AddServer`'s `Server.Security` referencing a connect-only scheme name resolves with the scheme present in `components/securitySchemes` — no dangling reference |
| `TestConnect_DropInMQTTClientForReqreply` (mqtt5, reqreply) | Confirms the plain `MQTTClient` returned by `mqtt5.Connect` works unmodified as the `MQTTClient` passed to reqreply's `Call`/`Serve` — zero adapter change needed (the PRIMARY mechanism) |
| `TestSecuredClient_DropInForReqreply` (mqtt5, reqreply) | Same, for `*mqtt5.SecuredClient` (the OPTIONAL secondary pre-check wrapper) — confirms it promotes transparently too |
| `TestConnectError_CarriesReasonCodeOnAuthRejection` (mqtt5) | A CONNACK reason code ≥ 0x80 (e.g. `0x86`/`0x87`) is captured into `ConnectError.ReasonCode`/`ReasonString`, not discarded — `errors.As` callers can branch on it directly |
| `TestConnectError_ZeroReasonCodeOnDialFailure` (mqtt5) | A "dial"-stage failure (no CONNACK ever received) leaves `ReasonCode`/`ReasonString` at their zero value — confirms the two failure modes stay distinguishable |
| `TestConnect_ObserverReportsSecurityRejectionOnAuthFailure` (mqtt5) | `ConnectOptions.Observer`, when set and implementing `stats.SecurityObserver`, receives `RecordSecurityRejection("connect", ...)` on an auth-rejection CONNACK — mirrors `NewSecuredClient`'s already-shipped pattern |

## Files to create/modify

| File | Responsibility | Phase |
|---|---|---|
| `adapters/nethttp/clienttransport.go` | **Prerequisite for Phase 2 (REST only)**: add `ClientMiddlewareHandlers` dispatch to `Call` AND `Consume` — mirroring `binding.go`/`client.go`'s existing `dispatchClientMiddlewareIn`/`Out` calls. Closes the confirmed, pre-existing gap blocking Security's migration AND SSE `Consume`'s client-side agnostic middleware support in one pass | Prereq |
| `middleware/layer.go` (new) | `Axis[T]`/`AxisVars[T]`, `DecodeLayer`/`EncodeLayer` — the new shared mechanism | 1 |
| `api/rest/transform.go` | Migrate `buildDecodeIn`/`buildEncodeIn`/`buildEncodeOut`/`buildDecodeOut` onto `middleware.DecodeLayer`/`EncodeLayer` | 1 |
| `api/events/transform.go` | Same migration, 2-axis (topic/property) | 1 |
| `api/reqreply/transform.go` | Same migration, 2-axis (topic/property), fully duplex like REST | 1 |
| `api/rest/middleware_declaration.go` | `SecurityMiddleware`'s generalized signature | 2 |
| `api/rest/middleware.go`/`client.go`/`adapter.go` (nethttp/chi) | Replace fixed-shape Fn type-assertions with dispatch through Phase 1's shared mechanism | 2 |
| `api/events/middleware_declaration.go`, `api/events/builder.go` | Same generalization for events | 2 |
| `api/reqreply/middleware_declaration.go`, `api/reqreply/route.go` | Same generalization for reqreply | 2 |
| `adapters/mqtt5/{transport.go,transport_dispatch.go,reqreply_transport.go}`, `adapters/zeromq`, `adapters/mqtt` | Replace fixed-shape Fn type-assertions with dispatch through the mw's own handler | 2 |
| `examples/go-edge-models/app/registry/auth.go` | Migrate `newAuthCredentialFunc`/`BearerAuthDeclaration` onto the new pattern — the motivating real case (REST) | 2 |
| `docs/features/security.md` | Full rewrite of the credential-Fn sections to show the new declarative pattern, for all 3 packages | 2 |
| `docs/design/d-0003-codec-declared-middlewares.md` | Add a new Addendum documenting `middleware.DecodeLayer`/`EncodeLayer` as the architectural embodiment of "middleware is a partial route/channel definition, stacked" — the mechanism itself is now the documentation, not just prose describing a convention | 1 |
| `middleware/context_field.go` | Add `SetContextFieldFromIn`/`SetContextFieldFromOut` declarative link methods to `Middleware[In,Out]`; extend `EnsureContextFields` call sites | 3 |
| `adapters/nethttp/clienttransport.go` | Add `EnsureContextFields` + `SetContextFieldFromIn`/`Out` dispatch to the CLIENT side (currently server-only) | 3 |
| `adapters/mqtt5/{transport.go,reqreply_transport.go}`, `adapters/zeromq`, `adapters/mqtt` | Greenfield `ContextField`/`SetContextFieldFromIn`/`Out` integration — confirmed zero existing usage in events/reqreply today | 3 |
| `api/events/builder.go` | Add `Client.AddConnectSecurityScheme` | 4 |
| `api/reqreply/builder.go` | Add `Builder.AddConnectSecurityScheme` | 4 |
| `adapters/mqtt5/errors.go` | Extend `ConnectError` with `ReasonCode byte`/`ReasonString string`, populated from the `*Connack` paho already returns on a "connect"-stage failure (today discarded) | 4 |
| `adapters/mqtt5/connect.go` | Add an optional `Observer` field to `ConnectOptions`; report `stats.SecurityObserver.RecordSecurityRejection("connect", ...)` on an auth-rejection CONNACK | 4 |

## Out of scope (Phase 2)

- OAuth2 token-endpoint response body credentials (a fundamentally
  different shape).
- Automatic prefix/format derivation from `scheme.Scheme` — Phase 1 keeps
  formatting explicit in the closure, matching the existing merge-field
  convention exactly.
- `api/mcp`/`ports` — confirmed (per `mcp-ports-declarative-middleware.md`,
  a sibling roadmap doc) to have a DIFFERENT, single-phase attachment
  model with no spec/two-phase declare-dispatch split; Security is
  already permanently N/A for MCP and not revisited here.
- **mqtt5's in-payload credential embedding via `*T` mutation on
  publish (RESOLVED: dropped)** — confirmed via code this is the ONLY
  place in the entire codebase (including the mechanism being replaced)
  where a middleware Fn gets automatic mutation access to an OUTGOING
  payload; every other send-side mechanism in all 3 packages
  (`ClientTransform`, the agnostic `WithSend` path) deliberately passes
  the value by VALUE, not pointer, on the theory the caller already owns
  and can mutate it before calling. Confirmed zero real test/example in
  this repo exercises the capability today (every test using this exact
  Fn signature leaves the `*T` parameter unused). A caller needing
  in-payload embedding does it themselves, directly, inside their own
  handler — not via the generic middleware mechanism.

## Open design decisions — ALL RESOLVED

All 8 items below were open as of this doc's prior draft. Each is now
resolved, with the decision and rationale recorded directly (not just a
pointer to a separate discussion) so this doc stays self-contained.

1. **Fail-fast vs. accumulate-all errors across stacked layers —
   RESOLVED: fail-fast (keep today's behavior).**
   `middleware.DecodeLayer`'s dispatch across stacked layers stops at
   the FIRST failing layer, exactly matching
   `DispatchMiddlewareHandlers`'s current, confirmed behavior — NOT
   changed to accumulate-all. Rationale: avoids a later layer's side
   effects (e.g. a credential Fn making a network call) running
   unconditionally after an earlier layer has already failed. This was
   surfaced via a tangential question about modeling middleware as an
   Either(Result,Error)-style pipeline — confirmed go-codex's existing
   `(T, error)` dispatch already IS this "railway" shape; no new
   mechanism (e.g. a formal `Either[Result,Error]` wrapper type) is
   introduced.
2. **`codex.EncodeVars`/`sparseFieldCodec[T]` extension — RESOLVED:
   yes, in scope for Phase 1.** Extend `EncodeVars` to check for the
   existing `sparseFieldCodec[T]` companion capability, mirroring
   `Struct`'s own `Encode` loop (`codex/object.go:150`) exactly — zero
   new codec concept, reuses `OmitEmptyField`/`MaybeField` as-is. Small,
   additive, not a hard prerequisite for `DecodeLayer`/`EncodeLayer`'s
   main work but bundled into the same phase.
3. **`Axis[T]`/`AxisVars[T]` shape: positional vs. named — RESOLVED:
   positional.** Every current call site has a FIXED, compile-time-known
   axis count (REST: 3; events/reqreply: 2), declared once per package —
   no dynamic/variable-axis scenario exists anywhere to justify a named
   `map[string]map[string]string` shape. Positional also matches today's
   existing calling convention exactly (`buildDecodeIn(headerVars,
   cookieVars, queryVars map[string]string)` is already positional). A
   throwaway spike against REST's 3-axis case still happens as the FIRST
   concrete Phase 1 implementation step — this decision gives that spike
   a target shape to build against, rather than resolving the shape
   question blind.
4. **Risk of subtle behavior regression during Phase 1 extraction —
   not a decision, a carried-forward discipline.** No choice to make
   here; the test plan's "every existing test must pass unchanged" bar
   IS the mitigation. Flagged as a real risk to watch during migration,
   not assumed away — but nothing to resolve in the design itself.
5. **`adapters/mqtt5`/`zeromq`/`mqtt` adapter-side plumbing — RESOLVED:
   small, well-scoped changes needed, confirmed via code.** Traced
   `adapters/mqtt5/adapter.go`'s publish dispatch directly: it ALREADY
   calls BOTH the old `ClientImplementations`-based Security path
   (`runPublishSecurityImpls`, line 997) AND the new
   `ClientMiddlewareHandlers`-based agnostic path (`events.
   DispatchPublishMiddlewareHandlers`, line 925) in the SAME function
   today. Phase 2 simply stops populating/reading `ClientImplementations`
   for Security-shaped attachments — the new path's dispatch call already
   exists and already runs; `wrapPublishGeneral`'s separate general-wrap
   use of `ClientImplementations` is untouched. Identical dual-path
   structure confirmed in `adapters/mqtt5/reqreply_transport.go`.
6. **`SecurityScheme.Codec` vs. `Middleware[In,Out].InCodec` redundancy
   — RESOLVED: confirmed redundant, plan to deprecate.** `SecurityScheme
   .Codec` has exactly one consumer (`ValidateSecurityCredentials`, a
   post-hoc re-check after the Fn's return is already merged into the
   wire request) and zero OpenAPI/AsyncAPI spec-rendering dependency.
   `mw.InCodec.Validate(in)` already validates the same semantic content,
   earlier and more directly, once a scheme migrates. Deprecate
   `SecurityScheme.Codec`/`SecurityCredentialError` once Phase 2's full
   sweep deletes the old dispatch path that was `Codec`'s only caller.
7. **Granted-scopes shape — RESOLVED: Option 3, a conventional
   `GrantedScopes` field on `Out`.** `Out` keeps the EXACT uniform
   `func(ctx, In) (Out, error)` signature every other agnostic middleware
   uses — no 3-tuple return, no Security-specific Fn arity. A Security
   `Out` type is expected to carry a `GrantedScopes map[string][]string`
   field, read by dispatch via `elem.FieldByName("GrantedScopes")` (the
   same reflection technique already used pervasively in this codebase,
   e.g. `clienttransport.go`'s `elem.FieldByName("Descriptor")`). `Out`
   stays free to carry additional genuine response merge fields on other
   fields of the same struct. Considered and rejected: `Out` being the
   map directly (forecloses genuine response data); a separate 3-tuple
   return channel (reintroduces Security-specific Fn arity).
8. **mqtt5's in-payload credential embedding (`*T` on publish) —
   RESOLVED: dropped.** Confirmed via code this is the ONLY place in the
   entire codebase — including the mechanism being replaced — where a
   middleware Fn gets automatic mutation access to an OUTGOING payload.
   Every other send-side mechanism in all 3 packages (`ClientTransform`,
   the agnostic `WithSend` path) deliberately passes the value by VALUE,
   not pointer (confirmed: REST's/events'/reqreply's `ClientTransform`
   doc comments all say the caller already owns and can mutate its own
   value before calling). Confirmed zero real test/example in this repo
   exercises the capability today (every test using this exact Fn
   signature leaves the `*T` parameter unused). Accepted as a documented
   Phase 2 limitation (see "Out of scope" above) — a caller needing
   in-payload embedding does it themselves, inside their own handler.
9. **Backward-compatibility migration mechanics for Phase 2 — RESOLVED:
   mechanical, representative-sample-then-full-sweep migration, NO
   transitional dual-dispatch window.** Follows directly from this doc's
   own already-confirmed scope decision (a full breaking replacement,
   not an additive/dual-dispatch mechanism) — the SAME migration
   discipline already used for this session's other breaking changes
   (e.g. middleware-consolidation's Phase D). `adapters/nethttp/
   *_test.go` alone has 15+ existing credential-Fn call sites; mqtt5/
   zeromq/mqtt add more — migrated mechanically, not via a transitional
   window. (Phase 1 itself has NO backward-compatibility concern — it is
   purely internal.)
