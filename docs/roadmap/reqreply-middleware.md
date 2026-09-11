# ReqReply Middleware — declare/implement split for `api/reqreply`

> **Status:** Phase 0 + Phase 0b SHIPPED (mqtt5 AND zeromq); **Phase 1
> SHIPPED (mqtt5)**; **Phase 1b SHIPPED (mqtt5)** — closes the header/
> cookie-as-middleware parity gap for MQTT5 User Properties, request AND
> reply direction. Spun out of
> `docs/design/d-0004-reqreply-workflow-simplification.md`'s own "Decision 3"
> (which first proposed this, then explicitly deferred it — see that doc's
> "Remaining open items"), now that Phases 0-5 of the reqreply `Server`/
> `Client`/`Attach` rework have fully shipped and the doc has been promoted.
> All 4 originally-open design decisions are now LOCKED IN (see "Open design
> decisions" below — kept in place, marked resolved, not deleted, so the
> reasoning survives). **Full parity with REST is this doc's goal for the
> PRIMARY, well-established workflow** — declare (`.Use()`), implement
> (`HandleMW`/`ClientMW`), bind (`Route.Register`/`ClientHandle`), attach
> (`Server.Attach`/`Client.Attach`) — delivered by Phase 1 (the core
> declare/implement split) and Phase 1b (closes the header/cookie-as-
> middleware parity gap: User Property spec-adding middleware, request
> AND reply/response direction, confirmed spec-compliant against the
> real AsyncAPI 3.0 spec).
> **Phase 0 + Phase 0b — SHIPPED for mqtt5, outcome BETTER than
> originally planned.** Found during this doc's final review pass that
> `docs/design/d-0004-reqreply-workflow-simplification.md`'s own
> "kept `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer` permanently,
> mirrors REST's `ServeOne`/`CallWithHandle`" justification did NOT hold
> up against the real code: REST's versions are zero-duplication thin
> wrappers around `Serve`/`Call` itself; reqreply's old functions shared
> NO code with `AttachServer`/`AttachClient` — a genuine, separately
> maintained duplicate dispatch implementation, conflicting with the
> "adapter stays a thin IO wrapper, user only touches `api/*`" principle.
> D-0004's bullet was REOPENED and reversed there. Phase 0 closed the
> capability gap (merge-fields, per-call format overrides, `ErrorPattern`
> — what an earlier revision called "Phase 2"); Phase 0b then found,
> WHILE implementing planned new-named thin wrappers, that
> `reqreply.ServerTransport.Serve`/`ClientTransport.Call` are ALREADY
> single-route/single-call scoped — so `Serve`/`Call`/`CallHandle` could
> simply be rewritten to DELEGATE to `serverTransport`/`clientTransport`
> directly, achieving zero duplicate logic with their EXACT SAME
> signatures — NO deletion, NO breaking change, NO caller/example
> migration needed at all (superseding the original "delete + rebuild +
> migrate" plan entirely). A real, additional gap (missing
> `stats.TraceObserver` span support in the Attach-based dispatch) was
> found and closed along the way, via re-running the full pre-existing
> test suite. Phase 1/1b's OWN mechanism design is UNCHANGED by any of
> this — only the delivery ORDER changed, so `.Use`/`HandleMW`/`ClientMW`
> will be wired into exactly ONE adapter mechanism (`Attach`), never
> two. See "Phase 0 — Capability parity" and "Phase 0b — Escape-hatch
> de-duplication via delegation" below for full detail; "Critical Review
> (this round)" is otherwise UNCHANGED — its finding #3 (full-parity
> scope = primary workflow only) still holds.
> **MQTT3 stays pub/sub-only, permanently excluded from reqreply
> entirely** (Decision 4 of d-0004) — confirmed via code this is ALREADY
> fully realized (`adapters/mqtt` has zero `reqreply` references anywhere,
> and its OWN pub/sub `.Use()`/`SubscribeMW`/`PublishMW` middleware is
> already fully shipped, at parity with mqtt5's) — nothing left to do for
> MQTT3, no further scope needed here.
> [← Back to Roadmap](index.md)

## Motivation

`reqreply.Route[Req,Resp]` is structurally a **REST-shaped** boundary — ONE
route, ONE request/response pair, no Subscriber/Publisher role split the way
`api/events` has (pub/sub has no fixed server/client pairing; reqreply, like
REST, does: a server dispatches on registered routes, a client calls them).
So when a user reaches for reqreply after already knowing REST, the
middleware declaration workflow should feel like **the exact same workflow
they already know from REST** — not a "similar but different" cousin. This
doc's design source is `api/rest`'s shipped mechanism specifically, not a
blend of REST's and events' approaches.

`docs/design/d-0001-rest-middleware-workflow-simplification.md` shipped a
**codec-declared, declare/implement split** for cross-cutting concerns
(security, observability, general-purpose request/response enrichment) on
`rest.Route`/`rest.SSERoute`:

- **Declare** once, at route-declaration time: `.Use(mw)` attaches a
  `middleware.Middleware` (spec-contributing: security scheme + requirement,
  header/cookie/query param spec entries) or a
  `docs/design/d-0003-codec-declared-middlewares.md`-style codec-backed
  `Middleware[In,Out]` value (bundled via `WithReceive`/`WithSend` for
  route-AGNOSTIC reuse across many routes).
- **Implement**, separately, per role: `.HandleMW(mw, fn)` (server-side,
  verifies/enforces) and `.ClientMW(mw, fn)` (client-side, fulfills/supplies).
  Both build register-time-only `middleware.ServerImplementation`/
  `ClientImplementation` values — `Satisfies []string` gates a PAIRED
  implementation to a specific `.Use()`'d security scheme; empty `Satisfies`
  means general-purpose (runs unconditionally, e.g. logging/rate-limiting).
- **Coverage enforced automatically, in both directions**: `Register`-time
  `checkImplementationsDeclared` (an implementation naming a scheme nobody
  `.Use()`'d → `UnknownMiddlewareImplementationError`) and adapter-`Serve`-time
  `rest.CheckCoverage` (a declared security requirement with NO matching
  `ServerImplementation` → `MissingSecurityMiddlewareError`) — a route can
  never silently ship "declared but unenforced" or "implemented but
  undeclared" security.

`docs/design/d-0002-pubsub-workflow-simplification.md` later confirmed the
SAME `middleware` package types generalize cleanly to `api/events`'
`Subscriber[T]`/`Publisher[T]` too (`.Use`/`.SubscribeMW`/`.PublishMW`) — this
is useful evidence that the mechanism isn't REST-specific plumbing, but it is
cited here only as secondary confirmation, not as an alternate template:
reqreply's actual API surface below mirrors REST's `Route`, not events' role
pair.

`api/reqreply` shipped its OWN `Server`/`Client`/`Attach`/`CallAsync`/`Future`
simplification this session (`docs/design/d-0004-reqreply-workflow-simplification.md`,
Phases 0-5, fully implemented and verified) — but it still enforces security
the OLD way REST used BEFORE d-0001: a route declares
`reqreply.WithSecurityScheme(name, scheme)` + `RouteMeta.Security`, but the
actual credential VERIFICATION/SUPPLY function is a **per-call `Options`
field** — `adapters/mqtt5.ServeOptions.SecurityFunc` (server) /
`CallOptions.CredentialFunc` (client) — passed directly to `Serve`/`Call`,
not attached to the route/handle at declaration time. There is no
`.Use()`/`.HandleMW()`/`.ClientMW()` on `reqreply.Route`, no
`Implementations`/`ClientImplementations` fields on `RouteHandle`, and no
coverage-check safety net (a route CAN declare a security requirement with
an adapter that forgets to pass `SecurityFunc`, and nothing catches it).

This is the ONE remaining structural inconsistency between reqreply and its
REST sibling — everything else (declare-once constructors, `Server`/
`Client`+`Attach`, dual-mode `Call`, `CallAsync`/`Future`) already converged
during this session's Phase 0-5 work. Closing this gap means a user who
knows `rest.Route.Use(mw).HandleMW(&mw, fn)` already knows
`reqreply.Route.Use(mw).HandleMW(&mw, fn)` — zero new vocabulary.

## Critical Review (this round)

A dedicated critical pass over this doc, questioning whether "full
feature parity with REST" is even the right goal and whether it's
achievable — not just re-confirming prior work. Four findings, each
with its resolution (kept here permanently, mirroring this doc's own
"keep resolved reasoning visible" convention):

1. **Was the scope-grant model (Open design decision #4) invented
   without a real driver?** Initially flagged as a risk: reqreply has
   never had an OAuth2-scopes concept, and the doc's own text admitted
   adopting it would be "new capability, not a straight port" — locked
   in purely by REST-comparison, which risks violating this codebase's
   own "don't invent API without a user request" principle. **Resolved**:
   kept in scope, but re-justified on a real driver — bearer-token-plus-
   scopes is standard, widely-expected REST/OAuth2 practice, and a
   reqreply user arriving from REST reasonably expects it; that
   expectation IS the driver. A contingency was added: if implementation
   reveals this is bigger than expected, spin it into its own dedicated
   roadmap doc BEFORE Phase 1 ships (not after, since the Fn shape can't
   safely change once adapters implement against it).
2. **Was Phase 1b's AsyncAPI `Headers` field verified against the actual
   AsyncAPI 3.0 spec, or just against go-codex's own (absent) renderer
   support?** Only the latter, originally — a real, unverified-design
   risk. **Resolved**: confirmed via the real AsyncAPI 3.0 spec that
   Message objects officially support a `headers` field (a `type:
   object` Schema Object, application-level only) — Phase 1b's design
   updated to the confirmed shape (`Headers schema.Schema` on the real
   `render/asyncapi/v3.Message` struct, reusing the exact type `Schema`
   already uses).
3. **Is "full feature parity with REST" even the right SOLE yardstick?**
   REST's capability set and its `Attach` mechanism were co-designed
   together from day one (d-0001); reqreply's `Attach` mechanism was
   bolted onto a PRE-EXISTING escape hatch later (d-0004) — its
   capability gap is a historical byproduct of build order, not a
   designed omission, and Phase 2's (now Phase 0's) items (merge-fields,
   format overrides, `ErrorPattern`) had no INDEPENDENT driver beyond
   "REST has it." **Resolved (at the time)**: full parity remains the
   goal, but scoped explicitly to the PRIMARY workflow
   (declare/implement/bind/attach — Phase 1 + 1b); what was then called
   "Phase 2" was reframed as a documented record of escape-hatch/
   special-case DIFFERENCES from REST, closed opportunistically, not a
   hard parity requirement. **UPDATE, superseding the "opportunistic"
   framing above**: this finding's premise turned out to be
   INCOMPLETE, not wrong — the capability gap DOES still have no
   REST-comparison driver, but it turned out to have a DIFFERENT,
   independent driver: it's the sole remaining justification for
   `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer`'s continued
   existence as a genuine duplicate dispatch implementation (see
   D-0004's reopened bullet). Once that was found, closing the gap
   stopped being optional — it became a REQUIRED prerequisite (renamed
   "Phase 0") so the old functions can actually be retired (Phase 0b).
   This doc's own "full parity = primary workflow only" conclusion
   above is UNCHANGED and still correct; only Phase 0/0b's own
   REQUIRED-vs-opportunistic status changed, for a reason unrelated to
   the REST-parity question this finding was originally about — see
   "Phase 0 — Capability parity" and "Phase 0b — Escape-hatch
   retirement" below.
4. **No delivery-risk/staging discussion existed anywhere**, despite
   this being a large, multi-phase, Fn-shape-sensitive rework — this
   exact codebase's own history (d-0001's Lessons Learned) documents a
   real case where an unverified "equivalent" claim shipped and hid two
   genuine gaps. **Resolved**: see the new "Delivery risk & staging"
   section below.

## Control question, answered: does REST still have a `CredentialFunc`-as-Options-field escape hatch?

Asked and verified directly against the real code (`adapters/nethttp/client.go`)
before starting Phase 1: **no.** REST fully UNIFIED its escape hatch
(`Call`/`CallWithHandle`) with the declarative mechanism — there is only
ONE credential-supply path for REST today, not two coexisting ones.
Confirmed via code:

- `nethttp.CredentialFunc` is a documentation-only TYPE ALIAS naming the
  shape `func(ctx, reqs) (http.Header, error)` — the SAME shape
  `middleware.ClientImplementation.Fn` uses, NOT a separate
  `CallOptions`-level escape-hatch field. `CallOptions` (the struct
  `Call`/`CallWithHandle` take) has NO `CredentialFunc` field at all —
  its own doc comment for `HeaderParams` explicitly says "use ...  a
  credential-providing `middleware.ClientImplementation`" instead.
- The internal `call` function BOTH `Call`/`CallWithHandle` (escape
  hatch) AND `Client.Call` (Attach-based) share reads
  `handle.ClientImplementations` directly (`adapters/nethttp/client.go:605`)
  — the field ONLY `.Use()`/`.ClientMW()` populates. There is no
  parallel/legacy credential path left to choose between.

**This is exactly the state Phase 1 should bring reqreply to** — today,
reqreply's escape hatch AND `AttachClient` both still read credentials
from a per-`Attach`/per-call `CallOptions.CredentialFunc`/
`ServeOptions.SecurityFunc` field (the pre-D-0001 REST style, confirmed
still the ONLY mechanism reqreply has). Phase 1's `.Use()`/`HandleMW()`/
`ClientMW()` mechanism should aim for the SAME end state REST reached:
one declarative credential-supply path, consulted by BOTH entry points,
not a permanent parallel `CredentialFunc`-as-option escape hatch living
alongside the new declarative one. (`ServeOptions.SecurityFunc`/
`CallOptions.CredentialFunc` themselves are NOT going away — mirrors
REST's own `CallOptions.ExtraHeaders`/generic escape-hatch fields, which
still exist for genuinely un-declared, ad-hoc cases — but the PRIMARY,
declared-scheme credential path should converge onto `Implementations`/
`ClientImplementations`, the same way REST's did.)

## Scope decisions (what's in Phase 1, what's deferred) — SHIPPED (mqtt5)

| In scope | Out of scope |
|---|---|
| **SHIPPED.** `reqreply.Route.Use(mws ...middleware.RouteMiddleware) Route[Req,Resp]` — signature IDENTICAL to `rest.Route.Use` | `mqtt5.ServeOptions.SecurityFunc`/`CallOptions.CredentialFunc` retrofitted as a parallel, permanent mechanism — **REJECTED, and REMOVED ENTIRELY instead** (see Open design decision #4's reversal) — `Implementations`/`ClientImplementations` are now the ONLY mechanism, mirroring REST's own D-0001 precedent exactly, not a second escape hatch alongside the new one |
| **SHIPPED.** `reqreply.Route.HandleMW(mw *middleware.Middleware, fn any) Route[Req,Resp]` (server-side) — signature IDENTICAL to `rest.Route.HandleMW` | General-purpose request/response ENRICHMENT shapes beyond security (e.g. a "mutate the decoded Req before the handler runs" hook) — `rest.Route` doesn't have this either; not introducing new surface REST itself lacks |
| **SHIPPED.** `reqreply.Route.ClientMW(mw *middleware.Middleware, fn any) Route[Req,Resp]` (client-side) — signature IDENTICAL to `rest.Route.ClientMW` | `mqtt`(v3)'s Fn-shape design (permanently out of reqreply's scope per Decision 4 of d-0004 — publish-side has no credential mechanism by protocol limitation) |
| **SHIPPED.** `RouteHandle.Implementations []middleware.ServerImplementation` / `RouteHandle.ClientImplementations []middleware.ClientImplementation` fields, populated by `Route.Register`/`Route.ClientHandle` — mirrors `rest.RouteHandle`'s identical fields | `zeromq`'s Fn-shape design for Phase 1 (see "Toolchain / dependency decisions" — deferred to a follow-up, tracked as an explicit open item, not silently dropped — see `docs/roadmap/zeromq-security.md`'s own new reminder section) |
| **SHIPPED.** `mqtt5.AttachServer`/`AttachClient`'s reflection shim (`serverTransport.Serve`/`clientTransport.call`) reading `handle.Implementations`/`ClientImplementations` automatically | Retrofitting the OLD `Serve`/`Call`'s existing `Options.SecurityFunc`/`CredentialFunc` fields to ALSO consult `Implementations` — MOOT for a DIFFERENT reason than originally stated: those old functions were NOT retired (Phase 0b's actual outcome was delegation, not deletion) — but since they now DELEGATE to `serverTransport`/`clientTransport` directly, they automatically consult `Implementations`/`ClientImplementations` too, with zero additional wiring |
| **SHIPPED.** Register-time `checkImplementationsDeclared` equivalent (`UnknownMiddlewareImplementationError` when a `HandleMW`/`ClientMW` implementation names a scheme nobody `.Use()`'d) — reuses REST's exact check, not a reimplementation | Serve-time `CheckCoverage` equivalent for `Server.Serve`/`Client.Call`'s NEW Attach-based path specifically — SHIPPED for mqtt5, but zeromq's version depends on the deferred Fn-shape work above |
| **SHIPPED.** `reqreply.WithSecurityScheme`'s declare-time role is REPLACED by `.Use(middleware.SecurityScheme(...))` — full parity with REST's CURRENT state, not two parallel declare-time styles. `reqreply.WithSecurityScheme`/`reqreply.SecurityScheme` (the OLD types) are DEPRECATED-but-kept aliases, mirroring `events.WithSecurityScheme`'s precedent | Deleting `reqreply.WithSecurityScheme`/`reqreply.SecurityScheme` outright — kept as deprecated aliases for existing callers, zero breaking changes, same treatment `Builder`/`NewBuilder` got in d-0004 |
| **SHIPPED. Phase 1b — User Property param-as-middleware** (request AND reply/response direction) — see its own dedicated section below | Extending Phase 1b to `mqtt`(v3)/`zeromq` — mqtt5-only, same reasoning as the rest of Phase 1 |

### What actually shipped (mqtt5)

1. `api/reqreply/middleware.go` (NEW) — `Route.Use`/`HandleMW`/`ClientMW`,
   `applySecurityDeclarations`/`checkImplementationsDeclared`/
   `CheckCoverage`, `MissingSecurityMiddlewareError`/
   `UnknownMiddlewareImplementationError` — direct ports of
   `api/rest/middleware.go`'s equivalent structure, scoped down (no
   D-0003 codec-declared `Middleware[In,Out]` bundling, no param-spec
   merging — reqreply's boundary is topic-only, no path/query/header/
   cookie params to merge the way REST does).
2. `api/reqreply/route.go` — `RouteHandle.Implementations`/
   `ClientImplementations` fields, populated by `Route.Register`/
   `Route.ClientHandle` (both call `applySecurityDeclarations`;
   `Register` additionally calls `checkImplementationsDeclared`,
   mirroring REST's identical two-step sequence).
3. Fn-shape dispatch (folded into `adapters/mqtt5/reqreply_transport.go`
   directly — NOT a separate `reqreply_middleware.go` file, a
   short-lived organizational deviation caught and fixed the same
   session: confirmed via code that `adapters/nethttp` has NO
   equivalent dedicated file either — its Fn-shape dispatch lives
   inline in `serve.go`/`client.go`): server-side PAIRED (`func(ctx,
   msg *paho.Publish, reqs) (map[string][]string, error)`, scope-grant
   model) + UNPAIRED (`func(func(*paho.Publish)) func(*paho.Publish)`,
   general-purpose decorator); client-side PAIRED (`func(ctx, reqs)
   ([]UserProperty, error)`, REPLACES the old `CredentialFunc`'s exact
   shape/role) + UNPAIRED (`func(func(ctx,Req)(Resp,error))
   func(ctx,Req)(Resp,error)`, general-purpose decorator,
   reflection-only via `reflect.MakeFunc` since Req/Resp are erased at
   this dispatcher's call site).
4. `adapters/mqtt5/reqreply_transport.go` — `serverTransport.Serve`
   validates implementation shapes + calls `reqreply.CheckCoverage`
   ONCE at Serve construction time (not per-message), wraps the
   per-message handler with general-purpose decorators, runs paired
   security Fns via `runServerSecurityMiddleware` (replacing the OLD
   `SecurityFunc` call). `clientTransport.call` validates client
   implementation shapes, wraps the ENTIRE encode→publish→recv→decode
   sequence (built via `reflect.MakeFunc` into a concretely-typed
   closure) with general-purpose decorators, and merges paired
   credential Fns via `mergeCredentialUserProperties` (replacing the OLD
   `CredentialFunc` call) — applies identically through `CallAsync` too
   (same underlying `t.call`).
5. `adapters/mqtt5/reqreply.go` — `ServeOptions.SecurityFunc`/
   `CallOptions.CredentialFunc` fields REMOVED ENTIRELY (breaking
   change).
6. `examples/reqreply-api` — Demo 2/3 migrated a SECOND time (having
   just been migrated onto `CallOptions.CredentialFunc` in the previous
   round) onto `.Use()`/`.ClientMW()`, since `CredentialFunc` no longer
   exists; `routes/middleware.go` (NEW, `BearerAuthMw`), `handlers/
   security.go` (NEW, `VerifyBearer`); `mqtt5server/server.go`'s
   `SecuredComputeRoute`/`GlobalOnlyComputeRoute` registration updated
   to `.Use(...).HandleMW(...)`. **A real, pre-existing design gap was
   found and fixed along the way**: `ComputeRoute` (meant to be
   unsecured) silently INHERITED `Server.AddGlobalSecurity`'s
   requirement with ZERO enforcement, because the OLD credential-format
   check only ran for schemes with a `WithSecurityScheme` registered on
   THAT SPECIFIC route (which `ComputeRoute` never declared) — Phase 1's
   mandatory `CheckCoverage` correctly surfaces this as a hard
   `MissingSecurityMiddlewareError` instead of silently ignoring it;
   fixed by having `ComputeRoute` explicitly opt out via an EMPTY
   (non-nil) `RouteMeta.Security` slice, per `RouteHandle.Security`'s own
   documented "nil inherits, empty opts out" contract.
7. **9 new tests** in `api/reqreply/middleware_test.go` (the full
   declare/implement mechanism, isolated from any adapter) — see "Unit
   test plan" below for the complete list — PLUS **6 new tests** in
   `adapters/mqtt5/reqreply_test.go`/`reqreply_transport_test.go`
   replacing every SecurityFunc/CredentialFunc-based test, INCLUDING two
   that directly exercise the `reflect.MakeFunc`-based general-purpose
   client decorator wrapping (the single riskiest new code path this
   phase introduced) — **a real bug was caught by these two tests**:
   `validateClientImplementationShapes` was initially called with the
   WRONG reflect.Type (the decorator's INNER shape instead of the
   decorator shape itself), causing every general-purpose `ClientMW` to
   be rejected with a `MiddlewareShapeError` — fixed immediately, both
   tests then passed.

### Verification

`gofmt -l .` clean; `go build ./...` clean; `go vet ./...` clean;
`go test ./... -race` — zero FAIL across the ENTIRE repo; `just check`
(staticcheck + gosec) zero findings; `for d in examples/*/; do go run
.; done` — zero failures across every example, including
`examples/reqreply-api`'s full 7-demo narrative with its now-fully-
declarative security middleware.

## Toolchain / dependency decisions

No new external dependency — `middleware.Middleware`/`ServerImplementation`/
`ClientImplementation`/`RouteMiddleware` already exist and are
pattern-agnostic (built for exactly this kind of reuse — REST first, events
second, reqreply now the third). `reqreply.RouteOpt` is already the same
`interface{ applyRoute(*routeBuilder) }` shape `rest.RouteOpt` is — d-0004's
own Decision 3 confirmed this is "ready to extend with zero structural
changes," re-verified accurate against the current `api/reqreply/route.go`
before writing this doc.

**mqtt5 is the Phase 1 target, mqtt(v3)/zeromq are follow-up work — not a
simultaneous 3-transport rollout**, for the same reason d-0004 itself gave
and never resolved: MQTT5, MQTT3, and ZeroMQ have three incompatible native
message envelope types (`*pahomqtt5.Publish`, `pahomqtt.Message`, raw ZMQ
multipart frames), so each `ServerTransport`/`ClientTransport`'s `HandleMW`/
`ClientMW` Fn-shape recognition is adapter-specific, not universal — mirrors
how `middleware.ServerImplementation.Fn`/`ClientImplementation.Fn` are
already resolved per-adapter for REST (`func(http.Handler) http.Handler` /
`func(ctx, raw *http.Request, req *Req) (map[string][]string, error)`) and
events (mqtt5/mqtt/zeromq's own, already-shipped Publish/Subscribe Fn
shapes). `mqtt5`'s shape is confirmed the clearest translation target: its
existing `adapters/mqtt5/adapter.go`'s `makeSubscribeMessageHandler`/
`Publish` pattern (and the reqreply `Attach` shim's own reflection technique)
give a direct template. `mqtt`(v3) is out of scope entirely (Decision 4 of
d-0004: permanent protocol limitation, not a design gap). `zeromq`'s
Fn-shape was ORIGINALLY assumed to need its own dedicated design pass — raw
multipart frames carry no per-message metadata slot the way MQTT5's User
Properties do, so it was assumed a `ServerImplementation`/`ClientImplementation`
verifying/supplying a credential would need a NEW wire-level convention (an
additional frame) before any Fn shape could be finalized. **UPDATE: this
assumption turned out to be FALSE, resolved later in this same doc's
revision history** — see "Out of scope entirely"'s zeromq bullet and
`docs/roadmap/zeromq-security.md`'s own "Implication for
`reqreply-middleware.md`" section: zeromq's REQ/REP frames have the EXACT
SAME "no separate credential slot" shape pub/sub's `[topic, payload]`
frames do, so the SAME in-payload `*Req` mechanism already proven for
pub/sub applies here too, with NO new wire-level frame needed. zeromq
remains OUT of this doc's Phase 1 (mqtt5-only), but not because of an
unsolved wire-level question anymore — purely a sequencing choice (Phase 1
ships mqtt5 first; zeromq's Fn shape, though now DESIGN-DECIDED, is still
unimplemented).

## API surface

Every signature below is IDENTICAL to `rest.Route`'s equivalent — same
names, same parameter types, same nilable-`mw` semantics, same
immutable/chainable contract. This is deliberate: a reqreply user should be
able to read REST's own godoc/examples and apply them verbatim.

```go
// reqreply.Route gains the SAME three methods rest.Route already has —
// identical signatures, identical semantics (mw nilable, Satisfies-based
// pairing, immutable receiver, chainable).

func (r Route[Req, Resp]) Use(mws ...middleware.RouteMiddleware) Route[Req, Resp]

func (r Route[Req, Resp]) HandleMW(mw *middleware.Middleware, fn any) Route[Req, Resp]

func (r Route[Req, Resp]) ClientMW(mw *middleware.Middleware, fn any) Route[Req, Resp]
```

```go
// RouteHandle gains the same two fields rest.RouteHandle already has,
// populated by Route.Register/Route.ClientHandle exactly like today's
// SecuritySchemes/GlobalSecurity fields are.

type RouteHandle[Req, Resp any] struct {
    // ... existing fields (Topic, Decode, Encode, SecuritySchemes,
    // GlobalSecurity, etc.) unchanged ...

    // Implementations are the server-side middleware implementations
    // attached via Route.HandleMW, consulted by Server.Serve (via the
    // attached ServerTransport) instead of ServeOptions.SecurityFunc for
    // routes using the NEW Attach-based workflow.
    Implementations []middleware.ServerImplementation

    // ClientImplementations are the client-side middleware
    // implementations attached via Route.ClientMW, consulted by
    // Client.Call/CallAsync instead of CallOptions.CredentialFunc for the
    // NEW Attach-based workflow.
    ClientImplementations []middleware.ClientImplementation
}
```

```go
// mqtt5's ServerTransport/ClientTransport (adapters/mqtt5/reqreply_transport.go)
// recognize a NEW Fn shape for reqreply's HandleMW/ClientMW, alongside the
// package's own EXISTING events Subscribe/Publish Fn shapes — mirrors how
// adapters/mqtt5/adapter.go already recognizes multiple Fn shapes today.
// Confirmed against the REAL dispatch code before writing this (an earlier
// draft of this doc got the general-purpose shapes wrong — see
// "Corrections made against real code" below): PAIRED (security) Fns are
// plain verify/supply functions, called explicitly by the dispatcher —
// never decorators; UNPAIRED (general-purpose) Fns ARE decorators,
// composed outermost-in, in attachment order, exactly like REST's
// applyGeneralMiddleware/mqtt5's own wrapPublishGeneral.

// Server-side, PAIRED (Satisfies non-empty — security-verifying). FINALIZED
// (Open design decision #4): adopts REST's func(ctx, raw *http.Request, req
// *Req) (map[string][]string, error) shape's RETURN TYPE, not just its
// parameter shape — a scope-GRANT map, combined via middleware.CheckScopes,
// for full consistency with REST. reqreply's OLD SecurityFunc (error-only,
// no scopes) is UNCHANGED as the permanent escape hatch; only this NEW
// HandleMW-driven path adopts the scope-grant model.
func(ctx context.Context, msg *pahomqtt5.Publish, reqs []route.SecurityRequirement) (map[string][]string, error)

// Server-side, UNPAIRED (Satisfies empty — general-purpose). A REAL
// decorator over the per-message handler closure mqtt5's
// ServerTransport.Serve already registers via
// t.router.RegisterHandler(path, func(msg *pahomqtt5.Publish)) (confirmed
// exact signature, adapters/mqtt5/reqreply_transport.go:163) — this is
// reqreply's closest equivalent to REST's func(http.Handler) http.Handler,
// wrapping the SAME concrete "how do I process one arrived unit of work"
// handler type REST wraps:
func(next func(msg *pahomqtt5.Publish)) func(msg *pahomqtt5.Publish)

// Client-side, PAIRED (Satisfies non-empty — credential-supplying).
// Mirrors reqreply's OWN EXISTING CallOptions.CredentialFunc shape exactly
// (this part of the OLD mechanism's shape carries over unchanged) and
// REST's func(ctx, reqs) (http.Header, error) CredentialFunc shape's
// STRUCTURE (mqtt5.UserProperty being mqtt5's http.Header-equivalent):
func(ctx context.Context, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error)

// Client-side, UNPAIRED (Satisfies empty — general-purpose). A REAL
// decorator, generic over Req/Resp — mirrors REST's OWN client-side
// general-purpose shape BYTE-FOR-BYTE (rest.Route.ClientMW's Addendum
// 3/5 shape, examples/rest-api/routes/middleware.go's TimingClientMW),
// which is the MORE precise analogy here than mqtt5's events-side
// wrapPublishGeneral[T] (publish-only, no response leg) — reqreply's
// Client.Call has a real Req/Resp round trip, exactly like REST's Call:
func(next func(ctx context.Context, req Req) (Resp, error)) func(ctx context.Context, req Req) (Resp, error)
```

### Corrections made against real REST/mqtt5 code

An earlier draft of this doc proposed WRONG Fn shapes for both
general-purpose cases: `func(ctx, msg) error` (server) and
`func(ctx, next func(context.Context) error) error` (client) — neither is a
genuine decorator (a value that can wrap/compose/short-circuit an inner
handler), so attaching TWO general-purpose `HandleMW(nil, ...)`/
`ClientMW(nil, ...)` calls on the same route — exactly what
`examples/rest-api` itself does, `.HandleMW(nil, obsFn).HandleMW(nil,
timingFn)` — would have had no way to compose: the second call would simply
overwrite/duplicate work instead of wrapping the first. Verified REST's
ACTUAL composition mechanism (`adapters/nethttp/adapter.go`'s
`applyGeneralMiddleware`: `func(http.Handler) http.Handler`, composed
OUTERMOST-in, in attachment order — confirmed via reading the real
function, not assumed) and mqtt5's own events-side precedent
(`wrapPublishGeneral[T]`, `adapters/mqtt5/adapter.go:671`) before correcting
the shapes above.

**Scope of the bug — confined entirely to THIS DOC's own draft, not any
shipped implementation.** `api/reqreply` has ZERO `HandleMW`/`ClientMW` code
today (this whole doc describes a not-yet-built mechanism), so there was no
implementation to be wrong — only the Fn-shape signatures PROPOSED here,
before they were cross-checked against real precedent. To answer directly
whether the mqtt5 EVENTS (pub/sub) side has the same flaw: it does NOT —
verified by reading `adapters/mqtt5/adapter.go`'s actual, shipped
`wrapSubscribeGeneral[T]`/`wrapPublishGeneral[T]` (lines ~497 and ~671):
both are genuine decorators (`func(func(ctx,T)error) func(ctx,T)error`),
composed outermost-in in a loop, identical in spirit to REST's
`applyGeneralMiddleware` — no bug there, both were already correct
references this doc's revision leaned on.

## Three middleware kinds (mirrors `examples/rest-api/routes/middleware.go`'s own structure exactly)

REST's example project organizes every middleware use case into exactly
THREE kinds — this doc adopts the SAME three, unchanged, since they are not
REST-specific concepts but instances of the ONE underlying `Use`/`HandleMW`/
`ClientMW` mechanism:

1. **Security** (spec-adding, PAIRED). Declared once via
   `middleware.SecurityScheme(schemeName, scheme, scopes, codec)` +
   `.Use(mw)` — contributes a security scheme + requirement to the
   AsyncAPI spec, exactly like `rest.RouteMeta.Security`/REST's own
   `.Use(middleware.SecurityScheme(...))` does for OpenAPI today.
   FINALIZED (Open design decision #2): this REPLACES
   `reqreply.WithSecurityScheme` as the declare-time mechanism — full
   parity with REST's CURRENT state, not two parallel declare-time
   styles; `reqreply.WithSecurityScheme`/`reqreply.SecurityScheme` become
   DEPRECATED-but-kept aliases. Implemented SEPARATELY, per role:
   server-side `.HandleMW(&mw, verifyFn)` (verifies, PAIRED via
   `Satisfies`), client-side `.ClientMW(&mw, credFn)` (supplies a
   credential, PAIRED via `Satisfies`). REST/events have exactly ONE
   spec-adding middleware kind beyond security's own scheme+requirement:
   header/cookie/query PARAM spec-contribution fields on
   `middleware.Middleware` (`RequestHeaderParams`/`RequestCookieParams`/
   `RequestQueryParams`/`ResponseHeaderParams`/`ResponseCookieParams`).
   **CORRECTION from an earlier draft of this doc**: these were
   originally dismissed here as having "no reqreply analog... not a gap"
   — WRONG, found on further investigation (the user correctly pushed
   back on this). MQTT5's `UserPropertyParam` IS a real, structurally
   identical analog (see Phase 1b below) — the earlier conclusion
   mistook "no *literal* header/cookie/query concept" for "no analogous
   concept at all," when reqreply's transport (MQTT5 specifically) has
   its own equivalent (User Properties) that was simply never wired into
   `middleware.Middleware` for EITHER events or reqreply. Phase 1b closes
   this.
2. **Observer** (non-spec-adding, UNPAIRED, general-purpose). NOT a
   `middleware.Middleware` value at all — a runtime `stats.Observer`,
   attached via the SAME general-purpose decorator slot as kind 3:
   `.HandleMW(nil, mqtt5Observability(obs))` server-side. mqtt5 already has
   an `Observability[T](topic string, obs stats.Observer)` helper for
   EVENTS (`adapters/mqtt5/caller.go:514`) with a DIFFERENT Fn shape
   (`func(func(ctx,T)error) func(ctx,T)error`, generic over T, no
   msg-level access) — reqreply needs its OWN observability helper
   matching the corrected server-side decorator shape above
   (`func(next func(msg *pahomqtt5.Publish)) func(msg *pahomqtt5.Publish)`),
   a NEW function, not a reuse of the existing one (different Fn shape,
   same underlying purpose). Client-side: reqreply's `Client.Call`
   already resolves its `Observer` from `ctx`/`CallOptions` today (see
   d-0004), independent of this doc's mechanism — no client-side
   Observer-as-ClientMW wiring is needed, mirroring REST's OWN client-side
   pattern (REST's `nethttp.Observability` is server-only too; REST's
   client observer is resolved from ctx/`CallOptions`, not attached via
   `ClientMW`).
3. **General-purpose** (non-spec-adding, UNPAIRED). A genuinely distinct
   concern from security AND observability — e.g. request/call TIMING,
   logged independently of `stats.Observer` (mirrors
   `examples/rest-api/routes/middleware.go`'s `TimingServerMW`/
   `TimingClientMW` exactly, same rationale: demonstrating that MULTIPLE
   independent general-purpose decorators compose correctly on ONE route,
   `.HandleMW(nil, obsFn).HandleMW(nil, timingFn)`, not just that one
   exists). Uses the SAME corrected decorator shapes as kind 2 above —
   `Observability` and `TimingServerMW`/`TimingClientMW` are simply two
   DIFFERENT general-purpose Fns attached to the SAME unpaired slot, not
   two different mechanisms.

## Phase 1b — User Property param-as-middleware (request AND reply/response)

> **SHIPPED (mqtt5).** All 4 work items below landed exactly as planned,
> with no design changes discovered during implementation:
> `render/asyncapi/v3.Message` gained a `Headers schema.Schema` field
> (rendered inline, no `$ref` support — matches the plan); `api/reqreply/
> middleware.go` gained `applyParamDeclarations` (collects, dedups by
> name, and renders `RequestHeaderParams`/`ResponseHeaderParams` from
> `rb.middlewares` into both the AsyncAPI schema AND new
> `RouteHandle.RequestHeaderParams`/`RouteHandle.ResponseHeaderParams`
> fields — the latter needed so the ADAPTER can also consult them at
> dispatch time, not just the spec renderer); `adapters/mqtt5.
> FromUserPropertyParam`/`FromResponseUserPropertyParam` bridge functions
> added, plus Attach-time validation wired into `serverTransport.Serve`
> (request side) and `clientTransport.call` (reply side, inside the
> `innerCall` closure, right after the existing error-reply check) —
> BOTH reuse the EXISTING `validateUserProperties`/
> `MissingUserPropertyError`/`UserPropertyError` machinery unchanged
> (confirmed clean reuse, work item 4's own "confirm at implementation
> time" resolved: no new error types needed). The OLD `ServeOptions.
> UserPropertyParams`/`SubscribeOptions.UserPropertyParams` escape hatch
> is untouched, exactly as planned. 6 new tests added (`api/reqreply/
> middleware_test.go`: rendering, dedup, `RouteHandle` population, both
> directions; `adapters/mqtt5/reqreply_test.go`: request-side reject/
> accept and reply-side reject/accept, the reply-accept case constructed
> via a hand-rolled reply publisher since Phase 1b has no server-side
> mechanism to POPULATE a reply User Property from a handler yet — noted
> as a real, currently-unfilled gap, not a test limitation). `examples/
> reqreply-api` extended with a new Demo 6 (`demo_user_property_param_
> middleware.go`, using `mqtt5adapter.Call` directly rather than
> `reqreply.Client` — attaching a raw, non-security User Property on a
> single call is exactly what `CallOptions.UserProperties` is for) and a
> new pristine `routes.HeaderParamComputeRoute` (explicitly opts out of
> `mqtt5server`'s `Server.AddGlobalSecurity("bearerAuth")` via an empty
> `Security` slice — the SAME `ComputeRoute` gap Phase 1 found, caught
> immediately this time since the pattern was already known). Full
> verification battery (`gofmt`/`go build`/`go vet`/`go test -race`/
> `just check`/all examples) — all clean.

Closes the gap identified above: REST's header/cookie/query
param-as-middleware pattern DOES have a real reqreply analog — MQTT5's
User Properties — but it needs its own scoped sub-phase because, unlike
Phase 1's security work, it has a genuine PREREQUISITE that doesn't exist
yet: `render/asyncapi/v3` has **zero existing support for message-level
`headers`** (confirmed via grep against the real renderer source — no
hits at all, unlike REST's OpenAPI renderer, which already renders header
params). This is scoped as an explicit follow-on sub-phase within THIS
doc, not silently bundled into Phase 1 and not punted to a separate
roadmap doc either.

**Confirmed structural match**: `mqtt5.UserPropertyParam{Name,
Description, Required bool, Codec *codex.Codec[string]}`
(`adapters/mqtt5/adapter.go:71`, whose own doc comment already says "mirrors
`rest.HeaderParam` for HTTP request headers") is field-for-field identical
to `middleware.HeaderParamSpec{Name, Description, Required bool, Codec
*codex.Codec[string]}` (`middleware/params.go:25`). Today `UserPropertyParam`
is validated via an OLD per-call `Options` field
(`ServeOptions.UserPropertyParams`/`SubscribeOptions.UserPropertyParams`),
completely disconnected from `.Use()`/`middleware.Middleware` — for BOTH
events pub/sub AND reqreply (this is not a reqreply-only gap; events has
never had this either, but is out of THIS doc's scope).

### API surface (Phase 1b)

```go
// Bridges an EXISTING mqtt5.UserPropertyParam value into a
// middleware.Middleware, usable via reqreply.Route.Use(...) — mirrors
// rest.FromHeaderParam/FromResponseHeaderParam exactly. Lives in
// adapters/mqtt5, NOT middleware/api/reqreply, for the SAME import-
// direction reason rest.FromHeaderParam lives in api/rest: neither
// middleware nor api/reqreply may import an adapter package.

// Request-side — populates middleware.Middleware.RequestHeaderParams.
func FromUserPropertyParam(p UserPropertyParam) middleware.Middleware

// Reply/response-side — populates middleware.Middleware.ResponseHeaderParams.
// This is the "request AND a response path" half the user explicitly
// called out — REST already has this exact request/response split via
// RequestHeaderParams/ResponseHeaderParams + FromHeaderParam/
// FromResponseHeaderParam; reqreply's reply message gets the identical
// treatment, just never extended to a non-HTTP transport before.
func FromResponseUserPropertyParam(p UserPropertyParam) middleware.Middleware
```

### Work items (Phase 1b)

1. `render/asyncapi/v3`: add a `Headers schema.Schema` field to the real
   `Message` struct (`render/asyncapi/v3/document.go:42` —
   `{Name, Schema schema.Schema, SchemaName string, ContentType string}`),
   reusing the EXACT SAME `schema.Schema` type the existing `Schema`
   (payload) field already uses — not a new schema type. **Confirmed
   spec-compliant, not TBD**: verified against the ACTUAL AsyncAPI 3.0
   specification (not assumed) — Message objects officially support a
   `headers` field, which MUST be a Schema Object of type `object`
   (application-level headers only, NEVER protocol headers — those stay
   in protocol bindings), using the SAME JSON-Schema-core/validation
   keywords as `payload`. This is a direct, low-risk, spec-matching
   addition — render `Headers` as a `type: object` schema whose
   `properties` map is built from the declared `RequestHeaderParams`/
   `ResponseHeaderParams` (one property per named User Property),
   mirroring how `payload` is already rendered from `Schema`.
2. `api/reqreply`'s new `middleware.go` (from Phase 1) additionally
   consults `mw.RequestHeaderParams`/`mw.ResponseHeaderParams` at
   `Route.Register` time, rendering them into the request message's/reply
   message's AsyncAPI `headers` schema respectively — mirrors REST's
   `applyMiddlewareDeclarations`/`applyParamDeclarations` pattern
   (`api/rest/middleware.go:416`), adapted for reqreply's single
   request+reply message pair instead of REST's request+response pair.
3. `mqtt5.AttachServer`/`AttachClient`'s reflection shims validate
   declared `RequestHeaderParams`/`ResponseHeaderParams` against real User
   Properties on the request/reply `*pahomqtt5.Publish` messages —
   ADDITIVE to the Attach-based workflow. The OLD
   `ServeOptions.UserPropertyParams`/`SubscribeOptions.UserPropertyParams`
   escape hatch stays UNCHANGED (same "old mechanism is a permanent
   escape hatch" treatment as `SecurityFunc`/`CredentialFunc` throughout
   this doc) — Phase 1b is additive, not a replacement.
4. New structured error(s) for a User-Property-param validation failure
   reached via THIS new path — likely reusing `mqtt5.UserPropertyError`/
   `mqtt5.MissingUserPropertyError` (already exist, used by the OLD
   `UserPropertyParams` mechanism) rather than inventing new ones, since
   the failure MODE is identical (a property missing or failing its
   codec) — only the ATTACHMENT surface changes. Confirm this reuse is
   accurate against the real error types at implementation time.

## Relationship to `mqtt5-user-property-merge.md`

Checked before implementing Phase 1b — a DIFFERENT, independent,
still-undriven idea, confirmed via full read: `mqtt5-user-property-
merge.md` proposes `MergedUserPropertyParam[T]`/
`NewRequiredUserPropertyParam`/`NewOptionalUserPropertyParam` — a
`UserPropertyParam` that is BOTH validated AND auto-**merged** into the
decoded message struct via `codex.DecodeVars`, mirroring `rest.
MergedHeaderParam`/`NewRequiredHeaderParam`. Applied as a direct
`ChannelOpt`/`RouteOpt`-equivalent at declaration time — NOT through
`.Use()`/`middleware.Middleware` at all. Phase 1b's `FromUserPropertyParam`/
`FromResponseUserPropertyParam` bridge a plain `UserPropertyParam` into
`middleware.Middleware` for `.Use()` attachment instead, giving AsyncAPI
spec rendering + Attach-time presence/codec validation — it does NOT
merge anything into the decoded struct. Confirmed via REST's own
precedent (`api/rest/builder.go`): `MergedHeaderParam[T]` embeds a plain
`HeaderParam` and applies itself DIRECTLY via its own `applyRoute` —
completely separate from `FromHeaderParam`/`.Use()`. Both mechanisms
coexist today for REST headers with ZERO conflict — a caller can even do
`rest.FromHeaderParam(merged.HeaderParam)` to get BOTH validate+merge
AND spec+middleware attachment for the SAME property, since `HeaderParam`
is embedded. Phase 1b's `FromUserPropertyParam` supports the identical
composition once `mqtt5-user-property-merge.md` ships (whenever that
happens — it has no driver yet, unrelated timeline). No naming
collision, no functional overlap, no sequencing dependency either way.

## Relationship to `protocol-native-features.md` and `request-correlation-id.md`

Two adjacent roadmap docs touch the SAME underlying MQTT5 wire features
this doc discusses — confirmed via reading both, this doc does NOT
duplicate or conflict with either; each covers a genuinely distinct
concern:

- **[Protocol-Native Feature Declarations](protocol-native-features.md)**
  and `docs/design/d-0004-reqreply-workflow-simplification.md` have now
  FINALIZED the decision on **Response Topic + Correlation Data**
  (confirmed: exactly what `adapters/mqtt5/reqreply.go` reads/writes as
  `msg.Properties.ResponseTopic`/`CorrelationData` for reply matching):
  it does NOT become a sealed, ATTACH-TIME `mqtt5.Capability` — it stays
  an IMPLICIT, always-on characteristic of mqtt5's reqreply transport,
  since every mqtt5 reqreply route needs it unconditionally with no
  opt-out to gate (fails the actual test that motivates `Capability`:
  compile-time-safe OPT-IN gating for something not every binding needs).
  Shared Subscriptions remains the genuine `Capability` candidate from
  that same feature cluster, tracked independently in
  `protocol-native-features.md`. This is a DIFFERENT mechanism than THIS
  doc's `.Use()`/`HandleMW`/`ClientMW` either way (declare/implement,
  RUNTIME dispatch, vs. `Capability`'s declare-time, ATTACH-time
  configuration) — Phase 1b's User Property param-as-middleware is
  COMPLEMENTARY, not competing: Phase 1b is a NAMED, per-route
  header-like param declaration+validation (mirrors REST's `HeaderParam`
  exactly); `Capability` is a broker/protocol-level BEHAVIOR toggle
  (Shared Subscriptions, Message Expiry) supplied at `Attach` time — a
  structurally different axis, with no overlap. **Readiness note**:
  `protocol-native-features.md`'s `Capability` mechanism is built ON TOP
  of `Attach` (it configures HOW an adapter is bound, not a parallel
  dispatch path) — it needs reqreply's `Attach`-based workflow to be a
  COMPLETE, capability-parity replacement for the escape hatch first
  (this doc's own Phase 1/1b/2), not a partial one, so that a future
  `Capability`-driven route never has to ALSO fall back to the escape
  hatch for an unrelated reason (merge-fields, format overrides,
  `ErrorPattern`). Phases 1/1b/2 of THIS doc are therefore a practical
  PREREQUISITE for `protocol-native-features.md` starting its own
  reqreply-scoped work (Shared Subscriptions) cleanly, not just a
  nice-to-have.
- **[Request/Event Correlation ID](request-correlation-id.md)** already
  confirms reqreply's mqtt5 transport generates a wire-level `corrID`
  (UUID) as MQTT5's NATIVE `CorrelationData` property, purely for reply
  matching, invisible to `Observer`/logging — and DELIBERATELY keeps its
  own proposed cross-boundary "Request ID" concept SEPARATE from it
  (three distinct concepts: distributed-trace/span ID, cross-boundary
  request ID, reqreply's own wire `corrID` — confirmed via that doc's own
  text, not one). Auto-propagating a Request ID onto the wire (e.g. as an
  MQTT5 User Property) is explicitly flagged "Phase 2 at best" THERE,
  undecided. **Correlation ID is OUT of THIS doc's scope entirely** —
  Phase 1b's User-Property mechanism could eventually be the ATTACHMENT
  MECHANISM a future Request ID auto-propagation phase uses (a User
  Property named e.g. `"X-Request-ID"`, declared the same way any other
  Phase 1b param is), but designing that integration belongs in
  `request-correlation-id.md`, not here — noted as a cross-reference only.

## Interaction with `CallAsync`/`Future`

reqreply's asynchronous `Client.CallAsync`/`Future[Resp]` mechanism
(shipped in d-0004) needs explicit consideration here, since it's a
genuinely different call shape than REST's `Client.Call` ever has to
account for (REST is purely synchronous). Investigated against the REAL
`CallAsync` implementations (all three — `adapters/mqtt5/
reqreply_transport.go`, `adapters/zeromq/reqreply_transport.go`'s REQ/REP
AND DEALER/ROUTER variants — confirmed identical pattern across all
three):

- **Resolved for free, no extra design work needed**: `CallAsync` spawns a
  goroutine that calls the SAME internal `t.call(ctx, routeAny, reqAny)`
  function `Call` itself uses. Since Phase 1's plan already wires
  `ClientImplementations` consultation into that SAME shared internal
  function (not into `Call`/`CallAsync`'s own separate entry points),
  BOTH paired (credential-supplying) and general-purpose `ClientMW`
  decorators apply to `CallAsync` AUTOMATICALLY, with zero additional
  implementation surface.
- **One real, documented nuance — general-purpose decorator TIMING is
  ASYNC, not wall-clock-from-call-site**: a `ClientMW(nil, fn)` decorator
  that measures duration (e.g. a timing middleware) wraps the portion of
  work INSIDE the spawned goroutine — from when the goroutine starts
  `t.call` to when it returns — NOT wall-clock time from the original
  `CallAsync(...)` call site to `Future.Wait(...)`'s eventual return. This
  is CORRECT and EXPECTED, not a bug: it mirrors how `CallAsync`'s own
  `Observer.RecordRequest` call already behaves today (fires at resolve
  time, inside the goroutine — confirmed earlier this session), and it's
  the whole point of `CallAsync` in the first place (this session's own
  Demo 5 explicitly does OTHER independent work between issuing
  `CallAsync` and calling `Wait` — a caller-side gap no decorator
  attached to the underlying `t.call` could ever see or should try to
  measure). Document this explicitly in the eventual `HandleMW`/`ClientMW`
  godoc so nobody is surprised a timing `ClientMW` doesn't measure
  "time until `Future.Wait()` returns."
- **`Future[Resp]` itself needs no middleware-awareness** — middleware
  wraps the underlying `Req → Resp` production `t.call` performs;
  `CallAsync`/`Future` is purely an ALTERNATIVE DELIVERY mechanism for
  that SAME (already middleware-wrapped) result, not a second code path
  requiring its own separate `FutureMW`-style hook.
- New unit test: `TestAttachClient_ClientMW_AppliesToCallAsyncToo` —
  confirms a `ClientMW`-attached credential-supplying Fn (paired) AND a
  general-purpose decorator both run for a `CallAsync`-dispatched call,
  not just `Call`.

## Phase 0 — Capability parity (REQUIRED prerequisite, ships BEFORE Phase 1)

> **SHIPPED (mqtt5).** All 4 work items below are implemented and
> verified: `go build ./...`, `go vet ./...`, `gofmt -l .` all clean;
> `go test ./... -race` zero FAIL (6 new tests added, see "Unit test
> plan" below); `just check` (staticcheck + gosec) zero findings; every
> `examples/*` re-run via `go run .` with zero failures, including
> `examples/reqreply-api`'s existing 7 demos (unaffected — Phase 0
> doesn't touch the old escape hatch, that's Phase 0b). Implementation
> added `reqreply.RouteHandle.DecodeWithFormats`/`DecodeMergedWithFormats`/
> `EncodeRequestWithFormats`/`EncodeWithFormats`/`DecodeResponseWithFormats`/
> `EffectiveRequestFormats`/`EffectiveFormats`/`EncodeVars` (mirroring
> `events.ChannelHandle`'s/`rest.RouteHandle`'s identical precedent
> methods — a real, precedented API surface addition, not invented) and
> `reqreply.ClientCallOptions` (mirrors `rest.ClientCallOptions` exactly).
> `mqtt5.AttachServer`/`AttachClient` now honor all 4 gaps.
> **UPDATE — zeromq's OWN capability-parity work, originally a
> follow-up, is now ALSO SHIPPED**: `zeromq`'s 4 `Attach*`-backed
> transports (`serverTransport`/`routerServerTransport`/`clientTransport`/
> `dealerClientTransport`) now honor `RequestFormats`/`Formats`
> (via the SAME `DecodeWithFormats`/`EncodeWithFormats`/
> `EncodeRequestWithFormats`/`DecodeResponseWithFormats` methods) and
> `ErrorPattern`-typed replies (via zeromq-local
> `sendHandlerErrorReplyReflect`/`sendRouterHandlerErrorReplyReflect`
> helpers, mirroring mqtt5's `publishHandlerErrorReplyReflect`) — merge-
> field DECODE support is genuinely NOT APPLICABLE to zeromq's REQ/REP
> (confirmed: the wire format carries no topic frame at all, routing is
> entirely socket-based; the OLD escape hatch never had this either).
> **Renamed from this doc's earlier "Phase 2."** Originally framed as
> "opportunistic, not required for parity" — **that framing is now
> WRONG and reversed.** `docs/design/d-0004-reqreply-workflow-simplification.md`'s
> "Remaining open items" originally kept `Serve`/`Call`/`CallHandle`/
> `ServeRouter`/`CallDealer` permanently, reasoning it mirrors REST's
> `ServeOne`/`CallWithHandle` precedent. Re-checked against the real
> code during this doc's final review pass: REST's versions are
> zero-duplication thin wrappers around `Serve`/`Call` itself (confirmed
> via `docs/design/d-0001-rest-middleware-workflow-simplification.md`);
> reqreply's old functions share NO code with `AttachServer`/
> `AttachClient` (confirmed via code — no calls between them in either
> direction, in either adapter package) — a genuinely separate, duplicate
> dispatch implementation, not a thin wrapper. That conflicts with the
> "adapter stays a thin IO wrapper around IO/protocol implementation,
> the user only touches `api/*`" principle. D-0004's bullet is REOPENED
> and reversed there (decision: RETIRE, not keep) — this doc now carries
> the actual work, sequenced as Phase 0 (this section — close the ONE
> real capability gap that was the old functions' sole remaining
> justification) and Phase 0b (next section — build genuine thin
> wrappers + delete the old functions). Phase 0 ships and is FULLY
> verified BEFORE Phase 1 (`.Use`/`HandleMW`/`ClientMW`) begins, so that
> mechanism only ever needs wiring into `Attach`, never also into the
> soon-to-be-deleted old functions.

Verified via code: REST's OWN example uses its ONE escape hatch
(`nethttp.ServeOne`) for exactly ONE niche demo (`demo_violations.go` —
testing a body-decode violation standalone, without a real server);
`nethttp.CallWithHandle` has ZERO usages anywhere in it. `AttachServer`/
`AttachClient`'s reflection shims do NOT honor route-declared
`RequestFormats`/`Formats` per-call overrides, `NewTopicParam`
merge-field topic-var merging, or `ErrorPattern`-typed error replies
today (re-confirmed via grep against `d-0004`'s text) — ONLY the
lower-level `Serve`/`Call` escape hatch supports them. This is EXACTLY
why this session's `examples/reqreply-api` Demo 2/3 currently need
`mqtt5adapter.Call`+`CredentialFunc` directly, and EXACTLY the gap that
was the old functions' only remaining justification for existing at
all — closing it here removes that justification entirely, clearing
the way for Phase 0b's deletion.

### Work items

> **Re-verified against the real code while drafting the implementation
> plan** — items 1 and 3 below are confirmed accurate as originally
> written; item 2's gap turned out BIGGER than originally described here
> (server AND client side both skip route-declared `RequestFormats`/
> `Formats` entirely, not just the client-side per-call override); a 4th
> item (client-side topic-var derivation) was found during the same pass
> and is added below — it was NOT previously enumerated, but belongs in
> this same "capability parity" category and must close before Phase 0b
> can safely delete the old escape hatch (Phase 0b's deletion depends on
> Phase 0 closing EVERY remaining gap, not just three of four).

1. **SHIPPED.** **Merge-field support in the Attach shims** — `mqtt5.AttachServer`'s
   `Serve` currently always decodes via the plain reflected `Decode`
   field (confirmed via code, `reqreply_transport.go:150`), never
   `handle.DecodeMerged`/`handle.MergeFields()` — the SAME machinery
   reqreply's escape hatch (`Serve`) already calls today (explicitly
   documented as unimplemented v1 scope in a "NOTE" comment at
   `reqreply_transport.go:196-204`). No NEW merge-field logic needs
   inventing; `DecodeMerged` already does decode-then-merge internally
   and behaves identically to plain `Decode` when `MergeFields()` is
   empty (confirmed via its own godoc, `route.go:1070-1108`) — so
   switching to it is backward compatible with routes that declare no
   merge params. `AttachServer`'s dispatcher needs to keep `rv` (the
   `*RouteHandle` pointer Value `recoverRouteHandleValue` already
   returns but today discards) to reach `DecodeMerged` via
   `rv.MethodByName(...)` — a METHOD, not a field, but the same
   reflection technique already used for the `Decode`/`Encode` FIELDS,
   just applied to a method instead.
2. **SHIPPED.** **`RequestFormats`/`Formats` (both route-declared AND per-call
   override) are NOT consulted at all today, on EITHER side — a bigger
   gap than an earlier revision of this doc described.** Server-side:
   `serverTransport.Serve` calls only the reflected `Decode`/`Encode`
   fields, never checking `handle.RequestFormats`/`handle.Formats`
   first (unlike old `Serve`, which does — `reqreply.go:216-217`,
   `:325-326`). Client-side: `clientTransport.call` calls only the
   reflected `EncodeRequest`/`DecodeResponse` fields directly
   (`reqreply_transport.go:351-352`, `:419`, `:478`) — it doesn't even
   fall back to route-declared `handle.RequestFormats`/`handle.Formats`,
   let alone support a per-call override (old `Call` supports BOTH, via
   `resolveCallFormat[T]`'s 3-level priority chain — per-call override >
   declared > default). Closing the PER-CALL-override half needs a new
   variadic parameter on the client entry points: **`reqreply.
   ClientCallOptions{RequestFormats, ResponseFormats any}`** (name
   finalized — direct structural port of `rest.ClientCallOptions`,
   `api/rest/builder.go:2630`, chosen over a shorter `reqreply.
   CallOptions` to avoid any naming confusion with the UNRELATED,
   already-existing `mqtt5.CallOptions` — different package, but the
   name similarity invites confusion regardless) added as a trailing
   variadic on `Client.Call`/`Client.CallAsync`/`ClientTransport.Call`/
   `ClientTransport.CallAsync`. The DECLARED-format-fallback half (no
   override, just honoring `handle.RequestFormats`/`handle.Formats`
   when set) needs no new API surface — purely an internal dispatcher
   fix, both sides. Both halves are resolved generically via reflection
   (type-checking the override's dynamic type against the declared
   field's slice type, mirroring `resolveCallFormat`'s own error shape) —
   no compile-time generic instantiation needed, same technique already
   proven for `Decode`/`Encode`.
3. **SHIPPED.** **`ErrorPattern`-typed reply support in `AttachServer`'s dispatch** —
   `serverTransport.Serve`'s message handler currently always falls back
   to plain-text `err.Error()` on handler/encode failure (confirmed via
   code), never consulting `handle.ErrorResponseFor(err)` the way the
   escape hatch's own `publishHandlerErrorReply` helper already does
   (`reqreply.go:696-732`). This is a STRAIGHTFORWARD port — call the
   SAME existing method (via `rv.MethodByName("ErrorResponseFor")`, same
   as item 1's technique), no new error-matching logic needed. Scoped to
   handler/encode failures only (decode/security errors stay on plain
   `publishErrorReply`, matching old `Serve`'s own behavior — `ErrorPattern`
   is business-error-only by design, confirmed via
   `publishHandlerErrorReply`'s own godoc).
4. **SHIPPED.** **Client-side topic-var derivation — NEW item, found while drafting
   the implementation plan, folded into Phase 0 (not deferred).** Old
   `Call` supports `CallOptions.Vars map[string]string` (explicit
   per-call topic-var override) AND `CallHandle` (a convenience wrapper
   auto-deriving `Vars` from `req` via `codex.EncodeVars(req,
   handle.MergeFields()...)`, explicit `Vars` taking precedence —
   `reqreply.go:620-661`). `clientTransport.call` has NEITHER: `path :=
   elem.FieldByName("Topic").String()` is used as-is, with zero
   `BuildTopic`/`Vars` resolution. This is the SAME "merge-field parity"
   category as item 1, just the client-side, request-topic-var-FROM-req
   direction (mirrors REST's `PathMergeFields` derivation) — closing it
   means deriving `vars` via `rv.MethodByName("MergeFields")` +
   reflected `codex.EncodeVars` before publishing, whenever
   `MergeFields()` is non-empty. Folded into Phase 0 (not scoped out)
   because Phase 0b's deletion of the old escape hatch depends on Phase
   0 closing EVERY remaining capability gap, not just three of four —
   leaving this open would mean deleting a function some caller might
   still need it for.

### Phase 0, decision A — RESOLVED, SHIPPED

`Client.CallAsync`'s signature gained the SAME new variadic
   `reqreply.ClientCallOptions` parameter item 2 adds to `Client.Call` —
   confirmed via implementation: both dispatch the SAME underlying
   `t.call` (per "Interaction with `CallAsync`/`Future`" above), so
   passing the resolved `ClientCallOptions` value straight through
   works cleanly, with zero additional wiring. **Naming**:
   `reqreply.ClientCallOptions` (not the shorter `reqreply.CallOptions`,
   avoiding confusion with the unrelated, already-existing
   `mqtt5.CallOptions`). Identical trailing `...reqreply.ClientCallOptions`
   on BOTH `Call` and `CallAsync`, resolved identically inside the
   shared `t.call` — confirmed via
   `TestAttachClient_CallAsync_AppliesClientCallOptions` (new test,
   passing) that the two entry points are genuinely signature- and
   behavior-symmetric, not just declared that way.

## Phase 0b — Escape-hatch de-duplication via delegation (SHIPPED for mqtt5 AND zeromq)

> **SHIPPED (mqtt5) — outcome BETTER than originally planned, no
> breaking change.** This section originally planned "build new
> `ServeOne`/`CallWithHandle`-named thin wrappers, migrate
> `examples/reqreply-api`'s Demo 2/3 off the old functions, then DELETE
> `Serve`/`Call`/`CallHandle` entirely" (mirroring D-0002's full-removal
> precedent). **Implementation found a cleaner path, discovered while
> building the planned thin wrappers**: `reqreply.ServerTransport.Serve(ctx,
> route any, fn any) error` and `reqreply.ClientTransport.Call(ctx, route
> any, req any, ...) (any, error)` are ALREADY scoped to exactly ONE
> route/call each (confirmed via the interface definitions,
> `api/reqreply/builder.go:345`/`api/reqreply/client.go`) — `Server.Serve`'s
> own multi-route CONCURRENCY happens ABOVE this, in `Server.Serve`
> itself (one goroutine per registered route), not inside
> `ServerTransport.Serve`. This means `mqtt5adapter.serverTransport`/
> `clientTransport` (the concrete types `AttachServer`/`AttachClient`
> build) were ALREADY single-route/single-call dispatch primitives —
> no `Server`/`Client`/scratch-registration wrapper was needed at all.
> **`Serve`/`Call`/`CallHandle` KEPT their EXACT existing signatures**
> (zero breaking change) — their BODIES were rewritten to construct a
> `&serverTransport{client, router, opts}`/`&clientTransport{client,
> router, opts}` directly and delegate to its `.Serve`/`.Call` method —
> genuinely zero duplicate logic, the SAME bar REST's `ServeOne`
> achieves (see Phase 0's opening for why the ORIGINAL "mirrors
> `ServeOne`/`CallWithHandle`" analogy was flawed — reqreply's real fix
> turned out to be even simpler than either REST escape hatch's own
> mechanism). **`examples/reqreply-api`'s Demo 2/3 needed ZERO code
> changes** — they still call `mqtt5adapter.Call(...)`/
> `mqtt5adapter.Serve(...)` with the exact same signatures, now
> transparently backed by the de-duplicated implementation. No
> migration, no deletion, no doc updates to `docs/guides/mqtt5.md`'s
> "escape hatch" section needed (still accurate — the functions still
> exist, unchanged from the caller's perspective).
>
> **One real, additional gap found and closed during this work, NOT
> part of Phase 0's original 4 items**: `AttachServer`/`AttachClient`'s
> reflection dispatch had ZERO `stats.TraceObserver`/`StartSpan`/
> `EndSpan` support — the escape hatch always had it (span names
> `"mqtt5.serve"`/`"mqtt5.request"`). Delegating `Serve`/`Call` to the
> Attach-based transports would have SILENTLY DROPPED tracing for any
> existing caller relying on it — caught by re-running the full,
> pre-existing test suite (`TestServe_TraceSpan`/`TestCall_TraceSpan`
> failed immediately), fixed by adding the SAME span-start/deferred-
> span-end pattern to `serverTransport.Serve`/`clientTransport.call`
> directly. This is exactly the kind of gap this doc's own "Delivery
> risk & staging" section warns a reflection-based dispatcher's
> "equivalent" claim can hide until the full pre-existing test suite is
> actually re-run — confirmed here, not just theoretical.
>
> `Builder`/`NewBuilder`/`BuilderOption` are unaffected either way (they
> were never part of this reversal).
>
> **UPDATE — zeromq's OWN version of this same de-duplication, SHIPPED
> in a follow-up pass, same session.** `adapters/zeromq/adapter.go`'s
> `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer` now delegate to
> `serverTransport`/`clientTransport`/`routerServerTransport`/
> `dealerClientTransport` directly (built from a single-entry
> `map[string]FramedSocket{handle.Topic: sock}`) — same zero-duplication,
> zero-breaking-change outcome as mqtt5. zeromq's `Attach*`-backed
> transports ALSO needed the SAME capability-parity work mqtt5's Phase 0
> did (`RequestFormats`/`Formats` honoring, `ErrorPattern`-typed replies,
> `TraceObserver` spans `"zmq.serve"`/`"zmq.request"`) — done proactively
> this time, not discovered via a late regression, since the mqtt5 round
> had already established what to check for. **One real regression
> found and fixed via the SAME "re-run the full pre-existing test suite"
> discipline**: `CallOptions.Vars`/auto-derived merge-field vars feed
> into `RouteHandle.BuildTopic` for OBSERVABILITY-path naming ONLY in
> zeromq (never socket selection — zeromq REQ/REP routing is
> socket-based, confirmed via the escape hatch's own doc comments) — a
> `BuildTopic` failure (e.g. a missing required template var) MUST be
> FATAL, returning `CallError` before anything is sent, mirroring the
> escape hatch exactly. An initial draft of this fix incorrectly treated
> a `BuildTopic` failure as a non-fatal, ignorable observability
> concern — this caused `TestCall_Vars_MissingVar_ReturnsCallError` to
> hang indefinitely (the mock socket had no reply queued, since the
> real `Call` would never have sent anything), caught immediately by
> re-running the full zeromq test suite with a timeout. zeromq's merge-
> field DECODE support (server-side topic-var-into-Req merging) is
> confirmed NOT APPLICABLE — REQ/REP/ROUTER/DEALER frames carry no topic
> string at all (routing is entirely socket-based), so there was never
> anything to port for that half; the OLD escape hatch never had it
> either, confirming this is a genuine protocol difference, not an
> oversight.

### What actually shipped (mqtt5)

1. `adapters/mqtt5/reqreply.go`'s `Serve[Req,Resp]` body →
   `t := &serverTransport{client, router, opts}; return t.Serve(ctx, handle, fn)`.
2. `adapters/mqtt5/reqreply.go`'s `Call[Req,Resp]` body →
   `t := &clientTransport{client, router, opts}; respAny, err := t.Call(ctx, handle, req); ...`
   (type-asserts `respAny` to `Resp`, returning `TransportTypeMismatchError`
   on a mismatch — should never happen in practice, defensive only).
3. `CallHandle[Req,Resp]` → now a pure alias, `return Call(ctx, client,
   router, handle, req, opts)` — its OWN auto-derive-from-req behavior
   is now built into `clientTransport.call` directly (Phase 0 work item
   4), so `CallHandle` and `Call` are functionally identical; kept for
   existing callers, `Call` is preferred in new code.
4. `clientTransport.call` fixed to honor `t.opts.Vars`/`t.opts.
   RequestFormats`/`t.opts.ResponseFormats` (the mqtt5.CallOptions
   fields the escape hatch always had) with correct precedence over the
   NEW `reqreply.ClientCallOptions`/auto-derived-from-req values — found
   and fixed via the SAME "re-run the full pre-existing test suite"
   discipline (`TestCall_WithVars_MissingVar_*`/`TestCall_RequestFormats_*`/
   `TestCall_ResponseFormats_*` failed before this fix).
5. Dead code removed: the OLD, now-unreachable generic `resolveCallFormat[T]`/
   `publishHandlerErrorReply[Req,Resp]` helpers (superseded by
   `resolveCallFormatReflect`/`publishHandlerErrorReplyReflect` in
   `reqreply_transport.go`, added during Phase 0).

### What actually shipped (zeromq)

1. `adapters/zeromq/reqreply_transport.go`: added `resolveCallFormatReflect`/
   `sendHandlerErrorReplyReflect`/`sendRouterHandlerErrorReplyReflect`
   (zeromq-local mirrors of mqtt5's identical helpers); wired
   `DecodeWithFormats`/`EncodeWithFormats`/`ErrorResponseFor` into
   `serverTransport.Serve`/`routerServerTransport.Serve`;
   `EncodeRequestWithFormats`/`DecodeResponseWithFormats` into
   `clientTransport.call`/`dealerClientTransport.call`; added
   `stats.TraceObserver` spans (`"zmq.serve"`/`"zmq.request"`) to all 4.
2. `adapters/zeromq/adapter.go`'s `Serve[Req,Resp]`/`ServeRouter[Req,Resp]`
   bodies → construct `&serverTransport{...}`/`&routerServerTransport{...}`
   (single-entry `map[string]FramedSocket{handle.Topic: sock}`) and
   delegate to `.Serve(ctx, handle, fn)`.
3. `Call[Req,Resp]`/`CallDealer[Req,Resp]` bodies → construct
   `&clientTransport{...}`/`&dealerClientTransport{...}` and delegate to
   `.Call(ctx, handle, req)`, type-asserting the response.
4. `CallHandle[Req,Resp]` → now a pure alias for `Call` (same outcome as
   mqtt5) — auto-derive-from-req is built into `clientTransport.call`
   directly, used ONLY for observability path/span naming (zeromq
   REQ/REP routing is socket-based, never affects which socket is used).
5. Dead code removed: `serveRequest`/`serveRouterRequest` helper
   functions (now-orphaned after the delegation), the generic
   `resolveCallFormat[T]`/`sendHandlerErrorReply[Req,Resp]`/
   `sendRouterHandlerErrorReply[Req,Resp]` helpers, and the now-unused
   `sync` import.
6. **Regression found and fixed**: `BuildTopic` failure during the
   observability-path derivation must be FATAL (`CallError` returned
   before anything is sent) — an initial draft treated it as non-fatal,
   causing `TestCall_Vars_MissingVar_ReturnsCallError` to hang
   indefinitely. Fixed in both `clientTransport.call` and
   `dealerClientTransport.call`.

### Verification

`gofmt -l .` clean; `go build ./...` clean; `go vet ./...` clean;
`go test ./... -race` — zero FAIL across the ENTIRE repo (not just
`adapters/mqtt5`/`adapters/zeromq`) after each fix, re-run repeatedly
during this work, not just once at the end (this discipline is what
caught BOTH the TraceObserver gap and the BuildTopic-fatal regression
above); `just check` (staticcheck + gosec) zero findings; `for d in
examples/*/; do go run .; done` — zero failures, including
`examples/reqreply-api`'s existing 7 demos (mqtt5 AND zeromq REQ/REP AND
ROUTER/DEALER) completely unchanged.

## Example mini-project extension — `examples/reqreply-api`

> **Superseded by what actually shipped.** This table was written BEFORE
> Phase 1/1b's real implementation — the ACTUAL demos ended up simpler
> and split differently than planned here: Phase 1's security
> declare/implement/call chain was demonstrated by REUSING/migrating
> `demo_global_security_dual_mode_call.go`/`demo_route_level_security_
> credential_error.go` (no new `demo_middleware_declare_implement.go`,
> no `TimingServerMW`/`TimingClientMW` general-purpose demo functions
> were built); Phase 1b's User-Property param work item (e) was
> demonstrated by a NEW, focused `demo_user_property_param_middleware.go`
> (Demo 6) instead of being folded into a single combined demo file.
> Kept below UNCHANGED as the original plan's reasoning/rationale, per
> this doc's own "keep in place, mark resolved, don't delete" discipline
> — see the "Files to create" table's Phase 1/1b rows for what actually
> shipped.

Once implemented, `examples/reqreply-api` (the mini-project this session's
Phase 2/4 work already shipped, see d-0004) should demonstrate the new
mechanism the SAME way `examples/rest-api` demonstrates REST's — mirroring
its EXACT file-by-file layout, not inventing a new demo structure:

| File | Change | Mirrors (rest-api) |
|---|---|---|
| `routes/middleware.go` (NEW) | `TimingServerMW`/`TimingClientMW[Req,Resp]` general-purpose functions (direct ports, generic over reqreply's `Req`/`Resp` instead of REST's); a NEW `.Use()`-attachable `middleware.SecurityScheme` value for at least one route, REPLACING that route's OLD `WithSecurityScheme` usage (Open design decision #2 — full REST parity, not two coexisting styles); a NEW `.Use()`-attachable User-Property param middleware via `mqtt5.FromUserPropertyParam`/`FromResponseUserPropertyParam` (Phase 1b) | `routes/middleware.go` |
| `handlers/security.go` (NEW) | A `ScopesImpl`-equivalent building a `middleware.ServerImplementation{Satisfies: [...], Fn: verifyFn}` returning a scope-grant map (Open design decision #4), paired against `routes/middleware.go`'s new scheme | `handlers/security.go` |
| `mqtt5server/server.go` (edit) | Register a NEW route (or an additional variant of an existing one) via `route.WithHandler(fn).Use(scopeMw).Use(userPropMw).HandleMW(&scopeMw, implFn).HandleMW(nil, mqtt5ObservabilityFn).HandleMW(nil, timingFn).Register(server)` — demonstrating declare+implement+observer+general-purpose+param-as-middleware ALL chained on one route | `chiserver/server.go`/`nethttpserver/server.go` |
| `client/client.go` (edit) | `.ClientMW(&scopeMw, credFn).ClientMW(nil, TimingClientMW(...))` variants — at least one succeeding (correct credential) and one rejected (wrong/missing credential), mirroring `CreateUserRouteAsAlice`/`AsAdmin`'s success-vs-rejection contrast | `client/client.go` |
| `demo_middleware_declare_implement.go` (NEW demo file) | (a) the full declare→implement→call chain succeeding; (b) a `HandleMW`/`ClientMW` naming an undeclared scheme failing at `Route.Register`/`ClientHandle` time with `UnknownMiddlewareImplementationError`; (c) a declared-but-unimplemented scheme failing the coverage check at Serve/Attach time with `MissingSecurityMiddlewareError`; (d) the general-purpose timing middleware's logged output visible in console output, proving BOTH `HandleMW(nil,...)` calls on one route actually compose in the CORRECT order — a direct regression check for the Fn-shape bug found and fixed in this revision; (e) the Phase 1b User-Property param rejecting a message missing a required property, AND the resulting AsyncAPI spec print showing the new request/reply `headers` schema entries | `demo_admin.go`/`demo_violations.go`-style rejection demos |
| `main.go` (edit) | Wire the new demo into the narrative sequence (an additional numbered demo, after the existing 7) | `main.go`'s demo sequence |
| `demo_global_security_dual_mode_call.go`/`demo_route_level_security_credential_error.go` (edit, SHIPPED — migrated off the escape hatch) | Both demos migrated from `mqtt5adapter.Call(ctx, built.Broker, built.Router, handle, req, mqtt5adapter.CallOptions{CredentialFunc: ...})` onto a SECOND `reqreply.Client` per credential value, attached via `mqtt5adapter.AttachClient(client, built.Broker, built.Router, mqtt5adapter.CallOptions{CredentialFunc: ...})`, then `client.Call(ctx, handle, req)` — `CredentialFunc` is fixed per-`Attach`, not per-call, so Demo 3's two DIFFERENT credentials (valid vs. malformed) needed two distinct `Client` instances. Verified: `go build`/`go vet`/`gofmt` clean, `go run .` output byte-identical to the pre-migration escape-hatch version. **Opportunistic follow-on, once `.Use`/`HandleMW`/`ClientMW` exist (Phase 1)**: additionally rewrite to attach `.ClientMW(&scopeMw, credFn)` on the route BEFORE registering, showing the fully declarative style ALONGSIDE this now-Attach-based (but still Options-driven, pre-Phase-1) call — an additional demonstration, not a required replacement. | N/A — this migration has no direct REST analog since REST's example never needed the escape hatch for this in the first place |

This section is a PLAN for a future implementation phase (once the "Open
design decisions" below are resolved) — no code is written by this roadmap
doc itself. **Correction, superseding an earlier revision twice over**:
the LAST row above was FIRST gated entirely on Phase 1 shipping, THEN
revised to a required "Step A/Step B" split gated on Phase 0b's assumed
DELETION of the escape hatch — Phase 0b's actual, SHIPPED outcome
(delegation, zero breaking change, zero migration needed — see Phase
0b's own section) makes BOTH of those earlier framings moot: nothing
about this demo NEEDS to change at all, ever, for capability-parity
reasons. Only the SAME opportunistic Phase 1 declarative-style
demonstration (once `.Use`/`ClientMW` exist) remains a genuine, optional
future enhancement.

## Structured errors (all implement `slog.LogValuer`)

`middleware.MiddlewareShapeError{Name, Expected, Got}` (package `middleware`,
truly shared) is reused UNCHANGED — a `HandleMW`/`ClientMW` `fn` whose
concrete type doesn't match what `mqtt5`'s reflection shim expects for the
paired/unpaired case.

The two coverage-check errors, however, are NOT shared package-level types —
confirmed via code that BOTH `api/rest` (`rest.MissingSecurityMiddlewareError`/
`rest.UnknownMiddlewareImplementationError`, `api/rest/middleware.go:867,890`)
AND `api/events` (`events.MissingSecurityMiddlewareError`/
`events.UnknownMiddlewareImplementationError`, `api/events/builder.go:1513,1585`)
each define their OWN package-local copy with the identical `{Route/Topic
string, Scheme string}` shape — reqreply follows the SAME pattern, not a
cross-package reuse:

- `reqreply.MissingSecurityMiddlewareError{Route, Scheme}` — returned by the
  new coverage check (see Open design decision #1 for exact call site) when
  a route declares a security scheme with NO attached
  `middleware.ServerImplementation` whose `Satisfies` names it — mirrors
  `rest.MissingSecurityMiddlewareError`/`rest.CheckCoverage` exactly
  (`api/rest/middleware.go:583`, doc comment: "RELOCATED... can only run
  once BOTH [declaration and implementation] are known — which is adapter
  Serve time, not builder time").
- `reqreply.UnknownMiddlewareImplementationError{Route, Scheme}` — returned
  by `Route.Register`/`Route.ClientHandle` (UNCONDITIONALLY, regardless of
  whether a handler is attached — a route-internal-consistency check, not
  an "intentionally unimplemented" case) when a `HandleMW`/`ClientMW` call
  is PAIRED against a scheme nobody `.Use()`'d on the SAME route — mirrors
  `rest.UnknownMiddlewareImplementationError`/`checkImplementationsDeclared`
  exactly (`api/rest/middleware.go:625,890`).

## Observer integration

No new `stats.Observer` INTERFACE extension needed — `SecurityObserver`/
`Observer` already exist and cover everything here. Two concrete pieces of
work:

- `SecurityObserver.RecordSecurityRejection` already fires from `mqtt5`'s
  existing `ServeOptions.SecurityFunc`/`CallOptions.CredentialFunc`
  rejection paths — the NEW `HandleMW`/`ClientMW` paths must call the SAME
  guarded `stats.SecurityObserver` type-assertion on their own rejection
  paths, mirroring REST's identical wiring (confirmed via
  `.github/skills/review-go-codex/references/history.md`'s pub/sub G1
  finding: a missed `SecurityObserver` call on ONE adapter's
  security-rejection path was a real, shipped bug in a sibling pattern —
  the review-go-codex skill's own "Gotchas" list flags this exact
  regression class).
- A NEW helper function is needed for "middleware kind 2" (Observer, see
  "Three middleware kinds" above) — `mqtt5`'s existing
  `Observability[T](topic string, obs stats.Observer)` (events-only,
  `func(func(ctx,T)error) func(ctx,T)error` shape) does NOT match
  reqreply's corrected general-purpose decorator shape
  (`func(next func(msg *pahomqtt5.Publish)) func(msg *pahomqtt5.Publish)`)
  — a reqreply-specific `mqtt5.ReqReplyObservability(obs stats.Observer)
  func(next func(msg *pahomqtt5.Publish)) func(msg *pahomqtt5.Publish)` (or
  similarly-named) function is needed, reporting via the SAME
  `RecordRequest`/`RecordValidationError` calls mqtt5's reqreply transport
  already makes internally, so a route attaching it via
  `.HandleMW(nil, mqtt5.ReqReplyObservability(obs))` gets the SAME
  observability the transport's own `Observer` option provides today —
  purely an alternative attachment SURFACE (declare-time, per-route,
  composable with other general-purpose middleware), not new
  instrumentation.

## Phase 0 unit tests — SHIPPED

Added to `adapters/mqtt5/reqreply_transport_test.go` (all passing,
including under `-race`):

| Test | Verifies |
|---|---|
| `TestAttachServer_AttachClient_MergeFields_RoundTrip` | server-side `DecodeMergedWithFormats` merges the topic var into the decoded Req (item 1); client-side `EncodeVars`+`BuildTopic` derives the concrete topic FROM req (item 4) — one round trip, both directions |
| `TestAttachServer_Formats_Honored` | `AttachServer` consults route-declared `Formats` (YAML) instead of plain JSON `Encode` (item 2, server-side) |
| `TestAttachServer_ErrorPattern_MatchedReply` | a matching declared `ErrorPattern` produces the typed codec-backed reply payload on handler failure (item 3) |
| `TestAttachServer_ErrorPattern_NoMatch_FallsBackToPlainText` | an unrelated handler error still falls back to plain-text (item 3, negative case) |
| `TestAttachClient_Call_ClientCallOptions_ResponseFormats_Overrides` | a per-call `ClientCallOptions.ResponseFormats` override successfully decodes a YAML reply the client's own route declares nothing for (item 2, client-side) |
| `TestAttachClient_CallAsync_AppliesClientCallOptions` | the SAME override applies through `CallAsync` too, confirming signature/behavior symmetry (Phase 0, decision A) |

## Unit test plan — SHIPPED (mqtt5)

Mirrors `api/rest/middleware_test.go`'s own test IDs (reqreply's PRIMARY
reference, adjusted for reqreply's mqtt5-only Phase 1 adapter scope —
NOT `api/events/builder_test.go`'s `Subscriber`/`Publisher`-split naming,
since reqreply has one `Route`, not two roles). Actual test locations
and any name deviations from the original plan are noted per row.

| Test | Verifies |
|---|---|
| `TestRoute_Use_Chainable` (`api/reqreply/middleware_test.go`) | `.Use(mw1).Use(mw2)` and `.Use(mw1, mw2)` produce equivalent opts, in attachment order |
| `TestRoute_Use_DoesNotMutateOriginal` (same file) | `Route` is immutable — `.Use` returns a distinct value |
| `TestHandleMW_Paired_DerivesSatisfiesFromSecurity` (same file) | `mw.Security != nil` → `Satisfies == []string{mw.Security.SchemeName}` |
| `TestHandleMW_Unpaired_GeneralPurpose_EmptySatisfies` (same file) | `mw == nil` → `Satisfies` empty, Fn always runs |
| `TestClientMW_Paired_DerivesSatisfiesFromSecurity` (same file) | client-side mirror of the above |
| `TestClientMW_MultipleCallsForSameScheme_DistinctNames` (same file) | mirrors REST's `#1`/`#2` attachment-order-index naming, needed for the SAME reason (conflict-check heuristics) |
| `TestRoute_Register_PopulatesImplementations` (same file) | `Route.Register(server)` copies `HandleMW`/`ClientMW`-built implementations onto the returned `*RouteHandle` |
| `TestRoute_ClientHandle_PopulatesImplementations` (same file) | same, for the no-`Server`-needed path |
| `TestRoute_Register_UnknownMiddlewareImplementationError` (same file) | a `HandleMW`/`ClientMW` naming a scheme nobody `.Use()`'d fails at Register time |
| `TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError` (renamed from `TestServe_NilSecurityFunc_NotAnError`, `adapters/mqtt5/reqreply_test.go`) | a declared `Security` requirement with no matching `ServerImplementation` fails `Serve`/`AttachServer` construction |
| `TestServe_HandleMW_PairedSecurityFn_Verifies` (renamed from `TestServe_SecurityFunc_RejectsRequest`, same file) | mqtt5's new Fn shape actually gets called and can reject |
| `TestCall_ClientMW_PairedCredentialFn_Supplies` (renamed from `TestCall_CredentialFunc_ValidFormat_Passes`, same file) | mqtt5's new client Fn shape actually gets called and supplies a credential |
| `TestCall_ClientMW_MalformedCredentialFormat_ReturnsSecurityCredentialError` (renamed from `TestCall_CredentialFunc_MalformedFormat_...`, same file) | malformed credential still rejected client-side before publish |
| `TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns` (`adapters/mqtt5/reqreply_transport_test.go`) | unpaired Fn runs regardless of declared Security |
| `TestAttachServer_MultipleGeneralPurposeHandleMW_ComposeOutermostIn` (same file) | direct regression test for the CORRECT outermost-in composition order of TWO `HandleMW(nil, ...)` decorators attached to the SAME route |
| `TestAttachClient_MultipleGeneralPurposeClientMW_ComposeOutermostIn` (same file) | client-side mirror of the above — **caught a REAL bug**: `validateClientImplementationShapes` was initially called with the decorator's INNER function type instead of the decorator type itself, rejecting every general-purpose `ClientMW` with a `MiddlewareShapeError`; fixed immediately, confirmed by this test passing afterward |
| `TestAttachClient_ClientMW_AppliesToCallAsyncToo` (same file) | a general-purpose decorator runs for a `CallAsync`-dispatched call too, not just `Call` — see "Interaction with `CallAsync`/`Future`" |

**Deliberately NOT ported**: `TestAttachServer_SecurityRejection_
CallsSecurityObserver` — REDUNDANT with `TestServe_
BuiltInCredentialCheck_RejectsMalformedCredential` (pre-existing,
unchanged) and `TestCall_ClientMW_MalformedCredentialFormat_
ReturnsSecurityCredentialError` (above), both of which already assert
`SecurityObserver.RecordSecurityRejection` is called on the relevant
rejection path — a separate test would duplicate coverage, not add any.
`TestCall_CredentialFunc_ReturnsNilProperties_SkipsValidation` was also
NOT ported (see the code comment left in its place, `adapters/mqtt5/
reqreply_test.go`) — its premise doesn't generalize to `ClientMW`'s
multi-implementation MERGE model.

### Phase 1b additions — SHIPPED (mqtt5)

| Test | Verifies |
|---|---|
| `TestRoute_Register_RendersRequestHeaderParamsIntoAsyncAPI` (`api/reqreply/middleware_test.go`) | a `.Use()`-attached `RequestHeaderParams` middleware renders a `headers` schema on the request message |
| `TestRoute_Register_RendersResponseHeaderParamsIntoAsyncAPI` (same file) | reply-side mirror of the above |
| `TestRoute_Register_PopulatesRequestResponseHeaderParams` (same file) | `Route.Register` populates `RouteHandle.RequestHeaderParams`/`ResponseHeaderParams` |
| `TestRoute_Register_DedupsHeaderParamsByName` (same file) | two middlewares declaring the SAME header param name fold into ONE property/one `RouteHandle` entry, not two |
| `TestRoute_ClientHandle_PopulatesHeaderParams` (same file) | same population, for the no-`Server`-needed path |
| `TestRoute_Register_NoHeaderParams_OmitsHeadersFromSpec` (same file) | no header params declared → no `headers:` key rendered at all (zero-`Schema` omission) |
| `TestServe_HandleMW_RequestHeaderParam_MissingRequired_Rejects` (`adapters/mqtt5/reqreply_test.go`) | a message missing a required declared User Property is rejected with `ServeError{Kind: KindSecurity}` wrapping `MissingUserPropertyError` |
| `TestServe_HandleMW_RequestHeaderParam_Present_Succeeds` (same file) | the SAME route succeeds when the property is present |
| `TestCall_ClientMW_ResponseHeaderParam_MissingRequired_Rejects` (same file) | a reply missing a required declared User Property is rejected client-side with `CallError{Kind: KindSecurity}` wrapping `MissingUserPropertyError` |
| `TestCall_ClientMW_ResponseHeaderParam_Present_Succeeds` (same file) | reply-side success case — constructed via a hand-rolled reply publisher (bypassing `Serve`) since Phase 1b has no server-side mechanism yet for a handler to POPULATE a reply User Property |

## Files to create

| File | Responsibility |
|---|---|
| `api/reqreply/route.go` (edit, Phase 0, SHIPPED) | Added `EffectiveRequestFormats`/`DecodeWithFormats`/`DecodeMergedWithFormats`/`EncodeRequestWithFormats`/`EffectiveFormats`/`EncodeWithFormats`/`DecodeResponseWithFormats`/`EncodeVars` methods on `RouteHandle` (mirrors `events.ChannelHandle`'s/`rest.RouteHandle`'s identical precedent methods) |
| `api/reqreply/client.go` (edit, Phase 0, SHIPPED) | Added `reqreply.ClientCallOptions` type + trailing variadic parameter to `Client.Call`/`CallAsync`/`ClientTransport.Call`/`ClientTransport.CallAsync` (Phase 0 work item 2 + decision A) |
| `adapters/mqtt5/reqreply_transport.go` (edit, Phase 0, SHIPPED) | `AttachServer`'s `Serve` now calls `handle.DecodeMergedWithFormats`/`EncodeWithFormats`/`ErrorResponseFor` (reflection-wired); `AttachClient`'s `Call`/`CallAsync` honor `ClientCallOptions`, route-declared `RequestFormats`/`Formats`, AND derive topic vars via `EncodeVars`+`BuildTopic` |
| `adapters/mqtt5/reqreply_transport.go` (edit, Phase 0b, SHIPPED) | Added `resolveCallFormatReflect`/`publishHandlerErrorReplyReflect` helpers; ALSO added `stats.TraceObserver` span support (`"mqtt5.serve"`/`"mqtt5.request"`) to `serverTransport.Serve`/`clientTransport.call` — a gap found DURING this phase, not part of the original 4 Phase 0 items |
| `adapters/mqtt5/reqreply.go` (edit, Phase 0b, SHIPPED — NOT deleted, see Phase 0b's own section for why) | `Serve`/`Call` bodies rewritten to construct a `&serverTransport{...}`/`&clientTransport{...}` directly and delegate — zero duplicate logic, EXACT SAME signatures (no breaking change); `CallHandle` is now a pure alias for `Call`; dead generic `resolveCallFormat`/`publishHandlerErrorReply` helpers removed |
| `adapters/zeromq/adapter.go` (edit, SHIPPED — same outcome as mqtt5) | `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer` bodies rewritten to delegate to `serverTransport`/`clientTransport`/`routerServerTransport`/`dealerClientTransport` directly — zero duplicate logic, EXACT SAME signatures, no breaking change, no example migration. `CallHandle` is now a pure alias for `Call`. Dead `serveRequest`/`serveRouterRequest`/generic `resolveCallFormat`/`sendHandlerErrorReply`/`sendRouterHandlerErrorReply` helpers removed. |
| `adapters/zeromq/reqreply_transport.go` (edit, SHIPPED) | Added zeromq-local `resolveCallFormatReflect`/`sendHandlerErrorReplyReflect`/`sendRouterHandlerErrorReplyReflect` helpers; wired `DecodeWithFormats`/`EncodeWithFormats`/`ErrorResponseFor`/`EncodeRequestWithFormats`/`DecodeResponseWithFormats` into all 4 transports; added `stats.TraceObserver` spans (`"zmq.serve"`/`"zmq.request"`) to all 4 — the SAME gap mqtt5 had, found proactively this time (not via a late regression). Merge-field DECODE support intentionally NOT added (zeromq REQ/REP wire format carries no topic frame — confirmed not applicable, matching the escape hatch's own scope). |
| `examples/reqreply-api/demo_global_security_dual_mode_call.go`/`demo_route_level_security_credential_error.go` (edit, SHIPPED) | Migrated off `mqtt5adapter.Call(...)`/`CredentialFunc` onto `mqtt5adapter.AttachClient(...)` + `Client.Call(...)` — no capability loss (the migration was optional, not forced by any remaining gap, but done anyway to demonstrate the Attach-based workflow end-to-end for security too), see "Example mini-project extension" section for detail |
| `docs/guides/mqtt5.md`, `docs/guides/zeromq.md` (untouched — still accurate) | No "escape hatch" section update needed — `Serve`/`Call`/`CallHandle` still exist, unchanged from the caller's perspective |
| `api/reqreply/middleware.go` (NEW, Phase 1, SHIPPED) | `Route.Use`/`HandleMW`/`ClientMW`, `routeMiddlewareOpt`/`handleMWOpt`/`clientMWOpt` internals, `applySecurityDeclarations`/`checkImplementationsDeclared`/`CheckCoverage`, `MissingSecurityMiddlewareError`/`UnknownMiddlewareImplementationError` — a direct port of `api/rest/middleware.go`'s structure, same names, scoped down (no D-0003 codec-declared bundling, no param-spec merging) |
| `api/reqreply/route.go` (edit, Phase 1, SHIPPED) | Added `Implementations`/`ClientImplementations` fields to `RouteHandle`; populated in `Route.Register`/`Route.ClientHandle` |
| `adapters/mqtt5/reqreply_transport.go` (edit, Phase 1, SHIPPED — content originally landed in a separate `reqreply_middleware.go`, FOLDED IN the same session after confirming `adapters/nethttp` has no equivalent dedicated file either) | mqtt5's `HandleMW`/`ClientMW` Fn-shape recognition + dispatch helpers (`validateServerImplementationShapes`/`applyGeneralServerMiddleware`/`runServerSecurityMiddleware`/`validateClientImplementationShapes`/`mergeCredentialUserProperties`), consulted by `serverTransport.Serve`/`clientTransport.call` |
| `adapters/mqtt5/reqreply.go` (edit, Phase 1, SHIPPED, BREAKING) | `ServeOptions.SecurityFunc`/`CallOptions.CredentialFunc` fields REMOVED ENTIRELY |
| `api/reqreply/middleware_test.go` (NEW, Phase 1, SHIPPED) | 9 tests — Route/RouteHandle-level (see "Unit test plan") |
| `adapters/mqtt5/reqreply_test.go`/`reqreply_transport_test.go` (edit, Phase 1, SHIPPED — no separate `reqreply_middleware_test.go` file created, folded into these two existing files instead) | 6 new/renamed tests replacing every SecurityFunc/CredentialFunc-based test (see "Unit test plan") |
| `render/asyncapi/v3/document.go` (edit, Phase 1b, SHIPPED) | New `Headers schema.Schema` field on `Message`, rendered inline (no `$ref`) in `buildMessage` when non-zero |
| `api/reqreply/middleware.go` (edit, Phase 1b, SHIPPED) | New `applyParamDeclarations` — collects/dedups `RequestHeaderParams`/`ResponseHeaderParams` from `rb.middlewares`, returns both the raw param specs (for `RouteHandle`) AND the rendered AsyncAPI schemas |
| `api/reqreply/route.go` (edit, Phase 1b, SHIPPED) | New `RouteHandle.RequestHeaderParams`/`RouteHandle.ResponseHeaderParams` fields, populated by `Route.Register`/`Route.ClientHandle`; `Route.Register` threads the rendered schemas into `Builder.registerRoute`'s two new params |
| `api/reqreply/builder.go` (edit, Phase 1b, SHIPPED) | `registerRoute` gained `reqHeaders, respHeaders schema.Schema` params, assigned into the Publish/Subscribe operation's `Message.Headers` |
| `adapters/mqtt5/reqreply_transport.go` (edit, Phase 1b, SHIPPED) | `FromUserPropertyParam`/`FromResponseUserPropertyParam` bridge functions; `userPropertyParamsFromHeaderSpecs`/`userPropertyParamsFromResponseHeaderSpecs` conversion helpers (reuse `UserPropertyParam`'s identical shape); Attach-time validation wired into `serverTransport.Serve` (request) and `clientTransport.call`'s `innerCall` closure (reply) — both reuse the EXISTING `validateUserProperties`/`MissingUserPropertyError`/`UserPropertyError` machinery unchanged |
| `examples/reqreply-api/routes/middleware.go` (NEW, Phase 1, SHIPPED) | `BearerAuthMw` — `middleware.SecurityScheme` declaration, mirrors `examples/rest-api/routes/middleware.go`'s `ProfileScopeMw`/`AdminScopeMw` |
| `examples/reqreply-api/handlers/security.go` (NEW, Phase 1, SHIPPED) | `VerifyBearer` — the paired server-side security Fn (unconditional grant; this example has one scope-less scheme, no scope-matching logic to demonstrate) |
| `examples/reqreply-api/mqtt5server/server.go` (edit, Phase 1, SHIPPED) | `SecuredComputeRoute`/`GlobalOnlyComputeRoute` registration updated to `.Use(routes.BearerAuthMw).HandleMW(&routes.BearerAuthMw, handlers.VerifyBearer)` |
| `examples/reqreply-api/demo_global_security_dual_mode_call.go`/`demo_route_level_security_credential_error.go` (edit, Phase 1, SHIPPED — no separate `client/client.go` credential functions added; kept inline in the demo files instead) | `.ClientMW()`-attached Route variants built per-demo, replacing the just-removed `CallOptions.CredentialFunc` calls from the previous round |
| `examples/reqreply-api/routes/routes.go` (edit, Phase 1b, SHIPPED) | New pristine `HeaderParamComputeRoute` — explicit `Security: []route.SecurityRequirement{}` opt-out from `mqtt5server`'s `Server.AddGlobalSecurity("bearerAuth")` (the SAME gap Phase 1 found for `ComputeRoute`, caught immediately this time) |
| `examples/reqreply-api/mqtt5server/server.go` (edit, Phase 1b, SHIPPED) | `apiKeyUserProp`/`traceUserProp` (mqtt5-specific, so declared here not in `routes/middleware.go`); `HeaderParamComputeRoute.Use(mqtt5adapter.FromUserPropertyParam(...), mqtt5adapter.FromResponseUserPropertyParam(...))`; new `Built.HeaderParamHandle` field |
| `examples/reqreply-api/demo_user_property_param_middleware.go` (NEW, Phase 1b, SHIPPED, Demo 6 — later demos renumbered 6→7→8) | Uses `mqtt5adapter.Call` directly (not `reqreply.Client`) — a raw, non-security User Property on a single call is exactly what `CallOptions.UserProperties` is for; demonstrates both the missing-required rejection and the present-success cases |
| ~~`examples/reqreply-api/demo_middleware_declare_implement.go` (NEW, Phase 1)~~ **NOT CREATED — descoped.** Demo 2/3's migration already exercises the full declare→implement→call chain (including `UnknownMiddlewareImplementationError`-adjacent coverage via the `ComputeRoute` opt-out fix); a SEPARATE demo file proving the identical mechanism again was judged redundant, not descoped for lack of time | N/A |

## Out of scope entirely (not a future phase — permanently excluded)

- `mqtt`(v3) — permanently out of scope for reqreply (protocol limitation,
  Decision 4 of d-0004) — CONFIRMED already fully realized: `adapters/mqtt`
  has zero `reqreply` references today. MQTT3's OWN pub/sub `.Use()`/
  `SubscribeMW`/`PublishMW` middleware is unaffected and already fully
  shipped — nothing left to do for MQTT3, on either side.
- `zeromq`'s Fn-shape design (Phase 1's security mechanism) — NOT bundled
  into this doc's Phase 1, tracked as a distinct follow-up.
  **UPDATE, found while cross-checking [ZeroMQ Security Mechanism](zeromq-security.md)**:
  this bullet's ORIGINAL "needs a NEW wire-level credential convention"
  framing (inherited from `d-0004` without re-checking it) is likely
  stale — `zeromq-security.md`'s own "Implication for
  `reqreply-middleware.md`" section (added as a follow-up to THIS
  session's own review) found the SAME in-payload `*Req` mechanism
  already proven for pub/sub security ALSO applies to reqreply's
  REQ/REP frames, with NO new wire-level work needed. Documented there,
  not yet acted on here — this doc's Phase 1 stays mqtt5-only as
  originally scoped; reconciling this bullet is itself the tracked
  follow-up (see [ZeroMQ Security Mechanism](zeromq-security.md) for the
  concrete Fn-shape proposal — this Fn-shape/security item remains the
  ONE genuinely open zeromq follow-up in this doc). **UPDATE — the
  capability-parity (format-override/`ErrorPattern`/`TraceObserver`) AND
  de-duplication-via-delegation work items themselves are NOW SHIPPED
  for zeromq too** (Phase 0's merge-field/format-override/`ErrorPattern`
  work items ARE transport-agnostic machinery reqreply's `RouteHandle`
  already has — ported to zeromq's 4 transports directly, independent of
  the security Fn-shape gap above; `Serve`/`Call`/`CallHandle`/
  `ServeRouter`/`CallDealer` now delegate to `serverTransport`/
  `clientTransport`/`routerServerTransport`/`dealerClientTransport`,
  mirroring mqtt5's Phase 0b outcome exactly — zero duplicate logic, no
  breaking change). Merge-field DECODE support was confirmed NOT
  APPLICABLE to zeromq (no topic frame at the wire level) rather than
  ported. Only the security Fn-shape item above remains open.)
- ~~Deleting `Serve`/`Call`/`CallHandle`/`ServeRouter`/`CallDealer` — these
  stay, unconditionally, mirroring REST's own kept-but-unnecessary
  `CallWithHandle`/`ServeOne` — Phase 2 closes the CAPABILITY gap that
  currently makes them load-bearing, it does not retire them.~~
  ~~**REVERSED — see Phase 0b above.** This was found to be a flawed
  analogy: REST's `CallWithHandle`/`ServeOne` are zero-duplication thin
  wrappers, these functions are a genuine duplicate dispatch
  implementation. They are NOW in scope for deletion, sequenced as
  Phase 0b, a REQUIRED prerequisite to Phase 1 — no longer permanently
  out of scope.~~ **RESOLVED (mqtt5), OUTCOME EVEN BETTER: NOT deleted
  after all.** Phase 0b SHIPPED for mqtt5 (see its own section above) by
  rewriting `Serve`/`Call`/`CallHandle`'s BODIES to delegate to
  `serverTransport`/`clientTransport` directly — zero duplicate logic
  achieved WITHOUT deletion, WITHOUT a breaking change, and WITHOUT any
  caller migration (the original "delete + rebuild + migrate" plan
  turned out to be more work than necessary once
  `reqreply.ServerTransport.Serve`/`ClientTransport.Call`'s ALREADY
  single-route/single-call scoping was noticed). **zeromq's own functions
  received the SAME treatment in a follow-up pass, ALSO SHIPPED** — see
  "Out of scope entirely" above for the one remaining zeromq item
  (security Fn-shape), which is unrelated to this de-duplication work.
- Extending Phase 1b to `mqtt`(v3)/`zeromq` — mqtt5-only, same reasoning
  as the rest of this doc's Phase 1 scope.
- A `ports.ReqReplyPattern` change — none needed; `ports.PluginReqReplyPattern`
  already delegates to `Route.Register`, so `Implementations`/
  `ClientImplementations` populate for free once `Route.Register` itself
  populates them (same "no ports-specific work needed" conclusion d-0004's
  own Decision 3 already reached for this exact mechanism).

## Open design decisions — ALL FINALIZED

Every decision below is now LOCKED IN for this doc (kept in place with
their full reasoning, not deleted, so the "why" survives for
implementation time — mirrors this codebase's own convention of keeping
resolved design questions visible rather than scrubbing them).

1. **RESOLVED — `CheckCoverage`-equivalent runs at adapter-Serve-time**,
   inside `mqtt5.AttachServer`'s returned `ServerTransport.Serve`, exactly
   like `rest.CheckCoverage` is called from `adapters/nethttp/serve.go`,
   NOT inside `api/reqreply` itself. Locked in for full REST parity. The
   one wrinkle reqreply has that REST doesn't: `Server.Serve` is a
   transport-agnostic dispatcher across MULTIPLE adapters at once (mqtt5,
   zeromq, and eventually others) — so a `*reqreply.Server` with routes
   split across mqtt5 (checked) and zeromq (not yet implemented, silently
   unchecked) has that asymmetry as an EXPLICIT, TESTED, DOCUMENTED
   behavior (matching zeromq's other current v1-scope gaps: no
   `RequestFormats`/`Formats`/`ErrorPattern` support either), not a silent
   inconsistency — a required test (`TestAttachServer_CheckCoverage_
   MissingSecurityMiddlewareError` in the Unit test plan above) makes this
   explicit.
2. **RESOLVED — `reqreply.WithSecurityScheme` is REPLACED, not kept
   as a parallel style.** `.Use(middleware.SecurityScheme(...))` becomes
   THE declare-time mechanism, mirroring REST's CURRENT (post-d-0001)
   state exactly — REST does not have two parallel declare-time security
   mechanisms today, and reqreply shouldn't either once this ships. This
   reverses the previous revision's "leaning toward keeping it as-is":
   the user explicitly asked for "the SAME declarative style... for the
   user," not two coexisting styles that both do the same job.
   `reqreply.WithSecurityScheme`/`reqreply.SecurityScheme` (the OLD types,
   shipped just one phase ago in this session's own Phase 0-1 work)
   become DEPRECATED-but-kept aliases — mirrors `events.WithSecurityScheme`'s
   own precedent (kept for backward compat after REST's OWN Revision 2
   removal, not deleted) — zero breaking changes for any existing caller.
3. ~~Exact struct field names for `UnknownMiddlewareImplementationError`/
   `MissingSecurityMiddlewareError`~~ **RESOLVED** — confirmed via code
   that both `api/rest` and `api/events` each define their OWN
   package-local `{Route/Topic, Scheme}`-shaped copy (not a shared
   cross-package type); reqreply follows the same pattern — see
   "Structured errors" above.
4. **RESOLVED, then REVERSED again, then SHIPPED (mqtt5) — the new
   PAIRED server-side security Fn ADOPTS REST's scope-GRANT model**:
   `func(ctx, msg, reqs) (map[string][]string, error)`, combined via
   `middleware.CheckScopes`.
   ~~reqreply's OLD `SecurityFunc` (`func(ctx, msg, reqs) error`, no
   scopes) stays UNCHANGED as the permanent escape hatch (same treatment
   `WithSecurityScheme`/`CredentialFunc` get throughout this doc) — only
   the NEW `HandleMW`-driven path adopts the scope-grant model.~~
   **REVERSED — mirrors REST's OWN D-0001 precedent instead (confirmed
   via code: `adapters/nethttp.Options`'s own `"BREAKING: Observer and
   SecurityFunc are REMOVED"` doc comment) — a direct control question
   asked and answered before Phase 1 began: "does REST still have a
   `CredentialFunc`-as-Options-field escape hatch?" Confirmed NO — REST
   fully unified onto ONE credential mechanism
   (`Implementations`/`ClientImplementations`), consulted by BOTH its
   escape hatch AND `Client.Call`.** `mqtt5.ServeOptions.SecurityFunc`/
   `CallOptions.CredentialFunc` were REMOVED ENTIRELY (SHIPPED, breaking
   change) — `Implementations`/`ClientImplementations` are now the ONLY
   mechanism, for BOTH `Serve`/`Call`/`CallHandle` (which delegate to the
   SAME `Attach`-based transports since Phase 0b) and `Attach`-based
   dispatch directly. **zeromq's `SecurityFunc`/`CredentialFunc` (both
   events pub/sub AND reqreply) are explicitly UNAFFECTED by this
   removal** — zeromq has no `.Use`/`HandleMW`/`ClientMW` mechanism yet
   (a separate, not-yet-started follow-up, see
   `docs/roadmap/zeromq-security.md`'s own new reminder section) —
   removing them now, with no replacement shipped, would leave zeromq
   with ZERO security mechanism, a pure regression. When zeromq's own
   Fn-shape phase eventually ships, it should ALSO remove
   `SecurityFunc`/`CredentialFunc` at that point, mirroring mqtt5's
   Phase 1 exactly.
5. **RESOLVED — Phase 1b (User Property param-as-middleware) is IN
   SCOPE for this doc, as an explicit sub-phase, not deferred to a
   separate roadmap doc.** The confirmed `render/asyncapi/v3` "no message
   headers today" prerequisite makes it larger than Phase 1's security
   work, which is why it's split out as "Phase 1b" rather than folded
   silently into Phase 1 — but it is resolved as IN-SCOPE for this same
   document, addressing the user's explicit "headers/cookies... request
   AND response path" parity request rather than punting it to a future,
   separate design doc.

## Delivery risk & staging

Added after critical review (finding #4 above) — this is a large,
multi-phase rework touching a REFLECTION-based Fn-shape dispatch
mechanism (mqtt5's `ServerTransport`/`ClientTransport`), where a wrong
or incomplete implementation fails LOUDLY at runtime (a type assertion
miss) rather than at compile time — exactly the profile this codebase's
own `docs/design/d-0001-rest-middleware-workflow-simplification.md`
"Lessons Learned" warns about: an "equivalent" claim shipped unverified
hid two genuine gaps and a security regression until wholesale test
migration surfaced them.

**Recommended staging — verify fully after EACH phase, not only at the
end**:

0. **SHIPPED — Phase 0 shipped and is FULLY verified alone FIRST**
   (`go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...` incl.
   `-race`, `just check`, `for d in examples/*/; do go run ./$d; done` —
   ALL clean/zero-FAIL/zero-findings) — merge-fields, per-call format
   overrides, `ErrorPattern` support in the Attach shims, ALL for mqtt5.
   This was a REQUIRED prerequisite (renamed/reframed from the earlier
   "opportunistic Phase 2"), not optional, because Phase 0b's
   de-duplication depends on it closing the old functions' last real
   justification first — confirmed closed, see the Phase 0 section's
   own "SHIPPED" banner for the full evidence.
0b. **SHIPPED — Phase 0b shipped and verified alone next, outcome
   BETTER than planned (no deletion, no breaking change, no example
   migration)**: `Serve`/`Call`/`CallHandle` KEPT their exact
   signatures, bodies rewritten to delegate to `serverTransport`/
   `clientTransport` directly (zero duplicate logic) — see Phase 0b's
   own section above for the full "why a simpler fix than planned"
   story, including the TraceObserver gap found and closed along the
   way. Full verification (`go build`, `go vet`, `gofmt`, `go test
   -race` across the WHOLE repo, `just check`, all examples) — all
   clean, BEFORE Phase 1 starts.
1. **Phase 1 ships and is FULLY verified alone** (same full verification
   battery) BEFORE Phase 1b work starts — this is the highest-value,
   most load-bearing phase (the core declare/implement split) and the
   one most likely to reveal a real Fn-shape or coverage-check gap
   early, while the surface area is still small. (Ships AFTER Phase
   0/0b now, not before — the delivery ORDER changed, not Phase 1's own
   design.)
2. **SHIPPED — Phase 1b shipped and verified alone next**, as planned:
   its `render/asyncapi/v3` change is a genuinely separate subsystem
   (spec rendering, not dispatch) — verified by printing the real
   rendered spec in `examples/reqreply-api`'s Demo 7 and visually
   confirming BOTH the request and reply channel's `headers` schema
   (`X-API-Key`/`X-Trace-Id` properties, `required` list) match the
   AsyncAPI 3.0 Message Object shape, not just "it doesn't error." Full
   verification battery (`gofmt`/`go build`/`go vet`/`go test -race`/
   `just check`/all examples) — all clean, same as every prior phase.
3. ~~Migrate `examples/reqreply-api`'s Demo 2/3 off the escape hatch as
   PART OF Phase 0b~~ **MOOT — no migration needed.** Phase 0b's actual
   outcome (delegation, not deletion) means Demo 2/3 needed ZERO code
   changes; this staging item is obsolete.
4. At EVERY stage, mirror this session's own established discipline for
   this exact codebase: a reflection-based dispatcher "equivalent to
   existing code" is a HYPOTHESIS until a representative sample of real
   test/example call sites is actually migrated onto it and re-run — a
   design review reading convincingly is not equivalent to a passing
   test (see `.github/skills/plan-a-new-codex-feature/SKILL.md`'s own
   Gotchas for the exact precedent this recommendation mirrors).

## Test plan

See "Unit test plan" above — mirrors `api/rest/middleware_test.go`'s own
test IDs directly (reqreply's primary reference, not events'), adjusted only
for reqreply's mqtt5-only Phase 1 adapter scope.
