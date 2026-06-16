# go-codex New Feature Planning Checklist

Run every section when planning a new feature. Each section must have an
explicit answer in the plan before implementation starts.

---

## 1. API Shape — Declarative, Simple, Consistent

| Check | Question to answer |
|-------|-------------------|
| Declare-once pattern | Can the new type be declared once and passed around as a value? |
| Naming parity | Does the name follow existing layer conventions? (`NewX`, `XHandle`, `XOpt`, `XMeta`) |
| Method vs function | Is this a method on an existing type, or a new constructor? Go methods on generic types cannot introduce new type parameters — use a free function (like `PatchEncoded[T, P any]`, `forge.NewFunction[In, Out]`) when a second type parameter is required. |
| Opt interface | Does the new option type implement the sealed `XOpt` interface? |
| Pointer-free ergonomics | Does it use `.WithCodec(c)` instead of `Codec: &c`? |
| No `Required` on template vars | Path/topic/file/URI template vars are always required — no `Required` field |

---

## 2. Structured Errors with `slog.LogValuer`

For every new error type, verify:

| Check | Expected |
|-------|---------|
| Implements `error` | `Error() string` |
| Implements `Unwrap()` | `Unwrap() error` — when the error wraps another |
| Implements `slog.LogValuer` | `LogValue() slog.Value` — always |
| Uses `slog.GroupValue(...)` | Returns `slog.GroupValue(slog.String("field", ...), ...)` |
| `errors.As`-navigable | Can be extracted via `errors.As` from wrapper errors |

**`LogValue()` attribute naming:**
- `path` — file path (string)
- `param` — parameter name (string)
- `value` — the value that failed (string)
- `cause` — the underlying error (any)
- `constraint` — constraint name (string)
- `message` — constraint message (string)

---

## 3. Observer Pattern

| Check | Expected |
|-------|---------|
| Observer default | `if obs == nil { obs = stats.NoopObserver{} }` at top of method |
| Happy path fires | Observer called with `success=true` on success |
| Every error path fires | Observer called with `success=false` on **every** error branch |
| No bare success-only | Never add observer call only on the happy path — all branches must call it |
| `ValidationErrors` propagated | `stats.ReportErrors(obs, "location", err)` before observer call on error |
| `FileObserver` type-asserted | `if fo, ok := obs.(stats.FileObserver); ok { fo.RecordFileRead(...) }` |
| `SecurityObserver` type-asserted | `if so, ok := obs.(stats.SecurityObserver); ok { so.RecordSecurityRejection(...) }` |
| Godoc updated | `stats.FileObserver.RecordFileRead` / `RecordFileWrite` godoc lists the new method |

**Observer location strings:**
- `"file"` — format.File read/write errors
- `"body"` — HTTP request/response body
- `"path"` — HTTP/file path param
- `"query"` — HTTP query param
- `"cookie"` — HTTP cookie
- `"header"` — HTTP header
- `"payload"` — MQTT payload
- `"input"` / `"output"` — forge function input/output
- `"env"` — environment variable

---

## 4. Unit Test Coverage

For every new exported type, method, or function:

| Test | Required |
|------|---------|
| Happy path (valid input → expected output) | ✓ |
| Error path (invalid input → correct typed error) | ✓ |
| `errors.As` chain traversal | ✓ for all error types |
| `LogValue()` returns `slog.GroupValue` with correct attrs | ✓ for all new error types |
| Observer called on success | ✓ |
| Observer called with `success=false` on error | ✓ |
| Pre-flight (no I/O) for unsupported operation | ✓ when applicable |
| Round-trip (encode + decode) | ✓ for codec types |
| Schema fields set correctly | ✓ for codec/constraint types with schema annotation |

**Test naming convention:** `Test_functionName_scenario` or `TestTypeName_MethodName_scenario`

**Test codec helpers:** use `codex.RequiredField(...)` / `codex.OptionalField(...)` — never `codex.Field[T,V]{...}` struct literals.

---

## 5. Documentation (three surfaces)

### 5a. `go-codex.instructions.md` (mandatory for every code change)

| Check | Expected |
|-------|---------|
| New type in Package Structure table | Row added or updated |
| New method listed in package entry | `File.Patch`, `Format.IsPatchable`, etc. |
| New error type listed | `FilePatchNotSupportedError{Path}` |
| `slog.LogValuer` note | "— implements `slog.LogValuer`" next to error types |
| Import graph unchanged | "Imports allowed from" column not widened without justification |

### 5b. `docs/` Zensical site (for major user-facing features)

| Check | Expected |
|-------|---------|
| Feature page updated | `docs/features/*.md` mentions new API |
| Guide updated | `docs/guides/*.md` shows new pattern if relevant |
| Code examples compile | All fenced Go blocks use current API (`WithCodec`, not `Codec: &`) |
| Cross-links correct | Internal `../` links resolve; no broken anchors |

### 5c. `*/doc.go` + `Example...()` functions

| Check | Expected |
|-------|---------|
| `doc.go` updated | Package doc mentions new major symbol |
| `Example...()` added if key workflow | `ExampleFile_Patch()` for major new methods |
| `// Output:` comment present | Every `Example...()` has matching output comment |

---

## 6. Example Update

| Check | Expected |
|-------|---------|
| Relevant example updated | The example most closely related to the new feature |
| New section added | Numbered section (e.g., "Section 6: Patch") |
| Comments explain "why" | Not just what the API call does, but the use case |
| `go run ./examples/X/` exits 0 | No panics, no unexpected errors |
| No stale patterns | `.WithCodec(c)` not `Codec: &c`; `RequiredField` not `Field[T,V]{...}` |

---

## 7. Verification

Run in order — all must pass before the plan is considered done:

```bash
go fmt ./...           # format; no diff should remain
go build ./...         # zero compile errors
go test ./...          # all packages pass
just check             # staticcheck + gosec; no new suppressions
for d in examples/*/; do go run ./$d; done   # all examples exit 0
```
