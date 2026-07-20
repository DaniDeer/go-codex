# Merge-Field Remaining Gaps — SSE, shared template-matching core, MCP, test hygiene

> **Status:** G2, G3, G4a SHIPPED. G1 and G4b remain deferred — no concrete
> use case has appeared for either, per this round's resolved design
> decisions (see "Open design decisions" below); do not build speculatively.
> [← Back to Roadmap](index.md)
>
> This roadmap doc supersedes `docs/roadmap/merge-field-port-adapter-gaps.md`
> (deleted — that doc's G1-G4 all shipped: `ports.Pattern` binding-layer
> per-item vars derivation, `adapters/zeromq` pub/sub merge wiring,
> `adapters/mqtt` v3 events merge wiring, and the stale checklist fix; see
> `.github/instructions/go-codex.instructions.md`'s "Declarative Var
> Extraction & Merge" section and `docs/features/rest-api.md`/`events.md`/
> `ports.md` for the shipped, authoritative API reference). This doc
> captures the two items the prior doc explicitly deferred (G5, G6 — now
> renumbered G1/G2 here) plus new items found while auditing that round's
> implementation against the actual codebase.

## Motivation

A review of the shipped merge-field-port-adapter-gaps round against the
current codebase confirms G1-G4 are correctly implemented and documented —
no regressions, no stale references remain (verified via repo-wide grep).
Two items that round explicitly deferred (SSE merge support, shared
topic-template-matching code) are still open, and two new items surfaced
while re-reading the implementation closely:

1. A FOURTH near-duplicate topic/path template-matching implementation was
   found (`ports/file.go`'s `matchFileTemplate`) — the old doc's G6 only
   counted three (`mqtt`, `mqtt5`, `zeromq`). All four exist for the exact
   same reason: `api/internal.MatchTemplate` cannot be imported outside
   `api/*` (Go's `internal/` visibility rule), and no package importable
   from `api/*`, `adapters/*`, AND `ports/` simultaneously exists today.
2. A PRE-EXISTING, unrelated data race was found in
   `adapters/mqtt/binding_test.go`'s `TestSubscribeAdapter_AutoDerivesWildcardFilter`
   (confirmed present before the merge-field-port-adapter-gaps round via
   `git stash` — not caused by that work, but surfaced while running
   `-race` on the touched package).
3. `api/mcp` (Resources/Prompts) was assessed for the first time (previously
   listed as "unassessed, out of scope" in the superseded doc) — see G4
   below. The finding is MORE BASIC than a merge-field gap: MCP Resources
   don't even have automatic URI-var EXTRACTION/VALIDATION today (a
   Round-0/pre-map-validation maturity level, one step behind REST/events/
   reqreply's ORIGINAL, pre-merge-field map-based stage), while MCP Prompts
   DO have automatic map validation but no merge-into-struct convenience.

## Scope decisions

| Gap | Boundary | Severity | In scope this round | Status |
|---|---|---|---|---|
| G1 (was G5) | `rest.SSERouteHandle` has zero merge support (no `MergeFields`/`DecodeMerged` for the pushed `Event` type) | `api/rest` (SSE), `adapters/nethttp` | Low | Resolved as deferred — no concrete SSE+query-param merge use case has appeared; see "Open design decisions" (resolved) | Deferred, not implemented |
| G2 (was G6) | Four near-identical topic/path template-matching implementations (`adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq`, `ports/file.go`) — none can share `api/internal.MatchTemplate` (import-visibility) | `adapters/mqtt`, `adapters/mqtt5`, `adapters/zeromq`, `ports` | Low | Yes | ✅ SHIPPED — new `internal/templatematch` package (`MatchNonWildcard`/`MatchMQTTWildcard`), all four duplicates (plus `api/internal.MatchTemplate` itself) now delegate to it |
| G3 (new) | Pre-existing data race in `adapters/mqtt/binding_test.go`'s `TestSubscribeAdapter_AutoDerivesWildcardFilter` (mockClient fields read/written across goroutines without synchronization) | `adapters/mqtt` (test-only) | Low — test-only, does not affect production code correctness | Yes — trivial, mechanical fix (add a mutex to `mockClient`, mirroring the pattern `adapters/mqtt5`'s `mockClient` already uses) | ✅ SHIPPED — `mockClient` now has a `sync.Mutex` + snapshot accessor methods; `go test -race` clean |
| G4 (new — assessed) | `api/mcp` Resources have ZERO automatic URI-var extraction/validation (`ResourceHandlerFunc` receives only the raw `req.Params.URI` string; `ResourceHandle.ValidateURIVars` exists but is never called by `adapters/mcpgo`); `api/mcp` Prompts DO auto-validate (`PromptHandler` calls `ValidateArgs`) but hand the app a raw `map[string]string`, not a merged typed struct | `api/mcp`, `adapters/mcpgo` | **Medium for Resources** (a real, user-visible gap independent of merge-fields — apps must hand-roll URI parsing today); **Low for Prompts** (works today via the map, just one maturity level short of the merge-field convenience) | G4a: yes (concrete, scoped win). G4b: resolved as deferred — not recommended without a demonstrated use case | G4a ✅ SHIPPED (`ResourceHandle.ExtractURIVars`, new `ResourceURIMismatchError`, `mcpgo.ResourceVarsHandlerFunc`/`ResourceHandlerWithVars`/`RegisterResourceWithVars` — additive, `ResourceHandlerFunc`/`ResourceHandler`/`RegisterResource` unchanged). G4b deferred, not implemented |

## API surface

**G1 — SSE merge support** (sketch only; NOT resolved which shape to take — see
Open design decisions):

```go
// api/rest/builder.go — SSERouteHandle would need an Event-side merge
// accessor, e.g.:
func (h *SSERouteHandle[Req, Event]) MergeFields() []codex.FieldCodec[Event]
func (h *SSERouteHandle[Req, Event]) DecodeMerged(payload []byte, vars map[string]string) (Event, error)

// adapters/nethttp — SSEAdapter/RegisterSSE would derive vars from the
// CONNECTION's Req (path+query at subscribe time, e.g. {machineID} in the
// path) ONCE per connection and merge them into EVERY Event pushed over
// that connection — NOT per-event, since SSE has no per-message topic the
// way MQTT/ZeroMQ pub/sub does. This is architecturally different from
// events/reqreply's per-message merge and needs its own design pass.
```

**G2 — shared template-matching core** (sketch only; NOT resolved WHERE the
shared code should live — see Open design decisions):

```go
// Candidate: a new top-level internal package importable from api/*,
// adapters/*, AND ports/ simultaneously — e.g. `internal/templatematch`
// at the repository root (Go's internal/ rule permits import from
// anywhere under the MODULE root, unlike api/internal which restricts to
// api/*'s own subtree).
package templatematch

// MatchNonWildcard is the shared core for zeromq/ports.File (no MQTT-style
// wildcard support needed).
func MatchNonWildcard(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error)

// MatchMQTTWildcard is the shared core for mqtt/mqtt5 (adds +/# support).
func MatchMQTTWildcard(template, concrete string, wrapMismatch func(template, concrete string) error) (map[string]string, error)
```

**G3 — mqtt test race fix** (test-only, mechanical):

```go
// adapters/mqtt/adapter_test.go — mockClient needs a sync.Mutex guarding
// publishedTopic/publishedPayload/subscribedTopic/subscribedHandler,
// mirroring adapters/mqtt5's mockClient (which already has this). Update
// every field access (test assertions + Publish/Subscribe methods) to
// lock/unlock, or add accessor methods like mqtt5's mockClient does.
```

**G4a — Resources: automatic URI-var extraction + validation** (the
concrete, recommended near-term win — NOT a merge-field feature, closes a
more basic pre-existing gap):

```go
// api/mcp/builder.go — new inverse of BuildFromTemplate, mirroring
// TopicVarsFromMessage/matchFileTemplate's shape (depends on G2's shared
// core if that lands first; otherwise a fifth local copy, same
// import-visibility constraint as G2).
func (h *ResourceHandle[T]) ExtractURIVars(uri string) (map[string]string, error)
// Matches uri against h.URITemplate, returning ResourceURIMismatchError
// (NEW error type, mirrors TopicMismatchError/FilePathMismatchError) on
// structural mismatch, then internally calls the existing
// ValidateURIVars for codec validation — one call replaces "parse the URI
// yourself" + "remember to call ValidateURIVars yourself" (the latter is
// never invoked by adapters/mcpgo today).

// adapters/mcpgo/adapter.go — ResourceHandlerFunc's signature would gain
// the extracted vars map (additive — a new function/signature, not a
// breaking change to the existing one):
type ResourceHandlerFunc[T any] func(ctx context.Context, uri string, vars map[string]string) (T, error)
// ResourceHandler's handlerFn calls ExtractURIVars BEFORE invoking fn,
// routing a mismatch/validation failure to the same RecordRequest(...,
// 500, ...) observer path decode errors already use.
```

**G4b — merge-field parity for Resources/Prompts** (assessed, NOT
recommended without a concrete use case — see Open design decisions):

```go
// Sketch only — Resources have no natural "input struct" the way REST/
// events do (T is OUTPUT content; there is no separate Req type). A
// speculative NewResourceParam-merge would need to merge vars into the
// SAME T the handler returns (mirrors events: merge topic vars into the
// decoded payload) — but unlike events, T is APPLICATION-PRODUCED, not
// wire-decoded, so "merge after the handler runs" is the only shape that
// makes sense, and it only helps if T's fields are meant to echo the URI
// vars verbatim (a narrower use case than events/reqreply's decode-merge).

// Prompts already receive args as map[string]string with validation done
// — a NewPromptArg-merge would let PromptHandlerFunc accept a typed Args
// struct instead of the map, e.g.:
type PromptHandlerFunc[Args any] func(ctx context.Context, args Args) ([]PromptMessage, error)
// This is more directly analogous to REST/events (merge INTO a fresh
// struct from a map, same direction as codex.DecodeVars) than G4a's
// Resources case — a more promising candidate IF a use case appears.
```

## Structured errors / Observer integration

No new error types or observer methods anticipated for G1-G3 — G1 would
reuse `codex.VarEncodeTypeError`/`ValidationErrors` exactly as
REST/events/reqreply already do; G2 is a pure internal refactor (existing
`TopicMismatchError`/`FilePathMismatchError` types stay, only the matching
algorithm's location changes); G3 is test-only. G4a needs ONE new error
type: `ResourceURIMismatchError` (mirrors `TopicMismatchError`/
`FilePathMismatchError` exactly — `Template`/`URI` fields, `Error()`,
`LogValue()`); reuses `ResourceParamError`/`MissingResourceVarError` for
the validation step (already exist, currently unused by any adapter
wiring). G4a's observer path reuses `RecordRequest("resource", ...)`
exactly as `ResourceHandler` already does for decode/encode errors — no
new observer method. G4b, if pursued, would need no new error types either
(reuses the same `ResourceParamError`/`PromptArgError` family).

## Unit test plan

| ID | Test |
|---|---|
| G1-1 | (deferred until design resolved) `SSERouteHandle.DecodeMerged` happy path + mismatch |
| G1-2 | (deferred until design resolved) SSE connection merges path/query vars into every pushed Event |
| G2-1 | (deferred until design resolved) shared `templatematch` core produces IDENTICAL results to the current `mqtt`/`mqtt5`/`zeromq`/`ports/file` implementations across the existing test matrices (regression guard — run old + new side by side before deleting any duplicate) |
| G3-1 | `go test -race ./adapters/mqtt/...` passes clean after `mockClient` gets a mutex |
| G4a-1 | (deferred until design resolved) `ResourceHandle.ExtractURIVars` happy path + `ResourceURIMismatchError` on structural mismatch + `ResourceParamError`/`MissingResourceVarError` on codec/missing-var failure |
| G4a-2 | (deferred until design resolved) `mcpgo.ResourceHandler`'s new-signature `fn` receives the extracted vars map; a template with NO `{var}` placeholders behaves identically to today (regression guard, empty vars map) |
| G4b | (deferred — not recommended without a concrete use case; no tests planned this round) |

## Files to create/change

| File | Responsibility |
|---|---|
| G1 | NOT implemented — deferred, no files touched |
| `internal/templatematch/templatematch.go` + `_test.go` (new, repo-root-level) | ✅ G2: shared matching core (`MatchNonWildcard`, `MatchMQTTWildcard`) |
| `api/internal/template.go` | ✅ G2: `MatchTemplate` now delegates to `templatematch.MatchNonWildcard` |
| `ports/file.go` | ✅ G2: `matchFileTemplate` now delegates to `templatematch.MatchNonWildcard` |
| `adapters/zeromq/topicvars.go` | ✅ G2: `matchTopicTemplate` now delegates to `templatematch.MatchNonWildcard` |
| `adapters/mqtt/topicvars.go`, `adapters/mqtt5/topicvars.go` | ✅ G2: `matchTopicTemplate` now delegates to `templatematch.MatchMQTTWildcard` |
| `adapters/mqtt/adapter_test.go` + `binding_test.go` | ✅ G3: `mockClient` mutex + snapshot accessors; all call sites updated |
| `api/mcp/builder.go`, `api/mcp/errors.go` + `builder_test.go` | ✅ G4a: `ResourceHandle.ExtractURIVars`, new `ResourceURIMismatchError` |
| `adapters/mcpgo/adapter.go` + `adapter_test.go` | ✅ G4a: new additive `ResourceVarsHandlerFunc`/`ResourceHandlerWithVars`/`RegisterResourceWithVars` |
| `docs/features/mcp.md` | ✅ G4a: documented `ExtractURIVars`/`RegisterResourceWithVars` |
| `docs/reference/project-structure.md` | ✅ G2: documented new `internal/templatematch` package |
| G4b | NOT implemented — deferred, no files touched |
| this doc | marked partially SHIPPED (G2/G3/G4a); G1/G4b remain deferred |

## Open design decisions (resolved during implementation)

1. **G1 — is per-CONNECTION merge (not per-event) actually useful?**
   RESOLVED as deferred — no concrete use case appeared; not implemented
   this round.
2. **G1 — Req-side merge for SSE already exists via `BuildPath`/
   `ValidatePathParams`; is Event-side merge even the right feature?**
   RESOLVED as leaned — SSE's existing capabilities (closures exposing the
   resolved `Req` to the event-producing goroutine) are sufficient; no new
   API added.
3. **G2 — where should the shared core live?** RESOLVED as leaned —
   `internal/templatematch` at the repository root. `api/internal.MatchTemplate`,
   `ports/file.go`'s `matchFileTemplate`, `adapters/zeromq`'s
   `matchTopicTemplate`, `adapters/mqtt`'s `matchTopicTemplate`, and
   `adapters/mqtt5`'s `matchTopicTemplate` all now delegate to it.
4. **G2 — is the wildcard/non-wildcard split the right factoring?**
   RESOLVED as leaned — implemented as two functions,
   `MatchNonWildcard` (REST/file/MCP/ZeroMQ) and `MatchMQTTWildcard`
   (mqtt/mqtt5), each preserving its consumers' EXACT prior algorithm
   (regex-based partial-segment capture for the former, split-based
   whole-segment-only capture with `+`/`#` support for the latter — these
   are genuinely different algorithms, not just "the same thing plus
   wildcards").
5. **G3 — is `sync.Mutex` + explicit lock/unlock the right fix?** RESOLVED
   as leaned — `adapters/mqtt`'s `mockClient` gained a `sync.Mutex` plus
   snapshot accessor methods (`subscribedTopicSnapshot`,
   `subscribedHandlerSnapshot`, `publishedTopicSnapshot`,
   `publishedPayloadSnapshot`, `publishedTopicsSnapshot`), mirroring
   `adapters/mqtt5`'s mock pattern; every test call site updated to use the
   accessors instead of bare field reads. `go test -race` is clean,
   confirmed stable across 5 repeated runs.
6. **G4a — is a NEW `ResourceHandlerFunc[T]` signature the right shape?**
   RESOLVED as leaned, but implemented as a NEW additive type +
   functions rather than changing the existing signature in place:
   `ResourceVarsHandlerFunc[T]` (new func type), `ResourceHandlerWithVars`/
   `RegisterResourceWithVars` (new functions) — `ResourceHandlerFunc`/
   `ResourceHandler`/`RegisterResource` are completely UNCHANGED, so no
   backward-compatibility question needed resolving after all (both paths
   coexist; callers choose per-resource).
7. **G4b — is full merge-field parity for Resources/Prompts worth building
   at all?** RESOLVED as leaned — deferred for BOTH Resources and Prompts;
   not implemented this round. No concrete user request has appeared.

## Verification

Same ritual as every prior round: gofmt, `go build ./...`, `go test
./...`, `-race` on touched packages (this round's own goal for G3 is
exactly a clean `-race` run on `adapters/mqtt`), `just check`, all
examples exit 0.
