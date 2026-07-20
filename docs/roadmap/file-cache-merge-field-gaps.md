# File & Cache Merge-Field Gaps — extending "one struct, one call" to `ports.File`/`ports.Cache`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> Produced by a `review-go-codex` skill audit of every adapter and every
> port against the merge-field / "one struct, one call" convenience pattern
> established by `api/rest`'s `NewPathParam`/`CallHandle` and already
> extended to `api/events`, `api/reqreply`, and the `ports.Pattern`
> binding layer (see `.github/instructions/go-codex.instructions.md`'s
> "Declarative Var Extraction & Merge" section and
> `docs/roadmap/merge-field-remaining-gaps.md`). This doc captures TWO
> previously-unassessed gaps found in that audit — `ports.File`/
> `adapters/file` and `ports.Cache`/`adapters/redis` — plus two smaller,
> related observations.

## Motivation

Every adapter (`nethttp`, `chi`, `mqtt`, `mqtt5`, `zeromq`, `sql`, `file`,
`redis`, `websocket`, `mcpgo`) and every port (`SourcePort`, `SinkPort`,
`IOPort`, `ToolPort`, `LatestPort`, `DuplexPort`, `File`, `Cache`,
`SQLPattern`) was reviewed against the five-point "one struct, one call"
checklist (`review-go-codex`'s Boundary Symmetry Guardrail):

1. Declare-once constructors for every var-boundary
2. Escape hatch preserved (plain validate-only params still work)
3. Encode/decode symmetry (role-aware accessors both directions)
4. Role symmetry (both sides of the boundary get the convenience)
5. Single-call convenience wrapper on encode + automatic wiring on decode

**`api/rest`, `api/events`, `api/reqreply`, and the `ports.Pattern`
binding layer (`nethttp`/`mqtt5`/`zeromq`/`mqtt`) all satisfy all five —
confirmed, not re-reported.** `api/mcp` Resources/Prompts were already
assessed in a prior round (`docs/roadmap/merge-field-remaining-gaps.md`'s
G4) — G4a (Resources' automatic URI-var extraction) shipped; G4b (full
merge-field parity for Resources/Prompts) was explicitly deferred pending
a concrete use case. **Not re-reported here.**

Two ports were found to have the DECLARE-ONCE CONSTRUCTOR (point 1) and
the MERGE-FIELDS ACCESSOR, but ZERO of points 3-5 — the "one struct, one
call" promise was never extended to them at all, despite having the same
building blocks REST/events/reqreply started from:

1. **`ports.File`** has `NewFilePathParam[T]` (declare-once, merge-capable)
   and `File[T].MergeFields()`, but `File.Read`/`File.Write` never call
   `codex.EncodeVars`/`codex.DecodeVars` with them — no `ReadMerged`
   (decode-merge) and no `WriteHandle`-equivalent (encode-side
   single-call convenience). `adapters/file`'s `ReadAdapter`/
   `ReadEachAdapter`/`DrainWriteFileAdapter` all require a **hand-written**
   `varsFor func(T) map[string]string` closure — even when `T` already
   declares its path vars via `NewFilePathParam`.
2. **`ports.Cache`** has `NewCacheKeyParam[T]` (declare-once, merge-capable)
   and `Cache[T].MergeFields()`, with a doc comment EXPLICITLY
   acknowledging "no bundling convenience method exists for Cache" — but
   the reasoning given ("`Get`/`Set` already take vars directly — no
   'body vs. vars' split to coordinate") does not hold up: `Set`'s value
   argument `v` IS the same type declaring the key vars, exactly like
   `mqtt5.PublishHandle`'s `msg` argument. `adapters/redis`'s
   `SetAdapter`/`DrainSetAdapter` require a **mandatory** hand-written
   `keyFn func(T) map[string]string` — there is no "leave nil, derive
   automatically" escape at all, unlike every `Vars`-carrying port-binding
   adapter fixed in the prior round.

Both gaps were found by reading `ports/file.go`, `ports/cache.go`,
`adapters/file/binding.go`, and `adapters/redis/binding.go` directly — not
speculation. Neither port/adapter pair appears in
`review-go-codex/references/checklist.md`'s section-12 boundary-symmetry
table at all, which is itself a process gap (the table only lists
`api/rest`/`api/events`/`api/reqreply`/the port-binding layer) — worth
fixing alongside the code (see G4).

## Scope decisions

| Gap | Boundary | Severity | In scope this round |
|---|---|---|---|
| G1 | `ports.File`/`adapters/file` never got single-call convenience (`ReadMerged`/`WriteHandle`-equivalent) despite having `NewFilePathParam`+`MergeFields()` | `ports/file.go`, `adapters/file/binding.go` | **bug** — a shipped, declare-once constructor with no convenience wrapper is an incomplete implementation of the documented pattern | Design only this round |
| G2 | `ports.Cache`/`adapters/redis` never got single-call convenience (`GetMerged`/`SetHandle`-equivalent) despite having `NewCacheKeyParam`+`MergeFields()`; existing doc comment's rationale for skipping it doesn't hold | `ports/cache.go`, `adapters/redis/binding.go` | **bug** — same category as G1 | Design only this round |
| G3 | `adapters/websocket`'s upgrade path uses `rest.PathParam`/`ValidatePathParams` (validate-only) for the CONNECTION's path vars — same "per-connection vs. per-message merge" open question already deferred for SSE (`merge-field-remaining-gaps.md`'s G1) | `adapters/websocket` | Low — same open question as SSE, no concrete use case | Not actioned — track alongside SSE's G1, revisit together if a use case appears for either |
| G4 | `review-go-codex/references/checklist.md`'s section-12 boundary-symmetry table has no `ports.File`/`ports.Cache` rows — the audit process itself doesn't check these two ports | `.github/skills/review-go-codex/references/checklist.md` | Low — process/doc gap, not a code bug | Yes — trivial, add two rows once G1/G2 land (or immediately with "❌ not yet" status, updated when shipped) |

## API surface

**G1 — `ports.File` single-call convenience** (mirrors `mqtt5.PublishHandle`/
`nethttp.CallHandle` exactly):

```go
// ports/file.go

// ReadMerged is the decode-merge convenience: reads and decodes like Read,
// then merges the vars used to build the path into the SAME returned
// value via codex.DecodeVars — mirrors events.ChannelHandle.DecodeMerged.
// Additive: Read is unchanged; ReadMerged behaves identically to Read when
// MergeFields() is empty.
func (fh File[T]) ReadMerged(vars map[string]string, opts FileOptions) (T, error)

// WriteHandle is the single-call convenience wrapper around Write: it
// derives vars from v automatically via codex.EncodeVars(v,
// fh.MergeFields()...) — one struct in, no manual vars map — mirroring
// mqtt5.PublishHandle/nethttp.CallHandle's convenience. Write remains the
// lower-level escape hatch for callers that build vars themselves (e.g.
// no merge fields declared, or vars come from a non-struct source).
func WriteHandle[T any](fh File[T], v T, opts FileOptions) error

// UpdateHandle mirrors Update, deriving vars from the CURRENT value
// (read first via ReadMerged, then vars re-derived from the updated value
// for the write) — needs a design decision (see Open design decisions).
func UpdateHandle[T any](fh File[T], fn func(T) T, opts FileOptions) error
```

```go
// adapters/file/binding.go — ReadAdapter/ReadEachAdapter/DrainWriteFileAdapter
// gain a "derive automatically" path: when varsFor is nil AND the file's
// MergeFields() is non-empty (only sensible for the SAME-type case —
// DrainWriteFileAdapter[T] and ReadAdapter[T,T]-shaped uses; ReadEachAdapter's
// independent In/Resp enrichment case keeps varsFor mandatory, unchanged),
// derive vars via codex.EncodeVars automatically instead of requiring a
// hand-written closure.
```

**G2 — `ports.Cache` single-call convenience** (mirrors `mqtt5.PublishHandle`/
`zeromq.PublishHandle` exactly):

```go
// ports/cache.go

// GetMerged mirrors ReadMerged: looks up like Get, then merges the key
// vars into the returned value via codex.DecodeVars.
func (c Cache[T]) GetMerged(vars map[string]string) (T, bool, error)
// (Get is currently a free function taking client+cache — GetMerged's
// exact signature/placement needs to match Get's existing shape; see
// Open design decisions.)

// SetHandle is the single-call convenience wrapper around Set: derives
// vars from v automatically via codex.EncodeVars(v, cache.MergeFields()...).
func SetHandle[T any](ctx context.Context, client Commands, cache Cache[T], v T, opts SetOptions) error
```

```go
// adapters/redis/binding.go — SetAdapter/DrainSetAdapter gain a
// "keyFn nil -> derive automatically" path, mirroring G1's DrainWriteFileAdapter
// fix — GetAdapter's independent Req/Resp enrichment shape keeps keyFn
// mandatory (same rationale as ReadEachAdapter).
```

## Structured errors / Observer integration

No new error types for G1/G2 — `ReadMerged`/`GetMerged` reuse
`codex.ValidationErrors`/existing `FilePathParamError`/`CacheKeyParamError`
exactly as `Read`/`Get` already do (the merge step uses the SAME
`codex.DecodeVars` error shape `nethttp.Handler`'s decode-merge already
produces). `WriteHandle`/`SetHandle` reuse `codex.VarEncodeTypeError`
exactly as `PublishHandle`/`CallHandle` already do. No new observer
methods — existing `recordFileRead`/`recordFileWrite`/`RecordCacheHit`/
`RecordCacheWrite` calls are unchanged; only the VALUE SOURCE for vars
changes (derived vs. caller-supplied).

## Unit test plan

| ID | Test |
|---|---|
| G1-1 | `File.ReadMerged` merges path vars into the decoded value when the file declares merge fields (regression guard: identical to `Read` when none declared) |
| G1-2 | `WriteHandle` derives vars from v's own merge-field-declared struct fields — two values with different path-var fields write to two different concrete paths |
| G1-3 | `file.DrainWriteFileAdapter`/`ReadAdapter` per-item derivation when `varsFor` is nil (mirrors the prior round's G1-1/G1-2 test shape exactly) |
| G2-1 | `Cache.GetMerged` merges key vars into the returned value (regression guard: identical to `Get` when no merge fields declared) |
| G2-2 | `SetHandle` derives key vars from v's own merge-field-declared struct fields |
| G2-3 | `redis.SetAdapter`/`DrainSetAdapter` per-item derivation when `keyFn` is nil |
| G4 | (no test — doc-only checklist correction) |

## Files to create/change

| File | Responsibility |
|---|---|
| `ports/file.go` + `_test.go` | G1: `ReadMerged`, `WriteHandle` (+ `UpdateHandle` if resolved) |
| `adapters/file/binding.go` + `_test.go` | G1: `varsFor`-nil derivation path for same-type adapters |
| `ports/cache.go` + `_test.go` | G2: `GetMerged`, `SetHandle` |
| `adapters/redis/binding.go` + `_test.go` | G2: `keyFn`-nil derivation path for `SetAdapter`/`DrainSetAdapter` |
| `.github/skills/review-go-codex/references/checklist.md` | G4: add `ports.File`/`ports.Cache` rows to the section-12 table |
| `docs/features/file.md`, `docs/features/redis.md` (if they exist) | Document the new convenience once shipped |
| this doc | mark SHIPPED once complete |

## Open design decisions (to resolve before implementation)

1. **G1 — `ReadMerged` naming and placement**: should it be a method on
   `File[T]` (mirrors `Read`/`Write`) or a free function (mirrors
   `WriteHandle`/`PublishHandle`'s free-function shape, needed because Go
   methods can't introduce new type params)? `ReadMerged` needs no new
   type param, so a method is natural; `WriteHandle` doesn't need one
   either here (unlike `forge.NewFunction`), so BOTH could be methods —
   confirm no naming collision with existing `File[T]` methods first.
2. **G1 — is `UpdateHandle` in scope, or just `ReadMerged`+`WriteHandle`?**
   `Update` already composes `Read`+`Write`; an `UpdateHandle` would need
   to decide whether vars are derived ONCE (from the pre-update value) or
   RE-derived after `fn` runs (from the updated value, in case `fn`
   changes a merge-field-declared field, e.g. renaming the ID) — this
   needs its own resolution, likely "re-derive after fn" to match
   `PublishHandle`'s "vars always reflect the value being written" model.
3. **G2 — where does `GetMerged` live given `Get` is a free function
   (`redis.Get(ctx, client, cache, vars, opts)`), not a `Cache[T]`
   method?** `Cache[T].MergeFields()` lives on the port type, but the
   actual lookup is a free function taking a `Commands` client — mirrors
   `zeromq.Call`/`CallHandle`'s split (`Call` needs a socket, so lives in
   the adapter package, not on `RouteHandle`). `GetMerged`/`SetHandle`
   likely belong in `adapters/redis` (alongside `Get`/`Set`), not
   `ports/cache.go` — needs confirming against the exact precedent
   (`mqtt5.PublishHandle` lives in `adapters/mqtt5`, not `api/events`).
4. **G1/G2 — should the port-binding adapters' `varsFor`/`keyFn`-nil
   derivation apply ONLY to same-type (`T`→`T`) adapters
   (`DrainWriteFileAdapter[T]`, `SetAdapter[T]`) or attempt it for the
   enrichment adapters too (`ReadEachAdapter[In,T,Resp]`,
   `GetAdapter[Req,Resp]`)?** Leaning: same-type only — the enrichment
   adapters' `In`/`Req` type is independent of the cached/file `T`/`Resp`
   type, so there is no single struct to derive vars from (same rationale
   already established for `ReadEachAdapter` in the existing gotchas).
5. **G3 — should websocket's per-connection path-var question be resolved
   together with SSE's, or kept as two separate open items?** Leaning:
   together — both are "one Req at connection/subscribe time, many
   messages after" shapes with the identical "is repeating vars into every
   message useful" question. Revisit both if EITHER gets a concrete use
   case.

## Verification

Same ritual as every prior round: gofmt, `go build ./...`, `go test
./...`, `-race` on touched packages, `just check`, all examples exit 0.
