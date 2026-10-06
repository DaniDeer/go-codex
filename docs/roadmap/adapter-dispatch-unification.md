# Adapter Dispatch-Core Unification — `adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq`

> **Status:** Idea / investigation only — Phase 1 scoped as an AUDIT, not a
> redesign. No code changes proposed yet.
> [← Back to Roadmap](index.md)

## Motivation

Spun out of a retrospective discussion (not a feature request) about
whether this project's bottom-up, codec-first design approach (declare
route/channel shape → attach middleware → now Router) produced clean
fundamentals, or accumulated "layers of cope" along the way. The honest,
evidence-grounded answer: the `api/*` declaration layer (codecs, builders,
opts) has stayed comparatively clean — proven by how fast
[`declarative-router-groups.md`](declarative-router-groups.md)'s own design
closed, reusing already-validated primitives at every step. The recurring,
EXPENSIVE failure mode in this project's history has instead been in the
ADAPTER-layer dispatch plumbing — confirmed by re-reading this project's
own design docs directly, not by re-deriving the claim from scratch:

- **`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s
  "Lessons Learned"** (7 numbered findings): a reflection-based dispatch
  mechanism claimed "equivalent" to the old one silently was NOT (missing
  Formats negotiation, missing `ErrorResponseFor`) — undetected until 128+
  old tests were migrated wholesale onto the new entry point.
- **`docs/design/d-0007-declarative-middleware-layering.md`'s "Post-ship
  regression found and fixed"**: events' Subscribe dispatch had **THREE
  separately-maintained reflection-based implementations** —
  `adapter.go`'s generic `makeSubscribeMessageHandler[T]` (exercised ONLY
  by the low-level escape hatch and unit tests), `transport.go`'s
  reflection-based `Subscribe` (the `ports.Pattern` binding path), and
  `caller.go`'s (mqtt5/mqtt) / `serve_subscribers.go`'s (zeromq)
  reflection-based `ServeSubscribers` — **the ACTUAL, documented,
  recommended `Client.Attach` workflow every example/tutorial tells a user
  to use.** That third path had NO `MiddlewareHandlers` dispatch of any
  kind, silently, for the entire lifetime of Rollout Phase B — found only
  when a NEW example was written specifically to exercise that exact entry
  point. The doc's own words: "this is the THIRD time this exact class of
  gap has surfaced in this doc's own history."

This recurring pattern — a capability correctly designed at the `api/*`
layer, but silently missing from ONE of several independently-maintained
adapter dispatch paths, undetected until a real consumer exercises that
EXACT entry point — is the concrete, named problem this roadmap doc
investigates.

## Confirmed asymmetry: REST already converged; events (confirmed) and reqreply (uncatalogued) did not

| Pattern | Dispatch paths confirmed | Converged? |
|---|---|---|
| `api/rest` (`adapters/nethttp`) | ONE — `serve.go`'s `ServeOne`/`Serve` reflects (`reflect.Value.Call`/`MethodByName`) directly into `RouteHandle[Req,Resp]`'s OWN generic, type-safe methods (`ApplyMergeFields`, `DecodeMerged`, `EncodeResponseMergeFields`, `ErrorStatusFor`, etc.) — confirmed via its own doc comment: "runs the SAME pipeline... invoked via `reflect.Value.Call`." | **YES** — the reference pattern to converge toward |
| `api/events` Subscribe (`adapters/mqtt`/`mqtt5`/`zeromq`) | THREE, confirmed directly from `d-0007`'s own regression writeup (cited above) | **NO** — confirmed broken, since fixed for the specific GrantedScopes/ContextField gap that surfaced it, but the 3-implementation STRUCTURE itself was not consolidated into one |
| `api/events` Publish | Not yet audited this round — `adapters/mqtt5/transport_dispatch.go`'s own doc comment confirms SOME ad hoc convergence already happened ("the SUBSCRIBE-side equivalents ALREADY EXIST in caller.go... this file reuses them directly rather than duplicating"), but the exact count/location for Publish specifically is unconfirmed | **UNKNOWN** |
| `api/reqreply` Call/Serve (`adapters/mqtt5`/`zeromq`) | Structurally SEPARATE files from events' own dispatch (`reqreply_transport.go` vs. `transport.go`) per adapter, confirmed via file listing — whether reqreply's OWN Call/Serve paths have the same "3 independent implementations" shape as events' Subscribe, or whether they already converged, is unconfirmed | **UNKNOWN** |

## Scope decisions (Phase 1 — THIS doc)

| In scope (Phase 1) | Out of scope |
|---|---|
| **AUDIT ONLY**: catalog the exact count and location of independent dispatch implementations per capability (Security, bound `Middleware`, `GrantedScopes`, `ContextField`, Capabilities/Disposition, ErrorPattern/DeadLetter), per adapter (`mqtt`, `mqtt5`, `zeromq`), per pattern (events Publish — unaudited; reqreply Call/Serve — unaudited) | Any actual consolidation/redesign of the dispatch mechanism itself — deferred to a follow-up round once the audit's true scope is known |
| Producing a literal audit table (capability × adapter × pattern → implementation count + file:line) so a future round can SEE the full blast radius before committing to a design | Redesigning reflection vs. codegen vs. any other dispatch TECHNIQUE — this doc does not take a position on reflection itself being the problem (REST's own converged, single-path design ALSO uses reflection — confirmed above — so reflection per se is not the issue) |
| Confirming/denying whether REST's OWN convergence pattern (one reflection path, calling into the handle's own generic methods) is a viable template events/reqreply could converge toward | Touching `api/*` — confirmed clean in this round's own retrospective; this is adapter-layer only |
| Cross-referencing [`declarative-router-groups.md`](declarative-router-groups.md) to confirm Router is UNAFFECTED (see below) | `adapters/websocket`/`adapters/redis`/`adapters/file`/`adapters/sql` — out of scope, no evidence yet that they share this shape (would need their own audit if a driver appears) |

## Why `declarative-router-groups.md`'s Router is NOT at risk of this regression class

Confirmed structurally, not just asserted: Router (all 3 patterns, fully
designed) is PURELY an `api/*`-layer, spec-ASSEMBLY-time construct — its own
"Observer integration" section states plainly that "once `Register()`
succeeds, the resulting handle is indistinguishable from one built without a
Router at all" and "every existing Observer call site downstream (adapter
dispatch) is completely unaware a Router was ever involved." Router never
touches dispatch, so it cannot introduce a 4th independently-maintained
dispatch path, or silently miss wiring a capability into one of the existing
ones — it composes `opts`/prefixes BEFORE any of the 3 (or however many)
existing dispatch paths ever run, using each leaf's own, completely
unchanged `Register`/`Handle` method.

## Audit plan — what Phase 2 (a follow-up round) would need to answer

1. For EACH of events' Publish side (mqtt/mqtt5/zeromq) and reqreply's
   Call/Serve side (mqtt5/zeromq): does the SAME "3 independent dispatch
   implementations" shape exist, or did it already converge (like
   `transport_dispatch.go`'s own comment suggests partially happened for
   events' Subscribe reflection helpers)? Produce a literal table: capability
   → file:line of EVERY place that capability's dispatch is implemented,
   per adapter, per pattern.
2. For any confirmed-duplicated capability, is REST's OWN converged
   pattern (reflect into the Route/Channel/RouteHandle's own generic
   methods, ONE reflection entry point per direction) mechanically
   applicable, or does events'/reqreply's protocol shape (pub/sub fan-out;
   async request/reply correlation) genuinely require a structurally
   different dispatch shape per entry point?
3. What is the actual TEST-COVERAGE gap per entry point today — i.e. for
   each of the 3 (or more) dispatch paths per capability, is there a test
   exercising that EXACT entry point (not just "a" test of the capability
   via the easiest-to-test path)? This is the concrete, actionable
   prevention measure `d-0007`'s own regression writeup names as what
   would have caught it earlier ("a new mechanism is 'shipped' only once a
   test or example exercises it through the SAME entry point real users
   are told to use").
4. Is a shared, EXPLICIT dispatch-core abstraction (e.g. one function per
   capability that EVERY entry point calls through, rather than 3
   independently-reasoned reflection blocks) mechanically feasible given
   each entry point's different starting point (fully-generic `T` known at
   compile time in `adapter.go`; type-erased `any` recovered via reflection
   in `transport.go`/`caller.go`)?

## Out of scope (deferred, no driver yet)

- Any actual code change — this doc is audit-scoping only.
- Performance/benchmarking of reflection-based dispatch — not the question
  this doc investigates (correctness/consistency is).
- Other adapters (`websocket`, `redis`, `file`, `sql`) — no evidence yet
  they share this specific "N independent per-entry-point reflection
  implementations" shape; would need their own audit if a driver surfaces.

## Open questions (not yet answered — Phase 2's job)

1. Does the Publish side of events and the Call/Serve side of reqreply
   share Subscribe's exact "3 independent implementations" shape, or is
   Subscribe's case worse/better than its siblings?
2. Is a shared dispatch-core abstraction mechanically achievable across
   compile-time-generic and runtime-reflected entry points, or does the
   type-erasure boundary (confirmed real: `transport.go`/`caller.go`
   recover `T` only via `reflect.ValueOf` against a type-erased
   `*ChannelHandle`) force genuine duplication no matter how it's
   organized?
3. Should the prevention measure be PROCESS (a checklist requirement: "a
   new capability's roadmap doc's Unit Test Plan MUST include one test per
   confirmed dispatch entry point, not just one test of the capability"),
   ARCHITECTURE (consolidate to fewer paths), or BOTH?
