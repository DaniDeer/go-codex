# ReqReply Workflow Simplification — design decisions

> **Status:** PLANNED — no implementation yet, but the core mechanism is
> now CONFIRMED via 5 rounds of throwaway Go prototypes and considered
> READY FOR IMPLEMENTATION. Final confirmed shape, in brief (see
> Decisions 1-5 for full evidence): `reqreply.Server` UNIFIES what a
> now-RETIRED, separate `Builder` type did (spec accumulation,
> `AddGlobalSecurity`) with dispatch/transport — ONE type, mirroring
> `rest.Server`'s own unification exactly. Server-side declaration is
> ONE fluent chain — `route.WithHandler(fn).Register(server)` — matching
> REST's REAL, dominant idiom byte-for-byte (an earlier round's separate
> free `reqreply.Handle(server, handle, fn)` function is superseded).
> `Server.Serve(ctx)` dispatches every registered route CONCURRENTLY
> (one goroutine per route, confirmed necessary — a sequential loop
> would starve every `zeromq` route after the first, since `zeromq.
> Serve` blocks forever while `mqtt5.Serve` returns immediately).
> `Client.Call` accepts EITHER a raw, unregistered `Route` (REST-style,
> zero `Server` needed, `GlobalSecurity` invisible — the SAME accepted
> limitation REST's own `ClientHandle()` has) OR an already-registered
> `*RouteHandle` (with `GlobalSecurity` enforced) — strictly MORE
> flexible than REST, not merely matching it. An ADDITIVE, reqreply-only
> async `CallAsync`/`Future[Resp]` (Decision 5) exists alongside the
> blocking `Call`, needed because reqreply's transport (unlike REST's
> synchronous HTTP) is genuinely asynchronous underneath. `zeromq.Attach`
> takes a topic→socket mapping (not a shared client value), validated at
> Attach time. A NEW, consolidated `examples/reqreply-api` mini-project
> (mirroring `examples/rest-api`'s layout) is now planned — see
> "Example mini-project" below — replacing 3 existing examples
> (`adapters-zeromq-reqrep`, `adapters-zeromq-dealer-router` deleted
> entirely; `adapters-mqtt5`'s request-reply demos stripped out and
> rebuilt there) with 7 focused demos, INCLUDING a dedicated
> `CallAsync`/`Future` async-call-and-promise demo. Remaining open items
> (below) are genuinely deferred design choices (mqtt v3/zeromq Fn
> shapes, migration checklist, Feature-declaration status for Response
> Topic/Correlation Data), not blocking gaps.
> Replaces the now-deleted
> "Events/ReqReply/Ports Workflow Simplification" doc's `api/reqreply`-
> scoped content (that doc's pub/sub-scoped content is superseded by
> [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md),
> now fully implemented). Spun out of a dedicated thin-adapter review
> (see [Feature/Provider](protocol-native-features.md)'s
> status banner, then titled "Protocol-Native Feature Declarations") that confirmed REST and pub/sub already follow the
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
[Feature/Provider](protocol-native-features.md) — then titled
"Protocol-Native Feature Declarations" — against the codebase's guiding
principle) confirmed, via direct code inspection:

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
  [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)
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
  Subscriptions, for reply-topic fan-out) as real `Feature`
  declarations under that doc's now-generalized model.

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
// ServerTransport's shape is CONFIRMED (not a placeholder) — Serve is
// called ONCE PER REGISTERED ROUTE, any-typed/reflection-recovered by
// the adapter, exactly like ClientTransport.Call. See Decision 1's
// "concurrent dispatch" finding below for why Server.Serve calls this
// PER ROUTE, CONCURRENTLY — not once for the whole Server the way
// rest.ServerTransport.Serve(ctx) (no args) does.
type ServerTransport interface {
    Serve(ctx context.Context, route any, fn any) error
}
type ClientTransport interface{ /* adapter-implemented IO binding */ }

type Server struct { /* accumulates registered routes/spec, mirrors rest.Server */ }
func (s *Server) Attach(t ServerTransport) error
func (s *Server) Serve(ctx context.Context) error // runs ALL registered routes CONCURRENTLY — see below

type Client struct { /* accumulates registered routes/spec, mirrors rest.Client */ }
func (c *Client) Attach(t ClientTransport) error
func (c *Client) Call(ctx context.Context, route any, req any) (any, error)
```

`Server.Serve`/`Client.Call` become the SOLE public entry points a
caller uses — mirroring `rest.Server.Serve`/`rest.Client.Call` and
`events.Client.ServeSubscribers`/`.Publish` exactly. The dispatch loop
that today lives inside `adapters/mqtt5.Serve`/`adapters/zeromq.Serve`
(reading `handle.Security` directly — confirmed
`adapters/mqtt5/reqreply.go:266,516`, decoding the request, running
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
[Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
Decision 5.

**CONFIRMED, via a throwaway Go prototype: `route any` here means an
ALREADY-REGISTERED `*RouteHandle`, NOT REST's raw `Route[Req,Resp]` —
a DELIBERATE divergence from REST's literal mechanism, not an
oversight.** A dedicated review pass caught that REST's real
`Client.Call` (confirmed `adapters/nethttp/clienttransport.go:190`)
takes the RAW, unregistered `Route[Req,Resp]` and calls `.ClientHandle()`
internally, FRESH, on every single call — and `ClientHandle()` "sources
no Builder" (confirmed via `rest.RouteHandle`'s own field doc comment),
meaning **REST's `Client.Call` can NEVER see `GlobalSecurity`, only
per-route `Security`.** reqreply's OWN CURRENT, real `mqtt5.Call`
(`ctx, client, router, handle, req, opts`) takes an ALREADY-REGISTERED
`*RouteHandle` instead — Builder-sourced, WITH `GlobalSecurity`.

The prototype built both conventions side by side against a route with
NO route-level `Security` (relying entirely on `GlobalSecurity`) and
confirmed via a passing/failing assertion: REST's raw-`Route`+fresh-
`ClientHandle()` convention **silently drops GlobalSecurity-gated
credential enforcement** for such a route — `CredentialFunc` is never
even invoked, because `handle.GlobalSecurity` is unreachable from
`ClientHandle()`. Confirmed via `adapters/mqtt5/reqreply.go:516-519`
that reqreply's `Call` ACTIVELY relies on
`secReqs := handle.Security; if secReqs == nil { secReqs =
handle.GlobalSecurity }` to decide whether to invoke `CredentialFunc`
and validate credential format at all — this is load-bearing behavior
today, not a hypothetical.

**SUPERSEDED by a later round's refinement — see immediately below.**
The original resolution here locked `Client.Call`'s `route any` to ONLY
accept an already-registered `*RouteHandle`, rejecting REST's raw-`Route`
convention outright. A follow-up round asked a sharper question: instead
of choosing ONE of the two conventions, could `Client.Call` support
BOTH, giving reqreply users a real choice rather than forcing a
trade-off REST doesn't even offer its own users?

**CONFIRMED via a further throwaway Go prototype: `Client.Call` accepts
EITHER shape, dispatched via a type-switch inside the adapter — strictly
MORE flexible than REST, not merely matching it.**

```go
// adapters/mqtt5 (confirmed dual-mode dispatch inside ClientTransport.Call)
switch v := routeOrHandle.(type) {
case reqreply.Route[Req, Resp]:
    // RAW, unregistered — REST-style: derive ClientHandle() fresh,
    // zero Builder/Server needed. GlobalSecurity intentionally
    // INVISIBLE here, same accepted limitation REST's own
    // Route.ClientHandle() has — this is NOT a reqreply-specific gap.
case *reqreply.RouteHandle[Req, Resp]:
    // Already-registered (via Route.Register(server), see below) —
    // GlobalSecurity IS visible and enforced, same as reqreply's
    // current, real Call behavior today.
}
```

Confirmed via 2 concrete test cases (in addition to the 5 from the
original Decision-1 prototype, all still passing unchanged): (1) a
raw, unregistered `Route` passed to `Client.Call` — round-trips
correctly, and `CredentialFunc` is confirmed NOT invoked (matching
REST's own accepted limitation, not a new one); (2) an already-
registered `*RouteHandle` passed to `Client.Call` — round-trips
correctly, and `CredentialFunc` IS invoked (preserving the original
finding above, unchanged).

**Resolved: `reqreply.Client.Call` accepts BOTH conventions.** A
reqreply CLIENT-ONLY application (no server, no `GlobalSecurity` need)
gets the SAME zero-ceremony, zero-`Server`-needed experience REST
offers via a raw `Route` value — closing what would otherwise have been
a real ergonomic gap relative to REST. An application that DOES need
`GlobalSecurity` registers via `Server` first (see below) and passes the
resulting `*RouteHandle` instead. Neither path is a compromise on the
other — this is a genuine capability REST's OWN `Client.Call` cannot
offer its users at all (REST has no way to see `GlobalSecurity`
client-side, full stop; reqreply now supports EITHER experience,
caller's choice).

**CONFIRMED via a throwaway Go prototype: `Server.Serve(ctx)` MUST
dispatch every registered route's `ServerTransport.Serve` call
CONCURRENTLY (one goroutine per route), NOT sequentially — a critical
gap a critical pre-implementation review pass caught before any code
was written.** REST's `ServerTransport.Serve(ctx context.Context) error`
takes NO per-route argument at all — the adapter (`nethttp`) wires EVERY
registered route into ONE shared `http.ServeMux` before `Serve` is ever
called, then `Serve` is a SINGLE blocking call
(`http.Server.ListenAndServe`). reqreply's `ServerTransport.Serve(ctx,
route, fn) error` is fundamentally different: it is called ONCE PER
ROUTE (confirmed necessary, since `mqtt5`/`zeromq`/`mqtt` have no
built-in equivalent of an HTTP mux to pre-wire many routes into one
call). This split behaves very differently depending on the transport,
confirmed via code:

- `adapters/mqtt5.Serve` (confirmed `adapters/mqtt5/reqreply.go:165-370`)
  is **non-blocking** — it registers a router callback and subscribes,
  then returns `nil` immediately; actual per-message dispatch happens
  later, asynchronously, via the MQTT client library's own background
  connection goroutine.
- `adapters/zeromq.Serve`/`ServeRouter` (confirmed
  `adapters/zeromq/adapter.go:817-849`, `:1200-1249`) **BLOCK forever**
  in their own `for { select { case <-ctx.Done(): return nil; default:
  }; sock.RecvFrames(); ... }` poll loop until `ctx` is cancelled or a
  fatal socket error occurs.

A SEQUENTIAL loop — `for _, r := range routes { t.Serve(ctx, r.route,
r.fn) }`, which is what an earlier round's throwaway prototype for
Decision 1 actually built and confirmed compiling/running (that
prototype tested only ONE registered route, so this never surfaced) —
works fine for `mqtt5` (each call returns fast) but **permanently
starves every route after the first for `zeromq`**: the first route's
`t.Serve` call never returns, so the loop never reaches the second
route's registration at all. A dedicated critical-review pass caught
this BEFORE implementation began, and a second throwaway prototype
confirmed the fix:

```go
// api/reqreply — Server.Serve's confirmed, concurrent implementation
func (s *Server) Serve(ctx context.Context) error {
    // ... for each registered route, launch:
    //   go func(r route) { errCh <- t.Serve(innerCtx, r.route, r.fn) }(r)
    // then select between:
    //   - an error arriving on errCh: cancel innerCtx (stopping every
    //     OTHER still-running route), wait for all to unwind, return
    //     that error.
    //   - the caller's own ctx being Done(): wait for all routes'
    //     t.Serve calls to actually finish (they must respect ctx
    //     internally, same as they do today), then return nil.
}
```

**Confirmed via 3 concrete test-scenario groups** (all passed): (1) an
all-`mqtt5`-style registration (non-blocking transport, 3 routes) —
`Server.Serve` correctly does NOT return early just because every
route's `t.Serve` call finished instantly; it still blocks until the
caller's `ctx` is cancelled, preserving the "blocks until ctx cancelled"
contract REST/events both guarantee; (2) an all-`zeromq`-style
registration (blocking transport, 5 routes) — confirmed via an
active-route high-water-mark counter that **all 5 routes ran
CONCURRENTLY** (the sequential-loop design would have shown a
high-water mark of 1, proving starvation); (3) one route's `t.Serve`
returning a real error — confirmed it propagates PROMPTLY as
`Server.Serve`'s own return value (without waiting for the caller to
cancel `ctx`), and correctly cancels every other still-running route
rather than leaving them dangling.

**A second, related finding confirmed by the same prototype: `zeromq`'s
`Attach` needs a topic→socket MAPPING, not a single shared transport
value, and this is possible because routes are registered BEFORE
`Attach` is called.** Unlike pub/sub's `zeromq.Attach(client, sock)`
(one socket naturally multiplexes many pub/sub topics via ZeroMQ's own
SUB-socket topic filtering), `zeromq.Serve`/`ServeRouter` are each
dedicated to ONE `sock FramedSocket` for ONE Req/Resp pair — there is no
topic-based multiplexing on a single REQ/REP-style socket. Confirmed via
the prototype: because route registrations (`route.WithHandler(fn).
Register(server)` — see below) happen BEFORE `zeromq.Attach(server,
...)` is called (mirroring REST's real
`Route.RegisterHandle(server)`-before-`nethttp.AttachMux(server, mux,
addr)` ordering), a new `Server.RegisteredTopics() []string` method
(backed by a plain, non-generic `Topical` interface every
`RouteHandle[Req,Resp]` satisfies, mirroring Decision 5's `FutureFactory`
pattern) lets `zeromq.Attach(server *reqreply.Server, socketsByTopic
map[string]FramedSocket) (*ServerTransport, error)` validate FULL
topic/socket coverage at Attach time — confirmed via a passing case
(complete coverage) and a failing case (one route's topic has no
matching socket, correctly rejected with a typed `MissingSocketError`,
not silently ignored).

**CONFIRMED via the same prototype round: `reqreply.Server` UNIFIES
what the existing, separate `reqreply.Builder` type does (spec
accumulation: `AddGlobalSecurity`, route bookkeeping) — mirroring
`rest.Server`'s own unification exactly.** A user-prompted re-comparison
against REST's REAL code surfaced a foundational question this doc had
never addressed in 4 prior rounds: REST has NO separate "Builder" type
at all — `rest.Server` (confirmed `api/rest/builder.go:2473`) is ONE
type handling BOTH spec accumulation AND dispatch/transport. reqreply,
by contrast, has a PRE-EXISTING, separate `Builder` type (spec-only,
AsyncAPI accumulation) alongside this doc's proposed NEW `Server` type
(dispatch-focused) — leaving it unclear which type a route registers
against, or whether both need to coexist.

**Resolved: `Server` ABSORBS `Builder`'s role — `AddGlobalSecurity` and
route bookkeeping move onto `Server` directly, and the standalone
`Builder` type is retired.** Confirmed via the prototype: `Route.
Register(s *Server) (*RouteHandle[Req, Resp], error)` reads `s`'s
accumulated `GlobalSecurity` directly (no intermediate `Builder` value
needed), exactly mirroring `rest.Route.Register(b *Server)`'s real
signature. This is a genuine simplification, not just parity for its
own sake — one type to construct, configure, register routes against,
attach a transport to, and serve, matching REST's own single-type
model exactly.

**A further, related refinement — also confirmed via the same
prototype — replaces the earlier round's separate free `reqreply.
Handle(server, handle, fn)` function with a fluent `Route.WithHandler`
method, mirroring REST's REAL, commonly-used idiom exactly (NOT the
method this doc originally compared against).** A closer look at
`examples/rest-api`'s actual usage (not just `RouteHandle.WithHandler`,
the method an earlier round's comparison used) revealed REST's
dominant, real pattern is `Route.WithHandler(fn) Route[Req, Resp]`
(confirmed `api/rest/middleware.go:207`) — a FLUENT, PRE-registration
method attaching `fn` to the UNREGISTERED `Route` value itself, chained
with further options, THEN committed with ONE `.Register(server)` call:

```go
// api/reqreply — CONFIRMED replacement for the prior round's free
// reqreply.Handle(server, handle, fn) function.
func (r Route[Req, Resp]) WithHandler(fn func(context.Context, Req) (Resp, error)) Route[Req, Resp]

// Route.Register reads the attached fn (if any) and records it into
// s's dispatch registry directly — no separate registration call.
func (r Route[Req, Resp]) Register(s *Server) (*RouteHandle[Req, Resp], error)
```

Confirmed via the prototype: `route.WithHandler(fn).Register(server)`
— ONE fluent chain — correctly dispatches `fn` when `server.Serve(ctx)`
runs, with ZERO separate registration step. **This makes reqreply's
server-side declaration workflow IDENTICAL IN SHAPE to REST's real,
common idiom** (`route.WithHandler(fn).HandleMW(...).Register(server)`),
not merely similar — resolving the ergonomic gap a fresh side-by-side
comparison surfaced: the earlier free-function design was reached by
comparing against the WRONG REST method (`RouteHandle.WithHandler`,
REST's less-common POST-registration variant), not REST's actual
dominant idiom. The free `reqreply.Handle` function from the prior round
is SUPERSEDED — `Route.WithHandler`+`Register` replaces it entirely.

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

**`zeromq`'s `Attach` signature is NOT the same shape as `mqtt5`'s —
confirmed via the prototype above, this is a genuine, necessary
divergence, not an inconsistency.** Because `zeromq.Serve`/`ServeRouter`
each need their OWN dedicated socket per route/topic (no topic-based
multiplexing on one REQ/REP-style socket, unlike `mqtt5`'s single
`MQTTClient` naturally handling many topics), `zeromq.Attach` takes a
topic→socket MAPPING instead of a single client value:

```go
// adapters/zeromq (reworked) — DIFFERENT shape from mqtt5.Attach,
// confirmed necessary by Decision 1's socket-per-route finding above.
func Attach(server *reqreply.Server, socketsByTopic map[string]FramedSocket) error
```

Called AFTER every route is registered via `route.WithHandler(fn).
Register(server)` (mirroring REST's real registration-before-Attach
ordering), so `Attach` can validate full topic/socket coverage
immediately, returning a typed `MissingSocketError` for any registered
route with no matching socket, rather than discovering the gap later at
`Serve` time.

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
- **SUPERSEDED by Decision 1's later `Route.WithHandler` finding — how
  the handler `fn` attaches server-side is now `route.WithHandler(fn).
  Register(server)`, NOT a free function.** An earlier round resolved
  this gap (`Server.Serve(ctx)` takes no per-route arguments, so `fn`
  must already be attached to something before `Serve` runs) by
  proposing a free `reqreply.Handle(server, handle, fn)` function —
  reached by comparing against `RouteHandle.WithHandler` (REST's
  less-common, POST-registration variant, confirmed
  `api/rest/builder.go:1182`). A later round's closer look at REST's
  ACTUAL, dominant real-world usage (`examples/rest-api`) found REST's
  common idiom is instead `Route.WithHandler(fn) Route[Req, Resp]`
  (confirmed `api/rest/middleware.go:207`) — a FLUENT, PRE-registration
  method. **Resolved: `reqreply.Route` gains the SAME fluent
  `WithHandler` method, and the free `reqreply.Handle` function is
  RETIRED** — see Decision 1's "Server/Builder unification" finding
  above for the confirmed shape and prototype evidence. This is no
  longer treated as a "stylistic choice with no functional difference"
  — it is the confirmed, REST-matching design.

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

### Decision 5 — an ADDITIVE async `CallAsync`/`Future[Resp]`, alongside `Call` — CONFIRMED via a throwaway Go prototype

**Motivation, grounded in existing code, not invented:**
`adapters/mqtt5/reqreply.go`'s CURRENT `Call` (confirmed
`adapters/mqtt5/reqreply.go:385-618`) already builds a promise
internally and never exposes it — it generates a correlation ID,
registers a reply handler that writes to `replyCh := make(chan
*pahomqtt5.Publish, 1)`, publishes the request, then immediately blocks
on `select { case <-ctx.Done(): ...; case <-timer.C: ...; case replyMsg
:= <-replyCh: ... }`. `replyCh` **is** a future in Go's native form (a
single-slot channel); `Call` just awaits it immediately instead of
handing it back. Unlike REST — whose `Call` is blocking because HTTP
itself is a synchronous, one-connection protocol with no natural async
variant — reqreply's underlying transport (MQTT5/ZeroMQ correlation-based
reply matching) is GENUINELY asynchronous; the blocking `Call` this doc
already designed is a CHOSEN convenience shape over an inherently async
mechanism, not the only possible one. This is a real, common pattern in
async-messaging RPC clients (Akka's `ask`, gRPC async stubs,
hand-rolled `Task<Response>`-correlation-map clients over RabbitMQ/Kafka
all offer both a blocking and a future-based call) — confirmed via grep
that this codebase has NO existing Future/Promise precedent anywhere, so
this is genuinely new territory, not a mirror of prior art.

**Proposed shape, confirmed via a throwaway prototype** (compiled and
run, then deleted):

```go
// package reqreply
type Future[T any] struct{ /* single-slot channel, unexported */ }

// Wait blocks until the future resolves or ctx is cancelled.
func (f *Future[T]) Wait(ctx context.Context) (T, error)

// ClientTransport gains CallAsync alongside Call — any-typed, same
// reflection-recovered shape as Call, for the same structural reason.
type ClientTransport interface {
    Call(ctx context.Context, route any, req any) (any, error)
    CallAsync(ctx context.Context, route any, req any) (any, error) // returns *Future[Resp] as any
}

func (c *Client) CallAsync(ctx context.Context, route any, req any) (any, error)
```

**The sharper, CONFIRMED structural finding — how an adapter recovers a
working `*Future[Resp]` without ever knowing `Resp` concretely:** the
FIRST prototype attempt tried constructing `Future[Resp]` at the
adapter's `CallAsync` dispatch time via `reflect` on `route any` alone —
this does NOT work; Go's `reflect` package cannot instantiate a generic
type for a type argument known only at runtime (the same category of
limitation `protocol-native-features.md`'s own rejected "type-erased
storage" candidate ran into). **The confirmed, working fix:** expose a
PLAIN, non-generic `FutureFactory` interface —

```go
type FutureFactory interface {
    NewFutureAny() (any, func(any, error)) // returns (*Future[Resp] as any, type-erased resolve fn)
}

// RouteHandle[Req,Resp] implements it — Resp is concretely known HERE
// (this method body is compiled once per Req/Resp instantiation), even
// though the interface method's OWN signature is fully any-typed.
func (h RouteHandle[Req, Resp]) NewFutureAny() (any, func(any, error))
```

— confirmed via the prototype that a plain interface type assertion
(`route.(FutureFactory)`) on the adapter's `route any` value recovers a
correctly-typed `*Future[Resp]` and a matching resolve closure with ZERO
reflection, because `RouteHandle[Req,Resp]` (for ANY Req/Resp pair)
automatically satisfies the non-generic interface — the type-erasure
boundary is crossed entirely at Go's own compile-time method dispatch,
not at runtime.

*(Note: this prototype used a VALUE receiver — `func (h
RouteHandle[Req, Resp]) NewFutureAny()` — in a round PRIOR to Decision
1's later-confirmed finding that `Client.Call`/`CallAsync`'s `route`
argument is a POINTER, `*RouteHandle[Req, Resp]`. This is not a
functional conflict — Go's method-set rules mean a value-receiver
method is automatically included in the POINTER's method set too, so
`(*RouteHandle[Req,Resp])`, passed as `any`, still satisfies
`FutureFactory` via the same type assertion. The two prototypes were
never re-run TOGETHER against the current, corrected convention,
though — flagged for a quick smoke-test at implementation time, not a
redesign.)*

**Confirmed via 4 concrete test cases** (all passed): (1) `CallAsync`
returns immediately without blocking, confirmed by doing other work
before awaiting; (2) the returned future is awaited from a DIFFERENT
call site/goroutine than the one that issued `CallAsync` — the actual
"send here, resolve elsewhere" property this decision is about, not
just a renamed blocking call; (3) 5 concurrent `CallAsync` calls resolve
with their OWN correlated replies, zero cross-talk; (4) `Future.Wait`
against an already-expired `context.Context` returns a timeout error,
mirroring `Call`'s existing `CallError{Kind: KindTimeout}` semantics.
`Call` itself is UNCHANGED — `CallAsync` is purely additive, same
discipline as every other confirmed mechanism in this doc/
`protocol-native-features.md`.

**Confirmed: `CallAsync` preserves Decision 2's api-as-abstraction-layer/
adapter-as-thin-IO-mapper split — not just Decision 1/2's `Call`/`Serve`
path.** The prototype makes the boundary concrete, not just asserted:

- **Lives in `api/reqreply` (the abstraction layer):** `Future[T]` itself
  (the single-slot-channel primitive), `Wait`'s timeout/cancellation
  semantics, and — the key piece — `FutureFactory`/`NewFutureAny`'s
  type-erasure crossing (`RouteHandle[Req,Resp]` knows how to build its
  OWN correctly-typed `Future[Resp]` and a matching resolve closure;
  no adapter ever re-implements this). `Client.CallAsync` itself is a
  thin, one-line delegation to `c.transport.CallAsync(...)` — identical
  in shape to `Client.Call`.
- **Stays adapter-specific (the thin IO mapper), by necessity, not
  oversight:** the confirmed prototype's `ClientTransport.CallAsync`
  implementation does exactly two adapter-owned things — (1) call
  `route.(FutureFactory).NewFutureAny()` to obtain the future/resolve
  pair (a one-line call INTO the API layer, not adapter-owned logic),
  and (2) launch whatever async delivery mechanism is NATIVE to that
  transport (subscribe to the MQTT5 reply topic, match the wire-level
  `CorrelationData` when a message arrives, THEN call the provided
  `resolve(decodedResp, err)`), calling `resolve` exactly once when the
  correlated reply arrives. This is the SAME shape `mqtt5.Call`'s
  existing `replyCh`-based wait already has today — `CallAsync` does not
  introduce any NEW adapter-owned responsibility, it only stops the
  adapter from being forced to block on it internally. No encode/decode,
  no correlation-ID GENERATION policy, and no `Future` construction
  logic live in the adapter — only the transport-native "detect the
  matching reply arrived, then call resolve" step does, which has no
  protocol-agnostic equivalent (mirrors Decision 2's own reasoning for
  why `Call`/`Serve`'s adapter-side stays thin but not empty).

**Not designed further here (flagged for implementation time):**
whether `Future[Resp]`'s single-slot-channel implementation needs a
richer API (e.g. a `Done() <-chan struct{}` for `select`-based
composition alongside other channels, mirroring `context.Context`'s own
shape); whether `Server`-side dispatch needs any equivalent concept
(unlikely — a server handler already runs synchronously per request in
this design, with no analogous "fire and check back later" need); and
the exact relationship, if any, to
[Protocol-Native Features](protocol-native-features.md)'s §8 Handler
Disposition — these are LIKELY orthogonal (Disposition is
server-side ack/nack/requeue outcome signaling; `Future`/`CallAsync` is
client-side response awaiting), but that has not been separately
verified and should not be assumed without a dedicated check if the two
mechanisms are ever implemented together.

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

## Relationship to `protocol-native-features.md` (now [Feature](protocol-native-features.md))

Once `reqreply.Server`/`Client`/`Attach` land, MQTT5's Response Topic +
Correlation Data — currently hardwired inside `adapters/mqtt5/reqreply.go`
— becomes expressible as a real, sealed `mqtt5.Capability` (or simply
remains an implicit, always-on capability of `mqtt5`'s `ServerTransport`/
`ClientTransport` implementation, since EVERY mqtt5 reqreply route needs
it — there may be nothing to "declare," since it is not optional the way
Shared Subscriptions are). Shared Subscriptions (`$share/group/topic`)
for reply-topic fan-out across multiple `Server` instances IS a genuine
candidate for a declared, sealed capability on a `reqreply.Route`,
mirroring the pub/sub use case in
[Feature](protocol-native-features.md)
directly. This determination is deferred to implementation time, once
the `Client`/`Server` shape (this doc) actually exists to declare
capabilities against.

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

## Example mini-project — `examples/reqreply-api` (consolidates 3 existing examples)

Mirroring `examples/rest-api`'s own layout (a `routes/` package for pure
route declarations, a `handlers/` package for handler functions +
security, per-adapter server subpackages, and a `client/` subpackage —
each doing ONLY the `route.WithHandler(fn).Register(server)` + `Attach` +
`Serve`/`Call` wiring for its own transport), this doc proposes a NEW,
consolidated `examples/reqreply-api` mini-project once `reqreply.Client`/
`Server`+`Attach` ships, replacing 3 existing, separately-maintained
examples that all demonstrate the SAME underlying request-reply
mechanism today, each calling adapter functions directly (the exact
anti-pattern this doc fixes):

- `examples/adapters-zeromq-reqrep/main.go` — **deleted entirely**
  (fully reqreply-scoped; REQ/REP socket variant).
- `examples/adapters-zeromq-dealer-router/main.go` — **deleted
  entirely** (fully reqreply-scoped; DEALER/ROUTER socket variant).
- `examples/adapters-mqtt5/main.go` — **its `runRequestReplyDemo`/
  `runSecurityDemo` functions are stripped out and rebuilt in the new
  project**; its pub/sub, client-attach, error-channel, and
  connect-security demos are UNRELATED to reqreply and stay in place,
  untouched.

### Proposed layout

```
examples/reqreply-api/
├── routes/
│   └── routes.go        // ComputeRoute, SecuredComputeRoute — reqreply.NewRoute declarations only
├── handlers/
│   └── handlers.go       // domain handler funcs + security (CredentialFunc, SecurityScheme wiring)
├── mqtt5server/
│   └── server.go         // reqreply.Server + route.WithHandler(fn).Register(server) + mqtt5.Attach
├── zeromqserver/
│   └── server.go         // same shape, zeromq.Attach (REQ/REP topic→socket mapping)
├── zeromqrouterserver/
│   └── server.go         // same shape, zeromq DEALER/ROUTER Attach variant
├── client/
│   └── client.go          // reqreply.Client + per-adapter ClientTransport Attach (mqtt5/zeromq)
└── main.go                // wires demos together, mirrors rest-api's demo_*.go convention
```

### Demos (mirroring `rest-api`'s `demo_*.go` convention — one aspect each)

1. **`demo_basic_call_and_serve.go`** — the baseline round trip:
   `route.WithHandler(fn).Register(server)` → `server.Attach(transport)`
   → `server.Serve(ctx)`, alongside `client.Attach(transport)` →
   `client.Call(ctx, route, req)` with a RAW, unregistered `Route` (no
   `Server` needed client-side) — the simplest possible shape.
2. **`demo_global_security_dual_mode_call.go`** — demonstrates
   `Client.Call`'s CONFIRMED dual-mode acceptance side by side: the SAME
   route, relying ONLY on `Server.AddGlobalSecurity` (no per-route
   `Security`), called once via a raw `Route` (confirms `CredentialFunc`
   is NOT invoked — `GlobalSecurity` invisible, same accepted REST
   limitation) and once via an already-registered `*RouteHandle`
   (confirms `CredentialFunc` IS invoked — `GlobalSecurity` enforced) —
   making the caller's choice and its consequence directly visible in
   one demo, not just described in prose.
3. **`demo_route_level_security_credential_error.go`** — a route WITH
   its own `Security` (not relying on `GlobalSecurity`), called with a
   deliberately malformed/missing credential — demonstrates
   `reqreply.SecurityCredentialError` surfacing via `errors.As`, same
   shape as `examples/adapters-mqtt5`'s existing `runSecurityDemo`.
4. **`demo_concurrent_multi_route_dispatch.go`** — registers 3+ routes
   against ONE `Server`, `Attach`es a `zeromq`-backed transport (the
   BLOCKING transport whose starvation risk motivated Decision 1's
   concurrent-dispatch fix), and demonstrates all routes actually
   answering calls concurrently — the real-world demonstration of the
   confirmed fix, not just its prototype's synthetic high-water-mark
   assertion.
5. **`demo_call_async_future.go`** — **the new async call + promise
   demo**, directly demonstrating the "send here, resolve elsewhere"
   mechanism Decision 5 confirms: `client.CallAsync(ctx, route, req)`
   returns a `*reqreply.Future[ComputeResp]` IMMEDIATELY (the demo
   explicitly does other work — e.g. issues a SECOND, independent
   `CallAsync` for a different route — before awaiting the first), then
   awaits BOTH futures from a call site distinct from the one that
   issued them (mirroring the confirmed prototype's actual
   "different call site/goroutine" test, not a same-line
   call-then-immediately-await that would look identical to a blocking
   `Call` renamed). A second scenario in the same demo shows
   `Future.Wait(ctx)` against an already-cancelled `ctx` returning a
   timeout error, matching `Call`'s own `CallError{Kind: KindTimeout}`
   semantics — establishing that `CallAsync`/`Future` is a genuine
   alternative shape over the SAME underlying mechanism `Call` uses, not
   a different transport-level guarantee.
6. **`demo_spec_printing_asyncapi.go`** — replaces the existing
   examples' spec-only `reqreply.NewBuilder`+`.Register(builder)` usage;
   demonstrates printing the AsyncAPI document straight off the SAME
   `Server` value used for real dispatch (no separate throwaway builder
   needed, since `Server` absorbs `Builder`'s role per Decision 1).
7. **`demo_zeromq_dealer_router_variant.go`** — the DEALER/ROUTER
   socket-topology variant carried over from
   `examples/adapters-zeromq-dealer-router`, rebuilt against the new
   `Server`/`Client`+`Attach` shape, demonstrating `zeromq.Attach`'s
   topic→socket coverage validation (Decision 2) with a DELIBERATE
   missing-socket case included, surfacing the typed `MissingSocketError`
   at `Attach` time rather than a later, harder-to-diagnose failure.

### Not yet decided

- Whether `mqtt5server`/`zeromqserver`/`zeromqrouterserver` truly need
  separate subpackages (mirroring `rest-api`'s `chiserver`/
  `nethttpserver` split) or whether, since each demo is single-route and
  much smaller than `rest-api`'s multi-route surface, a flatter
  `main.go`-only layout reads better — a styling call for whoever
  implements this, not a functional requirement either way.
- This mini-project cannot be scaffolded with real, compiling code until
  `reqreply.Client`/`Server`+`Attach` actually ships (this section is a
  DESIGN proposal for the example layout, not yet-runnable code) — it is
  recorded here now so the migration/example work is planned alongside
  the core implementation, not bolted on afterward as an unplanned
  extra step.

## Remaining open items (deferred to implementation time)

- ~~Exact reflection-based `Call`/`Serve` signatures for `reqreply.Client`/
  `Server`~~ **RESOLVED via a throwaway Go prototype** (compiled and run,
  not merely sketched — deleted after this finding was extracted).
  Built a standalone `reqreply.Client`/`Server`+`Attach` pair, a thin
  `mqtt5`-style stub adapter, and confirmed via 5 concrete test cases:
  (1) `Server.Serve` returns `NoServerTransportAttachedError` before
  `Attach`; (2) `Client.Call` returns `NoClientTransportAttachedError`
  before `Attach`; (3) a full round trip — `Client.Call(ctx, route, req)`
  → adapter's `ClientTransport.Call` → **real `reflect.Value.Call`**
  dispatch to the registered handler (mirroring
  `adapters/nethttp/clienttransport.go`'s actual established reflection
  idiom, not a simplified stand-in) → decoded response recovered via
  `respVal.Interface().(Resp)` — works end-to-end; (4)
  `TransportTypeMismatchError` fires correctly when a non-`RouteHandle`
  value is passed as `route`; (5) a second `Attach` call correctly
  returns `ClientTransportAlreadyAttachedError`. **Confirmed: this doc's
  proposed shape needs NO adjustment** — `reqreply.Client.Call`/
  `Server.Serve`'s `any`-typed signatures and `NoServerTransportAttachedError`/
  `NoClientTransportAttachedError`/`ClientTransportAlreadyAttachedError`/
  `TransportTypeMismatchError` error taxonomy are LOCKED as sketched
  above, verified identical in shape to `rest.Client`/`Server`'s real,
  shipped equivalents (`api/rest/builder.go:2592-2789`).
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
  [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
  own migration rounds (migrate every real example, not just tests,
  before deleting the old adapter-level entry points). The EXAMPLE side
  of this migration is now planned — see "Example mini-project —
  `examples/reqreply-api`" above: `examples/adapters-zeromq-reqrep` and
  `examples/adapters-zeromq-dealer-router` are deleted entirely,
  `examples/adapters-mqtt5`'s request-reply demos are stripped out and
  rebuilt, all consolidated into ONE new `examples/reqreply-api`
  mini-project mirroring `examples/rest-api`'s layout.
- Whether Response Topic + Correlation Data should be a DECLARED
  `Feature` at all, or remain an implicit, always-on capability
  of `mqtt5`'s reqreply transport (see "Relationship to
  `protocol-native-features.md`" above) — not decided, deferred until
  the `Client`/`Server` shape exists to prototype against.

No implementation has started. A future session should pick the lowest-
risk starting point first — likely `mqtt5` (closest existing analogue,
clearest Fn-shape translation, and the transport with the concrete
Response Topic/Correlation Data payoff) — before `mqtt`(v3)/`zeromq`.

## Test plan (once implementation begins)

Mirroring [D-0003](../design/d-0003-codec-declared-middlewares.md)'s
and [Protocol-Native Features](protocol-native-features.md)'s own Test
plan sections:

- `Client`/`Server`+`Attach`+reflection dispatch (Decision 1/2, already
  confirmed via prototype) — `NoServerTransportAttachedError`/
  `NoClientTransportAttachedError` before `Attach`; a full round trip via
  real `reflect.Value.Call` dispatch; `TransportTypeMismatchError` on a
  malformed `route`/`handle` value; `ClientTransportAlreadyAttachedError`
  on double-`Attach`.
- **`Client.Call`'s DUAL-MODE route-vs-handle acceptance (Decision 1,
  CONFIRMED via prototype — supersedes an earlier round's single-mode
  lock)**: a RAW, unregistered `Route` passed to `Call` round-trips
  correctly and confirms `CredentialFunc` is NOT invoked (matching
  REST's own accepted `GlobalSecurity`-invisible limitation, not a
  reqreply-specific gap); an already-registered `*RouteHandle` passed to
  `Call` round-trips correctly and confirms `CredentialFunc` IS invoked
  (preserving `GlobalSecurity` enforcement) — both cases exercised
  against the SAME route, confirming the caller's choice of value shape
  determines the behavior, not a global toggle.
- `route.WithHandler(fn).Register(server)` — ONE fluent chain (Decision
  1's "Server/Builder unification" finding, CONFIRMED via prototype,
  supersedes the retired `reqreply.Handle` free function) — confirms
  `fn` is dispatched correctly when `Server.Serve(ctx)` runs with NO
  separate registration call and NO per-route `Serve` arguments.
- `Server.AddGlobalSecurity` + `Route.Register(server)` (the confirmed
  `Server`/`Builder` unification) — confirms `GlobalSecurity` set
  directly on `Server` is visible to a registered route's
  `*RouteHandle.GlobalSecurity` with NO intermediate `Builder` value
  needed.
- **`Server.Serve`'s CONCURRENT per-route dispatch (Decision 1's
  "concurrent dispatch" finding, CONFIRMED via prototype — the critical
  case this Test plan must not skip)**: an all-non-blocking-transport
  registration (3+ routes, mqtt5-style — each `t.Serve` call returns
  instantly) confirms `Server.Serve` still blocks until the caller's
  `ctx` is cancelled, NOT returning early; an all-blocking-transport
  registration (5+ routes, zeromq-style — each `t.Serve` call blocks
  internally) confirms ALL routes run CONCURRENTLY, via an
  active-route high-water-mark assertion (a regression to the
  sequential-loop design would show a high-water mark of 1, proving
  starvation — this is the actual test that catches Finding B if it
  regresses); one route's `t.Serve` returning a real error confirms it
  propagates PROMPTLY as `Server.Serve`'s own return value (without
  waiting for the caller's `ctx` to be cancelled) and cancels every
  other still-running route.
- `zeromq.Attach`'s topic→socket coverage validation (Decision 2's
  divergent `Attach` shape, CONFIRMED via prototype) — routes registered
  BEFORE `Attach` with FULL topic/socket coverage succeeds; a route with
  NO matching socket is rejected with a typed `MissingSocketError` at
  Attach time, not discovered later at `Serve` time.
- `CallAsync`/`Future[Resp]` (Decision 5, already confirmed via
  prototype) — non-blocking return; await from a different call
  site/goroutine; concurrent calls with zero correlation cross-talk;
  timeout/cancellation parity with `Call`.
- `FutureFactory`/`NewFutureAny`'s type-erasure crossing (Decision 5) —
  confirms a plain interface type assertion recovers a correctly-typed
  `*Future[Resp]` with zero `reflect` use.
- Security/credential folding (Decision 3) — `.Use()`/`.HandleMW()`/
  `.ClientMW()` registration mirroring `rest.Route`'s own existing
  tests; `handle.Implementations`/`ClientImplementations` populated by
  `Route.Register`/`Route.ClientHandle`, read automatically by
  `Server.Serve`/`Client.Call` with NO per-call `Options.SecurityFunc`/
  `CredentialFunc` field remaining.
- `mqtt`(v3)'s absence from the reqreply transport interfaces (Decision
  4) — confirms `mqtt` package does NOT implement
  `ServerTransport`/`ClientTransport` (a compile-time absence, not a
  runtime check) unless/until a documented subset is designed.
- `ports.PluginReqReplyPattern` — confirms it continues to populate
  `Implementations`/`ClientImplementations` for free via its existing
  `.Register(builder)` delegation, with zero `ports`-specific changes.
- Escape hatches (carried forward) — a regression test confirming
  `adapters/zeromq`'s reqreply transport, once Decision 3 lands, DOES
  read and enforce `Implementations`/`ClientImplementations` (closing
  today's confirmed "never even read" gap) — this is the ONE existing
  escape-hatch bullet this rework is expected to actually fix, not just
  document.
