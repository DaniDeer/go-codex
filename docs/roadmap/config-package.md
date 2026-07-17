# `config` — a Dedicated Package for Environment-Based Configuration

> **Status:** Design evaluation — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [Formats & Serialization](../features/formats.md) · [Config guide](../guides/config.md)

## Motivation

`format.FromEnv[T]`/`FromEnvVar[T]`/`EnvVarError` load typed config from OS
environment variables using schema-driven coercion — but they do **not**
use `format.Format[T]` internally at all: no marshal/unmarshal, no wire
bytes, just `codex.Codec[T]` + `os.LookupEnv` + schema-driven string
coercion. They were correctly identified as "not really a format."

But unlike `ports.File[T]` (already migrated from `format` — see
`docs/features/ports.md`'s "Design pattern" section for the rationale), they also
have **no `ports.Pattern` counterpart and no adapter family** binds to them
— there is no `EnvPattern`, nothing in `adapters/*` ever calls them, they
never participate in pipeline wiring. `ports`' identity is specifically
"declarative descriptor bound via a `Pattern` to an adapter family" —
`FromEnv`/`FromEnvVar` never fit that relationship, so moving them to
`ports` would gain none of the reuse/consistency benefit that justifies
`Cache[T]`'s (and potentially `File[T]`'s) placement there.

A small, dedicated `config` package is the architecturally honest home: a
package whose sole job is "load typed values from the process environment,"
independent from both wire-format serialization (`format`) and
pipeline/port wiring (`ports`).

## Scope decisions

| In scope | Out of scope |
|---|---|
| New package `config` (name TBD — see Open design decisions) hosting `FromEnv[T]`, `FromEnvVar[T]`, `EnvVarError` | `ports.File`/`FilePathParam`/etc. — already migrated (see `docs/features/ports.md`) |
| Update the ~19 files referencing `format.FromEnv`/`FromEnvVar`/`EnvVarError` | Changing `FromEnv`/`FromEnvVar`'s behavior, schema coercion rules, or error shape — pure relocation |
| Doc pass: `docs/guides/config.md`, `docs/features/config.md` (already config-focused — likely just an import-path update), `docs/features/formats.md` (remove the section, cross-reference the new page) | Adding new config-loading capabilities (e.g. `.env` file support, flag-parsing integration) — no user request for this |

## Impact inventory

*(current as of this writing — re-verify with a fresh grep before
implementing)*

**Go files** (~6): `format/{env.go,env_test.go}` (move),
`validate/{format.go,doc.go}` (doc references), `codex/primitives.go` (doc
reference), `examples/{env-config,file-io,sensor-service}/**`

**Doc files** (~8): `.github/instructions/go-codex.instructions.md`,
`.github/skills/review-go-codex/references/checklist.md`,
`docs/guides/{config,observer,validation,ports}.md`,
`docs/features/{formats,config,schema-metadata}.md`,
`docs/reference/project-structure.md`,
`examples/sensor-service/README.md`

Much smaller blast radius than the companion `File`-to-`ports` migration.

## API surface (symbol rename map)

| Old (`format`) | New (`config`) |
|---|---|
| `format.FromEnv[T](codec, prefix) (T, error)` | `config.FromEnv[T](codec, prefix) (T, error)` |
| `format.FromEnvVar[T](key, codec) (T, error)` | `config.FromEnvVar[T](key, codec) (T, error)` |
| `format.EnvVarError{Key,Err}` | `config.EnvVarError{Key,Err}` |

Internals (`buildEnvIntermediate`, `coercePrimitive`, `parseSliceEnv`,
naming convention, JSON-detection rules) are **unchanged** — pure
relocation.

## Structured errors

`config.EnvVarError` keeps its existing `Error()`/`Unwrap()` (wraps
`codex.ValidationErrors`). Checked: `EnvVarError` currently has **no**
`LogValue()` — a small, independently-justifiable gap against the
mandatory-5-requirements rule. Worth fixing as a drive-by improvement
during the move (see Open design decisions).

## Observer integration

None today — `FromEnv`/`FromEnvVar` have no `ctx` param, no `Observer`
field. Unchanged by the move.

## Unit test plan

| ID | Name | Verifies |
|---|---|---|
| M1 | Full `config` package test suite (moved from `format/env_test.go`) passes unchanged | Behavior identical post-move |
| M2 | `format` package test suite passes with env tests removed | No regressions from the split |
| M3 | `examples/env-config` and `examples/sensor-service` build/run after the import-path update | No stale references |

## Files to create/change

See Impact inventory above — expect roughly 6 `.go` files and 8 `.md` files
(much smaller than the companion `File` migration).

## Open design decisions

1. **Package name: `config` vs `env`?** `config` reads more naturally at
   call sites (`config.FromEnv`) and leaves room for future non-env config
   sources (flags, files) without a rename; `env` is more precise about
   current scope but narrower. Leaning `config` — confirm before
   implementing.
2. **Should `EnvVarError` gain `LogValue()` during the move?** Currently
   missing (checked against the mandatory-5-requirements rule) — small,
   independently-justifiable fix; bundle with the move since the file is
   being touched anyway (leaning yes).
3. **Relationship to the existing `docs/features/config.md`/
   `docs/guides/config.md`?** Both pages already exist and are already
   config-focused — likely just need an import-path update in place
   (`format.FromEnv` → `config.FromEnv` throughout), not a rewrite.
