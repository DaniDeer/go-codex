# D-0004 — ReqReply Workflow Simplification — design decisions

> **Status:** Implemented — architectural foundation. All 5 phases (0-4:
> `api/reqreply` core types, `adapters/mqtt5` migration, the
> `examples/reqreply-api` mini-project, `adapters/zeromq` migration) are
> shipped and verified; Phase 5 (doc sync + doc promotion) is this
> promotion itself — `Builder`/`NewBuilder`/`BuilderOption` are kept as
> DEPRECATED aliases (zero-cost, no behavioral duplication, matching
> `d-0002`'s own precedent for aliases). **`mqtt5`/`zeromq`'s lower-level
> `Serve`/`Call`/`ServeRouter`/`CallDealer` — RESOLVED (see "Addendum:
> `reqreply-middleware.md` and `zeromq-security.md`" below for the final
> outcome).** Phase 5 originally kept these permanently as documented
> escape hatches (mirroring REST's `ServeOne`/`CallWithHandle`); that
> analogy was found FLAWED during a later review (`reqreply-middleware.md`,
> now folded into the Addendum below), reopening the question of whether
> to retire them entirely. The FINAL resolution: NOT deleted — they kept
> their exact existing signatures (zero breaking change), with their
> bodies rewritten to construct a `serverTransport`/`clientTransport`
> (etc.) directly and delegate to it, eliminating the duplicate dispatch
> logic that motivated reopening the question in the first place, with
> zero caller/example migration required.
> `docs/guides/mqtt5.md`/`docs/guides/zeromq.md` currently lead with
> the `Server`/`Client`+`Attach` workflow and demote the lower-level
> functions to an explicit "escape hatch" section (still accurate today,
> pending the retirement work above), and
> `.github/instructions/go-codex.instructions.md`'s `api/reqreply`/
> `adapters/mqtt5`/`adapters/zeromq` rows were brought current (they had
> fallen behind Phases 0/1/3, still describing the pre-`Server`/`Client`
> API — a real doc-sync gap this promotion also closed). Establishes the
> pattern `d-0001` (REST) and `d-0002` (pub/sub) already established —
> `Server`/`Client` + `Attach`, `route.WithHandler(fn).Register(server)` —
> extended to request-reply's genuinely asynchronous transport via the
> additive `CallAsync`/`Future[Resp]` mechanism.
>
> Below is the original phase-by-phase implementation record (`api/reqreply`
> core types: `Server`/`Client`/`ServerTransport`/`ClientTransport`/`Future`/
> `FutureFactory`, `Route.WithHandler`+`Register(*Server)`,
> `RegisteredTopics`/`Topical`, `ServerEntry` rename to resolve the
> `Server` naming collision between the asyncapi entry type and the new
> dispatch-owning type — `Builder`/`NewBuilder`/`BuilderOption` kept as
> DEPRECATED aliases for `Server`/`NewServer`/`ServerOption`, zero
> breaking changes to existing callers, all pre-existing tests/examples
> pass unchanged; `adapters/mqtt5`'s `AttachServer`/`AttachClient` AND
> `adapters/zeromq`'s `AttachServer`/`AttachClient`/`AttachRouterServer`/
> `AttachDealerClient` reflection-shim transports (covering REQ/REP AND
> ROUTER/DEALER), plus a repo-wide mechanical migration of `ports` + all
> example/test call sites off the deprecated `Builder` API to eliminate
> the `staticcheck` SA1019 regression it caused; the consolidated
> `examples/reqreply-api` mini-project — `routes/`, `handlers/`,
> `mqtt5server/`, `zeromqserver/`, `zeromqrouterserver/`, `client/`
> packages plus 7 demo files and `main.go` — replacing the deleted
> `examples/adapters-zeromq-reqrep`/`examples/adapters-zeromq-dealer-
> router` and `examples/adapters-mqtt5`'s stripped-out request-reply
> demos).
> Verified: `go build ./...`, `go test ./...` (incl. `-race`),
> `staticcheck`, `gosec`, `gofmt` all clean; new unit test
> suite (error taxonomy, dual-mode Call, Server/Builder unification,
> WithHandler/Register fluent dispatch, concurrent Serve w/ both
> blocking- and non-blocking-transport fakes, CallAsync/Future round
> trip, FutureFactory type-erasure) plus a new package-level `Example()`;
> mqtt5's 3 and zeromq's 7 new reflection-shim tests all pass under
> `-race`; every one of `examples/reqreply-api`'s 7 demos runs correctly
> via `go run ./examples/reqreply-api` (multiple repeat runs, no flakes,
> after fixing one genuine mqtt5-Serve-registration startup race with a
> 50ms sync sleep — mirrors `examples/adapters-mqtt5`'s own established
> convention); `for d in examples/*/; do go run ./$d; done` re-verified
> with zero failures across all examples, including the retired-and-
> rebuilt `examples/adapters-mqtt5`. **Phase 5 (full doc sync + doc
> promotion) is ALSO now shipped** — see this doc's top status header
> above and its own Phase 5 entry in the phased implementation plan
> below for the full record. Core mechanism CONFIRMED via 5
> rounds of throwaway Go prototypes prior to this phase. Final confirmed
> shape, in brief (see
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
> (mirroring `examples/rest-api`'s layout) was planned here and has
> SINCE SHIPPED — see "Example mini-project" below — replacing 3 existing examples
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
> (see [Feature/Provider](../roadmap/protocol-native-features.md)'s
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
[Feature/Provider](../roadmap/protocol-native-features.md) — then titled
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
[Protocol-Native Features](../roadmap/protocol-native-features.md)'s §8 Handler
Disposition — these are LIKELY orthogonal (Disposition is
server-side ack/nack/requeue outcome signaling; `Future`/`CallAsync` is
client-side response awaiting), but that has not been separately
verified and should not be assumed without a dedicated check if the two
mechanisms are ever implemented together.

## Confirmed adapter capability matrix (carried forward, mostly unchanged)

> **Final update**: all rows below are now SHIPPED/CLOSED for every
> adapter — corrected here, superseding the "STALE"/"not yet
> implemented" notes an earlier pass left in this table. See "Addendum:
> `reqreply-middleware.md` and `zeromq-security.md`" below for the full
> record (both source roadmap docs have since been deleted).

| Capability | `mqtt` (v3) | `mqtt5` | `zeromq` |
|---|---|---|---|
| Connection-level `SecuredClient`/`ConnectSecurityScheme` | ✅ | ✅ | ❌ permanently, by design (CURVE/ZAP is the caller's own concern — see the Addendum below's "Connection-level authentication — CLOSED" summary) |
| Message-level subscribe-side `SecurityFunc` | ✅ | ✅ | ✅ (in-payload, via `SubscribeOptions.SecurityFunc`) |
| Message-level publish-side `CredentialFunc` | ❌ (protocol limit — no per-message property channel) | ✅ | ✅ (in-payload, via `PublishOptions.CredentialFunc`) |
| Native Response Topic + Correlation Data (reqreply viability) | ❌ (protocol limit) | ✅ | n/a (own correlation mechanism) |
| Scope-grant / `middleware.CheckScopes` integration (reqreply) | n/a (no reqreply support) | ✅ SHIPPED (Phase 1) | n/a — zeromq reqreply's paired Fn shape uses a plain-`error`-returning shape instead (deliberately not REST's scope-grant model), SHIPPED across all 4 transports |
| Handle-attached implementation (vs. per-call `Options`) (reqreply) | n/a (no reqreply support) | ✅ SHIPPED (`RouteHandle.Implementations`/`ClientImplementations`, Phase 1) | ✅ SHIPPED (same fields, all 4 transports) |

The bottom two rows are what Decision 3 (above) closes — SHIPPED for
BOTH `mqtt5` and `zeromq` (see the Addendum below for the full,
adapter-specific detail).

## Relationship to `protocol-native-features.md` (now [Feature](../roadmap/protocol-native-features.md))

MQTT5's Response Topic + Correlation Data — hardwired inside
`adapters/mqtt5/reqreply.go`/`reqreply_transport.go` — was originally
considered as a candidate for a real, sealed `mqtt5.Capability`.
**DECIDED (now that `reqreply.Server`/`Client`/`Attach` have shipped and
this could actually be evaluated against a real `Attach` shape): it
stays an implicit, always-on characteristic of `mqtt5`'s
`ServerTransport`/`ClientTransport` implementation, NOT a declared
`Capability`.** EVERY mqtt5 reqreply route needs it, unconditionally —
there is nothing to "declare," since it is not optional the way Shared
Subscriptions are; a `Capability`'s whole point is compile-time-safe
OPT-IN gating for something not every binding needs, which doesn't apply
here. Shared Subscriptions (`$share/group/topic`) for reply-topic
fan-out across multiple `Server` instances remain the genuine candidate
for a declared, sealed capability on a `reqreply.Route` from this same
feature cluster — mirroring the pub/sub use case in
[Feature](../roadmap/protocol-native-features.md) directly — tracked
independently THERE, not blocked by anything in this doc.

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

## Phased implementation plan

Once the design was confirmed ready (5+ prototype rounds, all Decisions
1-5 locked), implementation is sequenced into 6 phases — each phase ends
with its own build/test/lint pass, not deferred to the end, per this
project's standard "verify before claiming success" discipline.

- **Phase 0 — `api/reqreply` core types (isolated, no adapter changes).
  SHIPPED.** `Server`/`Client`/`ServerTransport`/`ClientTransport`/
  `Future`/`FutureFactory`, `Route.WithHandler`+`Register(*Server)`,
  `Server.RegisteredTopics`/`Topical`. `Builder`/`NewBuilder`/
  `BuilderOption` kept as DEPRECATED aliases for `Server`/`NewServer`/
  `ServerOption` — zero breaking changes, every pre-existing test/example
  passes unchanged. **A naming collision was found and fixed during this
  phase**: the pre-existing `reqreply.Server` type alias (for
  `asyncapi.Server`, an AsyncAPI server ENTRY like `{URL: ...,
  Protocol: "mqtt5"}`) collided with the NEW dispatch-owning `Server`
  type this phase introduces — resolved by renaming the entry alias to
  `ServerEntry` (mirroring `rest.ServerEntry`/`rest.Server`'s own,
  already-established naming split for the identical concept; `api/events`
  never hit this because `events.Client` handles both roles without a
  separate "Server" dispatch type). All 9 real call sites of the old
  `reqreply.Server{...}` struct literal (7 in `api/reqreply`'s own
  doc-comments/tests, 2 in examples) were migrated to `reqreply.
  ServerEntry{...}`. New unit tests cover: the full error taxonomy
  (`NoServerTransportAttachedError`/`NoClientTransportAttachedError`/
  `ServerTransportAlreadyAttachedError`/`ClientTransportAlreadyAttachedError`/
  `TransportTypeMismatchError`); dual-mode `Client.Call` (raw `Route` vs.
  registered `*RouteHandle`, confirming `GlobalSecurity` visibility
  differs correctly); `Server`/`Builder` unification (`AddGlobalSecurity`
  visible on a registered handle with NO intermediate `Builder`);
  `WithHandler`+`Register` fluent dispatch; `Server.Serve`'s concurrent
  dispatch against BOTH a non-blocking-transport fake (mqtt5-style —
  confirms `Serve` still blocks until `ctx` is cancelled) and a
  blocking-transport fake (zeromq-style — confirms an active-route
  high-water mark equal to the route count, the actual regression test
  for Finding B); prompt error propagation + cancellation of other
  routes; `RegisteredTopics`; `CallAsync`/`Future` round trip (including
  "do other independent work, await from a different call site" and a
  cancelled-context timeout case); `FutureFactory`'s type-erasure
  crossing. All tests pass under `-race` (3 repeated runs). `staticcheck`/
  `gosec`/`gofmt` all clean. A new package-level `Example()` demonstrates
  the full `WithHandler`+`Register`+`Attach`+`Serve` / `Attach`+`Call`
  workflow against a minimal in-process transport stub.
- **Phase 1 — `adapters/mqtt5` migration. SHIPPED.** New
  `adapters/mqtt5/reqreply_transport.go`: `AttachServer(*reqreply.Server,
  MQTTClient, MQTTRouter, ...ServeOptions)` / `AttachClient(*reqreply.
  Client, MQTTClient, MQTTRouter, ...CallOptions)` implement
  `reqreply.ServerTransport`/`ClientTransport` via the SAME
  reflection-shim idiom already established by `adapters/mqtt5/
  transport.go`'s `events.Transport` implementation (and
  `adapters/nethttp/clienttransport.go`'s `Call`) — not a new pattern.
  Named `AttachServer`/`AttachClient` rather than `Attach` because
  `mqtt5.Attach` (for `events.Client`/pub-sub) already exists and Go has
  no function overloading. `CallAsync` recovers a `*Future[Resp]` via
  `FutureFactory.NewFutureAny()`'s plain-interface type assertion
  (crossing the generic/reflection erasure boundary without needing
  `reflect` to instantiate a generic type). Documented v1-scope
  limitation, mirroring the existing `events.Transport` shim's own
  documented limitation: per-call `RequestFormats`/`Formats` overrides,
  `NewTopicParam` merge-field topic-var merging, and `ErrorPattern`
  typed replies are NOT honored by this shim (routes needing them use
  `Serve`/`Call` directly, unchanged). 3 new tests (`TestAttachServer_
  AttachClient_RoundTrip`, `TestAttachClient_DualMode_GlobalSecurity`,
  `TestAttachClient_CallAsync_RoundTrip`) using a `wireBrokers` helper
  simulating a shared broker across independent mock client/router
  pairs; all pass under `-race` (2 repeated runs). Additionally,
  migrated `ports` package's real `reqreply.Builder` dependency
  (`PortOptions.ReqReplyBuilder`, `RegisterReqReply`,
  `PluginReqReplyPattern` internals — confirmed via repo-wide grep as a
  REAL dependency, not previously called out in this doc's migration
  bullet before the phased-plan round) off the now-deprecated
  `Builder`/`NewBuilder`/`BuilderOption` onto `Server`/`NewServer`/
  `ServerOption`: a repo-wide mechanical rename across `ports/*.go` (7
  files), the 3 example projects (`adapters-mqtt5`,
  `adapters-zeromq-dealer-router`, `adapters-zeromq-reqrep`),
  `adapters/zeromq/adapter_test.go`, 3 `adapters/mqtt5/*_test.go`
  files, and `api/reqreply/route_test.go`, plus manual updates to the 6
  remaining godoc-example-only mentions (`api/events/builder.go`,
  `api/llm/builder.go`, `api/reqreply/{builder,doc,route}.go`) that
  still showed `NewBuilder(...)` as the "primary" usage pattern in prose
  — done to eliminate the `staticcheck` SA1019 deprecation-warning
  regression this phase's `Builder` deprecation annotation caused
  against 19 previously-untouched call sites (`just check` must stay
  clean; no `//nolint`/suppression was added). Verified: `go build
  ./...`, `go vet ./...`, `gofmt -l .` (clean), `go test ./...` (all
  packages pass, zero FAIL), `staticcheck` (zero findings across every
  touched package), `gosec` (zero NEW findings — the 5 pre-existing
  `G304` findings in `ports/file.go` are unrelated), and all 3 migrated
  example projects re-run via `go run` (all exit 0).
- **Phase 2 — `examples/reqreply-api`, mqtt5 slice. SHIPPED** (built
  together with Phase 4 in one continuous effort — see Phase 4 below for
  the full mqtt5+zeromq mini-project writeup and verification evidence).
- **Phase 3 — `adapters/zeromq` migration. SHIPPED.** New
  `adapters/zeromq/reqreply_transport.go`: 4 Attach functions covering
  BOTH zeromq socket-pattern families — `AttachServer`/`AttachClient`
  (REQ/REP, mirrors [Serve]/[Call]) and `AttachRouterServer`/
  `AttachDealerClient` (ROUTER/DEALER, mirrors [ServeRouter]/
  [CallDealer], including the identity-frame envelope: dispatches each
  request in its OWN goroutine, matching `ServeRouter`'s own concurrency)
  — all 4 implement `reqreply.ServerTransport`/`ClientTransport` via the
  SAME reflection-shim idiom as `adapters/mqtt5`'s (duplicated, not
  shared — the two adapter packages don't import each other). A NEW
  `MissingSocketError` (with `errors.As`/`LogValue`) is returned by
  `AttachServer`/`AttachRouterServer` at Attach time when a route
  registered on the `*reqreply.Server` has no corresponding entry in the
  `topic → socket` map they take (ZMQ REQ/REP and ROUTER/DEALER sockets
  are point-to-point, unlike MQTT5's single shared client — this is the
  confirmed reason `Server.RegisteredTopics`/`Topical` were speced
  ahead of time in Phase 0 specifically for this check), and by the
  client-side transports at CALL time for a route with no socket entry
  (no upfront client-side check — the client may only need a subset of
  the server's routes). `CallAsync` on both client transports reuses
  the SAME `FutureFactory`/`NewFutureAny` mechanism `adapters/mqtt5`'s
  does, unchanged. Same documented v1-scope limitation as `adapters/
  mqtt5`'s shim: route-declared `RequestFormats`/`Formats` overrides and
  `ErrorPattern`-typed error replies are NOT honored — callers needing
  those use `Serve`/`Call`/`ServeRouter`/`CallDealer` directly,
  unaffected. 7 new tests (round trip + `MissingSocketError` coverage
  for both socket-pattern families, plus a `CallAsync`/`Future` round
  trip) using a channel-based in-memory `FramedSocket` pair
  (`chanSocket` for REQ/REP; a `dealerSocket`/`routerSocket` pair that
  correctly models ZMQ's automatic identity-frame prepend/strip
  behavior for ROUTER/DEALER) — no real ZMQ library needed. All pass,
  including under `-race` (2 repeated runs). Verified: `go build ./...`,
  `gofmt -l .` (clean), full `go test ./...` (zero FAIL), `staticcheck`/
  `gosec` (zero findings on the new file).
- **Phase 4 — `examples/reqreply-api`, zeromq slice + delete old
  examples. SHIPPED.** Built the full `examples/reqreply-api` mini-project
  per the "Example mini-project" section below, covering BOTH mqtt5
  (Phase 2) and zeromq (this phase) in one continuous effort: `routes/`
  (`ComputeRoute`/`DoubleRoute`/`TripleRoute`, `SecuredComputeRoute` with
  its OWN `RouteMeta.Security`, `GlobalOnlyComputeRoute` — declares
  `WithSecurityScheme` but leaves `RouteMeta.Security` nil so it inherits
  purely from `Server.AddGlobalSecurity`, confirming these two
  declarations are genuinely orthogonal — `RouterComputeRoute`,
  `MissingSocketRoute`), `handlers/` (`Add`/`Double`/`Triple`),
  `mqtt5server/` (mock broker/router, `AddGlobalSecurity(route.
  Require("bearerAuth"))`, exposes `GlobalHandle`/`SecuredHandle` for
  callers dispatching directly against an already-registered
  `*RouteHandle`), `zeromqserver/` (3 REQ/REP `chanSocket` pairs, one per
  route — REQ/REP's point-to-point limitation confirmed in practice, not
  just in the design doc), `zeromqrouterserver/` (`Build()`'s working
  ROUTER/DEALER config plus `BuildWithMissingSocket()`'s deliberately
  incomplete config triggering `zeromq.MissingSocketError` upfront,
  before `Serve` ever runs), and `client/` (`BuildMQTT5`/`BuildZeroMQ`/
  `BuildZeroMQDealer`). 7 demo files + `main.go` wire everything together
  in narrative order: (1) basic call via a raw, unregistered `Route`; (2)
  dual-mode `Client.Call` contrasting `GlobalSecurity`'s invisibility on
  a raw `Route` call against a directly-dispatched, already-registered
  `*RouteHandle` via `mqtt5adapter.Call` with a `CredentialFunc` — the
  server-side rejection in the raw-Route case surfaces as a generic
  wrapped error over the wire (NOT a re-hydrated typed
  `SecurityCredentialError` — that only happens for a CLIENT-side
  pre-check rejection, confirmed by running the demo, not just by
  reading the code); (3) route-level security via `SecuredComputeRoute`,
  demonstrating a CLIENT-side `SecurityCredentialError` for a malformed
  credential (caught before publish); (4) concurrent multi-route
  dispatch against zeromq's 3-route REQ/REP server, the real-world
  demonstration of `Server.Serve`'s concurrent-dispatch fix a blocking
  transport motivated (Decision 1); (5) `CallAsync`/`Future` — two
  independent async calls issued back-to-back before either is awaited,
  both awaited from a different call site, plus a `Future.Wait` against
  an already-cancelled `ctx`; (6) `Server.AsyncAPISpec()` + `MarshalYAML`
  printing, derived entirely from the route declarations already made;
  (7) the zeromq ROUTER/DEALER variant plus `BuildWithMissingSocket`'s
  `MissingSocketError`. Deleted `examples/adapters-zeromq-reqrep` and
  `examples/adapters-zeromq-dealer-router` entirely; stripped
  `examples/adapters-mqtt5/main.go`'s `runRequestReplyDemo`/
  `runSecurityDemo` functions and their now-unused supporting
  declarations (`ComputeReq`/`ComputeResp`/`ComputeRoute`/
  `SecuredComputeRoute`/`bearerAuth`/`printSpecs`), renumbering the
  remaining Connect-level-security demo from "Demo 4b" to "Demo 3" and
  updating the package doc comment to point at `examples/reqreply-api`
  for request-reply — its pub/sub, client-attach, error-channel, and
  connect-security demos are otherwise UNCHANGED. Fixed one genuine
  startup race discovered while verifying: `examples/reqreply-api`'s
  `main.go` started `mqtt5Built.Server.Serve(ctx)` in a goroutine and
  immediately issued the first `Client.Call` — since mqtt5's
  `ServerTransport.Serve` registers its router handler synchronously
  but only once ITS OWN dispatch goroutine (started by `Server.Serve`)
  actually runs, this raced and intermittently hung (a lost publish, no
  reply, no `ctx` deadline to time it out); fixed with the same
  50ms-`time.Sleep` synchronization convention already established in
  `examples/adapters-mqtt5`'s own demos — verified stable across
  multiple repeated `go run` invocations after the fix. Also updated
  `docs/reference/project-structure.md` (added `reqreply-api/` layout
  entry, updated `adapters-mqtt5`'s description, removed the deleted
  zeromq example entries) and `docs/guides/{zeromq,mqtt5}.md` (updated
  "See also" links and added pointers to `examples/reqreply-api`).
  Verified: `go build ./...`, `gofmt -l .`, `go vet ./...` all clean;
  `go test ./...` zero FAIL; `just check` (staticcheck + gosec) zero
  findings; all 7 `examples/reqreply-api` demos plus the retired-and-
  rebuilt `examples/adapters-mqtt5` demo re-verified via repeated
  `go run` invocations with zero flakes; `for d in examples/*/; do go
  run ./$d; done` re-run across every example with zero failures.
- **Phase 5 — full doc sync + doc promotion to `docs/design/`. SHIPPED.**
  ~~Decided AGAINST retiring `Builder`/`mqtt5.Serve`/`.Call`/`zeromq.Serve`/
  `.Call`/`.ServeRouter` (see "Remaining open items" above for the full
  reasoning — kept as documented escape hatches/deprecated aliases,
  a genuine ongoing need, not a stale leftover).~~ **REOPENED, REVERSED
  (see "Remaining open items" below)** — `Builder`/`NewBuilder`/
  `BuilderOption` stay kept, unaffected (zero-cost aliases, no
  duplicate logic); `mqtt5.Serve`/`.Call` and `zeromq.Serve`/`.Call`/
  `.CallHandle`/`.ServeRouter`/`.CallDealer` are now TARGETED for
  retirement instead — the "mirrors REST's `ServeOne`/`CallWithHandle`"
  justification below was found flawed: unlike REST's zero-duplication
  thin wrappers around `Serve`/`Call` itself, these functions share NO
  code with `AttachServer`/`AttachClient` — a genuine duplicate
  dispatch implementation, not a thin convenience wrapper, conflicting
  with the "adapter stays a thin IO wrapper, user only touches api/*"
  principle. Retirement is tracked and sequenced in
  `docs/roadmap/reqreply-middleware.md` (new Phase 0/0b), not here.
  Full doc sync (still accurate as of THIS phase's shipping, pending
  the reopened item above):
  rewrote `docs/guides/mqtt5.md`'s "Request-Reply" section and
  `docs/guides/zeromq.md`'s "REQ/REP"/"DEALER/ROUTER" sections to lead
  with the `Server`/`Client`+`Attach` workflow (concrete, runnable code
  samples, not just a pointer), demoting `Serve`/`Call`/`ServeRouter`/
  `CallDealer` to an explicit "escape hatch" subsection each — also
  discovered and fixed `docs/guides/zeromq.md`'s "REQ/REP" section had
  gone FAR further stale than expected: it still documented a
  non-existent `api/zeromq` package (`zmqapi.NewBuilder`/`Register`/
  `ContractMeta`) using `rest.NewRoute` for a ZMQ route, predating
  `api/reqreply`'s very existence (confirmed via `ls api/` — no
  `api/zeromq` directory exists in the repo at all) — not merely behind
  Phases 0-4, but describing an API surface that had ALREADY been
  renamed away before this doc's own Decision 1 was ever written.
  Brought `.github/instructions/go-codex.instructions.md`'s
  `api/reqreply`/`adapters/mqtt5`/`adapters/zeromq` rows current too — a
  real gap: they had NOT been updated during Phases 0/1/3 despite this
  skill's "every code change" mandatory-update rule, still describing
  `Route.Register(b *Builder)` and a stale `Server = asyncapi.Server`
  alias (renamed to `ServerEntry` in Phase 0 — the instructions row
  never caught up); added `Server`/`Client`/`Attach`/`CallAsync`/
  `Future`/the 5 adapter `Attach*` functions/`MissingSocketError`
  coverage to all 3 rows. Promoted this doc from `docs/roadmap/` to
  `docs/design/d-0004-reqreply-workflow-simplification.md` (removed from
  `docs/roadmap/index.md` + `zensical.toml`'s roadmap nav, added to
  `docs/design/index.md` + `zensical.toml`'s `[nav."Design Documents"]`)
  — qualifies under the "establishes a pattern multiple packages follow"
  bar: extends `d-0001`/`d-0002`'s `Server`/`Client`+`Attach` pattern to
  a third API boundary, confirming it generalizes to a genuinely
  asynchronous transport via the additive `CallAsync`/`Future[Resp]`
  mechanism neither REST nor pub/sub needed. Verified: `go build ./...`,
  `gofmt -l .` clean (doc-only changes, no Go code touched).

`mqtt`(v3) is explicitly OUT of this phased plan — Decision 4 confirms
its publish-side credential limitation is permanent, and whether it
implements ANY documented subset transport remains a separately-tracked
open question (see "Remaining open items" below), not attempted in any
phase here.

## Example mini-project — `examples/reqreply-api` (consolidates 3 existing examples)

> **SHIPPED** (Phases 2 and 4) — this section's layout/demo proposal below
> is the AS-BUILT shape (file names, package names, and demo numbering all
> match exactly what's now in `examples/reqreply-api`); see the phased
> implementation plan's Phase 4 entry above for the verification evidence.

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
- ~~`mqtt`(v3) and `zeromq`'s per-adapter Fn shapes for `HandleMW`/
  `ClientMW` (Decision 3) remain unresolved — `zeromq` in particular may
  need a NEW wire-level credential convention (an additional frame)
  before any Fn shape can be finalized~~ **RESOLVED** (updated
  after `docs/roadmap/reqreply-middleware.md`/`docs/roadmap/
  zeromq-security.md` were written, later in this same session — both
  Fn-shape DECISIONS are made; only their IMPLEMENTATION remains, tracked
  independently in those two roadmap docs, not a still-open DECISION of
  this doc's own):
  `mqtt`(v3) is N/A — permanently out of scope for reqreply entirely
  (confirmed zero `reqreply` code exists anywhere in `adapters/mqtt`;
  see Decision 4 below and the resolved bullet immediately below this
  one). `zeromq`'s Fn-shape premise — that a NEW wire-level credential
  convention would be needed — turned out to be FALSE once actually
  investigated: `docs/roadmap/zeromq-security.md`'s own "Implication for
  `reqreply-middleware.md`" section (added later, cross-checking this
  exact bullet against that doc's own already-resolved pub/sub finding)
  confirmed zeromq's REQ/REP frames have the EXACT SAME "no separate
  credential slot" shape pub/sub's `[topic, payload]` frames do, so the
  SAME in-payload `*Req` mechanism (no new wire-level frame) applies —
  Fn shape DECIDED there (design-only, not yet implemented). mqtt5's own
  `HandleMW`/`ClientMW` Fn shapes are DECIDED in
  `docs/roadmap/reqreply-middleware.md`'s own "API surface" section
  (also design-only, not yet implemented) — both are cross-referenced
  design decisions living in their own roadmap docs now, not still-open
  questions in this doc.
- ~~Whether `mqtt`(v3) implements a documented subset of the reqreply
  transport (application-level reply-topic convention) or does not
  implement it at all (Decision 4) — not decided~~ **RESOLVED**: `mqtt`(v3)
  implements NO reqreply transport subset AT ALL — confirmed via code
  (zero `reqreply` references anywhere in `adapters/mqtt`) during the
  session work that produced `docs/roadmap/reqreply-middleware.md`'s own
  status banner ("MQTT3 stays pub/sub-only, permanently excluded from
  reqreply entirely... nothing left to do for MQTT3"). Not a partial/
  documented-subset situation — a clean, total exclusion.
- ~~Migration path for existing callers of `adapters/mqtt5.Serve`/`.Call`
  and `adapters/zeromq.Serve`/`.Call`/`.ServeRouter` (breaking change)~~
  ~~**RESOLVED, decided AGAINST removal.** Unlike `d-0002`'s pub/sub
  precedent (which DID delete its old call-time-competing primitives),
  `Serve`/`Call`/`ServeRouter`/`CallDealer` are DELIBERATELY KEPT as
  documented escape hatches, not retired — confirmed a genuine, ongoing
  need during Phase 4: `examples/reqreply-api`'s own Demo 2/3 (dual-mode
  `Client.Call`, route-level security) call `mqtt5adapter.Call` directly
  against an already-registered `*RouteHandle` with a `CredentialFunc`,
  a capability the `Attach`-based `Client.Call`/`CallAsync` v1 reflection
  shim does not (yet) expose (same documented v1-scope limitation as
  `events.Transport`'s own shim: no per-call `RequestFormats`/`Formats`
  overrides, `NewTopicParam` merge-field topic-var merging, or
  `ErrorPattern`-typed error replies).~~ **REOPENED, DECISION REVERSED**
  (found during `docs/roadmap/reqreply-middleware.md`'s final review
  pass): the "mirrors REST's `ServeOne`/`CallWithHandle`" analogy above
  does not hold up against the real code. REST's `ServeOne`/
  `CallWithHandle` are confirmed (via `docs/design/
  d-0001-rest-middleware-workflow-simplification.md`) to be LITERALLY
  "build a scratch single-route `Server`, call `Serve`/`Call`" — the
  identical dispatch path, zero duplicate logic, a true thin wrapper.
  `adapters/mqtt5/reqreply.go`'s `Serve`/`Call` and `adapters/zeromq/
  adapter.go`'s `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer`
  are confirmed (via code — no calls between them and `AttachServer`/
  `AttachClient` in either direction, in either adapter package) to be a
  fully SEPARATE, duplicate protocol-dispatch implementation — not a
  thin wrapper at all. Keeping a second, independently-maintained
  dispatch path "because REST does" was the flawed premise; it
  conflicts with the "adapter stays a thin IO wrapper around
  IO/protocol implementation, the user only touches `api/*`" principle.
  **Decision (initial): RETIRE these functions**, mirroring `d-0002`'s
  own precedent of fully deleting its old call-time-competing
  primitives — sequenced as new Phase 0 (close the ONE remaining real
  capability gap — merge-fields, per-call format overrides,
  `ErrorPattern` — the gap that was this bullet's own original
  justification) and Phase 0b (build genuine thin wrappers, migrate
  callers, delete the old functions) in `docs/roadmap/
  reqreply-middleware.md`.
  **UPDATE — SHIPPED, outcome BETTER than this initial plan.** Phase 0
  shipped as planned. Phase 0b, while implementing the planned
  new-named thin wrappers, found `reqreply.ServerTransport.Serve`/
  `ClientTransport.Call` are ALREADY single-route/single-call scoped
  (confirmed via the interface definitions — `Server.Serve`'s own
  multi-route CONCURRENCY happens ABOVE this, not inside
  `ServerTransport.Serve`) — meaning `serverTransport`/`clientTransport`
  (the concrete types `AttachServer`/`AttachClient` build) were ALREADY
  single-route/single-call dispatch primitives, no `Server`/`Client`
  scratch-registration wrapper needed. So `Serve`/`Call`/`CallHandle`
  were rewritten to DELEGATE to `serverTransport`/`clientTransport`
  directly — zero duplicate logic achieved WITHOUT deletion, WITHOUT a
  breaking change, and WITHOUT any caller/example migration (superseding
  the "retire"/"delete + rebuild + migrate" framing above entirely — the
  functions are KEPT, unchanged signatures, now genuinely thin). A real,
  additional gap (missing `stats.TraceObserver` span support in the
  reflection-based dispatch) was found and closed along the way, via
  re-running the full pre-existing test suite. `Builder`/`NewBuilder`/
  `BuilderOption` were never affected either way (zero-cost aliases, no
  duplicate logic). Full detail lives in `docs/roadmap/
  reqreply-middleware.md`'s "Phase 0b" section, not duplicated here.
  zeromq's OWN `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer`
  received the SAME delegation fix in a follow-up pass, same session —
  ALSO SHIPPED (see `docs/roadmap/reqreply-middleware.md`'s "Phase 0b"
  section for the full detail, including a zeromq-specific regression
  found and fixed: `BuildTopic` failure during observability-path
  derivation must be FATAL, not silently ignored). `docs/guides/mqtt5.md`/
  `docs/guides/zeromq.md` need NO update (the functions still exist,
  unchanged from the caller's perspective) — the EXAMPLE side of the
  EARLIER consolidation (distinct from this item) remains accurate: see
  "Example mini-project — `examples/reqreply-api`" above —
  `examples/adapters-zeromq-reqrep` and
  `examples/adapters-zeromq-dealer-router` are deleted entirely,
  `examples/adapters-mqtt5`'s request-reply demos are stripped out and
  rebuilt, all consolidated into ONE new `examples/reqreply-api`
  mini-project mirroring `examples/rest-api`'s layout.
- ~~Whether Response Topic + Correlation Data should be a DECLARED
  `Feature` at all, or remain an implicit, always-on capability
  of `mqtt5`'s reqreply transport (see "Relationship to
  `protocol-native-features.md`" above) — not decided, deferred until
  the `Client`/`Server` shape exists to prototype against~~ **RESOLVED —
  DECIDED: stays IMPLICIT, NOT a declared `Capability`.** A `Capability`
  (per `docs/roadmap/protocol-native-features.md`'s own definition)
  exists to give a route/binding a compile-time-safe way to OPT INTO
  optional, protocol-specific behavior — sealed opt-in gating is the
  entire point of the mechanism. Response Topic + Correlation Data is
  NOT optional for `mqtt5` reqreply: EVERY route registered against
  `mqtt5.AttachServer`/`AttachClient` needs it, unconditionally — it IS
  the wire mechanism by which reqreply-over-mqtt5 works at all (there is
  no way to route a reply back to the right caller without it). There is
  no opt-out scenario to gate: a route either uses mqtt5 reqreply (and
  gets Response Topic/Correlation Data automatically, with no choice
  involved) or doesn't use mqtt5 reqreply at all. Declaring something
  that is unconditionally present, with no real alternative, would be
  pure ceremony — it fails the actual test that motivates `Capability`
  in the first place, mirroring how REST doesn't make callers "declare"
  that HTTP requests carry a `Content-Length` header. Shared
  Subscriptions (`$share/group/topic`) remain the genuine `Capability`
  candidate from this same MQTT5 feature cluster — a route CAN work with
  or without shared-subscription reply fan-out, and getting it wrong
  changes CORRECTNESS (competing vs. duplicating consumers), unlike
  Response Topic/Correlation Data's all-or-nothing nature — tracked
  independently in `docs/roadmap/protocol-native-features.md`, not
  blocked by anything in this doc.

~~No implementation has started. A future session should pick the
lowest-risk starting point first — likely `mqtt5`... before
`mqtt`(v3)/`zeromq`~~ **STALE, superseded by shipped work**: this
sentence predates Phases 0-5, all of which have since shipped and been
verified (`mqtt5` first, per this exact recommendation, then `zeromq`) —
see this doc's own top status banner and Phase-by-phase implementation
record above for the actual, completed history. Kept here only to show
the original sequencing recommendation was followed, not as a live
instruction.

**Update: one of the 6 items above was REOPENED and reversed** (the
"migration path for `Serve`/`Call`/`ServeRouter`/`CallDealer`" bullet,
above) — found flawed during `docs/roadmap/reqreply-middleware.md`'s
final review pass (the "mirrors REST's `ServeOne`/`CallWithHandle`"
premise doesn't hold against the real code: REST's versions are
zero-duplication thin wrappers, reqreply's old functions are a fully
separate duplicate dispatch path). Its resolution — RETIRE these
functions — is now tracked and sequenced entirely in
`docs/roadmap/reqreply-middleware.md`'s new Phase 0/0b, not here. This
is the ONLY item reopened; the other items remain closed as below.

**This doc (d-0004) otherwise has ZERO remaining open DECISIONS of its
own.** **Final update: ALL tracked implementation work below has SINCE
SHIPPED, for BOTH adapters.** `reqreply-middleware.md`'s Phase 0/0b
(escape-hatch delegation, BOTH mqtt5 AND zeromq) and Phase 1/1b (mqtt5's
`HandleMW`/`ClientMW` Fn shapes, the declare/implement split, plus the
User-Property param-as-middleware sub-phase) shipped first; **zeromq's
OWN `.Use`/`HandleMW`/`ClientMW` Fn-shape work (`zeromq-security.md`)
has SINCE ALSO SHIPPED** — the declare/implement split is now a
multi-adapter (mqtt5 AND zeromq) pattern. Both source roadmap docs have
since been deleted, per their own graduation policy; see "Addendum:
`reqreply-middleware.md` and `zeromq-security.md`" below for the full,
consolidated, durable record — promoted into this doc now that the
multi-adapter bar is met. Shared Subscriptions as a `Capability`
candidate (`docs/roadmap/protocol-native-features.md`) remains
separately tracked, unrelated to the above. None of this represents an
unresolved question WITHIN d-0004's own scope (the
`Server`/`Client`/`Attach` rework, which is fully shipped and verified).

## Test plan (executed during implementation — see the Phased
implementation plan above for the actual verification evidence per phase)

Mirroring [D-0003](../design/d-0003-codec-declared-middlewares.md)'s
and [Protocol-Native Features](../roadmap/protocol-native-features.md)'s own Test
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

## Addendum: `reqreply-middleware.md` and `zeromq-security.md` — the declare/implement split, for BOTH mqtt5 AND zeromq

Added after `docs/roadmap/reqreply-middleware.md` (mqtt5's declare/
implement split, Phases 0/0b/1/1b) and `docs/roadmap/zeromq-security.md`
(zeromq's own Fn-shape work, closing the last item the first doc
deferred) both shipped — recorded here so the lineage is discoverable
from this document, since the newer features' own roadmap docs did not
themselves narrate where their core mechanism came from. Both roadmap
docs have SINCE BEEN DELETED (per their own 3-way delete/keep/promote
graduation policy — single-feature roadmap docs, fully shipped, with no
lasting cross-cutting design value of their own beyond what is captured
here); this addendum is the durable record of the design lineage that
remains after that deletion.

### Phase 0 / 0b — escape-hatch delegation (mqtt5 AND zeromq)

- `RouteHandle` gained capability-parity methods mirroring
  `rest`/`events`: `DecodeWithFormats`, `DecodeMergedWithFormats`,
  `EncodeRequestWithFormats`, `EncodeWithFormats`,
  `DecodeResponseWithFormats`, `EffectiveRequestFormats`,
  `EffectiveFormats`, `EncodeVars`. `reqreply.ClientCallOptions` (mirrors
  `rest.ClientCallOptions`) added as a variadic trailing parameter to
  `Client.Call`/`CallAsync`/`ClientTransport.Call`/`CallAsync`.
- **Final resolution for `Serve`/`Call`/`CallHandle`/`ServeRouter`/
  `CallDealer`** (see the corrected status-banner note above for the
  short version): these functions KEPT their exact existing signatures
  (zero breaking change) — their bodies were rewritten to construct a
  `serverTransport`/`clientTransport` (etc.) directly and delegate to
  its `.Serve`/`.Call` method, achieving zero duplicate dispatch logic
  with zero caller/example migration. `CallHandle` is now a pure alias
  for `Call`. Applied identically to both mqtt5 and zeromq (all 4
  zeromq transport functions: `Serve`/`Call`/`ServeRouter`/`CallDealer`).
  A real, additional gap (missing `stats.TraceObserver` span support in
  the reflection-based dispatch) was found and closed along the way.

### Phase 1 / 1b — declare/implement split (mqtt5-only when shipped, now superseded by zeromq parity below)

- `Route[Req,Resp].Use(mws ...middleware.RouteMiddleware)`/`HandleMW(mw
  *middleware.Middleware, fn any)`/`ClientMW(mw *middleware.Middleware,
  fn any)` — `RouteHandle` gains `Implementations
  []middleware.ServerImplementation`/`ClientImplementations
  []middleware.ClientImplementation`.
- mqtt5 Fn shapes: server-side PAIRED (security-verifying, REST's
  scope-grant model: `func(ctx, msg *pahomqtt5.Publish, reqs)
  (map[string][]string, error)`), server-side UNPAIRED (general
  decorator wrapping the raw `*pahomqtt5.Publish`), client-side PAIRED
  (credential-supplying, returns `[]mqtt5.UserProperty`), client-side
  UNPAIRED (general decorator wrapping the full `func(ctx, Req) (Resp,
  error)` dispatch — no raw pre-decode form exists to wrap instead).
- **Breaking change** (mirrors REST's own D-0001 precedent): mqtt5's OLD
  `ServeOptions.SecurityFunc`/`CallOptions.CredentialFunc` fields were
  REMOVED entirely once `.Use`/`HandleMW`/`ClientMW`'s
  `Implementations`/`ClientImplementations` became the sole credential
  mechanism — one declarative mechanism only, no permanent parallel
  imperative escape hatch (this REVERSES an earlier decision in this
  same doc that had followed events pub/sub's "keep both permanently"
  precedent instead).
- `reqreply.WithSecurityScheme`/`SecurityScheme` kept as deprecated
  aliases; new package-local error types
  `MissingSecurityMiddlewareError{Route, Scheme}`/
  `UnknownMiddlewareImplementationError{Route, Scheme}` (mirror
  `rest`/`events`' own equivalents).
- `CheckCoverage`-equivalent runs at adapter-Serve-time inside
  `mqtt5.AttachServer`'s `ServerTransport.Serve` (mirrors
  `rest.CheckCoverage`'s own Serve-time-only-knowable-coverage
  precedent), not inside `api/reqreply` itself.
- **Phase 1b** (request AND reply direction, mqtt5-only): `FromUserPropertyParam`/
  `FromResponseUserPropertyParam` (`adapters/mqtt5`) attach User-Property
  params as spec-contributing `middleware.Middleware` values;
  `render/asyncapi/v3.Message` gained a `Headers schema.Schema` field;
  `RouteHandle` gained `RequestHeaderParams`/`ResponseHeaderParams`.
  Reused `mqtt5.UserPropertyError`/`MissingUserPropertyError` unchanged —
  no new error types needed.

### zeromq's own Fn-shape work (`zeromq-security.md`) — CORRECTS a stale claim in the paragraphs above

The "Remaining open items" narrative above (written mid-session, before
`zeromq-security.md` shipped) still calls zeromq's `CheckCoverage`
wiring and Fn-shape support "idea only, no driver yet" and "not yet
implemented" — **this is now stale and superseded.** Confirmed via real
code: `adapters/zeromq/reqreply_transport.go` calls
`reqreply.CheckCoverage` at both `serverTransport.Serve` and
`routerServerTransport.Serve` construction time (mirroring mqtt5's Phase
1 exactly), and all 4 zeromq reqreply transports
(`serverTransport`/`clientTransport`/`routerServerTransport`/
`dealerClientTransport`) now dispatch `.Use()`/`HandleMW`/`ClientMW`
paired-security and general-purpose Fn shapes — SHIPPED, not deferred.

- **Paired Fn shapes differ from mqtt5's by necessity**: zeromq has no
  raw-message-equivalent type to operate on (REQ/REP `[payload]` frames
  decode immediately) — both server-side paired
  (`func(ctx, req *Req, reqs) error`, verifying) and client-side paired
  (`func(ctx, req *Req, reqs) error`, credential-writing) operate on the
  DECODED `*Req` directly, using a NEW-to-this-package reflection
  mechanic: build a fresh addressable `reflect.New(reqType)`, copy the
  decoded value in, call the Fn against its pointer, read back whatever
  it mutated. Deliberately NOT REST's scope-grant model — each transport
  adapter mirrors its own established precedent, an intentional
  per-adapter difference, not an inconsistency.
- **General-purpose (unpaired) Fn shape**: one shape for both server AND
  client (`func(next func(ctx, req Req) (Resp, error)) func(ctx, req
  Req) (Resp, error)`) — zeromq has no raw pre-decode form on either
  side, unlike mqtt5's server-side raw-`*Publish`-wrapping decorator.
- **Implementation surface**: sized ×2 relative to mqtt5's Phase 1 (4
  transports — REQ/REP pair PLUS ROUTER/DEALER pair — vs. mqtt5's 2),
  duplicated rather than shared, consistent with how Phase 0's
  capability-parity work was also duplicated ×2 across the same 4
  transports.
- **Reused error types, no new ones needed**: `reqreply.SecurityError`/
  `SecurityCredentialError` (already transport-agnostic) wrap paired-Fn
  rejections identically to mqtt5's Phase 1; surfaced via the existing
  `ServeError`/`CallError{Kind: KindSecurity}` wrapping (`KindSecurity`
  already existed, no new `ErrorKind` needed).
- **A real bug found and fixed during this work, in BOTH adapters**:
  `clientTransport.call`/`dealerClientTransport.call`'s
  `reflect.MakeFunc`-built `innerCall` closures ignored `args[0]` (the
  `ctx` actually passed by a general-purpose `ClientMW` decorator),
  silently discarding any context mutation a decorator made before
  dispatch. The SAME bug existed in mqtt5's own earlier Phase 1
  `innerCall` (inherited when this work mirrored mqtt5's technique) —
  fixed in both adapters together, verified via a dedicated regression
  test per adapter.
- `examples/reqreply-api`'s Demo 9 (`demo_cross_api_oauth2_sharing.go`)
  demonstrates the SAME `middleware.SecurityScheme` declaration shared
  across a zeromq reqreply route AND a locally-declared REST route,
  proving byte-identical scheme sharing across `api/rest` and
  `api/reqreply`.
- Full verification (`gofmt`/`go build`/`go vet`/`go test -race`/`just
  check`/all examples) — all clean; all 12 planned unit tests pass.

### RESOLVED: `adapters/zeromq` pub/sub's flat `SecurityFunc`/`CredentialFunc` — RETIRED

`adapters/zeromq`'s PUB/SUB side (`SubscribeOptions`/`PublishOptions`)
already had its OWN declarative `.Use`/`SubscribeMW`/`PublishMW`
mechanism (per the D-0003 addendum in `docs/design/
d-0003-codec-declared-middlewares.md`), but its OLD flat
`SecurityFunc`/`CredentialFunc` fields permanently coexisted alongside
it — previously flagged here as a genuinely open question with no
decision recorded. **Now resolved: RETIRED**, mirroring mqtt5 reqreply's
own REST-precedent removal above — `SubscribeOptions[T].SecurityFunc`/
`PublishOptions[T].CredentialFunc` were removed entirely (BREAKING) from
ALL 3 pub/sub adapters (`adapters/zeromq`, `adapters/mqtt5`,
`adapters/mqtt` v3, not just zeromq) — see
[D-0002](d-0002-pubsub-workflow-simplification.md)'s own "Addendum:
Observability Core Consolidation, `SecurityFunc` Retirement, and
`examples/events-api`" for the full evidence/decision record.
