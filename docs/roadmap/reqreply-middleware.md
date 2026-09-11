# ReqReply Middleware — declare/implement split for `api/reqreply`

> **Status:** Design draft — not yet implemented. Spun out of
> `docs/design/d-0004-reqreply-workflow-simplification.md`'s own "Decision 3"
> (which first proposed this, then explicitly deferred it — see that doc's
> "Remaining open items"), now that Phases 0-5 of the reqreply `Server`/
> `Client`/`Attach` rework have fully shipped and the doc has been promoted.
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

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| `reqreply.Route.Use(mws ...middleware.RouteMiddleware) Route[Req,Resp]` — signature IDENTICAL to `rest.Route.Use` | Retrofitting the OLD `SecurityFunc`/`CredentialFunc` `Options` fields as a parallel, permanent mechanism — this roadmap's goal is convergence with REST's workflow, not a second escape hatch alongside the new one (see "Open design decisions" for the migration question) |
| `reqreply.Route.HandleMW(mw *middleware.Middleware, fn any) Route[Req,Resp]` (server-side) — signature IDENTICAL to `rest.Route.HandleMW` | General-purpose request/response ENRICHMENT shapes beyond security (e.g. a "mutate the decoded Req before the handler runs" hook) — `rest.Route` doesn't have this either; not introducing new surface REST itself lacks |
| `reqreply.Route.ClientMW(mw *middleware.Middleware, fn any) Route[Req,Resp]` (client-side) — signature IDENTICAL to `rest.Route.ClientMW` | `mqtt`(v3)'s Fn-shape design (permanently out of reqreply's scope per Decision 4 of d-0004 — publish-side has no credential mechanism by protocol limitation) |
| `RouteHandle.Implementations []middleware.ServerImplementation` / `RouteHandle.ClientImplementations []middleware.ClientImplementation` fields, populated by `Route.Register`/`Route.ClientHandle` — mirrors `rest.RouteHandle`'s identical fields | `zeromq`'s Fn-shape design for Phase 1 (see "Toolchain / dependency decisions" — deferred to a follow-up, tracked as an explicit open item, not silently dropped) |
| `mqtt5.AttachServer`/`AttachClient`'s reflection shim reading `handle.Implementations`/`ClientImplementations` automatically (mirrors `Server.Serve`/`Client.Call` already reading `handle.GlobalSecurity` today) | Retrofitting `Serve`/`Call`'s existing `Options.SecurityFunc`/`CredentialFunc` fields to ALSO consult `Implementations` — those are the documented escape hatch, kept deliberately separate (see d-0004's Phase 5 record) |
| Register-time `checkImplementationsDeclared` equivalent (`UnknownMiddlewareImplementationError` when a `HandleMW`/`ClientMW` implementation names a scheme nobody `.Use()`'d) — reuses REST's exact check, not a reimplementation | Serve-time `CheckCoverage` equivalent for `Server.Serve`/`Client.Call`'s NEW Attach-based path specifically — IN SCOPE for mqtt5, but zeromq's version depends on the deferred Fn-shape work above |

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
Fn-shape needs its own dedicated design pass — raw multipart frames carry no
per-message metadata slot the way MQTT5's User Properties do, so a
`ServerImplementation`/`ClientImplementation` verifying/supplying a
credential needs a new wire-level convention (an additional frame) before
any Fn shape can be finalized. This was flagged as an open question in
earlier design work and remains unresolved here, carried forward rather than
silently dropped.

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
// adapters/mqtt5/adapter.go already recognizes multiple Fn shapes today:

// Server-side (paired, Satisfies non-empty — security-verifying):
func(ctx context.Context, msg *pahomqtt5.Publish, reqs []route.SecurityRequirement) error

// Server-side (unpaired, Satisfies empty — general-purpose):
func(ctx context.Context, msg *pahomqtt5.Publish) error

// Client-side (paired, Satisfies non-empty — credential-supplying):
func(ctx context.Context, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error)

// Client-side (unpaired, Satisfies empty — general-purpose, wraps the
// publish step, mirrors adapters/mqtt5/adapter.go's existing
// wrapPublishGeneral precedent for events):
func(ctx context.Context, next func(context.Context) error) error
```

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

No new `stats.Observer` extension needed. `SecurityObserver.RecordSecurityRejection`
already fires from `mqtt5`'s existing `ServeOptions.SecurityFunc`/
`CallOptions.CredentialFunc` rejection paths — the NEW `HandleMW`/`ClientMW`
paths must call the SAME guarded `stats.SecurityObserver` type-assertion on
their own rejection paths, mirroring REST's identical wiring (confirmed via
`.github/skills/review-go-codex/references/history.md`'s pub/sub G1 finding:
a missed `SecurityObserver` call on ONE adapter's security-rejection path
was a real, shipped bug in a sibling pattern — the review-go-codex skill's
own "Gotchas" list flags this exact regression class).

## Unit test plan

Mirror `api/rest/middleware_test.go`'s own test IDs (reqreply's PRIMARY
reference, adjusted only for reqreply's mqtt5-only Phase 1 adapter scope —
NOT `api/events/builder_test.go`'s `Subscriber`/`Publisher`-split naming,
since reqreply has one `Route`, not two roles):

| Test | Verifies |
|---|---|
| `TestRoute_Use_Chainable` | `.Use(mw1).Use(mw2)` and `.Use(mw1, mw2)` produce equivalent opts, in attachment order |
| `TestRoute_Use_DoesNotMutateOriginal` | `Route` is immutable — `.Use` returns a distinct value |
| `TestHandleMW_Paired_DerivesSatisfiesFromSecurity` | `mw.Security != nil` → `Satisfies == []string{mw.Security.SchemeName}` |
| `TestHandleMW_Unpaired_GeneralPurpose_EmptySatisfies` | `mw == nil` → `Satisfies` empty, Fn always runs |
| `TestClientMW_Paired_DerivesSatisfiesFromSecurity` | client-side mirror of the above |
| `TestClientMW_MultipleCallsForSameScheme_DistinctNames` | mirrors REST's `#1`/`#2` attachment-order-index naming, needed for the SAME reason (conflict-check heuristics) |
| `TestRoute_Register_PopulatesImplementations` | `Route.Register(server)` copies `HandleMW`/`ClientMW`-built implementations onto the returned `*RouteHandle` |
| `TestRoute_ClientHandle_PopulatesImplementations` | same, for the no-`Server`-needed path |
| `TestRoute_Register_UnknownMiddlewareImplementationError` | a `HandleMW`/`ClientMW` naming a scheme nobody `.Use()`'d fails at Register/ClientHandle time |
| `TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError` | a declared `Security` requirement with no matching `ServerImplementation` fails at `Server.Serve` (or `AttachServer`, per the open design decision below) |
| `TestAttachServer_HandleMW_PairedSecurityFn_Verifies` | mqtt5's new Fn shape actually gets called and can reject |
| `TestAttachClient_ClientMW_PairedCredentialFn_Supplies` | mqtt5's new client Fn shape actually gets called and supplies a credential |
| `TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns` | unpaired Fn runs regardless of declared Security |
| `TestAttachServer_SecurityRejection_CallsSecurityObserver` | mirrors pub/sub G1's regression test — the NEW path must call `SecurityObserver` too |

## Files to create

| File | Responsibility |
|---|---|
| `api/reqreply/middleware.go` | `Route.Use`/`HandleMW`/`ClientMW`, `routeMiddlewareOpt`/`handleMWOpt`/`clientMWOpt` internals — a direct port of `api/rest/middleware.go`'s structure, same names |
| `api/reqreply/route.go` (edit) | Add `Implementations`/`ClientImplementations` fields to `RouteHandle`; populate them in `Route.Register`/`Route.ClientHandle` |
| `adapters/mqtt5/reqreply_middleware.go` (or fold into `reqreply_transport.go`) | mqtt5's `HandleMW`/`ClientMW` Fn-shape recognition + dispatch, consulted by `AttachServer`'s `Serve`/`AttachClient`'s `Call`/`CallAsync` |
| `api/reqreply/middleware_test.go` | Unit test plan above (Route/RouteHandle-level tests) |
| `adapters/mqtt5/reqreply_middleware_test.go` | Unit test plan above (adapter-level tests) |

## Out of scope (Phase 2+)

- `mqtt`(v3) — permanently out of scope (protocol limitation, Decision 4 of
  d-0004).
- `zeromq`'s Fn-shape design — needs a NEW wire-level credential convention
  before ANY Fn shape can be finalized; tracked as a distinct follow-up, not
  bundled into this doc's Phase 1.
- Retrofitting `Serve`/`Call`'s existing `Options.SecurityFunc`/
  `CredentialFunc` to ALSO read `Implementations`/`ClientImplementations` —
  those functions are the documented, permanent escape hatch (see d-0004's
  Phase 5 record); this doc's new mechanism is additive, scoped to the
  `Server`/`Client`+`Attach` workflow only.
- A `ports.ReqReplyPattern` change — none needed; `ports.PluginReqReplyPattern`
  already delegates to `Route.Register`, so `Implementations`/
  `ClientImplementations` populate for free once `Route.Register` itself
  populates them (same "no ports-specific work needed" conclusion d-0004's
  own Decision 3 already reached for this exact mechanism).

## Open design decisions (to resolve before/during implementation)

1. **Where does the `CheckCoverage`-equivalent run: `Server.Serve` (transport-
   agnostic, in `api/reqreply`) or inside `mqtt5.AttachServer`'s returned
   `ServerTransport.Serve` (adapter-specific, mirroring REST's own precedent
   exactly)?** Working assumption, since this doc's whole framing is
   "mirror REST": place it in the adapter, exactly like `rest.CheckCoverage`
   is called from `adapters/nethttp/serve.go`, NOT inside `api/rest` itself.
   The one wrinkle reqreply has that REST doesn't: `Server.Serve` is meant
   to be a transport-agnostic dispatcher across MULTIPLE adapters at once
   (mqtt5, zeromq, and eventually others, per other roadmap docs) — so if
   the check lives per-adapter, a `*reqreply.Server` with routes split
   across mqtt5 (checked) and zeromq (not yet implemented, silently
   unchecked) needs that asymmetry to be an explicit, tested, DOCUMENTED
   behavior (matching zeromq's other current v1-scope gaps), not a silent
   inconsistency. Confirm this against the real `ServerTransport.Serve`
   dispatch shape before implementing, not merely assumed from this
   paragraph.
2. **Should `reqreply.WithSecurityScheme` + `RouteMeta.Security` stay
   UNCHANGED as the declare-time half** (this doc only replaces the
   IMPLEMENT-time half — `SecurityFunc`/`CredentialFunc` → `HandleMW`/
   `ClientMW`), or does the declare-time half also need a `.Use()`-based
   rework mirroring `middleware.SecurityScheme`'s constructor shape? REST's
   OLD `rest.WithSecurityScheme` was REMOVED in favor of `middleware.
   SecurityScheme`+`.Use()`. reqreply's `WithSecurityScheme` is RELATIVELY
   NEW (shipped as part of this session's Phase 0-1 security parity work,
   not legacy baggage) — leaning toward keeping it as-is and ONLY adding the
   implement-time `.HandleMW()`/`.ClientMW()` half, but this needs explicit
   confirmation before implementation, not an assumption carried in
   silently.
3. ~~Exact struct field names for `UnknownMiddlewareImplementationError`/
   `MissingSecurityMiddlewareError`~~ **RESOLVED** — confirmed via code
   that both `api/rest` and `api/events` each define their OWN
   package-local `{Route/Topic, Scheme}`-shaped copy (not a shared
   cross-package type); reqreply follows the same pattern — see
   "Structured errors" above.

## Test plan

See "Unit test plan" above — mirrors `api/rest/middleware_test.go`'s own
test IDs directly (reqreply's primary reference, not events'), adjusted only
for reqreply's mqtt5-only Phase 1 adapter scope.
