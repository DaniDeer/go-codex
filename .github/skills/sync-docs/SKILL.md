---
name: sync-docs
description: 'Syncs README.md, /docs (when present), and .github/instructions with the go-codex codebase. Use when asked to sync docs, update README, sync instructions, keep docs in sync, or after adding/renaming/removing packages, types, or exported functions in go-codex.'
---

# sync-docs

Scan the actual files on disk and bring README.md, `.github/instructions/go-codex.instructions.md`, and `/docs` (if it exists) into alignment with the current codebase state.

## When to Use This Skill

- User asks to "sync docs", "update README", "sync instructions", "keep docs in sync"
- After a package, type, or function is added, renamed, or removed
- Periodic drift correction when docs and code diverge

## What to Sync and Where

### README.md — package tree

The tree block inside the code fence is the single package map for the project. Rebuild it to match the actual directory layout:

- Include every directory that contains at least one `.go` file.
- Preserve inline comments (the `# ...` annotations) — update them to reflect current package responsibility.
- `examples/` belongs in the tree but annotated as non-importable demos.

### .github/instructions/go-codex.instructions.md — package table and examples

Two sections to keep current:

1. **Package Structure table** — one row per package. Add rows for new packages; remove rows for deleted ones; update the "Responsibility" and "Imports allowed from" columns when they change.
2. **Code examples** — if a referenced symbol no longer exists, update the example to match the current API. If the package is still a stub (no exported symbols yet), preserve the design-intent example as-is; do not fabricate new API surface.

### /docs — only when directory exists

Check `docs/` existence before touching anything. If present, update Markdown files that describe package APIs — fix stale type names, signatures, and import paths. If absent, skip entirely.

### Verification

After syncing, run `just check` (fmt + staticcheck + gosec) and `just test`. Fix any issues before finishing.

## Gotchas

- **Never create `/docs` from scratch.** Its absence is intentional. Only sync it when the directory already exists.
- **`examples/` is not importable.** It appears in the README tree and the module root but must NOT appear in the Package Structure table in `go-codex.instructions.md`.
- **Empty stub files establish valid packages.** A `.go` file with only a `package` declaration still counts as a package. Do not remove it from the package table because it has no exported symbols.
- **Preserve design-intent examples.** `go-codex.instructions.md` may contain examples for APIs that are not yet implemented (stubs). Keep those — they are design specifications, not errors.
- **Do not invent new design decisions.** Only sync what is observable from files on disk. If a package's responsibility is ambiguous, leave the existing description and flag it for human review.
- **Module path is `github.com/DaniDeer/go-codex`.** All import paths in examples must use this prefix.
