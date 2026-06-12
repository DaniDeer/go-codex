---
description: 'Maintenance rules: keep go-codex.instructions.md, docs/, and doc.go files in sync with code changes'
applyTo: '**/*.go,**/*.instructions.md,**/docs/**/*.md,**/doc.go'
---

# go-codex Instructions Maintenance

When modifying files in this repository, keep `.github/instructions/go-codex.instructions.md` accurate and up to date.

## When Modifying Go Source Files

| Change type                                  | Required update to go-codex.instructions.md             |
|----------------------------------------------|---------------------------------------------------------|
| New type or codec added                      | Add to the relevant section with a code example         |
| Type or codec renamed                        | Update all references and examples                      |
| Type or codec removed                        | Remove references and examples                          |
| Signature of `Codec[T]`, `MapCodecSafe`, `Refine`, `Constraint`, `Field`, `Variant` changed | Update the corresponding section's interface and examples |
| New package added under the module           | Add row to the Package Structure table with responsibility and allowed imports |
| Package removed                              | Remove its row from the Package Structure table         |
| Import rule changed (new allowed/disallowed dependency) | Update the "Imports allowed from" column     |
| New naming convention established            | Add row to the Naming Conventions table                 |
| Error handling pattern changed               | Update the Error Handling section                       |
| New reusable constraint added to `validate/` | Add to the Validation section                           |
| New codec or type added                      | Add `_test.go` cases: round-trip, error path, schema   |
| Function signature changed                   | Update all `_test.go` files that call it                |
| Codec renamed                                | Rename references in test files                         |
| New `validate/` constraint added             | Add cases to `validate/number_test.go` or `validate/string_test.go` |

## Run Examples After Significant Changes

Run all examples with `go run` after any change that affects:
- Public API signatures (codec, format, field helpers, builder methods)
- Adapter behaviour (nethttp, mqtt)
- Spec renderers (openapi, asyncapi)
- Any change that touches more than one package

```bash
just examples
# or manually:
for d in examples/*/; do echo "=== $d ===" && go run ./$d; done
```

All examples must produce output without errors (exit code 0). If an example
panics or prints an unexpected error, the change broke something. Investigate
before committing.

The examples are the primary integration test — they exercise the full stack
from codec → format → builder → renderer → adapter.

## When Modifying go-codex.instructions.md

- Verify every code example compiles: run `go build ./...` to confirm no example references a non-existent symbol.
- Verify package names in examples match actual package declarations.
- Verify import paths use `github.com/DaniDeer/go-codex/...`.
- After updating, confirm the Package Structure table still matches the actual directory layout.

## Documentation Maintenance

go-codex has three documentation surfaces. Keep them in sync:

| Surface | Files | Updated by |
|---------|-------|-----------|
| **API instructions** | `.github/instructions/go-codex.instructions.md` | Every code change (required) |
| **Zensical docs site** | `docs/**/*.md`, `zensical.toml` | Significant feature additions |
| **pkg.go.dev** | `*/doc.go`, `Example...()` in `*_test.go` | New packages / major API additions |
| **README.md** | `README.md` | Only for Quick Start and links — keep minimal |

### When to update docs/ (Zensical site)

| Change type | Required docs update |
|-------------|---------------------|
| New exported package | Add row to `docs/reference/index.md`; add stub `docs/guides/<pkg>.md`; add nav entry in `zensical.toml` |
| New major user-facing feature | Add/update relevant `docs/guides/*.md` or `docs/concepts/*.md` |
| New adapter (nethttp, mqtt, etc.) | Update `docs/guides/<transport>.md` with the new adapter pattern |
| API rename or removal | Update all `docs/` pages that reference the old name |
| New example added | Check if `docs/guides/` links to or mentions it |
| New codec-as-contract example | Update `docs/concepts/codec-as-contract.md` |

### When to update doc.go files

- When a package's exported API surface grows significantly, ensure the `// Package ...` comment (or `doc.go`) covers the new symbols.
- When a package is new, write at least 10 lines of package-level documentation.

### When to add Example() functions

- When a key user workflow lacks a runnable pkg.go.dev example, add an `Example...()` function to the package's `*_test.go` file.
- Every `Example...()` function must compile, produce output, and match the `// Output:` comment.

### Using the documentation skill

- **`review-docs`** — the single documentation skill. Run it after code changes to patch stale references (reactive), and periodically to audit docs quality and completeness (proactive). It covers README sync, instructions.md sync, docs/ content accuracy, doc.go quality, and Example() function coverage.

## Sync Checklist (run mentally before committing)

- [ ] All renamed symbols updated in instruction examples
- [ ] Package Structure table matches `go-codex/` directory tree (including `render/internal/schemarender`)
- [ ] New patterns have at least one code example
- [ ] Removed patterns no longer appear in instructions
- [ ] When adding a new `schema.Schema` field: update `render/internal/schemarender/schemarender.go` (both `render/openapi` and `render/asyncapi` use it automatically)
- [ ] When adding a new codec type to `codex/`: add entry to Package Structure table, add section in "Codec Patterns", add entry in README "Available Codecs" table
- [ ] `AdditionalPropertiesSchema *Schema` takes precedence over `AdditionalProperties *bool` in schemarender — keep this ordering in `schemarender.go`
- [ ] `just check` passes (fmt + staticcheck + gosec)
- [ ] `just test` passes
- [ ] `go build ./...` passes with no errors referencing symbols from examples
- [ ] All examples run without errors: `just examples`
- [ ] If a new package was added: `docs/reference/index.md` has a new row, `zensical.toml` has a nav entry
- [ ] If a major feature was added: a `docs/guides/*.md` or `docs/concepts/*.md` page exists (stub is OK)
- [ ] If an API was renamed: stale name not present in `docs/**/*.md` files
