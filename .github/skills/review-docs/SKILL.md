---
name: review-docs
description: >
  Documentation quality audit and sync for go-codex. Reviews the Zensical docs site (docs/),
  doc.go files, Example() functions, and README for accuracy, completeness, and sync with the
  actual codebase. Use when asked to "review docs", "audit documentation", "check docs quality",
  "sync docs with code", "update README", "keep docs in sync", or after any feature addition,
  package change, or API rename. This is the single documentation skill — it covers both proactive
  quality review and reactive sync after code changes.
---

# review-docs

Documentation quality audit and sync across all three documentation surfaces:

| Surface | Files | Role |
|---------|-------|------|
| **Zensical site** | `docs/**/*.md`, `zensical.toml` | Full guides, concepts, tutorials |
| **pkg.go.dev** | `*/doc.go`, `Example...()` in `*_test.go` | API reference, runnable examples |
| **README.md** | `README.md` | Entry point, quick start, links |

Also keeps these files in sync with the codebase:

| File | What to keep current |
|------|---------------------|
| `README.md` | Project Structure tree (matches actual directory layout) |
| `.github/instructions/go-codex.instructions.md` | Package Structure table + code examples |
| `docs/reference/index.md` | Package reference links to pkg.go.dev |
| `zensical.toml` | Nav entries (must match docs/*.md files) |

## User Experience North Star

Every finding must be evaluated against:

> **Can a new user find what they need within 2 minutes — either in the docs site, on pkg.go.dev, or in the README?**

## When to Use This Skill

**Proactive (quality audit):**
- User says "review docs", "audit documentation", "check docs quality", "docs out of date"
- After a significant feature addition (new package, new adapter, new pattern)
- After a `review-go-codex` round that changed exported API surface
- Periodically (every few feature additions) to prevent documentation drift

**Reactive (sync after code changes):**
- User says "sync docs", "update README", "sync instructions", "keep docs in sync"
- After a package, type, or function is added, renamed, or removed
- After adding a new example in `examples/`

## Step-by-Step Workflow

### Phase 1 — Read sources in parallel

Read all of these before opening any finding:

| File/Directory | Why |
|----------------|-----|
| `docs/index.md` | Home page accuracy and links |
| `docs/get-started.md` | Quick start correctness — must compile |
| `docs/concepts/*.md` | Conceptual accuracy vs actual API |
| `docs/guides/*.md` | Guide content vs current API surface |
| `docs/reference/index.md` | pkg.go.dev links — check package paths |
| `zensical.toml` | Nav entries — must match docs/ files |
| `codex/doc.go` | Package-level overview |
| `api/rest/doc.go` | REST package overview |
| `api/events/doc.go` | Events package overview |
| `api/mcp/doc.go` | MCP package overview |
| `adapters/nethttp/doc.go` | nethttp server + client overview |
| `forge/doc.go` | forge overview |
| `schema/doc.go` | schema model overview |
| `validate/doc.go` | validate constraints overview |
| `README.md` | Quick start + Project Structure tree + all links |
| `.github/instructions/go-codex.instructions.md` | API surface truth (canonical source) |

Also scan:
```bash
grep -rn "^func Example" api/ adapters/ forge/ codex/ --include="*_test.go"
find docs/ -name "*.go" 2>/dev/null  # doc.go files in docs would be an error
```

Then read `references/history.md` to see what was already fixed. **Do not re-report these.**

### Phase 2 — Apply the checklist

Work through `references/checklist.md` section by section:

1. Navigation completeness
2. API accuracy (type names, method names, import paths)
3. Code example validity
4. Coverage gaps (major features without documentation)
5. Stale content (removed/renamed API references)
6. Example() function coverage on pkg.go.dev
7. doc.go content quality
8. Cross-link correctness
9. README sync (Project Structure tree + links)
10. instructions.md sync (Package Structure table)

### Phase 3 — Record findings

For every issue found, assign:

| Field     | Values                           |
|-----------|----------------------------------|
| ID        | `D<N>` (sequential, D for Docs)  |
| Severity  | `bug` / `small` / `trivial`      |
| Category  | one of the 10 checklist sections |
| File:line | exact location                   |
| Problem   | one sentence                     |
| Fix       | one sentence                     |

**Severity guidance for docs:**
- `bug` — incorrect API reference that would cause a user's code not to compile; broken link; wrong import path
- `small` — missing documentation for a major exported feature; guide refers to stale pattern; Package Structure table row missing or wrong
- `trivial` — doc.go comment is thin (<10 lines); minor wording improvement; missing Example() function

Present findings as a table, then group: bugs first, then small, then trivial.

### Phase 4 — Produce a plan

Write findings and priority order to the session plan.md. Do NOT start implementing until the user confirms.

### Phase 5 — Implement (after approval)

For each finding, in priority order:

1. Fix the docs file / doc.go / instructions / README
2. Run `zensical build` — must succeed with no errors
3. Run `go build ./...` — code examples in docs must not reference non-existent symbols
4. Run `go test ./...` — Example() functions must compile and pass
5. Run `just check` — no new staticcheck or gosec warnings

### Phase 6 — Verify

```bash
zensical build          # docs site builds without errors
go build ./...          # library still compiles
go test ./...           # all tests (including Example functions) pass
just check              # fmt + staticcheck + gosec clean
```

### Phase 7 — Update History

Append a new section to `references/history.md`:

```markdown
## Round <N> (<short title>)

- **<ID> — <finding title>**: one-sentence description.
- ...

---
```

Insert the new section **above** Round <N-1> (newest at top, below the header).

### Phase 8 — Commit Summary

Format:

```
<imperative title — max 72 chars>

Docs surface(s): <Zensical site / pkg.go.dev / README / instructions>
Round: DR<N>

Findings fixed:
- <ID> [<severity>] <one-line description>
- ...

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>
```

## Accuracy Guardrail

All code examples in docs and doc.go files must use current API:

- Import paths: `github.com/DaniDeer/go-codex/...`
- Field constructors: `codex.RequiredField(...)` / `codex.OptionalField(...)` — not `codex.Field[T,V]{...}`
- Codec setting: `.WithCodec(c)` — not `Codec: &c`
- Route/channel creation: `rest.NewRoute[Req,Resp](...)` — not `builder.AddRoute(...)`
- `Route.ClientHandle()` for client-only usage (no builder needed)

## Coverage Guardrail

Each of the following must have at least one documentation reference:

| Feature | Expected coverage |
|---------|------------------|
| `codex.Codec[T]` | `docs/concepts/codec.md` |
| `rest.NewRoute` + server | `docs/guides/http-server.md` |
| `nethttp.Call` + client | `docs/guides/http-client.md` |
| `events.NewChannel` + MQTT | `docs/guides/mqtt.md` |
| `mcp.NewTool` | `docs/guides/mcp.md` |
| `forge.NewFunction` | `docs/concepts/pipelines.md` |
| codec-as-contract pattern | `docs/concepts/codec-as-contract.md` |
| Structured errors + slog | `docs/guides/error-handling.md` |
| Observer / metrics | `docs/guides/observer.md` |
| OpenAPI spec generation | `docs/guides/openapi.md` |
| AsyncAPI spec generation | `docs/guides/asyncapi.md` |
| CLI / config / env vars | `docs/guides/config.md` |

If a guide exists but is clearly incomplete (no code examples, just a paragraph), file a `small` finding.

## pkg.go.dev Guardrail

Key packages must have at least one `Example...()` function in their test files:

| Package | Minimum Example |
|---------|----------------|
| `codex` | `ExampleStruct()` or `ExampleRequiredField()` |
| `api/rest` | `ExampleNewRoute()` |
| `api/events` | `ExampleNewChannel()` |
| `adapters/nethttp` | `ExampleCall()` |
| `forge` | `ExampleNewFunction()` |

If an `Example...()` function is absent for a key package, file a `trivial` finding.

**All packages must have `doc.go`.** This is now established — every package under the module root has one. If a new package is added without `doc.go`, file a `small` finding. If an existing `doc.go` has fewer than 10 meaningful lines, file a `trivial` finding.

## Navigation Guardrail

Every nav entry in `zensical.toml` must have a corresponding `docs/*.md` file.
Every `docs/*.md` file should have a corresponding nav entry.

Check with:
```bash
grep -o '"docs/[^"]*"' zensical.toml | sort
find docs/ -name "*.md" | sort
```

## README Sync Guardrail

The Project Structure tree in `README.md` must match the actual directory layout:
- Include every directory that contains at least one `.go` file
- Preserve inline comments — update them to reflect current package responsibility
- `examples/` belongs in the tree but annotated as non-importable demos

## instructions.md Sync Guardrail

The Package Structure table in `.github/instructions/go-codex.instructions.md` must stay current:
- Add rows for new packages (responsibility + imports allowed from)
- Remove rows for deleted packages
- Update the "Responsibility" column when a package's role changes
- `examples/` must NOT appear in the Package Structure table (not importable)

## Gotchas

- **All packages have `doc.go`.** This was established in a dedicated doc.go creation pass. Every package under the module root (except `examples/`) has a `doc.go`. If a review finds one missing, it is a `small` finding, not `trivial`.
- **Stub guides are no longer expected.** All `docs/guides/*.md` files were populated with content. If content is missing or thin, file a `small` finding.
- **pkg.go.dev link format.** Links use `https://pkg.go.dev/github.com/DaniDeer/go-codex/<pkg>` — verify the path exists.
- **Do not invent API.** If a guide describes a feature not yet in the codebase, flag it as `bug` — the guide is wrong, not the code.
- **zensical.toml format is TOML.** The nav section uses `[nav]` and `[nav.Section]` table syntax.
- **Module path is `github.com/DaniDeer/go-codex`.** All import paths in examples must use this prefix.
- **`examples/` is not importable.** It appears in the README tree and must NOT appear in the Package Structure table in `go-codex.instructions.md`.
- **Preserve design-intent examples.** `go-codex.instructions.md` may contain examples for APIs not yet implemented. Keep those — they are design specifications.

## References

- [`references/checklist.md`](references/checklist.md) — full documentation quality checklist
- [`references/history.md`](references/history.md) — findings fixed in previous rounds
- [`.github/instructions/go-codex.instructions.md`](../../instructions/go-codex.instructions.md) — API surface truth
- [Zensical docs](https://zensical.org/docs/) — site generator reference
