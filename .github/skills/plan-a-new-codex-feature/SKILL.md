---
name: plan-a-new-codex-feature
description: 'Planning skill for new go-codex features. Follows a roadmap-first workflow: every idea starts as docs/roadmap/<feature>.md and is refined before implementation. Covers structured errors with slog.LogValuer, Observer pattern for metrics/logging/tracing, unit test coverage, and three-surface documentation sync. Use when asked to "plan a new feature", "evaluate possibility of X", "add to go-codex", "design a new API", "plan a new adapter", or before writing any new exported symbol.'
---

# plan-a-new-codex-feature

Planning guide for new go-codex features. Every feature starts as a **living design
document** in `docs/roadmap/` and is refined over time before implementation.
This separates exploration (cheap, reversible) from implementation (expensive,
harder to change).

---

## When to Use This Skill

Three distinct modes — identify which before proceeding:

| Trigger | Mode | Output |
|---------|------|--------|
| "evaluate X", "assess possibility", "plan a new adapter/feature", "can go-codex do X" | **Explore** | `docs/roadmap/<feature>.md` |
| "review the X plan", "refine", "take Y into account", "update the roadmap doc" | **Refine** | Update existing `docs/roadmap/<feature>.md` |
| "implement it", "start", "now implement what we planned in roadmap" | **Implement** | `plan.md` + todos + code (against mandatory requirements) |

**Never skip the roadmap doc.** Even for features that will be implemented
immediately, the roadmap doc captures design intent, rejected alternatives, and
scope decisions — this is the institutional memory of why the API looks the way it does.

---

## Mode 1 — Explore: Write a Roadmap Doc

### What a roadmap doc is

`docs/roadmap/<feature>.md` is a **living design document**:
- Not a spec frozen at a point in time — it is updated as the design evolves
- Not a commit message — it captures the *why*, rejected alternatives, and scope
- Not a TODO list — it captures API surface, error model, observer hooks, test plan
- Survives across sessions: the next session can pick up where this one left off

### Step-by-step

**Phase 1 — Research**

Read the relevant packages and how similar features are built:

| What to read | Why |
|---|---|
| The adapter/package being extended | Current API surface and patterns |
| `.github/instructions/go-codex.instructions.md` | Import rules, design constraints |
| `adapters/mqtt5/binding.go` — reference binding pattern | Pattern for new transport adapters (SourceAdapter, SinkAdapter, IOAdapter) |
| `adapters/mqtt5/adapter.go` — non-stream adapter functions | Standalone functions to keep; binding.go wraps these |
| `ports/` package | Adapter interfaces and port types that bindings must implement |
| External library being wrapped (if any) | Capabilities and limitations |

Background research agents are appropriate here for complex external libraries
(e.g. RxGo, AMQP protocol, TCP framing). Always evaluate:
- Is the external library actively maintained?
- Does it preserve go-codex's type safety (no `interface{}` boxing)?
- Does the proposed feature map naturally to the three-layer model?

**Phase 2 — Write the roadmap doc**

Create `docs/roadmap/<feature-name>.md` following this template:

```markdown
# <Feature Title> — `<package path>`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation
Why this feature belongs in go-codex. The concrete user problem it solves.
One paragraph.

## Scope decisions (what's in Phase 1, what's deferred)
| In scope | Out of scope |
|---|---|
| ... | ... |

## Toolchain / dependency decisions (if applicable)
Why this library was chosen, what was rejected and why.

## API surface
Exact Go signatures — not pseudocode. Use correct generics syntax.
Include godoc sketches for Options structs.

## Structured errors (all implement `slog.LogValuer`)
Every new error type with:
- Fields (typed)
- `Error() string` return value
- `LogValue()` attribute group

## Observer integration
Which stats.Observer extension is used (FileObserver / SQLObserver / etc.)
Which methods fire and when (success path, error paths).
Type-assertion guard pattern.

## Unit test plan
Table of test IDs, names, and what each verifies.
Minimum: happy path, error path, observer called, LogValue shape.

## Files to create
| File | Responsibility |
|---|---|
| ... | ... |
| `adapters/<transport>/binding.go` | Port adapter bindings: `SourceAdapter`, `SinkAdapter`, `IOAdapter` implementations; all transport-to-port wiring |

## Out of scope (Phase 2)
Deferred capabilities, with rationale.

## Open design decisions (to resolve before/during implementation)
Questions that remain open, with trade-offs noted.
```

**Phase 3 — Update index and nav**

1. Add row to `docs/roadmap/index.md` table
2. Add entry to `zensical.toml` under `[nav.Roadmap]`
3. Verify `go build ./...` still passes (no Go code changed, but importing the page shouldn't break anything)

**Phase 4 — Present and await direction**

Present a concise summary. The user will either:
- Say "looks good, keep it as a roadmap doc" → done for this session
- Say "let's refine X" → update the roadmap doc
- Say "implement it" → switch to **Mode 3 — Implement**

---

## Mode 2 — Refine an Existing Roadmap Doc

1. Read `docs/roadmap/<feature>.md` fully
2. Read any context the user provided (new requirements, constraints, patterns to include)
3. Update the relevant sections — never delete; add to "Scope decisions" or "Open design decisions" if the change is significant
4. Note what changed and why (a sentence in the relevant section)

---

## Mode 3 — Implement: Translate Roadmap to Code

### Pre-implementation checklist

Before writing any code:

1. Read `docs/roadmap/<feature>.md` — this is the authoritative design
2. Resolve any remaining "Open design decisions" — pick an answer and document it in the roadmap doc
3. Check the roadmap doc's "Unit test plan" — use it to write the test matrix in `plan.md`
4. Verify all **five mandatory requirements** (below) are addressed in the roadmap doc

### Five mandatory requirements

Every new feature implementation **must** satisfy all five before it is considered complete.

#### 1. Structured Errors with `slog.LogValuer`

Every new error type **must** implement `Error()`, `Unwrap()`, and `LogValue()`.
Pattern from `codex/errors.go` and `format/embedded.go`:

```go
type MyNewError struct {
    Boundary string  // the named boundary (table, path, topic, etc.)
    Op       string  // the operation (insert_user, read, etc.)
    Err      error
}

func (e MyNewError) Error() string {
    return fmt.Sprintf("pkg: %s (%s): %v", e.Boundary, e.Op, e.Err)
}

func (e MyNewError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MyNewError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("boundary", e.Boundary),
        slog.String("op", e.Op),
        slog.Any("err", e.Err),
    )
}
```

Users get structured logging for free:
```go
slog.Error("operation failed", "error", myErr)  // LogValue fires automatically
```

**`errors.As` chain**: callers must be able to reach the inner error. `Unwrap()` is
mandatory whenever `Err error` is a field.

#### 2. Observer Pattern

Determine which `stats.Observer` extension applies:

| Layer | Interface | Methods | Type-assertion guard |
|---|---|---|---|
| Format / File I/O | `stats.FileObserver` | `RecordFileRead`, `RecordFileWrite` | ✅ always guard |
| SQL adapter | `stats.SQLObserver` | `RecordValidation`, `RecordMigration` | ✅ always guard |
| REST / MQTT / ZeroMQ | `stats.Observer` | `RecordRequest`, `RecordSubscribe`, `RecordPublish` | embedded — no guard |
| Forge pipelines | `stats.PipelineObserver` | `RecordApply` | type-asserted in `Registry.WithObserver` |
| Security rejection | `stats.SecurityObserver` | `RecordSecurityRejection` | ✅ always guard |
| Distributed tracing | `stats.TraceObserver` | `StartSpan`, `EndSpan` | ✅ always guard |

**New observer extension for new adapters:** If the feature introduces a new kind of
lifecycle event not covered by existing interfaces, add a new optional extension to
`stats/observer.go` following the `SQLObserver` pattern:
- Interface goes in `stats/observer.go`
- `NoopObserver`, `LoggingObserver`, `fanout` all implement it
- Compile-time assertion in `stats/observer_test.go`

Rules (non-negotiable):
- **For functions with a direct `ctx context.Context` parameter**: `if obs == nil { obs = stats.ObserverFromContext(ctx) }` — NOT `NoopObserver{}`. `ObserverFromContext` returns `NoopObserver{}` automatically when no context observer is stored, so behaviour is identical for callers that don't use `stats.WithObserver`.
- **For constructor functions that return closures** (e.g. an `http.Handler` factory): resolve `obs` inside the closure from the request/call context (`r.Context()` for HTTP, the tool-call ctx for MCP). This enables per-request observer injection via middleware.
- **Exception — functions without `ctx`**: use `NoopObserver{}` directly (e.g. `sql.Validate`). Document the limitation.
- **`ports.File`**: use two-step guard: `if obs == nil && opts.Context != nil { obs = stats.ObserverFromContext(opts.Context) }` then `if obs == nil { obs = stats.NoopObserver{} }`.
- Observer fires on **every** code path — success AND every error branch
- `stats.ReportErrors(obs, "location", err)` propagates `ValidationErrors` per-field
- Location string convention: `"sql_row"`, `"file"`, `"body"`, `"payload"`, `"topic_var"`, `"input"`, `"env"`
- **`forge.Registry.WithObserver`** — explicit builder API; do not add context observer integration to Registry

#### 3. Unit Tests

For every new exported symbol:

| Test | Required |
|---|---|
| Happy path (valid input → expected output) | ✓ |
| Error path (invalid input → typed error, correct fields) | ✓ |
| `errors.As` chain reaches inner error | ✓ for all error types |
| `LogValue()` returns `slog.KindGroup` with correct keys | ✓ for all error types |
| Observer called on success (correct args) | ✓ |
| Observer called on failure (correct error type passed) | ✓ |
| `nil` Observer → no panic | ✓ |
| Plain Observer (not implementing extension) → graceful fallback | ✓ |
| Round-trip encode/decode | ✓ for codec types |
| Example function (`Example...()`) for pkg.go.dev | ✓ for key new symbols |

**Test helper rule:** Use `codex.RequiredField(...)` / `codex.OptionalField(...)` in
test codecs — never `codex.Field[T,V]{...}` struct literals.

**Test quality rule:** Error type tests must check `slog.KindGroup` AND all field
keys — not just `Kind().String() != ""`. See `TestValidate_LogValue` in
`adapters/sql/validate_test.go` as the reference pattern.

#### 4. Documentation (three surfaces)

| Surface | File(s) | When required |
|---|---|---|
| **API instructions** | `.github/instructions/go-codex.instructions.md` | Every code change — mandatory, no exceptions |
| **Zensical feature page** | `docs/features/<feature>.md` | All user-facing features |
| **Zensical guide** | `docs/guides/<feature>.md` | Features with a step-by-step workflow |
| **pkg.go.dev** | `*/doc.go` + `Example...()` in `*_test.go` | New packages or major API additions |
| **Project structure** | `docs/reference/project-structure.md` | New directories / packages |
| **Roadmap index** | `docs/roadmap/index.md` | Remove row when feature ships |
| **Nav** | `zensical.toml` | Remove roadmap entry; add feature/guide entries |

When a feature ships:
- Remove `docs/roadmap/<feature>.md` (or keep as design history — user decides)
- Remove from `docs/roadmap/index.md` table
- Remove from `zensical.toml` roadmap nav
- Add to `docs/features/` and `docs/guides/` and `zensical.toml` features/guides nav

#### 5. Example Update

If the feature is significant enough for the Zensical site, it should also have a
runnable example or be demonstrated in an existing example:

- Update or create `examples/<feature>/main.go`
- The example must run without errors: `go run ./examples/<feature>/`
- Comments must explain *why* (use case), not just *what* (API call)
- All examples must pass: `for d in examples/*/; do go run ./$d; done`

---

## Roadmap Doc → Implementation Checklist

When moving from roadmap to implementation, use this transition checklist:

| Step | Action |
|---|---|
| Resolve open design decisions | Pick an answer, update roadmap doc |
| Map roadmap "Unit test plan" → plan.md todos | One todo per test block |
| Map roadmap "Files to create" → plan.md todos | One todo per file |
| Check roadmap "Out of scope" is still current | Mark anything that crept in |
| Remove `> Status: Design complete — not yet implemented` from roadmap doc | Or delete the file entirely when shipped |

---

## Verification (after implementation)

Run in order — all must pass before the feature is considered done:

```bash
go fmt ./...           # format; no diff must remain
go build ./...         # zero compile errors
go test ./...          # all packages pass
just check             # staticcheck + gosec; no new suppressions
for d in examples/*/; do go run ./$d; done   # all examples exit 0
```

---

## Gotchas

- **Never invent API without a user request.** This skill is for planning requested features, not for proposing new ones.
- **Roadmap docs are living documents.** Updating them later is expected and encouraged — don't over-engineer the first draft.
- **New adapters implement port interfaces, not stream bridge functions.** Every new transport adapter must implement `ports.SourceAdapter[T]`, `ports.SinkAdapter[T]`, `ports.IOAdapter[Req,Resp]`, and/or `ports.ToolAdapter[In,Out]` (server-side request/response, complement of `IOAdapter`) in a `binding.go` file. Do NOT add `SubscribeStream`, `DrainPublish`, or `CallStream` functions — those patterns have been removed. Non-stream functions (`Subscribe`, `Publish`, `Call`, `Serve`) remain for standalone use.
- **Adapter binding constructors belong in `adapters/<transport>/binding.go`.** The adapter's `Activate`/`Transform`/`Bind` methods call the underlying non-stream adapter functions (`SubscribeHandler`, `Publish`, `Call`, `Serve`, etc.) — never implement transport IO from scratch.
- **New handle-backed adapters (REST/events/reqreply/MCP-shaped) should support `ports.Pattern`.** If the transport has an `api/*` builder (`rest.NewRoute`, `events.NewChannel`, `reqreply.NewRoute`, `apimcp.NewTool`), the corresponding `ports.RESTPattern`/`EventPattern`/`ReqReplyPattern`/`MCPPattern` should already work via the existing `RESTHandle`/`EventHandle`/`ReqReplyHandle`/`MCPHandle` accessors and `PortOptions.RESTBuilder`/etc. fields — no new plumbing needed unless the transport introduces a genuinely new `api/*` package. Handle-less adapters (no `api/*` builder — e.g. `file`, `sql`) use `PortOptions.Params`/`IOParam` + `ports.ValidateParams` instead.
- **Type-assertion guards are non-negotiable.** `FileObserver`, `SQLObserver`, `SecurityObserver`, `TraceObserver` must always be type-asserted. Never embed them in `Observer`.
- **`just check` must pass clean.** Never add `//nolint` or `//gosec` suppressions to silence new findings.
- **All Examples must exit 0.** Fix stale patterns in existing examples if your change affects their API.
- **Go generics methods cannot introduce new type params.** Use free functions (`forge.NewFunction[In, Out]`) when a second type parameter is needed on a generic type.
- **Observer location strings are shared vocabulary** — use existing strings (`"sql_row"`, `"file"`, `"payload"`) before inventing new ones. New ones only when genuinely different.

---

## References

- [`docs/roadmap/`](../../../docs/roadmap/) — existing roadmap docs as format reference
- [`references/checklist.md`](references/checklist.md) — detailed per-feature planning checklist
- [`.github/instructions/go-codex.instructions.md`](../../instructions/go-codex.instructions.md) — design contract
- [`codex/errors.go`](../../../codex/errors.go) — reference implementation for error types with `slog.LogValuer`
- [`stats/observer.go`](../../../stats/observer.go) — all Observer interfaces
- [`adapters/sql/validate.go`](../../../adapters/sql/validate.go) — reference: type-assertion guard + ReportErrors pattern
- [`adapters/sql/validate_test.go`](../../../adapters/sql/validate_test.go) — reference: LogValue test quality
- [`.github/skills/review-go-codex/references/checklist.md`](../review-go-codex/references/checklist.md) — cross-layer consistency checklist
