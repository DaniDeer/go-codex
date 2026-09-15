# Observer Pattern — Param-Error Reporting Bug + Thin-Adapter Consolidation

> **Status:** PARTIALLY SHIPPED. O1 (the core bug) and the first slice of
> O5 (mqtt v3 cleanup) are implemented, tested, and verified. O2-O4 are
> confirmed fixed FOR FREE by O1 (no code change needed in zeromq/mqtt5).
> O5's remaining adapters (mqtt5), O6, O7, O8, and O9 are documented here,
> NOT yet implemented — this doc is the landing place for that remaining
> work, mirroring the now-shipped-and-retired "Events Pub/Sub
> Consolidation" roadmap plan's own phased, implementation-ready format
> (see [D-0002](../design/d-0002-pubsub-workflow-simplification.md)'s
> Addendum for that plan's durable record).
> [← Back to Roadmap](index.md)

## Motivation

Investigated the Observer pattern across the api layer for opportunities
to move responsibility from adapters into the api/core abstraction,
following the same "thin adapter, thick api layer" principle already
applied to `reqreply.Observability`/`events.Observability` (see
[D-0002](../design/d-0002-pubsub-workflow-simplification.md)'s Addendum
for the events-side precedent). Rather than speculate, every finding
below was confirmed via direct code tracing
and, for the core bug, a **live reproduction** (a throwaway Go module
built in the session workspace with a `replace` directive against this
repo, run, and deleted after confirming the result).

## O1 — CONFIRMED BUG, FIXED — `stats.ReportErrors` silently dropped Param-sourced validation errors

**Status: ✅ SHIPPED this round.**

`stats.ReportErrors`'s generic error walker (`stats/observer.go`) was
documented to handle exactly 3 error shapes: `codex.ValidationErrors`,
`codex.KeyError`, `codex.ElementError`. It silently produced **zero**
`RecordValidationError` calls for `codex.ParamError`/`MissingParamError`/
`InvalidParamError` — confirmed via a live reproduction, including the
REALISTIC case where `ParamError.Err` is a `codex.ConstraintError` (what
`codex.Codec.Validate` actually returns; `ConstraintError` has no
`Unwrap()`, so the walker's fallback unwrap-and-recurse chain never
reaches a handled type either).

These 3 types are the shared foundation of **every** "{varName}"
template-var mechanism in go-codex — `rest.PathParamError`/
`MissingPathVarError`, `events.TopicParamError`/`MissingTopicVarError`,
`reqreply.RouteParamError`/`MissingRouteParamError` are all type ALIASES
of these exact `codex` types.

**Fix shipped:** extended `walkErrors` in `stats/observer.go` to handle
all 3 additional types, reporting via `obs.RecordValidationError`
identically to the existing 3 cases (constraint name via the existing
`ConstraintName` helper for `ParamError`, `"required"` for
`MissingParamError`, `"declared-var-not-in-template"` for
`InvalidParamError`), recursing into the inner error for `ParamError`
(consistent with the other 3 handled types' own recursive design).

**Tests added** (`stats/observer_test.go`):
- `TestReportErrors_ParamError_RealisticConstraintFailure` — reproduces
  the exact realistic-failure scenario found during investigation.
- `TestReportErrors_MissingParamError`
- `TestReportErrors_InvalidParamError`

**Verification:** `gofmt -l .` clean, `go build ./...` clean,
`go test ./stats/...` all pass (new + existing).

**No adapter code required any change for this fix** — every existing
generic `stats.ReportErrors(obs, location, err)` call site across the
entire repo now works correctly automatically.

## O2/O3/O4 — CONFIRMED fixed for free by O1

- **O2**: `adapters/zeromq/adapter.go` (3 sites, pub/sub topic_var) and
  `adapters/zeromq/reqreply_transport.go` (2 sites, reqreply topic_var)
  call the generic `stats.ReportErrors(obs, "topic_var", err)` directly
  with NO private unpacker at all — zeromq's topic-var/route-param
  Observer reporting was 100% broken for `Missing`/realistic-`Param`
  cases before O1, now correct with zero code change.
- **O3**: `adapters/mqtt5` had private unpacker helpers
  (`reportTopicParamErrors`/`reportMissingTopicVarErrors`/
  `reportInvalidTopicErrors`) but only called them on the PUBLISH-side
  `BuildTopic` path — the SUBSCRIBE-side (`adapter.go:296,306`) and the
  reqreply Attach server-side dispatch (`reqreply_transport.go:531`)
  called the generic (previously broken) path directly. Now correct with
  zero code change — **mqtt5's own equivalent cleanup (removing its now-
  redundant private helpers) is still open, see O5 below.**
- **O4**: `adapters/mqtt`(v3)'s subscribe-side topic_var handling omitted
  `reportMissingTopicVarErrors` at one call site (asymmetric with the
  publish-side call, which included it) — folded into O5's mqtt v3
  cleanup (done, see below), which replaced the incomplete manual call
  set with the single, always-correct generic call.

**No further action needed for O2/O3/O4** — confirmed via
`go test ./adapters/zeromq/... ./adapters/mqtt5/... ./adapters/mqtt/...`,
all green.

## O5 — thin-adapter cleanup: delete now-redundant private Param-error unpackers

**Status: mqtt (v3) DONE this round. mqtt5 NOT YET DONE.**

Once O1 shipped, the private per-adapter helpers that manually unpacked
`ParamError`/`MissingParamError` became fully redundant — they duplicate
what the generic `stats.ReportErrors` now does correctly and uniformly.

### `adapters/mqtt` (v3) — ✅ DONE this round

- Deleted `reportTopicParamErrors`/`reportMissingTopicVarErrors`
  (2 functions) from `adapters/mqtt/adapter.go`.
- Updated all 3 call sites (subscribe-side `TopicVarsFromMessage` error,
  subscribe-side `codex.DecodeVars` merge error, subscribe-side handler
  business error, publish-side `BuildTopic` error) to call
  `stats.ReportErrors(obs, "topic_var", err)` directly instead.
- `reportInvalidTopicErrors`/`reportTopicMismatchErrors` were KEPT
  UNCHANGED (they handle `events.InvalidTopicError`/`TopicMismatchError`
  — adapter-specific runtime types NOT covered by O1's fix; see O6).
- **Bonus fix found during implementation**: the subscribe-side
  `codex.DecodeVars` merge-error call site (`adapter.go`, was line ~275)
  was calling `reportTopicParamErrors(mergeErr, obs)` — but
  `codex.DecodeVars` returns `codex.ValidationErrors`, NOT `ParamError`,
  so that call was ALREADY a no-op (a pre-existing, separate, smaller bug
  — wrong-shaped reporter called for that error type — not part of the
  original O1-O9 finding list, discovered while tracing every call site
  during implementation). Fixed as part of the same edit: now calls
  `stats.ReportErrors(obs, "topic_var", mergeErr)`, which correctly
  handles `ValidationErrors` (always did) AND would handle `ParamError`/
  `MissingParamError` too if `DecodeVars` ever changed shape.
- Verified: `gofmt -l .` clean, `go build ./adapters/mqtt/...` clean,
  `go test ./adapters/mqtt/...` all pass (no test needed updating —
  behavior is either unchanged or now MORE correct, no test asserted the
  old broken behavior).

### `adapters/mqtt5` — NOT YET DONE

Mirrors the same 3-call-site pattern as mqtt v3, still pending:

| File | Action needed |
|---|---|
| `adapters/mqtt5/adapter.go` | Delete `reportTopicParamErrors`/`reportMissingTopicVarErrors` (byte-identical to mqtt v3's now-deleted copies). Update subscribe-side `TopicVarsFromMessage`/`codex.DecodeVars` merge-error call sites (currently generic already, per O3 — just needs the private-helper PUBLISH-side call sites updated to match) and the publish-side `BuildTopic` error call site (currently calls the private helpers — needs to switch to `stats.ReportErrors(obs, "topic_var", err)`). |
| `adapters/mqtt5/reqreply.go` | Delete `reportRouteParamErrors` (the reqreply-escape-hatch-specific single-instance helper, same redundancy class as the events trio) — replace its one call site with `stats.ReportErrors(obs, "topic_var", err)`. |
| `adapters/mqtt5/*_test.go` | Confirm no test asserts the old private-helper behavior directly (expected: none, since O1 makes the generic call produce IDENTICAL output) — spot-check before deleting. |

**Recommended next step**: mirror mqtt v3's exact edit pattern (already
proven, tested, verified this round) — same call-site tracing rigor,
same verification order (`gofmt`/`go build`/`go test` on the touched
package only, then a repo-wide sweep once all adapters are done).

## O6 — DEFERRED — `TopicMismatchError` has no shared reporting mechanism

**Status: NOT investigated further this round — still open.**

`adapters/mqtt` (v3) has `reportTopicMismatchErrors` (kept, since
`TopicMismatchError` is a per-adapter, non-aliased type not covered by
O1). `adapters/mqtt5` and `adapters/zeromq` have NO equivalent reporter
at all, despite `adapters/mqtt5/topicvars.go` defining a byte-identical
`TopicMismatchError` struct.

**Open question, not resolved:** (a) each adapter defines its own small
reporter (cheap, mirrors mqtt v3's existing one, no dedup possible
without type unification) vs. (b) unify `TopicMismatchError` into ONE
shared `api/events`-level type all adapters reference (bigger, BREAKING
change — every adapter's exported `TopicMismatchError` symbol would need
to become a type alias or be removed). Recommend (a) for a future round
unless a concrete driver for (b) emerges. Needs confirmation of whether
zeromq's own topic-matching model even produces an equivalent mismatch
case before implementing.

## O7 — DEFERRED — REST's 8 duplicated Observer/Diagnostics helpers (nethttp vs. chi)

**Status: NOT implemented this round — still open.**

Confirmed via `diff`: `diagnosticObserver` (type) plus
`reportBodyErrors`/`reportQueryErrors`/`reportCookieErrors`/
`reportHeaderErrors`/`reportResponseHeaderErrors`/
`reportResponseCookieErrors`/`reportPathErrors` (7 functions) are
byte-for-byte IDENTICAL between `adapters/nethttp` and `adapters/chi`.
All 8 operate ONLY on `context.Context`/`error`/`rest.*ParamError` types
— zero `net/http`-specific dependency, fully portable to `api/rest`.

**Also confirmed, a REST-specific analogue of O1** (NOT fixed by O1,
since REST's `reportPathErrors` routes through the SEPARATE
`stats.RecordDiagnostic` ctx-ferry mechanism, not `RecordValidationError`
directly): `reportPathErrors` only unpacks `rest.PathParamError`, never
`MissingPathVarError`/`InvalidPathParamError` — confirmed REACHABLE via
`nethttp.Call`'s client-side `handle.BuildPath(vars)` (a caller
forgetting a required path var produces `MissingPathVarError` with ZERO
`Diagnostic` reported, though the correct error IS still returned).

**Proposed fix** (not yet implemented): move all 8 symbols into a new
`api/rest/observability.go` (mirrors `api/reqreply/observability.go`'s
existing precedent for the api layer importing `stats`) as exported
symbols; delete `chi`'s copies entirely; decide whether `nethttp` keeps
its own copies calling straight through to the new exports, or deletes
its copies too and calls the `api/rest` exports directly (open question
— affects whether `nethttp`'s `adapter.go` changes at all). While moving,
extend `reportPathErrors`'s REST-side equivalent to also unpack
`MissingPathVarError`/`InvalidPathParamError` (mirrors O1's coverage for
the Diagnostics-ferry mechanism specifically).

## O8 — DEFERRED — `firstScheme`/`firstSchemeName` duplicated across 5 adapters

**Status: NOT implemented this round — still open.**

Confirmed via `diff`: byte-for-byte identical `[]route.SecurityRequirement
→ string` extraction logic duplicated across `adapters/nethttp`,
`adapters/chi`, `adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq` (5
copies, only the name differs — zeromq calls it `firstSchemeName`).
Operates PURELY on `route.SecurityRequirement`, the core `route`
package's own type — zero adapter-specific logic.

**Proposed fix** (not yet implemented): add `route.FirstSchemeName(reqs
[]SecurityRequirement) string` to `route/security.go` (alongside the
existing `route.Satisfied` helper); delete all 5 adapter copies; update
~15+ call sites across the 5 adapters to use `route.FirstSchemeName(...)`.

## O9 — DEFERRED — needs further tracing before deciding if it's even a bug

**Status: NOT investigated further this round — flagged only.**

`adapters/mqtt5/caller.go`'s subscribe dispatch (~line 461) reports the
wrapped HANDLER's own business-logic return error via
`stats.ReportErrors(obs, "topic_var", fnErr)`. Initially flagged as a
possible location-string mislabeling bug — but while implementing O5's
mqtt v3 cleanup, found the SAME pattern at a mqtt v3 call site
(`adapter.go`, `if err = fn(ctx, value); err != nil { ... }`), which
calls ALL FOUR topic-var-family reporters against the handler's OWN
returned error. This is more likely INTENTIONAL: the framework
defensively checks whether an application handler's returned error
happens to match one of the known typed-error shapes (e.g. a handler
choosing to return a `TopicParamError` itself to signal a business-level
topic-var problem), not a copy-paste mistake. **Revised assessment: likely
NOT a bug, but not confirmed either way — needs a dedicated trace (check
whether any handler in practice/tests ever returns one of these types)
before deciding whether to keep, rename the location string, or remove.**

## Verification (already-shipped portion)

`gofmt -l .` clean, `go build ./...` clean,
`go test ./stats/... ./adapters/mqtt/... ./adapters/mqtt5/...
./adapters/zeromq/... ./adapters/nethttp/... ./adapters/chi/... ./route/...`
all pass (zero regressions from O1 + O5's mqtt v3 slice).

## Verification (once remaining work ships)

`gofmt -l .`, `go build ./...`, `go test -count=1 ./...`, `just check`,
full `for d in examples/*/; do go run ./$d; done` sweep — same order as
the `review-go-codex` skill's Phase 6. Update
`.github/instructions/go-codex.instructions.md` for `stats.ReportErrors`'s
expanded handled-types list (already shipped, doc update still pending)
and any new exported symbols from O7/O8 once implemented.

## Related, separately tracked

- [D-0002](../design/d-0002-pubsub-workflow-simplification.md)'s own
  Addendum ("Observability Core Consolidation, `SecurityFunc`
  Retirement, and `examples/events-api`") — the SAME "thin adapter, thick
  api layer" principle applied to the `Observability[T]` decorator family
  (a different Observer mechanism — declare-time general-purpose
  middleware — from this doc's `stats.ReportErrors` param-error focus).
  **✅ SHIPPED** — that Addendum's `SecurityFunc`/`CredentialFunc` removal
  has landed, including on `adapters/mqtt5/adapter.go` and
  `adapters/mqtt/adapter.go` — the file-overlap concern previously
  flagged here is now moot for those specific fields (they no longer
  exist). This doc's own O5 (mqtt5 slice: deleting the now-redundant
  private Param-error unpacker helpers in `adapters/mqtt5`) remains
  separately pending — re-verify current file state before implementing,
  since that removal already changed `adapters/mqtt5/adapter.go`'s shape.
- [Feature — sealed, per-adapter capability interfaces](protocol-native-features.md)'s
  §7 "[Review-12]" open item — flags whether every `Capability` should
  carry its own observable declaration hook, a related but DISTINCT
  question from this doc's param-error-reporting focus (capability
  runtime-effect observability vs. param validation-error observability).
