# go-codex Documentation Checklist

Run each section during Phase 2 of the review-docs skill.

---

## 1. Navigation Completeness

| Check | Expected |
|-------|----------|
| Every `[nav]` entry in `zensical.toml` maps to an existing `docs/*.md` file | No 404s |
| Every `docs/*.md` file appears in `zensical.toml` nav | No orphan pages |
| `zensical build` exits 0 | No build errors |

---

## 2. API Accuracy

| Check | Expected |
|-------|----------|
| All type names in docs match exported types in Go source | No stale names |
| All method names match exported methods | No stale names |
| All import paths use `github.com/DaniDeer/go-codex/...` | Correct module path |
| Code examples use `RequiredField`/`OptionalField` not `codex.Field[T,V]{...}` | Current API |
| Code examples use `.WithCodec(c)` not `Codec: &c` | Current API |
| Code examples use `NewRoute`/`NewChannel` constructors, not `AddRoute`/`AddChannel` | Current API |
| `Route.ClientHandle()` shown for client-only usage | Current API |

---

## 3. Code Example Validity

For each fenced code block in `docs/` that contains Go code:

| Check | Expected |
|-------|----------|
| Import paths resolve | `go build` won't fail |
| Referenced types exist | No stale type references |
| Referenced functions/methods exist | No stale function references |
| `Example...()` functions in `*_test.go` pass `go test` | Runnable on pkg.go.dev |

---

## 4. Coverage Gaps

| Feature | Required docs page |
|---------|-------------------|
| `Codec[T]` fundamentals | `docs/concepts/codec.md` — must have code examples |
| `rest.NewRoute` + net/http server | `docs/guides/http-server.md` — must have code examples |
| `nethttp.Call` + client | `docs/guides/http-client.md` — must have code examples |
| `events.NewChannel` + MQTT | `docs/guides/mqtt.md` — must have code examples |
| `mcp.NewTool` | `docs/guides/mcp.md` — must have code examples |
| `forge.NewFunction` | `docs/concepts/pipelines.md` — must have code examples |
| Codec-as-contract pattern | `docs/concepts/codec-as-contract.md` — must have code examples |
| Structured errors + slog | `docs/guides/error-handling.md` — must have code examples |
| Observer + metrics | `docs/guides/observer.md` — must have code examples |
| OpenAPI spec generation | `docs/guides/openapi.md` — must have code examples |
| AsyncAPI spec generation | `docs/guides/asyncapi.md` — must have code examples |
| CLI / config / env vars | `docs/guides/config.md` — must have code examples |
| `format.Binary` + `codex.Bytes`/`Base64` | `docs/features/formats.md` — must have Binary section with Gob vs Binary table, `codex.Bytes` vs `codex.Base64` table, and binary format constraint examples |
| Binary over MQTT | `docs/guides/mqtt.md` — must have "Binary payloads" section with `format.Binary` + `WithFormats` wiring |
| Binary over HTTP server | `docs/guides/http-server.md` — must have "Binary payloads" section covering `WithRequestFormats` + `WithFormats` + `MaxBodyBytes` subtlety |
| Binary over HTTP client | `docs/guides/http-client.md` — must have "Binary requests and responses" section |
| Binary file format validators | `docs/features/formats.md` — `validate.PNG/JPEG/GIF/WebP/PDF/ZIP` must be listed with usage table |

A page with no code examples for a mature feature = `small` finding.
A page that is entirely missing = `small` finding.

---

## 5. Stale Content

Scan every `docs/*.md` and `*/doc.go` file for:

| Pattern | Finding |
|---------|---------|
| Reference to a removed or renamed exported symbol | `bug` |
| Reference to a deleted package path | `bug` |
| `codex.Field[T,V]{...}` struct literal syntax | `small` |
| `Codec: &codec` pointer pattern | `small` |
| `builder.AddRoute(` / `builder.AddChannel(` | `small` |
| `(if exists)` language about doc.go — all packages now have one | `trivial` |
| Link to a non-existent GitHub file/example | `trivial` |

---

## 6. Example() Function Coverage

Spot-check:
```bash
grep -rn "^func Example" api/ adapters/ forge/ codex/ --include="*_test.go"
```

| Package | Minimum Example |
|---------|----------------|
| `codex` | `ExampleStruct()` or `ExampleRequiredField()` |
| `api/rest` | `ExampleNewRoute()` |
| `api/events` | `ExampleNewChannel()` |
| `adapters/nethttp` | `ExampleCall()` |
| `forge` | `ExampleNewFunction()` |

Missing = `trivial` finding. Example that doesn't compile = `bug` finding.

---

## 7. doc.go Content Quality

**All packages now have `doc.go`.** The check is about content quality, not presence.

```bash
find . -name "doc.go" | grep -v examples | sort
# Should list 22 files (one per package under module root)
```

| Package | Check |
|---------|-------|
| `codex/doc.go` | Rich overview with sections, `# Section` headings, typical usage |
| `forge/doc.go` | Rich overview with Apply sequence, collection ops, registry |
| `api/rest/doc.go` | Package purpose, typical usage, OpenAPI spec note |
| `api/events/doc.go` | Package purpose, typical usage, AsyncAPI spec note |
| `api/mcp/doc.go` | Package purpose, Tools/Resources/Prompts pattern |
| `adapters/nethttp/doc.go` | Server AND client (Call) covered |
| `schema/doc.go` | Zero-dependency design rationale |
| `validate/doc.go` | Format + range + binary byte constraints + binary file format constraints + "when to use which" guide + composition ordering rule |
| All other doc.go files | At least 5 lines of meaningful description |

A `doc.go` with fewer than 10 meaningful lines for a key public package = `trivial` finding.
A new package added without `doc.go` = `small` finding.

---

## 8. Cross-Link Correctness

| Check | Expected |
|-------|----------|
| All `https://pkg.go.dev/github.com/DaniDeer/go-codex/...` in `reference/index.md` use correct package paths | Match actual directory layout |
| All `https://github.com/DaniDeer/go-codex/tree/main/examples/...` in docs point to existing example directories | `ls examples/` confirms existence |
| Internal doc links (`../guides/http-server.md`) resolve correctly | No broken relative paths |

---

## 9. README Sync

| Check | Expected |
|-------|----------|
| Project Structure tree includes all directories with `.go` files | `find . -name "*.go" -not -path "*/examples/*"` → all dirs listed |
| Project Structure tree does NOT include stale/removed packages | Cross-check vs actual filesystem |
| Examples grouped correctly by layer (Codec / REST / Events / Forge) | Matches actual example directory purposes |
| Link to Zensical docs site present and correct | `https://danideer.github.io/go-codex/` |
| Link to pkg.go.dev present and correct | `https://pkg.go.dev/github.com/DaniDeer/go-codex` |
| No broken anchor links | `#anchor` refs point to existing headings |

---

## 10. instructions.md Sync

| Check | Expected |
|-------|----------|
| Package Structure table has a row for every Go package under module root | Match `find . -name "*.go" -not -path "*/examples/*" \| xargs -I{} dirname {} \| sort -u` |
| `examples/` NOT in the Package Structure table | examples are not importable |
| Newly added packages have correct "Responsibility" and "Imports allowed from" values | Cross-check with actual imports |
| Removed packages no longer appear | No stale rows |
| Code examples in instructions reference existing exported symbols | `go build ./...` won't fail |
