# Declarative-Workflow Walkthrough & Per-API Tutorial Skills

> **Status:** Design draft — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

`docs/roadmap/capability-requirement-composition.md`'s Phase 8 review
closeout left two linked items unstarted: a **joint declarative-workflow
walkthrough** (user + agent declare one `api/rest`, one `api/events`, and
one `api/reqreply` API from scratch, end-to-end, as a genuine first-time-user
simulation) and **three per-API guided tutorial skills**
(`tutorial-api-{rest,events,reqreply}`) meant to teach that same workflow
live, informed by whatever friction the walkthrough surfaces.

Design review alone (the `review-go-codex`/`review-docs` rounds this
session ran) catches API-surface bugs and stale docs, but it does not catch
**workflow friction** — the awkward pauses, the "which method do I call
next" hesitations, the steps a static guide glosses over because its author
already knows the answer. Only walking the real declare → capability →
attach → run journey, live, surfaces that. This doc plans both the
walkthrough itself (a one-time session activity, not a code change) and the
tutorial skills it feeds (a durable, repeatable artifact — `.github/skills/`
content other sessions can load).

## Scope decisions

| In scope | Out of scope |
|---|---|
| A structured walkthrough SCRIPT (not a transcript) — exact steps, decision points, and "what friction to watch for" prompts for each of the 3 APIs | Actually running every step with a live broker/socket connection — the walkthrough uses the same in-process mock clients `examples/{rest,events,reqreply}-api` already use; no new infrastructure |
| Three tutorial `SKILL.md` files, one per API, each teaching the FULL declare → register/attach capability → run workflow live | A combined single tutorial skill — explicitly rejected per the main roadmap doc's "split rather than combined" preference, same reasoning as the 1.1/1.2/1.3 and 2.1-2.4 per-API review splits this session already established |
| Capturing walkthrough findings as roadmap-doc Learnings entries (in THIS doc, not the now-closed `capability-requirement-composition.md`) or spinning out a new follow-on roadmap doc if a finding is substantial | Fixing every friction point found — a genuinely substantial fix (e.g. a missing convenience method) gets its OWN follow-on roadmap doc per the `plan-a-new-codex-feature` skill's own rule, not folded in here silently |
| Referencing/building on the existing flagship examples (`examples/rest-api`, `examples/events-api`, `examples/reqreply-api`, `examples/sensor-service`) rather than inventing new demo code | Writing brand-new example programs — the walkthrough and tutorials should point at and walk through EXISTING, already-verified-green examples, not duplicate them |
| Deciding each tutorial skill's "teaching style" (does it generate real files in the user's workspace, or talk through the `examples/*-api` source read-only?) | Authoring any skill content before the walkthrough runs — tutorials must reflect the POLISHED workflow the walkthrough validates, not a guess at one |
| **A true interactive, per-step GATED mode**: the skill performs ONE step, presents it, then STOPS and waits for the user's explicit go-ahead (`ask_user` with options "Continue to next step" / "Redo this step" / "Go back to step N" / "Stop here") before proceeding — never auto-advances through multiple steps in one turn | A freeform chat-only pacing with no enforced stop ("I'll let you know when to continue" left to prose alone) — rejected per explicit user direction; gating MUST be tool-enforced (`ask_user`), not prose-only, since prose-only pauses are not reliably honored under autopilot/auto-approval runtime modes |
| **A second "developer mode" INSIDE each of the 3 tutorial skills** (a mode SWITCH, not a 4th separate skill) — confirmed via explicit user direction — where the skill documents the user's OWN real implementation work (not a scripted walkthrough) step by step, then drafts a `docs/roadmap/<feature>.md` from it using the SAME forward-looking Explore-mode template `plan-a-new-codex-feature` already defines | A 4th, standalone, general-purpose "document any go-codex work" skill — explicitly rejected; developer mode stays scoped to `api/rest`/`api/events`/`api/reqreply` work, reached only via one of the 3 tutorial skills |
| **Step 0 as the mode-selection checkpoint**: every tutorial skill invocation starts with a short, explicit explanation of what the skill does and an `ask_user` choice between "Tutorial mode" (guided walkthrough) and "Developer mode" (document my own build) — confirmed via explicit user direction | Auto-detecting which mode to use from context (e.g. "the user is writing real code, so infer developer mode") — explicitly rejected; the user was unsure this would be reliable, so Step 0 ALWAYS asks explicitly instead of guessing |

## Toolchain / dependency decisions

No new dependencies. The walkthrough and resulting skills are documentation/
process artifacts only — they consume existing `api/rest`/`api/events`/
`api/reqreply` + adapter code as-is. `.github/instructions/
agent-skills.instructions.md` governs the 3 new `SKILL.md` files' format
(frontmatter `name`/`description`, body sections) — see that file + the
`review-docs`/`review-go-codex` skills in this same repo as the closest
structural precedent for a multi-step, methodology-driven skill (as opposed
to a tool-wrapping skill like `webapp-testing`).

## API surface

No Go code changes are anticipated as part of THIS doc's own scope. If the
walkthrough surfaces a genuine API gap (e.g. a missing convenience
constructor), that becomes its OWN follow-on roadmap doc per the Scope
Decisions table above — not an addition folded into this one.

## The walkthrough: structure and what it produces

The walkthrough is a **single combined session** (not 3 separate ones) run
once this doc is approved, structured as 3 back-to-back passes — one per
API — each following the IDENTICAL shape so friction patterns common to all
3 (vs. API-specific ones) are easy to tell apart afterward:

1. **Struct + codec** — declare a small, realistic domain struct (e.g. a
   sensor reading, a user-created event, a compute request/response) and
   its `codex.Struct[T]` codec from scratch, narrating each
   `RequiredField`/`OptionalField` choice.
2. **Route/channel declaration** — `rest.NewRoute`/`events.NewChannel`/
   `reqreply.NewRoute`, including at least one path/topic variable (so
   `PathParam`/`TopicParam`/merge-field ergonomics get exercised, not just
   the flat/no-vars happy path).
3. **Capability requirement declaration** — a `RequireX`/explicit
   `Capability` value declared on the route/channel (e.g. `RequireQoS` for
   events, a REST `HeaderParam`-implied Tier 2 requirement) — the step this
   whole capability-requirement-composition roadmap built, and the one
   most likely to surface fresh friction since it's the newest, least-
   polished part of the workflow.
4. **Adapter attachment** — `Client.Attach`/`Server.Attach` with a real
   adapter's `New*Transport` constructor (the in-process mock client/socket
   each flagship example already wires up — no new infrastructure).
5. **Run it** — dispatch one request/message/call end-to-end and observe
   the result.

**At every step, narrate (and record) 3 things:** what the next call should
logically be before checking docs/source (does the obvious guess match
reality?), whether any step required jumping between 2+ files/docs pages to
piece together (a single-page-origin signal would be ideal), and whether an
error message (if one is deliberately triggered, e.g. a missing capability)
is immediately actionable without extra investigation.

**Output of the walkthrough**: a "Learnings" section appended to THIS doc
(not a separate file) listing every friction point found, each tagged
`cosmetic` (wording/doc-only fix), `small` (a guide/example improvement),
or `substantial` (spins into its own follow-on roadmap doc) — mirroring the
`bug`/`small`/`trivial` severity vocabulary `review-docs` already uses, for
consistency across this session's review methodology.

## Tutorial mode — the interactive step-gating mechanism

Raised directly by the user: a skill CAN support a genuine, user-paced,
step-by-step teaching mode — a `SKILL.md` is instructions loaded into the
agent's context, and those instructions can direct the agent to perform
exactly ONE step, then stop and wait. This repo's own `review-docs` skill
already has a lighter version of this ("Do NOT start implementing until the
user confirms" — its Phase 4). The design below makes the gate TOOL-
ENFORCED rather than prose-only, since a prose-only "I'll wait for you to
say next" is not reliably honored under autopilot/auto-approval runtime
modes — a tool call that returns a structured choice is a real, enforced
stop.

**Mechanism**: every tutorial-mode step (the SAME 5-step shape the
walkthrough validates: struct+codec → route/channel → capability
requirement → adapter attach → run) ends with an `ask_user` call — never a
plain text question. The call presents, at minimum:
- `continue` — "Continue to next step"
- `redo` — "Redo this step" (with a freeform field for what to change)
- `go_back` — "Go back to an earlier step" (paired with a freeform field:
  which step, and what to change)
- `stop` — "Stop here for now"

On `go_back`, the skill must actually RE-DO the named step with the
requested change — not just acknowledge it — before presenting the NEXT
`ask_user` gate again. **The skill must never perform 2+ steps in one
turn without an intervening `ask_user` call**, even if the user's prior
answer seems to imply eagerness to move fast — one step, one gate, always.

## Developer mode — document-as-you-build

A second, user-requested operating mode living INSIDE each of the 3
tutorial skills (a mode SWITCH, not a 4th separate skill — confirmed via
explicit user direction) for a user who wants to build something REAL
(not follow a script) while still getting roadmap-style documentation out
of the session. Scoped to `api/rest`/`api/events`/`api/reqreply` work only,
reached only through one of the 3 tutorial skills — not a general-purpose
"document any go-codex work" capability.

**Step 0 — shared mode-selection checkpoint.** Every tutorial-skill
invocation starts here, in BOTH modes: a short (3-5 sentence) explanation
of what the skill does, followed by an `ask_user` 2-way choice:
- "Tutorial mode — guided, step-by-step, I teach you"
- "Developer mode — I document what you're building as you build it"

No auto-detection — the user was explicitly unsure an automatic "infer
from context" trigger would be reliable, so Step 0 ALWAYS asks rather than
guessing.

**In developer mode**, the skill does NOT walk the user through
pre-scripted tutorial steps. Instead, the user drives their OWN real
implementation (their own struct/codec, route/channel, capability
requirement, adapter attachment, run) and the skill's job is to: (a)
observe/assist as needed, (b) capture each step's concrete decisions as
they're made (what was declared, why, any alternatives considered), and
(c) at the end of the session (or on explicit request), draft a NEW
`docs/roadmap/<feature-name>.md` using the EXACT `plan-a-new-codex-feature`
Explore-mode template (Motivation / Scope decisions / API surface /
Structured errors / Observer integration / Unit test plan / Files to
create / Out of scope / Open design decisions) — populated from what was
ACTUALLY built, written in the same forward-looking voice the template
uses elsewhere (as if this had been planned this way), not as a
chronological journal/changelog of events.

Developer mode's own internal pacing (does it ALSO gate per-step via
`ask_user` the way tutorial mode does, or does it let the user drive
freely and only interject to ask clarifying questions when a decision
needs capturing?) is flagged as an Open design decision below — not
resolved by this round's answers.

## The 3 tutorial skills: shared shape

Each of `tutorial-api-rest`, `tutorial-api-events`, `tutorial-api-reqreply`
is a `.github/skills/tutorial-api-<name>/SKILL.md` teaching ONE API's full
workflow, live, to a user working in THIS repo or a project depending on
go-codex. Both Tutorial mode and Developer mode above live in the SAME
file, selected at Step 0. Shared structure (mirrors `review-docs`/
`review-go-codex`'s own "methodology skill" shape, not a tool-wrapping
skill):

| Section | Content |
|---|---|
| Frontmatter | `name: tutorial-api-<rest\|events\|reqreply>`; `description` naming the API, the keywords a user would say ("teach me api/events", "walk me through declaring a REST route", "how do I set up MQTT capabilities"), and explicitly mentioning the declare → capability → attach → run shape |
| `## Step 0 — mode selection` | Shared by both modes: short explanation of what the skill does + an `ask_user` 2-way choice between Tutorial mode and Developer mode (see the dedicated section above) — always asked, never inferred |
| `## When to Use This Skill` | Concrete trigger phrases, mirrored from the description |
| `## Tutorial mode — the workflow, step by step` | The SAME 5-step shape the walkthrough used (struct+codec → route/channel → capability → attach → run), each step pointing at the REAL current API surface (not frozen at drafting time — the skill should read current source if uncertain, same discipline this session applied throughout its review rounds), each step ending in the `ask_user` gate described above |
| `## Tutorial mode — worked example` | Walks through (not duplicates) the relevant flagship example — `examples/rest-api`/`examples/events-api`/`examples/reqreply-api` — pointing at specific files/line ranges rather than re-pasting large code blocks that can drift out of sync |
| `## Tutorial mode — common pitfalls` | Directly seeded from the walkthrough's own Learnings entries for that API — this is the whole reason the walkthrough runs BEFORE the skills are authored |
| `## Developer mode — document-as-you-build` | The mode described in the dedicated section above: observe the user's own real implementation, capture decisions, draft a new roadmap doc at the end via the `plan-a-new-codex-feature` Explore-mode template |
| `## References` | Link to that API's `docs/features/*.md`/`docs/guides/*.md` pages, so the skill teaches the LIVE workflow while guides stay the static reference |

## Structured errors

Not applicable — no new Go error types; this is a process/documentation
deliverable.

## Observer integration

Not applicable — no new runtime code.

## Unit test plan

Not applicable in the usual sense (no Go code). Verification instead:
- `go build ./...` after any example-pointing edits (confirms no code
  changes were accidentally introduced and no referenced file/symbol is
  stale).
- A manual re-run of each tutorial skill's own worked-example steps against
  the CURRENT repo state before considering it done, confirming every
  referenced file/line-range/symbol still exists and the narrated commands
  actually produce the stated result.

## Files to create

| File | Responsibility |
|---|---|
| `docs/roadmap/declarative-workflow-tutorials.md` (this file) | Design doc; gains a "Learnings" section once the walkthrough runs |
| `.github/skills/tutorial-api-rest/SKILL.md` | Live tutorial: declare → capability → attach → run for `api/rest` |
| `.github/skills/tutorial-api-events/SKILL.md` | Live tutorial: declare → capability → attach → run for `api/events` |
| `.github/skills/tutorial-api-reqreply/SKILL.md` | Live tutorial: declare → capability → attach → run for `api/reqreply` |

No `docs/features/`/`docs/guides/` page additions are anticipated — these
skills are a NEW surface (live, interactive teaching) distinct from the
existing static feature/guide pages, which stay as the reference material
the skills link out to.

**Both Tutorial mode and Developer mode live in the SAME 3 `SKILL.md`
files above, selected at each skill's Step 0** — no separate file per
mode, and no 4th skill for developer mode (confirmed via explicit user
direction; see Scope decisions above).

## Out of scope (Phase 2)

- A 4th tutorial skill for `api/mcp` — not requested; `api/mcp` was outside
  this whole roadmap's scope (it has no `Capability`/`CapabilityRequirement`
  mechanism to teach) and already has `docs/guides/mcp.md`.
- Automating the walkthrough (e.g. a script that runs all 3 passes
  unattended) — the whole point is a HUMAN-in-the-loop friction-finding
  session; automating it would defeat the purpose.
- Any fix substantial enough to need its own roadmap doc, per the Scope
  Decisions table above — those are tracked as separate follow-ons, not
  absorbed into this doc's own Files-to-create list.

## Open design decisions

- **Does the walkthrough run as part of implementing THIS doc, or as a
  separate, later session?** Leaning toward: run it as this doc's own
  Implement step (walkthrough is cheap, ~1 session, and directly produces
  the Learnings section the tutorial skills depend on) — not pre-decided
  here, confirm before starting Implement.
- **Should each tutorial skill generate real scratch files in the user's
  workspace (a true "build it with me" session), or walk through the
  existing `examples/*-api` source read-only?** The shared-shape table
  above leans toward "point at existing examples, don't duplicate them,"
  but a live, hands-on skill arguably teaches better by having the user
  type the declarations themselves in a scratch file. Resolve during the
  walkthrough itself — whichever felt more natural there is the answer.
- **Exact severity bar for "substantial enough to spin into its own
  roadmap doc"** — mirrors `review-docs`'s existing `bug`/`small`/`trivial`
  distinction loosely, but that vocabulary was built for DOC bugs, not
  workflow-ergonomics gaps; may need its own, slightly different bar
  (e.g. "touches exported API surface" vs. "wording/example-only").
- **Developer mode's own internal pacing** — does it ALSO gate per-step
  via `ask_user` the way Tutorial mode does, or does it let the user drive
  freely (their own real implementation, at their own pace) and only
  interject with a clarifying question when a decision needs capturing
  for the eventual roadmap draft? Not resolved by this round's `ask_user`
  answers — the user confirmed developer mode's SHAPE (mode switch, Step-0
  trigger, scope, output template) but not its internal pacing. Resolve
  during Refine/Implement, likely by trying both during the walkthrough
  itself (developer mode, if exercised there, can test which pacing feels
  right).
- **Exact `ask_user` schema for the "go back to step N" case** — a single
  freeform text field ("which step, what to change") vs. a structured
  `{step: enum, change: string}` pair. Leaning freeform for simplicity
  (fewer rigid enum values to keep in sync as steps evolve), but not
  locked in — resolve during Implement when the actual `ask_user` call is
  written.

## See also

- [Composable Capability Requirements](capability-requirement-composition.md) —
  Phase 8's review that raised this need; the two-step declare/capability
  model this walkthrough exercises end-to-end.
- [`docs/concepts/declaring-apis-and-ports.md`](../concepts/declaring-apis-and-ports.md) —
  the durable narrative home for the declare → capability → attach model
  this walkthrough and these tutorials teach live.
- [`.github/instructions/agent-skills.instructions.md`](../../instructions/agent-skills.instructions.md) —
  skill-authoring conventions the 3 new `SKILL.md` files must follow.
- `examples/rest-api`, `examples/events-api`, `examples/reqreply-api`,
  `examples/sensor-service` — the existing flagship examples the
  walkthrough and tutorials build on rather than duplicate.
