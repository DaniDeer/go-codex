# Forge/Pipeline Middleware Integration — `forge`, `stats`

> **Status:** Idea only — no driver yet. Spun out from
> [Declarative Middleware](declarative-middleware.md)'s "L14" finding
> (third critical review pass). Independent of that doc's own
> implementation status — no sequencing dependency either way.
> [← Back to Roadmap](index.md)

## Motivation

[Declarative Middleware](declarative-middleware.md) reviewed and
resolved coverage for every Layer 2 (request/response or
per-call-invoked) boundary go-codex ships — REST, events, reqreply,
MCP, and ports — but `forge.Registry`/pipeline functions (Layer 3 —
`forge.NewFunction`, `Compose`, `Registry.Apply`) were never checked
against that design at all. That doc's "coverage across every API/port
boundary" claim was, until this spin-out, inaccurate by omission.

Forge already has its OWN parallel cross-cutting mechanism:
`stats.PipelineObserver.RecordApply`, wired via `Registry.WithObserver`
— an EXPLICIT builder API, deliberately WITHOUT context integration,
by design (pipelines are long-lived registries configured ONCE at
startup, not per-call constructs the way routes/channels/tools/ports
are). Forge also has NO security/authorization concept at all today —
a pipeline function doesn't correspond to an inbound request needing
authentication, unlike every boundary `middleware.Middleware` was
designed around.

Whether this asymmetry is a genuine gap or simply a correct reflection
of forge's different shape (long-lived registry vs. per-call boundary)
is the open question this doc exists to resolve, in a FUTURE dedicated
design pass — not decided here.

## Open questions (carried forward from Declarative Middleware's "L14", unresolved)

1. **Document `Registry.WithObserver` as already adequate.** Forge's
   ONE cross-cutting concern (observability) is already fully served
   by its existing explicit-builder mechanism; formally close the gap
   by stating this plainly in both docs, with NO new mechanism.
2. **Actually extend `middleware.Middleware` to forge.** A
   decorator-shaped `Fn` wrapping `Registry.Apply` per function —
   mirrors `ports.File`'s decorator shape (see Declarative
   Middleware's "Ports get the same treatment" section) — a real new
   mechanism. No concrete driver has been identified yet (no known
   need for per-function security/rate-limiting/tracing beyond what
   `Registry.WithObserver` already provides).
3. **Narrow the claim instead of extending the mechanism.** Restate
   Declarative Middleware's "every boundary" claim to explicitly mean
   Layer 2 only (REST/events/reqreply/MCP/ports), excluding Layer 3 by
   definition — the simplest resolution, but leaves open whether a
   future concrete need (e.g. per-function authorization in a
   multi-tenant pipeline registry) would ever justify option 2.

None of these is decided — this doc exists so the question has a
proper home, separate from Declarative Middleware's now-closed L1-L14
punch list.

## Scope decision

Not yet made. A future pass should: (a) audit whether ANY real
forge-based deployment has ever needed per-function
security/authorization; (b) if not, lean toward option 1 (document as
adequate) per this codebase's general bias against speculative
mechanism (see `dynamic-port-rebinding.md`'s "L11" resolution for the
same "no concrete driver, don't build it yet" precedent); (c) if a
concrete driver appears, revisit option 2 with `ports.File`'s decorator
shape as the template.

## See also

- [Declarative Middleware](declarative-middleware.md) — "L14" in
  "Known limitations and open risks" is the finding this doc spins out
  from; that doc's own status is now "L1-L14 ALL RESOLVED" (L14
  resolved BY being spun out here, not by being decided).
- [Dynamic Port Rebinding](dynamic-port-rebinding.md) — "L11" in
  Declarative Middleware used the SAME "no concrete driver → don't
  build it yet, cross-reference instead" resolution style this doc
  follows.
