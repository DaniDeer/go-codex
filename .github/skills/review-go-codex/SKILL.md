---
name: review-go-codex
description: >
  Systematic consistency review of the go-codex library: codecs (Layer 1), REST/event API contracts
  (Layer 2), and forge pipelines (Layer 3). Use when asked to "review go-codex", "consistency audit",
  "check for inconsistencies", "simple workflow review", "audit API surface", or after a significant
  round of refactoring. Reviews cross-layer naming parity, param types, builder methods, error shapes,
  structured errors, observer pattern, unit test coverage, and example correctness.
---

# review-go-codex

Systematic consistency audit of the go-codex library across all three layers. Reads key source files,
applies the checklist in `references/checklist.md`, collects findings, categorises by severity, and
plans a fix round.

## User Experience North Star

Every finding and fix must be evaluated against one question:

> **Does this make the library more declarative, simple, and consistent for the user?**

The three layers form a single workflow. A user should be able to move between them without context
switching or learning a new mental model:

| Layer                      | User action                      | How it should feel                                                     |
| -------------------------- | -------------------------------- | ---------------------------------------------------------------------- |
| **Layer 1 — Codec**        | Define `codex.Codec[T]`          | Declare shape + constraints once; derive encode/decode/schema for free |
| **Layer 2 — API contract** | `NewRoute` / `NewChannel`        | Declare the contract as a value; pass it around; register it anywhere  |
| **Layer 3 — Pipeline**     | `forge.NewFunction` + `Registry` | Declare computation contracts; compose; register; govern               |

The workflow across layers is always: **declare → compose → register**. If a finding breaks this
pattern — forces the user to use imperative calls, repeat themselves, or learn layer-specific
vocabulary — it is at minimum a `small` finding.

Concrete implications for every review:

- **Declarative**: API objects (`RouteHandle`, `ChannelHandle`, `FunctionSpec`) are values, not
  builder side-effects. Users pass and store them. No magic, no global state.
- **Simple**: one method does one thing. Avoid overloaded methods. Param defaults should be safe.
- **Consistent**: same concept, same name. `Meta` structs, `Opt` interfaces, `Builder`/`Registry`
  fluent methods, error type shapes — all should follow the same pattern across layers. If Layer 2
  has `WithCodec`, Layer 2 events should have it too; if Layer 3 adds governance fields, their
  names should mirror Layer 2's `Meta` naming.

## When to Use This Skill

- User says "review go-codex", "consistency audit", "check go-codex API", "audit"
- User says "consistent, declarative, simple workflow"
- After a significant feature addition or refactoring round
- When something "feels off" but the user cannot pinpoint it

## Step-by-Step Workflow

### Phase 1 — Read key files in parallel

Read all of these before opening any finding:

| File                                            | Why                                                         |
| ----------------------------------------------- | ----------------------------------------------------------- |
| `api/rest/builder.go`                           | Layer 2 REST: all param types, builder methods, error types |
| `api/events/builder.go`                         | Layer 2 events: ChannelHandle, TopicParam, Builder          |
| `forge/forge.go`                                | Layer 3: PipelineInfo, FunctionMeta, Registry, error types  |
| `render/pipeline/pipeline.go`                   | Pipeline YAML renderer                                      |
| `render/asyncapi/v3/document.go`                | AsyncAPI renderer                                           |
| `render/openapi/openapi.go`                     | OpenAPI renderer                                            |
| `adapters/nethttp/handler.go`                   | Adapter: observer calls, security enforcement               |
| `adapters/chi/handler.go`                       | Adapter: same as nethttp                                    |
| `adapters/mqtt/handler.go`                      | Adapter: subscribe/publish, observer calls                  |
| `.github/instructions/go-codex.instructions.md` | Design contract and prior decisions                         |

Also scan: `api/rest/*_test.go`, `api/events/*_test.go`, `forge/*_test.go`, `render/pipeline/*_test.go`

Then read `references/history.md` to see what was already fixed in R1–R10. **Do not re-report these.**

### Phase 2 — Apply the checklist

Work through `references/checklist.md` section by section:

1. Cross-layer naming parity
2. Param type consistency
3. Builder method parity
4. Codec field godoc
5. Format API parity
6. Forge consistency
7. Error sentinel consistency (structured errors)
8. Observer pattern
9. Unit test coverage
10. Example correctness

### Phase 3 — Record findings

For every issue found, assign:

| Field     | Values                           |
| --------- | -------------------------------- |
| ID        | `G<N>` (sequential)              |
| Severity  | `bug` / `small` / `trivial`      |
| Category  | one of the 10 checklist sections |
| File:line | exact location                   |
| Problem   | one sentence                     |
| Fix       | one sentence                     |

Present findings as a table, then group by priority: bugs first, then small, then trivial.

### Phase 4 — Produce a plan

Write findings and their priority order to the session plan.md. Do NOT start implementing until the
user confirms the plan or says "start" / "get to work".

### Phase 5 — Implement (after approval)

For each finding, in priority order:

1. Apply the fix
2. Run `go fmt ./...` — format immediately after each edit
3. Run `go build ./...` (must stay clean)
4. Run `go test ./...` (all packages must pass)
5. Run `just check` (staticcheck + gosec — no new warnings)
6. Update `.github/instructions/go-codex.instructions.md` if any exported API changed

After all fixes:

- Run every example: `for d in examples/*/; do go run ./$d; done` — each must exit 0
- If any example uses a stale pattern, update it to match the current API

### Phase 6 — Verify

Run in this order — each must be clean before proceeding to the next:

```bash
go fmt ./...          # format all files; no diff should remain
go build ./...        # must compile with zero errors
go test ./...         # all packages must pass
just check            # staticcheck + gosec; no new suppressions allowed
```

If `just check` surfaces a warning introduced by your changes, fix it before proceeding.
Do not add `//nolint` or `//gosec` suppressions to silence new findings.

### Phase 7 — Update History

Before producing the commit summary, append a new section to
`references/history.md` for this round. Follow the existing format:

```markdown
## Round <N> (<short title>)

- **<ID> — <finding title>**: one-sentence description of what was wrong and how it was fixed.
- ...

---
```

Rules:

- Include only findings that were actually implemented (not deferred/skipped).
- One bullet per finding; keep the description concise and factual.
- Insert the new section **above** Round <N-1> (newest round at top of file, below the header).

### Phase 8 — Commit Summary

After updating history.md, produce a commit-ready summary. **Do not commit** — present this to
the user and let them decide.

Format:

```
<short imperative title — max 72 chars>

Layer(s) affected: <Layer 1 / Layer 2 REST / Layer 2 events / Layer 3 forge>
Round: R<N>

Findings fixed:
- <ID> [<severity>] <one-line description of fix>
- ...

Tests: <N> new / updated test(s) in <package(s)>
Examples: <"no changes needed" | list of updated examples>
Docs: .github/instructions/go-codex.instructions.md updated

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>
```

Rules:

- Title must be imperative: "Fix ValidateTopicVars missing key check", not "Fixed…"
- List only findings that were actually implemented, not skipped or deferred
- If zero examples changed, write "Examples: no changes needed"
- Always include the `Co-authored-by` trailer

## Structured Errors Guardrail

go-codex uses typed errors, not bare strings. Check every error return site:

- **Adapters** must return typed errors: `rest.PathParamError`, `rest.QueryParamError`,
  `rest.HeaderParamError`, `rest.CookieParamError`, `rest.MissingPathVarError`,
  `rest.SecurityCredentialError`, `rest.SecurityError`.
- **events adapter** must return: `events.TopicParamError`, `events.MissingTopicVarError`.
- **forge** must return: `forge.InputError`, `forge.OutputError`, `forge.ApplyError`,
  `forge.RefinementError`.
- `fmt.Errorf("...")` wrapping a structured error is fine; a bare `fmt.Errorf` without wrapping a
  typed sentinel is a finding.
- Callers must be able to `errors.As` / type-switch on every error for structured logging.

## Observer Pattern Guardrail

`stats.Observer` has four interfaces; adapters must call them correctly:

| Interface                  | Who calls it                  | When                                                      |
| -------------------------- | ----------------------------- | --------------------------------------------------------- |
| `stats.ValidationObserver` | codecs (internal)             | on codec validation failure                               |
| `stats.Observer`           | adapters (nethttp, chi, mqtt) | on decode/encode start+finish, errors                     |
| `stats.PipelineObserver`   | forge `Registry.Apply`        | on each function apply                                    |
| `stats.SecurityObserver`   | adapters                      | on security rejection — **type-asserted, never embedded** |

Rules:

- `SecurityObserver` must be guarded: `if so, ok := obs.(stats.SecurityObserver); ok { so.RecordSecurityRejection(...) }`
- `PipelineObserver.RecordApply` must be called for every function in a pipeline, including
  wrapped collection ops (Map, Filter, etc.)
- Adapter `Observer` must be called on every code path, including early-exit error paths

## Unit Test Coverage

For each exported type or method added, there must be at least one test covering:

- Happy path (valid input → expected output)
- Error path (invalid input → correct typed error)
- For builder methods: that the method returns the right shape (field set correctly)

Spot-check:

```bash
grep -n "func Test" api/rest/builder_test.go api/events/builder_test.go forge/forge_test.go render/pipeline/pipeline_test.go
```

If a new exported symbol has no corresponding `TestX` function, file a `trivial` finding.

## Example Correctness

Every `examples/*/main.go` must:

1. Build cleanly: `go build .`
2. Demonstrate the feature its directory name implies
3. Use current API (no stale patterns):
   - Must use `.WithCodec(codec)` not `Codec: &codec`
   - Must use `NewRoute` / `NewSSERoute` / `NewChannel` declarative constructors
   - Must not call deleted or renamed methods

Run them all:

```bash
for d in examples/*/; do
  echo "=== $d ===" && cd $d && timeout 5 go run . &
  cd - >/dev/null
done
wait
```

If an example panics or uses a stale pattern, file a finding.

## Gotchas

- **Do not re-report R1-Rxx items.** See `references/history.md` for the full list of what is already fixed.
- **`FunctionKindScalar` is `""` (empty string).** `NewFunction`/`Compose` never write `Kind` — scalar functions have `Kind==""` by design. The `render/pipeline` renderer omits `kind:` for scalar. This is correct.
- **`rest.Builder.AddServer` discards `name` after description fallback.** OpenAPI servers are a keyless ordered array. `events.Builder.AddServer` stores the name (AsyncAPI servers are keyed). Same call site, different semantics.
- **`PathParam` and `TopicParam` have no `Required` field.** This is by design — OpenAPI mandates path params are always required; topic vars must always be present. Godoc explains this.
- **`SecurityScheme` codec is spec-only.** No adapter enforces it unless `SecurityFunc` does so explicitly.
- **`SubscribeFormats` / `PublishFormats` take priority over `Formats`.** Adapters must check these before falling back to `handle.Formats`.
- **Do not invent new API surface** during a review. Findings should fix inconsistencies in existing API, not design new features.
- **Update `go-codex.instructions.md` after every code change.** The instructions file is the single source of design truth — it must stay in sync.

## References

- [`references/history.md`](references/history.md) — findings fixed in Rounds
- [`references/checklist.md`](references/checklist.md) — full cross-layer consistency checklist
- [`.github/instructions/go-codex.instructions.md`](../../instructions/go-codex.instructions.md) — design contract
