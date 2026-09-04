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
| [D-0002 — Pub/Sub Workflow Simplification](d-0002-pubsub-workflow-simplification.md) | `api/events`, `ports`, `adapters/mqtt5`, `adapters/mqtt`, `adapters/zeromq` | Pub/sub's client-centric role model — no fixed server/client pairing, since a broker is the intermediary and both publisher and subscriber are CLIENTS of a channel — resolved via `events.Client` + role-scoped `Subscriber[T]`/`Publisher[T]` builders (`WithSubscribe`/`WithPublish`, `.Use`/`.SubscribeMW`/`.PublishMW`, `.Handle(client)`/`.Register(client)`), unconditional security-coverage enforcement (`CheckCoverage`/`checkImplementationsDeclared`), and a reflection-based `Client.Attach`/`.Publish`/`.Subscribe`/`.ServeSubscribers` convenience layer unified with `d-0001`'s own `Server.Attach`/`rest.Client`/`nethttp.Attach` design. Format resolution (JSON/YAML/TOML/Gob/custom binary) is centralized on `ChannelHandle`/`RouteHandle` themselves (`EncodeWithFormats`/`DecodeMergedWithFormats`), so every adapter — escape-hatch primitive AND `Client.Attach` shim alike — is a thin caller of one canonical method. Fully shipped across all three pub/sub adapters plus REST/`nethttp`/`chi`, with every old, call-time-competing public primitive removed (confirmed exceptions kept for genuine advanced needs). Establishes the pattern `api/reqreply`'s own planned rework (see `docs/roadmap/reqreply-workflow-simplification.md`) follows. |

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
