# Design Documents

This section preserves the full design rationale behind go-codex's foundational,
cross-cutting architectural patterns — decisions that shaped how MULTIPLE apis/ports
work, not a single feature. Unlike [`docs/roadmap/`](../roadmap/index.md), everything
here IS implemented and shipped; unlike [`docs/concepts/`](../concepts/codec.md), these
are not usage guides — they keep the ORIGINAL reasoning, rejected alternatives, and the
review history that led to the shipped design, for future maintainers extending the same
pattern to a new boundary.

A document graduates here from `docs/roadmap/` — and only from there — when it meets
either bar:

- It is **fully shipped** AND establishes a pattern **multiple** apis/ports/packages are
  expected to follow (not a single-adapter feature); or
- The design **fundamentally changes how an existing api/port/package works**.

This is a deliberately high bar, reserved for bigger architecture designs and reworks
worth keeping in full. Most shipped roadmap docs still just follow `docs/roadmap/`'s own
existing lifecycle (removed once shipped, or kept in place if a follow-on phase remains
open) — see `.github/skills/plan-a-new-codex-feature/SKILL.md` for the exact policy.

### Numbering convention

Every document here is filed as `d-NNNN-<slug>.md` — a sequential, zero-padded number
assigned in the CHRONOLOGICAL order design docs were WRITTEN (not necessarily the order
they shipped), mirroring an ADR-style numbering scheme. `d-0001` is REST's own
middleware/workflow simplification — the FIRST of these two designs, and the one the
second one adopted and adapted concepts from. `d-0002` is pub/sub's — written second,
after REST's `d-0001` had already landed, deliberately reusing and adapting as many of
its concepts as pub/sub's own structural differences allow (role model, security
merge/coverage, `Client.Attach`), with two-way sync back onto `d-0001` itself whenever a
gap surfaced in ONE that the other had already solved (see either doc's own addenda for
the concrete back-and-forth). When promoting a NEW roadmap doc here, assign it the next
sequential number — do not renumber existing documents.

---

## Documents

| Document | Package | Summary |
|---|---|---|
| [D-0001 — REST Middleware Workflow Simplification](d-0001-rest-middleware-workflow-simplification.md) | `middleware`, `api/rest`, `adapters/nethttp`, `adapters/chi` | The declare/implement middleware split (`HandleMW`/`ClientMW`) plus whole-API declarative wiring (`Route.Register(builder)` + `Serve`/`ServeOne`/`ServeSSE` as the sole server-side entry points, `Call`/`CallWithHandle` as the sole client-side ones) — fully shipped, including removal of the older per-route `Handler`/`Register`/`SSEHandler`/`RegisterSSE` functions. Establishes the pattern the `ports.Pattern` binding layer (`RESTPattern`) reuses unchanged, and the one `d-0002` (pub/sub) adopted and adapted next. |
| [D-0002 — Pub/Sub Workflow Simplification](d-0002-pubsub-workflow-simplification.md) | `api/events`, `ports`, `adapters/mqtt5`, `adapters/mqtt`, `adapters/zeromq` | Pub/sub's client-centric role model — no fixed server/client pairing, since a broker is the intermediary and both publisher and subscriber are CLIENTS of a channel — resolved via `events.Client` + role-scoped `Subscriber[T]`/`Publisher[T]` builders (`WithSubscribe`/`WithPublish`, `.Use`/`.SubscribeMW`/`.PublishMW`, `.Handle(client)`/`.Register(client)`), unconditional security-coverage enforcement (`CheckCoverage`/`checkImplementationsDeclared`), and a reflection-based `Client.Attach`/`.Publish`/`.Subscribe`/`.ServeSubscribers` convenience layer unified with `d-0001`'s own `Server.Attach`/`rest.Client`/`nethttp.Attach` design. Format resolution (JSON/YAML/TOML/Gob/custom binary) is centralized on `ChannelHandle`/`RouteHandle` themselves (`EncodeWithFormats`/`DecodeMergedWithFormats`), so every adapter — escape-hatch primitive AND `Client.Attach` shim alike — is a thin caller of one canonical method. Fully shipped across all three pub/sub adapters plus REST/`nethttp`/`chi`, with every old, call-time-competing public primitive removed (confirmed exceptions kept for genuine advanced needs). Establishes the pattern `api/reqreply`'s own rework (see [D-0004](d-0004-reqreply-workflow-simplification.md), now shipped) follows. |
| [D-0004 — ReqReply Workflow Simplification](d-0004-reqreply-workflow-simplification.md) | `api/reqreply`, `ports`, `adapters/mqtt5`, `adapters/zeromq` | Extends `d-0001`/`d-0002`'s `Server`/`Client`+`Attach` pattern to request-reply's third API boundary: `reqreply.Server` UNIFIES what a separate, now-deprecated `Builder` type did (AsyncAPI 3.0 spec accumulation, `AddGlobalSecurity`) with dispatch/transport — ONE type, mirroring `rest.Server`. Server-side declaration is ONE fluent chain, `route.WithHandler(fn).Register(server)`, matching `rest.Route`'s dominant idiom byte-for-byte. `Client.Call` accepts EITHER a raw, unregistered `Route` (REST-style, `GlobalSecurity` invisible — same accepted limitation as `rest.Route.ClientHandle()`) OR an already-registered `*RouteHandle` (`GlobalSecurity` enforced) — dispatched via a type-switch, a real choice REST itself cannot offer. An ADDITIVE, reqreply-only async `Client.CallAsync`/`Future[Resp]` exists alongside the blocking `Call`, needed because reqreply's underlying transport (MQTT5/ZeroMQ correlation-based reply matching) is genuinely asynchronous, unlike REST's/pub-sub's dispatch — confirmed via a `FutureFactory` interface letting an adapter recover a correctly-typed future via a plain interface type assertion, zero `reflect`-based generic instantiation needed. `adapters/mqtt5`'s `AttachServer`/`AttachClient` and `adapters/zeromq`'s `AttachServer`/`AttachClient`/`AttachRouterServer`/`AttachDealerClient` (covering REQ/REP AND ROUTER/DEALER, the latter pair with a new `MissingSocketError` validating full topic/socket coverage upfront at Attach time) implement the reflection-shim idiom `adapters/mqtt5`'s own `events.Transport` implementation already established. `Builder`/`NewBuilder`/`BuilderOption` stay DELIBERATELY KEPT as deprecated aliases (zero-cost, no duplicate logic). **mqtt5's AND zeromq's lower-level `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer` — REOPENED, decision REVERSED, then RESHIPPED via delegation, KEPT unchanged**: originally kept as documented escape hatches by analogy to REST's `ServeOne`/`CallWithHandle`; that analogy was found flawed during [ReqReply Middleware](../roadmap/reqreply-middleware.md)'s review (these functions shared no code with `AttachServer`/`AttachClient`/`AttachRouterServer`/`AttachDealerClient`, a genuine duplicate dispatch implementation) — but rather than deleting them (the initial plan), Phase 0b found `reqreply.ServerTransport.Serve`/`ClientTransport.Call` are already single-route/single-call scoped, so every one of these functions' BODIES were rewritten to delegate directly to the matching `serverTransport`/`clientTransport`/`routerServerTransport`/`dealerClientTransport` — zero duplicate logic, EXACT SAME signatures, no breaking change, no caller migration, for BOTH adapters. Fully shipped across `api/reqreply` core types, `adapters/mqtt5`, `adapters/zeromq`, and the `examples/reqreply-api` mini-project (7 demos spanning mqtt5 AND zeromq REQ/REP + ROUTER/DEALER, replacing 3 deleted/rebuilt examples). |
| [D-0003 — Codec-Declared Middlewares](d-0003-codec-declared-middlewares.md) | `middleware`, `api/rest`, `api/events`, `adapters/nethttp`, `adapters/chi`, `adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq` | A codec-backed `middleware.Declaration[In,Out]` core (Input/Output shape, like a route/channel's own Req/Resp) + per-pattern derived types `rest.Middleware[In,Out]`/`events.Middleware[In,Out]`, additive via a `middleware.RouteMiddleware` marker interface — zero breaking changes to `middleware.Middleware`/`SecurityScheme`/existing `.Use(...)` call sites. Two attachment styles, one shared vocabulary across `Route`, `SSERoute`, and `Subscriber`/`Publisher`: `Transform`/`ClientTransform` (route/channel-BOUND, `fn` gets `req`/`msg` access plus a codec-validated `In`) and plain `.Use(mw)` (route/channel-AGNOSTIC, via `WithReceive`/`WithSend`-bundled Fns, reused verbatim across many routes/channels). Establishes the guiding principle that middleware is fundamentally a spec-layering mechanism (a declare-time value that can layer into the final spec and/or carry runtime dispatch behavior; "non-spec-adding" middleware is simply the case where the spec column is empty) — governing REST/events middleware today and a confirmed-feasible future `ports.Middleware[In,Out]` extension. Resolved 7 design decisions (D1-D7): pre-handler-only timing (D1); `fn` errors are `ErrorPattern`/`ErrorChannel`-eligible, falling back to a new `MiddlewareError` (D2); 3-tier client precedence, explicit > middleware-derived > route/channel-derived (D3); a mandatory spec-layering conflict-check guard covering multiple `Middleware[In,Out]` contributions (D4); `stats.ReportErrors` integration at `"middleware:in"`/`"middleware:fn"` (D5); `Declaration.Name` uniqueness enforcement, a new `DuplicateMiddlewareNameError` (D6); rejection of combining both attachment styles on one value, a new `AmbiguousMiddlewareAttachmentError` (D7). Fully shipped across REST (`Route` + `SSERoute`, server AND client dispatch) and events (subscribe AND publish, all three pub/sub adapters). Supersedes `docs/roadmap/common-middleware-architecture.md`; complementary to (not competing with) `docs/roadmap/protocol-native-features.md`. |

---

## How to read these documents

Each document here was originally a `docs/roadmap/` design doc, refined through one or
more critical review passes before and during implementation. Expect:

- **Motivation** — the problem that drove the pattern, and why a narrower fix wasn't enough
- **Rejected alternatives** — approaches considered and why they were set aside
- **API surface** — the actual shipped type signatures, as-built
- **Known limitations and open risks** — a running punch list, resolved item by item
  during implementation (kept, not deleted, so the reasoning survives)
- **Coverage** — how the pattern extends (or is planned to extend) across every
  api/port boundary it applies to, not just the first one it shipped for
