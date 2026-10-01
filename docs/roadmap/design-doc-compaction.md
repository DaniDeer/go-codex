# Design-Doc Compaction & the Workflow-Model Narrative

> **Status: IMPLEMENTED.** Scope widened at execution time (explicit user
> direction): rather than just trimming D-0006 and relocating the
> interface-audit table, `docs/roadmap/capability-requirement-composition.md`
> — confirmed to be D-0006's own REWORK, extending it from events-only to
> all 3 api packages — was merged wholesale into D-0006 as its new §9, and
> the roadmap file deleted (not promoted separately), after a full ~100-file
> blast-radius sweep repointed every inline reference. See "Learnings"
> below for the full executed scope.
> [← Back to Roadmap](index.md)

## Motivation

`docs/design/d-0006-protocol-native-capabilities.md`'s Phase 8 review
concluded that go-codex's api layer and adapter layer are now
architecturally AND feature complete for the model this whole roadmap
built: a two-step declaration workflow (declare the communication
pattern — route/channel/topic, via codecs — then declare capability
requirements/values on top of it) with ONE adapter programming
contract for protocol-native behavior (the sealed `Capability`
interface, implemented by the adapter, dispatched via `Apply`/
`events.ApplyCapabilities` — never a parallel field/positional
backdoor, per D-0006's now-explicit "zero backdoor" design goal).

That model was worked out incrementally across `docs/design/
d-0006-protocol-native-capabilities.md` and 8 phases of
`capability-requirement-composition.md`, and the WORKING NOTES from
that incremental process (addenda, "reopened → reversed → reshipped"
narrations, renamed-symbol amendments layered on top of each other) are
now baked into the design docs' prose. That churn was valuable DURING
design — it's exactly what `docs/design/`'s own stated purpose says
these docs preserve — but it is noise for a future reader who only
wants: (a) the current, final shape of the model, and (b) why it looks
this way. This doc captures a plan to trim that noise where it has
accumulated most (D-0006 first), and to give the now-stable overall
model ONE clear, durable home outside the roadmap-doc lifecycle — so
the model's description doesn't disappear when
`capability-requirement-composition.md` itself is eventually deleted or
promoted (see its own Phase 8 "roadmap doc's fate" item).

## Scope decisions

| In scope | Out of scope |
|---|---|
| Trim `docs/design/d-0006-protocol-native-capabilities.md`'s remaining historical layering (its body, §0-§9, kept largely as-is per repo convention — its STATUS BLOCK was already consolidated during Phase 8's own item-5 execution; this doc plans any FURTHER trims found necessary during Refine/Implement) | Compacting `d-0001`/`d-0002`/`d-0003`/`d-0004`/`d-0005` — not requested; may be a future follow-on once this pass proves the pattern is worth repeating |
| Extend `docs/concepts/declaring-apis-and-ports.md`'s "two declaration workflows" section with an explicit capability-declaration step | Changing `docs/design/index.md`'s own stated purpose ("preserve full design rationale... review history") — that policy stays as-is; this doc's compaction is an editorial trim within an individual doc, not a policy change |
| Move the Phase 8 interface-audit table (available interfaces / gaps / deliberately-non-interface) out of the roadmap doc into a new, durable section of `docs/concepts/ports-and-adapters.md` | Auditing NEW interfaces beyond what Phase 8 already found — this doc relocates and reframes existing findings, it doesn't re-derive them |

## API surface

No Go code changes — this is a documentation-only design. No new
exported symbols.

## Where the workflow-model narrative goes

`docs/concepts/declaring-apis-and-ports.md`'s existing "The two
declaration workflows" section documents the Spec-backed 3-step flow:

```
NewRoute / NewChannel / NewResource / NewTool
    │
    └─ .Register(builder) ──→ Handle ──→ adapter (nethttp / mqtt5 / mcpgo)
                         └──→ builder.OpenAPISpec() / AsyncAPISpec() / MCPSpec()
```

This stops BEFORE capability/requirement declaration — it doesn't show
capabilities as an explicit step in the picture at all today. Planned
addition: a step between `.Register`/`Handle` and adapter attach,
showing the now-complete 3-part model:

```
NewRoute / NewChannel                      (step 1 — declare the pattern)
    │
    ├─ RequireX(...) / Capability values    (step 2 — declare capability
    │  via WithOptions/ChannelOpt            requirements and/or values)
    │
    └─ .Register(builder) ──→ Handle ──→ adapter (step 3 — attach)
```

Plus a short paragraph stating plainly: **"`Capability` is the
interface an adapter implements against — the PROGRAMMING CONTRACT
between the api layer and the adapter layer for protocol-native
behavior, exactly like `ServerTransport`/`ClientTransport` are the
contract for the base pattern itself."** This is the crystallization
the user asked for: one place a new reader naturally lands on already
(rather than piecing the model together from 8 roadmap phases).

## Where the interface-audit table goes

The Phase 8 interface audit (3 sections — A. available interfaces
adapters implement against, B. missing interfaces/gaps + potential
closes, C. interactions deliberately NOT interface-implemented, with
reasons) currently lives inline as a large bullet in
`capability-requirement-composition.md`'s Phase 8 section — a roadmap
doc, whose whole point is to be removed/promoted once its feature
ships. That table is durable reference material a future maintainer
adding a 7th adapter would want to find easily; it should not
disappear with the roadmap doc's eventual fate.

Planned new home: `docs/concepts/ports-and-adapters.md`, which already
ends with a "Guardrail: adapters as pure protocol shims" section — a
new section immediately after it, "Interface inventory: what adapters
implement against," is the natural landing spot: a durable, rewritten
(not copy-pasted) version of the 3-section audit, reframed as timeless
reference prose matching this concepts page's existing register (no
"Phase 8"/"this session found" framing — just the current state of the
world). Once moved, `capability-requirement-composition.md`'s Phase 8
bullet for the interface audit shrinks to a one-line pointer.

## Structured errors

Not applicable — documentation-only change.

## Observer integration

Not applicable — documentation-only change.

## Unit test plan

Not applicable — documentation-only change. Verification is `go build
./...` (confirms no dangling godoc `[Symbol]` links break) plus a
manual read-through of the edited sections for internal consistency.

## Files to create / change

| File | Responsibility |
|---|---|
| `docs/design/d-0006-protocol-native-capabilities.md` | Further trims, if any remain, beyond the status-block consolidation already done in Phase 8's item 5 |
| `docs/concepts/declaring-apis-and-ports.md` | Add the capability-declaration step + "Capability is the adapter's programming contract" paragraph to "The two declaration workflows" |
| `docs/concepts/ports-and-adapters.md` | Add "Interface inventory: what adapters implement against" section (the relocated, rewritten audit table) |
| `docs/design/d-0006-protocol-native-capabilities.md` | Shrink the Phase 8 interface-audit bullet to a pointer once the table is moved |

## Out of scope (Phase 2)

- Compacting `d-0001` through `d-0005` — deferred pending this pass
  proving the approach is worth repeating.
- Any change to `docs/design/index.md`'s stated preservation policy.
- Re-auditing for NEW interface gaps — this doc only relocates
  already-completed findings.

## Open design decisions — RESOLVED at Implement time

- **Exact trim boundary for D-0006's body (§0-§9)** — RESOLVED: trimmed
  only §7's dense `[Review-N, Severity]` bracket-tag churn (13 tags
  removed mechanically, substance/reasoning untouched) — the prototype
  REASONING in §0-§6/§8 was confirmed to have real, lasting design value
  (not just process noise) and was left as-is per repo convention.
- **Whether "Interface inventory" in `ports-and-adapters.md` should be a
  living section** — RESOLVED: yes, explicitly marked as a living
  section in its own intro paragraph, to be updated whenever a new
  adapter ships or a new interface is introduced.
- **Scope widened at execution time, beyond this doc's original plan**:
  confirmed via explicit user direction that
  `capability-requirement-composition.md` is D-0006's own REWORK (it
  literally says so in its own Phase 1: "rewriting D-0006's shipped
  mechanism into the three-tier vocabulary"), not a separate,
  independently-promotable design. Decided outcome: merge + delete, not
  merge + promote-separately — rewrote D-0006's status block to
  describe the COMPLETE current three-tier model (adding Tier 2 REST
  Header/Cookie/Query, which D-0006 never originally covered), then
  appended the roadmap doc's full Phase 1-8 content wholesale as a new
  §9 (heading levels demoted, phase labels preserved VERBATIM), then
  mechanically repointed all ~100 files' inline
  `"...capability-requirement-composition.md's Phase N"` references at
  D-0006 instead (safe specifically because the phase labels were
  preserved verbatim during the §9 merge), then deleted the roadmap
  file and its roadmap-index/nav entries. A full repo-wide grep
  confirmed zero remaining references to the old path before deletion.

## Learnings

- **The "safe mechanical replace" insight was the key unlock**: a naive
  rename/reference-sweep of ~100 files citing specific "Phase N" sections
  would normally require re-deriving which NEW section each old
  reference should point to — expensive and error-prone at this scale.
  Preserving the roadmap doc's phase headers VERBATIM during the
  wholesale §9 merge (rather than renumbering them to fit D-0006's own
  `§N.M` scheme) meant a single, blind path-string replace
  (`docs/roadmap/capability-requirement-composition.md` →
  `docs/design/d-0006-protocol-native-capabilities.md`) kept every
  citation accurate with zero per-reference judgment calls needed.
- **A blast-radius grep BEFORE executing a merge+delete is not
  optional** — the original plan assumed a routine "compact one design
  doc" scope; the actual blast radius (~100 files, deeply embedded in
  shipped source code's own godoc comments, not just a few stray doc
  mentions) was only discovered by grepping first. Confirms the
  `plan-a-new-codex-feature` skill's own "Removing an old API" checklist
  rule: sweep for documentation references BEFORE declaring a removal
  done — this applies to deleting a DOC other files cite, not just
  deleting a Go symbol.
- **A seemingly-safe exclude filter can silently misfire**: an
  `if "/.git" in root: continue` check (meant to skip the `.git`
  directory) ALSO matched `.github` as a substring (`.github` contains
  the literal characters `.git` at its start) — silently skipping all 6
  `.github/` files during the first mechanical sweep pass. Caught only
  by a follow-up verification grep, not by the sweep's own reported
  "changed N files" count looking wrong. Lesson: path-prefix exclusion
  checks need an explicit path-separator boundary (e.g. `root ==
  "./.git" or root.startswith("./.git/")`), not a bare substring test.

## See also

- [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md) — the design doc this whole compaction merged into; see its §9 for the full merged phase history.
- [`docs/concepts/declaring-apis-and-ports.md`](../concepts/declaring-apis-and-ports.md) — new home for the workflow-model narrative (capability-declaration step added).
- [`docs/concepts/ports-and-adapters.md`](../concepts/ports-and-adapters.md) — new home for the interface-inventory table (its own "Interface inventory" section).
