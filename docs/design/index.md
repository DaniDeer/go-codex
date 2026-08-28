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

---

## Documents

_No document has graduated to this section yet._

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
