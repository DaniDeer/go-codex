---
name: deep-dive-bug-hunt
description: 'Two-phase methodology for finding real bugs that broad audits miss: (Phase A) a concept-by-concept deep dive through ONE API/port layer at a time, tracing every capability''s full path from declaration through every adapter''s dispatch, reproducing suspected bugs live before fixing; (Phase B) a mandatory re-review of the fixes against the governing design docs. Use when asked to "go in-depth into every X capability/concept", "go from X-concept to X-concept the full path", "deep dive into api/rest or api/events or api/reqreply or api/mcp", "find substantial bugs", "bug hunt", or "review the changes against the design docs and architecture" after such a deep dive.'
---

# deep-dive-bug-hunt

A narrow, deep, sequenced bug hunt through ONE API or port layer's capabilities — concept by
concept, tracing the FULL path from declaration to every adapter's dispatch — followed by a
mandatory re-review of the fixes against the governing design docs. This found real, significant
bugs (header-casing breaking both validation and responses, a silently-dropped `ClientHandle`
field, a `PropertyParam` merge gap, a publish-side capability-coverage gap across 3 adapters, an
entire `ErrorChannel`/`DeadLetter` fallback unwired on the primary recommended dispatch path) that
a broader, shallower pass missed across multiple prior review rounds.

## When to Use This Skill

- User asks to "go in-depth into every [rest/events/reqreply/mcp] capability/concept"
- User says "go from [X]-concept to [X]-concept the full path" (declaration → middleware →
  router → adapter dispatch → adapter implementation)
- User reports "we still find substantial bugs" after a lighter review pass
- User asks to re-review a just-completed deep dive's changes against design docs/architecture

## Relationship to `review-go-codex`

Different tool, different job — do not conflate or duplicate:

| | `review-go-codex` | `deep-dive-bug-hunt` (this skill) |
|---|---|---|
| Scope | ALL layers at once (REST, events, MCP, forge, reqreply) | ONE layer, one capability/concept at a time |
| Depth | Shallow — checklist pass over key files | Deep — full declare-to-dispatch trace, N-way adapter parity |
| Cadence | Periodic consistency audit | Triggered when a layer is suspected to still have real bugs |
| Fix discipline | Fix, verify, done | Fix ONLY after a LIVE failing-test reproduction |
| Output | `history.md` Round + commit summary | Same `history.md` convention, PLUS a mandatory Phase B design-doc re-review |

This skill REUSES `review-go-codex/references/checklist.md` (apply §7 structured errors, §8
observer pattern, §9 test coverage, §12 merge-field/boundary symmetry, §13 error-path ergonomics
per concept — do not invent a parallel checklist) and the SAME append-only `history.md` file and
Round-numbering convention. Read `review-go-codex/SKILL.md` first if unfamiliar with either.

## Phase A — Concept-by-concept deep dive

1. **Enumerate the layer's concepts/capabilities.** For `api/rest`: path/query/header/cookie
   params, status codes, security schemes, SSE, etc. For `api/events`: topic params, user
   properties, QoS, retained, security schemes, dead-letter, address, spec-endpoint. For
   `api/reqreply`/`api/mcp`: the equivalent per-package vocabulary. For `api/file`: `PathParam`/
   `MergedPathParam`, `MetadataParam`/`MergedMetadataParam` (the metadata merge-field axis),
   `Capability` (`AccessMode`/`NativePOSIXMode`), `BoundMiddleware` (one per role), `Router`,
   `ErrorPattern`, and the 10 roles themselves (Read/Create/Patch/Delete/List/Exists/Copy/Move/
   Mkdir/BatchDelete). Read the layer's own `builder.go` (and sibling files) to get the REAL
   list — do not assume REST's shape transfers 1:1 to another layer (events has no
   `ClientHandle`/`Register` split REST has, for example; `api/file` has no header/cookie/query
   axis at all — only path vars and its own metadata axis).
2. **Check `history.md` for already-covered concepts** (e.g. middleware, router/`ServeSpec`
   structural correctness are often reviewed separately already) — exclude those from fresh
   re-review; a concept round only re-derives what's genuinely unexamined.
3. **Sequence concepts via dependency-ordered SQL todos**, not a flat list:
   - The shared merge-field/transform substrate EVERY concept dispatches through goes FIRST
     (foundational — e.g. `transform.go`/`transform_dispatch.go`). A bug here would surface
     identically in every later round, so prove it clean (or fix it) before anything else.
   - User-named concepts next, each depending only on the foundational round (parallelizable in
     principle, done in sequence in practice).
   - A client-path/"handletransport" sweep depends on ALL concept rounds (it re-sweeps every
     concept on the CLIENT/publish side).
   - The spec/doc endpoint (OpenAPI/AsyncAPI content negotiation) goes LAST — it depends on
     every concept rendering correctly first.
4. **Per concept, trace the FULL path**: declare-time source (the `*Param`/`*Opt` constructor
   and its builder-applied field) → router/spec contribution (does it render correctly in the
   generated spec?) → EVERY relevant adapter's dispatch — this means N-way parity (3 adapters
   for events: mqtt, mqtt5, zeromq; 2 for REST: nethttp, chi), not just comparing 2 and calling
   it done. **Post-D-0010**, some concepts now route through a SINGLE shared dispatch-core
   function instead of N independent per-adapter copies (e.g. `adapters/internal/restdispatch`
   for the REST+SSE family, consumed by both `nethttp` and `chi`; a per-family
   `adapters/<name>/*_dispatch_core.go` for events/reqreply, e.g.
   `adapters/mqtt/subscribe_dispatch_core.go`, `adapters/mqtt5/reqreply_dispatch_core.go`). For
   these concepts the N-way check becomes three narrower checks instead of one: (a) prove the
   shared core itself correct ONCE; (b) verify EVERY consuming adapter actually delegates to it
   rather than silently reimplementing or drifting back to a duplicate copy; (c) separately
   check any PRE/POST logic an adapter still keeps OUTSIDE the shared core (setup, security-gate
   ordering, observer hooks) — consolidation removes duplicate-copy drift risk, but an adapter's
   own wiring around the shared call can still have an adapter-specific bug the core itself
   can't catch. **A layer with only ONE real adapter has no N-way parity to check in the usual
   sense** (none remain as of this writing — `api/file` graduated from its own Phase-1-era
   single-adapter state to 3 real backends, `adapters/file`/`adapters/s3`/`adapters/smb`, plus
   `ports`'s private `osBackend` as a 4th same-interface implementation built to satisfy
   `ports.File`/`ports.Dir` without an import cycle — genuine N-way `Backend`-method/optional-
   extension parity now applies to it normally, the same as REST's `nethttp`/`chi` or events'
   `mqtt`/`mqtt5`/`zeromq`) — but if a future layer is ever added with only one real adapter, the
   equivalent check becomes "does the one adapter correctly implement every required interface
   method + optional extension interface, including ones a future 2nd adapter would also need to
   get right." Where a SECOND, same-interface implementation exists for a non-adapter reason
   (mirroring `ports.osBackend`'s own precedent), treat that as the de-facto 2nd parity check
   instead — it carries the exact same under-scrutinized-new-code risk profile an adapter would.
5. **Reproduce any suspected bug LIVE, via a failing test, BEFORE fixing it.** This is the
   single highest-value practice in this methodology — a purely static read of the dispatch code
   missed real bugs across MULTIPLE prior review passes until an actual failing test exposed
   them. Write the test, confirm it fails against the CURRENT code, fix, confirm it then passes.
6. **Fix in priority order**, `gofmt`/`go build`/`go test`/`just check` after each; a full
   `examples/*/` sweep at the end of the round.
7. **Append a new `## Round <N>` to `history.md`** (continuing the existing numbering) — even a
   CLEAN round (no bug found) gets a short entry, so a future round never re-investigates the
   same concept from scratch.
8. **Update `go-codex.instructions.md`** only if exported API surface or documented behavior
   changed.

## Phase B — Re-review the fixes against design docs (mandatory, not optional)

Once every concept round in the layer is done, do ONE consolidated re-review pass — do not skip
this even if Phase A felt thorough; it catches a DIFFERENT class of problem (doc/architecture
drift, not code bugs):

1. For each Round that changed code, locate the governing `docs/design/d-*.md` doc(s) that
   originally specified the mechanism being fixed.
2. Confirm three things per fix: (a) the fix's BEHAVIOR matches what the doc already specifies —
   not a reinterpretation; (b) the fix doesn't reintroduce a pattern the doc explicitly rejected
   or retired; (c) any NEW `go-codex.instructions.md` note doesn't contradict an OLDER note
   about the SAME mechanism — read them together, not in isolation. An older note's unqualified
   wording ("adapter X's dispatch now does Y") can become misleading once a NEW fix changes what
   "dispatch" covers, even though neither note is individually wrong.
3. Produce a short verdict table: Round → doc(s) cross-checked → aligned/misaligned.
4. Fix ONLY documentation-clarity issues found this way directly. A genuine CODE/design
   misalignment (the fix's behavior contradicts the doc, not just imprecise wording) is a NEW
   finding — reopens Phase A for that concept, it is not something Phase B patches directly.

## Gotchas

- **A single "dispatch path" concept can have MULTIPLE independent reflection-based
  implementations that do NOT share code** — e.g. events pub/sub had up to 4 per role: a
  low-level escape-hatch function, a `Client.Attach`+`ServeSubscribers` reflection path, a
  `ports.Pattern`-binding `Transport` reflection path, and a `ports.SourceAdapter.Activate`. A
  fix applied to ONE of these does NOT imply the others got it — this exact gap recurred
  multiple times in one session (capability coverage, then again for dead-letter/`ErrorChannel`
  wiring) because each concept round has to re-verify ALL of a layer's dispatch paths, not
  just the ones touched by the previous round.
- **"Clean" (no bug found) is still a real, useful round — do not skip its `history.md` entry.**
  A concept with zero consumers anywhere in the codebase (e.g. an intentionally-additive,
  forward-looking type) is itself worth one line confirming it was checked and is genuinely
  inert, not silently unreviewed.
- **`history.md` is append-only — new Rounds go ABOVE older ones, numbering never resets.**
  Never edit a prior Round's entry to "correct" it after the fact; if Phase B finds a prior
  Round's conclusion needs qualifying, that's a NEW entry (or a documentation fix in
  `go-codex.instructions.md`), not a rewrite of history.
- **A design doc's own unit-test-plan table is usually package-level, not
  adapter-dispatch-path-specific** — this is WHY a pure doc-conformance read can miss a gap that
  only a live, adapter-level reproduction test catches. Do not trust "the design doc's test plan
  doesn't mention this path" as evidence the path is fine.
- **Sequencing matters for avoiding wasted rework**: if the foundational merge/transform round
  (first in Phase A) turns up a bug, every later concept round would have silently inherited it
  — always do that round FIRST and get it clean before fanning out to named concepts.
- **The "API-defined conformance test suite" packages (`resttest`, `eventstest`,
  `reqreplytest`, `portstest`, D-0010) are a NEW testable surface in their own right, not just
  test fixtures to trust blindly.** A concept round touching a family that has one of these
  packages should include one mutation-test check per existing `Assert*` function: deliberately
  break the thing the assertion claims to verify (in a scratch/throwaway copy, never committed),
  confirm the assertion actually fails, then revert. A conformance package with a quietly-too-
  loose assertion would otherwise rubber-stamp a real regression forever, across every adapter
  wired into it — and because these packages are themselves fairly new/unreviewed, don't assume
  their own correctness is already established just because adapters pass against them.
- **A concept round should also check whether the CONCEPT'S OWN TEST FILES reach into a shared
  `internal/<name>` package directly instead of the consuming pattern's public alias** — this is
  a distinct finding from a dispatch-path code bug (no runtime behavior changes), but it's the
  SAME root-cause class and was found TWICE in one session only because the user separately
  prompted for it afterward, not because a concept round's own methodology surfaced it
  proactively. While tracing a concept's declare-time source (step 4), also grep its test file(s)
  for `internal/route`/`internal/middleware`/etc. imports — a bare internal-package reference in
  test code is a signal EITHER that the test should use the public alias instead (if one exists)
  OR that a production-code public alias is itself missing (see
  `.github/skills/review-go-codex/references/checklist.md`'s §16 "Exported error types are
  aliased too" / "Test files use the public alias too" rows) — the SAME investigation that led to
  the Round 196/197 fixes and the subsequent 80-test-file rework.

## References

- [`review-go-codex/SKILL.md`](../review-go-codex/SKILL.md) — the broader audit this skill
  complements; read first if unfamiliar with the checklist/history conventions reused here.
- [`review-go-codex/references/checklist.md`](../review-go-codex/references/checklist.md) —
  the per-concept checklist sections (§7/8/9/12/13) applied during Phase A.
- [`review-go-codex/references/history.md`](../review-go-codex/references/history.md) — the
  shared, append-only Round log both skills write to.
