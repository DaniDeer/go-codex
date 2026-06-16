---
name: plan-a-new-codex-feature
description: 'Planning skill for new go-codex features. Preloads all mandatory requirements: structured errors with slog.LogValuer, Observer pattern for metrics, unit test coverage, three-surface documentation sync (instructions.md, docs/, doc.go), and example updates. Use when asked to "plan a new feature", "add to go-codex", "design a new API", "plan a codec feature", "implement X for go-codex", or before writing any new exported symbol in the library.'
---

# plan-a-new-codex-feature

Planning guide for new go-codex features. Every new feature must satisfy all
five mandatory requirements before implementation is considered complete.

## When to Use This Skill

- User says "plan a new feature for go-codex"
- User asks to design a new exported type, method, or package
- User says "add X to go-codex" or "implement X for the format/file package"
- Starting a new planning round for any Layer 1 (codec), Layer 2 (API), or Layer 3 (forge) addition

## Step-by-Step Planning Workflow

### Phase 1 — Understand the feature

Read the relevant source files before writing a single line of the plan:

1. The package(s) being extended (e.g. `format/file.go`, `api/rest/builder.go`)
2. The cross-layer consistency checklist: `references/checklist.md` in this skill
3. The design contract: `.github/instructions/go-codex.instructions.md`
4. Existing similar features for API shape reference (e.g. how does `File.Update` relate to the proposed `File.Patch`)

### Phase 2 — Design against the North Star

Every API addition must answer yes to: **Does this make the library more declarative, simple, and consistent for the user?**

The declare-once workflow must hold:
```
declare (NewX / NewFile / NewRoute) → compose → register / use
```

### Phase 3 — Write the plan

Use `references/checklist.md` to verify all five mandatory requirements are addressed.
The plan must include:

1. **What it does** — one-paragraph summary with the concrete use case
2. **API surface** — exact signatures, with godoc sketches
3. **Structured errors** — every new error type listed with its `LogValue()` attributes
4. **Observer hooks** — which Observer methods fire and when
5. **Unit test matrix** — table of test IDs to be written
6. **Files to change** — table of file → what changes
7. **Usage example** — runnable code snippet

### Phase 4 — Get plan approved before implementing

Present the plan and wait for explicit approval ("start", "implement it").

---

## Mandatory Requirements (checklist)

Read `references/checklist.md` for the full checklist. Summary:

### 1. Structured Errors with `slog.LogValuer`

Every new error type **must** implement `LogValue() slog.Value`. Pattern from `codex/errors.go`:

```go
type MyNewError struct {
    Path string
    Err  error
}

func (e MyNewError) Error() string { return fmt.Sprintf("...%q: %s", e.Path, e.Err) }
func (e MyNewError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e MyNewError) LogValue() slog.Value {
    return slog.GroupValue(
        slog.String("path", e.Path),
        slog.Any("cause", e.Err),
    )
}
```

Usage (users get structured logging for free):
```go
slog.Warn("operation failed", "error", myErr)   // ← LogValue() fires automatically
```

### 2. Observer Pattern

Determine which Observer interface applies and where to fire it:

| Layer | Interface | Methods | When |
|-------|-----------|---------|------|
| Format/File | `stats.FileObserver` | `RecordFileRead`, `RecordFileWrite` | After read phase, after write phase |
| REST adapter | `stats.Observer` | `RecordRequest` | Every request path (success + error) |
| MQTT adapter | `stats.Observer` | `RecordSubscribe`, `RecordPublish` | Every message path |
| Forge | `stats.PipelineObserver` | `RecordApply` | Every Function.Apply call |
| Security | `stats.SecurityObserver` (type-assert only) | `RecordSecurityRejection` | On rejection |

Rules:
- Observer must fire on **every** code path — including early-exit error paths, not just the happy path
- `SecurityObserver` must be type-asserted, never embedded: `if so, ok := obs.(stats.SecurityObserver); ok { ... }`
- `FileObserver` must be type-asserted: `if fo, ok := obs.(stats.FileObserver); ok { ... }`
- Use `stats.NoopObserver{}` as default when `opts.Observer == nil`
- Call `stats.ReportErrors(obs, "location", err)` to propagate `ValidationErrors` to `RecordValidationError`

### 3. Unit Tests

For every new exported symbol, write at minimum:

| Test category | Required |
|--------------|---------|
| Happy path | ✓ |
| Error path → correct typed error | ✓ |
| `errors.As` navigable | ✓ for all error types |
| `slog.LogValuer` fires correctly | ✓ for all new error types |
| Observer fired on success | ✓ |
| Observer fired on failure | ✓ |
| `IsMergeable`/`IsPatchable`/`IsStreamable` per format | ✓ if applicable |

Use `codex.RequiredField(...)` and `codex.OptionalField(...)` in test codecs — never `codex.Field[T,V]{...}` struct literals.

### 4. Documentation (three surfaces)

All three documentation surfaces must be updated:

| Surface | File | When |
|---------|------|------|
| API instructions | `.github/instructions/go-codex.instructions.md` | Every code change — mandatory |
| Zensical site | `docs/features/*.md` or `docs/guides/*.md` | Major user-facing features |
| pkg.go.dev | `*/doc.go` and `Example...()` in `*_test.go` | New packages or major API additions |

`go-codex.instructions.md` must always be updated. The other two surfaces depend on significance.

### 5. Example Update

If the feature touches an existing example area:
- Update the relevant `examples/*/main.go` to demonstrate the new API
- The example must run without errors: `go run ./examples/X/`
- Comment on "why" (use case), not just "what" (API call)

---

## Gotchas

- **Never invent API during a review.** `plan-a-new-codex-feature` is for new feature planning, not for inventing features that don't exist yet. Findings fix inconsistencies; new features have explicit user requests.
- **File errors have no `slog.LogValuer` yet (pre-R22+).** When adding any new file error, also add `LogValue()` to all existing file error types in the same pass to maintain consistency.
- **`FileObserver` is type-asserted.** `format.File[T]` uses `if fo, ok := obs.(stats.FileObserver); ok` — it does NOT embed `FileObserver` in `Observer`. Existing `Observer` implementations work unchanged.
- **`map[string]any` patch semantics follow RFC 7396** (JSON Merge Patch): patch keys win over existing; absent keys are preserved; `null` in patch removes keys in JSON (go-codex does not handle `null` removal unless explicitly noted).
- **`format.Binary` and `format.Gob` are not patchable** — they use typed marshal/unmarshal paths that bypass `map[string]any`. Always return `FilePatchNotSupportedError` before any I/O.
- **`just check` must pass clean.** After every implementation: `go fmt ./...` → `go build ./...` → `go test ./...` → `just check`. Do not suppress new staticcheck/gosec warnings.
- **All examples must exit 0.** `for d in examples/*/; do go run ./$d; done` — every example.

## References

- [`references/checklist.md`](references/checklist.md) — full per-feature planning checklist
- [`.github/instructions/go-codex.instructions.md`](../../instructions/go-codex.instructions.md) — design contract
- [`codex/errors.go`](../../../codex/errors.go) — reference implementation for `slog.LogValuer` on error types
- [`stats/observer.go`](../../../stats/observer.go) — all Observer interfaces
- [`.github/skills/review-go-codex/references/checklist.md`](../review-go-codex/references/checklist.md) — consistency checklist (cross-layer parity, param types, etc.)
