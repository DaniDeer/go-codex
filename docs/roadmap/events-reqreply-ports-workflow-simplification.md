# Events / ReqReply / Ports Workflow Simplification — findings

> **Status:** Findings only — no proposal, no driver yet. Spun out from
> [Middleware Workflow Simplification](../design/middleware-workflow-simplification.md)
> while reviewing that doc's implications beyond REST. **Deliberately
> DEFERRED**: the plan is to implement the REST redesign
> (`middleware-workflow-simplification.md`'s Decisions 1–8) FIRST, then
> return to this doc for events/reqreply/ports once that ships and its
> real-world lessons are known. [← Back to Roadmap](index.md)
>
> This doc captures ONLY what was confirmed via code inspection — no
> design decisions have been made yet, unlike `middleware-workflow-
> simplification.md`'s fully-resolved state. Treat every section below
> as "here is the starting point," not "here is the plan."

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

## How the new REST pattern might translate (unresolved — starting points only)

None of the below are decided. These are the shapes that seemed like
plausible translations while reviewing, recorded so the eventual design
round doesn't have to re-derive them from scratch.

- **Pub/sub's asymmetry maps onto REST's server/client roles
  imperfectly, but not badly.** There is no inherent "one side hosts, one
  side calls" relationship in pub/sub — but the EXISTING (soon-to-be-
  replaced) naming in `mqtt5` already implies a natural mapping:
  `SecurityFunc` (verify an incoming message) belongs to the SUBSCRIBE
  side; `CredentialFunc` (supply an outgoing credential) belongs to the
  PUBLISH side. A translated design might look like
  `Channel.HandleMW(mw, verifyFn)` for subscribe-side verification and
  `Channel.ClientMW(mw, credFn)` for publish-side credentials — plausible,
  not yet validated against real usage the way REST's translation was.
- **`api/reqreply` is the closest analogue to REST** — genuine
  request/reply, not pub/sub — likely the most direct, least-adapted
  translation of the three boundaries covered here.
- **Whether a whole-API `Serve` makes sense for events is unclear.**
  E.g. a hypothetical `mqtt5.Serve(client, builder) error` walking every
  `Channel` an `events.Builder` has accumulated, subscribing each one
  with `.WithHandler()` attached (mirroring REST's Decision "`Serve`'s
  whole-builder failure semantics" — no handler → skip silently) seems
  like a plausible translation. Complication not present in REST: THREE
  independent transport adapters (`mqtt`, `mqtt5`, `zeromq`) can all
  subscribe/publish against the SAME abstractly-declared `Channel` —
  unlike REST's `nethttp`/`chi`, which are both HTTP. Whether one `Serve`
  per adapter (mirroring `chi.Serve`/`nethttp.Serve` already being
  separate, adapter-specific functions) is the right shape, or something
  else, is unresolved.
- **`ports` likely needs a genuinely NEW capability, not a migration.**
  Since `.HandleMW()`/`.WithHandler()` (in whatever the REST
  implementation actually ships as) are chain methods on the
  `Route`/`Channel` VALUE itself, and `ports` already builds its handle
  via `Route.Register(builder)`/`Channel.Register(builder)` from a
  `Pattern`'s `Opts`, the DECLARE-time half (`.Use(mw)`) already flows
  through unmodified (see "Confirmed current state" above). The
  IMPLEMENT-time half does not exist for `ports` at all today — a
  plausible fix is a new `PortOptions` field (e.g.
  `ServerImplementations []middleware.ServerImplementation`/
  `ClientImplementations []middleware.ClientImplementation` — REST's own
  shipped types, see [Middleware Workflow
  Simplification](middleware-workflow-simplification.md)) that
  `binding.go`'s adapters thread through internally before calling
  `.Register()` —
  mirroring how `RESTBuilder`/`EventBuilder` already let a caller supply
  a shared `Builder`. Not validated against `ports`' actual construction
  flow in detail yet.

## Explicitly out of scope for this doc, for now

- No API surface is proposed or confirmed anywhere above — everything
  is "a plausible starting point for the next round," not a decision.
- `api/mcp`/`adapters/mcpgo` are believed unaffected (no security model,
  by design) but were not re-audited in depth for this doc — worth a
  quick confirmation pass when this doc is picked back up.
- Sequencing: this doc is intentionally NOT worked further until
  `middleware-workflow-simplification.md`'s REST implementation ships —
  real implementation experience there (in particular, whatever the
  ACTUAL shipped shape of `HandleMW`/`ClientMW`/`Serve`/`Call` turns out
  to be, and any adjustments discovered during REST's own implementation
  phase) should inform this doc's eventual design round, not the other
  way around.
