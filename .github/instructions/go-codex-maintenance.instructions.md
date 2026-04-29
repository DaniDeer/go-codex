---
description: 'Maintenance rules: keep go-codex.instructions.md in sync with code changes'
applyTo: '**/*.go,**/*.instructions.md'
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

## When Modifying go-codex.instructions.md

- Verify every code example compiles: run `go build ./...` to confirm no example references a non-existent symbol.
- Verify package names in examples match actual package declarations.
- Verify import paths use `github.com/DaniDeer/go-codex/...`.
- After updating, confirm the Package Structure table still matches the actual directory layout.

## Sync Checklist (run mentally before committing)

- [ ] All renamed symbols updated in instruction examples
- [ ] Package Structure table matches `go-codex/` directory tree
- [ ] New patterns have at least one code example
- [ ] Removed patterns no longer appear in instructions
- [ ] `just check` passes (fmt + staticcheck + gosec)
- [ ] `just test` passes
- [ ] `go build ./...` passes with no errors referencing symbols from examples
