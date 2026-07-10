# go-codex New Feature Planning Checklist

Two phases: **Explore** (roadmap doc) and **Implement** (code). Run the relevant
sections. Both must be addressed before implementation is considered done.

---

## Phase A — Explore: Roadmap Doc Quality

Use this when writing or reviewing `docs/roadmap/<feature>.md`.

### A1. Roadmap Doc Completeness

| Section | Required |
|---------|---------|
| Motivation — concrete user problem | ✓ |
| Scope decisions — what's in Phase 1, what's deferred | ✓ |
| API surface — exact Go signatures (generics correct, not pseudocode) | ✓ |
| Structured errors — every error type with fields + `LogValue()` attributes | ✓ |
| Observer integration — which interface, which methods, type-assertion guard | ✓ |
| Unit test plan — table of test IDs, names, what each verifies | ✓ |
| Files to create — table of file → responsibility | ✓ |
| Out of scope (Phase 2) — explicit list | ✓ |
| Open design decisions — questions still to resolve | ✓ if any exist |

### A2. Roadmap Doc Navigation

| Check | Expected |
|-------|---------|
| Row in `docs/roadmap/index.md` | `| [Title](file.md) | pkg | Status | Summary |` |
| Entry in `zensical.toml` `[nav.Roadmap]` | `"— Feature Name" = "roadmap/<file>.md"` |
| Status header at top of doc | `> **Status:** Design complete — not yet implemented.` |
| Back-link to index | `> [← Back to Roadmap](index.md)` |
| `go build ./...` still passes | No Go code changed, but verify |

---

## Phase B — Implement: Code Quality

Use this when implementing an approved roadmap feature.

### B1. API Shape — Declarative, Simple, Consistent

| Check | Question to answer |
|-------|-------------------|
| Declare-once pattern | Can the new type be declared once and passed around as a value? |
| Naming parity | Does the name follow existing layer conventions? (`NewX`, `XHandle`, `XOpt`, `XMeta`) |
| Method vs free function | Go methods on generic types cannot introduce new type parameters — use a free function when a second type parameter is needed |
| Opt interface | Does the new option type implement the sealed `XOpt` interface? |
| Pointer-free ergonomics | `.WithCodec(c)` not `Codec: &c` |
| No `Required` on template vars | Path/topic/file/URI template vars always required — no `Required` field |

### B2. Structured Errors with `slog.LogValuer`

For every new error type:

| Check | Expected |
|-------|---------|
| `Error() string` | Descriptive, includes boundary context (`"pkg: boundary (op): cause"`) |
| `Unwrap() error` | Present whenever error wraps another; enables `errors.As` |
| `LogValue() slog.Value` | Always present; returns `slog.GroupValue(...)` |
| Group attributes match fields | `slog.String("boundary", ...)`, `slog.Any("err", ...)` |
| `errors.As`-navigable | Can be extracted from wrapper errors |

**Standard attribute names:**
- `table`, `op` — SQL adapter context
- `path` — file path
- `param`, `value` — parameter name and failing value
- `err`, `cause` — underlying error (use `err` when it's the only error field)
- `function`, `input`, `output` — forge context
- `topic`, `op` — MQTT/ZeroMQ context

### B3. Observer Pattern

| Check | Expected |
|-------|---------|
| Default observer | `if obs == nil { obs = stats.NoopObserver{} }` at top of method |
| Happy path fires | Observer called with success on happy path |
| Every error path fires | Observer called with failure on **every** error branch — no gaps |
| `ValidationErrors` propagated | `stats.ReportErrors(obs, "location", err)` before observer call |
| New extension interface | Added to `stats/observer.go`; `NoopObserver`+`LoggingObserver`+`fanout` implement it |
| Type-assertion guard | `if fo, ok := obs.(stats.XObserver); ok { fo.RecordX(...) }` — never embedded |
| `stats/observer_test.go` updated | Compile-time assertion + delegation test for new extension interface |

**Observer location strings (use existing where possible):**
`"sql_row"` · `"file"` · `"body"` · `"path"` · `"query"` · `"cookie"` · `"header"` · `"payload"` · `"topic_var"` · `"user_property"` · `"input"` · `"output"` · `"env"`

### B4. Unit Test Coverage

| Test | Required |
|------|---------|
| Happy path — valid input → expected output | ✓ |
| Error path — invalid input → correct typed error with correct fields | ✓ |
| `errors.As` chain traversal reaches inner error | ✓ for all error types |
| `LogValue()` returns `slog.KindGroup` + all field keys present | ✓ for all error types |
| Observer called on success (args verified) | ✓ |
| Observer called on failure (error type verified — not just `!= nil`) | ✓ |
| `nil` Observer → no panic | ✓ |
| Plain Observer (no extension interface) → graceful fallback | ✓ |
| Round-trip encode/decode | ✓ for codec types |
| `Example...()` function with `// Output:` | ✓ for key new public symbols |

**Test helper rule:** `codex.RequiredField(...)` / `codex.OptionalField(...)` — never `codex.Field[T,V]{...}`.

**LogValue test quality:** Assert `slog.KindGroup` AND presence of all individual attribute keys. Do not check only `Kind().String() != ""`.

### B5. Documentation (three surfaces)

| Surface | File | Required |
|---------|------|---------|
| API instructions | `.github/instructions/go-codex.instructions.md` | ✓ always |
| Feature page | `docs/features/<feature>.md` | ✓ all user-facing features |
| Guide | `docs/guides/<feature>.md` | ✓ if step-by-step workflow |
| Package doc | `*/doc.go` | ✓ for new packages |
| Example function | `*_test.go` `Example...()` | ✓ for new packages, major symbols |
| Project structure | `docs/reference/project-structure.md` | ✓ for new packages/dirs |
| Roadmap cleanup | Remove from `docs/roadmap/index.md` + `zensical.toml` roadmap nav | ✓ when feature ships |
| Nav additions | Add feature/guide to `zensical.toml` | ✓ |

### B6. Example Update

| Check | Expected |
|-------|---------|
| Existing or new example demonstrates feature | The example closest to the new feature |
| Comments explain "why" (use case) | Not just what the API call does |
| `go run ./examples/X/` exits 0 | No panics, no unexpected errors |
| No stale patterns | `.WithCodec(c)` not `Codec: &c`; `RequiredField` not `Field[T,V]{...}` |

### B7. Verification

Run in order — all must pass:

```bash
go fmt ./...           # format; no diff must remain
go build ./...         # zero compile errors
go test ./...          # all packages pass
just check             # staticcheck + gosec; no new suppressions
for d in examples/*/; do go run ./$d; done   # all examples exit 0
```
