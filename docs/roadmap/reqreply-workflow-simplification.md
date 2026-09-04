# ReqReply Workflow Simplification — design decisions

> **Status:** PLANNED — no implementation yet. Replaces the now-deleted
> "Events/ReqReply/Ports Workflow Simplification" doc's `api/reqreply`-
> scoped content (that doc's pub/sub-scoped content is superseded by
> [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md),
> now fully implemented). Spun out of a dedicated thin-adapter review
> (see [Protocol-Native Feature Declarations](protocol-native-features.md)'s
> status banner) that confirmed REST and pub/sub already follow the
> codebase's guiding principle — adapters stay THIN (pure IO, attach-only,
> adapter-specific config/options); ALL workflow (middleware/handler
> attachment, calling client/server functions) lives in the `api/*`
> declaration layer — but `api/reqreply` does not. This doc designs the
> fix: a `reqreply.Client`/`reqreply.Server` + `Attach` architecture
> mirroring `rest.Client`/`rest.Server` (the reference model), PLUS
> folds forward the still-relevant middleware/security decisions the
> deleted doc had already resolved for `api/reqreply` specifically.
> [← Back to Roadmap](index.md)

## Why this exists

A dedicated review (triggered while reviewing
[Protocol-Native Feature Declarations](protocol-native-features.md)
against the codebase's guiding principle) confirmed, via direct code
inspection:

- **REST already fully follows the thin-adapter principle.**
  `rest.Client`/`rest.Server` own `Attach`/`Call`
  (`api/rest/builder.go`); `adapters/nethttp.Attach`/`AttachMux`,
  `adapters/chi.AttachRouter` are thin `ClientTransport`/
  `ServerTransport` binders ONLY — no workflow logic lives in the
  adapter package.
- **Pub/sub (`api/events`) already fully follows it too.**
  `events.Client` owns `Attach`/`Publish`/`Subscribe`/
  `ServeSubscribers`; `mqtt5.Attach`/`mqtt.Attach`/`zeromq.Attach` are
  thin `Transport` binders. Decision 7 of
  [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)
  added a second, complementary spec-free path
  (`events.PublishHandle`/`SubscribeHandle` + each adapter's
  `NewPublishTransport`/`NewSubscribeTransport`) that keeps the SAME
  split: the adapter provides a thin per-`T` transport value; the
  workflow FUNCTION lives in `api/events`.
- **`api/reqreply` is the one confirmed violation.** It has NO
  `Client`/`Server`/`Attach` type at all. The entire request-reply
  WORKFLOW — topic subscribe, correlation-ID generation/matching,
  reply-topic management, handler dispatch loop — lives directly
  inside `adapters/mqtt5.Serve`/`.Call` and
  `adapters/zeromq.Serve`/`.Call`/`.ServeRouter`. Users call ADAPTER
  functions directly today (`mqtt5.Serve(ctx, client, router, handle,
  fn, opts)`, `mqtt5.Call(ctx, client, router, handle, req, opts)`) —
  the opposite of every other boundary's shipped design.
- This is not a hypothetical concern: it is exactly where
  `protocol-native-features.md`'s own confirmed, already-shipped
  protocol-native capability — MQTT5's Response Topic + Correlation
  Data — is hardwired (`adapters/mqtt5/reqreply.go` reads/writes
  `msg.Properties.ResponseTopic`/`CorrelationData` directly inside
  `Serve`/`Call`). Fixing `api/reqreply`'s architecture is a
  prerequisite for cleanly exposing that capability (and Shared
  Subscriptions, for reply-topic fan-out) as real `ProtocolFeature`
  declarations.

## The reference model — `rest.Client`/`rest.Server`

```go
// api/rest/builder.go (existing, shipped)
type ServerTransport interface{ /* adapter-implemented IO binding */ }
type ClientTransport interface{ /* adapter-implemented IO binding */ }

type Server struct { /* accumulates registered routes/spec */ }
func (s *Server) Attach(t ServerTransport) error
func (s *Server) Serve(ctx context.Context) error // or equivalent wiring

type Client struct { /* accumulates registered routes/spec */ }
func (c *Client) Attach(t ClientTransport) error
func (c *Client) Call(ctx context.Context, route any, req any) (any, error)
```

`adapters/nethttp.AttachMux(builder *rest.Server, mux *http.ServeMux, addr
string) error` and `adapters/nethttp.Attach(client *rest.Client, httpClient
*http.Client, baseURL string) error` are THIN — they wire the adapter's
own IO primitive (an `http.ServeMux`/`http.Client`) to the `rest.Server`/
`rest.Client` value as its `ServerTransport`/`ClientTransport`. All
routing, middleware dispatch, security enforcement, and request/response
lifecycle logic lives in `api/rest` itself, shared identically by
`nethttp` and `chi`.

## Proposed design — `reqreply.Client`/`reqreply.Server`

### Decision 1 — introduce `Client`/`Server` + `Attach`, mirroring REST exactly

```go
// api/reqreply (new)
type ServerTransport interface{ /* adapter-implemented IO binding */ }
type ClientTransport interface{ /* adapter-implemented IO binding */ }

type Server struct { /* accumulates registered routes/spec, mirrors rest.Server */ }
func (s *Server) Attach(t ServerTransport) error
func (s *Server) Serve(ctx context.Context) error // runs the dispatch loop

type Client struct { /* accumulates registered routes/spec, mirrors rest.Client */ }
func (c *Client) Attach(t ClientTransport) error
func (c *Client) Call(ctx context.Context, route any, req any) (any, error)
```

`Server.Serve`/`Client.Call` become the SOLE public entry points a
caller uses — mirroring `rest.Server.Serve`/`rest.Client.Call` and
`events.Client.ServeSubscribers`/`.Publish` exactly. The dispatch loop
that today lives inside `adapters/mqtt5.Serve`/`adapters/zeromq.Serve`
(reading `handle.Descriptor.Security`, decoding the request, running
the domain handler, encoding and publishing the reply, correlating
requests to replies) moves into `api/reqreply` itself, operating
generically against `ServerTransport`/`ClientTransport` — NOT against
`*pahomqtt5.Publish`/ZMQ frames directly.

Like REST's `Call`/`Serve` (which take `any` and recover concrete types
via reflection, since `Client`/`Server` are non-generic Go types and Go
forbids generic methods on non-generic types), `reqreply.Client.Call`/
`reqreply.Server.Serve` follow the SAME reflection-based shape — this is
a structural necessity, not a design choice, and mirrors
`events.Transport`'s identical justification in
[Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
Decision 5.

### Decision 2 — each adapter implements a THIN `ServerTransport`/`ClientTransport`

```go
// adapters/mqtt5 (reworked)
func Attach(server *reqreply.Server, client MQTTClient, router MQTTRouter) error
func AttachClient(client *reqreply.Client, mqttClient MQTTClient, router MQTTRouter) error
```

The adapter's job shrinks to: (a) satisfy the `ServerTransport`/
`ClientTransport` interface by translating `api/reqreply`'s generic
dispatch calls into MQTT5-specific IO (subscribe to the route's topic,
publish the reply with `ResponseTopic`/`CorrelationData` set, etc.),
and (b) carry adapter-specific CONFIG/OPTIONS only (QoS, retained,
`ConnectOptions`) — no request/reply WORKFLOW logic of its own. This is
the same shrink `nethttp.Attach`/`mqtt5.Attach` (pub/sub) already went
through relative to their own pre-Decision-5 designs.

`adapters/mqtt` (v3) and `adapters/zeromq` implement the SAME thin
interfaces, each translating the generic dispatch into their own native
IO — `mqtt`(v3) via plain `pahomqtt.Client.Subscribe`/`.Publish` (no
Response Topic/Correlation Data — see Decision 4 below); `zeromq` via
its own `Serve`/`Call`/`ServeRouter`'s existing socket-frame handling,
relocated behind the transport interface instead of exposed as the
public entry point.

### Decision 3 — fold forward the still-relevant middleware/security decisions

The deleted doc had already resolved 4 middleware/security decisions
for `api/reqreply` specifically (Decisions 1/3/4 of that doc). These
remain sound and are folded forward here, adjusted for the new
`Client`/`Server` shape:

- **`reqreply.Route` gains `.Use()`/`.HandleMW()`/`.ClientMW()`**,
  mirroring `rest.Route`'s equivalents exactly — `reqreply.RouteOpt` is
  already the same `interface{ applyRoute(*routeBuilder) }` shape
  `rest.RouteOpt` is, ready to extend with zero structural changes.
  `RouteHandle` gains `Implementations []middleware.ServerImplementation`
  / `ClientImplementations []middleware.ClientImplementation` fields,
  populated by `Route.Register`/`Route.ClientHandle` exactly like
  `rest.RouteHandle` already populates its own.
- **No whole-builder `Serve`/`Call` split is needed beyond
  `Server.Serve`/`Client.Call` themselves.** Unlike REST's old
  per-route `Handler`+`Register` split (which needed consolidating),
  `reqreply` never had that split — `Serve`/`Call` have always taken
  one handle directly. The ONLY change from today is WHERE the
  security/credential function is read from: from a per-call
  `Options.SecurityFunc`/`CredentialFunc` field to the
  handle-attached `Implementations`/`ClientImplementations` populated
  at declare time (previous bullet) — `Server.Serve`/`Client.Call`
  read `handle.Implementations`/`ClientImplementations` automatically,
  mirroring `nethttp.Serve`/`Call`'s already-shipped read pattern.
- **Per-adapter Fn shapes stay adapter-specific, not universal** — MQTT5,
  MQTT3, and ZeroMQ have three incompatible native message envelope
  types (`*pahomqtt5.Publish`, `pahomqtt.Message`, raw ZMQ frames), so
  each `ServerTransport`/`ClientTransport` implementation recognizes
  its OWN Fn shape internally, translating to/from the transport-neutral
  `middleware.ServerImplementation`/`ClientImplementation` contract
  `api/reqreply` itself operates on. `mqtt5`'s shape is the clearest
  translation target (confirmed via `adapters/mqtt5/adapter.go`'s
  existing `makeSubscribeMessageHandler`/`Publish` pattern); `mqtt`(v3)
  and `zeromq`'s shapes need a dedicated follow-up pass (see
  "Remaining open items" below — this was ALSO left open in the
  deleted doc and is not newly introduced by this rework).
- **`ports` needs no new capability.** Once `Route.Register(builder)`
  populates `handle.Implementations`/`ClientImplementations` itself,
  `ports.PluginReqReplyPattern` gets this for free via the SAME
  `.Register(builder)` delegation it already performs for every other
  handle field — no `ports`-specific changes required.

### Decision 4 — `mqtt` (v3)'s permanent protocol limitation stays permanent

MQTT 3.1.1 has no Response Topic/Correlation Data property (MQTT5-only)
— confirmed via the protocol spec and `adapters/mqtt`'s own existing
doc comments. `adapters/mqtt` therefore CANNOT implement a full
`reqreply.ServerTransport`/`ClientTransport` the way `mqtt5` can; it
either (a) does not implement the reqreply transport interfaces at all
(reqreply over MQTT 3.1.1 remains unsupported, matching today's actual
state — `api/reqreply` has never been wired to `adapters/mqtt`), or (b)
implements a documented SUBSET using an application-level convention
(e.g. a well-known reply-topic-per-request-topic naming scheme instead
of a protocol-native Response Topic) — NOT decided here, flagged for
implementation time.

## Confirmed adapter capability matrix (carried forward, unchanged)

| Capability | `mqtt` (v3) | `mqtt5` | `zeromq` |
|---|---|---|---|
| Connection-level `SecuredClient`/`ConnectSecurityScheme` | ✅ | ✅ | ❌ (see [ZeroMQ Security Mechanism](zeromq-security.md)) |
| Message-level subscribe-side `SecurityFunc` | ✅ | ✅ | ❌ |
| Message-level publish-side `CredentialFunc` | ❌ (protocol limit — no per-message property channel) | ✅ | ❌ |
| Native Response Topic + Correlation Data (reqreply viability) | ❌ (protocol limit) | ✅ | n/a (own correlation mechanism) |
| Scope-grant / `middleware.CheckScopes` integration | ❌ | ❌ | ❌ |
| Handle-attached implementation (vs. per-call `Options`) | ❌ today | ❌ today | ❌ today |

The bottom two rows are what Decision 3 (above) closes, across all
three transports, once implemented.

## Relationship to `protocol-native-features.md`

Once `reqreply.Server`/`Client`/`Attach` land, MQTT5's Response Topic +
Correlation Data — currently hardwired inside `adapters/mqtt5/reqreply.go`
— becomes expressible as a real `ProtocolFeature` (or simply remains an
implicit, always-on capability of `mqtt5`'s `ServerTransport`/
`ClientTransport` implementation, since EVERY mqtt5 reqreply route needs
it — there may be nothing to "declare," since it is not optional the way
Shared Subscriptions are). Shared Subscriptions (`$share/group/topic`)
for reply-topic fan-out across multiple `Server` instances IS a genuine
candidate for a declared `ProtocolFeature` on a `reqreply.Route`, mirroring
the pub/sub use case in
[Protocol-Native Feature Declarations](protocol-native-features.md)
directly. This determination is deferred to implementation time, once
the `Client`/`Server` shape (this doc) actually exists to declare
features against.

## Escape hatches (carried forward from the deleted doc, still accurate)

1. **`Descriptor.Security`/`GlobalSecurity` are never even read by
   `adapters/zeromq`'s reqreply `Call`/`Serve` today** — confirmed via
   grep. EVERY zeromq reqreply call is unconditionally unenforced,
   regardless of what the route declares. This rework does not
   automatically fix this — `zeromq`'s `ServerTransport`/
   `ClientTransport` implementation must actually read and enforce
   `Implementations`/`ClientImplementations` once Decision 3 lands.
2. **`mqtt`(v3) publish-side has NO credential mechanism, by protocol
   limitation** — not a gap to close, must stay documented so no future
   design assumes parity with `mqtt5` is achievable here.
3. **`SecurityScheme.Codec` nil = "no format validation"** — an
   explicit, documented escape hatch (same as REST): the security Fn
   receives the raw, unvalidated credential string when `Codec` is nil.
4. **Last-registered-wins on `WithSecurityScheme` name collisions**
   across routes sharing a builder — same policy as REST, silent (no
   error).

## Remaining open items (deferred to implementation time)

- Exact reflection-based `Call`/`Serve` signatures for `reqreply.Client`/
  `Server` — sketched above, not locked. Should mirror
  `rest.Client.Call`/`events.Client.Publish`'s exact `any`-typed shape
  and error taxonomy (`NoTransportAttachedError`-equivalent,
  `TransportTypeMismatchError`-equivalent) for consistency.
- `mqtt`(v3) and `zeromq`'s per-adapter Fn shapes for `HandleMW`/
  `ClientMW` (Decision 3) remain unresolved — `zeromq` in particular may
  need a NEW wire-level credential convention (an additional frame)
  before any Fn shape can be finalized. This is genuinely new protocol
  design, not a mirror of an existing mechanism — carried forward
  unchanged from the deleted doc's own "Remaining open items".
  (`mqtt`(v3)'s publish-side is a CONFIRMED permanent protocol
  limitation, not an open question — see Decision 4.)
- Whether `mqtt`(v3) implements a documented subset of the reqreply
  transport (application-level reply-topic convention) or does not
  implement it at all (Decision 4) — not decided.
- Migration path for existing callers of `adapters/mqtt5.Serve`/`.Call`
  and `adapters/zeromq.Serve`/`.Call`/`.ServeRouter` (breaking change) —
  needs an explicit checklist before implementation, mirroring
  [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md)'s
  own migration rounds (migrate every real example, not just tests,
  before deleting the old adapter-level entry points).
- Whether Response Topic + Correlation Data should be a DECLARED
  `ProtocolFeature` at all, or remain an implicit, always-on capability
  of `mqtt5`'s reqreply transport (see "Relationship to
  `protocol-native-features.md`" above) — not decided, deferred until
  the `Client`/`Server` shape exists to prototype against.

No implementation has started. A future session should pick the lowest-
risk starting point first — likely `mqtt5` (closest existing analogue,
clearest Fn-shape translation, and the transport with the concrete
Response Topic/Correlation Data payoff) — before `mqtt`(v3)/`zeromq`.
