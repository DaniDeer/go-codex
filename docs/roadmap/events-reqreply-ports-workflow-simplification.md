# Events / ReqReply / Ports Workflow Simplification — design decisions

> **⚠️ SUPERSEDED (pub/sub scope):** A critical re-review found this doc's
> Decision 1 borrowed REST/`reqreply`'s fixed `Register`/`ClientHandle`
> role split onto pub/sub without checking whether pub/sub's role
> structure actually fits — it does not (pub/sub has 3 independent roles:
> spec owner, subscriber, publisher; REST/reqreply always have exactly 2,
> fixed-paired). This doc's `api/events` (pub/sub)-scoped content is now
> **superseded** by [Pub/Sub Workflow Simplification](pubsub-workflow-simplification.md),
> which restarts the pub/sub design fresh from that finding. This doc's
> `api/reqreply`-scoped content (Decisions 1/3's reqreply halves) remains
> **on hold** — its own dedicated review is deferred until the pub/sub
> doc reaches design-complete status. The already-started `api/reqreply`/
> `adapters/mqtt5` reqreply implementation (uncommitted at the time of
> this notice) is paused, untouched, pending that review.
>
> **Status:** Design decisions resolved for the core translation (4 of 4
> originally-open questions) — not yet implemented. Spun out from
> [Middleware Workflow Simplification](../design/middleware-workflow-simplification.md)
> while reviewing that doc's implications beyond REST. The deferred
> precondition ("implement the REST redesign first, then return here") is
> now MET — REST's redesign shipped, AND its stream variant (SSE client
> consumption) shipped and was confirmed via a dedicated `review-go-codex`
> pass to generalize the same core decisions cleanly to a second,
> structurally different boundary shape. This refine pass resolves the
> doc's original "How the new REST pattern might translate" speculative
> starting points into the 4 concrete "Decisions" below, mirroring
> `middleware-workflow-simplification.md`'s own decision-by-decision
> structure. A follow-up pass (security design deep-dive) confirmed the
> full adapter security capability matrix, added an exhaustive "Escape
> hatches" section, answered "protocol-native vs. sophisticated OAuth2/
> scopes middleware" (both, selectable — same as REST today), and
> confirmed AsyncAPI security-scheme rendering is ALREADY fully complete
> (no gap). Several sub-questions remain genuinely open — see "Remaining
> open items" near the end — and implementation has not started.
> [← Back to Roadmap](index.md)

---

## Why this exists

While reviewing `middleware-workflow-simplification.md`'s implications
for `api/events`, `api/reqreply`, and `ports`, it became clear the
situation is NOT "these boundaries are one generation behind REST and
need to catch up to the OLD design before adopting the new one" — it's
closer to "these boundaries never adopted ANY generation of the
`middleware` package, and can skip straight to whatever REST's new
declare/implement/`Serve`/`Call` pattern ends up being." That reframing
is the main reason this is worth its own doc rather than a REST-doc
footnote.

## Confirmed current state (via code inspection, not assumption)

- **`api/events` has ZERO `middleware` package integration.** No
  `WithMiddleware`, no `Channel.Use()`, nothing — confirmed via
  repo-wide grep across `api/events/*.go` (non-test files): zero hits
  for `middleware.`.
- **`adapters/mqtt5`, `adapters/mqtt` (v3), and `api/reqreply` all still
  use the OLD, PRE-`middleware`-package pattern** — plain
  `Options.SecurityFunc`/`CallOptions.CredentialFunc` closures, the
  EXACT mechanism removed from `adapters/nethttp`/`chi` during REST's
  own Phase 1 (well before this session's Revision 2 / this session's
  redesign). Confirmed via `grep` — these fields are live today in
  `adapters/mqtt5/adapter.go` (`SecurityFunc func(context.Context, *pahomqtt5.Publish,
  []route.SecurityRequirement) error` on `SubscribeOptions`;
  `CredentialFunc func(context.Context, []route.SecurityRequirement) ([]UserProperty, error)`
  on publish-side options), `adapters/mqtt/adapter.go` (same shape), and
  `api/reqreply/route.go` (same `ServeOptions.SecurityFunc`/
  `CallOptions.CredentialFunc` naming as the old REST design).
- **`adapters/zeromq` has NO security model at all** — not even the old
  `SecurityFunc` pattern. Confirmed via grep: zero hits for
  `SecurityFunc`/`CredentialFunc`/`middleware.` in `adapters/zeromq/*.go`.
- **`adapters/mcpgo`/`api/mcp` has neither, BY DESIGN** — MCP security is
  handled externally to the adapter, unaffected either way, consistent
  with prior review notes elsewhere in this repo's docs.
- **`ports` never attaches ANY middleware/security implementation to a
  Pattern-built route today — confirmed even for REST**, the most
  mature boundary. `adapters/nethttp/binding.go`'s own comment on its
  SSE port adapter:

  ```go
  // RegisterSSE now returns an error for eager middleware Fn-shape
  // validation (see docs/roadmap/declarative-middleware.md) — unreachable
  // here since no middleware is attached at this call site.
  _ = RegisterSSE(a.mux, a.handle, fn, a.opts.Options)
  ```

  `ports`' `mqtt5`/other event-adapter bindings similarly just forward
  whatever `SecurityFunc`/`CredentialFunc` field their own `PortOptions`-
  equivalent struct carries straight to the underlying adapter — no
  `ports`-level middleware concept exists at all.
- **`ports.Pattern`'s `Opts []rest.RouteOpt`/`[]events.ChannelOpt` fields
  already accept a `.Use(mw)`-style declare-time attachment today, with
  ZERO `ports`-specific code changes needed** — since `Pattern` is a
  thin wrapper that builds its handle via the SAME
  `Route.Register(builder)`/`Channel.Register(builder)` call a
  hand-declared route makes, ANY valid `RouteOpt`/`ChannelOpt` (including
  a middleware-declaring one) already flows through unmodified. The gap
  is entirely on the REGISTER/IMPLEMENT side, not the declare side.

## Decisions (resolved this pass, via direct code investigation)

### Decision 1 — `Channel`/`reqreply.Route` gain `.Use()`/`.HandleMW()`/`.ClientMW()`

Mirrors `rest.Route`'s equivalents EXACTLY at the declaration layer — this
part is fully transport-agnostic, unlike the adapter-side Fn shapes
(Decision 3). Confirmed via code: `events.ChannelOpt`/`reqreply.RouteOpt`
are the SAME `interface{ applyRoute(*builder) }`-shaped interfaces
`rest.RouteOpt` already is — ready to extend with zero structural changes.

New API surface:
- `events.WithMiddleware(mws ...middleware.Middleware) ChannelOpt` /
  `reqreply.WithMiddleware(mws ...middleware.Middleware) RouteOpt`
- `Channel.Use(mws ...middleware.Middleware) Channel[T]` /
  `reqreply.Route.Use(mws ...middleware.Middleware) Route[Req, Resp]`
- `Channel.HandleMW(mw *middleware.Middleware, fn any) Channel[T]` —
  SUBSCRIBE-side verify (mirrors `Route.HandleMW`'s nilable-`mw`
  paired/unpaired semantics exactly)
- `Channel.ClientMW(mw *middleware.Middleware, fn any) Channel[T]` —
  PUBLISH-side credential supply (mirrors `Route.ClientMW`)
- Same 4 for `reqreply.Route` (request-side verify via `HandleMW`,
  reply-side — actually request-publish-side — credential via
  `ClientMW`, matching `reqreply`'s existing `Serve`/`Call` role split)

`ChannelHandle`/`reqreply.RouteHandle` gain two new fields (confirmed via
code: NEITHER has them today):
- `Implementations []middleware.ServerImplementation`
- `ClientImplementations []middleware.ClientImplementation`

populated by `Channel.Register`/`reqreply.Route.Register` (server-side) and
`Channel.ClientHandle`/`reqreply.Route.ClientHandle` (client-side) exactly
like `RouteHandle` already populates its own — same `rb.impls`/
`rb.clientImpls` accumulation-then-apply pattern `api/rest` already ships.

### Decision 2 — NO whole-builder `Serve` needed (resolves the biggest open question)

Confirmed via code: `mqtt5`/`mqtt`(v3)/`zeromq` NEVER had REST's OLD
per-route `Handler`+`Register` split to begin with. `Subscribe`/`Publish`/
`Serve`/`Call` have ALWAYS taken one `*ChannelHandle`/`*RouteHandle`
directly, explicitly, per call — there has never been a
`SecurityFunc`/`CredentialFunc`-carrying whole-builder walk anywhere in
these three adapters. REST's "whole-API `Serve`" complexity was entirely
about CONSOLIDATING that old per-route split into one call over an
accumulated `Builder` — it was never about middleware attachment per se.
Since events/reqreply never had the split, there is nothing to
consolidate.

**Resolution: the existing per-handle call pattern (`Subscribe(ctx, sock,
handle, fn, opts)`, `mqtt5.Call(ctx, client, router, handle, req, opts)`,
etc.) stays EXACTLY as-is — no `mqtt5.Serve(client, builder)`-style
whole-builder entry point is introduced.** Only the SOURCE of the security/
credential function changes: from a per-call `Options.SecurityFunc`/
`CredentialFunc` field to the handle-attached `Implementations`/
`ClientImplementations` populated at declare time (Decision 1) —
`Subscribe`/`Publish`/`Serve`/`Call` read `handle.Implementations`/
`handle.ClientImplementations` automatically, the same way
`nethttp.Serve`/`Call` now read `handle.Implementations`/
`ClientImplementations` instead of a per-call `Options` field.

### Decision 3 — per-adapter Fn shapes (transport-specific, NOT one universal shape)

Unlike REST — ONE wire transport (HTTP) shared by BOTH `nethttp`/`chi`, so
ONE Fn shape (`func(context.Context, *http.Request, *Req) (map[string][]string, error)`)
works for both server adapters — pub/sub has THREE incompatible native
message envelope types (`*pahomqtt5.Publish`, `pahomqtt.Message`, raw ZMQ
frames). **Each adapter must recognize its OWN Fn shape** — a direct
upgrade of its EXISTING `SecurityFunc`/`CredentialFunc`, gaining two things
REST's shape already has that these currently lack: (a) access to the
DECODED value as `*T` (pointer — parity with REST's `*Req` write-back
capability, letting a verify step also derive/mutate a claim into the
message struct), and (b) a returned scope-grants `map[string][]string` for
`middleware.CheckScopes` integration (REST already has this via
`runSecurityMiddleware`; `mqtt5`/`mqtt`/`zeromq` today do a plain
verify-only `error` return with no grant/scope concept at all).

- **`mqtt5` — FULLY RESOLVED** (has the clearest existing model to
  translate 1:1, confirmed via `adapters/mqtt5/adapter.go`'s
  `makeSubscribeMessageHandler`/`Publish`): subscribe-side
  `func(context.Context, *pahomqtt5.Publish, *T) (map[string][]string, error)`;
  publish-side (same shape as today, just relocated from `PublishOptions`
  to a `ClientMW`-attached `ClientImplementation`)
  `func(context.Context, []route.SecurityRequirement) ([]UserProperty, error)`.
- **`api/reqreply` (server via `mqtt5.Serve`/`zeromq.Serve`, client via
  `mqtt5.Call`/`zeromq.Call`/`CallDealer`) — FULLY RESOLVED**, identical
  transformation — confirmed `SecurityFunc`/`CredentialFunc` already use
  the EXACT SAME naming in `api/reqreply/route.go` as `mqtt5`'s.
- **`mqtt` (v3) and `zeromq` — NOT resolved this pass, flagged for a
  follow-up investigation pass**:
  - `mqtt` (v3)'s subscribe-side `SecurityFunc` exists
    (`func(ctx, pahomqtt.Message, reqs) error` in `adapters/mqtt/adapter.go`),
    so its subscribe-side translation likely mirrors `mqtt5`'s directly —
    but its publish-side `CredentialFunc` equivalent was NOT confirmed to
    exist during this pass and needs a dedicated check before finalizing.
  - `zeromq` has **NO credential-carrying mechanism of any kind today** —
    confirmed via grep: zero hits for `SecurityFunc`/`CredentialFunc`/
    `middleware.` anywhere in `adapters/zeromq/*.go`. `Subscribe`/`Publish`
    only ever see `[topic, payload]` frames — there is no header/property
    slot to extract a credential FROM. Giving `zeromq` security parity
    would require inventing a NEW envelope convention (e.g. an additional
    frame carrying credential data) — this is genuinely NEW protocol
    design, not a mirror of an existing mechanism, and is explicitly
    NOT decided here.

#### Decision 3 follow-up pass — confirmed findings, security design questions answered

A dedicated follow-up investigation (deeper current-state audit of
`adapters/mqtt`/`mqtt5`/`zeromq` + `docs/features/security.md`) resolved
the two items the original Decision 3 left as "not confirmed" or implicit,
and directly answers two design questions raised while reviewing:
"should a caller be able to choose protocol-native credential checking OR
a sophisticated OAuth2-scope-checking middleware?" and "how do security
schemes render into the AsyncAPI spec?"

**`mqtt` (v3) publish-side `CredentialFunc` — CONFIRMED to not exist, by
protocol limitation, not oversight.** `adapters/mqtt/adapter.go`'s
`PublishOptions` has its own doc comment stating this explicitly: MQTT
3.1.1 exposes NO per-message metadata channel at all (no User
Properties — that is MQTT5-only), so there is nowhere to carry a
per-publish credential even in principle. `mqtt` (v3) will NEVER have
message-level publish-side credentials — any future `ClientMW` design for
`mqtt` (v3) must not assume feature parity with `mqtt5` is achievable here.

**Full adapter capability matrix** (confirmed via code, all three
transports):

| Capability | `mqtt` (v3) | `mqtt5` | `zeromq` |
|---|---|---|---|
| Connection-level `SecuredClient`/`ConnectSecurityScheme` | ✅ | ✅ | ❌ (no CONNECT-credential concept in base REQ/REP/PUB/SUB) |
| Message-level subscribe-side `SecurityFunc` | ✅ | ✅ | ❌ |
| Message-level publish-side `CredentialFunc` | ❌ (protocol limit) | ✅ | ❌ |
| Built-in codec-based credential FORMAT check (server) | ❌ (no property to extract) | ✅ | ❌ |
| `api/reqreply` `Serve`/`Call` security (any layer) | n/a (reqreply not implemented over `mqtt` v3) | ✅ (mirrors pub/sub message-level exactly) | ❌ (confirmed: `Descriptor.Security`/`GlobalSecurity` never even READ in `Serve`/`Call`/`CallHandle`/`CallDealer`) |
| Scope-grant / `middleware.CheckScopes` integration | ❌ | ❌ | ❌ |
| Handle-attached implementation (vs. per-call `Options`) | ❌ | ❌ | ❌ |

The bottom 2 rows are unimplemented across ALL THREE transports — exactly
the gap Decision 1 (handle-attached `Implementations`/`ClientImplementations`)
and this Decision already target closing.

**Design answer — protocol-native vs. sophisticated OAuth2/scopes: BOTH,
selectable, not a forced choice — mirroring how REST already works.**
REST's `middleware.SecurityScheme` + `ServerImplementation.Fn` does not
dictate HOW verification happens — `Fn` can be a trivial presence/format
check OR a full JWT-verifying, scope-extracting closure returning a real
grant map checked via `middleware.CheckScopes`. The library provides ONLY
the shape (`Satisfies`-gated dispatch + `CheckScopes`), never the crypto/
JWT verification itself (go-codex imports no crypto/JWT library anywhere —
confirmed, see `docs/features/security.md`'s opening line). The SAME
applies to the translated `Channel.HandleMW(mw, fn)`: `fn` can be as
simple or as sophisticated as the caller wants. Concretely:
- `mqtt5`'s EXISTING connection-level `SecuredClient` stays UNCHANGED —
  already the "protocol-native, simple" choice, composes independently
  with message-level exactly as documented today; needs no new design.
- The translated message-level `HandleMW`/`ClientMW` gains
  `middleware.CheckScopes` integration (per Decision 3's Fn-shape upgrade
  above) — this is what MAKES the "sophisticated OAuth2 + scopes" path
  possible: a `SecurityScheme` declared with `route.OAuth2Scheme(flows)` +
  scoped `route.Require("oauth2", "topic:read", "topic:write")`, verified
  by a `HandleMW`-attached `fn` that decodes a JWT and returns its scopes
  as the grant map — same mechanism REST uses, retargeted at MQTT5 User
  Properties instead of an HTTP header. A caller who only wants "is there
  a non-empty bearer token" still just returns an empty-but-non-nil grant
  map — same spectrum, same code path, caller's choice of `fn` complexity.
- **Topic-scoped authorization is a NATURAL, ALREADY-SUPPORTED consequence
  of `route.Require`, not new API.** `route.SecurityRequirement` is
  `map[string][]string` (scheme → scopes) — nothing stops a caller from
  naming scopes after topics (e.g. `route.Require("oauth2",
  "sensors/{sensorID}/data:subscribe")`) and writing a `HandleMW` `fn` that
  checks the token's scopes against the CONCRETE topic (post
  `BuildTopic`/topic-var substitution) rather than the template. This is a
  usage pattern to DOCUMENT once implemented (`docs/features/security.md`'s
  events section), not new API surface — the scope string is just a
  string; giving it topic-shaped values is the caller's convention,
  matching how REST callers already name scopes after resource paths
  (e.g. `"users:read"`).

**Design answer — AsyncAPI spec rendering: ALREADY FULLY COMPLETE, no gap,
no new design needed.** Confirmed via `render/asyncapi/v3/asyncapi.go` +
`document.go`: `buildSecuritySchemes`/`buildSecurityScheme` correctly
render every `SecuritySchemeType` (apiKey/http/oauth2/openIdConnect)
INCLUDING full OAuth2 `flows` (all 4 flow kinds, each with
authorizationUrl/tokenUrl/refreshUrl/scopes) via `buildOAuthFlows`/
`buildOAuthFlow`; `components.securitySchemes` is aggregated from every
registered channel's `WithSecurityScheme` declaration; BOTH per-operation
(`o["security"]`) AND per-server (`srv["security"]`) security arrays are
rendered, not just the schemes list. This entire piece of the user's
original question is answered by EXISTING, WORKING code — nothing here
needs to change when `Channel.HandleMW`/`ClientMW` are introduced, since
the spec-declaration side (`WithSecurityScheme`, or its eventual
`middleware.SecurityScheme`-based replacement per Decision 1) feeds the
SAME rendering pipeline unchanged.

### Decision 4 — `ports` needs NO new capability (resolves the 4th open question)

The earlier "plausible fix" sketch (a new `PortOptions.ServerImplementations`/
`ClientImplementations` field threaded manually into `binding.go`'s internal
`Register()` calls) is **not needed**. Once `Channel.Register(builder)`/
`reqreply.Route.Register(builder)` populate `handle.Implementations`/
`ClientImplementations` themselves (Decision 1), `ports.PluginEventPattern`/
`PluginReqReplyPattern` get this FOR FREE via the SAME `.Register(builder)`
delegation they already perform for every other handle field — confirmed via
re-reading how `ports` builds `Pattern`-derived handles: `Pattern.Opts` is a
thin `[]events.ChannelOpt`/`[]reqreply.RouteOpt` pass-through, and `Register`
populates whatever fields the underlying `Channel`/`Route` type populates,
with zero `ports`-specific enumeration of individual fields. No `ports`
code changes are needed for this piece — only `api/events`/`api/reqreply`
(Decision 1) and the adapters (Decision 3).

## Escape hatches that exist today (exhaustive, mirrors `middleware-workflow-simplification.md`'s own list format)

1. **`events.WithSecurityScheme`/`reqreply.WithSecurityScheme` declared
   with NO matching `SecurityFunc`/`CredentialFunc` EVER supplied** — since
   these are raw closures on PER-CALL `Options` structs (not handle-
   attached), there is no "coverage check" of any kind today. A channel
   can declare `Security` and be subscribed to/published on for the
   entire program's life with zero verification, silently. REST had this
   exact gap too, closed by `rest.CheckCoverage`/`MissingSecurityMiddlewareError`
   at `Register`/`RegisterSSE` time — events/reqreply never got the fix
   because they never got the underlying `HandleMW`/`Register`-time
   coverage mechanism to begin with. Decision 1 + Decision 3 together are
   what would let a future pass close this the same way.
2. **`SecurityScheme.Codec` nil = "no format validation"** — an explicit,
   documented escape hatch (same as REST): `SecurityFunc`/`CredentialFunc`
   receives the raw, unvalidated credential string when `Codec` is nil.
3. **`SecurityFunc`/`CredentialFunc` are PER-CALL `Options` fields, not
   declaration-attached.** Unlike REST (where `HandleMW`/`ClientMW` attach
   the implementation to the ROUTE VALUE itself, enforced identically on
   every call), a caller can pass a COMPLETELY DIFFERENT `SecurityFunc` on
   every individual `Subscribe`/`Serve` call for the SAME channel/route —
   nothing prevents drift between calls, and nothing prevents silently
   omitting it on some calls but not others. This is the CORE gap
   Decision 1 already targets closing.
4. **`zeromq` has literally NO security mechanism at any layer** — not an
   escape hatch in the traditional "alternate path" sense, but the most
   severe gap: EVERY zeromq call is unconditionally unenforced, regardless
   of what the Channel/Route declares. Confirmed via exhaustive grep (see
   the capability matrix above).
5. **`mqtt` (v3) publish-side has NO credential mechanism, by protocol
   limitation** — not a gap to close (there is nothing to attach even in
   principle), but must be documented so a future `ClientMW` design does
   not assume feature parity with `mqtt5` is achievable here.
6. **Last-registered-wins on `WithSecurityScheme` name collisions** across
   channels sharing a builder — documented, same policy as REST, silent
   (no error) — a genuine escape hatch if two channels accidentally
   declare the same scheme name with different metadata.
7. **`Builder.AddGlobalSecurity`/nil-inherits/empty-means-none** — same
   3-state contract as REST (inherit/explicit/none) — not itself a gap,
   but worth naming: a caller can silently rely on inheritance without
   realizing a specific channel opted out (empty slice) or overrode it.
8. **Connection-level `SecuredClient` is entirely SEPARATE and
   UNCOORDINATED with message-level `SecurityScheme`/`SecurityFunc`.** A
   caller can configure BOTH independently with no cross-check; nothing
   verifies the connection-level scheme's claims are consistent with the
   message-level one. By design — "both layers are independent and
   composable" per `docs/features/security.md` — but worth naming as a
   consequence, not silently assumed.

## Remaining open items (deferred to a later pass, not decided here)

- `zeromq`'s Fn shapes for BOTH directions remain unresolved (Decision 3's
  flagged gap) — it may need a new wire-level credential convention (e.g.
  an additional frame) before any Fn shape can be finalized; this is
  genuinely new protocol design, not a mirror of an existing mechanism.
  (`mqtt` v3's publish-side is now CONFIRMED as a permanent protocol
  limitation, not an open question — see Decision 3's follow-up pass
  above; only its subscribe-side Fn shape, which mirrors `mqtt5` directly,
  remains to be finalized during implementation.)
- Whether pub/sub "scopes" is a meaningful concept to enforce via
  `middleware.CheckScopes` for EVERY credential kind, or whether a simpler
  pass/fail is more honest for some MQTT/ZMQ credentials (a JWT's scopes
  translate naturally via `route.OAuth2Scheme`/`route.Require` — already
  fully supported at the spec level, see Decision 3's follow-up pass; a
  bare API key or pre-shared secret might not carry any scope concept at
  all — the design answer above resolves this as "caller's choice, not a
  forced mechanism," but the DEFAULT/recommended pattern for each scheme
  type is still worth documenting once implemented).
- Removing the OLD `SecurityFunc`/`CredentialFunc` `Options` fields is a
  BREAKING CHANGE for existing callers (`examples/adapters-mqtt-security`,
  `examples/adapters-mqtt5`, and others) — needs an explicit migration
  checklist before implementation, mirroring `middleware-workflow-
  simplification.md`'s own "Removing an old API"/"Lessons Learned"
  sections (enumerate every responsibility the old fields carried, migrate
  every REAL example — not just tests — before deleting anything).
- `api/mcp`/`adapters/mcpgo` remain believed unaffected (no security
  model, by design) but were not re-audited in depth this pass — worth a
  quick confirmation check when implementation starts.

Implementation has not started. A future session should pick the lowest-
risk starting point first — `api/reqreply` (Decision 3: fully resolved,
closest REST analogue) — before `mqtt5`'s pub/sub case, then circle back
to the `mqtt`(v3)/`zeromq` follow-up investigation.
