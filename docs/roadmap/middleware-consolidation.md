# Middleware Consolidation — evaluating `middleware.Middleware` (legacy) vs. codec-backed `Middleware[In,Out]` (D-0003)

> **Status:** Design draft — Explore mode, evaluating whether
> consolidation is even correct, not presupposing it. Not yet
> implemented.
> [← Back to Roadmap](index.md)

Spun out while reviewing [Composable Capability Requirements — Phase
3](capability-requirement-composition.md#phase-3--apirest-a-new-synchronous-transport-stateless-adapter)'s
own relationship to declarative middleware. This doc is scoped
narrowly: it evaluates dropping the LEGACY, non-generic
`middleware.Middleware` type in favor of the codec-backed
`middleware.Declaration[In,Out]`/`rest.Middleware[In,Out]`/
`events.Middleware[In,Out]`/`reqreply.Middleware[In,Out]` family (D-0003),
now that breaking changes are pre-approved for this codebase's current
work. It does NOT re-litigate D-0003's own design (already shipped) or
Phase 3's own capability mechanism (designed separately, unaffected by
this doc's outcome either way).

## Motivation

Two middleware mechanisms exist side by side today:

- **Legacy `middleware.Middleware`** (`middleware/middleware.go`) — a
  single, non-generic struct: `Name`, `Security *SecurityDeclaration`,
  `RequestHeaderParams`/`RequestCookieParams`/`RequestQueryParams`
  (`[]HeaderParamSpec`/etc. — plain, non-merge spec-only declarations),
  `ResponseHeaderParams`/`ResponseCookieParams`. Attached via
  `Route.HandleMW`/`Route.ClientMW` (REST), `Subscriber.SubscribeMW`/
  `Publisher.PublishMW` (events), `Route.HandleMW`/`Route.ClientMW`
  (reqreply) — ALL THREE APIs' pairing methods are hard-coded to this
  CONCRETE type.
- **Codec-backed `Middleware[In,Out]`** (D-0003,
  `docs/design/d-0003-codec-declared-middlewares.md`) — a generic type
  per API (`rest.Middleware[In,Out]`/`events.Middleware[In,Out]`/
  `reqreply.Middleware[In,Out]`), each embedding the shared
  `middleware.Declaration[In,Out]` core, adding per-pattern merge-field
  vocabulary (REST: `WithRequestHeader`/`WithRequestCookie`/
  `WithRequestQuery`/response siblings; events: topic-var; reqreply:
  topic-var + property-var). Attached via `Transform`/`ClientTransform`
  (route/channel-BOUND) or plain `.Use(mw)` (route/channel-AGNOSTIC, via
  `WithReceive`/`WithSend`).

Having two middleware "shapes" a declaring user must learn — when to
reach for `middleware.SecurityScheme(...)` + `.HandleMW(...)` vs.
`rest.NewMiddleware(...).WithRequestHeader(...)` + `Transform(...)` — is
exactly the kind of API-surface duplication this session's broader
capability-composition work has been eliminating elsewhere. Worth a
dedicated evaluation now that breaking changes are acceptable, rather
than carrying two mechanisms forward indefinitely by inertia.

## Key findings (this round, traced through actual code — not assumed)

**The codec-backed type is NOT a superset of the legacy one — it is
missing 2 real capabilities, confirmed via code, not merely by reading
doc comments:**

1. **Security has NO codec-backed equivalent at all.**
   `middleware.SecurityScheme(schemeName, scheme, scopes, codec)` and
   `rest.FromSecurityScheme(...)` both return LEGACY `middleware.
   Middleware` values (`.Security *SecurityDeclaration` populated).
   `Middleware[In,Out]` has no `Security` field, no equivalent
   constructor. **`Route.HandleMW`/`ClientMW` (REST/reqreply) and
   `Subscriber.SubscribeMW`/`Publisher.PublishMW` (events) — the paired-
   security-Fn attachment mechanism — are hard-coded to accept
   `*middleware.Middleware` (the concrete legacy type), NOT the shared
   `RouteMiddleware` interface both types implement.** This is the
   REAL blocker: even though `Middleware[In,Out]` implements
   `RouteMiddlewareMarker()` (satisfying `RouteMiddleware`), it cannot
   be passed to `HandleMW`/`ClientMW` today — the signature simply
   doesn't accept it. Security enforcement pairing is therefore
   entirely UNAVAILABLE to the codec-backed family as it stands.
2. **Presence-only (non-merged) header/cookie/query declarations have
   no codec-backed equivalent either — and this is an ACTIVELY USED
   pattern, not a hypothetical.** `mqtt5.FromUserPropertyParam`/
   `FromResponseUserPropertyParam` (`adapters/mqtt5/reqreply_transport.go`,
   Phase 1b of `docs/design/d-0004-reqreply-workflow-simplification.md`)
   build a legacy `middleware.Middleware{RequestHeaderParams:
   []HeaderParamSpec{...}}` with **no merge field at all** — pure
   spec+validation declaration (an MQTT5 User Property that needs
   validating and rendering into the AsyncAPI spec, but has NO
   corresponding Go struct field to decode into). `Middleware[In,Out].
   WithRequestHeader` REQUIRES a `MergedHeaderParam[In]` (a get/set pair
   into an `In` struct field) — there is no presence-only variant on the
   codec-backed type today. Folding this usage onto the codec-backed
   family would force an artificial merge-field even where none is
   wanted.

## Migration-surface survey (counts, not estimates)

| Mechanism | Usage count (non-test `.go` files) |
|---|---|
| Legacy construction (`middleware.SecurityScheme`/`FromSecurityScheme`/`FromHeaderParam`/`FromCookieParam`/`FromQueryParam`/`FromResponseHeaderParam`/`FromResponseCookieParam`/`FromUserPropertyParam`/`FromResponseUserPropertyParam`) | 16 files |
| `HandleMW`/`ClientMW`/`SubscribeMW`/`PublishMW` call sites (hard-coded to legacy type) | 52 files |
| Codec-backed `NewMiddleware[...]` construction | 3 files |

The paired-security-Fn mechanism (52 files) is the DOMINANT,
foundational usage — Security is not a peripheral feature to casually
fold into a newer, far-less-adopted mechanism (3 files) without a real
plan for parity.

## Scope decisions

| In scope (this doc evaluates) | Out of scope |
|---|---|
| Whether/how to fold Security declaration into the codec-backed family, or keep it permanently separate | D-0003's own already-shipped design (not reopened) |
| Whether/how to add a presence-only (non-merged) param variant to `Middleware[In,Out]` | Phase 3's own capability mechanism (`capability-requirement-composition.md`) — unaffected by this doc's outcome either way, confirmed by tracing that both middleware mechanisms already coexist correctly with it |
| A migration/removal plan for legacy `middleware.Middleware` IF consolidation is found superior | Inventing a THIRD middleware mechanism — every option below reuses existing pieces |

## Open design decisions (genuinely open — no leaning presupposed)

1. **Fold Security into `Middleware[In,Out]`.** Add an optional
   `Security *middleware.SecurityDeclaration` field (mirrors legacy's
   own field) plus a presence-only `WithRequestHeaderSpec`-style
   variant (no merge field required) to `Middleware[In,Out]`; widen
   `HandleMW`/`ClientMW`/`SubscribeMW`/`PublishMW` to accept the
   `RouteMiddleware` interface generically, type-asserting for
   `.Security`/presence-only fields when present. **Consolidates fully
   — one type family — but requires touching all 3 APIs' pairing
   method signatures (52-file usage surface) and adds fields to
   `Middleware[In,Out]` that are meaningless for the MERGE-shaped,
   route/channel-BOUND `Transform`/`ClientTransform` attachment style
   (Security doesn't make sense mixed with a `Transform`-attached
   value that also reads `*Req`) — a real design tension, not a clean
   fold.**
2. **Keep Security as its own standalone, non-generic type — rename
   away from "Middleware" entirely** (e.g. `middleware.
   SecurityDeclaration` becomes the top-level attachable type, not a
   field ON a `Middleware`), decoupled from the codec-backed family's
   naming/shape entirely. Migrate ONLY the presence-only header/
   cookie/query use case (`FromUserPropertyParam`'s pattern) onto a
   NEW presence-only variant added to `Middleware[In,Out]`. Legacy
   `middleware.Middleware` SHRINKS to become purely
   `SecurityDeclaration`-shaped (effectively renamed/refocused), not
   fully deleted — `HandleMW`/`ClientMW`/etc. keep their existing
   signatures, now accepting the renamed, narrower type. **Smaller
   blast radius (16 legacy-construction files touched, not 52), avoids
   the Transform/Security mixing tension in option 1, but leaves TWO
   middleware type families conceptually distinct (Security vs.
   codec-backed enrichment) — an explicit acceptance that they serve
   different purposes, not full consolidation.**
3. **Do nothing structural — conclude the two-mechanism split is
   CORRECT, not a duplication to fix.** Security enforcement (credential
   extraction + authorization) and codec-backed param merging
   (enrichment/observability/typed transformation) are genuinely
   different concerns with different attachment-timing needs (Security
   pairs a scheme name with an enforcing Fn; codec-backed middleware
   merges a decoded value into a route's own Req/Resp or runs
   route-agnostic). This doc's own migration-surface survey (52 vs. 3
   files) suggests the codec-backed mechanism has NOT organically grown
   to threaten Security's role — maybe correctly so. **Leaning stated
   explicitly for the reader's benefit: this option currently looks
   most consistent with the evidence gathered, but is NOT pre-decided
   — Refine this doc once the 3 options have been discussed.**

## If consolidation IS chosen (options 1 or 2) — "Removing an old API" checklist application

Per the `plan-a-new-codex-feature` skill's checklist, BEFORE any legacy
type/method is deleted:

1. **Enumerate every responsibility of `middleware.Middleware`, not
   just Security** — confirmed above: Security declaration, presence-
   only param spec (3 sub-kinds × request/response = up to 5 fields),
   `Name` (used in errors/observability). Any migration must re-derive
   ALL of these, not just the obvious Security piece.
2. **Verify "equivalent" claims by migration, not review** — port a
   REPRESENTATIVE SAMPLE of real legacy usages (at minimum:
   `examples/rest-api/routes/middleware.go`'s `BearerAuthMw`, `adapters/
   mqtt5/reqreply_transport.go`'s `FromUserPropertyParam`) onto whichever
   new shape is chosen, and re-run their existing tests, BEFORE
   committing to removing the old type.
3. **Check every real consumer** — 52 `HandleMW`/`ClientMW`/
   `SubscribeMW`/`PublishMW` call sites across REST/events/reqreply
   examples and adapters; migrate ALL of them, not a subset.
4. **Sweep for documentation references** — `.github/instructions/
   go-codex.instructions.md`'s "Protocol-Native..." and middleware
   sections, `docs/features/codec-declared-middleware.md`,
   `docs/design/d-0003-codec-declared-middlewares.md`, `docs/design/
   d-0001-rest-middleware-workflow-simplification.md`, and every
   `.github/skills/*/references/*.md` mentioning `middleware.Middleware`
   by name.

## Unit test plan (if consolidation proceeds)

| Test | Verifies |
|---|---|
| `TestMiddlewareInOut_Security_PairedFnDispatch` | A `Middleware[In,Out]`-attached Security declaration (option 1) or renamed `SecurityDeclaration` (option 2) still enforces via `HandleMW`/`ClientMW` identically to today |
| `TestMiddlewareInOut_PresenceOnlyHeaderParam_ValidatedNoMergeField` | A presence-only header declaration (mirrors `FromUserPropertyParam`) validates without requiring an `In` struct field |
| `TestHandleMW_AcceptsRouteMiddlewareInterface` (option 1 only) | `HandleMW`/`ClientMW`/etc. accept ANY `RouteMiddleware`-satisfying value, not just the concrete legacy type |
| `TestLegacyMiddleware_StillCompiles_DuringMigrationWindow` | Existing legacy call sites keep compiling until their own migration lands (staged rollout, not a big-bang break) |

## Files to create/modify (sketch — exact list depends on chosen option)

| File | Change |
|---|---|
| `middleware/middleware.go` | Depends on option: add `RouteMiddleware`-interface-based dispatch support (1), rename/narrow to `SecurityDeclaration`-only (2), or unchanged (3) |
| `api/rest/middleware_declaration.go`, `api/events/middleware_declaration.go`, `api/reqreply/middleware_declaration.go` | `Middleware[In,Out]` gains `Security`/presence-only fields (option 1 only) |
| `api/rest/middleware.go`, `api/events/builder.go`, `api/reqreply/middleware.go` | `HandleMW`/`ClientMW`/`SubscribeMW`/`PublishMW` signature changes (option 1 only) |
| `adapters/mqtt5/reqreply_transport.go` | `FromUserPropertyParam`/`FromResponseUserPropertyParam` migrated onto the new presence-only shape (options 1/2) |
| Every example under `examples/*-api/routes/middleware.go`-equivalent | Migrated per the representative-sample-then-full-sweep discipline above |

## See also

- [`docs/design/d-0003-codec-declared-middlewares.md`](../design/d-0003-codec-declared-middlewares.md)
  — the codec-backed mechanism's own shipped design, not reopened here
- [`docs/design/d-0001-rest-middleware-workflow-simplification.md`](../design/d-0001-rest-middleware-workflow-simplification.md)
  — the legacy mechanism's own shipped design, including its "Lessons
  Learned" on removing an old API the hard way
- [Composable Capability Requirements — Phase
  3](capability-requirement-composition.md#phase-3--apirest-a-new-synchronous-transport-stateless-adapter)
  — where this evaluation was spun out from; confirmed unaffected by
  this doc's outcome either way
- [`plan-a-new-codex-feature` skill](../../.github/skills/plan-a-new-codex-feature/SKILL.md)
  — the "Removing an old API" checklist applied above
