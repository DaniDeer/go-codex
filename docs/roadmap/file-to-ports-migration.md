# `format.File` → `ports` Migration

> **Status:** Design evaluation — not yet implemented. This is a
> breaking-change migration; do not start without explicit go-ahead.
> [← Back to Roadmap](index.md)
>
> See also: [Ports feature](../features/ports.md) · [Formats & Serialization](../features/formats.md) · [Config Package (companion roadmap)](config-package.md)

## Motivation

`ports.Cache[T]` (shipped alongside `CacheKeyParam`/`NewCache`) established
a pattern: a type that "has a" `format.Format[T]` field but IS itself a
declarative, protocol-agnostic **addressing** descriptor — bound to a port
via a `Pattern`, used by exactly one adapter family — belongs in `ports`,
not `format`. `format.File[T]` fits this description exactly:

- Path-addressed (`{var}` template), the same way `Cache[T]` is
  key-addressed.
- Bound to a port via `ports.FilePattern`, the same way `Cache[T]` is bound
  via `ports.CachePattern`.
- Used by exactly one adapter family (`adapters/file`), the same way
  `Cache[T]` is used by `adapters/redis`.

But `File[T]` currently lives in `format`, for purely historical reasons —
it predates the `ports`/`Pattern` system entirely. This creates a visible
inconsistency: `Cache[T]`'s package signals "I'm a pipeline-bindable
descriptor"; `File[T]`'s package signals "I'm a wire format" — even though
both play the identical structural role relative to their respective
adapter families.

## Scope decisions

| In scope | Out of scope |
|---|---|
| Move `File[T]`, `FilePathParam`, `FileOpt`, `NewFile`, `PatchEncoded` from `format` to `ports` | `format.FromEnv`/`FromEnvVar` — separate rationale, no `ports.Pattern` relationship (see the companion [Config Package](config-package.md) roadmap) |
| Move the 7 file error types (`FilePathParamError`, `MissingFilePathVarError`, `FileReadError`, `FileDecodeError`, `FileEncodeError`, `FileWriteError`, `FilePatchNotSupportedError`) | Changing `format.Format[T]`, `format.JSON`/`YAML`/`TOML`/`Gob`/`Binary`/`New`/`NewTyped`/`NewStreamed` — these stay in `format`; `ports.File[T]` continues to embed a `format.Format[T]` exactly as today |
| Update `adapters/file` to use `ports.File[T]` instead of `format.File[T]` (signature-only change — `ReadAdapter`/`ReadEachAdapter`/`DrainWriteFileAdapter`/`DrainPatchAdapter`/`DrainPatchEncodedAdapter`) | Changing `adapters/file`'s behavior, error handling, or test coverage — purely a type-origin change |
| `ports.FileHandle[T]` accessor becomes same-package (currently returns `format.File[T]`, becomes `ports.File[T]`) | Adding new capabilities to `File[T]` as part of this move — pure relocation, zero new behavior |
| Full doc/example pass (~20 `.md` files, several `examples/*`) | Deprecation aliases in `format` (see Open design decisions — hard cutover is the likely choice given this project's track record on breaking changes) |

## Impact inventory

*(current as of this writing — re-verify with a fresh grep before
implementing; the codebase changes between now and then)*

**Go files referencing the moving symbols** (~15):
`adapters/file/{binding.go,errors.go,binding_test.go}`,
`adapters/redis/binding.go` (doc cross-reference only),
`format/{file.go,file_test.go,format.go,format_test.go,doc.go}`,
`ports/{pattern.go,handle.go,port_test.go}`,
`validate/binary.go` (doc reference),
`stats/{context.go,observer.go}` (doc reference),
`examples/{file-patch-sink,file-io,flat-key-patch,redis-cache,sensor-service/ioports}/*.go`

**Doc files** (~20):
`.github/instructions/go-codex.instructions.md`,
`.github/skills/{plan-a-new-codex-feature,review-go-codex}/**`,
`docs/concepts/{observable-layers,codec,pipelines}.md`,
`docs/guides/{sql,config,ports,observer}.md`,
`docs/features/{formats,ports,redis,observer}.md`,
`docs/reference/project-structure.md`

## API surface (symbol rename map)

| Old (`format`) | New (`ports`) |
|---|---|
| `format.File[T]` | `ports.File[T]` |
| `format.NewFile[T](template, fmt, opts...)` | `ports.NewFile[T](template, fmt, opts...)` |
| `format.FileOpt` | `ports.FileOpt` |
| `format.FilePathParam` | `ports.FilePathParam` |
| `format.FilePathParamError` | `ports.FilePathParamError` |
| `format.MissingFilePathVarError` | `ports.MissingFilePathVarError` |
| `format.FileReadError` | `ports.FileReadError` |
| `format.FileDecodeError` | `ports.FileDecodeError` |
| `format.FileEncodeError` | `ports.FileEncodeError` |
| `format.FileWriteError` | `ports.FileWriteError` |
| `format.FilePatchNotSupportedError` | `ports.FilePatchNotSupportedError` |
| `format.PatchEncoded[T,P](fh, vars, patchCodec, patch, opts)` | `ports.PatchEncoded[T,P](fh, vars, patchCodec, patch, opts)` |

`File[T]`'s internals (embedded `format.Format[T]` field, `Read`/`Write`/
`Update`/`Patch`/`BuildPath`/`ValidatePathVars`/`PathParamSchemas` methods)
are **unchanged** — this is a package-origin move, not a redesign. No
import-cycle obstacle: `ports` already imports `format` (for
`format.Format[T]` itself, used by `CachePattern`/`FilePattern`/
`SocketPattern` today); `format` never imports `ports`.

## Structured errors

Unchanged shapes, just package-qualified differently
(`ports.FileReadError{Path,Err}` etc.) — all keep their existing
`Error()`/`Unwrap()`/`LogValue()` implementations verbatim.

## Observer integration

Unchanged — the `stats.FileObserver` type-assertion guard pattern inside
`File.Read`/`Write`/`Update`/`Patch` is identical regardless of which
package hosts `File[T]`.

## Unit test plan

| ID | Name | Verifies |
|---|---|---|
| M1 | Full `format` package test suite still passes after removing File-related tests (moved to `ports`) | No regressions from the split |
| M2 | Full `ports` package test suite passes with the moved File tests | Behavior identical post-move |
| M3 | `adapters/file` package test suite passes unchanged (only import path changed) | Adapter behavior untouched |
| M4 | Every example using `format.File`/`NewFile` builds and runs after the rename | No stale references |

## Files to create/change

See Impact inventory above — expect roughly 15 `.go` files and 20 `.md`
files touched in one atomic round (Go's compiler enforces consistency —
partial states won't build, so this should not be split across multiple
merged increments).

## Open design decisions

1. **Hard cutover vs. deprecation aliases in `format`** — e.g. keep
   `format.File = ports.File` type aliases + `//Deprecated:` wrapper
   functions for one release before removing? This project's `history.md`
   shows a consistent preference for hard cutovers on breaking changes (no
   long deprecation windows recorded) — leaning hard cutover, but confirm
   before implementing.
2. **Does `ports.NewFile` collide with any existing `ports` symbol name?**
   No collision found at time of writing (`ports` has no `File`/`NewFile`
   today) — re-check at implementation time.
3. **Should this be one big round, or split (move the type first, then
   update call sites in a second pass)?** Given Go's compiler enforces
   consistency, a single atomic round is safer — a partial state simply
   won't compile, so there is no meaningful way to split this safely across
   multiple merged commits without a temporary deprecation alias (see #1).
