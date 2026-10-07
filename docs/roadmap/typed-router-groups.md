# Strongly-Typed `Router[Req,Resp]` — `api/rest`, `api/events`, `api/reqreply`

> **Status:** Idea / investigation only — spun out of
> [D-0008 — Declarative Router Groups](../design/d-0008-declarative-router-groups.md)'s
> Phase C deferred-item review (item 3). No design committed yet.
> [← Back to Roadmap](index.md)

## Motivation

[`d-0008-declarative-router-groups.md`](../design/d-0008-declarative-router-groups.md)
(formerly this roadmap's `declarative-router-groups.md`) shipped a fully
IMMUTABLE, TYPE-ERASED `Router` per pattern (`api/rest`, `api/events`,
`api/reqreply`) — one `Router` value groups leaves of potentially
DIFFERENT `Req`/`Resp`/`T` type pairs (e.g. a REST `Router` grouping a
`GET /users` returning `[]User` alongside a `POST /users` taking
`CreateUserReq`). This heterogeneity is deliberate and load-bearing: it's
what lets `.Route(a).Route(b)` accept ANY leaf satisfying the package's
`routable` interface, regardless of its type parameters.

That SAME heterogeneity is exactly why `d-0008`'s Router can never accept
a `BoundMiddleware[Req,Resp,...]` (`docs/design/d-0007-declarative-middleware-layering.md`'s
bound-class mechanism) — a bound middleware is tied to ONE concrete type
pair by construction (that's the entire point of "bound": compile-time
guaranteed field access into a specific `Req`/`Resp`). There is no single
`Req` type a type-erased `Router.Use(boundMW)` call could type-check
against.

This doc captures the alternative `d-0008`'s own review explicitly
rejected as "not a bullet in that doc" — a strongly-typed `Router[Req,Resp]`
that only ever groups SAME-typed leaves, as a PARALLEL construct to
`d-0008`'s existing heterogeneous `Router`, not a replacement for it.

## Why this wasn't folded into `d-0008` directly

Two alternatives were considered during `d-0008`'s own deferred-item
review and both were rejected for staying IN that doc:

1. **A reflection-based runtime type-check** — `Router` accepts a bound
   middleware and, at `Walk`/`Register` time, uses reflection to test
   whether each grouped leaf's `Req`/`Resp` matches the bound
   middleware's type parameters, applying it only where it matches and
   silently skipping leaves where it doesn't. Rejected: this would be the
   ONE place in the entire declarative layer where a type mismatch
   becomes a SILENT skip instead of a loud, typed error — contradicting
   the `HandleCallbackTypeError`/`BoundMiddlewareReqMismatchError`
   precedent everywhere else in `d-0008`.
2. **A strongly-typed `Router[Req,Resp]`** (this doc's subject) — would
   require EVERY leaf grouped under one Router instance to share the SAME
   `Req`/`Resp` pair, enforced at compile time via a type parameter on
   `Router` itself. This is a fundamentally different, PARALLEL
   construct from `d-0008`'s existing type-erased `Router` — not an
   incremental addition to it — so it belongs in its own roadmap doc, to
   be designed independently (including whether it's even worth the
   added API-surface complexity of maintaining TWO Router shapes per
   pattern).

## Open questions (unexplored — this is an idea, not a design)

- **Does a concrete need exist at all?** No driver has surfaced yet
  (same honest caveat `d-0008`'s own deferred items carry). A same-typed
  Router is most plausible for reqreply/events (multiple topics sharing
  one request/payload shape) — less obviously useful for REST, where
  different HTTP methods on one resource naturally have DIFFERENT
  Req/Resp shapes (the exact case that motivated `d-0008`'s
  heterogeneous design in the first place).
- **Naming/coexistence** — would this be `rest.TypedRouter[Req,Resp]`
  (distinct name, avoids confusion) or could `Router` itself gain a type
  parameter with today's heterogeneous usage becoming `Router[any,any]`
  or similar? The latter risks breaking every existing `d-0008` call site
  — almost certainly the former (a distinct, new type) is the only
  non-breaking path.
- **Does `Mount`/`Group` even make sense for a same-typed Router?**
  `d-0008`'s `Mount` composes prefixes across potentially different leaf
  types; a strongly-typed Router nesting another strongly-typed Router
  of a DIFFERENT `Req`/`Resp` pair raises the same heterogeneity question
  `d-0008` solved by NOT type-parameterizing `Router` at all — an
  unresolved tension worth exploring before committing to this shape.
- **Is a Router the right vehicle at all**, or would a simpler,
  non-Router mechanism (e.g. a free function `ApplyBoundMiddleware(bm,
  leaves ...Route[Req,Resp]) []Route[Req,Resp]` that just maps over a
  same-typed slice) solve the SAME underlying need with less new API
  surface than a whole parallel `Router` type? Worth comparing before
  designing `Router[Req,Resp]` in earnest.

## Out of scope (for now)

Everything — this is a pure idea capture, Phase 1 would be scoping which
of the open questions above actually matter, not designing an API
surface yet.

## See also

- [`d-0008-declarative-router-groups.md`](../design/d-0008-declarative-router-groups.md) —
  the sibling, heterogeneous `Router` mechanism this idea would
  complement, not replace. Its Phase C deferred-item review (item 3) is
  where this idea was spun out from — see that section for the full
  rejected-alternatives writeup.
- [`d-0007-declarative-middleware-layering.md`](../design/d-0007-declarative-middleware-layering.md) —
  the `BoundMiddleware[Req,Resp,...]` mechanism motivating this idea.
