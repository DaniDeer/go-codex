# Pub/Sub Workflow Simplification — design decisions

> **Decision 7 status: IMPLEMENTED and verified** (`go build`/`go vet`/
> `go test`/`just check`/`just examples` all green) — inverts the
> handle-based primitives Decision 6 confirmed as legitimate exceptions
> into `api/events` itself: new GENERIC `PublishTransport[T]`/
> `SubscribeTransport[T]` interfaces (mirroring `ports.SourceAdapter[T]`'s
> proven convention — NOT the `any`-typed reflection shape
> `events.Transport` uses, since these are FREE FUNCTIONS, which CAN have
> type parameters, unlike `Client`'s own methods) + `events.PublishHandle`/
> `SubscribeHandle` call-time functions requiring NO `*events.Client`/spec
> at all; `events.NewClient`'s `Info` became optional via a new
> `events.WithInfo` option; `adapters/mqtt5`/`adapters/mqtt` gained new
> connection-owning `Connect(ctx, brokerURL, opts)` entry points
> (`adapters/zeromq` explicitly deferred — confirmed CGO-dependency
> conflict with its own "no CGO in the adapter" design goal). Shipped
> across `adapters/mqtt5`/`adapters/mqtt`/`adapters/zeromq`, all 4
> mqtt-family examples migrated, and docs synced. See Decision 7 below for
> the full design and rationale.
>
> **Decision 6 status: IMPLEMENTED and verified** (`go build`/`go vet`/
> `go test`/`just check`/`just examples` all green) — a direct follow-up
> user request ("I want a consistent workflow across the rest and event
> api ... the single only [workflow] the framework provides without
> escape hatches") removed every OLD, lower-level, call-time-competing
> public primitive (`Caller`/`NewCaller`,
> `Subscribe[T]`/`SubscribeWithHandle[T]`, `Publish[T]`/`PublishHandle[T]`,
> `NewPublisherFor[T]`, `ServeOneSubscriber[T]` on the pub/sub side;
> `Caller`/`NewCaller`, `Call[Req,Resp]`/`CallWithHandle[Req,Resp]`,
> `Serve`/`ServeOne`/`ServeSSE` on the REST side) from each adapter's
> PUBLIC API, WITH CONFIRMED EXCEPTIONS kept public for genuine advanced
> needs (`nethttp.CallWithHandle`/`ServeOne`; each pub/sub adapter's
> `SubscribeWithHandle`/`SubscribeHandler`/`Publish`/`PublishHandle`) —
> see Decision 6 below for the full removal list, scope boundary
> (pub/sub and REST ONLY — `reqreply`/`sql`/`redis`/`file`/`mcpgo`/`mcp`/
> `llm`/`websocket` are explicitly deferred, unchanged), and the
> confirmed-exception rationale.
>
> **Decision 5 status: IMPLEMENTED and verified** (`go build`/`go vet`/
> `go test`/`just check`/`just examples` all green): `events.Transport`
> interface + `Client.Attach`/`.Publish`/`.Subscribe`/`.ServeSubscribers`
> methods — a reflection-based, type-erased convenience layer giving
> `Client` a literal `Publish(ctx, pub, msg)`/`Subscribe(ctx, sub, fn)`
> call shape (Go forbids generic methods, so this trades compile-time
> type safety for the call shape, explicitly and narrowly). Shipped
> across all 3 pub/sub adapters (`zeromq.Attach`, `mqtt5.Attach`,
> `mqtt.Attach`, each wrapping their existing `*Caller` internally) plus
> `examples/adapters-zeromq` reworked to demonstrate the full workflow
> end to end. See Decision 5 below for the full design, and
> [d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary)
> for the matching `api/rest` counterpart this decision is unified with
> (also implemented: `rest.Server.Attach`/`.Serve`, `rest.Client`/
> `.Call`, `nethttp.AttachMux`/`chi.AttachRouter`, `nethttp.Attach`).
>
> **Implementation status (Decisions 1-4):** Design phases (below) are all CLOSED, and the
> resulting design has been IMPLEMENTED and verified (`go build`/`go vet`/
> `go test` all green): `events.Client` rename; role-scoped
> `Subscriber[T]`/`Publisher[T]` builders (`WithSubscribe`/`WithPublish`,
> `.Use`/`.SubscribeMW`/`.PublishMW`, `.Handle(client)`); `FromSecurityScheme`
> + unconditional `CheckCoverage`; `events.SubscriberServer`/
> `events.PublisherClient[T]` interfaces; adapter wiring across
> `adapters/mqtt5`, `adapters/mqtt` (v3, `Caller` gains genuinely new
> capability since v3 had no router/bare `Subscribe` before), and
> `adapters/zeromq` (message-level security via `SubscribeMW`/
> `PublishMW`) — **`Caller`/`NewCaller`/`NewPublisherFor` were later
> UNEXPORTED/REMOVED by Decision 6's no-escape-hatches pass; read that
> decision, not this paragraph, for the adapters' final public shape**;
> `ports.EventPattern` gained dedicated `Subscribe`/`Publish` fields.
> **The `events.WithSecurityScheme` migration is COMPLETE**: verified via
> repo-wide grep that every EXTERNAL call site (examples, adapter tests)
> has migrated to `.Use(events.FromSecurityScheme(...))` — the only
> remaining call sites are `api/events/builder_test.go`'s OWN regression
> tests for the deprecated function itself, an intentional, permanent
> keep (see Lessons Learned item 3: full removal was correctly declined
> because the package's own test suite is a legitimate caller, not an
> oversight to fix). `events.WithSecurityScheme` therefore stays
> deprecated-but-kept by design — not a pending migration. **A
> post-implementation audit (see escape hatches #12/#13 below) found and
> fixed two real gaps between this doc's own stated design and what
> shipped**: `Channel.Register`/`ClientHandle` were supposed to be
> REMOVED per this doc's own Migration Checklist but were initially kept,
> silently reopening `CheckCoverage`'s core "no opt-out" guarantee for
> any handle built via them — now fully resolved by completing the
> removal (both methods gone, every call site migrated to
> `WithSubscribe`/`WithPublish` + `.Handle()`); and Decision 1's promised
> `checkImplementationsDeclared`/`UnknownMiddlewareImplementationError`
> reverse-coverage check (mirrors REST) was never actually implemented —
> now added. **A final gap-closure pass (triggered by a dedicated review
> of this doc against the shipped code, ahead of graduating it to
> `docs/design/`) found and fixed one more real gap**:
> `api/events/handletransport.go`'s `EncodeAndBuildTopic`/
> `DecodeAndMergeVars` (Decision 7) still hand-rolled the OLD
> format-resolution logic instead of delegating to Decision 9's
> canonical `ChannelHandle.EncodeWithFormats`/`DecodeMergedWithFormats` —
> the one place Decision 9's centralization pass missed (verified: zero
> shipped adapter calls either function, so the miss was latent, but
> both are public API documented as the pattern for a hand-written
> `PublishTransport[T]`/`SubscribeTransport[T]`) — now fixed to delegate,
> closing the loop on Decision 9. See the doc's closing addendum for the
> full write-up. Everything below
> this banner is the ORIGINAL design-decision narrative, preserved as
> historical record — read the code (`api/events/builder.go`,
> `adapters/mqtt5`, `adapters/mqtt`, `adapters/zeromq`, `ports`) as the
> source of truth for the SHIPPED shape; treat any divergence below as
> superseded by the code.

## Lessons Learned

Real issues hit while implementing this doc's design (11 phases, executed
mostly via parallel background agents), recorded here so future
multi-phase implementation rounds can avoid repeating them.

1. **Background agent session loss (happened twice).** Mid-flight session
   interruptions lost two background agents outright (once during adapter
   Fn-shape work, once during documentation sync) — checking on the agent
   afterward returned "agent not found" with no partial output
   recoverable. The recovery procedure that worked cleanly both times:
   check `git status --short` to confirm prior phases' work was intact
   and uncommitted-but-present, re-verify `go build`/`go test` still
   green, then re-launch the SAME phase with an equivalent prompt under a
   fresh agent ID. No duplicate or conflicting work resulted either time.
   **Takeaway:** treat long-running background-agent phases as
   resumable-but-not-durable; always re-verify repo state via
   `git status`/build/test before relaunching rather than assuming the
   lost agent made no progress (it may have, or may not have — check,
   don't guess).

2. **Additive-first staging was essential, not optional.** Every early
   phase (`Client` rename, role-scoped builders, security merge) was
   instructed to keep old mechanisms working (deprecated aliases,
   `ChannelOpt` satisfaction, etc.) instead of a "big bang" breaking
   change. This let the later exhaustive repo-wide migration phase run
   safely against a codebase that was building/testing green at every
   intermediate step. **Takeaway:** for large, multi-phase redesigns
   spanning many files, sequence breaking removals LAST and keep every
   intermediate phase independently green — never let two phases both
   assume the other's not-yet-shipped shape.

3. **Two "final removal" items were correctly refused by the
   implementing agent.** The migration phase was asked to remove
   `events.WithSecurityScheme` and `Subscribe`/`Publish`'s `ChannelOpt`
   satisfaction entirely, but declined both because `api/events`'s OWN
   regression test suite (intentionally preserved, not to be edited away
   just to unblock a removal) still legitimately exercises both
   mechanisms. **Takeaway:** "remove the old API" tasks must account for
   the package's own test suite as a legitimate caller, not just external
   call sites — full removal is a distinct, separately-scoped follow-up
   decision, not an automatic conclusion of "no external callers remain."

4. **Cross-agent file touch during parallel phases.** Running the three
   adapter-wiring phases (mqtt5, mqtt v3, zeromq) plus the ports
   integration phase as 4 simultaneous background agents caused one
   incidental cross-package touch (the mqtt5 agent edited a line in
   `adapters/mqtt/binding.go` for a shared generic instantiation while
   the mqtt v3 agent was concurrently working in that same package).
   Both agents' own summaries flagged awareness of it; the post-hoc
   full-repo build/test after all 4 completed showed no actual conflict.
   **Takeaway:** real but low-probability risk when parallelizing agents
   across adjacent packages that share a generic type — acceptable given
   a cheap post-hoc full-repo verification step, but worth watching for
   in any future parallel-adapter work.

5. **A stricter runtime check (`CheckCoverage` made unconditional)
   silently broke a previously-passing example, and `go build`/`go vet`/
   `go test` did not catch it.** `examples/adapters-mqtt-security`
   declared a security requirement via `Subscribe.Security` +
   `.Use(FromSecurityScheme(...))` but never paired it with a
   `SubscribeMW` implementation — this compiled fine and passed `go
   test` (no test exercises the example's `main()`), but failed at
   runtime with `MissingSecurityMiddlewareError` the first time the
   example was actually run. It was caught only because the final
   verification pass explicitly ran every example, not because any
   automated check flagged it. **Takeaway:** running every example
   end-to-end is a MANDATORY verification step for any change that
   tightens a runtime validation rule, not an optional nicety — static
   checks are provably insufficient for this failure class.

6. **A documentation-sync agent that self-reported "complete" still
   missed two stale symbol references.** The docs-sync agent updated
   `.github/instructions/go-codex.instructions.md`, the Zensical guides,
   `doc.go`s, README, and project-structure, and reported success — but
   a manual `grep -rn "events\.Builder\b"` sweep across `docs/`
   afterward still found two live references
   (`docs/features/api-builder.md`, `docs/features/websocket.md`) that
   would not compile if copy-pasted. **Takeaway:** a green build does not
   prove no dangling references remain — always run an explicit
   repo-wide grep sweep for every renamed/removed exported symbol after
   ANY doc-sync pass, even one an agent reports as complete; do not trust
   self-reported completion for this specific failure class.

7. **A function bundling two responsibilities silently dropped the less
   obvious one when its call path was swapped.** `RegisterEvent` (in
   `ports/spec.go`) was replaying a spec-less base `Channel[T]` instead of
   the role-scoped `Subscriber[T]`/`Publisher[T]` builder, which produced
   spurious AsyncAPI errors — found and fixed during the migration phase,
   not anticipated by the original phase plan. **Takeaway:** when
   swapping the underlying builder type behind an existing integration
   point, explicitly re-verify EVERY caller of the old type's methods,
   not just the ones the phase's task description called out — bundled
   side effects are easy to lose silently.

8. **Broker-dependent examples could only ever be build-verified, not
   run, in this sandbox.** `examples/adapters-mqtt5`, `adapters-mqtt`,
   `adapters-zeromq`, and `sensor-service` require live broker/socket
   infrastructure absent from the implementation environment — accepted
   as a permanent, unavoidable verification gap for this implementation
   round, not a shortcut that should be revisited without first
   provisioning that infrastructure.

---

> **Prior status (design-complete, pre-implementation):** Decision 1 RESOLVED, and every design gap raised during
> a follow-up review pass has been CLOSED (Client-centric role model +
> role-scoped `Subscriber[T]`/`Publisher[T]` builders — the former
> separate "Decision 2, middleware attach mechanism" is FOLDED INTO
> Decision 1). Spun out of a now-removed doc, "Events/ReqReply/Ports
> Workflow Simplification", after a critical re-review found that doc's
> Decision 1 borrowed REST/`reqreply`'s `Register`/`ClientHandle` role
> split onto pub/sub WITHOUT checking whether pub/sub's role structure
> actually fits — it does not (see "Why pub/sub needs its own doc"
> below). That doc's pub/sub-scoped content is now fully superseded by
> this one (this doc, design-complete and shipped); its `reqreply`-scoped
> content was carried forward into its own dedicated doc — see
> [ReqReply Workflow Simplification](reqreply-workflow-simplification.md).
> **Resolution (went through 3 drafts before landing
> here — see Decision 1's own subsections for the full history):**
> pub/sub has no "server" role — a broker is the intermediary, both
> publisher and subscriber are CLIENTS of a channel. `events.Builder` is
> renamed to `events.Client`. `Channel.Register`/`ClientHandle` are
> replaced by `Channel.WithSubscribe(events.Subscribe{...})`/
> `Channel.WithPublish(events.Publish{...})`, each returning a small
> role-scoped builder (`events.Subscriber[T]`/`events.Publisher[T]`)
> carrying its OWN `Use`/`SubscribeMW`-or-`PublishMW` and a terminal
> `.Handle(client)` (client optional; topic-based dedup of SPEC entries
> only — never a shared/mutated handle object, avoiding a data race —
> for bidirectional clients). `Subscribe(fn)`/`SubscribeWithHandle(fn)`
> (naming now LOCKED IN) keep `fn` as a required CALL-TIME param for
> single-channel, imperative "start consuming now" use — but
> `Subscriber[T]` ALSO gained back a declare-time `WithHandler(fn)`,
> consumed by a NEW, per-adapter whole-client `ServeSubscribers` METHOD
> on a NEW `Caller` type (mirrors `rest.Serve(mux, builder)`; renamed
> from a bare "Serve" after finding `mqtt5`/`zeromq` already export
> `Serve[Req,Resp]` for `reqreply`) — fully independent of `Subscribe(fn)`,
> no "which wins" ambiguity. **A follow-up design-gap-closure pass found
> and fixed a blocking problem**: the B2 data-race fix made `.Handle()`
> always return unretained handles, so `ServeSubscribers` had nothing to
> invoke — fixed via a `sync.RWMutex`-guarded registry on `Client` with 2
> decoupled slots (a spec-copy value; a replaceable, never-mutated
> reference for `ServeSubscribers`). **A LATER critical review found
> that FIX ITSELF had a real bug**: having `.Handle(client)` update
> BOTH registry slots meant the value-based `Subscribe(fn)`'s internal
> `.Handle()` call (whose `Subscriber[T]` normally has no `Handler`
> attached) could SILENTLY overwrite a previously `ServeSubscribers`-
> registered `Handler` for the same topic+client — contradicting this
> very decision's "fully independent paths" claim. **Fixed by fully
> separating the two: `.Handle(client)` now NEVER touches the
> registry at all; a NEW, separate `Subscriber[T].Register(client)
> error` method is the ONLY way to feed `ServeSubscribers`** (requires
> `Handler != nil`, erroring via a new `events.MissingHandlerError`
> otherwise — no silent no-op, no accidental unregistration possible).
> The SAME pass found that pub/sub's generic-dispatch/per-channel-
> adapter-options problem (previously
> believed harder than REST's) is SOLVED by mirroring REST's OWN
> existing `HandlerOpts`/`WithOptions`/`resolveOptions` pattern exactly —
> `ChannelHandle[T]` gains `HandlerOpts any`, `Client.SubscriberEntries()`
> mirrors `rest.RouteEntry`. A NEW `events.SubscriberServer` interface
> (`ServeSubscribers(ctx) error`) lets application code stay transport-
> agnostic at the call site — each adapter's `*Caller` implements it. A
> follow-up goal-alignment review found `SubscriberServer` only closed
> HALF of the transport-agnosticism goal (subscribe side) — a matching
> `events.PublisherClient[T]` interface (`Publish(ctx, msg) error`) was
> added to complete the symmetry, backed by a small per-adapter
> `PublisherFor[T]` binding type (naming deliberately avoids colliding
> with the ALREADY-EXISTING `events.Publisher[T]` role-scoped builder —
> a real collision caught before finalizing). Whether REST/`nethttp`/
> `chi` should adopt an ANALOGOUS interface is spun out to its own doc,
> [d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary),
> after finding a real shape mismatch (REST's `Serve` wires-and-returns
> immediately; pub/sub's `ServeSubscribers` blocks-and-runs). Checking
> `mqtt`(v3)/`zeromq`'s actual code also caught a real error in an
> earlier draft: `zeromq`'s existing `sock`-taking shape maps cleanly
> onto the same `Caller`/two-tier design, but `mqtt`(v3) has NO router
> and NO bare `Subscribe` at all — it needs GENUINELY NEW capability (a
> higher-level `Caller`/`Subscribe`/`ServeSubscribers` wrapping the
> existing, unchanged `SubscribeHandler` primitive), not a mechanical
> rename. `adapters/mqtt5.Subscribe`/`Publish` ALSO gained the two-tier
> `Subscribe`/`SubscribeWithHandle` split mirroring REST's `Call`/
> `CallWithHandle`, taking a NEW `mqtt5.Caller` (bundling the 3 params
> they'd otherwise repeat) — deliberately WITHOUT a `WithBaseURL`
> equivalent (MQTT has no analog to HTTP's connection-pooled transport
> independent of its target). **Escape-hatch simplification review
> complete** (all 8 open items walked step-by-step): coverage
> enforcement is now UNCONDITIONALLY wired; Decision 3 gained a
> `PublishMW` `*T`-write-access generalization, closing `mqtt`(v3)'s
> publish-side gap via an in-payload credential mechanism that also
> significantly de-risks zeromq (spun out to
> [ZeroMQ Security Mechanism](zeromq-security.md)); the other 6 items
> were confirmed KEEP, matching REST's own precedent. The security
> model is documented as exactly 2 mechanisms (connection-level
> authentication; message-level authorization), not 3 — see "Security
> model: two mechanisms, not three." The "is pub/sub scopes meaningful"
> question is also RESOLVED (confirmed non-issue — `route.Satisfied`
> already degrades gracefully to plain pass/fail). **All known design
> gaps in this doc's OWN scope are now closed** — remaining work is
> `zeromq`'s own narrower open questions (its own spun-out doc) and
> per-adapter IMPLEMENTATION DETAIL (not shape) for the future
> adapter-wiring pass. **A subsequent critical review round is fixing
> further design flaws one-by-one:** F1 (fixed) — the registry fix
> above initially had `.Handle()` ALSO feed `ServeSubscribers`'s
> registry, which let `Subscribe(fn)`'s internal `.Handle()` call
> silently unregister a previously-registered `Handler`; fully
> separated via a NEW `Subscriber[T].Register(client) error` (the sole
> way to feed `ServeSubscribers`, requires `Handler != nil`, errors via
> new `events.MissingHandlerError` otherwise) — `.Handle()` never
> touches that registry at all anymore. F3 (fixed) — `events.WithSecurityScheme`
> (the OLD, shipped scheme-declaration mechanism) and `middleware.Middleware.Security`
> (the NEW `.Use()`-read mechanism) were an unresolved duplication;
> **`events.WithSecurityScheme` is REMOVED**, replaced by a NEW
> `events.FromSecurityScheme(schemeName, scheme, scopes) middleware.Middleware`
> bridging constructor — mirrors REST's OWN Revision 2 (which made the
> identical removal) exactly. F4 (fixed) — `events.Subscribe{Security:...}`/
> `events.Publish{Security:...}` (manual, unchanged) and `.Use()`-attached
> middleware `Security` could silently DISAGREE on scopes for the same
> scheme, with zero conflict detection; fixed by porting REST's OWN
> `applySecurityDeclarations`/`ConflictingSecurityDeclarationError`
> mechanism verbatim, run SEPARATELY per role. F5 (resolved) — a
> concern that this decision's 20+ new symbols undermine the "simple"
> goal was checked CONCRETELY: the actual minimal (no-security,
> single-channel) case's OLD-vs-NEW delta is only +1 step
> (`NewCaller`) — see the new "Quick start" subsection; the rest of the
> vocabulary is opt-in, never touched by the common path. F6 (interim
> fix + spun out) — `middleware.Middleware`'s 5 REST-only param fields
> are meaningless for pub/sub, so a NEW `events.UnsupportedMiddlewareParamsError`
> rejects any `.Use()`-attached middleware setting them; the PROPER
> long-term fix (a common-base + per-pattern-derived middleware TYPE
> hierarchy) is bigger than pub/sub — confirmed `api/reqreply` has the
> SAME pre-existing issue already shipped — spun out to
> [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md).
> F7 (resolved) — `ServeSubscribers` (unlike the already-minimal
> `Subscribe(fn)`) genuinely needed a non-nil `*events.Client` with ≥1
> registered `Subscriber[T]`, with no zero-ceremony shortcut; fixed by
> adding `ServeOneSubscriber`, mirroring `nethttp.ServeOne` exactly
> (builds a throwaway `*events.Client`+`*Caller` internally). F8/F9/F10
> (documentation-only) — `NewCaller`'s `eventsClient` nilability,
> `ServeSubscribers`'s snapshot-at-call-time behavior, and the
> `io.Reader`-style scope of "transport-agnostic" are all now stated
> explicitly at their respective definitions. **A SEPARATE review round
> of the SecurityScheme/middleware/spec-rendering path found and fixed
> two more issues:** F11 — `WithSecurityScheme`'s removal (F3) never
> specified what populates `ChannelHandle.SecuritySchemes`, which would
> have silently rendered `components.securitySchemes` EMPTY; fixed by
> extending the ALREADY-ported merge function (F4) to ALSO populate it,
> mirroring REST's `applySecurityDeclarations` exactly (confirmed pub/sub
> needs only ONE fallible merge function, not REST's fallible/infallible
> split, since `.Handle()` is already universally fallible). And a
> review of the middleware concept for NON-spec-adding middlewares (like
> an Observer) found `SubscribeMW`/`PublishMW` never specified a
> general-purpose (non-security) Fn shape at all — fixed by giving BOTH
> a `func(next func(ctx, T) error) func(ctx, T) error` wrapping shape
> (mirrors REST server-side `HandleMW`'s `func(http.Handler) http.Handler`
> for `SubscribeMW`; a NEW design for `PublishMW`, since REST's
> client-side `ClientMW` has NO general-purpose hook at all — a
> pre-existing REST gap, spun out at the time to a dedicated roadmap doc
> rather than fixed here; now resolved and folded into
> [d-0001's Addendum 3](d-0001-rest-middleware-workflow-simplification.md#addendum-3-client-side-general-purpose-clientmw-hook-closes-the-last-known-restevents-middleware-asymmetry)).
> **A dedicated review of `TopicParam`/param
> declaration, matching, and rendering confirmed TopicParams are
> IDENTICAL across both roles by construction** (declared on
> `Channel[T]` itself, before the role fork) **and found `.Handle(client)`
> can run its FULL validation suite (topic-param-name check,
> merge-field-type check, format-type check, security-conflict check,
> unsupported-middleware-params check) UNCONDITIONALLY, regardless of
> `client`'s nilness** — closing two REST-mirrored gaps (topic-param
> validation and a second, previously-unmentioned merge-field-type
> panic site) that REST itself cannot close without also making its
> OWN `ClientHandle()` fallible (out of scope). `client`'s nilness now
> affects ONLY spec registration, never which checks run. **A dedicated
> wildcard review found a real, PRE-EXISTING bug (not caused by this
> redesign, but directly inherited by its new primary entry points):**
> the top-level exported `mqtt5.Subscribe`/`zeromq.Subscribe` send the
> RAW topic template (e.g. `"sensors/{sensorID}/data"`) VERBATIM as the
> broker subscription filter, never deriving a real wildcard (`+`)
> filter for mqtt5 or a byte-prefix filter for zeromq — meaning a
> templated-topic channel silently never receives any message against a
> real broker/socket. Fixed by adding a `TopicFilter string` field to
> `mqtt5.SubscribeOptions`/`zeromq.SubscribeOptions`/`zeromq.
> SubscribeAdapterOptions` (empty auto-derives via mqtt5's
> already-existing `deriveWildcardFilter`, or zeromq's new
> `deriveTopicPrefix`) — purely additive, non-templated topics
> unaffected. **A dedicated review of how `api/events` integrates into
> `ports` found Decision 4 (this doc's OWN earlier revision) was
> internally self-contradictory**: it assumed `pat.Subscribe`/
> `pat.Publish` could still be scanned out of `EventPattern.Opts
> []events.ChannelOpt`, while Decision 1 (written later) makes
> `Subscribe`/`Publish` stop satisfying `ChannelOpt` entirely — making
> that scan impossible. Corrected by giving `EventPattern` two new
> dedicated fields (`Subscribe *events.Subscribe`/`Publish
> *events.Publish`, separate from `Opts`), confirmed against real usage
> (`examples/sensor-service`) which already declares exactly one of the
> two per pattern value. A wrong-role field set (e.g. a `SourcePort`'s
> pattern declaring `Publish`) is now rejected eagerly via a new
> `PatternRegisterError`; a nil correct-role field defaults to a
> zero-value, matching today's implicit behavior. Implementation has
> not started.
> [← Back to Roadmap](index.md)

---

## Why pub/sub needs its own doc

REST (`api/rest`) and `api/reqreply` share one structural property that
made their `middleware`-package redesign clean: **the two roles are
always paired the same way, every time.** The side that calls
`Route.Register(builder)` is ALWAYS the verify/receive side (HTTP server,
reqreply replier) and needs `Implementations`. The side that calls
`Route.ClientHandle()` is ALWAYS the credential-supply/initiate side (HTTP
client, reqreply caller) and needs `ClientImplementations`. One axis, two
roles, fixed pairing — `Register` populates one field, `ClientHandle`
populates the other, and that is the whole story.

**Pub/sub does not have this fixed pairing.** A `Channel[T]` has (at
least) three independent roles in play:

1. **Spec owner** — whichever process calls `Channel.Register(builder)`
   and accumulates the channel into an `events.Builder` for
   `AsyncAPISpec()`/`AppendTo()` output.
2. **Subscriber** — the verify side. Needs `Implementations` (to check
   incoming message security).
3. **Publisher** — the credential-supply side. Needs
   `ClientImplementations` (to attach credentials to outgoing messages).

Unlike REST/reqreply, **these three roles are not fixed to two sides of
one call.** A single process can be spec-owner + subscriber + publisher
all at once (a demo/test binary). Or the spec owner can be a totally
separate "schema registry" service that neither subscribes nor publishes.
Or — the case that actually breaks the current design — **the spec owner
can be the publisher, while a completely separate subscriber-only
microservice needs to verify incoming messages but never touches the
shared `Builder`.**

Confirmed via code: `Channel.ClientHandle()`'s own godoc already
anticipates this ("Use ClientHandle... when sharing a Channel definition
between publisher and subscriber in the same binary without a builder
registration" — i.e. it already expects to be called from either role).
But the old doc's Decision 1 draft had `ClientHandle()` populate ONLY
`ClientImplementations` (mirroring REST/reqreply's client role exactly) —
which means **a subscriber-only process calling `ClientHandle()` would
have no way to attach a verify-side `Implementations` to its handle at
all.** This is not a hypothetical: it is the natural shape of a
multi-service pub/sub deployment (publisher and subscriber are usually
different binaries, often different teams/repos).

**One mitigating factor, confirmed via code:** `ports.EventPattern`
(`ports/handle.go`'s `buildEventPatternHandles`) ALWAYS calls
`channel.Register(b)` — for BOTH `SourcePort` (subscribe) and `SinkPort`
(publish) roles — creating a private single-use `events.Builder` when
none is supplied. So **the `ports` layer never hits this gap**; it is
specific to hand-declared `Channel` values shared directly across
processes via `ClientHandle()`. Still a real gap for that (very common)
usage pattern, and worth fixing at the `api/events` layer directly rather
than only working around it inside `ports`.

## Diagram — the CURRENT workflow, every input and escape hatch

The prose above describes the role-asymmetry gap; this diagram shows
WHERE, concretely, every declaration-time input and every escape hatch
sits along the current (pre-Decision-1/2) pub/sub call path — both the
subscribe and publish sides, since they diverge after the shared
declaration step. (This repository has no rendered/mermaid diagram
convention anywhere in `docs/` — every doc is prose/tables/code fences
only — so this uses plain ASCII box-and-arrow art inside a fenced code
block, consistent with that style.)

```text
DECLARE (shared, one Channel[T] value, both roles read it)
┌──────────────────────────────────────────────────────────────┐
│ events.NewChannel[T](topic, codec, opts...)                   │
│   opts:                                                       │
│    • events.Subscribe{OperationID, Security, ...}  (spec only)│
│    • events.Publish{OperationID, Security, ...}    (spec only)│
│    • events.WithSecurityScheme(name, SecurityScheme)          │
│    • events.TopicParam{...}.WithCodec(...) / MergedTopicParam │
│    • events.Formats/.SubscribeFormats/.PublishFormats         │
│    • events.ErrorChannel(...)  (error-path declaration)       │
└──────────────────────────────────────────────────────────────┘
        │                                   │
        │ (subscriber process)              │ (publisher process)
        ▼                                   ▼
┌───────────────────────────┐     ┌───────────────────────────┐
│ channel.Register(builder) │     │ channel.Register(builder) │
│  OR channel.ClientHandle()│     │  OR channel.ClientHandle()│
│  → *ChannelHandle[T]      │     │  → *ChannelHandle[T]      │
│  (builder param: SAME     │     │  (builder param: SAME     │
│  *events.Builder either   │     │  *events.Builder either   │
│  role must share to avoid │     │  role must share to avoid │
│  a duplicate spec entry — │     │  a duplicate spec entry — │
│  ESCAPE HATCH: nothing    │     │  ESCAPE HATCH: nothing    │
│  enforces this sharing)   │     │  enforces this sharing)   │
└───────────────────────────┘     └───────────────────────────┘
        │                                   │
        ▼                                   ▼
┌───────────────────────────┐     ┌───────────────────────────┐
│ adapters/mqtt5.Subscribe( │     │ adapters/mqtt5.Publish(    │
│   ctx, sock, handle, qos, │     │   ctx, sock, handle, msg,  │
│   fmt, opts)              │     │   fmt, opts)               │
│                            │     │                            │
│ opts.SecurityFunc          │     │ opts.CredentialFunc        │
│  func(ctx, *Publish, reqs) │     │  func(ctx, reqs)           │
│    error                   │     │    ([]UserProperty, error) │
│  — PER-CALL Options field, │     │  — PER-CALL Options field, │
│  NOT handle-attached        │     │  NOT handle-attached        │
│  ESCAPE HATCH: caller can  │     │  ESCAPE HATCH: caller can  │
│  pass a DIFFERENT          │     │  pass a DIFFERENT          │
│  SecurityFunc on every     │     │  CredentialFunc on every   │
│  individual call — no      │     │  individual call — no      │
│  drift prevention          │     │  drift prevention          │
│                            │     │                            │
│  nil SecurityFunc + a      │     │  nil CredentialFunc + a    │
│  declared Security = ESCAPE│     │  declared Security = ESCAPE│
│  HATCH: silently           │     │  HATCH: silently           │
│  unenforced, no error      │     │  unenforced, no error      │
└───────────────────────────┘     └───────────────────────────┘
        │                                   │
        ▼                                   ▼
  (same shape repeats for adapters/mqtt (v3) subscribe-side;
   adapters/mqtt (v3) publish-side has NO CredentialFunc at all —
   protocol limitation, not an escape hatch;
   adapters/zeromq has NEITHER SecurityFunc NOR CredentialFunc —
   ESCAPE HATCH: every zeromq call unconditionally unenforced)

OTHER STANDING ESCAPE HATCHES (not tied to one call site above):
  • SecurityScheme.Codec nil → raw credential passed through unvalidated
  • Builder.AddGlobalSecurity nil-inherits / empty-means-none (3-state,
    easy to misread which state a channel is in)
  • Last-registered-wins on WithSecurityScheme name collisions (silent)
  • Connection-level SecuredClient (mqtt5/mqtt v3) totally uncoordinated
    with message-level SecurityScheme/SecurityFunc (both configured
    independently, no cross-check)
```

**Key observation this diagram makes visible:** every security-relevant
input in the current design is either (a) a per-call `Options` field
(drifts freely between calls, easy to forget) or (b) a spec-only
declaration with NO enforcement mechanism attached at all
(`WithSecurityScheme`/`Security` fields are metadata-only unless a
matching `SecurityFunc`/`CredentialFunc` closure happens to be supplied
at the SAME call). There is no single place a reader can look to know
"is this channel actually enforced, and by what."

## Confirmed current state (fresh code audit, this pass)

### Declaration layer
- `events.WithSecurityScheme(name, SecurityScheme) ChannelOpt` — the only
  declaration mechanism today. No `middleware` package integration
  anywhere in `api/events` — confirmed via repo-wide grep, zero hits for
  `middleware.` in `api/events/*.go` (non-test).
- `Channel[T]` carries `Subscribe{}`/`Publish{}` `ChannelOpt` markers
  (`cb.subscribe`/`cb.publish`, both `*Subscribe`/`*Publish`, nil when not
  declared) — these already express "which direction(s) this channel
  participates in" at the SPEC level (AsyncAPI operation generation). They
  are NOT currently consulted by `Register`/`ClientHandle` to decide which
  security-relevant fields to populate — an opportunity for Decision 1 to
  make use of, not a new concept to invent.

### Adapter layer — per-transport security mechanism (fresh audit)

| Capability | `mqtt` (v3) | `mqtt5` | `zeromq` |
|---|---|---|---|
| Connection-level `SecuredClient`/`ConnectSecurityScheme` | ✅ | ✅ | ❌ (see [ZeroMQ Security Mechanism](zeromq-security.md)) |
| Message-level subscribe-side `SecurityFunc` | ✅ `func(ctx, pahomqtt.Message, reqs) error` (confirmed, mirrors mqtt5 exactly) | ✅ `func(ctx, *pahomqtt5.Publish, reqs) error` | ❌ today; tractable via the in-payload mechanism below (see [ZeroMQ Security Mechanism](zeromq-security.md)) |
| Message-level publish-side `CredentialFunc` | ⚠️ protocol-native (`UserProperty`) output impossible (no per-message property channel) — **BUT the in-payload mechanism below (Decision 3) works identically to mqtt5's**, closing the practical gap | ✅ `func(ctx, reqs) ([]UserProperty, error)` — **revised by Decision 3 to also accept `*T`** | ❌ today; tractable via the same in-payload mechanism (see [ZeroMQ Security Mechanism](zeromq-security.md)) |
| Handle-attached implementation (vs. per-call `Options`) | ❌ | ❌ | ❌ |
| Scope-grant / `middleware.CheckScopes` integration | ❌ | ❌ | ❌ |

`adapters/zeromq` confirmed (fresh grep, zero hits for
`SecurityFunc`/`CredentialFunc`/`middleware.` in `adapters/zeromq/*.go`)
to have NO security mechanism of any kind today — `Subscribe`/`Publish`
only ever see `[topic, payload]` frames, no header/property slot exists
to carry a credential OUT-OF-BAND. **This no longer means zeromq
security requires inventing a new wire-level convention before ANYTHING
is possible** — Decision 3's in-payload mechanism (below) needs no wire
change at all, since a credential embedded as an ordinary field in the
codec-decoded payload works identically regardless of transport. See
[ZeroMQ Security Mechanism](zeromq-security.md) for the narrower
remaining question (an OPTIONAL out-of-band frame-based mechanism, plus
connection-level/CURVE) — spun out to its own doc since it still has no
concrete driver, unlike the in-payload mechanism which is folded
directly into this doc's Decision 3.

### Security model: two mechanisms, not three

A review of pub/sub's escape hatches surfaced a terminology question
worth resolving explicitly, since it shapes how every subsequent
Decision talks about "security": pub/sub has exactly **two** security
mechanisms, not three.

1. **Connection-level authentication** (to the broker) — `SecuredClient`
   (mqtt5/mqtt v3), unchanged, out of scope for this doc. Broker-native
   topic ACLs (Mosquitto `acl_file`, EMQX rules, etc.) that key off
   CONNECT-time identity are ALSO connection-level under this
   definition — confirmed via `docs/features/security.md`: "This
   already works today with ZERO go-codex involvement." A caller who
   only needs "one connection = one identity, broker enforces which
   topics that identity may touch" needs NOTHING from this doc at all.
2. **Message-level authorization** — `SubscribeMW`/`PublishMW`
   (Decision 1/3). A custom, topic-scoped authorization scheme (e.g. an
   OAuth2 REST endpoint issuing topic-shaped scopes) is a USAGE
   CONVENTION of this SAME mechanism, NOT a third, separate one — declare
   a scope shaped like the topic (`"sensors/{sensorID}/data:subscribe"`)
   and check it against the concrete topic post-`BuildTopic` inside the
   `fn`. No dedicated new API (e.g. no `TopicScope` type) is introduced
   for this — it is exactly the pattern already documented in the
   superseded events-reqreply-ports doc, carried forward unchanged.

**Why message-level authorization is still valuable even though the
message is already received/decoded by the time it's rejected:** it is
NOT redundant with topic-level checks (broker ACL or scope-convention)
— it exists for when topic-level checks are too COARSE. The concrete
case: one shared connection/subscription can multiplex traffic for
MULTIPLE logical identities via a wildcard topic (e.g. `sensors/+/data`
— many devices, one subscription) — no topic-level check can
distinguish individual devices UNDER that wildcard; only inspecting the
message's OWN embedded claim can. The "cost already paid" concern (the
message is already decoded before rejection) is real but minor — the
same trade-off HTTP middleware already accepts (a request body is often
already read before a header-based rejection fires).

## Decisions

### Decision 1 — Client-centric role model + role-scoped `Subscriber[T]`/`Publisher[T]` builders (RESOLVED this pass, supersedes 2 earlier drafts)

**Pub/sub has no "server" role at all.** A broker (MQTT/ZeroMQ) is the
actual intermediary — BOTH a publisher and a subscriber are CLIENTS of a
channel. A client can play either role, or both (some channels are
bidirectional for a given client). The spec is owned by whichever
`Client` declares its own channel usage — not by an anonymous,
role-agnostic `*Builder` parameter, which is what made "who owns the
spec" genuinely ambiguous for pub/sub (unlike REST/reqreply, where the
asymmetric HTTP-server/HTTP-client or replier/caller relationship never
raised this question).

**`events.Builder` is RENAMED to `events.Client`** — a deliberate,
full-blast-radius rename (not a same-named alias, not an additive
wrapper type): same struct, same methods (`AddServer`, `AddSchema`,
`AddGlobalSecurity`, `WithTopicCodec`, `WithTopicConstraints`,
`AsyncAPISpec()`, `AppendTo()`, `AddChannelItem()` all keep their exact
name/behavior), just renamed. `NewBuilder(...)` → `NewClient(...)`. This
touches every existing pub/sub call site that constructs/references
`*events.Builder` today, not only the security/middleware feature — see
"Migration checklist" below. **Scoped to `api/events` ONLY** —
`rest.Server`/`reqreply.Builder` are NOT renamed: REST/reqreply's
"Builder builds a SERVER-owned spec" framing already matches their
genuinely asymmetric client/server relationship; there is no equivalent
"who owns the spec" confusion to resolve there, and this rename must not
be proposed for those packages.

#### Why this decision went through two earlier drafts before landing here

A clarifying question about the declaration workflow ("can we declare a
channel, THEN attach publish and/or subscribe config separately, each
with its own `.Use()`?") surfaced two real gaps in an earlier flat
`Channel.SubscriberHandle(client)`/`Channel.PublisherHandle(client)`
draft (fully superseded by what follows — the diagrams below already
reflect the FINAL resolved shape, not this intermediate step):

1. **Per-role Security gap.** Shipped `events.Subscribe{Security:...}`/
   `events.Publish{Security:...}` ALREADY let each operation declare an
   INDEPENDENT security requirement (even against different schemes) —
   confirmed via `api/events/builder.go`'s own doc comment example. A
   single CHANNEL-level `Use(mws...)` cannot express "subscribing needs
   scope A, publishing needs scope B" — a real regression versus what
   was already possible.
2. A second question then arose: should `Subscribe`'s business handler
   also move to declare time (mirroring `rest.Route.WithHandler`)? On
   reflection this was rejected — see "Why `Subscribe`'s handler stays
   call-time, not declare-time" below — but exploring it surfaced a
   THIRD, genuinely useful finding: where exactly does middleware
   attachment live relative to the imperative `Subscribe`/`Publish`
   calls, and should those calls take a value (not a pre-built handle)
   directly? Resolved by the two-tier API described below, borrowed
   directly from REST's OWN `Call`/`CallWithHandle` precedent.

#### The resolved shape

**`Channel[T]` gains two role-scoped builder accessors — NOT a flat
`SubscriberHandle`/`PublisherHandle` pair on `Channel[T]` itself:**

```go
func (c Channel[T]) WithSubscribe(s Subscribe) events.Subscriber[T]
func (c Channel[T]) WithPublish(p Publish) events.Publisher[T]
```

`events.Subscribe{}`/`events.Publish{}` are UNCHANGED in shape/fields
(`OperationID`, `Summary`, `Description`, `Tags`, `SchemaName`,
`Security`) — still pure AsyncAPI operation metadata — but move from
"one of many `NewChannel` opts" to "the sole argument of a dedicated
role-entry method." This is the one breaking USAGE change to these two
existing types (their fields are untouched).

**`events.Subscriber[T]`/`events.Publisher[T]` — new, small, role-scoped
builder types** (agent-noun naming, matching the `Client`/`Subscriber`/
`Publisher` vocabulary this doc already establishes). Each independently
carries:

- **Its OWN `Use(mws ...middleware.Middleware)`** — returns
  `Subscriber[T]`/`Publisher[T]` respectively. Fixes gap 1: a
  `Subscriber[T]` declares security wholly independent of whatever a
  `Publisher[T]` for the SAME channel declares. **`Channel[T]` itself
  does NOT keep a channel-level `Use()`/`WithMiddleware` at all —
  removed entirely, not kept as an overridable shared default.**
  Rationale: simplest, one way to declare security per role, no
  "two-declare-sites, which wins" escape hatch. A requirement shared by
  both roles is declared twice (once per role's builder) — marginally
  more typing for the common case, zero ambiguity in exchange.
- **`Subscriber[T].SubscribeMW(mw *middleware.Middleware, fn any) Subscriber[T]`** /
  **`Publisher[T].PublishMW(mw *middleware.Middleware, fn any) Publisher[T]`** —
  mirror `rest.Route.HandleMW`/`ClientMW`'s nilable-`mw` paired/unpaired
  semantics exactly (paired: `mw.Security != nil`, matched against a
  previously-`.Use()`'d declaration via `Satisfies`; unpaired: `mw` nil
  or `Security` nil, general-purpose, runs unconditionally — see
  Decision 3's "General-purpose (non-spec) Fn shapes" subsection for
  WHAT the unpaired Fn's SHAPE actually is, e.g. for an
  Observer-equivalent). Living on their own role-scoped receiver type
  also eliminates, for free, an earlier naming-collision concern an
  intermediate flat-`Channel` draft had — `Subscriber.SubscribeMW`/
  `Publisher.PublishMW` read unambiguously now that role and method
  both live on a role-named type.
- **No `WithHandler` on `Subscriber[T]`, and no equivalent was ever
  proposed for `Publisher[T]`.** See the dedicated subsection below —
  this was explored and explicitly rejected.

#### `events.WithSecurityScheme` REMOVED — `.Use()` becomes the sole security-scheme declaration mechanism (found during a critical review, resolved by mirroring REST's OWN Revision 2)

A critical review found a real, unresolved duplication: `events.WithSecurityScheme`
(the EXISTING, shipped mechanism — still the ONLY way to declare a
channel's security scheme BEFORE this decision) and `middleware.Middleware.Security`
(the NEW mechanism `.Use()` reads) are TWO independent, overlapping ways
to describe the SAME conceptual thing. Confirmed via code:
`middleware.SecurityDeclaration` already carries a full SUPERSET of
`events.SecurityScheme`'s fields (`SchemeName`, `Scheme
route.SecurityScheme`, `Scopes`, AND `Codec *codex.Codec[string]`) — so
there is nothing `WithSecurityScheme` can express that `.Use()` cannot.

**REST faced this EXACT duplication once already, and resolved it in
its own Revision 2 by REMOVING `rest.WithSecurityScheme` entirely**,
replacing it with `middleware.SecurityScheme(...)`/
`rest.FromSecurityScheme(...)` bridging into `.Use()` (confirmed via
`docs/features/security.md`). **Pub/sub does the SAME here:**

- **`events.WithSecurityScheme` is REMOVED** — a breaking change,
  deliberately accepted, consistent with this doc's already-established
  pattern (`Register`/`ClientHandle` were already removed earlier in
  this same decision).
- **`events.SecurityScheme` STAYS** as a plain value type (mirrors
  `rest.SecurityScheme` remaining a value type after REST's own
  removal — it's still the natural place to declare a scheme's spec
  metadata + optional `Codec`).
- **NEW: `events.FromSecurityScheme(schemeName string, scheme SecurityScheme, scopes []string) middleware.Middleware`**
  — bridging constructor, mirrors `rest.FromSecurityScheme` exactly
  (a one-line field copy into `middleware.Middleware{Security:
  &middleware.SecurityDeclaration{...}}`). Usage:
  ```go
  var bearerAuth = events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
      WithCodec(codex.String().Refine(validate.BearerToken))

  sub := channel.WithSubscribe(events.Subscribe{...}).
      Use(events.FromSecurityScheme("bearerAuth", bearerAuth, []string{"subscribe:sensors"}))
  ```
- `.Use()` is now the SOLE way to declare a channel's security scheme —
  resolves the duplication completely (one way to do things, no
  competing paths).

**`events.WithSecurityScheme` name-collision escape hatch does NOT
disappear — it RELOCATES, unchanged in kind.** REST's OWN docs confirm
this same relocated risk persists even after REST's own removal
(`docs/features/security.md`: "When two routes declare the same scheme
name with different values, the last-registered route wins (no
error)") — the SAME "last-registered-wins, silent" policy applies to
`.Use(FromSecurityScheme(...))`-declared schemes sharing a name across
channels registered against the same `Client`. See the updated escape
hatch #7 below — reworded, not removed.

#### Merge/conflict-detection for security REQUIREMENTS + SecuritySchemes population — ports REST's `applySecurityDeclarations`/`ConflictingSecurityDeclarationError`, run per role (found during the same critical review, resolved; EXTENDED after a later review found the scheme-metadata half was missing)

Removing `events.WithSecurityScheme` (above) resolved the SCHEME
duplication — but a SEPARATE, DISTINCT duplication remained unaddressed:
`events.Subscribe{Security:...}`/`events.Publish{Security:...}` (the
EXISTING, unchanged manual field — confirmed still present, "The
resolved shape" above states `Subscribe{}`/`Publish{}` are "UNCHANGED in
shape/fields... `Security`") and `.Use()`-attached middleware's
`Security` (`SecurityDeclaration.Scopes`) are TWO independent sources
that can BOTH declare a REQUIREMENT (which scopes a given scheme needs)
for the SAME operation — with ZERO conflict detection between them.

**Confirmed via code: REST solved this EXACT problem already.**
`applySecurityDeclarations` (`api/rest/middleware.go`) merges
contributions from the manual `RouteMeta.Security` field AND every
`.Use()`-attached middleware's `Security`, erroring via
`ConflictingSecurityDeclarationError{Route, Scheme, FirstSource,
SecondSource, FirstScopes, SecondScopes}` if the SAME scheme name gets
DIFFERENT scopes from different sources.

**Fix: port this mechanism, run SEPARATELY per role** (matching
Decision 1's existing per-role independence — the SAME reason
`Subscriber[T]`/`Publisher[T]` each got their own `.Use()` instead of a
shared channel-level one):

- `Subscriber[T]`: merges `events.Subscribe{Security:...}` (manual) +
  every `.Use()`-attached middleware's `Security` for THIS `Subscriber[T]`
  — same-scheme-different-scopes across sources is a conflict.
- `Publisher[T]`: identical mechanism, against
  `events.Publish{Security:...}` + its OWN `.Use()`-attached
  middlewares — entirely independent of `Subscriber[T]`'s merge.
- **NEW `events.ConflictingSecurityDeclarationError{Topic, Scheme,
  FirstSource, SecondSource, FirstScopes, SecondScopes}`** — mirrors
  REST's error exactly (`Route` renamed `Topic`, pub/sub's own
  identifier vocabulary).
- Runs EAGERLY at `.Handle(client)`/`.Register(client)` time — the SAME
  validation point as the existing `UnknownMiddlewareImplementationError`
  check (both are "catch a declaration-time mistake before the handle
  is ever used" checks).
- **The merged result — not either source picked ad hoc — is what
  actually feeds `route.SecurityRequirement` for spec rendering AND
  `CheckCoverage`.** This resolves the previously-ambiguous "which
  source wins" question completely: it is never a silent pick, always
  an explicit, validated merge (or a hard error on genuine conflict).

**A later review found this fix, as originally written, was
INCOMPLETE: it never specified what populates `ChannelHandle.SecuritySchemes`
after `WithSecurityScheme`'s removal.** `AsyncAPISpec()` aggregates
`components.securitySchemes` from `ChannelHandle.SecuritySchemes`
(`map[string]SecurityScheme`) — TODAY (pre-redesign), this map is
populated EXCLUSIVELY by `WithSecurityScheme`'s `applyChannel`. Removing
`WithSecurityScheme` (above) without replacing THIS specific
responsibility would leave `ChannelHandle.SecuritySchemes` PERMANENTLY
EMPTY, silently rendering `components.securitySchemes` empty even for
channels declaring security — a real spec-rendering regression that the
original write-back missed.

**Confirmed via code: REST's `applySecurityDeclarations` ALREADY does
double duty** — it merges the REQUIREMENT (above) AND SEPARATELY
populates `rb.securitySchemes[schemeName] = SecurityScheme{Scheme:
mw.Security.Scheme, Codec: mw.Security.Codec}` for each `.Use()`-attached
middleware, using `if _, exists := ...; !exists` (FIRST-registered-wins
WITHIN one route's own merge loop, when multiple middlewares are
attached to the SAME route). **Fix: the SAME per-role merge function
above ALSO populates `ChannelHandle.SecuritySchemes`, identically.**
This is not a NEW mechanism — it is the missing HALF of the mechanism
already being ported.

**Two DISTINCT, COEXISTING "wins" policies, stated precisely to avoid
conflating them:**
1. **FIRST-registered-wins WITHIN one role's own merge** — if a SINGLE
   `Subscriber[T]` (or `Publisher[T]`) has MULTIPLE `.Use()`-attached
   middlewares naming the SAME scheme, the FIRST one's `Scheme`/`Codec`
   populates `SecuritySchemes`; later ones are silently ignored FOR
   METADATA PURPOSES ONLY (their SCOPES still participate in the
   requirement merge above, and a scope MISMATCH still triggers
   `ConflictingSecurityDeclarationError` — only the scheme METADATA
   itself uses first-wins, mirroring REST exactly).
2. **LAST-registered-wins ACROSS channels sharing a `Client`** —
   escape hatch #7, UNCHANGED: if TWO DIFFERENT channels declare the
   SAME scheme name with DIFFERENT metadata, `AsyncAPISpec()`'s
   aggregation (across `b.entries`, not within one role's merge) uses
   last-registered-wins, silently, no error.

**Confirmed: pub/sub needs only ONE fallible merge function, NOT REST's
two-function split.** REST has TWO separate functions —
`applySecurityDeclarations` (fallible, conflict-checked, called from
`Register`) and `applyMiddlewareSecurityForClient` (INFALLIBLE, no
conflict check, called from `ClientHandle`) — because REST's
`ClientHandle()` MUST stay infallible (no error return at all). Pub/sub's
`.Handle(client)` is fallible UNIVERSALLY, even with `client == nil`
(an ALREADY-LOCKED-IN earlier decision — the deliberate fix for
`ClientHandle()`'s old silent-panic-on-format-mismatch wart, see "The
resolved shape" above). Since `.Handle()` can ALWAYS return an error,
ONE fallible merge function (same name/error-type/logic as REST's
`applySecurityDeclarations`, INCLUDING the `SecuritySchemes` population)
suffices for BOTH nil and non-nil `client` — REST's infallible variant
is unnecessary duplication for pub/sub's simpler, universally-fallible
shape.

**Terminal accessor — builds the runtime handle:**

```go
func (s Subscriber[T]) Handle(client *Client) (*ChannelHandle[T], error)
func (p Publisher[T]) Handle(client *Client) (*ChannelHandle[T], error)
```

(Named `.Handle(client)`, not `SubscriberHandle`/`PublisherHandle` — the
receiver type already says the role, so the suffix is redundant now.)

- **`.Handle(client)` runs the COMPLETE validation suite
  UNCONDITIONALLY — regardless of whether `client` is nil or non-nil**
  (found and resolved during a later review of `TopicParam`/param
  declaration, matching, and rendering — see below for the full
  reasoning): topic-param-name-matches-template validation (mirrors
  `codex.ValidateDeclaredParams`, returning the SAME shared
  `codex.InvalidParamError` — no new pub/sub-local error type needed),
  merge-field TYPE checking (via the FALLIBLE `assertMergeFields`,
  NEVER a panicking variant), format-type checking (`FormatOptError`),
  the security-declaration-conflict check
  (`ConflictingSecurityDeclarationError`, see below), and the
  unsupported-middleware-params check (`UnsupportedMiddlewareParamsError`,
  see below) ALL run identically whether `client` is nil or not.
  **`client`'s nilness affects ONLY whether the descriptor is ALSO
  registered into the `Client`'s spec-accumulating registry
  (topic-based dedup, `AsyncAPISpec()` contribution) — NEVER which
  validations run.** This is a pub/sub-specific simplification NOT
  available to REST: checked REST's own `Route.ClientHandle()` and
  confirmed it has the IDENTICAL two panic sites (a `FormatOptError`-
  style panic, AND a SEPARATE `mustAssertMergeFields` panic on
  merge-field type mismatches) and the IDENTICAL topic/path-param-name
  validation gap (`ValidateDeclaredParams` only runs in `Register`,
  never `ClientHandle`) — but REST's OWN doc comment EXPLICITLY
  justifies this: "ClientHandle stays infallible: conflict detection
  and drift-closing coverage checking... do NOT run here." That
  rationale simply does not apply to pub/sub's `.Handle(client)`,
  since it is ALREADY universally fallible (both nil and non-nil
  `client` can always return an error) — there is no infallibility
  constraint left to preserve, so nothing is lost by running every
  check unconditionally. REST cannot adopt this SAME simplification
  without ALSO making its OWN `ClientHandle()` fallible — a separate,
  much bigger breaking change, explicitly out of scope here.
- `client` non-nil → look up `client`'s internal entry bookkeeping for
  this channel's topic (in ADDITION to the unconditional validation
  above):
  - **Miss:** build the full descriptor/security-schemes/formats/
    merge-fields from the `Subscriber[T]`/`Publisher[T]`'s underlying
    `Channel[T]` declaration (same as today's `Register`), append a NEW
    spec entry recording this descriptor-level info (topic + a `T`
    witness, for future dedup lookups against the SAME topic).
  - **Hit, same `T`:** the topic's descriptor-level info is ALREADY
    registered — spec dedup achieved (no new spec entry appended,
    avoiding a duplicate AsyncAPI channel item when the same client
    plays both roles for one channel). Reuse the FIRST call's stored
    descriptor-level info — first-registered-wins for descriptor/
    formats/merge-fields on topic reuse (a caller passing a `Channel`
    value with genuinely DIFFERENT declared opts for the same
    topic+client on a later call is a documented escape hatch, not
    detected/errored — mirrors this codebase's existing
    "last-registered-wins on `WithSecurityScheme` name collisions"
    family of policies).
  - **Hit, different `T`:** the stored entry's type-assertion fails —
    return a NEW `ChannelTypeConflictError{Topic, Want, Got}` (a topic
    reused with an incompatible payload type is a genuine caller error,
    not an escape hatch).
  - In EVERY case (miss, same-`T` hit, or the error path), the call
    returns its OWN freshly-built, independent `*ChannelHandle[T]` —
    **no handle object is ever shared or mutated in place across calls**
    (an earlier draft mutated a shared pointer in the "hit" case;
    critical review found this an unsynchronized concurrent
    read/write hazard — a caller using a handle from one call
    concurrently, before a later call mutates that SAME pointer's other
    role field, is a data race. Returning a fresh, immutable-after-
    construction handle from every call removes the hazard entirely;
    only the CLIENT's internal spec-entry bookkeeping dedups by topic,
    never a handle object).
  - `Subscriber[T].Handle` populates `Implementations` (from
    `SubscribeMW`-attached impls) ONLY; `Publisher[T].Handle` populates
    `ClientImplementations` (from `PublishMW`-attached impls) ONLY.
    Neither ever populates the other field.
- **Confirmed, not a finding: `TopicParam`/`MergedTopicParam` are
  IDENTICAL across both roles by construction.** `TopicParam`s are
  `ChannelOpt`s applied via `c.opts` on `Channel[T]` itself — BEFORE
  `WithSubscribe`/`WithPublish` fork into `Subscriber[T]`/`Publisher[T]`.
  Since both role-scoped builders wrap the SAME underlying `Channel[T]`
  value, the declared topic params/merge-fields (and their spec
  rendering, a pure function of `topic`+`topicParams`) are IDENTICAL
  for both roles — no possible divergence between what
  `Subscriber[T].Handle`/`Publisher[T].Handle` see for the SAME
  channel. Checked and confirmed during a dedicated review of
  `TopicParam` declaration/matching/rendering; no design change needed
  here.
- **Confirmed, not a finding: format flexibility (GOB, templ/HTML,
  ANY custom `format.Format[T]`) is FULLY PRESERVED and UNAFFECTED by
  this redesign, for BOTH roles.** `Formats`/`SubscribeFormats`/
  `PublishFormats` are unchanged `ChannelOpt`s, carried through the
  "Miss" case above exactly like every other descriptor field — the
  Client-centric role split changes NOTHING about how formats are
  declared or applied. `format.Format[T]` itself is a general,
  transport-agnostic struct (`format.Gob`, `format.NewTyped`,
  `adapters/templ.Format`, etc.) — confirmed via code that
  `format.NewTyped`'s OWN doc comment already cites templ/HTML
  rendering as a first-class use case, and `examples/events-nested-binary`
  ALREADY demonstrates a custom GOB-style `format.NewTyped` format
  composing with topic merge fields over `adapters/mqtt5` today.
  `adapters/templ.Format[Props]` returns a plain
  `format.Format[Props]` — usable via `.WithPublishFormats(templ.Format(...))`
  with ZERO adapter changes (HTML rendering is naturally
  PUBLISH-ONLY, matching `NewTyped`'s own "HTML is not decodable"
  convention — the same one-way-format pattern REST already
  established). Also confirmed: pub/sub adapters correctly NEVER call
  `Format.MarshalTo`/`IsStreamable` (REST's streaming-format support)
  — NOT a gap, since MQTT/ZeroMQ publish one discrete message payload
  per call, so there is no "avoid buffering a large HTTP response
  body" use case to serve; the full encoded bytes are needed as the
  payload frame regardless of format. No design change needed here.
- **Eager validation:** `Subscriber[T].Handle` runs a
  `checkImplementationsDeclared`-equivalent check — an implementation
  whose `Satisfies` names a scheme never `.Use()`'d is a NEW
  `events.UnknownMiddlewareImplementationError` (mirrors REST/reqreply's
  `UnknownMiddlewareImplementationError` exactly). `Publisher[T].Handle`
  does NOT run an equivalent check — mirrors REST/reqreply's existing
  asymmetry (this eager check only ever validates the verify/subscribe
  side, never the credential/publish side, carried over unchanged, not a
  new gap).
- **`CheckCoverage`-equivalent (signature decided now, wiring deferred):**
  a future
  `events.CheckCoverage(topic string, secReqs []route.SecurityRequirement, impls []middleware.ServerImplementation) error`
  mirroring `reqreply.CheckCoverage`/`rest.CheckCoverage` exactly. Its
  SIGNATURE is fixed by this decision, but actually WIRING it into
  `mqtt5.Subscribe`/`mqtt.Subscribe` (subscribe-side only, same asymmetry
  as above) is explicitly deferred to Decision 3's future adapter-wiring
  implementation pass — not part of this doc's Decision 1 scope.
- `Register(b)` and `ClientHandle()` are **REMOVED** — fully replaced by
  `Subscriber[T].Handle(client)`/`Publisher[T].Handle(client)`. This is a
  breaking change, deliberately accepted: `Implementations`/
  `ClientImplementations` don't exist on `ChannelHandle[T]` at all today
  (confirmed: `api/events` has ZERO `middleware` package integration), so
  there is no shipped behavior for THIS feature to preserve, and
  `Register`/`ClientHandle`'s pre-existing topic/codec/spec-building
  behavior is fully subsumed by the new accessors. Confirmed via
  repo-wide search: **zero examples** call `events.Channel.ClientHandle()`
  today (the `ClientHandle` hits found in `examples/` all belong to
  `reqreply.Route.ClientHandle()`, an unrelated type) — only `api/events`'s
  own test suite needs updating during implementation.

`ChannelHandle[T]` gains `Implementations []middleware.ServerImplementation`
and `ClientImplementations []middleware.ClientImplementation`, populated
per the `Handle` rules above.

**`ports.EventPattern`/`buildEventPatternHandles` gets a real correctness
fix, not just an unaffected pass-through.** Today it ALWAYS calls
`channel.Register(b)` regardless of `SourcePort`(subscribe)/`SinkPort`
(publish) role — under this design it builds a `Subscriber[T]`/
`Publisher[T]` (from the pattern's `Opts`) and calls the ROLE-CORRECT
`.Handle(client)` (subscriber's for `SourcePort`, publisher's for
`SinkPort`), naturally fixing a latent inconsistency: previously a
`SourcePort`, which only ever subscribes, still got BOTH
`Implementations` AND `ClientImplementations` populated on its handle —
harmless today (nothing consumed those fields yet) but incorrect once
adapters start reading them.

#### `middleware.Middleware`'s REST-only fields, rejected eagerly for pub/sub (found during a critical review, INTERIM fix — see spun-out doc for the long-term fix)

A critical review found that `middleware.Middleware` — SHARED, non-
generic, directly imported by `api/rest`, `api/reqreply`, AND `api/events`
— carries `RequestHeaderParams`/`RequestCookieParams`/
`RequestQueryParams`/`ResponseHeaderParams`/`ResponseCookieParams`,
meaningless for pub/sub's topic-only boundary. Since `Subscriber[T].Use()`/
`Publisher[T].Use()` accept the SAME `middleware.Middleware` type REST
uses, nothing in Go's type system stops a caller from accidentally
attaching a REST-oriented middleware value (e.g. one built via
`rest.FromHeaderParam(...)`) directly to a pub/sub channel — a genuine
possible mistake, not just cosmetic risk.

**Fix: eager validation, mirrors this doc's established pattern**
(catch mistakes at declare time, same as
`UnknownMiddlewareImplementationError`/`MissingHandlerError`/
`ConflictingSecurityDeclarationError` above):

- New `events.UnsupportedMiddlewareParamsError{Topic string, Middleware string}`
  — returned if ANY `.Use()`-attached middleware (for EITHER role, NOT
  gated on `Security != nil` — a middleware can carry ONLY param
  contributions with no security at all, per REST's own design) has a
  non-empty `RequestHeaderParams`/`RequestCookieParams`/
  `RequestQueryParams`/`ResponseHeaderParams`/`ResponseCookieParams`.
- Runs at the SAME eager-validation point as the doc's other checks:
  `Subscriber[T].Handle(client)`/`Subscriber[T].Register(client)`/
  `Publisher[T].Handle(client)`.

**This is a deliberately-scoped INTERIM fix, not the final word** — a
follow-up discussion confirmed the underlying issue is BIGGER than
pub/sub: `api/reqreply` (already shipped earlier this session) uses the
SAME flat `middleware.Middleware` type directly too, and reqreply is
ALSO topic-only — so this "REST-only fields leak in unused" pattern is
PRE-EXISTING, not pub/sub-specific, and the PROPER fix (a common-base +
per-API-pattern-derived middleware TYPE hierarchy, making the mistake a
Go COMPILE error instead of a runtime one) means retrofitting REST's
and reqreply's already-shipped code — genuinely out of THIS doc's
scope. See [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md)
for that bigger investigation, spun out rather than folded in here.

#### Why `Subscribe(fn)` itself never became declare-time-only (still true — refined, not reversed, by the next subsection)

An earlier iteration of this decision proposed replacing `Subscribe`'s
call-time `fn` ENTIRELY with
`Subscriber[T].WithHandler(fn func(context.Context, T) error) Subscriber[T]`,
directly mirroring `rest.Route.WithHandler`'s declare-time, chainable
attachment, with `adapters/mqtt5.Subscribe` losing its `fn` param
altogether in favor of reading a new `ChannelHandle[T].Handler` field.
**That REPLACEMENT was rejected on review** — but, per the next
subsection, `WithHandler` itself was subsequently reinstated for a
DIFFERENT purpose, so read this carefully: the conclusion below is about
`Subscribe`'s OWN call-time `fn` staying exactly as shipped, NOT a
blanket rejection of declare-time handler attachment everywhere.

REST's `Serve`/`ServeOne` NEED declare-time attachment because they walk
a whole builder or wrap a single route into a long-lived `http.Handler`
with NO separate "start" call at all — the handler has to already be
there when `Serve` runs. Pub/sub's `Subscribe` call IS ITSELF a "start"
call for ONE channel — imperative: "here's my handler, start consuming
THIS channel now." There is no separate "wire it up" step it's building
toward for that single-channel use case. **`Subscribe`/`SubscribeWithHandle`
keep `fn` as a required call-time param, exactly as shipped today —
this part is UNCHANGED.** `Publisher[T]` never had (or needs) an
equivalent question — confirmed against REST's `Call`, which has no
handler-equivalent either (a one-shot payload-in operation has nothing
to attach a handler to).

#### `Subscriber[T].WithHandler` REINSTATED — for a NEW whole-client `ServeSubscribers`, fully independent of `Subscribe(fn)`

A follow-up question surfaced the piece the rejection above missed:
REST's OWN precedent has EXACTLY the "declare handlers on many routes,
then wire ALL of them with one call" shape too — that is what
`rest.Serve(mux, builder)` does, walking a WHOLE `Builder`'s registered
routes at once. Pub/sub has a directly analogous need that v7's
single-channel `Subscribe`/`SubscribeWithHandle` never served: an
application declaring N channels to subscribe to wants ONE call to start
consuming ALL of them, without manually repeating `Subscribe(...)` N
times. **Resolution: add BOTH capabilities, fully independent, with NO
"which wins" ambiguity — because they never read the same data:**

- `Subscriber[T].WithHandler(fn func(context.Context, T) error) Subscriber[T]` —
  **reinstated**, declare-time, chainable. Populates
  `ChannelHandle[T].Handler` (new field) — consumed **ONLY** by the new
  whole-client `ServeSubscribers` below.
- **New, per adapter** (mirrors `rest.Serve(mux, builder)` exactly —
  confirmed placement: the ADAPTER (`mqtt5`/`mqtt`/`zeromq`, each gets
  its OWN) owns the responsibility of walking `events.Client`'s
  registered entries and starting consumption; the `Client` itself just
  holds the registry, same relationship `rest.Server` has to
  `nethttp.Serve`/`chi.Serve` — two DIFFERENT adapters, ONE shared
  builder). Takes a `*Caller` (see "New `mqtt5.Caller` bundling type"
  below):
  ```go
  func (c *Caller) ServeSubscribers(ctx context.Context) error
  ```
  **Named `ServeSubscribers`, NOT `Serve`** — a naming collision was
  found and fixed before finalizing this decision: `adapters/mqtt5` and
  `adapters/zeromq` ALREADY export a `Serve[Req, Resp any](...)`
  function for `reqreply`'s request/reply pattern (confirmed via code:
  `adapters/mqtt5/reqreply.go`, `adapters/zeromq/adapter.go`) — Go does
  not support function overloading, so the bare name `Serve` was
  unavailable in either package. `ServeSubscribers` avoids the
  collision and applies uniformly across all 3 adapters for consistency
  (only `mqtt5`/`zeromq` strictly needed the rename; `mqtt`(v3) has no
  existing `Serve` since `reqreply` isn't implemented there, but the
  same name is used everywhere regardless).

  Walks every `Subscriber[T]` entry registered against `c`'s
  `*events.Client` via `Subscriber[T].Register(client)` (see "A blocking
  gap" below for why this is a SEPARATE call from `.Handle(client)`,
  and why every entry here is therefore guaranteed to already have a
  `Handler` attached — `Register()` rejects handler-less registration
  eagerly, so the "skip handler-less entries" check mirrors `rest.Serve`'s
  "Part 1" semantics defensively, but is not expected to actually
  trigger in normal use). **Confirmed: one goroutine per subscribe route** (per
  channel, not one per `ServeSubscribers` call) — blocks until `ctx` is
  cancelled or all goroutines exit, collecting/returning errors (this
  specific blocking behavior is the doc's assumed default, not
  separately re-confirmed — adjustable at implementation time if it
  proves wrong).

  **Confirmed explicitly (found undocumented during a critical
  review): `ServeSubscribers` takes a ONE-TIME SNAPSHOT of
  `SubscriberEntries()` at call time.** It does NOT watch the registry
  for later changes — registering a NEW `Subscriber[T]` via
  `Register()` AFTER `ServeSubscribers` has already started does NOT
  get picked up; the caller would need to stop and restart
  `ServeSubscribers`, or call `Subscribe(fn)` directly for that one new
  channel instead. This is the intentional, documented behavior —
  consistent with go-codex's general simplicity bias (no precedent
  elsewhere in this codebase for "watch a registry, dynamically add
  subscriptions at runtime").

  It is a METHOD on `*Caller`, not a free function taking `*Caller` as
  a param — this is DELIBERATE, not a stylistic choice: it lets
  `*Caller` satisfy a new shared interface (see "A shared,
  transport-agnostic `SubscriberServer` interface" below).
- `Subscribe(ctx, caller, sub, qos, fn, opts)` /
  `SubscribeWithHandle(ctx, client, router, handle, qos, fn, opts)` —
  **UNCHANGED IN BEHAVIOR**, per the subsection above (signatures
  revised below to take `*Caller`/raw params respectively). These NEVER
  consult `handle.Handler` — even if the SAME `Subscriber[T]` ALSO has
  `WithHandler` attached (e.g. for use with the whole-client
  `ServeSubscribers` elsewhere), `Subscribe(fn)` ignores it entirely and
  uses only its own explicit `fn` argument.

**NEW — `ServeOneSubscriber`, a zero-ceremony shortcut for
`ServeSubscribers` (found during a critical review, resolved).**
Unlike `Subscribe(fn)` (already minimal — see the "Quick start"
subsection below Decision 1), `ServeSubscribers` genuinely NEEDS a
non-nil `*events.Client` with ≥1 `Register()`-ed `Subscriber[T]` —
there was no "just one subscriber, no separate `Client` ceremony"
shortcut for that specific path, mirroring EXACTLY why REST needed
`nethttp.ServeOne(route)` (its only entry point, `Serve`, requires a
`*rest.Server` with ≥1 registered route). Fix, per adapter:

```go
// mqtt5.ServeOneSubscriber(ctx, client, router, sub) error — mirrors
// nethttp.ServeOne(route) exactly: builds a private, throwaway
// *events.Client internally, calls sub.Register on it, wraps
// client+router+privateClient into a throwaway *Caller, and calls
// ServeSubscribers — "just wire this ONE handler-bearing Subscriber[T]
// and start consuming it" in one call.
func ServeOneSubscriber[T any](ctx context.Context, client MQTTClient, router MQTTRouter, sub Subscriber[T]) error
```

No naming collision (confirmed: no existing `ServeOne*` symbol in
`mqtt5`/`mqtt`/`zeromq`). Same per-adapter mirroring/exceptions already
established for `Caller`/`Subscribe` (`zeromq.ServeOneSubscriber` maps
cleanly onto its existing `sock`-taking shape; `mqtt`(v3)'s equivalent
is part of the SAME genuinely-new higher-level capability that
transport needs overall, not a mechanical mirror).

#### A blocking gap, found and fixed this pass: `ServeSubscribers` needs something to invoke, but `.Handle()` deliberately never retains anything

Tracing through the mechanics surfaced a genuine problem: `rest.Route.Register(builder)`/`Route.ClientHandle()` are STRUCTURALLY
different (`Register` ALWAYS retains a live, mutex-guarded handle for
`Serve` + spec rendering; `ClientHandle` NEVER retains anything, no
builder param at all) — REST never has to ask "does `Serve` have
something to walk" because only `Register`'d entries ever feed `Serve`.
Pub/sub's `Subscriber[T].Handle(client)` collapsed these into ONE
dual-purpose method (a nilable `client` controls both "touch spec?" and,
implicitly, "retain for `ServeSubscribers`?"). But the B2 data-race fix
(see "The resolved shape" above) made EVERY `.Handle()` call return a
FRESH, UNRETAINED object — so a `WithHandler`-configured
`Subscriber[T]`'s built handle (with its `Handler` closure) was never
being stored anywhere. `ServeSubscribers` would have had nothing to
find.

Re-checking the existing dedup-bookkeeping design confirmed it was
ALREADY correctly built to store a DECOUPLED VALUE COPY (topic,
descriptor, `SecuritySchemes` — for `AsyncAPISpec()` rendering), never a
handle pointer — so spec rendering was never at risk. Only
`ServeSubscribers`'s need (an invokable closure) was missing.

**Resolution:** `Client` gains a `sync.RWMutex`-guarded registry
(mirrors `rest.Server`'s own `mu sync.RWMutex`, confirmed via code)
with TWO DECOUPLED SLOTS per topic:

1. The EXISTING plain-value spec copy (unchanged, feeds
   `AsyncAPISpec()`).
2. A REPLACEABLE REFERENCE to the latest `Subscriber[T]`-handle-with-
   `Handler` (feeds `ServeSubscribers`).

**A critical follow-up review found a real bug in an earlier version of
this resolution, now fixed: `.Handle(client)` must NEVER touch slot 2 at
all.** The original draft had `.Handle(client)` update BOTH slots
whenever `client` was non-nil — but `.Handle(client)` is ALSO the exact
call the value-based `Subscribe(fn)` convenience makes internally to
build ITS OWN handle (per the two-tier design below), and that call's
`Subscriber[T]` normally has NO `Handler` attached (per "Why
`Subscribe(fn)` itself never became declare-time-only" above — the
handler stays call-time for THAT path). Under the original "last-
registered-wins on both slots" policy, calling `Subscribe(fn)` for a
channel that ALSO had a `ServeSubscribers`-registered `Handler` would
SILENTLY OVERWRITE slot 2 with a `Handler == nil` handle — clobbering
the registration with no error, no warning. This directly contradicted
this very decision's own "two fully independent paths" claim: the two
paths don't READ each other's data, but under the original design they
DID write to the same slot, and the last writer won silently.

**Fixed via full separation — `.Handle()` and slot 2 registration are
now two SEPARATE calls, touching completely disjoint state:**

```go
// .Handle(client) — UNCHANGED from "The resolved shape" above, touches
// ONLY slot 1 (spec descriptor dedup). NEVER writes to slot 2, no
// matter how many times or with what Subscriber[T] value it's called.
// This is what Subscribe(fn)/SubscribeWithHandle(fn)/Publisher use —
// genuinely independent of ServeSubscribers now, zero shared state.
func (s Subscriber[T]) Handle(client *Client) (*ChannelHandle[T], error)

// NEW — the ONLY way to populate slot 2. Requires s's underlying
// ChannelHandle to have Handler != nil (built via WithHandler) — a
// handler-less call is a caller mistake, caught EAGERLY via a NEW
// events.MissingHandlerError{Topic}, never silently accepted or
// silently ignored. Also contributes to slot 1 (same topic-based
// dedup .Handle() already performs) — so a caller wiring a channel for
// ServeSubscribers calls ONLY Register, never needing a separate
// .Handle() call too.
func (s Subscriber[T]) Register(client *Client) error
```

`Register` is a DELIBERATE name reuse — not a revival of the OLD,
REMOVED `Register(b) error` from earlier in this doc (which conflated
topic/codec/spec-building + security-implementation population + spec
registration all in one call). This NEW `Register` is narrower and
precisely scoped to ONE purpose: "attach a handler-bearing entry for
whole-client `ServeSubscribers` consumption" — directly analogous to
REST's OWN `Route.Register(builder)`'s role feeding `Serve`, just for
pub/sub's `ServeSubscribers` instead. Slot 2's own last-registered-wins
policy (mirrors escape hatch #7's existing policy) now applies ONLY
among repeated `Register()` calls for the SAME topic+client — it is
NEVER affected by any `.Handle()` call, closing the bug above
completely. (`Publisher[T]` gets NO equivalent `Register` method — it
has nothing analogous to feed `ServeSubscribers`, mirrors the existing
"no `WithHandler` on `Publisher[T]`" asymmetry.)

**Explicit non-goal:** there is no way to UN-register a previously-
registered entry from slot 2 — a `Register()` call with `Handler == nil`
is a hard error (`MissingHandlerError`), never a silent no-op and never
a "clear" operation. Deferred until a concrete use case for explicit
un-registration appears.

#### `Client.SubscriberEntries()` + `ServeSubscribers`'s generic dispatch mechanism — mirrors REST's OWN existing `HandlerOpts`/`WithOptions` pattern exactly

Two more pieces were previously believed to be UNSOLVED, harder-than-
REST design questions. Both turned out to already have a working
precedent in REST's own shipped code:

- **Entries accessor** — new sealed interface, mirrors `rest.RouteEntry`
  exactly (unexported marker method seals it against external
  implementations, same technique):
  ```go
  type SubscriberEntry interface {
      Topic() string
      HasHandler() bool
      Handle() any // *events.ChannelHandle[T], erased
      isSubscriberEntry()
  }
  func (c *Client) SubscriberEntries() []SubscriberEntry
  ```
  Backed by slot 2 of the mutex-guarded registry above (`c.mu.RLock()`
  around the read — mirrors `rest.Server.RouteEntries()` exactly) —
  populated EXCLUSIVELY by `Subscriber[T].Register(client)` calls, never
  by `.Handle(client)` calls (see the fixed bug above): every entry
  `SubscriberEntries()` returns is therefore guaranteed to have
  `HasHandler() == true` by construction (a handler-less `Register()`
  call is rejected eagerly, never reaches the registry at all) — so
  `ServeSubscribers`'s "skip `!HasHandler()` entries" check below is a
  defensive belt-and-braces guard, not something expected to ever
  actually trigger in practice.
- **Per-channel adapter options (e.g. QoS), previously believed
  impossible from inside a non-generic `ServeSubscribers`** — confirmed
  via code that REST ALREADY solved this EXACT problem: `RouteHandle`/
  `SSERouteHandle` carry `HandlerOpts any` (type-erased), settable via
  `rest.Route.WithOptions(opts)`, read back via
  `nethttp.resolveOptions` (`handlerOpts.(Options)` type assertion,
  `OptionsShapeError` on shape mismatch, nil means "adapter zero-value
  options apply"). `ChannelHandle[T]` gains the IDENTICAL field:
  `HandlerOpts any`. `Subscriber[T]` gains `WithOptions(opts any)
  Subscriber[T]` (or an mqtt5-specific typed convenience wrapping it) to
  declare per-channel adapter options at declare time — e.g.
  `sub.WithOptions(mqtt5.SubscribeAdapterOptions{QoS: 2})`.
- **`ServeSubscribers`'s dispatch mechanism** — mirrors REST's OWN
  "Decision: `Serve`'s generic dispatch mechanism" EXACTLY: `reflect`
  isolated ENTIRELY inside each adapter (`mqtt5`/`mqtt`/`zeromq`), ZERO
  `reflect` dependency in `api/events`. Each adapter's
  `ServeSubscribers` walks `caller.events.SubscriberEntries()`, skips
  `!HasHandler()` entries, and dispatches each via `reflect.Value.Call`
  against the handle's exported closures (`Decode`, `Handler`) —
  matching a concrete `Fn`'s dynamic type at call time, per-entry
  `HandlerOpts` type-asserted to the adapter's own concrete options
  type exactly like `resolveOptions` does. This mirrors REST's
  precedent so closely that pub/sub's dispatch problem is NOT actually
  harder than REST's, contrary to an earlier assumption in this doc —
  it needed the SAME solution REST already shipped, not a new one.
  Per REST's own precedent (`nethttp.Serve`/`chi.Serve` are two
  INDEPENDENT implementations of the identical PATTERN, never shared
  code), this dispatch loop is DUPLICATED per adapter — not centralized
  — since the underlying "subscribe on the wire" step is irreducibly
  different per transport (confirmed via code: `zeromq.FramedSocket`,
  `mqtt5.MQTTClient`, and `mqtt`(v3)'s `pahomqtt.Client` share ZERO
  common interface).

#### A shared, transport-agnostic `SubscriberServer` interface (NEW, confirmed to add)

A follow-up question asked whether application code should be able to
write ONE generic "start consuming" call across transports, without
caring whether mqtt5/mqtt3/zeromq is underneath — matching go-codex's
foundational goal of transport-agnostic declarative workflows.
**Confirmed: yes, add a small shared interface:**

```go
// Lives in api/events — transport-agnostic, zero adapter imports,
// mirrors how io.Reader/io.Writer live in io: a neutral location every
// adapter already depends on, avoiding circular imports.
type SubscriberServer interface {
    ServeSubscribers(ctx context.Context) error
}
```

Each adapter's `*Caller` type (`mqtt5.Caller`, `mqtt.Caller`,
`zeromq.Caller`) implements this via its own `ServeSubscribers` method
(Go structural typing — no explicit "implements" declaration needed,
though a compile-time assertion var, e.g.
`var _ events.SubscriberServer = (*mqtt5.Caller)(nil)`, is good practice
and should be added in each adapter package). **Consequence:**
adapter-specific `SubscribeOptions` moves from a per-call param to a
`Caller`-LEVEL field, so `ServeSubscribers`'s signature stays EXACTLY
`ctx`-only across all 3 adapters and satisfies the shared interface
uniformly — e.g. `Caller` gains a chainable
`.WithOptions(opts SubscribeOptions) *Caller` (mirrors
`nethttp.Caller.WithBaseURL`'s chainable-rebuild ergonomics), or
`NewCaller` accepts it as a functional option. The exact mechanism
(chainable method vs. functional option) is not fully locked this
pass — flagged for implementation time, does not affect the interface
shape itself.

**Whether REST/`nethttp`/`chi` should adopt an analogous shared
interface for `Serve` is a SEPARATE, NOT YET RESOLVED question** — see
[d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary),
spun out to its own doc after finding a real shape mismatch (REST's
`Serve` wires-and-returns immediately; pub/sub's `ServeSubscribers`
blocks-and-runs) that needs its own investigation, not a drop-in mirror
of this decision.

**Scope of "transport-agnostic," made explicit (found undocumented
during a critical review, confirmed correct as-is):** `SubscriberServer`
(and `PublisherClient[T]` below) only provide agnosticism AFTER
construction — building a `Caller` at all remains fully
adapter-specific (`mqtt5.NewCaller` vs. `mqtt.NewCaller` vs.
`zeromq.NewCaller`). This mirrors `io.Reader`'s own idiom exactly (you
still need `os.Open`/`bytes.NewReader`, etc. to GET an `io.Reader` in
the first place) and is the correct, expected scope — NOT a
shortcoming — but was worth stating plainly so a reader doesn't
over-claim what "transport-agnostic" covers here: it describes the
CONSUMING code's relationship to an already-built `Caller`, never the
construction step itself.

#### A matching `events.PublisherClient[T]` interface — completing the transport-agnostic symmetry (NEW, found during a goal-alignment review, confirmed to add)

A later review against this doc's own two headline goals (simple
declarative workflow; transport/protocol-agnostic abstraction) found
that `SubscriberServer` above only closes HALF of the transport-
agnosticism goal — the SUBSCRIBE side. The PUBLISH side had NO
equivalent: a caller wanting to write transport-agnostic "publish `T`
to this channel" code still had to know which adapter's
`Publish[T any](ctx, client, handle, ...)` free function to call
directly, with no unifying abstraction. Since `Publish` is already
generic and handle-based (`func Publish[T any](ctx context.Context,
client MQTTClient, handle *events.ChannelHandle[T], qos byte, retained
bool, msg T, vars map[string]string, opts PublishOptions, formats
...format.Format[T]) error`, confirmed via code), a parallel interface
is tractable. **Confirmed: add it, completing the symmetry:**

```go
// Lives in api/events, alongside SubscriberServer — same rationale
// (neutral, transport-agnostic location, zero adapter imports). Named
// PublisherClient, NOT Publisher — events.Publisher[T] is ALREADY the
// role-scoped BUILDER struct from Channel.WithPublish (Decision 1); a
// naming collision was caught and fixed before finalizing this
// decision (Go does not allow two types named Publisher[T] in the same
// package). PublisherClient emphasizes this is a bound RUNTIME client
// object, distinct from the declare-time builder.
type PublisherClient[T any] interface {
    Publish(ctx context.Context, msg T) error
}
```

Unlike `SubscriberServer` (implemented directly by `*Caller`, since
`ServeSubscribers` needs nothing beyond `ctx`), `Publish[T any]`'s free
function needs `client`+`handle`+`qos`+`retained`+`vars`+`opts`+
`formats` to actually publish — a SMALL BINDING TYPE per adapter is
needed to capture these at construction time and satisfy the interface
with just `(ctx, msg)`:

```go
// mqtt5.PublisherFor[T] binds a *Caller + a Publisher[T]-built
// *events.ChannelHandle[T] + QoS/retained/opts/formats together —
// constructed ONCE, reused for repeated Publish calls, mirroring
// Publish's own "build the handle once, call many times" efficiency
// shape (unchanged from the two-tier design above).
type PublisherFor[T any] struct { /* caller, handle, qos, retained, opts, formats */ }

func NewPublisherFor[T any](caller *Caller, handle *events.ChannelHandle[T],
    qos byte, retained bool, opts PublishOptions, formats ...format.Format[T],
) *PublisherFor[T]

func (p *PublisherFor[T]) Publish(ctx context.Context, msg T) error
```

**Known limitation, flagged explicitly rather than silently accepted:**
`Publish`'s `vars map[string]string` (topic template variable
substitution, e.g. `"sensors/{sensorID}/data"`) is NOT expressible
through the interface's `(ctx, msg)`-only shape — `vars` must be baked
in at `PublisherFor[T]` construction time, meaning ONE `PublisherFor[T]`
instance per CONCRETE topic (e.g. one per sensor ID), not one shared
instance for an entire parameterized topic template. This mirrors an
EXISTING, already-documented escape hatch elsewhere in this codebase
(`DrainPublish`'s "static `Vars` — same map applied to every item;
per-item topic var substitution requires `stream.Drain` + `Publish`
directly") — not a new kind of limitation, the SAME one, applied here
for the SAME reason (per-call topic variability isn't expressible
through a fixed binding). A caller needing genuinely per-call topic
variability should call the adapter's `Publish` free function directly,
bypassing `PublisherClient[T]`/`PublisherFor[T]` entirely — same escape
hatch as `DrainPublish` already documents.

#### New `mqtt5.Caller` bundling type — mirrors `nethttp.Caller`'s param-reduction value, WITHOUT a `WithBaseURL` equivalent

A follow-up question asked whether `events.Client` should get a
`WithBaseURL`-equivalent mirroring `nethttp.Caller.WithBaseURL`.
Investigation surfaced a real structural mismatch, worth recording
explicitly rather than silently deciding against it: **`Caller.WithBaseURL`'s
actual value is reusing the SAME `*http.Client` (connection pool) while
cheaply swapping the target host string** — Go's `*http.Client` is
transport/connection-pool-agnostic to the target host per request.
**MQTT has no equivalent, confirmed via `adapters/mqtt5.MQTTClient`'s
interface**: an `MQTTClient` wraps an ALREADY-CONNECTED `pahomqtt5.Client`,
bound to exactly ONE broker at construction (TCP/TLS handshake already
done) — there is no shared, reusable "transport" independent of the
broker target the way `*http.Client` is independent of the target host;
rebasing to a different broker requires an entirely NEW connection, not
a cheap string swap. **A literal `WithBaseURL`/rebase method is
therefore NOT introduced** — it would just be a disguised constructor
call with no real ergonomic win, since there is no expensive shared
resource to preserve across a "rebase." Constructing a brand-new
`Caller` via `NewCaller` is the only way to target a different broker —
exactly as costly as it should be.

**What DOES carry over from `nethttp.Caller`: its OTHER real value —
bundling repeated call-site parameters into one reusable value.**
Without this bundling, BOTH the whole-client `ServeSubscribers`
(previous subsections) and the per-channel `Subscribe` (next subsection)
would each repeat THREE params on every call: `client MQTTClient`,
`router MQTTRouter`, and `eventsClient *events.Client`. `mqtt5.Caller` bundles
exactly these three — mirroring `nethttp.Caller`'s `client`+`baseURL`
bundling shape, adapted to pub/sub's three repeated params instead of
REST's two:

```go
// mqtt5.Caller — lives in adapters/mqtt5 (NOT api/events — mirrors
// nethttp.Caller living in adapters/nethttp, not api/rest).
type Caller struct {
    client MQTTClient
    router MQTTRouter
    events *events.Client
}

// eventsClient IS NILABLE (confirmed explicitly — found undocumented
// during a critical review): pass nil for a Caller that never touches
// the AsyncAPI spec at all, mirroring Subscriber[T].Handle(client)'s
// own already-documented "client optional, nil = spec-free" contract.
// This is the SAME pattern the "Quick start" comparison below Decision
// 1 and ServeOneSubscriber (below) both already rely on.
func NewCaller(client MQTTClient, router MQTTRouter, eventsClient *events.Client) *Caller
```

Applies to the value-based tier ONLY — the handle-based primitive stays
on raw params, mirroring `CallWithHandle` staying on raw `client`+
`baseURL` rather than taking a `*Caller`:

```go
// Value-based convenience — takes *Caller:
func Subscribe[T any](ctx context.Context, caller *Caller,
    sub events.Subscriber[T], qos byte, fn func(context.Context, T) error,
    opts SubscribeOptions, formats ...format.Format[T]) error

// Handle-based primitive — stays on RAW client+router, no *Caller, no
// eventsClient (a pre-built handle has no spec dependency at all):
func SubscribeWithHandle[T any](ctx context.Context, client MQTTClient,
    router MQTTRouter, handle *events.ChannelHandle[T], qos byte,
    fn func(context.Context, T) error, opts SubscribeOptions,
    formats ...format.Format[T]) error

// ServeSubscribers has no handle-based variant (it walks MANY entries,
// no single handle to speak of) — it is a METHOD on *Caller (not a free
// function taking *Caller as a param), so *Caller can satisfy
// events.SubscriberServer:
func (c *Caller) ServeSubscribers(ctx context.Context) error
```

`Caller` also gains a way to carry adapter-specific `SubscribeOptions`
at the `Caller` level (mechanism TBD — chainable `.WithOptions(opts)`
vs. a `NewCaller` functional option, see "A shared, transport-agnostic
`SubscriberServer` interface" above) so `ServeSubscribers`'s signature
can stay EXACTLY `ctx`-only and satisfy `events.SubscriberServer`
uniformly.

`Publish`/`Publisher[T].Handle` are **UNCHANGED** — `Publish` never
repeated `eventsClient` at the adapter call site to begin with (it
already takes a pre-built `handle` directly, per the two-tier
subsection's "no new tier needed" resolution below), so there is
nothing for `Caller` to usefully bundle there; introducing `Caller` for
`Publish` is not proposed.

**`Caller`'s mirroring across `mqtt`(v3)/`zeromq` — CORRECTED this
pass, checked against actual code rather than assumed:**

- **`zeromq` — original claim CONFIRMED accurate.** `adapters/zeromq`'s
  `Subscribe[T any](ctx, sock FramedSocket, handle, fn, opts,
  formats...)`/`Publish[T any](ctx, sock FramedSocket, handle, msg,
  ...)` ALREADY take a single client-like `sock FramedSocket` param (no
  router-equivalent, but zeromq genuinely has none — `sock` already
  plays the "client" role directly). `zeromq.Caller{sock FramedSocket,
  events *events.Client}` (2 fields, no router field) bundles this
  cleanly, and the two-tier `Subscribe`(value-based,
  takes `*Caller`)/`SubscribeWithHandle`(handle-based, raw
  `sock`+`handle`) split maps directly onto zeromq's EXISTING shape —
  no new capability needed.
- **`mqtt`(v3) — ORIGINAL CLAIM WAS WRONG, corrected here.** Checked
  `adapters/mqtt/adapter.go`: it has NO router concept at all (zero
  hits for `Router`/`AddRoute`/`RegisterHandler`), and its
  `SubscribeHandler(ctx, handle, fn, opts, formats...)` is a
  fundamentally LOWER-LEVEL shape than `mqtt5.Subscribe` — it takes NO
  `client`, NO `router` at all; it returns a bare
  `pahomqtt.MessageHandler` closure, and the caller wires it into their
  own connection however they see fit, entirely outside this package
  (confirmed via `examples/adapters-mqtt-security/main.go`'s actual
  usage pattern — the caller calls the returned closure directly).
  There is NO bare `Subscribe` function to split into two tiers, and NO
  router to bundle into a `Caller`. **Given the user's guiding
  principle — swapping which `Caller`/broker connection a DECLARED
  `Subscriber[T]` uses should be easy, without touching the
  declaration, UNIFORMLY across transports** — `mqtt`(v3) needs a
  GENUINELY NEW, higher-level capability, not a mechanical rename:
  - `mqtt.Caller{client pahomqtt.Client, events *events.Client}` — NO
    router field (v3 genuinely has none).
  - NEW `mqtt.Subscribe(ctx, caller, sub, qos, fn, opts)` — internally
    does what today's callers currently do BY HAND (build the
    `SubscribeHandler` closure via the EXISTING primitive, then wire it
    into `caller.client` via v3's own subscribe mechanism), achieving
    the SAME swappable-`Caller` workflow uniformly with mqtt5, even
    though the underlying wiring mechanics differ.
  - Existing `mqtt.SubscribeHandler` STAYS UNCHANGED as the low-level
    primitive — mirrors how `mqtt5.SubscribeWithHandle` is the
    lower-level path beneath `mqtt5.Subscribe`.
  - This is genuinely NEW capability for `mqtt`(v3), not a mechanical
    rename — deferred to the future adapter-wiring implementation pass
    (exactly how `SubscribeHandler`'s closure gets registered against
    `caller.client` depends on which v3 MQTT library method that
    requires, TBD at implementation time).

#### Two-tier `Subscribe` API — where middleware-carrying values meet the imperative call (NEW this pass)

Rejecting `WithHandler` above raised a natural follow-up: middleware
(`Use`/`SubscribeMW`/`PublishMW`) still lives on the `Subscriber[T]`/
`Publisher[T]` VALUE (declare time, before any handle exists), while
`Subscribe`/`Publish` are call-time/imperative — so where does the
value-to-handle conversion happen, and must the caller do it as an
explicit separate step? Traced against REST's OWN precedent for this
EXACT tension: `nethttp.Call(ctx, caller, route, req, opts)` takes the
`Route` VALUE directly (with `ClientMW`-attached impls already on it) and
internally calls `route.ClientHandle()` for the caller — a one-shot,
value-based convenience — while `nethttp.CallWithHandle(ctx, client,
baseURL, handle, req, opts)` is the lower-level, handle-based primitive
for repeated calls / `ports` usage (build the handle once, call many
times — confirmed via its own doc comment). **The same two-tier split
applies to pub/sub, adapted asymmetrically** since `Publish` (called
per-message, frequently) and `Subscribe` (called once, starts a
long-running loop) have different "how often is this called" profiles:

- **`Publish` — unchanged, no new tier needed.** The EXISTING
  handle-based signature
  `Publish(ctx, client, handle *ChannelHandle[T], qos, retained, msg T, vars, opts, formats...)`
  is ALREADY the efficient "build the handle once (via
  `Publisher[T].Handle(client)`), call many times (once per outgoing
  message)" shape — confirmed this is the right shape for a frequently-
  called operation; a value-based convenience wrapper is not essential
  here (may be added later as pure sugar, not required by this doc).
- **`Subscribe` — gains a NEW value-based convenience tier**, mirroring
  `nethttp.Call` taking a `Route` value directly. **Signature revised
  by the `mqtt5.Caller` finding above** — takes `*Caller` (bundling
  `client`+`router`+`eventsClient`) instead of those 3 params
  separately:

  ```go
  // NEW — value-based convenience. Builds the handle internally via
  // sub.Handle(caller.events) (caller's *events.Client MAY be nil for a
  // spec-free handle — the common case for a typical application
  // subscribing without also registering a spec), then behaves
  // identically to the handle-based form below. fn is STILL a
  // call-time param, unchanged from today (see the rejected-WithHandler
  // subsection above) — matches the imperative "here's my handler,
  // start consuming now" mental model exactly.
  func Subscribe[T any](
      ctx context.Context,
      caller *Caller,
      sub events.Subscriber[T],
      qos byte,
      fn func(context.Context, T) error,
      opts SubscribeOptions,
      formats ...format.Format[T],
  ) error

  // EXISTING shape — handle-based primitive, kept as the lower-level
  // path on RAW params (NOT *Caller — mirrors CallWithHandle staying on
  // raw client+baseURL; used directly by ports/SubscribeAdapter-style
  // repeated/advanced callers who already own a pre-built handle;
  // confirmed via code that ports' SubscribeAdapter does NOT call this
  // exported function at all today — it calls a lower-level primitive
  // directly — so this signature staying available has ZERO impact on
  // ports regardless of which tier is added above it).
  func SubscribeWithHandle[T any](
      ctx context.Context,
      client MQTTClient,
      router MQTTRouter,
      handle *events.ChannelHandle[T],
      qos byte,
      fn func(context.Context, T) error,
      opts SubscribeOptions,
      formats ...format.Format[T],
  ) error
  ```

  **Naming LOCKED IN this pass** (previously TBD): the value-based
  convenience keeps the bare name `Subscribe` (mirroring REST's `Call`
  keeping the short name while the handle-based primitive gets a longer
  name, `CallWithHandle`) — `Subscribe`/`SubscribeWithHandle`, matching
  REST's own convention exactly, no longer flagged as undecided. This IS
  a source-breaking rename for EXISTING callers of today's handle-based
  `Subscribe` — they must update to call `SubscribeWithHandle` instead
  to keep their exact current behavior, or migrate to the new
  value-based form. `adapters/zeromq`'s `Subscribe` gets the SAME
  two-tier treatment (confirmed, its existing shape maps cleanly — see
  "New `mqtt5.Caller` bundling type" above); `adapters/mqtt`(v3) does
  NOT get a mechanical rename — it gets a genuinely NEW, higher-level
  `Subscribe` function instead (see the same subsection's corrected
  mqtt(v3) finding).

#### Quick start — does the simple case stay simple? (found during a critical review; documentation-only fix)

A critical review flagged the sheer VOLUME of new vocabulary this
decision introduces — tallied at 20+ new symbols across `Subscriber[T]`,
`Publisher[T]`, `Use`, `SubscribeMW`, `PublishMW`, `WithHandler`,
`WithOptions`, `Handle`, `Register`, `Caller`, `NewCaller`,
`ServeSubscribers`, `SubscriberServer`, `PublisherClient[T]`,
`PublisherFor[T]`, `SubscriberEntries`, `CheckCoverage`,
`FromSecurityScheme`, and 4 new error types — worth checking
concretely against this doc's OWN "simple, declarative" goal rather
than left as an abstract concern.

**The actual minimal case (no security, single channel, imperative
consume) — OLD (pre-redesign, shipped today) vs. NEW (this decision):**

```go
// OLD:
var SensorReadings = events.NewChannel[Reading]("sensors/data", readingCodec)
handle, err := SensorReadings.Register(b)  // or .ClientHandle() for spec-free
err = mqtt5.Subscribe(ctx, client, router, handle, qos, fn, opts)

// NEW:
var SensorReadings = events.NewChannel[Reading]("sensors/data", readingCodec)
sub := SensorReadings.WithSubscribe(events.Subscribe{})
caller := mqtt5.NewCaller(client, router, nil)  // nil = no spec
err := mqtt5.Subscribe(ctx, caller, sub, qos, fn, opts)
```

**Net delta for the actual simple case: +1 step** (`NewCaller` — a
one-time, reusable bundling of `client`+`router`+`eventsClient`, not a
per-call cost). The 20+-symbol tally above is real, but the OVERWHELMING
majority of it is OPT-IN, never touched by this minimal path:
`Use`/`SubscribeMW`/`PublishMW`/`FromSecurityScheme`/
`ConflictingSecurityDeclarationError` (security, opt-in);
`WithHandler`/`Register`/`ServeSubscribers`/`SubscriberServer`/
`SubscriberEntries` (the whole-client consume-many-channels-at-once
path, opt-in — the imperative single-channel `Subscribe(fn)` shown
above never touches any of it); `WithOptions`/`HandlerOpts` (per-channel
adapter options, opt-in, defaults apply when unset);
`PublisherClient[T]`/`PublisherFor[T]` (transport-agnostic publish
abstraction, opt-in — a caller happy calling `mqtt5.Publish(...)`
directly never needs either). The doc's "simple, declarative" goal
holds for the case that matters most — the common path — even though
the FULL vocabulary surface genuinely did grow to cover every
additional capability this decision added along the way.

### Migration checklist (blast radius of this decision)

- **No new error type needed for the unconditional-validation
  principle** (confirmed during the `TopicParam` review): the
  topic-param-name check reuses the EXISTING, ALREADY-SHARED
  `codex.InvalidParamError` (same type `codex.ValidateDeclaredParams`
  already returns) — purely a CALL-SITE change (`.Handle(client)` now
  calls this validation unconditionally instead of only inside the old
  `Register`), not a new type to document.
- `api/events/builder.go` (possibly renamed alongside, e.g. `client.go` —
  TBD at implementation time) — the `Builder`→`Client` rename touches
  the type itself, all its methods.
- `Subscribe{}`/`Publish{}` call-site USAGE changes repo-wide: every
  existing `events.NewChannel[T](topic, codec, events.Subscribe{...},
  events.Publish{...}, ...)` pattern must migrate to
  `channel.WithSubscribe(events.Subscribe{...})` /
  `channel.WithPublish(events.Publish{...})` as SEPARATE statements —
  audit exhaustively during implementation, touches every example/test
  declaring a channel with either operation.
- `ports/handle.go`'s `buildEventPatternHandles` (`builder *events.Builder`
  param → `*events.Client`) and the `EventPattern` case's `Register` call
  site, updated to build a `Subscriber[T]`/`Publisher[T]` and call
  `.Handle(client)` based on `portRole`.
- `ports/io_param.go`'s `PortOptions.EventBuilder *events.Builder` field
  — rename the type reference (consider renaming the field itself to
  `EventClient` for consistency; confirm during implementation).
- `adapters/mqtt5.Subscribe`'s bare name is REASSIGNED from the
  handle-based form (renamed `SubscribeWithHandle`) to a NEW value-based
  form — existing direct callers of the OLD `Subscribe` need to either
  rename their call to `SubscribeWithHandle` (identical behavior) or
  adopt the new value-based convenience. Same for `adapters/mqtt` (v3)/
  `adapters/zeromq`. Confirmed via code: `ports`/`SubscribeAdapter` does
  NOT call the exported `Subscribe` function at all (calls a lower-level
  primitive directly) — zero impact on `ports` regardless.
- Every example under `examples/` constructing `events.NewBuilder(...)`
  or referencing `*events.Builder`, `events.Subscribe{}`/`Publish{}` as
  inline `NewChannel` opts, or calling `adapters/mqtt5.Subscribe`
  directly (mqtt5/mqtt/zeromq pub/sub examples, `sensor-service`, etc.)
  — audit exhaustively during implementation; this is a broader symbol
  set than any single earlier draft's checklist covered.
- `docs/concepts/api-contracts.md`, `docs/features/*.md` wherever
  `events.Builder`/`events.NewBuilder`/`events.Subscribe{}`/`Publish{}`
  inline usage/`adapters/mqtt5.Subscribe` appears in prose/code
  snippets — per the `review-go-codex` skill's checklist §14 (Godoc &
  Documentation-Site Reference Integrity), this exact kind of rename is
  invisible to `go build`/`go vet`/`go test` and needs an explicit
  full-repo sweep.
- `api/events/*_test.go` — every `NewBuilder`/`Register`/`ClientHandle`/
  inline-`Subscribe`/`Publish` call site.
- `.github/instructions/go-codex.instructions.md` update scope: document
  the new `Subscriber[T]`/`Publisher[T]` types, `WithSubscribe`/
  `WithPublish`, `.Handle(client)`, the two-tier `Subscribe`/
  `SubscribeWithHandle` split, `Subscriber[T].WithHandler`, the new
  `Subscriber[T].Register(client)` method and `events.MissingHandlerError`
  (distinct from `.Handle()` — see the F1 bug fix under "A blocking
  gap"), `events.FromSecurityScheme` (replacing the REMOVED
  `events.WithSecurityScheme`), the new
  `events.ConflictingSecurityDeclarationError` and
  `events.UnsupportedMiddlewareParamsError`, the new
  `ChannelHandle[T].Handler`/`HandlerOpts` fields, `Client`'s new
  `sync.RWMutex`-guarded registry and `SubscriberEntries()` accessor,
  the new `events.SubscriberServer`/`events.PublisherClient[T]`
  interfaces, the new `mqtt5.Caller`/`NewCaller`/`PublisherFor[T]`
  bundling types (and their `mqtt`(v3)/`zeromq` equivalents), and the
  new per-adapter whole-client `(*Caller).ServeSubscribers(ctx)` entry
  point.
- `events.WithSecurityScheme`'s REMOVAL is a BREAKING change (unlike
  most of this checklist's mostly-additive items) — every existing
  `WithSecurityScheme(...)` call site in examples/tests must migrate to
  `.Use(events.FromSecurityScheme(...))`.
- **New additions, not renames — no existing-caller migration burden
  from this specific piece:** `Subscriber[T].WithHandler`,
  `Subscriber[T].Register`/`events.MissingHandlerError`,
  `events.FromSecurityScheme`/`events.ConflictingSecurityDeclarationError`/
  `events.UnsupportedMiddlewareParamsError`,
  `ChannelHandle[T].Handler`/`HandlerOpts`, `Client.SubscriberEntries()`,
  `events.SubscriberServer`/`events.PublisherClient[T]`,
  `mqtt5.Caller`/`NewCaller`/`PublisherFor[T]` (and their per-adapter
  equivalents), each adapter's new `ServeSubscribers` method, and each
  adapter's new `ServeOneSubscriber` shortcut are all ADDITIVE —
  nothing existing calls or reads them today, so there is no migration
  cost for this piece specifically (unlike the
  `Builder`→`Client` rename and the `Subscribe`/`SubscribeWithHandle`
  reassignment above, which DO have real migration costs).
- `mqtt`(v3) gets GENUINELY NEW capability (a higher-level `Caller`/
  `Subscribe` wrapping the existing, UNCHANGED `SubscribeHandler`
  primitive) rather than a mechanical two-tier split — no existing
  caller of `SubscribeHandler` is affected at all; this is purely
  additive for `mqtt`(v3) specifically, unlike `mqtt5`/`zeromq` where
  the bare `Subscribe` name IS reassigned.
- **New `TopicFilter string` field on `mqtt5.SubscribeOptions`,
  `zeromq.SubscribeOptions`, AND `zeromq.SubscribeAdapterOptions`**
  (wildcard-filter bug fix, found during the wildcard review) — purely
  ADDITIVE, empty-string default preserves today's behavior for
  non-templated topics unchanged; only a templated topic's behavior
  CHANGES (from "silently never matches" to "correctly matches"), which
  is a bug fix, not a breaking behavior change worth treating specially.
  New `zeromq.deriveTopicPrefix` helper (mirrors `mqtt5`/`mqtt`'s
  existing `deriveWildcardFilter`).
- **BREAKING: `ports.EventPattern` gains two new dedicated fields,
  `Subscribe *events.Subscribe`/`Publish *events.Publish`** (found
  during the `ports`/`api/events` integration review; corrects Decision
  4's own earlier, self-contradictory revision — see Decision 4 for the
  full write-up). Existing callers passing `events.Subscribe{}`/
  `events.Publish{}` inline inside `EventPattern.Opts` (e.g.
  `examples/sensor-service/ioports/ports.go`'s `SensorsPattern`/
  `AlertsPattern`) must migrate to the new dedicated fields — this is
  IN ADDITION TO, not a duplicate of, the already-tracked
  `Subscribe{}`/`Publish{}` `ChannelOpt`-removal breaking change
  above (that change is WHY this one is necessary: once `Subscribe`/
  `Publish` stop satisfying `ChannelOpt`, they can no longer live
  inside `Opts []events.ChannelOpt` at all). New `PatternRegisterError`
  case (`Kind: patternKindEvent`) added for the wrong-role-field-set
  rejection. `ports/pattern.go`'s `EventPattern` doc comment example
  needs updating alongside the checklist §14 godoc sweep this doc
  already calls for.

### Diagram — the NEW design (Decision 1 resolved) and its remaining gaps

Same shape as the prior diagram, now showing the role-scoped
`Subscriber[T]`/`Publisher[T]` builders, each with its OWN `Use`/
`SubscribeMW`-or-`PublishMW`, and the two-tier `Subscribe`/
`SubscribeWithHandle` split — but the adapter-side boxes are marked
`██ GAP ██` where Decision 3/4 specify WHAT should happen without this
doc having actually WIRED it yet. This diagram is the concrete map for
"what does the next implementation pass still need to do," and
separately (its closing list) for "what escape hatches does the NEXT
design-review pass (the one after this diagram) need to walk through one
by one."

```text
DECLARE (shared, one Channel[T] value; role config forks IMMEDIATELY,
unlike the flat single-Channel shape shown in the current-workflow
diagram above)
┌──────────────────────────────────────────────────────────────┐
│ events.NewChannel[T](topic, codec, opts...)                   │
│   opts (UNCHANGED from the current-workflow diagram):          │
│    • events.TopicParam / events.Formats family                │
│    • events.ErrorChannel(...)                                  │
│   (events.Subscribe{}/Publish{} NO LONGER passed here — moved   │
│    to WithSubscribe/WithPublish below, a BREAKING usage change) │
└──────────────────────────────────────────────────────────────┘
        │                                   │
        ▼                                   ▼
┌───────────────────────────┐     ┌───────────────────────────┐
│ channel.WithSubscribe(     │     │ channel.WithPublish(       │
│   events.Subscribe{...})   │     │   events.Publish{...})     │
│  → events.Subscriber[T]    │     │  → events.Publisher[T]     │
│                             │     │                             │
│ sub.Use(mws...)            │     │ pub.Use(mws...)            │
│  — THIS role's OWN Security│     │  — THIS role's OWN Security│
│  declaration; NO channel-  │     │  declaration; NO channel-  │
│  level Use() exists at all │     │  level Use() exists at all │
│  (removed — fixes a         │     │  (removed — fixes a         │
│  per-role Security gap an  │     │  per-role Security gap an  │
│  earlier flat draft had)   │     │  earlier flat draft had)   │
│                             │     │                             │
│ sub.SubscribeMW(mw, fn)     │     │ pub.PublishMW(mw, fn)       │
│  — attaches verify Fn       │     │  — attaches credential Fn   │
│  (paired via                │     │  (paired via                │
│  mw.Security.SchemeName, or │     │  mw.Security.SchemeName, or │
│  unpaired/general)          │     │  unpaired/general)          │
│                             │     │                             │
│ sub.WithHandler(fn)         │     │ (Publisher never had, never │
│  — REINSTATED, declare-time;│     │  needs, a handler concept —  │
│  consumed ONLY by the NEW    │     │  confirmed against REST's    │
│  whole-client Serve below,   │     │  handler-less Call)          │
│  NEVER by Subscribe/          │     │                             │
│  SubscribeWithHandle (fully   │     │                             │
│  independent paths, no        │     │                             │
│  "which wins" ambiguity)      │     │                             │
└───────────────────────────┘     └───────────────────────────┘
        │                                   │
        ▼                                   ▼
┌───────────────────────────┐     ┌───────────────────────────┐
│ sub.Handle(eventsClient)    │     │ pub.Handle(eventsClient)   │
│  → *ChannelHandle[T]       │     │  → *ChannelHandle[T]       │
│  (FRESH, independent per   │     │  (FRESH, independent per   │
│  call — no shared/mutated  │     │  call — no shared/mutated  │
│  pointer; client optional, │     │  pointer; client optional, │
│  nil = spec-free handle)   │     │  nil = spec-free handle)   │
│                             │     │                             │
│  client non-nil: DEDUPS     │     │  client non-nil: DEDUPS     │
│  this topic's spec entry    │     │  this topic's spec entry    │
│  (first-registered-wins on  │     │  (first-registered-wins on  │
│  descriptor content; a      │     │  descriptor content; a      │
│  DIFFERENT T on the same    │     │  DIFFERENT T on the same    │
│  topic → ChannelTypeConflict│     │  topic → ChannelTypeConflict│
│  Error, NEW)                │     │  Error, NEW)                │
│                             │     │                             │
│  populates ONLY             │     │  populates ONLY             │
│  Implementations (from      │     │  ClientImplementations (from│
│  SubscribeMW), runs eager   │     │  PublishMW) — NO eager check │
│  UnknownMiddlewareImpl-      │     │  (mirrors REST/reqreply's    │
│  ementationError check      │     │  asymmetry, unchanged)       │
│  (NEW, mirrors REST/reqreply)│    │                             │
│                             │     │                             │
│  NEVER touches the           │     │                             │
│  ServeSubscribers registry    │     │                             │
│  (see side-branch below —     │     │                             │
│  bug fixed this pass: an       │     │                             │
│  earlier draft had .Handle()   │     │                             │
│  ALSO update that registry,     │     │                             │
│  letting Subscribe(fn)'s        │     │                             │
│  internal .Handle() call        │     │                             │
│  SILENTLY clobber a              │     │                             │
│  ServeSubscribers-registered      │     │                             │
│  Handler — now impossible,         │     │                             │
│  fully separate call/state)         │     │                             │
└───────────────────────────┘     └───────────────────────────┘
        │                                   │
        ├─── SIDE BRANCH (subscribe-only,     │
        │    separate from the main flow      │
        │    below): sub.Register(eventsClient)│
        │    error — NEW, the ONLY way to feed │
        │    ServeSubscribers. Requires         │
        │    Handler != nil (WithHandler        │
        │    called earlier) — else NEW         │
        │    events.MissingHandlerError.         │
        │    Contributes to spec (same dedup     │
        │    as .Handle()) AND updates Client's   │
        │    mutex-guarded registry (pointer-      │
        │    swap, never field-mutation — own       │
        │    last-registered-wins, scoped ONLY to    │
        │    repeated Register() calls, NEVER         │
        │    affected by .Handle() calls). No          │
        │    Publisher[T] equivalent — nothing to       │
        │    feed.                                       │
        ▼                                   ▼
┌───────────────────────────┐     ┌───────────────────────────┐
│ NEW — mqtt5.Caller{client,  │     │                             │
│  router, events} bundles    │     │                             │
│  the 3 repeated params for   │     │                             │
│  BOTH Subscribe and          │     │                             │
│  ServeSubscribers (below).   │     │                             │
│  NO WithBaseURL — confirmed  │     │                             │
│  not meaningful: MQTTClient  │     │                             │
│  is bound to ONE broker at   │     │                             │
│  connection, no shared        │     │                             │
│  reusable transport           │     │                             │
│  independent of target like  │     │                             │
│  *http.Client has             │     │                             │
│                             │     │                             │
│  *Caller implements NEW      │     │                             │
│  events.SubscriberServer      │     │                             │
│  interface (ServeSubscribers  │     │                             │
│  (ctx) error) — transport-    │     │                             │
│  agnostic call sites; mqtt/   │     │                             │
│  zeromq's own Caller types    │     │                             │
│  implement it too              │     │                             │
├───────────────────────────┤     ├───────────────────────────┤
│ TWO-TIER (NEW):             │     │ ONE TIER (unchanged):       │
│                             │     │                             │
│ adapters/mqtt5.Subscribe(   │     │ adapters/mqtt5.Publish(     │
│   ctx, caller, sub, qos, fn,│     │   ctx, client, handle, qos,  │
│   opts, formats...)         │     │   retained, msg, vars, opts, │
│  — value-based convenience, │     │   formats...)                │
│  takes *Caller (not 3       │     │  — handle-based ONLY; already│
│  separate params); builds   │     │  the efficient "build once,  │
│  handle internally via       │     │  call per-message" shape; no │
│  sub.Handle(caller.events);  │     │  new tier needed             │
│  fn STILL call-time (this    │     │                             │
│  is the imperative "start    │     │                             │
│  consuming now" entry point) │     │                             │
│                             │     │                             │
│ adapters/mqtt5.Subscribe    │     │                             │
│  WithHandle(ctx, client,    │     │                             │
│   router, handle, qos, fn,  │     │                             │
│   opts, formats...)         │     │                             │
│  — RENAMED from today's bare│     │                             │
│  Subscribe; handle-based     │     │                             │
│  primitive, RAW params (NOT │     │                             │
│  *Caller), used by ports/    │     │                             │
│  advanced callers            │     │                             │
│                             │     │                             │
│  ██ GAP — NOT YET WIRED ██  │     │  ██ GAP — NOT YET WIRED ██   │
│  Decision 3 fixes the Fn    │     │  Decision 3 fixes the Fn     │
│  SHAPE but this doc does    │     │  SHAPE but this doc does     │
│  NOT yet specify the        │     │  NOT yet specify the         │
│  adapter-side consumption   │     │  adapter-side consumption    │
│  of handle.Implementations/ │     │  of handle.ClientImplement-  │
│  ClientImplementations (the │     │  ations (the reqreply/mqtt5  │
│  reqreply/mqtt5 pass already│     │  pass already prototyped this│
│  prototyped this exact      │     │  exact wiring pattern — see  │
│  pattern additively — see   │     │  paused WIP in                │
│  paused WIP in               │     │  adapters/mqtt5/reqreply.go — │
│  adapters/mqtt5/reqreply.go)│     │  reuse, don't redesign)        │
│                             │     │                             │
│  CheckCoverage-equivalent    │     │  (no coverage check on the   │
│  SIGNATURE decided, WIRING   │     │  publish side by design —    │
│  into Subscribe deferred     │     │  mirrors REST/reqreply)       │
└───────────────────────────┘     └───────────────────────────┘
        │
        ▼
┌───────────────────────────────────────────────────────────────┐
│ NEW — (*mqtt5.Caller).ServeSubscribers(ctx) error                  │
│   — METHOD on *Caller (not a free fn); implements the NEW          │
│   events.SubscriberServer interface; RENAMED from a bare "Serve"    │
│   after finding mqtt5/zeromq ALREADY export Serve[Req,Resp] for     │
│   reqreply (Go has no overloading) — whole-CLIENT entry point,     │
│   mirrors rest.Serve(mux, builder) exactly                          │
│                                                                     │
│  Walks caller.events.SubscriberEntries() (NEW sealed interface,     │
│  mirrors rest.RouteEntry exactly, backed by Client's NEW mutex-     │
│  guarded registry — populated EXCLUSIVELY by sub.Register() calls,  │
│  see the side-branch above, NEVER by .Handle() calls); every entry  │
│  is therefore GUARANTEED to already have a Handler (Register()      │
│  rejects handler-less registration eagerly) — the "skip             │
│  !HasHandler() entries" check mirrors rest.Serve's Part-1 skip       │
│  semantics defensively, but is not expected to trigger in practice;  │
│  ONE GOROUTINE PER SUBSCRIBE ROUTE (confirmed);                     │
│  blocks until ctx cancelled or all goroutines exit, collecting      │
│  errors (assumed default, not separately re-confirmed)              │
│                                                                     │
│  DISPATCH — RESOLVED this pass, mirrors REST's OWN EXISTING         │
│  HandlerOpts/WithOptions/resolveOptions pattern exactly (NOT         │
│  harder than REST's, as previously assumed): reflect.Value.Call     │
│  isolated entirely inside mqtt5 (zero reflect in api/events),        │
│  ChannelHandle[T] gains HandlerOpts any (type-erased, mirrors        │
│  RouteHandle.HandlerOpts), Subscriber[T].WithOptions(opts any)       │
│  declares per-channel QoS/etc. at declare time, type-asserted        │
│  back exactly like nethttp.resolveOptions does                      │
└───────────────────────────────────────────────────────────────┘
        │                                   │
        ▼                                   ▼
  ██ GAP — adapters/mqtt (v3): NEW higher-level mqtt.Caller/mqtt.Subscribe/
     mqtt.ServeSubscribers capability needed (NOT a rename — v3 has no
     router, no bare Subscribe today, only the lower-level
     SubscribeHandler closure-builder, which stays UNCHANGED); publish-
     side gets the SAME in-payload *T-write mechanism as mqtt5/zeromq
     (Decision 3, resolved) — deferred to the future adapter-wiring
     pass, wiring mechanics TBD ██

  ██ GAP — adapters/zeromq: message-level Fn shape RESOLVED via
     Decision 3's in-payload *T-write mechanism (no wire-level
     convention needed after all) — but the Caller/Subscribe/
     ServeSubscribers wiring itself is still deferred to the future
     adapter-wiring pass (zeromq's existing sock-taking shape maps
     cleanly, confirmed above) ██

  ██ GAP — ports.EventPattern: Decision 4 SPECIFIES the fix (build a
     Subscriber[T]/Publisher[T] and call the role-correct .Handle(client)
     instead of always Register) but it is NOT YET IMPLEMENTED — still
     calls Register today ██

REMAINING STANDING ESCAPE HATCHES (kept in sync with the full
"Escape hatches" section below — see there for full detail/history):
  • SecurityScheme.Codec nil → unvalidated raw credential (item 3, KEEP)
  • Coverage enforcement (item 2) — RESOLVED, CheckCoverage now
    unconditional
  • Last-registered-wins on security-scheme name collisions (item 7,
    KEEP — RELOCATED from WithSecurityScheme, which is REMOVED, to
    .Use(FromSecurityScheme(...)) — same risk, same policy as REST)
  • Client.AddGlobalSecurity 3-state inheritance (item 8, KEEP)
  • Connection-level SecuredClient uncoordinated with message-level
    scheme (item 9, KEEP)
  • Subscriber[T]/Publisher[T].Handle(client) called twice for the same
    channel+client+role → two independently-valid handles, no
    "last wins" detection (item 10, KEEP — scoping corrected: this is
    about .Handle()'s RETURNED VALUES only, never the separate
    Register()-fed registry)
```

**Key observation this diagram makes visible:** Decision 1 fixed the
STRUCTURAL problem (who can build what handle, safely, and how
middleware-carrying values meet the imperative `Subscribe` call) but has
NOT YET touched the ENFORCEMENT problem the prior diagram already
showed (escape hatches 2/3/7/8/9 all survive unchanged into the new
design, plus new item 10). The next pass's job is exactly this: walk
each surviving escape hatch and ask "should this still be possible after
the redesign, or should the new design make it structurally impossible
instead of just documented."

### Decision 3 — Per-adapter Fn shapes (mostly resolved this pass; `PublishMW` gains `*T` write-access — resolves the mqtt v3 publish-side "gap")

**Headline finding, surfaced during the escape-hatch review below:**
today's `PublishMW`/`CredentialFunc` Fn shape
(`func(context.Context, []route.SecurityRequirement) ([]UserProperty, error)`)
is asymmetric with `SubscribeMW`/`SecurityFunc`
(`func(context.Context, *pahomqtt5.Publish, *T) (map[string][]string, error)`)
in one important way: `SubscribeMW`'s `fn` gets a WRITABLE `*T` pointer
into the decoded payload (mirroring REST's own security `Fn` being
"Req-generic AND POINTER — reads AND WRITES the already-merged struct");
`PublishMW`'s `fn` gets NO `*T` access at all — it can ONLY produce
protocol-native `UserProperty` values. This asymmetry is EXACTLY why
mqtt v3's publish side looked permanently impossible: the only
mechanism that existed was protocol-native (User Properties), and MQTT
3.1.1's PUBLISH packet has no property field to attach one to.

**Resolution: give `PublishMW`'s `fn` write-access to `*T`, mirroring
`SubscribeMW`'s read/write access** — an ADDITIONAL mechanism (caller's
choice, alongside any protocol-native output where available) that lets
a credential be embedded as an ordinary field IN THE PAYLOAD itself.
This works identically across ALL THREE transports, since a payload is
just codec-encoded bytes, transport-agnostic — closing the mqtt v3
publish-side gap for the in-payload case specifically (only "attach via
an out-of-band protocol-native side channel" remains mqtt5-exclusive, a
correctly-scoped, permanent, and much NARROWER limitation than
previously stated).

- **`mqtt5` — fully resolved**, REVISED this pass: subscribe-side
  UNCHANGED, `func(context.Context, *pahomqtt5.Publish, *T) (map[string][]string, error)`;
  publish-side REVISED to
  `func(context.Context, msg *T, reqs []route.SecurityRequirement) ([]UserProperty, error)`
  — can write into `*T` (in-payload) AND/OR return `UserProperty` values
  (protocol-native), caller's choice, both available simultaneously if
  desired.
- **`mqtt` (v3) — BOTH sides now FULLY resolved** (subscribe-side
  confirmed, not assumed, this pass; publish-side NEWLY resolved via the
  headline finding above): subscribe-side
  `func(context.Context, pahomqtt.Message, *T) (map[string][]string, error)`,
  mirroring mqtt5's translation exactly. Publish-side
  `func(context.Context, msg *T, reqs []route.SecurityRequirement) error`
  — NO `UserProperty`-equivalent return (the protocol has no concept to
  attach it to), writes into `*T` only. **This is genuinely NEW
  capability for mqtt v3's publish side** — previously believed
  impossible; the "no message-level credential mechanism" framing was
  too broad, since it only ever applied to the protocol-native-only
  design that existed before this revision.
- **`zeromq` — message-level mechanism now TRACTABLE** (previously
  believed to require inventing a new wire-level convention before
  anything was possible — no longer accurate): subscribe-side and
  publish-side both follow the SAME `*T`-only shape as mqtt v3
  (`func(context.Context, msg *T, reqs []route.SecurityRequirement) error`
  for both directions — zeromq's `[topic, payload]` frames carry
  nothing beyond what's already decoded into `T`, so there is no
  raw-message-equivalent parameter to also pass, unlike mqtt/mqtt5's
  subscribe-side). **Not fully designed here** — spun out to
  [ZeroMQ Security Mechanism](zeromq-security.md), which now scopes down
  to a much narrower remaining question (an OPTIONAL additional
  out-of-band frame-based mechanism, plus the separate connection-level/
  CURVE question) rather than "invent an entire wire convention from
  scratch."

#### General-purpose (non-spec) Fn shapes for `SubscribeMW`/`PublishMW` — found missing during a critical review of the middleware concept for things like Observer, resolved

Everything above in this Decision only ever specifies the
SECURITY-shaped Fn for `SubscribeMW`/`PublishMW`. A dedicated review of
the middleware concept for NON-spec-adding middlewares (like an
Observer) — the pub/sub analogue of `nethttp.Observability` — found
this was a genuine, unaddressed gap, traced precisely against REST's
own dispatch code:

- **REST server-side (`HandleMW`) dispatch recognizes exactly TWO Fn
  shapes** — confirmed via `validateImplementationShapesReflect`: the
  security-Fn shape, AND a general-purpose `func(http.Handler) http.Handler`.
  This SECOND shape is what lets `nethttp.Observability(obs)` attach at
  all, via `route.HandleMW(nil, nethttp.Observability(obs))` — REST's
  server-side `Options.Observer` field was fully REMOVED in favor of
  this general-purpose hook.
- **REST client-side (`ClientMW`) dispatch recognizes only ONE shape**
  — confirmed via `validateClientImplementationShapes`: the
  credential-Fn shape only, hard-erroring (`middleware.MiddlewareShapeError`)
  on anything else. NO general-purpose hook exists on `ClientMW` at
  all — not for observability, not for ANY other cross-cutting
  concern. Confirmed via code this is because `nethttp.Call` is a
  single, self-contained function (unlike `Serve`, which dispatches
  MANY routes through ONE shared `http.ServeMux` and genuinely needs
  external middleware chaining) — `Call`/`CallWithHandle` call
  `obs.RecordRequest(...)` DIRECTLY in their own function body, driven
  by ctx/`opts`-resolved `obs`, so no middleware wrapper was ever
  needed for THAT specific concern. **This is a genuine, PRE-EXISTING
  gap in REST's OWN `ClientMW` design** for any OTHER use case (custom
  logging, request transformation, retries) — spun out at the time to a
  dedicated roadmap doc rather than fixed here (out of THIS doc's scope,
  exactly like the `zeromq`/common-middleware findings above); now
  resolved and folded into
  [d-0001's Addendum 3](d-0001-rest-middleware-workflow-simplification.md#addendum-3-client-side-general-purpose-clientmw-hook-closes-the-last-known-restevents-middleware-asymmetry).

**Fix, scoped to pub/sub, designed for BOTH `SubscribeMW` and
`PublishMW`** (unlike REST, where only the server side got a
general-purpose hook — pub/sub gives BOTH roles one, since the user's
own review explicitly asked for observer-and-beyond on both sides):

- **`SubscribeMW`'s general-purpose shape** (mirrors `func(http.Handler)
  http.Handler`'s "wrap the next thing" idea, retargeted at pub/sub's
  own per-message handler type since there is no literal `http.Handler`
  equivalent to wrap):
  ```go
  func(next func(context.Context, T) error) func(context.Context, T) error
  ```
  Wraps the caller's own business handler (`Subscribe(fn)`'s `fn`, or
  `Register()`'s `WithHandler`-attached `Handler`) — an UNPAIRED
  (general-purpose, `mw` nil or `Security` nil) `SubscribeMW`-attached
  Fn of this shape runs UNCONDITIONALLY, composing around the actual
  handler invocation. This is the mechanism a pub/sub
  `Observability`-equivalent would use:
  `sub.SubscribeMW(nil, mqtt5.Observability(obs))`, where
  `mqtt5.Observability` returns a value of exactly this shape, calling
  `obs.RecordSubscribe(...)` around `next(ctx, msg)`.
- **`PublishMW`'s general-purpose shape — NEW design here, no direct
  REST precedent to mirror** (REST's `ClientMW` gap is being spun out,
  not fixed, so there is nothing to copy) — designed for DELIBERATE
  SYMMETRY with `SubscribeMW`'s shape above, rather than a 1:1 REST
  mirror. Confirmed via code that `Publish[T any](ctx, client, handle,
  qos, retained, msg T, vars, opts, formats...) error` has NO
  caller-supplied handler function at all — it takes `msg T` directly
  and performs the encode+transmit itself, internally. The
  general-purpose hook wraps THAT internal step instead:
  ```go
  func(next func(context.Context, T) error) func(context.Context, T) error
  ```
  Same shape as `SubscribeMW`'s (deliberate symmetry, easier to learn
  once) — `next(ctx, msg)` represents "encode `msg` and transmit it"
  (the adapter's OWN internal step), letting an attached general-purpose
  Fn wrap it: add tracing, mutate/log `msg` before it is sent,
  implement custom retry logic around the transmit — the SAME class of
  "more use cases than just security and observation" this review was
  asked to check for.
- **Both shapes validated EAGERLY**, mirroring REST's
  `validateImplementationShapesReflect`/`validateClientImplementationShapes`
  exactly: an adapter's `Subscribe`/`SubscribeWithHandle`/
  `ServeSubscribers`/`Publish` checks every attached `SubscribeMW`/
  `PublishMW` Fn against the TWO recognized shapes (security-shaped OR
  the general-purpose wrapping shape above) at construction/dispatch
  time — a wrong shape is a hard error (reusing
  `middleware.MiddlewareShapeError` directly, since it is ALREADY
  transport/boundary-agnostic by name and shape — confirmed no
  pub/sub-specific variant is needed).
- **Observer specifically does NOT need to move off `SubscribeOptions.Observer`/
  `PublishOptions.Observer` entirely** — those per-call fields, and
  `stats.ObserverFromContext(ctx)`'s ctx-injection fallback, remain the
  PRIMARY, zero-ceremony path for the common case (confirmed via
  `docs/features/observer.md`: MQTT/ZeroMQ adapters already resolve
  `obs` from ctx when the call-time field is nil, exactly like REST's
  client side). The general-purpose `SubscribeMW`/`PublishMW` hook is
  for callers who want DECLARE-TIME, PER-CHANNEL observability
  attachment (or any other cross-cutting concern) CONSISTENTLY applied
  regardless of which specific `Subscribe`/`Publish`/`ServeSubscribers`
  call site is used — an ADDITIONAL, opt-in capability, not a
  replacement for the existing per-call/ctx-injection mechanism.
- **A follow-up review confirmed `nethttp.Transform`'s use case
  (deriving/enriching a value from raw message data BEFORE the handler
  runs) is ALREADY covered — for FREE — by the EXISTING, PRE-DECISION-3
  security-shaped Fn, attached UNPAIRED, with NO new mechanism
  needed.** Traced precisely: `Transform`'s returned closure
  (`func(ctx, *http.Request, *Req) (map[string][]string, error)`) has
  the EXACT SAME Go type as REST's security-Fn shape — it IS that shape,
  just attached with `mw` nil (unpaired), always returning `nil` grants.
  `SubscribeMW`'s existing security-shaped Fn
  (`func(ctx, *pahomqtt5.Publish, *T) (map[string][]string, error)`) has
  IDENTICAL structure — write-access to `*T`, attachable unpaired —
  so `sub.SubscribeMW(nil, fn)` with THAT shape already IS a
  Transform-equivalent (e.g. reading an MQTT5 User Property into a
  field on `*T` before the handler runs). Same for `PublishMW`'s
  revised shape (`func(ctx, msg *T, reqs) (...)`, from this Decision's
  earlier `*T`-write-access fix) — it already supports "enrich `msg`
  before publish" as a side effect of that fix, never previously called
  out explicitly. **No new shape, no new mechanism — just a
  previously-undocumented capability, now stated plainly.**

**A SEPARATE, genuinely spec-adding kind of custom middleware — REST's
`FromHeaderParam`-style bridge — has NO 1:1 pub/sub equivalent, by
design, but raised a bigger question spun out to its own doc.** Pub/sub's
only var-boundary (the topic) is declared directly on `Channel[T]`, never
middleware-contributed, and AsyncAPI's `Operation`/`ChannelItem` has no
header/cookie/query-equivalent spec surface at all — so no
`FromHeaderParam` analogue is possible or needed here. But this raised a
DEEPER, unresolved question: how should PROTOCOL-NATIVE capabilities
(MQTT5 User Properties, Shared Subscriptions, Message Expiry; ZeroMQ's
Conflate/HWM) be declared, given pub/sub spans THREE incompatible
transports within ONE pattern (unlike REST, always HTTP)? See
[Protocol-Native Feature Declarations](protocol-native-features.md) —
spun out rather than resolved here, proposing a `ProtocolFeature` sealed-
interface mechanism (mirrors `ports.Pattern`) for "declare a capability,
let the binding adapter validate/fulfill it or reject," generalized
beyond pub/sub as an open question there.

#### Confirmed bug, fixed this pass: `mqtt5.Subscribe`/`zeromq.Subscribe` never derive a broker-compatible filter from a templated topic

**Found while reviewing how wildcards interact with the new design —
pre-existing, NOT caused by this redesign, but directly inherited by the
new `Subscribe`/`SubscribeWithHandle`/`ServeSubscribers` entry points
this doc makes the PRIMARY, application-facing API surface.**

Traced precisely via code: `adapters/mqtt5/binding.go` already has an
internal `deriveWildcardFilter(topic string) string` helper — replaces
each `{varName}` placeholder with MQTT's single-level wildcard `+`
(`"sensors/{sensorID}/data"` → `"sensors/+/data"`; a topic with no
placeholders passes through unchanged) — but it is called ONLY from the
unexported `ports`-binding adapter (`mqtt5SubscribeAdapter.Activate`).
The TOP-LEVEL, EXPORTED `mqtt5.Subscribe` function — the one this doc's
`Subscribe`/`SubscribeWithHandle`/`ServeSubscribers` are all built
directly on top of — sends `handle.Topic` (the RAW template) VERBATIM
as the broker subscription filter
(`Subscriptions: []pahomqtt5.SubscribeOptions{{Topic: handle.Topic,
QoS: qos}}`). Against a REAL MQTT5 broker this NEVER matches any
concrete published topic (brokers require `+`/`#` wildcard syntax, not
`{var}` placeholders) — the bug was invisible until now because no
example/test exercises a templated-topic channel against a real (or
filter-checking) broker; every test/example either uses a non-templated
topic or a permissive mock that ignores the filter value entirely.

`adapters/zeromq.Subscribe` has the SAME bug, and WORSE: `zeromq.
SubscribeOptions` (top-level) AND `zeromq.SubscribeAdapterOptions`
(`ports`-binding layer) BOTH have zero filter-override field at all —
confirmed via code, `SubscribeAdapterOptions` is just `{Buffer int}`.
`Subscribe` calls `sock.SetSubscription(handle.Topic)` unconditionally
— since ZeroMQ subscription filtering is plain BYTE-PREFIX matching (no
wildcard concept at all, confirmed via `zeromq/topicvars.go`'s own doc
comment), a template like `"sensors/{sensorID}/readings"` used as a
literal prefix filter NEVER matches a real topic like
`"sensors/f47ac.../readings"` — **this bug affects EVERY zeromq
subscribe path today, `ports`-based or direct, with zero exception**
(unlike mqtt5, where the `ports` layer already got it right).

`mqtt`(v3) has no bug in its OWN shipped code (its exported function
never calls the broker's `Subscribe` itself — today's caller wires that
manually) — but Gap E's planned NEW `mqtt.Subscribe(ctx, caller, sub,
qos, fn, opts)` convenience (not yet implemented) would need to get this
right from day one, or it inherits the identical gap the moment it
ships.

**Fix confirmed, designed for both mqtt5 and zeromq:**

- **`mqtt5.SubscribeOptions` gains a new `TopicFilter string` field**
  (mirrors `SubscribeAdapterOptions.TopicFilter` exactly, moved up to
  the top-level, primary-path option struct) — explicit override; empty
  (the common case) auto-derives via the ALREADY-EXISTING
  `deriveWildcardFilter(handle.Topic)`. `mqtt5.Subscribe`'s broker-
  subscribe call resolves `opts.TopicFilter` if non-empty, else the
  derived filter — the SAME resolution `ports`'s binding adapter
  already does, just now ALSO applied to the top-level function (and
  therefore to `SubscribeWithHandle`/`ServeSubscribers`/
  `ServeOneSubscriber`, which all funnel through it). No new helper
  needed — `deriveWildcardFilter` already exists in the package, just
  needs an additional call site.
- **`zeromq.SubscribeOptions` gains the SAME `TopicFilter string`
  field** (reused name, for cross-adapter vocabulary consistency,
  despite the different underlying semantic — documented explicitly as
  "a ZeroMQ prefix filter, not an MQTT wildcard filter"). Empty
  auto-derives via a NEW helper,
  `deriveTopicPrefix(topic string) string` — returns the substring of
  `topic` up to (not including) the FIRST `{` placeholder, or `topic`
  unchanged if it has none (e.g. `"sensors/{sensorID}/readings"` →
  `"sensors/"`). `zeromq.Subscribe`'s `sock.SetSubscription(...)` call
  resolves `opts.TopicFilter` if non-empty, else the derived prefix.
  **Confirmed SAFE via code trace**: a broader prefix subscription
  necessarily receives some non-matching concrete topics too (anything
  else starting with the same prefix) — but `Subscribe`'s receive loop
  already routes a `TopicVarsFromMessage`/`ValidateTopic` mismatch
  through `opts.OnError` and `continue`s the loop, non-fatally — this
  was ALREADY the correct, safe behavior for a mismatched topic
  (unrelated to this fix), so subscribing broader than the exact
  template is not a new risk.
- `zeromq.SubscribeAdapterOptions` (`ports`-binding layer) gets the
  SAME `TopicFilter` field added too — this is a genuine BUG FIX to
  ALREADY-SHIPPED code, not just new-design scope; `ports`'s zeromq
  binding was equally broken before this pass, unlike mqtt5's.
- Flagged explicitly for whenever `mqtt`(v3)'s NEW `mqtt.Subscribe`
  convenience (Gap E) is implemented: it MUST derive/accept a
  `TopicFilter` (v3's OWN `deriveWildcardFilter`, confirmed already
  present in `adapters/mqtt/binding.go`, mirrors mqtt5's) from day one,
  wiring it into whichever v3 broker-subscribe call the new function
  performs on the caller's behalf.

Both new fields are ADDITIVE (new struct fields, zero breaking changes
to existing callers — a non-templated topic's behavior is byte-for-byte
unchanged either way).

### Decision 4 — `ports` needs a small, fully-specified update (REVISED again this pass — the FIRST version of this decision contradicted itself)

Previously believed to need ZERO `ports`-specific code changes (the old
doc's Decision 4, carried into this doc's original draft), then revised
once to account for Decision 1's role-scoped builders replacing
`Register(b)` — **that revision itself was found, during a dedicated
review of how `api/events` integrates into `ports`, to be internally
contradictory and is corrected here.**

**The contradiction, confirmed via code:** the earlier revision of this
Decision said the `EventPattern` fix was "building
`channel.WithSubscribe(pat.Subscribe)`/`channel.WithPublish(pat.Publish)`
(whichever the pattern's `Opts` declare)" while ALSO claiming
"`Pattern`'s existing `Opts []events.ChannelOpt` pass-through is
untouched." Both cannot be true simultaneously: Decision 1's resolved
shape explicitly makes `Subscribe`/`Publish` STOP being valid
`ChannelOpt` values (today, confirmed via code, `Subscribe`/`Publish`
implement `applyChannel` and ARE `ChannelOpt`s — Decision 1 calls their
move to dedicated `WithSubscribe`/`WithPublish` methods "the one
breaking USAGE change to these two existing types," which is only
breaking if that `applyChannel` method goes away). Once `Subscribe`/
`Publish` no longer satisfy `ChannelOpt`, they literally cannot be
scanned out of `pat.Opts []events.ChannelOpt` anymore — `pat.Subscribe`/
`pat.Publish` as referenced by the earlier revision do not exist as
anything then-defined on `EventPattern`.

**Confirmed via real usage** (`examples/sensor-service/ioports/
ports.go`): every `EventPattern` value declared in practice already
carries EXACTLY ONE of `events.Subscribe{}` OR `events.Publish{}` inside
its `Opts` — never both on the same pattern value (`SensorsPattern`
declares `Subscribe` and binds only to a `SourcePort`; `AlertsPattern`
declares `Publish` and binds only to a `SinkPort`). This confirms a
clean, already-established 1:1 mapping between pattern value and port
role — there is no existing use case for one `EventPattern` value
serving both a `SourcePort` and a `SinkPort` simultaneously.

**Corrected fix: `EventPattern` gains two NEW, dedicated fields,
separate from `Opts`:**

```go
type EventPattern struct {
    Topic     string
    Opts      []events.ChannelOpt // TopicParam, ChannelMeta, Formats — unchanged
    Subscribe *events.Subscribe   // consulted only when role == roleSource
    Publish   *events.Publish     // consulted only when role == roleSink
}
```

`Opts` keeps carrying every `ChannelOpt` that stays role-agnostic
(`TopicParam`/`MergedTopicParam`, `ChannelMeta`, `Formats`/
`SubscribeFormats`/`PublishFormats`, `WithTopicConstraints`) — NONE of
those are affected by Decision 1's `Subscribe`/`Publish` change, so this
part of the earlier revision's claim ("`Opts` pass-through is untouched")
remains correct for everything EXCEPT `Subscribe`/`Publish` themselves.

`buildEventPatternHandles`'s `EventPattern` case is updated:

- Builds the shared `Channel[T]` from `pat.Topic`, `codec`, `pat.Opts...`
  (unchanged from today, minus `Subscribe`/`Publish` ever appearing in
  `Opts`).
- `role == roleSource`: consults `pat.Subscribe`. If non-nil, uses it;
  if nil, defaults to a zero-value `events.Subscribe{}` — this exactly
  mirrors TODAY's implicit behavior when a caller simply omitted the
  `Subscribe{}` opt from `Opts` (a `Channel[T]` with no declared
  `Subscribe`/`Publish` metadata already builds a valid, if
  metadata-sparse, `ChannelHandle[T]` today — this is not new
  leniency, just preserving existing behavior through the new field).
  Calls `channel.WithSubscribe(*resolved).Handle(client)`.
- `role == roleSink`: same, consulting `pat.Publish`, calling
  `channel.WithPublish(*resolved).Handle(client)`.
- **New: if the pattern declares the WRONG role's field** (a
  `SourcePort`'s `EventPattern` has non-nil `Publish`, or a `SinkPort`'s
  has non-nil `Subscribe`) **this is treated as a caller mistake and
  rejected eagerly** with a new `PatternRegisterError{Port: portName,
  Kind: patternKindEvent, Err: ...}` — silently ignoring a
  wrong-role field would let a caller believe their `Publish{}`
  metadata is taking effect on a `SourcePort` when it is quietly
  discarded, which is exactly the kind of silent-divergence gap this
  entire redesign has been eliminating elsewhere (mirrors this doc's
  own `RESTPattern`-on-`SinkPort` non-GET-method rejection precedent,
  a few lines above in the same function).
- `role` chosen the same way as before — `role == roleSource` vs.
  `role == roleSink` — the `portRole` value driving the choice already
  exists in that function's signature; unchanged.

`PortOptions.EventBuilder *events.Builder` still becomes
`PortOptions.EventClient *events.Client` (rename, per the migration
checklist, unaffected by this correction). `ports/pattern.go`'s
`EventPattern` doc comment example (currently showing
`events.Subscribe{Summary: ...}` inline inside `Opts`) must be updated
to show the new dedicated `Subscribe:` field instead — flagged for the
same full-repo godoc sweep (checklist §14) the migration checklist
already calls for elsewhere in this doc.

This remains a MUCH smaller change than the `api/events` side of this
doc — flagged here, now fully self-consistent, so implementation
doesn't mistakenly follow the EARLIER, contradictory version of this
Decision.

### Decision 5 — `events.Transport` + `Client.Attach`/`.Publish`/`.Subscribe`/`.ServeSubscribers` (NEW, added after implementation review found the user wanted a literal `Client.Publish`/`.Subscribe` call shape)

A post-implementation design review (triggered by a direct question:
"how do I attach mqtt/zeromq to the client and use
`Client.Publish`/`Client.Subscribe`?") found that Decisions 1-4's
shipped design has NO such call shape — the workflow is
adapter-function-takes-declaration (`zeromq.Subscribe(ctx, caller, sub,
fn, opts)`), never `client.Subscribe(...)`. This decision adds that
missing call shape as a NEW, ADDITIVE layer — every symbol below is
new; nothing from Decisions 1-4 is removed, renamed, or behaviorally
changed.

**This decision is unified with an analogous change to `api/rest` —
see
[d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary),
which this decision's `Transport` interface directly resolves (that
doc was "idea only, no driver yet" — this decision, plus its own REST
counterpart, is the concrete driver it was waiting for).**

#### Why this can't be a literal generic method (confirmed via compile test)

Go forbids a method from introducing its own type parameters — verified
directly: `func (c *Client) Publish[T any](msg T) error` fails to
compile with "method must have no type parameters," and this applies
identically whether `T` is written explicitly on the method or would be
inferred from an argument like `pub events.Publisher[T]`. Since
`events.Client` is (and must remain) a single non-generic type
accumulating MANY different channels of different payload types into
ONE AsyncAPI spec, there is no way to give it a literal, compile-time
type-safe `Publish[T]`/`Subscribe[T]` method. **The only way to get an
ACTUAL method named `Publish`/`Subscribe` on `Client` is to type-erase
the signature to `any` and recover the concrete type internally via
reflection** — accepted as a deliberate, explicit, scoped trade-off
(loses compile-time type safety at just this ONE call site; every other
declarative API in this codebase stays fully type-safe).

#### The reflection technique that makes this feasible

Go cannot reflect-instantiate a generic FUNCTION for a type only known
at runtime (there is no `reflect.ValueOf(zeromq.Publish[SomeRuntimeType])`).
But a method CAN be called via reflection on a value whose DYNAMIC type
is already a fully-instantiated generic type (e.g. an
`events.Publisher[SensorReading]` value boxed in an `any`) — the method
itself is already monomorphized/compiled for that concrete type, so
`reflect.ValueOf(pubAny).MethodByName("Handle").Call(...)` works. Once
the resulting `*ChannelHandle[T]` is recovered (also boxed in `any`),
its exported closures (`Encode func(T)([]byte,error)`, `BuildTopic`,
`MergeFields()`, etc.) are themselves already-concrete, reachable via
`reflect.Value.FieldByName(...)`/`.Call(...)` — EXACTLY the mechanism
`zeromq.ServeSubscribers`'s `buildSubscriberRoute` (Decision 3) already
uses for decode/dispatch, now mirrored for the encode/publish
direction. Each adapter's `Attach`-built shim re-implements the small
"encode + build topic vars + send frames" sequence directly against
these closures via reflection — it does NOT (cannot) call `Publish[T]`/
`PublishHandle[T]` via reflection; it reimplements their few lines
using the same already-proven technique, then hands off to the
CONCRETE, non-generic transport (`FramedSocket.SendFrames`,
`pahomqtt.Client.Publish`, etc.) with zero further reflection needed.

#### The design

```go
// api/events — new:
type Transport interface {
    Publish(ctx context.Context, pub any, msg any) error
    Subscribe(ctx context.Context, sub any, fn any) error
    ServeSubscribers(ctx context.Context) error
}

// Client (existing type) gains:
func (c *Client) Attach(t Transport) error   // returns TransportAlreadyAttachedError on a 2nd call
func (c *Client) Publish(ctx context.Context, pub any, msg any) error
func (c *Client) Subscribe(ctx context.Context, sub any, fn any) error
func (c *Client) ServeSubscribers(ctx context.Context) error  // unchanged semantics vs today's *Caller.ServeSubscribers — walks the SAME SubscriberEntries() registry, just relocated onto Client
```

Each adapter provides an `Attach` entry point building an internal,
unexported `transport` type that WRAPS the existing, unchanged
`*Caller` (no wire logic is duplicated — the shim's `Subscribe`/
`ServeSubscribers` re-enter `*Caller`'s existing generic methods once
the concrete `T` is recovered via reflection; only `Publish` needs the
NEW reflection-native encode path described above, since there is no
way to re-enter a generic `Publish[T]` call with a runtime-only `T`):

```go
// e.g. adapters/zeromq:
func Attach(client *events.Client, sock FramedSocket) error {
    return client.Attach(&transport{caller: NewCaller(sock, client)})
}
```

Resulting workflow:
```go
client := events.NewClient(events.Info{Title: "Sensor Network", Version: "1.0.0"})
if err := zeromq.Attach(client, sock); err != nil { /* ... */ }

sub := ReadingsChannel.WithSubscribe(events.Subscribe{...})
pub := ReadingsChannel.WithPublish(events.Publish{...})

err := client.Subscribe(ctx, sub, fn)
err := client.Publish(ctx, pub, reading)
```

#### Resolved sub-decisions

- **`Attach` is exclusive**: a second `Attach` call on the same
  `Client` returns `TransportAlreadyAttachedError` rather than silently
  replacing the transport (avoids silently swapping the wire underneath
  already-in-flight calls). A caller wanting a different transport
  builds a fresh `Client` — cheap, no different from today.
- **`Client.ServeSubscribers` requires the SAME `Register()`-based
  pre-registration** `*Caller.ServeSubscribers` already requires
  (walks `Client.SubscriberEntries()`) — purely relocated onto `Client`
  itself (which already owns that registry), zero behavior change.
- **`*Caller` (mqtt5/mqtt/zeromq) is KEPT, not removed** — remains the
  lower-level primitive each `Attach` shim wraps internally; every
  existing direct `*Caller` usage (`NewCaller`, `Subscribe[T]`,
  `NewPublisherFor[T]`) is completely unaffected.
- **`Client` is no longer always side-effect-free once transport-attached**:
  its doc comment must say so explicitly — `Publish`/`Subscribe`/
  `ServeSubscribers` perform real I/O once a `Transport` is attached,
  unlike every OTHER `Client` method (spec/registry manipulation, pure).
- A `pub`/`sub` argument whose declared payload type doesn't match
  `msg`'s/`fn`'s dynamic type is a NEW `TransportTypeMismatchError`,
  returned at call time (not a compile error) — the explicit, accepted
  cost of this convenience layer.

### Decision 6 — Single-workflow REST + Events: delete every OLD, call-time-competing public primitive (PLANNED)

**Trigger**: a direct follow-up user request, after Decision 5 shipped
`Client.Attach`/`.Publish`/`.Subscribe`/`.ServeSubscribers` (pub/sub) and
`Server.Attach`/`.Serve` + `rest.Client`/`.Call` (REST): "I want a
consistent workflow across the rest and event api. I want this new
workflow the single only [workflow] the framework provides without
escape hatches ... despite what is currently necessary o[f] the ports
integration (which we also rework towards this simplification)."

**Scope boundary (confirmed via `ask_user`)**: PUB/SUB (`api/events` +
`adapters/mqtt5`/`mqtt`/`zeromq`, pub/sub surface ONLY) and REST
(`api/rest` + `adapters/nethttp`/`chi`) ONLY. Every other port
type/pattern is explicitly deferred to a later round, untouched here:
`reqreply` (including its mqtt5/zeromq-hosted reqreply-shaped functions
— `Serve[Req,Resp]`, `Call[Req,Resp]`, `CallHandle`, `ServeRouter`,
`CallDealer`, `UUIDReplyTopic`, `SharedReplyTopic`, zeromq's
`LatestAdapter[Resp]`/`AsPipelineFunc`/`ServeLatest`, either adapter's
reqreply-shaped `CallAdapter[Req,Resp]`/`ServeAdapter[Req,Resp]`),
`sql`, `redis`, `file`, `mcpgo`, `mcp`, `llm`, `websocket`.

**What "no escape hatches" means concretely**: every OLD, lower-level,
call-time-competing public function/type that predates Decision 5/the
REST `Attach` work is REMOVED from its package's public API — no
back-compat alias, no `// Deprecated:` stub. Where the underlying LOGIC
is still load-bearing (used internally by an `Attach` shim, or by
`ports`' binding.go adapters), it is RELOCATED to an unexported
(lowercase) function/type in the same package, never left publicly
reachable.

**Ports stays exactly as today, by explicit user confirmation**: "On
the port boundary we set the api declaration (route, channel, other
ports) and set the adapter that implements the declaration. That is
the port declaration." — `ports.SourcePort.Bind(zeromq.SubscribeAdapter(...))`-style
binding IS the desired final shape, not something this decision
changes. Only the INTERNAL implementation of each binding.go adapter
function changes, to stop referencing the (now-removed) old public
primitives — a pure internal rename, since `SubscribeAdapter[T]`/
`PublishAdapter[T]`/`CallAdapter[Req,Resp]` etc. are themselves already
generic with T known at compile time (no reflection ever needed here,
unlike `Client.Publish`/`.Subscribe`'s `any`-typed shim).

**Exact removal/unexport list — pub/sub (`adapters/mqtt5`/`mqtt`/`zeromq`, pub/sub surface only):**

- `NewCaller`, `Caller` (type) → unexported — still used internally by
  each adapter's `Attach`/`transport` and by `SubscribeAdapter`/
  `PublishAdapter` (ports binding).
- `Subscribe[T]` (the `*Caller`-based convenience, NOT the reqreply
  `Serve`/`Call` family), `NewPublisherFor[T]`, `ServeOneSubscriber[T]` →
  unexported (or deleted, for `PublisherFor[T]`/`NewPublisherFor[T]`,
  confirmed dead code with zero internal callers once `Client.Publish`
  became the modern path), relocated where their logic is still needed.
- **`SubscribeWithHandle[T]`(zeromq/mqtt5)/`SubscribeHandler[T]`(mqtt v3)
  and `Publish[T]`/`PublishHandle[T]` (all three) are a CONFIRMED
  EXCEPTION — stay PUBLIC, not unexported.** Found during implementation,
  mirroring REST's identical `CallWithHandle`/`ServeOne` exception: these
  are the direct pub/sub analogs of `nethttp.CallWithHandle` —
  handle-based primitives supporting the FULL `SubscribeOptions`/
  `PublishOptions` (custom `OnError`, `Observer`, explicit vars, security
  impls) that `events.Client.Subscribe`/`.Publish`'s v1-scope reflection
  shim CANNOT express (no `OnError` support at all, no custom options).
  Real examples (`adapters-mqtt`, `adapters-mqtt-contract`,
  `adapters-mqtt-security`, `adapters-mqtt5`) genuinely need these for
  advanced scenarios (custom error handling, wildcard topics, security
  demos) — the reflection shim only covers the simple common case.
- `Observability[T](obs) func(...)` **stays PUBLIC** — this is a
  declare-time `.Use()`-attachable general-purpose middleware VALUE
  (part of the DECLARATION story, not a competing call-time mechanism).
- `SubscribeAdapter[T]`/`PublishAdapter[T]` (ports binding) stay
  PUBLIC, reimplemented internally against the now-unexported
  primitives.
- `Attach`, `SubscribeOptions`, `PublishOptions[T]`, all error types
  stay public, unchanged.
- `api/events.Client`/`Transport`/`Attach`/`.Publish`/`.Subscribe`/
  `.ServeSubscribers` (Decision 5) — unchanged, already the sole
  intended call-time workflow. `Channel.WithSubscribe`/`.WithPublish`/
  `Subscriber[T]`/`Publisher[T]`/`.Handle(client)`/`.Register(client)`
  are the DECLARATION layer (build the spec + registry entry) — NOT an
  escape hatch, stay fully public; a `pub`/`sub` value built this way is
  a REQUIRED argument to `Client.Publish`/`.Subscribe`, not an
  alternate path around them.

**Exact removal/unexport list — REST (`adapters/nethttp`/`chi`):**

- `NewCaller`, `Caller` (type), `Call[Req,Resp]` (nethttp only — chi has
  no client side) → unexported, still used internally by
  `clientTransport` (Decision 5's REST counterpart) and by
  `CallAdapter`/`DrainCallAdapter`/`PollAdapter` (ports binding).
- **`CallWithHandle[Req,Resp]` and `ServeOne[Req,Resp]` are a CONFIRMED
  EXCEPTION — stay PUBLIC, not unexported.** Found during implementation:
  `adapters/mcprest` (an MCP-to-REST bridge, structurally adjacent to the
  explicitly-deferred `mcp`/`mcpgo` area) calls `CallWithHandle` directly
  against an ALREADY-BUILT `*rest.RouteHandle[Req,Resp]` (built via
  `.ClientMW(...).ClientHandle()` for a custom per-route credential) —
  `rest.Client.Call`'s reflection-based, `Route`-value-only signature
  cannot express this (no pre-built-handle support, no custom
  `CallOptions`). `api/rest/builder_test.go` similarly uses both to
  demonstrate advanced handle-based reuse (custom merge fields,
  credentials) as core library test coverage. Both remain the fully
  typed, no-reflection foundation for callers needing a pre-built handle
  or non-default `CallOptions` — exactly the same role `ClientHandle()`/
  `Register()` play for the port layer (structurally required lower-level
  building blocks, not a competing convenience for the common case).
- `Serve(mux, server)`/`Serve(r, server)`, `ServeOne[Req,Resp]`,
  `ServeSSE(mux, server)`/`ServeSSE(r, server)` → unexported. Fixes a
  real gap found while scoping this decision: `AttachMux`/`AttachRouter`
  today wire ONLY plain routes, never SSE routes — SSE is completely
  unreachable through the `Attach` workflow. `AttachMux`'s/
  `AttachRouter`'s `Serve(ctx)` now wires BOTH internally.
- Port-binding adapters stay PUBLIC unchanged (`IngestAdapter`,
  `SSEAdapter`, `CallAdapter`, `DrainCallAdapter`, `PollAdapter`,
  `PipelineAdapter`, `LatestAdapter`, `CallSSEAdapter`, `Consume`,
  `HandlerLatest`/`RegisterLatest`/`PipelineHandler`/`RegisterPipeline`/
  `SSEFromHub`, chi's socket adapters) — the ports declaration surface,
  reimplemented internally.
- `api/rest.Server`/`Client`/`Attach`/`.Serve`/`.Call` (Decision 5's REST
  counterpart, see [d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary))
  — unchanged, already the sole intended workflow.

**Execution plan**: one adapter package at a time (`zeromq` → `mqtt5` →
`mqtt` → `nethttp` → `chi` — sequential, not parallel, per this doc's
own Lessons Learned item 4 about cross-package touch risk when
parallelizing adjacent adapter work), each verified green (`go build`/
`go vet`/`go test` for that package) before moving to the next, then
every affected example migrated to the Attach-only workflow, then full
repo verification (`gofmt`/`go build`/`go vet`/`go test`/`just check`/
`just examples` + a dangling-reference grep sweep for every removed
symbol), then a docs sync pass (`.github/instructions/go-codex.instructions.md`,
`docs/guides/{http-server,http-client,mqtt5,mqtt,zeromq}.md`).

### Decision 7 — Invert handle-based primitives into `api/events`; optional `Client.Info`; mqtt5/mqtt broker-ownership (IMPLEMENTED)

**Trigger**: a direct follow-up user request, after Decision 6 confirmed
each adapter's handle-based primitives (`SubscribeWithHandle`/
`SubscribeHandler`/`Publish`/`PublishHandle`) as legitimate, KEPT-PUBLIC
exceptions for advanced use — the user still wanted the "no spec needed"
call surface itself INVERTED into `api/events` (mirroring `Client.Attach`'s
own inversion exactly), rather than living per-adapter with 3 different
shapes: *"I want to rework the handle-based approach to be part of the
event module (invert the dependencies like we did in the new Client
approach). The idea is to have an event.PublishHandle(channel, adapter,
event) or event.SubscribeHandle(channel, adapter, handler)."* Also
requested: making `events.NewClient`'s `Info` optional (today a
mandatory positional arg, even though `Channel.WithSubscribe(...).Handle(nil)`
already builds a fully-working spec-free handle with ZERO `Client`
involved — the friction was specifically about the `Client`+`Attach` path
requiring spec ceremony even when unwanted), and giving `adapters/mqtt5`/
`adapters/mqtt` new broker-connection-owning capability (both already
hard-depend on `paho.golang`/`paho.mqtt.golang` at the type level).

**A critical design question was raised and answered before finalizing
the shape**: *"Are Go interfaces the right choice... for a route or
channel [adapter]?"* Investigated this codebase's OWN established
precedent for "what must an adapter implement to plug into a
route/channel/port" — `ports.SourceAdapter[T any]`/`ports.SinkAdapter[T any]`/
`ports.IOAdapter[Req,Resp any]`/`ports.ToolAdapter[In,Out any]` are ALL
GENERIC interfaces, each adapter providing a GENERIC CONSTRUCTOR function
(e.g. `zeromq.SubscribeAdapter[T any](sock, handle, fmt, opts) ports.SourceAdapter[T]`)
returning a per-T-instantiated concrete type — fully type-safe, ZERO
reflection, because Go forbids generic METHODS on a non-generic type but
happily allows a GENERIC TYPE's own methods to use its type parameter.
This is DIFFERENT from `events.Transport`/`rest.ServerTransport`/
`rest.ClientTransport` (Decision 5), which are non-generic, `any`-typed,
reflection-based — NOT because interfaces were the wrong tool there, but
because `Client.Publish`/`.Subscribe`/`Server.Serve`/`Client.Call` are
METHODS on the single, non-generic `*Client`/`*Server` value (one
instance serves every channel/route T in an app) — methods cannot
introduce their own type parameters, so THAT call shape genuinely has no
generic option. Decision 7's `PublishHandle[T]`/`SubscribeHandle[T]` are
being designed as FREE FUNCTIONS (never methods on a shared value), which
CAN have type parameters — so there is no structural reason to fall back
to reflection here; the adapter-facing interfaces below are GENERIC,
mirroring `ports.SourceAdapter[T]` exactly. An earlier draft of this
decision proposed a non-generic, byte/topic-level `HandleTransport`
interface — rejected as an unforced regression to `any`-shaped thinking.

**Resolved design:**

```go
// api/events — two new GENERIC interfaces (mirrors ports.SourceAdapter[T]/SinkAdapter[T]):
type PublishTransport[T any] interface {
    Publish(ctx context.Context, handle *ChannelHandle[T], msg T) error
    AdapterName() string
}
type SubscribeTransport[T any] interface {
    Subscribe(ctx context.Context, handle *ChannelHandle[T], fn func(context.Context, T) error) error
    AdapterName() string
}

// api/events — shared GENERIC helper functions (functions CAN be
// generic; this is what lets the adapter-specific PROTOCOL logic stay
// thin while the merge-field/topic-derivation logic — duplicated 3x
// today across zeromq/mqtt5/mqtt's own Publish[T]/PublishHandle[T]/
// SubscribeWithHandle[T] — gets exactly ONE source of truth):
func EncodeAndBuildTopic[T any](handle *ChannelHandle[T], msg T, formats ...format.Format[T]) (topic string, payload []byte, err error)
func DecodeAndMergeVars[T any](handle *ChannelHandle[T], topic string, payload []byte, formats ...format.Format[T]) (T, error)

// api/events — the actual call-time surface (mirrors the user's own
// requested shape exactly): channel here is the role-scoped
// Publisher[T]/Subscriber[T] value from Channel.WithPublish/
// WithSubscribe — NO *events.Client involved anywhere in this path.
func PublishHandle[T any](ctx context.Context, pub Publisher[T], adapter PublishTransport[T], msg T) error {
    handle, err := pub.Handle(nil) // spec-free, mirrors Client.Publish's internal Handle(client) call with client=nil
    if err != nil { return err }
    return adapter.Publish(ctx, handle, msg)
}
func SubscribeHandle[T any](ctx context.Context, sub Subscriber[T], adapter SubscribeTransport[T], fn func(context.Context, T) error) error {
    handle, err := sub.Handle(nil)
    if err != nil { return err }
    return adapter.Subscribe(ctx, handle, fn)
}
```

Each adapter provides GENERIC constructor functions (mirrors
`zeromq.SubscribeAdapter[T]` exactly):

```go
// adapters/mqtt5 (new):
func NewPublishTransport[T any](client MQTTClient, opts PublishTransportOptions) events.PublishTransport[T]
type PublishTransportOptions struct { QoS byte; Retained bool; ContentType string; UserProperties []UserProperty }

func NewSubscribeTransport[T any](client MQTTClient, router MQTTRouter, opts SubscribeTransportOptions) events.SubscribeTransport[T]
type SubscribeTransportOptions struct {
    TopicFilter  string // empty = derive via deriveWildcardFilter, unchanged from today
    QoS          byte
    OnError      func(SubscribeError)
    Observer     stats.Observer
    SecurityFunc func(ctx context.Context, msg *pahomqtt5.Publish, reqs []route.SecurityRequirement) error // stays mqtt5-NATIVE
}
// adapters/mqtt (v3): same pattern, SecurityFunc stays pahomqtt.Message-shaped (unchanged, matches its own
// documented no-op-for-credentials limitation). adapters/zeromq: same pattern, SecurityFunc stays *T-shaped
// (unchanged — zeromq's SecurityFunc already operates on the decoded value, nothing new needed).
```

Each concrete `mqtt5PublishTransport[T]`/`mqtt5SubscribeTransport[T]`
(etc.) is a small generic struct whose `Publish`/`Subscribe` methods call
`events.EncodeAndBuildTopic[T]`/`events.DecodeAndMergeVars[T]`
internally, then layer the adapter's own protocol-specific send/receive
around it. **A direct, welcome consequence of this shape**: since each
adapter's transport is its OWN concrete type with its OWN options struct,
there is NO forced cross-adapter unification of `SecurityFunc`'s shape
needed (an earlier draft proposed forcing all 3 adapters' `SecurityFunc`
into one `headers map[string]string`-based signature — dropped as
unnecessary complexity once the interface itself went generic).

**Also confirmed as part of this decision:**

- `SubscribeWithHandle[T]`/`Publish[T]`/`PublishHandle[T]` (zeromq,
  mqtt5) and `SubscribeHandler[T]`/`Publish[T]`/`PublishHandle[T]`
  (mqtt v3) are REPLACED (removed, not deprecated — same "replace, not
  deprecate" precedent as Decision 6) — their logic relocates into each
  adapter's new transport types, consumed exclusively via
  `events.PublishHandle`/`events.SubscribeHandle` going forward.
- `events.NewClient(info Info, opts ...ClientOption) *Client` becomes
  `events.NewClient(opts ...ClientOption) *Client` + a new
  `events.WithInfo(info Info) ClientOption` — BREAKING signature change,
  every call site migrates to `events.NewClient(events.WithInfo(info))`.
  Zero-value `Info{}` is used when `WithInfo` is never called.
- `adapters/mqtt5`/`adapters/mqtt` gain a NEW, ADDITIVE
  `Connect(ctx context.Context, brokerURL string, opts ConnectOptions) (...)`
  entry point wrapping `paho.golang`/`paho.mqtt.golang`'s own connection
  APIs — today's `Attach` (unchanged signature, still takes an
  already-connected client) remains for callers needing full paho
  control (custom TLS/keepalive/reconnect). **Confirmed: no env-var
  reading inside go-codex itself** (verified: zero `os.Getenv` calls
  anywhere in `adapters/*`/`api/*` non-example code, unchanged by this
  decision) — `Connect` takes `brokerURL` as a plain string; sourcing it
  from an env var remains the calling APPLICATION's job, exactly like
  REST's baseURL already works.
- **`adapters/zeromq` connection-ownership is explicitly DEFERRED** — a
  real, confirmed finding: `adapters/zeromq` has ZERO dependency on any
  concrete ZMQ library today (fully abstracted behind `FramedSocket`,
  with an explicit, documented "no CGO in the adapter" design goal in
  its own doc comments) — adding a `zeromq.Connect(brokerURL, ...)` would
  force a hard `pebbe/zmq4` (CGO) dependency onto EVERY consumer of the
  package, including ones never using the new capability. NOT
  implemented this phase — see "Remaining open items" below for the
  deferred options (accept the CGO cost outright; isolate it in a new
  `adapters/zeromq/zmq4` sub-package; or continue skipping entirely).
  zeromq DOES still get the handle-transport INVERSION itself
  (`NewPublishTransport[T]`/`NewSubscribeTransport[T]`, caller-connects-
  the-socket, exactly as `zeromq.Attach` works today) — inversion and
  connection-ownership are ORTHOGONAL; only the latter is deferred.

**Explicitly out of scope for Decision 7** (same boundary as Decision 6):
`api/reqreply` and its mqtt5/zeromq reqreply-shaped functions (`Serve`,
`Call`, `CallHandle`, `ServeRouter`, `CallDealer`, etc.); `sql`/`redis`/
`file`/`mcpgo`/`mcp`/`llm`/`websocket`.

### Decision 8 — Observer + `ErrorChannel` parity for `Client.Attach` (IMPLEMENTED)

**Trigger**: a dedicated review of error handling and `stats.Observer`
integration across REST and events under the "thin adapter" `Client.Attach`
workflow (Decision 5), triggered by a direct user request to confirm both
concerns are handled declaratively now that adapters are reduced to thin
IO-binding layers. Companion REST-side fix documented in
`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s own addendum.

**Confirmed bug**: `mqtt5`, `mqtt` (v3), and `zeromq`'s `transport.go`
(the reflection-based shim behind `events.Client.Publish`/`.Subscribe`,
built by each adapter's `Attach`) called `stats.Observer` **NOWHERE AT
ALL** — confirmed via repo-wide grep returning zero hits for
`stats.`/`Observer` in any of the three files, before this fix. This
silently dropped ALL metrics/logging/tracing for every call made through
the "preferred" `Client.Attach` workflow, while the escape-hatch
primitives (`publish`/`subscribeHandler`/`subscribeWithHandle`, consumed
via `NewPublishTransport`/`NewSubscribeTransport` + `PublishHandle`/
`SubscribeHandle`) had always wired `stats.Observer` correctly
(`RecordPublish`/`RecordSubscribe`, `TraceObserver` spans,
`SecurityObserver` where applicable).

**Fix**: each adapter's `transport.Publish`/`transport.Subscribe` now
resolves `obs := stats.ObserverFromContext(ctx)` (ctx-only — `events.
Transport`'s interface shape has no per-call `Options` struct to carry an
explicit override, mirroring how `Client.Attach`'s v1 scope note already
documents no per-call format/QoS overrides either) and calls
`RecordPublish`/`RecordSubscribe` on every exit path, plus wires
`TraceObserver` spans identically to the escape-hatch primitives
(`"mqtt5.publish"`/`"mqtt.publish"`/`"zmq.publish"` span names, matching
each adapter's own existing `publish` function's span name exactly).

**Second confirmed gap, closed in the same pass**: a plain SUBSCRIBE
handler's returned domain error previously had NO declarative redirect
path — only the escape-hatch req-reply `Serve` (handler error → typed
reply) and the `SinkAdapter`/`PublishAdapter` binding (upstream stream
error → typed error-channel message) consulted a declared
`events.ErrorChannel`; the plain subscribe path (both `Client.Attach`'s
`Subscribe` AND the escape-hatch `subscribeHandler`/`subscribeHandle`/
`subscribeWithHandle`) simply forwarded every handler error straight to
`opts.OnError`, with no way to express "redirect this specific domain
error to a typed error-output topic" declaratively. **Fixed**: when the
caller's subscribe handler (`fn`) returns a non-nil error, EVERY adapter
now consults `handle.ErrorResponseFor(err)` FIRST — on a match with
action `events.ErrorRespond`, the typed payload is published to the
declared error-output topic (via the adapter's own native publish
primitive: `client.Publish`/`sock.SendFrames` as appropriate) and
`OnError` is SKIPPED; any other action (or no match) falls through to
`OnError` unchanged — mirroring `mqtt5PublishAdapter.handleUpstreamError`'s
existing action-dispatch precedent exactly, now extended symmetrically to
the subscribe side across all three adapters (`mqtt5`'s
`makeSubscribeMessageHandler`, `mqtt`(v3)'s `subscribeHandler`,
`zeromq`'s `subscribeWithHandle`, AND each adapter's `Client.Attach`
`Subscribe` reflection shim).

**Files changed**: `adapters/nethttp/clienttransport.go` (REST's
`Client.Attach` companion fix — see `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s
addendum for the REST-specific detail); `adapters/mqtt5/transport.go`,
`adapters/mqtt5/adapter.go` (+ `binding.go` call-site update);
`adapters/mqtt/transport.go`, `adapters/mqtt/adapter.go` (+ `binding.go`/
`caller.go` call-site updates); `adapters/zeromq/transport.go`,
`adapters/zeromq/adapter.go`. New tests in each package's
`transport_test.go`/`adapter_test.go` cover: `Client.Attach` RecordPublish/
RecordSubscribe/TraceObserver wiring (success and failure paths), and a
matched `ErrorChannel` redirecting a subscribe handler's domain error to
the declared error-output topic (both via `Client.Attach` and via each
adapter's escape-hatch subscribe primitive). Verified: `gofmt`/`go build`/
`go vet`/`go test` (including `-race` for `mqtt`/`mqtt5`/`zeromq`) all
green repo-wide; zero regressions in existing test suites.

### Decision 9 — Centralized format resolution on the handle (IMPLEMENTED)

**Trigger**: while proving Decision 8's Observer/`ErrorChannel` fix via
example rework, a deeper review of `Client.Attach`'s format handling
surfaced a second, unrelated bug of the same shape. The identical fix
also landed on the REST side in the same round — see
`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s Addendum 2 for the
REST-specific detail (`RouteHandle[Req,Resp]`'s mirror-image methods).

**Confirmed bug**: `Client.Attach`'s `Publish`/`Subscribe` reflection
shims (all three events adapters — `mqtt5`, `mqtt` v3, `zeromq`) always
called `ChannelHandle[T]`'s plain `Encode`/`Decode`, which are hardcoded
to JSON codec encode/decode by original design (their own doc comments
say so explicitly), silently ignoring a channel's declared
`WithFormats`/`WithPublishFormats`/`WithSubscribeFormats` (YAML, TOML,
Gob, or a custom binary codec). Every escape-hatch adapter primitive
(`publish`, `subscribeHandler`, `subscribeWithHandle`) DID correctly
resolve and honor this declaration, but duplicated the identical
resolution logic inline at its own call site ("call-time override >
declared SubscribeFormats/PublishFormats > declared Formats > fallback to
plain Encode/Decode") — `Client.Attach` was simply the one caller that
never had this logic at all. A route/channel declared with a non-JSON
format would silently break the moment a caller switched from the escape
hatch to `Client.Attach`. This is **not** about per-call format overrides
(which stay legitimately out of `Client.Attach`'s scope, same as
Decision 5's other documented v1 limitations) — it is about the
channel's own **declared** format, the single most basic contract a
handle makes about its own wire shape.

**Fix**: rather than teach `Client.Attach` to duplicate the same
resolution logic a fourth time, the logic moved **onto
`ChannelHandle[T]` itself** — the single source of truth for a channel's
own declared configuration — and every existing escape-hatch adapter
primitive was refactored to call the new canonical method too, deleting
its own duplicated copy. This makes **every** adapter (`Client.Attach`'s
shims AND the escape-hatch primitives) a thin caller of one method; the
declaration lives in exactly one place. New methods on
`ChannelHandle[T]` (`api/events/builder.go`):

```go
// EffectivePublishFormats returns the full candidate format slice for a
// publish: call-time override (if any) > declared PublishFormats > declared
// Formats. Exposed as its own method (not folded into EncodeWithFormats)
// because mqtt5's ContentType-property auto-match needs to scan every
// candidate, not just take the winner.
func (h *ChannelHandle[T]) EffectivePublishFormats(formats ...format.Format[T]) []format.Format[T]

// EncodeWithFormats resolves EffectivePublishFormats and encodes with the
// winner, falling back to plain Encode if the list is empty.
func (h *ChannelHandle[T]) EncodeWithFormats(msg T, formats ...format.Format[T]) ([]byte, error)

// EffectiveSubscribeFormats mirrors EffectivePublishFormats for the
// subscribe direction (declared SubscribeFormats > declared Formats).
func (h *ChannelHandle[T]) EffectiveSubscribeFormats(formats ...format.Format[T]) []format.Format[T]

// DecodeWithFormats resolves EffectiveSubscribeFormats and decodes with
// the winner (no topic-var merge — callers needing separate decode vs.
// merge error reporting, like mqtt5's ContentType path, use this alone).
func (h *ChannelHandle[T]) DecodeWithFormats(payload []byte, formats ...format.Format[T]) (T, error)

// DecodeMergedWithFormats composes DecodeWithFormats + the existing merge
// step — the one-call convenience used by Client.Attach's Subscribe shim.
func (h *ChannelHandle[T]) DecodeMergedWithFormats(payload []byte, topicVars map[string]string, formats ...format.Format[T]) (T, error)
```

**Adapters refactored to call the canonical methods**:

- `adapters/mqtt5/adapter.go` — `publish`'s encode step now calls
  `handle.EncodeWithFormats(msg, formats...)`; `subscribeWithHandle`'s
  effective-format resolution now calls
  `handle.EffectiveSubscribeFormats(formats...)` (unchanged:
  `makeSubscribeMessageHandler` keeps its own MQTT5-specific
  ContentType-property auto-match pre-step, a genuine protocol/wire
  concern that stays in the adapter).
- `adapters/mqtt/adapter.go` (v3) — `publish`'s encode step and
  `subscribeHandler`'s decode step collapse to
  `handle.EncodeWithFormats`/`handle.DecodeWithFormats` (no ContentType
  concept in v3, so the whole block collapses).
- `adapters/zeromq/adapter.go` — same collapse in `publish`/
  `subscribeWithHandle`.
- **`Client.Attach`'s three reflection shims** (`mqtt5`/`mqtt`/`zeromq`'s
  `transport.go` `Publish`/`Subscribe`) now call the SAME canonical
  methods via `reflect.Value.MethodByName(...)` with zero-length
  variadic (no call-time override — `Client.Attach` still has no
  per-call override concept, unchanged from Decision 5's documented v1
  scope).

Net effect: every adapter's `publish`/`subscribeHandler`/`Client.Attach`
shim shrinks (duplicated resolution logic deleted); the only logic left
in each adapter is genuine protocol/wire-specific work (envelope
construction, QoS, MQTT5's ContentType property matching) — exactly the
"thin adapter, pure IO" principle already established for
Decision 5/6/7/8.

**Tests**: canonical method unit tests in `api/events/builder_test.go`
(declared-format-wins, call-time-override-wins-over-declared,
empty-falls-back-to-plain-Encode/Decode, merge still runs after
`DecodeMergedWithFormats`); existing escape-hatch adapter tests
(YAML/ContentType-match/merge-field coverage already in each
`adapter_test.go`) pass unmodified, confirming the refactor preserves
100% existing behavior; new round-trip tests proving `Client.Attach` now
honors a declared YAML format end-to-end —
`TestAttach_ClientPublishSubscribe_HonorsDeclaredYAMLFormat` in
`adapters/mqtt5/transport_test.go`, `adapters/mqtt/transport_test.go`,
and `adapters/zeromq/transport_test.go`.

**Verified**: `gofmt`/`go build`/`go vet`/`go test` all green repo-wide;
`just check` (staticcheck + gosec) with no new suppressions; zero
regressions in existing test suites.

### Decision 10 — Final gap-closure pass, ahead of graduating this doc to `docs/design/` (IMPLEMENTED)

**Trigger**: a direct user request to review this doc against the
shipped implementation, check for missed goals or newly-introduced
escape hatches, and confirm whether it is ready to graduate from
`docs/roadmap/` to `docs/design/` (the "fully shipped AND establishes a
pattern multiple packages follow" bar — see `docs/design/index.md`,
already met once before by
[Middleware Workflow Simplification](d-0001-rest-middleware-workflow-simplification.md)).

**Method**: every concrete claim in this doc (Decisions 1-9, the
Migration Checklist, every "Escape hatches" item, every "Remaining open
items" entry) was re-verified directly against the shipped code via
targeted grep/inspection of `api/events`, `adapters/mqtt5`,
`adapters/mqtt`, `adapters/zeromq`, `ports`, and
`.github/instructions/go-codex.instructions.md`.

**Result: no new escape hatches were introduced**, and every previously
flagged escape hatch that this doc claims is resolved was confirmed
resolved in code (role-scoped `Subscriber[T]`/`Publisher[T]`,
`Channel.Register`/`ClientHandle` fully removed with zero remaining call
sites, `CheckCoverage`/`checkImplementationsDeclared` wired
unconditionally, `ports.EventPattern.Subscribe`/`Publish` dedicated
fields, `TopicFilter`/`deriveWildcardFilter`/`deriveTopicPrefix`,
Decision 6's exact removal/kept-exception list verified symbol-by-symbol
across all three adapters plus REST/`nethttp`/`chi`). Two things were
found and fixed:

1. **One real, if latent, code gap**: `api/events/handletransport.go`'s
   exported `EncodeAndBuildTopic`/`DecodeAndMergeVars` (added by
   Decision 7) still hand-rolled the OLD format-resolution logic
   (`formats > handle.PublishFormats/SubscribeFormats > handle.Formats >
   plain Encode/Decode`, duplicated inline) instead of delegating to
   Decision 9's canonical `ChannelHandle.EncodeWithFormats`/
   `DecodeMergedWithFormats` — the one place Decision 9's centralization
   pass missed. Confirmed via repo-wide grep that ZERO shipped adapter
   calls either function (all three adapters' `NewPublishTransport`/
   `NewSubscribeTransport` delegate to their own already-fixed
   `publishHandle`/`subscribeWithHandle`/`subscribeHandler` primitives
   instead), so the miss was latent — but both functions are PUBLIC API
   whose doc comments explicitly invited a third-party author writing a
   NEW `PublishTransport[T]`/`SubscribeTransport[T]` (for a transport
   this package doesn't ship an adapter for) to follow this exact
   pattern, meaning anyone doing so today would get non-canonical,
   drift-prone format logic. Fixed: both functions now delegate directly
   (`EncodeAndBuildTopic` calls `handle.EncodeWithFormats`;
   `DecodeAndMergeVars` becomes a one-line wrapper around
   `handle.DecodeMergedWithFormats`, which already does exactly
   "decode via resolved format, then merge topic vars" — a strict
   behavioral improvement, since `DecodeMergedWithFormats` also gracefully
   handles an empty payload the same way `DecodeMerged` always did,
   which the old hand-rolled body did not). Doc comments corrected to
   remove the now-false "each adapter's PublishTransport implementation
   calls this once internally" claim. New test:
   `TestEncodeAndBuildTopic_DecodeAndMergeVars_HonorDeclaredYAMLFormat`
   in `api/events/handletransport_test.go`, mirroring the round-trip
   tests Decision 9 added for the other 4 adapters.
2. **Documentation staleness** (not a code bug, but this doc no longer
   accurately described reality): the top status banner's "one phase
   remains genuinely open" claim about the `events.WithSecurityScheme`
   migration was stale — verified via grep that every EXTERNAL call
   site had already migrated to `FromSecurityScheme`, with only
   `api/events`'s own regression tests remaining (an intentional,
   permanent keep per Lessons Learned item 3, not pending work); the
   "Remaining open items" entry deferring `mqtt`(v3)/`zeromq`'s
   `Caller`/`ServeSubscribers` implementation detail to "a future
   adapter-wiring pass" was stale — verified via grep that all three
   adapters ship it today; and a trailing "Implementation has not
   started" sentence directly contradicted the rest of the doc. All
   three corrected in place (see the top banner and "Remaining open
   items" above).

**Confirmed accurate, no changes needed**: `adapters/zeromq`'s
connection-ownership deferral (a real, intentional, CGO-driven
tradeoff — not a gap); the REST-workflow-review reminder (genuinely
still open, but already fully and concretely tracked via spun-out
docs — `zeromq-security.md`, `reqreply-workflow-simplification.md`,
`common-middleware-architecture.md`, `protocol-native-features.md` —
all still "idea only"/"PLANNED", none blocking THIS doc's own
completion; the fifth spun-out item, REST's client-side general-purpose
`ClientMW` hook, has since been resolved and folded into
[d-0001's Addendum 3](d-0001-rest-middleware-workflow-simplification.md#addendum-3-client-side-general-purpose-clientmw-hook-closes-the-last-known-restevents-middleware-asymmetry)).

**Verified**: `gofmt`/`go build`/`go vet`/`go test` all green repo-wide
after the `handletransport.go` fix; zero regressions.

**Conclusion**: this doc is fully shipped, establishes the pattern all
three pub/sub adapters (`mqtt5`/`mqtt`/`zeromq`) AND REST (via Decision
6's unification) follow, and meets `docs/design/`'s graduation bar —
moved to `docs/design/d-0002-pubsub-workflow-simplification.md` (see
`docs/design/index.md` for its new entry; this doc's `docs/roadmap/`
listing is removed).

### Decision 11 — REST/events architectural-sync review (IMPLEMENTED)

**Trigger**: a direct user request, after `d-0001`'s (REST) and this
doc's own designs had both shipped, to review the two APIs against the
original goal stated when this whole pub/sub effort began: keep REST and
events in architectural/conceptual sync — including their adapters
staying equally thin — while respecting their genuinely different
communication patterns (REST: real client/server split, request/
response; pub/sub: broker-mediated, no server role, both publisher and
subscriber are clients of a channel).

**Method**: systematically compared `api/rest` vs `api/events`'s
exported type/method surfaces, `adapters/nethttp`/`chi` vs
`adapters/mqtt5`/`mqtt`/`zeromq`'s file structure and escape-hatch symbol
shapes, and middleware Fn-shape validation on both sides — judging each
apparent asymmetry against whether it's explained by the two APIs'
documented, fundamentally different communication patterns before
calling it a gap.

**Confirmed correct/intentional** (the large majority of the surface):
`rest.ServerTransport`/`ClientTransport` (REST's genuine two-role split)
vs. `events.Transport` (pub/sub's single-role model); `rest.RouteEntry`/
`SSERouteEntry` (server-side dispatch registry) vs.
`events.SubscriberEntry` (pub/sub's client-side registry, since there is
no server); `HandleMW`/`ClientMW` vs. `SubscribeMW`/`PublishMW` (identical
signature shape on both sides, `FromSecurityScheme`/`CheckCoverage`/
`checkImplementationsDeclared` mirrored symbol-for-symbol); `rest.
CallWithHandle`/`ServeOne` staying adapter-level public escape hatches
while pub/sub's equivalent (`SubscribeWithHandle`/`Publish`/
`PublishHandle`) got INVERTED into `api/events` itself by Decision 7 —
justified, since Decision 7 solved a genuinely PUB/SUB-SPECIFIC problem
(3 independent broker adapters each duplicating the SAME merge-field/
topic-derivation logic) that REST never had (a single HTTP mechanism,
with chi delegating via swapHandler rather than an independent
reimplementation); `events.Client`'s much richer method set
(`AddServer`/`AddSchema`/`AddGlobalSecurity`/`SubscriberEntries`/
`Attach`/`Publish`/`Subscribe`/`ServeSubscribers`/`AsyncAPISpec`/
`AppendTo`/`AddChannelItem`) vs. `rest.Client`'s minimal 2-method set —
correct, because pub/sub has no separate Server type (one `Client`
unifies "spec builder" + "subscriber registry" + "transport-attached
call surface"), while REST's spec-building lives entirely on
`rest.Server` instead, which carries the mirrored richer set.

**Two real, confirmed gaps found and fixed**:

1. **`events.PublisherClient[T]` was dead, unimplemented public API with
   a factually incorrect doc comment.** Zero implementations existed
   anywhere in the shipped codebase (confirmed via exhaustive grep for
   its exact 1-arg `Publish(ctx, msg T) error` signature — only a
   throwaway compile-check type in `api/events/builder_test.go`). Its
   own doc comment claimed `Client.Publish` "satisfies" it — false:
   `Client.Publish`'s actual signature is `Publish(ctx, pub any, msg any)
   error` (2 dynamic args), which structurally cannot satisfy a 1-arg
   interface. The same false claim was repeated verbatim in
   `adapters/mqtt5/doc.go` and `adapters/mqtt/doc.go`'s package doc
   comments, and a softer version in `docs/guides/zeromq.md`. Its only
   INTENDED implementer, `mqtt5.PublisherFor[T]`/`NewPublisherFor[T]`,
   was already explicitly deleted by Decision 6 ("confirmed dead code
   with zero internal callers once `Client.Publish` became the modern
   path") — but `PublisherClient[T]` itself was never revisited after
   that deletion. Its sibling `SubscriberServer` interface, by contrast,
   IS genuinely implemented by all 3 adapters (`var _
   events.SubscriberServer = (*caller)(nil)`) and fully load-bearing —
   confirming this was a real, ONE-SIDED asymmetry inside `api/events`
   itself, not a protocol-driven difference. **Fixed**: removed
   `events.PublisherClient[T]` entirely (same precedent as
   `PublisherFor[T]`'s earlier deletion); removed the dead
   `dummyPublisherClient[T]` compile-check type and its half of the
   combined interface-compliance test; fixed every doc-comment
   cross-reference that falsely claimed `Client.Publish` "satisfies"/
   "implements" it (`api/events/builder.go`'s `SubscriberServer` doc
   comment now self-contained; `adapters/mqtt5/doc.go`,
   `adapters/mqtt/doc.go`, `docs/guides/zeromq.md`,
   `docs/reference/project-structure.md`, `.github/instructions/
   go-codex.instructions.md`).
2. **`adapters/mqtt5/doc.go` and `adapters/zeromq/doc.go`'s package doc
   comments were stale** — still described `SubscribeWithHandle`/
   `Publish`/`PublishHandle` as public/exported primitives and included a
   `zeromq.SubscribeWithHandle(...)` code example calling a function that
   no longer exists, even though Decision 7 unexported all of these
   (`subscribeWithHandle`/`publish`/`subscribe` lowercase in every
   adapter) in favor of the canonical `events.PublishHandle`/
   `SubscribeHandle` + `NewPublishTransport`/`NewSubscribeTransport`
   path. `adapters/mqtt/doc.go` (v3) was ALREADY correctly updated to
   describe this shape — confirming the staleness was inconsistent
   ACROSS the 3 adapters, itself a form of "not in sync" this very
   review was looking for. **Fixed**: updated both files' doc comments
   and code examples to describe the current shape, mirroring
   `adapters/mqtt/doc.go`'s already-correct wording as the reference.

**Confirmed, real, but correctly out of scope at the time**: REST's
`ClientMW` accepted only the credential-Fn shape, while events'
`PublishMW` additionally accepted a general-purpose wrapping shape — a
genuine asymmetry, but NOT new and NOT silently dropped: tracked in its
own dedicated roadmap doc at the time ("idea only, no driver yet"). Not
re-opened or fixed by this pass — **since resolved**, in a later round,
by mirroring `PublishMW`'s shipped `wrapPublishGeneral` precedent
exactly; see
[d-0001's Addendum 3](d-0001-rest-middleware-workflow-simplification.md#addendum-3-client-side-general-purpose-clientmw-hook-closes-the-last-known-restevents-middleware-asymmetry).

**Verified**: `gofmt`/`go build`/`go vet`/`go test` all green repo-wide;
`just check`/`just examples` clean; zero remaining references to
`PublisherClient` anywhere in the repo.

**Conclusion**: aside from the two fixes above (now closed), REST and
events are confirmed architecturally in sync given their different
communication patterns — every apparent difference in the remaining
surface traces directly to a real protocol/role difference between a
genuine client/server split (REST) and a broker-mediated, no-server-role
model (pub/sub), not to an unintentional drift between the two designs.

## Escape hatches that exist today (pub/sub-scoped, fresh audit)

1. ~~**Role-asymmetry gap**~~ — **RESOLVED** by Decision 1's
   `Client`-centric `Subscriber[T]`/`Publisher[T]` builder model — a
   subscriber-only or publisher-only process can now build a correctly
   role-scoped handle via its own `*events.Client`, with or without spec
   registration. Also, as a byproduct, **the per-role Security gap that
   an EARLIER, flatter draft of this same decision introduced is
   resolved too** — `Subscriber[T]`/`Publisher[T]` each carry their own
   independent `Use()`, so subscribe and publish can declare genuinely
   different security requirements against the same channel, matching
   what `events.Subscribe{Security:...}`/`Publish{Security:...}` already
   supported at the spec-metadata level.
2. ~~**`events.WithSecurityScheme` declared with no matching
   `SubscribeMW`/`PublishMW` implementation ever attached**~~ —
   **FULLY RESOLVED, per the escape-hatch simplification review.**
   `CheckCoverage` (signature already fixed by Decision 1) is now wired
   UNCONDITIONALLY into `Subscribe`/`SubscribeWithHandle`/`Serve`,
   exactly mirroring REST's guarantee (a declared `Security` scheme with
   no attached `SubscribeMW` is now a hard `MissingSecurityMiddlewareError`-
   equivalent, always, no opt-out). Confirmed this does NOT conflict
   with connection-level `SecuredClient` auth (escape hatch 9): per
   `docs/features/security.md`, connection-level auth is configured
   ENTIRELY in the caller's own connect code, BEFORE any `Channel` is
   touched — it NEVER appears as a declared `Security` requirement via
   `.Use(events.FromSecurityScheme(...))` at all (see "`events.WithSecurityScheme`
   REMOVED" under Decision 1 — `FromSecurityScheme` is `WithSecurityScheme`'s
   replacement), so `CheckCoverage` only ever fires for schemes that
   WERE declared — exactly the case that should always have a matching
   implementation. See "Security model: two mechanisms, not three"
   above for the full reasoning.
3. **`SecurityScheme.Codec` nil = no format validation** on the raw
   credential — documented, same as REST/reqreply, carries over
   unchanged. (Reviewed during the escape-hatch simplification pass —
   confirmed KEEP: matches REST exactly, a legitimate "no fixed shape to
   validate, `fn` handles it" use case, not an enforcement gap.)
4. ~~**`SecurityFunc`/`CredentialFunc` are per-call `Options` fields, not
   declaration-attached**~~ — **RESOLVED for the SECURITY-relevant
   fields** — `SubscribeMW`/`PublishMW`, attached to `Subscriber[T]`/
   `Publisher[T]` at declare time, are now the ONE way to supply a
   verify/credential Fn; nothing lets a caller silently swap it per-call
   anymore. **Do NOT conflate this with `Subscribe`'s business handler
   `fn`, which is a SEPARATE concept and DELIBERATELY stays a call-time
   param** (see Decision 1's "why `Subscribe`'s handler stays call-time"
   subsection) — `fn`'s call-time nature was never a security concern
   and is not itself an escape hatch; it is the correct, imperative
   shape for "start consuming now." (An earlier draft of this item also
   flagged a naming collision between adapter `Subscribe`/`Publish`
   functions and `Channel`-level accessors of the same name — fully
   MOOT now: `Subscriber.Handle`/`Publisher.Handle` share nothing
   lexically with `adapters/mqtt5.Subscribe`/`.Publish`.) **Distinct
   from, and NOT resolved by, this item:** whether TWO DIFFERENT
   sources declaring a security REQUIREMENT (`Subscribe{Security:...}`
   vs. `.Use()`-attached middleware) could silently conflict was a
   SEPARATE gap — this item is about the Fn IMPLEMENTATION having one
   home, not about requirement-merge conflict detection. See "Merge/
   conflict-detection for security REQUIREMENTS" under Decision 1 for
   that fix (now resolved separately, via a ported
   `ConflictingSecurityDeclarationError`).
5. ~~**`zeromq` has literally no security mechanism at any layer**~~ —
   **SIGNIFICANTLY DE-RISKED, spun out to
   [ZeroMQ Security Mechanism](zeromq-security.md).** Every zeromq
   pub/sub call remains unconditionally unenforced TODAY (confirmed via
   exhaustive grep this pass, see capability matrix above), but this no
   longer requires inventing a new wire-level convention before ANY
   progress is possible — Decision 3's in-payload `*T`-write mechanism
   (discovered while resolving escape hatch 6) needs no wire change at
   all and applies to zeromq identically. The spun-out doc's remaining
   scope narrows to an OPTIONAL additional out-of-band frame-based
   mechanism plus the separate connection-level/CURVE question — a much
   smaller, more tractable investigation than originally scoped.
6. ~~**`mqtt` (v3) publish-side has no credential mechanism, by protocol
   limitation**~~ — **RESOLVED, scope corrected.** The original framing
   was too broad: MQTT 3.1.1's PUBLISH packet genuinely has no property
   field, so an OUT-OF-BAND, protocol-native credential (mirroring
   mqtt5's `UserProperty` output) remains permanently impossible for v3
   — that narrower fact still holds and must be documented so no future
   design assumes protocol-native parity with mqtt5 here. But Decision
   3's `*T`-write-access generalization gives `mqtt`(v3)'s `PublishMW` a
   genuinely NEW, fully-working IN-PAYLOAD credential mechanism instead
   — the PRACTICAL gap ("mqtt v3 publish-side can't do message-level
   security at all") is closed.
7. **Last-registered-wins on security-scheme name collisions** across
   channels sharing a client — documented, same policy as REST, silent
   (no error). **RELOCATED, not removed:** `events.WithSecurityScheme`
   itself no longer exists (removed — see "`events.WithSecurityScheme`
   REMOVED" under Decision 1); the SAME collision risk now applies to
   `.Use(events.FromSecurityScheme(...))`-declared schemes sharing a
   name — confirmed via `docs/features/security.md` that REST's OWN
   analogous removal kept the identical "last-registered route wins, no
   error" policy for its `.Use()`-declared schemes too. (Reviewed —
   confirmed KEEP: matches REST exactly, "define once as a shared
   value" is sufficient existing guidance.)
8. **`Client.AddGlobalSecurity`/nil-inherits/empty-means-none** — same
   3-state contract as REST — a caller can silently rely on inheritance
   without realizing a channel opted out or overrode it. (Reviewed —
   confirmed KEEP: a standard, load-bearing pattern shared with REST,
   not a leaky escape hatch.)
9. **Connection-level `SecuredClient` is entirely separate and
   uncoordinated with message-level `SecurityScheme`/`SecurityFunc`** —
   by design, both layers independent and composable, worth naming as a
   consequence rather than silently assumed. (Reviewed — confirmed
   KEEP: explicitly documented as intentional in
   `docs/features/security.md`; cross-checking would require
   application-specific semantics go-codex shouldn't own. See "Security
   model: two mechanisms, not three" above.)
10. **`Subscriber[T].Handle(client)`/`Publisher[T].Handle(client)`
    called multiple times for the SAME channel+client+role** (e.g.
    `Subscriber.Handle` called twice) — each call returns its OWN fresh,
    independent handle populated from THAT call's own currently-declared
    `SubscribeMW`/`PublishMW` implementations; nothing detects or
    prevents two DIFFERENT-content handles existing for the same
    role+channel+client simultaneously (no "last one wins" — both
    returned handles remain independently valid, an easy way to
    accidentally end up with two differently-configured handles for what
    is conceptually "the same" role). Only a genuinely DIFFERENT
    topic+different-`T` collision is caught (`ChannelTypeConflictError`,
    Decision 1) — same-role, same-topic, same-`T` redeclaration is an
    escape hatch, not validated, mirroring item 7's policy. (Reviewed —
    confirmed KEEP: checked REST's precedent first — `rest.Route.Use()`/
    `ClientMW()` use the IDENTICAL copy-on-write pattern, so
    `route.ClientHandle()` called on two divergently-configured `Route`
    variables has the SAME unaddressed scenario; REST never found this
    worth solving.) **Important scoping correction, found during a
    later critical review:** this item's "no last-wins, both returned
    values independently valid" claim describes ONLY `.Handle()`'s
    RETURNED VALUES — it does NOT (and, since the fix below, never did)
    extend to `Subscriber[T].Register(client)`'s SEPARATE registry
    slot. An EARLIER draft of the `Register`-equivalent mechanism
    conflated the two (having `.Handle()` ALSO write to that registry),
    which produced a genuine, confirmed bug: `Subscribe(fn)`'s internal
    `.Handle()` call could silently overwrite/unregister a
    `ServeSubscribers`-registered `Handler` for the same topic+client —
    contradicting this decision's own "fully independent paths" claim.
    Fixed by fully separating the two (`.Handle()` never touches the
    registry at all; only explicit `Register()` calls do, with their
    OWN, correctly-scoped last-registered-wins policy — see "A blocking
    gap" under Decision 1 for the full fix). This item's ORIGINAL claim
    about `.Handle()`'s returned values remains accurate and KEPT
    unchanged; the fix above is what makes that claim actually TRUE in
    practice now, rather than incidentally true only until
    `ServeSubscribers` was introduced.
11. **`Subscribe`'s two-tier naming (`Subscribe` vs. `SubscribeWithHandle`)
    is a SOURCE-BREAKING rename for existing direct callers of today's
    handle-based `Subscribe`** — not an ongoing escape hatch once
    migrated, but worth naming as a one-time migration cost distinct from
    every other item in this list (which describe STANDING, permanent
    behavior, not a one-time transition cost).
12. ~~**`Channel.Register(b)`/`Channel.ClientHandle()` were kept instead
    of removed during implementation, silently reopening escape hatch
    #2's entire guarantee**~~ — **FOUND AND FULLY RESOLVED by a
    post-implementation audit.** This doc's own Migration Checklist
    always said these two methods are REMOVED, fully replaced by
    `Subscriber[T].Handle`/`Publisher[T].Handle` — but the implementation
    pass initially kept them anyway (an undocumented deviation). Traced
    the runtime consequence: both old methods built a `*ChannelHandle[T]`
    with `Implementations`/`ClientImplementations` always `nil` and never
    called `CheckCoverage` — so a channel built via these methods that
    DECLARED a `Security` requirement got **zero runtime enforcement,
    silently, no error** — exactly what escape hatch #2's "unconditional,
    no opt-out" `CheckCoverage` guarantee was supposed to make
    impossible. The audit confirmed zero existing test combined a
    declared `Security` requirement with `Register`/`ClientHandle`
    (low blast radius), but both remained fully exported public API with
    no deprecation warning — any external caller could have hit this
    silently. **Resolved by completing the original plan**: both methods
    removed entirely; every call site (`api/events`'s own ~84-call-site
    regression-test suite plus a dozen adapter test/example call sites)
    migrated to `WithSubscribe`/`WithPublish` + `.Handle(client)` (or
    `.Handle(nil)` for `ClientHandle`'s old spec-free behavior).
13. ~~**Decision 1 explicitly promised a `checkImplementationsDeclared`/
    `events.UnknownMiddlewareImplementationError` check — the
    REST-mirrored reverse-direction sibling to `CheckCoverage` — but it
    was never actually implemented**~~ — **FOUND AND FIXED by the same
    post-implementation audit.** `rest.checkImplementationsDeclared`/
    `rest.UnknownMiddlewareImplementationError` exist and are called
    unconditionally by `Route.Register`; the `api/events` equivalent
    Decision 1 promised ("mirrors REST/reqreply's
    `UnknownMiddlewareImplementationError` exactly") was simply never
    written. Catches a `SubscribeMW` call PAIRED against a security
    scheme name that was never `.Use()`'d on the same channel (e.g. a
    copy-paste mistake reusing a different channel's
    `middleware.Middleware`) — lower severity than item 12 (doesn't
    silently disable enforcement of a REAL declared scheme, it's a
    "did you mean" / dead-Fn catch), but a genuine gap between this
    doc's own stated design and what shipped. Fixed: added
    `events.UnknownMiddlewareImplementationError`/
    `checkImplementationsDeclared`, called unconditionally alongside
    `CheckCoverage` in `Subscriber.Handle`/`Subscriber.Register`
    (subscribe-side only, same asymmetry as `CheckCoverage` — mirrors
    REST exactly).

## Remaining open items

- **NEW (Decision 7) — `adapters/zeromq` connection-ownership deferred.**
  Unlike `adapters/mqtt5`/`adapters/mqtt` (both already hard-depend on
  their respective paho package at the type level, making a
  connection-owning `Connect(ctx, brokerURL, opts)` straightforward),
  `adapters/zeromq` has ZERO dependency on any concrete ZMQ library
  today — fully abstracted behind `FramedSocket`, with an explicit,
  documented "no CGO in the adapter" design goal (the real `pebbe/zmq4`
  library requires CGO). Three options were identified but NOT decided:
  (1) accept the CGO cost outright as a direct dependency of
  `adapters/zeromq` itself; (2) isolate it in a NEW
  `adapters/zeromq/zmq4` sub-package, keeping the core package CGO-free
  for consumers who don't need connection-ownership; (3) continue
  skipping zeromq connection-ownership indefinitely (caller always
  brings their own connected `FramedSocket`, as today). Left for a
  future round to decide — `zeromq.Attach`/the new handle-transport
  inversion (`NewPublishTransport[T]`/`NewSubscribeTransport[T]`) are
  UNAFFECTED by this and ship normally (caller-connects-the-socket,
  unchanged from today).
- ~~Naming: whether to adopt "Caller"/"Server" terminology for
  `reqreply`~~ (out of scope for this doc, deferred to its own
  dedicated review pass once this doc is fully design-complete) —
  pub/sub's own naming (`Client`/`Subscriber[T]`/`Publisher[T]`/
  `WithSubscribe`/`WithPublish`/`SubscribeMW`/`PublishMW`/`Handle`/
  `ServeSubscribers`/`SubscriberServer`) is now RESOLVED by Decision 1
  above.
- ~~The bare `Subscribe` name's final tier assignment~~ — **LOCKED IN.**
  `Subscribe`=value-based convenience, `SubscribeWithHandle`=handle-
  based primitive, matching REST's `Call`/`CallWithHandle` exactly, no
  longer TBD.
- ~~The whole-client `Serve`'s generic dispatch mechanism~~ —
  **RESOLVED.** Mirrors REST's OWN EXISTING `HandlerOpts`/`WithOptions`/
  `resolveOptions` pattern exactly — NOT harder than REST's problem, as
  previously assumed. See "`Client.SubscriberEntries()` +
  `ServeSubscribers`'s generic dispatch mechanism" under Decision 1.
- ~~`events.Client`'s entries-accessor shape~~ — **RESOLVED.**
  `Client.SubscriberEntries() []SubscriberEntry`, a new sealed
  interface mirroring `rest.RouteEntry` exactly, backed by a new
  `sync.RWMutex`-guarded registry. See the same subsection.
- **NEW this pass, found and fixed before write-back — a `Serve`
  naming collision.** `adapters/mqtt5`/`adapters/zeromq` ALREADY export
  `Serve[Req, Resp any]` for `reqreply` — the whole-client entry point
  was RENAMED to `ServeSubscribers` throughout to avoid the collision
  (Go doesn't support overloading).
- **NEW this pass — a blocking gap found and fixed.** `.Handle(client)`'s
  deliberately-unretained design (the B2 fix) meant `ServeSubscribers`
  had NOTHING to invoke — resolved via a mutex-guarded registry with 2
  decoupled slots (spec-copy value; a replaceable, never-mutated
  reference for `ServeSubscribers`). See "A blocking gap, found and
  fixed this pass" under Decision 1.
- **NEW this pass — a shared, transport-agnostic `SubscriberServer`
  interface**, confirmed to add: `events.SubscriberServer{
  ServeSubscribers(ctx) error }`, implemented by each adapter's
  `*Caller`. Whether REST/`nethttp`/`chi` should adopt an ANALOGOUS
  interface for `Serve` is spun out to its own doc —
  [d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary)
  — after finding a real shape mismatch (REST's `Serve` wires-and-
  returns immediately; pub/sub's `ServeSubscribers` blocks-and-runs)
  that needs its own investigation.
- ~~`Caller`'s mirroring across `mqtt`(v3)/`zeromq`~~ — **CORRECTED
  and FULLY RESOLVED (verified via a later code audit).** `zeromq`'s
  original claim was confirmed accurate (its existing `sock`-taking
  shape maps cleanly onto the two-tier split, no new capability
  needed). `mqtt`(v3)'s original claim was WRONG — checked the actual
  code and found no router, no bare `Subscribe` at all (only the
  lower-level, unchanged `SubscribeHandler` closure-builder) —
  `mqtt`(v3) needed GENUINELY NEW capability (a higher-level
  `Caller`/`Subscribe`/`ServeSubscribers` wrapping the existing
  primitive), not a mechanical rename. **Both now SHIPPED**: all three
  adapters (`mqtt`, `mqtt5`, `zeromq`) confirmed via code to have
  `Caller`(unexported by Decision 6)/`ServeSubscribers`/the two-tier
  `Subscribe`/`SubscribeWithHandle` split — no per-adapter
  implementation detail remains deferred.
- ~~`zeromq`'s Fn shapes for both directions... needs a new wire-level
  credential convention~~ — **RESOLVED, scope narrowed.** Decision 3's
  `*T`-write-access mechanism gives zeromq a fully-tractable Fn shape
  with NO wire-level convention needed. What remains open (its own
  optional out-of-band frame mechanism, connection-level/CURVE) is spun
  out to [ZeroMQ Security Mechanism](zeromq-security.md) — zeromq's
  `Caller`/`ServeSubscribers`/two-tier `Subscribe` mirroring SHIPPED (see
  the item above); only the spun-out doc's own narrower remaining
  question is still open.
- ~~Coverage-check enforcement semantics during Decision 3's adapter
  wiring~~ — **RESOLVED.** `CheckCoverage` is now wired unconditionally
  (escape hatch #2, "Security model: two mechanisms, not three"
  section) — this is settled for pub/sub, though the analogous question
  in the `reqreply`-scoped doc remains its own open item there.
- ~~Whether pub/sub "scopes" is a meaningful concept to enforce via
  `middleware.CheckScopes` for every credential kind~~ — **RESOLVED,
  confirmed non-issue for PUB/SUB.** `route.Satisfied`/`scopesSatisfied`
  already degrade gracefully to plain pass/fail when a requirement
  declares zero scopes — nothing forces OAuth2-style scoping on
  credential kinds without a natural scope concept (bare API key,
  pre-shared secret). The analogous question in the `reqreply`-scoped
  doc is UNCHANGED by this finding (not re-litigated here) — its own
  dedicated review remains deferred.
- The `reqreply`-scoped review (whether its own fixed `Register`/
  `ClientHandle` split — genuinely different from pub/sub's, since
  request-reply has a real requestor/replier asymmetry, unlike pub/sub's
  symmetric client roles — needs ANY equivalent restructuring, likely
  none) is explicitly deferred until this doc is fully design-complete.
- **Reminder for when this doc reaches full finalization**: a dedicated
  review of REST's OWN shipped design against this doc's SAME 2 goals
  (simple/declarative workflow; transport/protocol-agnostic abstraction)
  is still owed — this doc's OWN reviews repeatedly surfaced REST-side
  gaps only as BYPRODUCTS (never as the target of a dedicated REST
  pass): see
  [d-0001's Addendum 5](d-0001-rest-middleware-workflow-simplification.md#addendum-5-servertransportclienttransport-serverattachserverctx-and-clientnethttpattachcall--the-transport-agnostic-attach-then-call-vocabulary)'s
  own "carries a reminder" note (found `Serve`'s wire-vs-block shape
  mismatch as a byproduct of reviewing PUB/SUB's own goals, explicitly
  NOT a dedicated REST review), plus REST's `ClientMW` general-purpose
  hook gap (found while reviewing pub/sub's OWN middleware concept,
  since resolved — see
  [d-0001's Addendum 3](d-0001-rest-middleware-workflow-simplification.md#addendum-3-client-side-general-purpose-clientmw-hook-closes-the-last-known-restevents-middleware-asymmetry)),
  [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md)
  (REST's `middleware.Middleware` struct carries fields only REST
  uses — found while reviewing pub/sub's OWN middleware params), and
  [Protocol-Native Feature Declarations](protocol-native-features.md)
  (a generalization that could also apply to REST's header/cookie/query
  params — found while reviewing pub/sub's OWN spec-adding middleware).
  None of these four docs was produced by actually SITTING DOWN and
  reviewing REST against "is this simple/declarative? is this
  transport/protocol-agnostic?" the way this entire doc has done for
  pub/sub — that dedicated pass is still owed, and should check whether
  these four found gaps are REST's ONLY gaps or whether a direct review
  turns up more. Not started.

**Implementation is now COMPLETE and verified** (this paragraph
originally read "Implementation has not started" — stale, pre-
implementation text never updated once Decisions 1-9 shipped; corrected
here). **Decision 1 is RESOLVED** (the former separate "Decision 2" is
folded into it), and every design gap raised during this doc's many
review passes has been closed (see the list above, and Decisions 1-9's
own status banners) — the only things left OUTSIDE this doc's own scope
are `zeromq`'s remaining, much-narrower open questions (spun out to
[ZeroMQ Security Mechanism](zeromq-security.md), still "idea only"), and
the REST-workflow-review reminder immediately above (already fully
tracked via its own 4 spun-out docs, all still "idea only"/"PLANNED —
no implementation yet" — none of these block THIS doc's own
completion). The per-adapter `Caller`/`ServeSubscribers` implementation
detail for `mqtt`(v3)/`zeromq` this paragraph used to defer is DONE (see
the corrected item above) — nothing adapter-shaped remains open for
pub/sub itself.
